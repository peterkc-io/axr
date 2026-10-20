// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

//go:build axr

package main

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func preserveAXRState(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	use, short, long, example := cmd.Use, cmd.Short, cmd.Long, cmd.Example
	t.Cleanup(func() {
		cmd.Use, cmd.Short, cmd.Long, cmd.Example = use, short, long, example
	})
	if cmd == rootCmd {
		name, url := productName, productURL
		usage := cmd.Flags().Lookup("version").Usage
		t.Cleanup(func() {
			productName, productURL = name, url
			cmd.Flags().Lookup("version").Usage = usage
		})
	}
	for _, sub := range cmd.Commands() {
		preserveAXRState(t, sub)
	}
}

func TestAXRIdentity(t *testing.T) {
	preserveAXRState(t, rootCmd)
	version, commit, date := Version, GitCommit, BuildDate
	t.Cleanup(func() { Version, GitCommit, BuildDate = version, commit, date })
	Version, GitCommit, BuildDate = "v0.1.0", "test", "test"
	configureAXR()

	if got := rootCmd.Use; got != "axr" {
		t.Errorf("root Use = %q, want axr", got)
	}
	if got := rootCmd.Short; got != "AXR - Catalog for writing and reviewing code and prose" {
		t.Errorf("root Short = %q, want the AXR tagline", got)
	}
	if !strings.Contains(rootCmd.Long, "Catalog for writing and reviewing code and prose") ||
		!strings.Contains(rootCmd.Long, "Without AXR flags") || !strings.Contains(rootCmd.Long, "ocr") {
		t.Errorf("root Long = %q, want the tagline and ocr compatibility explanation", rootCmd.Long)
	}
	if got := rootCmd.Flags().Lookup("version").Usage; got != "version for axr" {
		t.Errorf("--version usage = %q, want version for axr", got)
	}
	want := fmt.Sprintf("axr v0.1.0 (test) %s/%s\nbuilt at: test\nhttps://github.com/peterkc-io/axr\n", runtime.GOOS, runtime.GOARCH)
	if got := versionString(); got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestAXRSubcommandHelpNamesAXR(t *testing.T) {
	preserveAXRState(t, rootCmd)
	configureAXR()

	ocr := regexp.MustCompile(`(^|[^A-Za-z0-9])ocr([^A-Za-z0-9]|$)`)
	title := regexp.MustCompile(`OpenCodeReview|(^|[^A-Za-z0-9_])OCR([^A-Za-z0-9_]|$)`)
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			for field, text := range map[string]string{"Long": sub.Long, "Example": sub.Example} {
				if ocr.MatchString(text) || title.MatchString(text) {
					t.Errorf("%s %s still names ocr or OpenCodeReview: %q", sub.CommandPath(), field, text)
				}
			}
			walk(sub)
		}
	}
	walk(rootCmd)

	if !strings.Contains(configSetCmd.Example, "axr config set provider anthropic") {
		t.Errorf("config set example = %q, want axr config set provider anthropic", configSetCmd.Example)
	}
	if !strings.HasPrefix(delegateCmd.Long, "AXR - Delegation Mode") {
		t.Errorf("delegate help = %q, want the AXR title", delegateCmd.Long)
	}
	if !strings.Contains(completionCmd.Long, `/_axr"`) {
		t.Errorf("completion help = %q, want the zsh file named _axr", completionCmd.Long)
	}
}
