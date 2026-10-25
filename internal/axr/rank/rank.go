// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rank

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/alibaba/open-code-review/internal/axr/catalog"
	"github.com/alibaba/open-code-review/internal/axr/embed"
)

type Scored struct {
	RuleID string  `json:"rule_id"`
	Score  float64 `json:"score"`
}
type FileResult struct {
	Path         string   `json:"path"`
	Language     string   `json:"language"`
	RulesInScope int      `json:"rules_in_scope"`
	Hunks        int      `json:"hunks"`
	Ranked       []Scored `json:"ranked"`
}
type Result struct {
	Schema  string `json:"schema"`
	Model   string `json:"model"`
	Mode    string `json:"mode"`
	Dims    int    `json:"dims"`
	Catalog struct {
		Root           string `json:"root"`
		RulesLoaded    int    `json:"rules_loaded"`
		DraftsIncluded bool   `json:"drafts_included"`
	} `json:"catalog"`
	Files []FileResult `json:"files"`
	Usage struct {
		RuleTokens  int `json:"rule_tokens"`
		DiffTokens  int `json:"diff_tokens"`
		CachedRules int `json:"cached_rules"`
	} `json:"usage"`
}

func Score(rules []catalog.Rule, vectors [][]float64, chunks [][]float64, path string, top int) ([]Scored, int, error) {
	ranked := []Scored{}
	for i, rule := range rules {
		if !rule.InScope(path) {
			continue
		}
		best := -1.0
		for _, chunk := range chunks {
			if len(chunk) != len(vectors[i]) {
				return nil, 0, fmt.Errorf("rule and diff vector dimensions differ")
			}
			dot, a, b := 0.0, 0.0, 0.0
			for j, x := range chunk {
				y := vectors[i][j]
				dot += x * y
				a += x * x
				b += y * y
			}
			best = math.Max(best, dot/math.Sqrt(a*b))
		}
		ranked = append(ranked, Scored{rule.ID, math.Round(best*10000) / 10000})
	}
	slices.SortFunc(ranked, func(a, b Scored) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return strings.Compare(a.RuleID, b.RuleID)
	})
	total := len(ranked)
	if top > 0 && top < len(ranked) {
		ranked = ranked[:top]
	}
	return ranked, total, nil
}

func Run(ctx context.Context, rules []catalog.Rule, files []File, c embed.Client, cache string, top int) (Result, error) {
	out := Result{Schema: "axr-rank/1", Model: c.Model, Mode: "plain", Dims: c.Dims, Files: []FileResult{}}
	if c.Contextual() {
		out.Mode = "contextualized"
	}
	texts := make([]string, len(rules))
	for i, r := range rules {
		texts[i] = r.Text
	}
	vectors, tokens, cached, err := c.Rules(ctx, texts, cache)
	if err != nil {
		return out, err
	}
	out.Usage.RuleTokens = tokens
	out.Usage.CachedRules = cached
	var docs [][]string
	ends := make([]int, len(files))
	_, inputType := c.InputTypes()
	for i, file := range files {
		fr := FileResult{Path: file.Path, Language: catalog.Language(file.Path), Hunks: len(file.Hunks), Ranked: []Scored{}}
		for _, r := range rules {
			if r.InScope(file.Path) {
				fr.RulesInScope++
			}
		}
		if fr.RulesInScope > 0 && len(file.Hunks) > 0 {
			if !c.Contextual() {
				docs = append(docs, []string{file.Patch})
			} else {
				var group []string
				size := 0
				for _, hunk := range file.Hunks {
					if size+len(hunk) > embed.MaxBytes || len(group) == 1000 {
						if len(group) > 0 {
							docs = append(docs, group)
						}
						group = nil
						size = 0
					}
					group = append(group, hunk)
					size += len(hunk)
				}
				if len(group) > 0 {
					docs = append(docs, group)
				}
			}
		}
		ends[i] = len(docs)
		out.Files = append(out.Files, fr)
	}
	if len(docs) == 0 {
		return out, nil
	}
	embedded, used, err := c.Embed(ctx, docs, inputType)
	if err != nil {
		return out, err
	}
	out.Usage.DiffTokens = used
	start := 0
	for i, end := range ends {
		if end > start {
			var chunks [][]float64
			for _, doc := range embedded[start:end] {
				chunks = append(chunks, doc...)
			}
			out.Files[i].Ranked, _, err = Score(rules, vectors, chunks, files[i].Path, top)
			if err != nil {
				return out, err
			}
		}
		start = end
	}
	return out, nil
}
