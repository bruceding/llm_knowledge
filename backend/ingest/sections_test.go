package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSectionJSON(t *testing.T) {
	// Parser must tolerate ```json fences, bare JSON, AND leading/trailing
	// prose Claude might add despite "only JSON" instructions.
	cases := []string{
		"```json\n[{\"title\":\"Intro\",\"body\":\"x\"}]\n```",
		"[{\"title\":\"Intro\",\"body\":\"x\"}]",
		"Here is the JSON array:\n```json\n[{\"title\":\"Intro\",\"body\":\"x\"}]\n```\nDone.",
	}
	for _, in := range cases {
		got, err := parseSectionJSON(in)
		if err != nil {
			t.Fatalf("parseSectionJSON(%q) error: %v", in, err)
		}
		if len(got) != 1 || got[0].Title != "Intro" || got[0].Body != "x" {
			t.Fatalf("got %+v for %q", got, in)
		}
	}

	if _, err := parseSectionJSON("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSectionIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadSectionIndex(dir); ok {
		t.Fatal("expected miss on missing index.json")
	}
	entries := []sectionIndexEntry{
		{Index: 0, Title: "Introduction", Slug: "sec0-aaaa"},
		{Index: 1, Title: "Method", Slug: "sec1-bbbb"},
	}
	if err := SaveSectionIndex(dir, entries); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSectionIndex(dir)
	if !ok {
		t.Fatal("expected hit after save")
	}
	if len(got) != 2 || got[1].Title != "Method" || got[1].Slug != "sec1-bbbb" || got[1].Body != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestSectionExplainCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	slug := "sec0-abcd1234"

	if _, ok := LoadSectionExplain(dir, slug); ok {
		t.Fatal("expected cache miss on missing file")
	}
	if err := SaveSectionExplain(dir, slug, "## 关键要点\n- a\n"); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSectionExplain(dir, slug)
	if !ok {
		t.Fatal("expected cache hit after save")
	}
	if got != "## 关键要点\n- a\n" {
		t.Errorf("got %q", got)
	}
	// ensure index.json and explanation file coexist without collision
	if err := SaveSectionIndex(dir, []sectionIndexEntry{{Index: 0, Title: "T", Slug: slug}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatal("index.json missing")
	}
	if _, err := os.Stat(filepath.Join(dir, slug+".md")); err != nil {
		t.Fatal("explanation file missing")
	}
}
