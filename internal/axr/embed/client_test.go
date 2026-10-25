// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndpointsAndCache(t *testing.T) {
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
	if _, _, err := c.Embed(context.Background(), [][]string{{strings.Repeat("x", MaxBytes+1)}}, "document"); err == nil {
		t.Fatal("accepted oversized document")
	}
}

func TestFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		calls  int
	}{
		{401, "secret denied", 1}, {503, "unavailable", 3}, {200, `{"data":[]}`, 1},
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
	transport := &timeoutTransport{}
	c := Client{Key: "secret", Model: "voyage-code-4", HTTP: &http.Client{Transport: transport}}
	_, _, err := c.Embed(context.Background(), [][]string{{"text"}}, "query")
	if err == nil || !strings.Contains(err.Error(), "timeout") || transport.calls != 3 {
		t.Fatalf("timeout = %v, calls %d", err, transport.calls)
	}
}
