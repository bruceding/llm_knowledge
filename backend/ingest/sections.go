package ingest

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Section is one chapter of a paper.md, split by ## / ### headings.
// Body is unexported so it is never serialized into API JSON.
type Section struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	body  string `json:"-"`
}

// Body returns the section's source text (used by the generator, not by JSON).
func (s Section) Body() string { return s.body }

var (
	// headingRe matches ## or ### headings. H1 is intentionally excluded —
	// paper.md H1 is usually the paper title, not a chapter.
	headingRe = regexp.MustCompile(`^(#{2,3})\s+(.+?)\s*$`)
	// pageMarker matches "## Page 12" markers emitted by LLMExtract between
	// pages. These are page boundaries, not chapters, and must not become
	// sections.
	pageMarker = regexp.MustCompile(`(?i)^Page\s+\d+$`)
)

// SplitSections parses paper.md into sections by ## / ### headings.
// "## Page N" page markers are skipped. Returns sections in document order.
// Returns (nil, nil) if the file has no recognisable headings (e.g. the
// pdftotext output written by UploadPDF); the caller distinguishes that from
// a missing file via os.Stat before calling.
func SplitSections(paperMdPath string) ([]Section, error) {
	data, err := os.ReadFile(paperMdPath)
	if err != nil {
		return nil, err
	}
	text := stripFrontmatter(string(data))

	var sections []Section
	var cur *Section
	flush := func() {
		if cur == nil {
			return
		}
		cur.body = strings.TrimSpace(cur.body)
		sections = append(sections, *cur)
	}

	for _, line := range strings.Split(text, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil && !pageMarker.MatchString(m[2]) {
			flush()
			title := strings.TrimSpace(m[2])
			cur = &Section{
				Index: len(sections),
				Title: title,
				Slug:  sectionSlug(title, len(sections)),
			}
			continue
		}
		if cur != nil {
			cur.body += line + "\n"
		}
	}
	flush()
	return sections, nil
}

// sectionSlug is charset-safe (hash-based) so CJK titles produce valid
// filenames, and includes the index to guarantee uniqueness even when two
// sections share a title.
func sectionSlug(title string, index int) string {
	h := sha1.Sum([]byte(title))
	return fmt.Sprintf("sec%d-%x", index, h[:4])
}

// stripFrontmatter removes a leading "---\n...\n---\n" YAML block if present.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return s
	}
	return rest[idx+5:]
}

// LoadSectionExplain reads a cached explanation for slug, if present.
func LoadSectionExplain(sectionsDir, slug string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(sectionsDir, slug+".md"))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// SaveSectionExplain writes an explanation to the cache, creating the dir.
func SaveSectionExplain(sectionsDir, slug, content string) error {
	if err := os.MkdirAll(sectionsDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sectionsDir, slug+".md"), []byte(content), 0644)
}
