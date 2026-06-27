//go:build integration

// Integration test for Sectionize against a real paper.md + Claude CLI.
// Not run by default. Run manually:
//   SECTIONIZE_USER_DIR=/Users/.../.llm-knowledge/users/2 \
//   SECTIONIZE_RAW_RELPATH=raw/papers/2502.18965v1 \
//   go test ./ingest/ -tags=integration -run TestSectionizeIntegration -timeout 300s -v
package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSectionizeIntegration(t *testing.T) {
	userDir := os.Getenv("SECTIONIZE_USER_DIR")
	rawRelPath := os.Getenv("SECTIONIZE_RAW_RELPATH")
	claudeBin := os.Getenv("SECTIONIZE_CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}
	if userDir == "" || rawRelPath == "" {
		t.Skip("set SECTIONIZE_USER_DIR and SECTIONIZE_RAW_RELPATH to run")
	}

	sections, err := Sectionize(userDir, rawRelPath, claudeBin)
	if err != nil {
		t.Fatalf("Sectionize: %v", err)
	}
	if len(sections) == 0 {
		t.Fatal("expected >0 sections")
	}
	t.Logf("got %d sections:", len(sections))
	for _, s := range sections {
		t.Logf("  [%d] %q (slug=%s, body=%d bytes)", s.Index, s.Title, s.Slug, len(s.Body))
	}

	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	if _, err := os.Stat(filepath.Join(sectionsDir, "index.json")); err != nil {
		t.Fatal("index.json not written")
	}
	// First section should have a <slug>.src.md body file.
	if _, err := os.Stat(filepath.Join(sectionsDir, sections[0].Slug+".src.md")); err != nil {
		t.Fatalf("first section src.md not written: %v", err)
	}

	// Second call must hit cache (no Claude) and return the same list.
	sections2, err := Sectionize(userDir, rawRelPath, claudeBin)
	if err != nil {
		t.Fatalf("cached Sectionize: %v", err)
	}
	if len(sections2) != len(sections) {
		t.Fatalf("cache mismatch: %d vs %d", len(sections2), len(sections))
	}
	t.Log("cache hit OK")
}
