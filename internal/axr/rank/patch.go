// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rank

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type File struct {
	Path, Patch string
	Hunks       []string
}

var gitHeader = regexp.MustCompile(`^diff --git ("(?:[^"\\]|\\.)*"|a/.*?) ("(?:[^"\\]|\\.)*"|b/.*)$`)

var hunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,([0-9]+))? \+[0-9]+(?:,([0-9]+))? @@`)

func patchPath(s string) (string, error) {
	s = strings.SplitN(s, "\t", 2)[0]
	if strings.HasPrefix(s, `"`) {
		value, err := strconv.Unquote(s)
		if err != nil {
			return "", err
		}
		s = value
	}
	if s == "" {
		return "", fmt.Errorf("empty patch path")
	}
	return s, nil
}

func ParsePatch(text string) ([]File, error) {
	files := []File{}
	var current *File
	oldPath := ""
	oldLeft, newLeft := 0, 0
	flush := func() {
		if current != nil {
			files = append(files, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n") {
		if line == `\ No newline at end of file` && current != nil && len(current.Hunks) > 0 {
			current.Hunks[len(current.Hunks)-1] += "\n" + line
			current.Patch += line + "\n"
			continue
		}
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case line == "" || strings.HasPrefix(line, " "):
				oldLeft--
				newLeft--
			case strings.HasPrefix(line, "-"):
				oldLeft--
			case strings.HasPrefix(line, "+"):
				newLeft--
			default:
				return nil, fmt.Errorf("invalid or truncated hunk")
			}
			if oldLeft < 0 || newLeft < 0 {
				return nil, fmt.Errorf("hunk line counts do not match")
			}
			current.Hunks[len(current.Hunks)-1] += "\n" + line
			current.Patch += line + "\n"
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			current = &File{Hunks: []string{}}
			if match := gitHeader.FindStringSubmatch(line); match != nil {
				path, err := patchPath(match[2])
				if err != nil {
					return nil, err
				}
				current.Path = strings.TrimPrefix(path, "b/")
			}
		case strings.HasPrefix(line, "--- "):
			if current != nil && len(current.Hunks) > 0 {
				flush()
			}
			if current == nil {
				current = &File{Hunks: []string{}}
			}
			var err error
			oldPath, err = patchPath(line[4:])
			oldPath = strings.TrimPrefix(oldPath, "a/")
			if err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				return nil, fmt.Errorf("new path without old path")
			}
			path, err := patchPath(line[4:])
			path = strings.TrimPrefix(path, "b/")
			if err != nil {
				return nil, err
			}
			if path == "/dev/null" {
				path = oldPath
			}
			current.Path = path
		case strings.HasPrefix(line, "rename to "):
			if current != nil {
				path, err := patchPath(line[len("rename to "):])
				if err != nil {
					return nil, err
				}
				current.Path = path
			}
		case strings.HasPrefix(line, "@@"):
			if current == nil || current.Path == "" {
				return nil, fmt.Errorf("hunk without file path")
			}
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("invalid hunk header")
			}
			counts := []*int{&oldLeft, &newLeft}
			for i, p := range counts {
				*p = 1
				if match[i+1] != "" {
					n, err := strconv.Atoi(match[i+1])
					if err != nil {
						return nil, err
					}
					*p = n
				}
			}
			current.Hunks = append(current.Hunks, line)
		}
		if current != nil {
			current.Patch += line + "\n"
		}
	}
	if oldLeft != 0 || newLeft != 0 {
		return nil, fmt.Errorf("truncated hunk")
	}
	flush()
	if len(files) == 0 {
		return nil, fmt.Errorf("no unified diff files found")
	}
	for _, f := range files {
		if f.Path == "" {
			return nil, fmt.Errorf("missing diff file path")
		}
	}
	return files, nil
}
