// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package embed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (c Client) Rules(ctx context.Context, texts []string, dir string) ([][]float64, int, int, error) {
	inputType, _ := c.InputTypes()
	paths := make([]string, len(texts))
	output := make([][]float64, len(texts))
	var missing [][]string
	var indices []int
	cached := 0
	for i, text := range texts {
		identity, _ := json.Marshal([]any{"voyage", c.Model, c.Dims, inputType, text})
		paths[i] = filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256(identity)))
		data, err := os.ReadFile(paths[i])
		if err == nil && json.Unmarshal(data, &output[i]) == nil && Valid(output[i]) && (c.Dims == 0 || len(output[i]) == c.Dims) {
			cached++
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, 0, 0, err
		}
		indices = append(indices, i)
		missing = append(missing, []string{text})
	}
	ends, err := batchEnds(missing, c.Contextual())
	if err != nil {
		return nil, 0, cached, err
	}
	if c.Key == "" {
		return nil, 0, cached, fmt.Errorf("VOYAGE_API_KEY is required")
	}
	if len(indices) > 0 {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, 0, cached, err
		}
	}
	tokens, start := 0, 0
	for _, end := range ends {
		vectors, used, err := c.request(ctx, missing[start:end], inputType)
		if err != nil {
			return nil, tokens, cached, err
		}
		tokens += used
		for j, i := range indices[start:end] {
			output[i] = vectors[j][0]
			data, err := json.Marshal(output[i])
			if err != nil {
				return nil, tokens, cached, err
			}
			if err := writeCache(paths[i], data); err != nil {
				return nil, tokens, cached, err
			}
		}
		start = end
	}
	return output, tokens, cached, nil
}

func writeCache(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".embedding-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
