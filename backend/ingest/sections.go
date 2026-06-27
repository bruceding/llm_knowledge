package ingest

import (
	"context"
	"crypto/sha1"
	"fmt"
	"llm-knowledge/claude"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

const sectionExplainPrompt = `请用 Read 工具读取文件 %s。
这是一篇学术论文的一个章节，章节标题为「%s」。
请在文中定位该标题下的章节内容（通常是 ## 或 ### 标题；忽略形如 "## Page N" 的分页标记），然后用中文讲解这一章节，让读者不读原文也能听懂。

输出要求（纯 Markdown，不要输出任何额外说明）：
1. 第一段：用 150-300 字讲清楚这一章在做什么、为什么需要、核心思路。
2. 如果本章涉及算法 / 方法 / 模型：用通俗的话讲清它怎么工作，讲直觉，不要照抄公式。
3. 如果本章点明了某个待解决的问题或挑战：说明它是什么、作者如何应对。
4. 最后以 "## 关键要点" 为标题，列 3-5 条一句话要点；不适用项可省略。
`

// GenerateSectionExplain asks Claude (via -p + Read, same path as GenerateSummary)
// to explain one chapter of the paper. userDir is the Claude working directory;
// paperRelPath is paper.md relative to userDir (e.g. "raw/papers/foo/paper.md").
// Concurrency is bounded by the shared summarySem so it never races with
// summary/ingest generation. The sem acquire is bounded by ctx so a queued
// caller (sem held by a background summary/ingest) can't block the HTTP
// handler forever — it fails with ctx.Err() instead.
func GenerateSectionExplain(userDir, paperRelPath, sectionTitle, claudeBin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	select {
	case summarySem <- struct{}{}:
		defer func() { <-summarySem }()
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for generation slot: %w", ctx.Err())
	}

	client := claude.NewClientWithPath(claudeBin)
	prompt := fmt.Sprintf(sectionExplainPrompt, paperRelPath, sectionTitle)
	out, err := client.SendSimpleWithRead(ctx, prompt, userDir)
	if err != nil {
		return "", fmt.Errorf("failed to generate section explain: %w", err)
	}
	return out, nil
}
