// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

//go:build axr

package main

import (
	"regexp"

	"github.com/spf13/cobra"
)

func init() {
	configureBuild = configureAXR
}

// The leading group also catches `_ocr`, the zsh completion file name.
var ocrWord = regexp.MustCompile(`(^|[^A-Za-z0-9])ocr\b`)

// Only the capitalized product names; `.opencodereview` and `OCR_*` stay.
var ocrTitle = regexp.MustCompile(`OpenCodeReview|\bOCR\b`)

func configureAXR() {
	productName = "axr"
	productURL = "https://github.com/peterkc-io/axr"
	rootCmd.Use = "axr"
	rootCmd.Short = "AXR - Catalog for writing and reviewing code and prose"
	rootCmd.Long = `AXR - Catalog for writing and reviewing code and prose

axr is OpenCodeReview with AXR's additions. Without AXR flags, its commands
behave as they do in ocr.`
	rootCmd.Flags().Lookup("version").Usage = "version for axr"
	renameHelp(rootCmd)
}

func renameHelp(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		sub.Long = renameText(sub.Long)
		sub.Example = renameText(sub.Example)
		renameHelp(sub)
	}
}

func renameText(s string) string {
	return ocrTitle.ReplaceAllString(ocrWord.ReplaceAllString(s, "${1}axr"), "AXR")
}
