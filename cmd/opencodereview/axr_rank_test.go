// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

//go:build axr

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAXRRankCommand(t *testing.T) {
	found := false
	for _, cmd := range rulesCmd.Commands() {
		if cmd.Name() == "rank" {
			found = true
		}
	}
	if !found {
		t.Fatal("rank not registered")
	}
	for _, tc := range []struct {
		args      []string
		key, want string
	}{
		{nil, "", "--catalog and --diff"},
		{[]string{"--catalog", "catalog", "--diff", "-"}, "", "VOYAGE_API_KEY"},
		{[]string{"--catalog", "catalog", "--diff", "-", "--top", "-1"}, "secret", "nonnegative"},
	} {
		t.Setenv("VOYAGE_API_KEY", tc.key)
		cmd := newAXRRankCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(output.String(), `"schema"`) {
			t.Fatalf("command error: %v; output %s", err, output.String())
		}
	}
}
