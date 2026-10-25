// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v4"
)

type Rule struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Languages []string `yaml:"languages"`
	Paths     []string `yaml:"paths"`
	Exclude   []string `yaml:"exclude"`
	Status    string   `yaml:"status"`
	Text      string   `yaml:"-"`
}

func Parse(text string) (Rule, error) {
	var rule Rule
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return rule, fmt.Errorf("missing YAML front matter")
	}
	end := slices.Index(lines[1:], "---") + 1
	if end == 0 {
		return rule, fmt.Errorf("unterminated YAML front matter")
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &rule); err != nil {
		return rule, err
	}
	if rule.ID == "" || rule.Title == "" || rule.Status == "" || len(rule.Languages) == 0 {
		return rule, fmt.Errorf("id, title, status and languages are required")
	}
	for _, pattern := range append(slices.Clone(rule.Paths), rule.Exclude...) {
		if !doublestar.ValidatePattern(pattern) {
			return rule, fmt.Errorf("invalid glob %q", pattern)
		}
	}
	body := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	parts := strings.Split(body, "\n")
	if len(parts) == 0 || parts[0] != "# "+rule.Title {
		return rule, fmt.Errorf("missing matching title line")
	}
	summary := ""
	for _, line := range parts[1:] {
		if strings.HasPrefix(line, "#") {
			break
		}
		if strings.TrimSpace(line) != "" {
			summary = strings.TrimSpace(line)
			break
		}
	}
	section := func(name string) string {
		start := slices.Index(parts, "## "+name)
		if start < 0 {
			return ""
		}
		stop := start + 1
		for stop < len(parts) && !strings.HasPrefix(parts[stop], "## ") {
			stop++
		}
		if strings.TrimSpace(strings.Join(parts[start+1:stop], "\n")) == "" {
			return ""
		}
		return strings.TrimSpace(strings.Join(parts[start:stop], "\n"))
	}
	instruction, report := section("Rule"), section("When to report")
	if summary == "" || instruction == "" || report == "" {
		return rule, fmt.Errorf("summary, Rule and When to report sections are required")
	}
	rule.Text = strings.Join([]string{parts[0], summary, instruction, report}, "\n\n")
	return rule, nil
}

func Load(root string, includeDrafts bool) ([]Rule, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	ids := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(resolved, "rules"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(resolved, target)
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		rule, err := Parse(string(data))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if ids[rule.ID] {
			return fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		ids[rule.ID] = true
		if rule.Status == "active" || (includeDrafts && rule.Status == "draft") {
			rules = append(rules, rule)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("no rules loaded; use --include-drafts for a draft catalog")
	}
	slices.SortFunc(rules, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	return rules, nil
}

func Language(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".pyi", ".ipynb":
		return "python"
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return "typescript"
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	}
	return ""
}

func (r Rule) InScope(path string) bool {
	language := Language(path)
	if language == "" {
		return false
	}
	found := false
	for _, candidate := range r.Languages {
		if candidate == "javascript" {
			candidate = "typescript"
		}
		if candidate == language {
			found = true
		}
	}
	if !found {
		return false
	}
	matches := func(patterns []string) bool {
		for _, p := range patterns {
			if ok, _ := doublestar.Match(p, path); ok {
				return true
			}
		}
		return false
	}
	return (len(r.Paths) == 0 || matches(r.Paths)) && !matches(r.Exclude)
}
