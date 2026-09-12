package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileUpToDate ensures impersonate/presets_gen.go matches what the
// generator produces from the vendored impersonate.c, so the committed data
// cannot silently drift from the parser.
func TestGeneratedFileUpToDate(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "impersonate", "upstream", "impersonate.c"))
	if err != nil {
		t.Fatal(err)
	}
	presets, err := parsePresets(stripComments(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 39 {
		t.Fatalf("parsed %d presets, want 39", len(presets))
	}
	code, err := renderGo(presets)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "impersonate", "presets_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(code) != string(want) {
		t.Error("impersonate/presets_gen.go is stale; run: go generate ./...")
	}
}
