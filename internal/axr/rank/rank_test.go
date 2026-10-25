// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rank

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/axr/catalog"
	"github.com/alibaba/open-code-review/internal/axr/embed"
)

const patch = "diff --git a/a.py b/a.py\n--- a/a.py\n+++ b/a.py\n@@ -1 +1 @@\n-old\n+new\n@@ -4 +4 @@\n-before\n+after\n"

func TestPatch(t *testing.T) {
	for _, text := range []string{patch, strings.TrimPrefix(patch, "diff --git a/a.py b/a.py\n"), strings.ReplaceAll(patch, "\n", "\r\n")} {
		files, err := ParsePatch(text)
		if err != nil || len(files) != 1 || files[0].Path != "a.py" || len(files[0].Hunks) != 2 {
			t.Fatalf("parse = %v, %v", files, err)
		}
	}
	for _, text := range []string{"invalid", strings.TrimSuffix(patch, "+after\n"), "@@ -1 +1 @@\n-a\n+b"} {
		if _, err := ParsePatch(text); err == nil {
			t.Errorf("accepted invalid patch %q", text)
		}
	}
	deleted := "--- a/old.py\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"
	files, err := ParsePatch(deleted)
	if err != nil || files[0].Path != "old.py" {
		t.Fatalf("delete: %v %v", files, err)
	}
	files, err = ParsePatch("diff --git a/old.py b/a/new.py\nrename from old.py\nrename to a/new.py\n")
	if err != nil || files[0].Path != "a/new.py" {
		t.Fatalf("rename: %v %v", files, err)
	}
	files, err = ParsePatch("--- \"a/a b.py\"\n+++ \"b/a b.py\"\n@@ -1 +1 @@\n-a\n+b\n")
	if err != nil || files[0].Path != "a b.py" {
		t.Fatalf("quoted: %v %v", files, err)
	}
	files, err = ParsePatch("diff --git \"a/a b.txt\" \"b/a b.txt\"\nBinary files differ\n")
	if err != nil || files[0].Path != "a b.txt" || len(files[0].Hunks) != 0 {
		t.Fatalf("binary path: %v %v", files, err)
	}
	marker := `\ No newline at end of file`
	files, err = ParsePatch(strings.Replace(patch, "-old\n", "-old\n"+marker+"\n", 1))
	if err != nil || !strings.Contains(files[0].Hunks[0], marker) || !strings.Contains(files[0].Patch, marker) {
		t.Fatalf("newline marker: %v %v", files, err)
	}
}

func TestScore(t *testing.T) {
	rules := []catalog.Rule{{ID: "b", Languages: []string{"python"}}, {ID: "a", Languages: []string{"python"}}, {ID: "c", Languages: []string{"python"}}}
	vectors := [][]float64{{1, 0}, {1, 0}, {-1, 0}}
	got, total, err := Score(rules, vectors, [][]float64{{0, 1}, {1, 0}}, "a.py", 2)
	if err != nil || total != 3 || len(got) != 2 || got[0].RuleID != "a" || got[1].RuleID != "b" || got[0].Score != 1 {
		t.Fatalf("rank: %v %d %v", got, total, err)
	}
	got, _, err = Score(rules, vectors, [][]float64{{1, 1}}, "a.py", 0)
	if err != nil || got[0].Score != 0.7071 {
		t.Fatalf("rounding: %v %v", got, err)
	}
	if _, _, err := Score(rules, vectors, [][]float64{{1}}, "a.py", 0); err == nil {
		t.Fatal("accepted mismatched dimensions")
	}
}

func TestRun(t *testing.T) {
	for _, model := range []string{"voyage-context-4", "voyage-code-4"} {
		t.Run(model, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/contextualizedembeddings" {
					fmt.Fprint(w, `{"data":[{"index":0,"data":[{"index":0,"embedding":[1,0]}]}],"usage":{"total_tokens":2}}`)
				} else {
					fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,0]}],"usage":{"total_tokens":2}}`)
				}
			}))
			defer server.Close()
			rules := []catalog.Rule{{ID: "rule", Text: "text", Languages: []string{"python"}}}
			files := []File{{Path: "a.py", Patch: "patch", Hunks: []string{"hunk"}}, {Path: "a.txt", Hunks: []string{"other"}}}
			c := embed.Client{BaseURL: server.URL, Key: "secret", Model: model, HTTP: server.Client()}
			out, err := Run(context.Background(), rules, files, c, t.TempDir(), 0)
			if err != nil || calls != 2 || out.Schema != "axr-rank/1" || out.Usage.RuleTokens != 2 || out.Usage.DiffTokens != 2 || len(out.Files) != 2 || out.Files[0].Ranked[0].Score != 1 || len(out.Files[1].Ranked) != 0 || out.Files[1].Ranked == nil {
				t.Fatalf("run: %+v %v calls %d", out, err, calls)
			}
		})
	}
}
