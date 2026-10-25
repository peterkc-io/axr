// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

//go:build axr

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alibaba/open-code-review/internal/axr/catalog"
	"github.com/alibaba/open-code-review/internal/axr/embed"
	"github.com/alibaba/open-code-review/internal/axr/rank"
	"github.com/spf13/cobra"
)

func newAXRRankCommand() *cobra.Command {
	var root, patch, model, cache string
	var drafts bool
	var dims, top int
	cmd := &cobra.Command{Use: "rank", Short: "Experimentally rank catalog rules against a unified diff", Args: cobra.NoArgs}
	flags := cmd.Flags()
	flags.StringVar(&root, "catalog", "", "catalog root directory")
	flags.StringVar(&patch, "diff", "", "patch file, or - for stdin")
	flags.BoolVar(&drafts, "include-drafts", false, "include draft rules")
	flags.StringVar(&model, "embed-model", "voyage-context-4", "Voyage embedding model")
	flags.IntVar(&dims, "dims", 0, "output dimensions (0 uses the model default)")
	flags.IntVar(&top, "top", 0, "maximum rules per file (0 returns all)")
	flags.StringVar(&cache, "cache-dir", "", "rule embedding cache directory (default: user cache/axr/embeddings)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if root == "" || patch == "" {
			return fmt.Errorf("--catalog and --diff are required")
		}
		if top < 0 || dims < 0 || model == "" {
			return fmt.Errorf("--top and --dims must be nonnegative and --embed-model must not be empty")
		}
		key := os.Getenv("VOYAGE_API_KEY")
		if key == "" {
			return fmt.Errorf("VOYAGE_API_KEY is required")
		}
		if cache == "" {
			dir, err := os.UserCacheDir()
			if err != nil {
				return err
			}
			cache = filepath.Join(dir, "axr", "embeddings")
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		rules, err := catalog.Load(absolute, drafts)
		if err != nil {
			return err
		}
		input := cmd.InOrStdin()
		if patch != "-" {
			file, err := os.Open(patch)
			if err != nil {
				return err
			}
			defer file.Close()
			input = file
		}
		data, err := io.ReadAll(input)
		if err != nil {
			return err
		}
		files, err := rank.ParsePatch(string(data))
		if err != nil {
			return err
		}
		out, err := rank.Run(cmd.Context(), rules, files, embed.Client{Key: key, Model: model, Dims: dims}, cache, top)
		if err != nil {
			return err
		}
		out.Catalog.Root = absolute
		out.Catalog.RulesLoaded = len(rules)
		out.Catalog.DraftsIncluded = drafts
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(out)
	}
	return cmd
}

func init() { rulesCmd.AddCommand(newAXRRankCommand()) }
