// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEndpointsAndCache(t *testing.T) {
	fastRetries(t)
	for _, model := range []string{"voyage-context-4", "voyage-code-4"} {
		t.Run(model, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer secret" || r.Method != "POST" {
					t.Error("invalid HTTP request")
				}
				var body struct {
					Model     string     `json:"model"`
					InputType string     `json:"input_type"`
					Dims      int        `json:"output_dimension"`
					Input     []string   `json:"input"`
					Inputs    [][]string `json:"inputs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				contextual := strings.HasPrefix(model, "voyage-context-")
				wantType, wantPath := "document", "/embeddings"
				if contextual {
					wantType, wantPath = "query", "/contextualizedembeddings"
				}
				if body.Model != model || body.InputType != wantType || body.Dims != 2 || r.URL.Path != wantPath {
					t.Errorf("request = %+v %s", body, r.URL.Path)
				}
				if contextual {
					if len(body.Inputs) != 2 || body.Inputs[0][0] != "first" || body.Input != nil {
						t.Error("wrong contextual inputs")
					}
				} else {
					if len(body.Input) != 2 || body.Input[0] != "first" || body.Inputs != nil {
						t.Error("wrong plain input")
					}
				}
				if calls == 1 {
					w.WriteHeader(429)
					fmt.Fprint(w, "try later")
					return
				}
				if contextual {
					fmt.Fprint(w, `{"data":[{"index":1,"data":[{"index":0,"embedding":[0,1]}]},{"index":0,"data":[{"index":0,"embedding":[1,0]}]}],"usage":{"total_tokens":7}}`)
				} else {
					fmt.Fprint(w, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"total_tokens":7}}`)
				}
			}))
			defer server.Close()
			c := Client{BaseURL: server.URL, Key: "secret", Model: model, Dims: 2, HTTP: server.Client()}
			dir := t.TempDir()
			texts := []string{"first", "second"}
			v, tokens, cached, err := c.Rules(context.Background(), texts, dir)
			if err != nil || calls != 2 || tokens != 7 || cached != 0 || len(v) != 2 || v[0][0] != 1 {
				t.Fatalf("first = %v %d %d %v; calls %d", v, tokens, cached, err, calls)
			}
			_, tokens, cached, err = c.Rules(context.Background(), texts, dir)
			if err != nil || calls != 2 || tokens != 0 || cached != 2 {
				t.Fatalf("cached = %d %d %v; calls %d", tokens, cached, err, calls)
			}
		})
	}
}

func TestContextualChunksAndBatching(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["output_dimension"]; ok {
			t.Error("zero dimensions not omitted")
		}
		var kind string
		json.Unmarshal(body["input_type"], &kind)
		if kind != "document" {
			t.Error("wrong input type")
		}
		var docs [][]string
		json.Unmarshal(body["inputs"], &docs)
		data := make([]item, len(docs))
		for i, doc := range docs {
			data[i].Index = i
			for j := range doc {
				data[i].Data = append(data[i].Data, item{Index: j, Embedding: []float64{1, 0}})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	c := Client{BaseURL: server.URL, Key: "secret", Model: "voyage-context-4", HTTP: server.Client()}
	docs := [][]string{{strings.Repeat("x", MaxBytes/2), "two"}, {strings.Repeat("y", MaxBytes/2)}}
	v, _, err := c.Embed(context.Background(), docs, "document")
	if err != nil || calls != 2 || len(v[0]) != 2 {
		t.Fatalf("chunks: %v, calls %d", err, calls)
	}
	for _, invalid := range [][][]string{{{strings.Repeat("x", MaxBytes)}, {"second"}, {strings.Repeat("x", MaxBytes+1)}}, {{"first"}, {}}, {{"first"}, {"", "second"}}, {{"first"}, make([]string, 1001)}} {
		if _, _, err := c.Embed(context.Background(), invalid, "document"); err == nil || calls != 2 {
			t.Fatalf("invalid documents: %v, calls %d", err, calls)
		}
	}
}

func TestFailureBoundaries(t *testing.T) {
	fastRetries(t)
	for _, tc := range []struct {
		status int
		body   string
		calls  int
	}{
		{400, "bad request", 1}, {401, "secret denied", 1}, {503, "unavailable", 3}, {200, `{"data":[]}`, 1},
		{200, `{"data":[{"index":0,"embedding":[0,0]}]}`, 1},
		{200, `{"data":[{"index":1,"embedding":[1,0]}]}`, 1},
	} {
		t.Run(fmt.Sprint(tc.status, tc.body), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c := Client{BaseURL: server.URL, Key: "secret", Model: "voyage-code-4", HTTP: server.Client()}
			_, _, err := c.Embed(context.Background(), [][]string{{"text"}}, "query")
			if err == nil || strings.Contains(err.Error(), "secret") || calls != tc.calls {
				t.Fatalf("failure = %v, calls %d", err, calls)
			}
		})
	}
	if _, err := batchEnds([][]string{{"a", "b"}}, false); err == nil {
		t.Fatal("accepted multiple plain texts")
	}
	if _, _, err := (Client{}).Embed(context.Background(), nil, "query"); err == nil || !strings.Contains(err.Error(), "VOYAGE_API_KEY") {
		t.Fatal("missing key not named")
	}
}

type timeoutTransport struct{ calls int }

func (r *timeoutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, &net.DNSError{IsTimeout: true}
}

func TestTimeoutRetry(t *testing.T) {
	fastRetries(t)
	transport := &timeoutTransport{}
	c := Client{Key: "secret", Model: "voyage-code-4", HTTP: &http.Client{Transport: transport}}
	_, _, err := c.Embed(context.Background(), [][]string{{"text"}}, "query")
	var cause net.Error
	if err == nil || !errors.As(err, &cause) || !cause.Timeout() || !strings.Contains(err.Error(), "timeout") || transport.calls != 3 {
		t.Fatalf("timeout = %v, calls %d", err, transport.calls)
	}
}

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryWait
	t.Cleanup(func() { retryWait = old })
	retryWait = func(context.Context, time.Duration) error { return nil }
}

func TestRuleBatchResumeOrderAndCorruption(t *testing.T) {
	fastRetries(t)
	texts := []string{strings.Repeat("a", MaxBytes/2), strings.Repeat("b", MaxBytes/2), "c"}
	fail, calls := true, 0
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		requested = append(requested, body.Input...)
		if fail && len(body.Input) == 1 {
			w.WriteHeader(400)
			return
		}
		data := make([]item, len(body.Input))
		for i, text := range body.Input {
			data[i] = item{Index: i, Embedding: []float64{float64(text[0]), 1}}
		}
		json.NewEncoder(w).Encode(response{Data: data})
	}))
	defer server.Close()
	c := Client{BaseURL: server.URL, Key: "secret", Model: "voyage-code-4", Dims: 2, HTTP: server.Client()}
	dir := t.TempDir()
	if _, _, _, err := c.Rules(context.Background(), texts, dir); err == nil {
		t.Fatal("expected second batch failure")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(paths) != 2 || calls != 2 {
		t.Fatalf("saved %d vectors, calls %d", len(paths), calls)
	}
	fail, requested = false, nil
	v, _, cached, err := c.Rules(context.Background(), texts, dir)
	if err != nil || cached != 2 || !reflect.DeepEqual(requested, []string{"c"}) || v[2][0] != 99 {
		t.Fatalf("resume: %v %d %v %v", v, cached, requested, err)
	}
	// Removing the middle entry catches confusion between cache and request indices.
	middle, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, path := range middle {
		data, _ := os.ReadFile(path)
		if string(data) == "[98,1]" {
			for _, corrupt := range []string{"", "bad json", "[0,0]", "[1]"} {
				if corrupt == "" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
						t.Fatal(err)
					}
				}
				requested = nil
				v, _, cached, err = c.Rules(context.Background(), texts, dir)
				data, _ = os.ReadFile(path)
				if err != nil || cached != 2 || !reflect.DeepEqual(requested, texts[1:2]) || !reflect.DeepEqual(v, [][]float64{{97, 1}, {98, 1}, {99, 1}}) || string(data) != "[98,1]" {
					t.Fatalf("cache recovery %q: %v %d %v %v", corrupt, v, cached, requested, err)
				}
			}
			return
		}
	}
	t.Fatal("middle cache entry missing")
}

func TestRetryAfterAndCancellation(t *testing.T) {
	wait := retryWait
	fastRetries(t)
	for _, header := range []string{"2", "", "120"} {
		var waits []time.Duration
		retryWait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", header)
			w.WriteHeader(429)
		}))
		c := Client{BaseURL: server.URL, Key: "secret", HTTP: server.Client()}
		_, _, err := c.Embed(context.Background(), [][]string{{"x"}}, "query")
		server.Close()
		want := []time.Duration{time.Second, 2 * time.Second}
		if header != "" {
			n := 2 * time.Second
			if header == "120" {
				n = 60 * time.Second
			}
			want = []time.Duration{n, n}
		}
		if err == nil || !reflect.DeepEqual(waits, want) {
			t.Fatalf("header %q: waits %v, err %v", header, waits, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retryWait = func(ctx context.Context, d time.Duration) error { cancel(); return wait(ctx, d) }
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer server.Close()
	c := Client{BaseURL: server.URL, Key: "secret", HTTP: server.Client()}
	if _, _, err := c.Embed(ctx, [][]string{{"x"}}, "query"); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel during wait: %v, calls %d", err, calls)
	}
}

func TestTransportCauseAndRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "secret")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := Client{BaseURL: server.URL + "/secret", Key: "secret", HTTP: server.Client()}
	_, _, err := c.Embed(ctx, [][]string{{"x"}}, "query")
	var cause *url.Error
	if !errors.Is(err, context.Canceled) || !errors.As(err, &cause) || strings.Contains(err.Error(), c.Key) {
		t.Fatalf("transport error: %v", err)
	}
	_, _, err = c.Embed(context.Background(), [][]string{{"x"}}, "query")
	if !errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), c.Key) {
		t.Fatalf("body read error: %v", err)
	}
}

func TestBatchBoundaries(t *testing.T) {
	docs := slices.Repeat([][]string{{"x"}}, 1001)
	ends, err := batchEnds(docs, true)
	if err != nil || !reflect.DeepEqual(ends, []int{1000, 1001}) {
		t.Fatalf("document limit: %v %v", ends, err)
	}
	docs = [][]string{slices.Repeat([]string{"x"}, 1000), {"x"}}
	ends, err = batchEnds(docs, true)
	if err != nil || !reflect.DeepEqual(ends, []int{1, 2}) {
		t.Fatalf("chunk limit: %v %v", ends, err)
	}
	if _, err := batchEnds([][]string{slices.Repeat([]string{"x"}, 1001)}, true); err == nil {
		t.Fatal("accepted too many chunks")
	}
}
