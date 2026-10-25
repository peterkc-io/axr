// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `---
id: test
title: Test
languages: [python]
paths: ["**/*.py"]
exclude: ["vendor/**"]
status: draft
version: 1
---
# Test

Summary.

## Rule
Do this.

## Why
Not embedded.

## When to report
When broken.

## Correct
Not embedded either.
`

func TestParseAndLoad(t *testing.T) {
	if _, err := Parse(strings.Replace(fixture, "[python]", "[java]", 1)); err == nil || !strings.Contains(err.Error(), "java") {
		t.Fatalf("unsupported language: %v", err)
	}
	if _, err := Parse(strings.Replace(fixture, "[python]", "[javascript]", 1)); err != nil {
		t.Fatal(err)
	}
	r, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Test\n\nSummary.\n\n## Rule\nDo this.\n\n## When to report\nWhen broken."
	if r.Text != want {
		t.Fatalf("text = %q", r.Text)
	}
	for _, text := range []string{"bad", strings.Replace(fixture, "status: draft", "status: [draft]", 1), strings.Replace(fixture, "title: Test", "", 1), strings.Replace(fixture, "## Rule", "## Other", 1), strings.Replace(fixture, "**/*.py", "[", 1)} {
		if _, err := Parse(text); err == nil {
			t.Errorf("accepted malformed rule %q", text)
		}
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, true); err == nil || strings.Contains(err.Error(), "--include-drafts") || !strings.Contains(err.Error(), filepath.Join(root, "rules")) {
		t.Fatalf("empty catalog with drafts: %v", err)
	}
	path := filepath.Join(root, "rules", "one.md")
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, false); err == nil || !strings.Contains(err.Error(), "--include-drafts") {
		t.Fatalf("draft filtering error = %v", err)
	}
	rules, err := Load(root, true)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules = %v, %v", rules, err)
	}
	outside := filepath.Join(t.TempDir(), "escape.md")
	if err := os.WriteFile(outside, []byte("not a rule"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "rules", "escape.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, true); err != nil {
		t.Fatalf("escaping symlink not skipped: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(fixture, "draft", "active", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules", "duplicate.md"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, true); err == nil {
		t.Fatal("accepted duplicate ID")
	}
}

func TestLoadSymlinkedRulesDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "source"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "one.md"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "source"), filepath.Join(root, "rules")); err != nil {
		t.Fatal(err)
	}
	if rules, err := Load(root, true); err != nil || len(rules) != 1 {
		t.Fatalf("symlinked rules dir inside root: %v, %v", rules, err)
	}

	escaping := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "one.md"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(escaping, "rules")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(escaping, true); err == nil || !strings.Contains(err.Error(), "outside the catalog root") {
		t.Fatalf("escaping rules dir: %v", err)
	}
}

func TestScope(t *testing.T) {
	r := Rule{Languages: []string{"python"}}
	for _, path := range []string{"a.py", "a.pyi", "a.ipynb"} {
		if !r.InScope(path) {
			t.Errorf("not in scope: %s", path)
		}
	}
	r = Rule{Languages: []string{"javascript"}, Paths: []string{"src/**"}, Exclude: []string{"**/test/**"}}
	for _, ext := range []string{"ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs"} {
		if !r.InScope("src/nested/a." + ext) {
			t.Errorf("missing %s", ext)
		}
	}
	for _, path := range []string{"src/test/a.js", "other/a.ts", "src/a.py", "src/a.txt"} {
		if r.InScope(path) {
			t.Errorf("unexpected scope: %s", path)
		}
	}
	if Language("x.go") != "go" || Language("x.rs") != "rust" || Language("x.txt") != "" {
		t.Fatal("incorrect language map")
	}
}
