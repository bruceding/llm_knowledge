package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitSections(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "paper.md")
	// Mirrors LLMExtract output: a "## Page N" page marker plus real headings.
	content := "---\ntitle: Test\n---\n\n" +
		"## Page 1\n\nintro text before headings\n\n" +
		"## Introduction\n\nWe study X.\n\n" +
		"## Method\n\nThe algo is Y.\n\n" +
		"### 3.1 Sub-detail\n\nMore on Y.\n\n" +
		"## Conclusion\n\nDone.\n"
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := SplitSections(p)
	if err != nil {
		t.Fatal(err)
	}

	wantTitles := []string{"Introduction", "Method", "3.1 Sub-detail", "Conclusion"}
	if len(got) != len(wantTitles) {
		t.Fatalf("got %d sections, want %d: %+v", len(got), len(wantTitles), got)
	}
	for i, want := range wantTitles {
		if got[i].Title != want {
			t.Errorf("section %d title: got %q want %q", i, got[i].Title, want)
		}
		if got[i].Index != i {
			t.Errorf("section %d index: got %d want %d", i, got[i].Index, i)
		}
		if got[i].Slug == "" {
			t.Errorf("section %d slug should not be empty", i)
		}
	}
	if got[0].Slug == got[1].Slug {
		t.Error("slugs should differ across sections")
	}
	// Method body ends where the "### 3.1 Sub-detail" sub-section starts.
	if !strings.Contains(got[1].Body(), "The algo is Y.") {
		t.Errorf("Method body missing text; got %q", got[1].Body())
	}
	if strings.Contains(got[1].Body(), "More on Y.") {
		t.Errorf("Method body should not include the sub-section body; got %q", got[1].Body())
	}
}

func TestSplitSectionsNoHeadings(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "paper.md")
	// Mirrors pdftotext output (UploadPDF): no markdown headings at all.
	if err := os.WriteFile(p, []byte("just plain text\nno headings here\n--- Page Break ---\nmore text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := SplitSections(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 sections for heading-less paper.md, got %d: %+v", len(got), got)
	}
}
