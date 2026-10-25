// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

const MaxBytes = 60000 // Leaves room below the 32K-token limit with a bytes/3 estimate.

type Client struct {
	BaseURL string
	Key     string
	Model   string
	Dims    int
	HTTP    *http.Client
}

type item struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
	Data      []item    `json:"data"`
}

type response struct {
	Data  []item `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

func (c Client) Contextual() bool { return strings.HasPrefix(c.Model, "voyage-context-") }

func (c Client) Embed(ctx context.Context, docs [][]string, inputType string) ([][][]float64, int, error) {
	if c.Key == "" {
		return nil, 0, fmt.Errorf("VOYAGE_API_KEY is required")
	}
	vectors := make([][][]float64, 0, len(docs))
	tokens := 0
	for start := 0; start < len(docs); {
		end, size, chunks := start, 0, 0
		for end < len(docs) && end-start < 1000 {
			n := 0
			for _, text := range docs[end] {
				n += len(text)
			}
			if len(docs[end]) == 0 || n == 0 || n > MaxBytes || len(docs[end]) > 1000 {
				return nil, tokens, fmt.Errorf("embedding document must contain 1..1000 chunks and 1..%d bytes", MaxBytes)
			}
			if !c.Contextual() && len(docs[end]) != 1 {
				return nil, tokens, fmt.Errorf("plain embedding requires one text per document")
			}
			if size+n > MaxBytes || chunks+len(docs[end]) > 1000 {
				break
			}
			size += n
			chunks += len(docs[end])
			end++
		}
		batch, used, err := c.request(ctx, docs[start:end], inputType)
		if err != nil {
			return nil, tokens, err
		}
		vectors = append(vectors, batch...)
		tokens += used
		start = end
	}
	return vectors, tokens, nil
}

func (c Client) request(ctx context.Context, docs [][]string, inputType string) ([][][]float64, int, error) {
	body := map[string]any{"model": c.Model, "input_type": inputType}
	endpoint := "/embeddings"
	if c.Contextual() {
		endpoint = "/contextualizedembeddings"
		body["inputs"] = docs
	} else {
		texts := make([]string, len(docs))
		for i := range docs {
			texts[i] = docs[i][0]
		}
		body["input"] = texts
	}
	if c.Dims != 0 {
		body["output_dimension"] = c.Dims
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.voyageai.com/v1"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	var data []byte
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			data, requestErr = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			resp.Body.Close()
		}
		retry := false
		if requestErr != nil {
			var timeout net.Error
			retry = ctx.Err() == nil && errors.As(requestErr, &timeout) && timeout.Timeout()
			err = fmt.Errorf("Voyage request failed: response unavailable")
			if retry {
				err = fmt.Errorf("Voyage request timeout")
			}
		} else {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			}
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
			excerpt := strings.ReplaceAll(string(data), c.Key, "[redacted]")
			excerpt = excerpt[:min(len(excerpt), 300)]
			err = fmt.Errorf("Voyage HTTP %d: %s", resp.StatusCode, excerpt)
		}
		if !retry || attempt == 2 {
			return nil, 0, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
	var result response
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, 0, fmt.Errorf("invalid Voyage JSON: %w", err)
	}
	if len(result.Data) != len(docs) {
		return nil, 0, fmt.Errorf("Voyage returned wrong document count")
	}
	output := make([][][]float64, len(docs))
	dimension := c.Dims
	for _, doc := range result.Data {
		if doc.Index < 0 || doc.Index >= len(docs) || output[doc.Index] != nil {
			return nil, 0, fmt.Errorf("invalid Voyage document index")
		}
		chunks := doc.Data
		if !c.Contextual() {
			chunks = []item{{Index: 0, Embedding: doc.Embedding}}
		}
		if len(chunks) != len(docs[doc.Index]) {
			return nil, 0, fmt.Errorf("Voyage returned wrong chunk count")
		}
		output[doc.Index] = make([][]float64, len(chunks))
		for _, chunk := range chunks {
			if chunk.Index < 0 || chunk.Index >= len(chunks) || output[doc.Index][chunk.Index] != nil {
				return nil, 0, fmt.Errorf("invalid Voyage chunk index")
			}
			if dimension == 0 {
				dimension = len(chunk.Embedding)
			}
			if !Valid(chunk.Embedding) || len(chunk.Embedding) != dimension {
				return nil, 0, fmt.Errorf("invalid Voyage vector dimensions or values")
			}
			output[doc.Index][chunk.Index] = chunk.Embedding
		}
	}
	return output, result.Usage.TotalTokens, nil
}

func Valid(v []float64) bool {
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
		norm += x * x
	}
	return len(v) > 0 && norm > 0 && !math.IsInf(norm, 0)
}
