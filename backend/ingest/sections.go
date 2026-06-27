package ingest

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Section is one chapter of a paper.
//
// Body holds the section's source text (produced by Sectionize, stored in
// <slug>.src.md). It is populated by Sectionize/LoadSectionIndex for internal
// use; the API layer maps to sectionDTO and does not expose Body.
type Section struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	Body  string `json:"body,omitempty"`
}

// sectionIndexEntry is the on-disk shape of sections/index.json. Body is
// intentionally omitted — body text lives in per-section <slug>.src.md files
// so the index stays small and listable without loading every section.
type sectionIndexEntry struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

const sectionizePrompt = `请用 Read 工具读取文件 %s。这是一篇学术论文，由 pdftotext 提取为纯文本，采用双栏排版——同一行里可能并排出现左右两栏的文本。请识别论文的章节结构。

输出一个 JSON 数组，每个元素代表一个章节：{"title": "章节标题", "body": "该章节正文"}。

识别要点：
- 章节标题形如 "Abstract"、"1 Introduction"、"3.1 Preliminary"、"5.1.3 Evaluation Metric"、"References"、"Acknowledgments" 等。编号可能是 1、3.1、5.1.3，也可能无编号。
- 必须区分章节标题与算法/伪代码步骤：出现在算法块里、形如 "1 Compute w <- ..."、"4 Initialize unassigned set U <- V" 的编号行是伪代码步骤，不是章节，不要当作标题也不要单独成节。
- body 是该标题下到下一章节标题之前的正文。请清洗双栏并排造成的杂乱换行与多余空格，按语义合并成可读段落；公式、符号、引用编号原样保留。
- References 作为一节即可，body 可为空或简短说明，不要逐条展开文献。
- 只输出 JSON 数组本身，不要 markdown 代码块标记，不要任何解释。`

// rawSection is the shape Claude returns in the sectionize JSON.
type rawSection struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Sectionize identifies the paper's chapters via one Claude call (paper.md is
// pdftotext output with no markdown headings, so local parsing can't do this).
// Results are cached to sections/index.json (+ per-section <slug>.src.md) so
// re-opening the view is instant. userDir is the Claude working directory;
// rawRelPath is the paper dir relative to userDir (e.g. "raw/papers/foo").
func Sectionize(userDir, rawRelPath, claudeBin string) ([]Section, error) {
	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	if cached, ok := LoadSectionIndex(sectionsDir); ok {
		return cached, nil
	}

	// Bound the sem acquire separately from the (longer) sectionize call so a
	// caller queued behind a background summary/ingest doesn't burn the whole
	// call budget waiting.
	acquireCtx, acCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer acCancel()
	select {
	case summarySem <- struct{}{}:
		defer func() { <-summarySem }()
	case <-acquireCtx.Done():
		return nil, fmt.Errorf("timed out waiting for sectionize slot: %w", acquireCtx.Err())
	}

	client := claude.NewClientWithPath(claudeBin)
	// Sectionize reads the whole paper and emits all section bodies — give it
	// more room than a single-section explain.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	paperRelPath := filepath.ToSlash(filepath.Join(rawRelPath, "paper.md"))
	prompt := fmt.Sprintf(sectionizePrompt, paperRelPath)
	out, err := client.SendSimpleWithRead(ctx, prompt, userDir)
	if err != nil {
		return nil, fmt.Errorf("failed to sectionize: %w", err)
	}
	raws, err := parseSectionJSON(out)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sectionize output: %w", err)
	}

	if err := os.MkdirAll(sectionsDir, 0755); err != nil {
		return nil, err
	}
	sections := make([]Section, 0, len(raws))
	entries := make([]sectionIndexEntry, 0, len(raws))
	for i, r := range raws {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			continue
		}
		body := strings.TrimSpace(r.Body)
		slug := sectionSlug(title, i)
		if body != "" {
			if err := os.WriteFile(filepath.Join(sectionsDir, slug+".src.md"), []byte(body), 0644); err != nil {
				return nil, err
			}
		}
		sections = append(sections, Section{Index: i, Title: title, Slug: slug, Body: body})
		entries = append(entries, sectionIndexEntry{Index: i, Title: title, Slug: slug})
	}
	if err := SaveSectionIndex(sectionsDir, entries); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseSectionJSON parses the JSON array Claude returned, tolerating
// ```json fences.
func parseSectionJSON(s string) ([]rawSection, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	var raws []rawSection
	if err := json.Unmarshal([]byte(s), &raws); err != nil {
		return nil, err
	}
	return raws, nil
}

// LoadSectionIndex reads sections/index.json (titles+slugs only, no bodies).
// Returns ok=false if not cached.
func LoadSectionIndex(sectionsDir string) ([]Section, bool) {
	b, err := os.ReadFile(filepath.Join(sectionsDir, "index.json"))
	if err != nil {
		return nil, false
	}
	var entries []sectionIndexEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, false
	}
	out := make([]Section, 0, len(entries))
	for _, e := range entries {
		out = append(out, Section{Index: e.Index, Title: e.Title, Slug: e.Slug})
	}
	return out, true
}

// SaveSectionIndex writes sections/index.json.
func SaveSectionIndex(sectionsDir string, entries []sectionIndexEntry) error {
	if err := os.MkdirAll(sectionsDir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sectionsDir, "index.json"), b, 0644)
}

// sectionSlug is charset-safe (hash-based) so CJK titles produce valid
// filenames, and includes the index to guarantee uniqueness even when two
// sections share a title.
func sectionSlug(title string, index int) string {
	h := sha1.Sum([]byte(title))
	return fmt.Sprintf("sec%d-%x", index, h[:4])
}

// SectionBodyExists reports whether a <slug>.src.md body file was written by
// Sectionize. Parent sections whose content lives entirely in sub-sections
// have no body file and thus can't be explained.
func SectionBodyExists(sectionsDir, slug string) bool {
	_, err := os.Stat(filepath.Join(sectionsDir, slug+".src.md"))
	return err == nil
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
这是上述学术论文的一个章节，章节标题为「%s」。请用中文讲解这一章节，让读者不读原文也能听懂。

输出要求（纯 Markdown，不要输出任何额外说明）：
1. 第一段：用 150-300 字讲清楚这一章在做什么、为什么需要、核心思路。
2. 如果本章涉及算法 / 方法 / 模型：用通俗的话讲清它怎么工作，讲直觉，不要照抄公式。
3. 如果本章点明了某个待解决的问题或挑战：说明它是什么、作者如何应对。
4. 最后以 "## 关键要点" 为标题，列 3-5 条一句话要点；不适用项可省略。
`

// GenerateSectionExplain asks Claude (via -p + Read) to explain one chapter.
// Unlike the earlier title-locate design, it points Claude at the section's
// pre-extracted body file (<slug>.src.md from Sectionize) — so duplicate
// titles can no longer make Claude explain the wrong occurrence.
// userDir is the Claude working directory; srcRelPath is <slug>.src.md
// relative to userDir.
func GenerateSectionExplain(userDir, srcRelPath, sectionTitle, claudeBin string) (string, error) {
	acquireCtx, acCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer acCancel()
	select {
	case summarySem <- struct{}{}:
		defer func() { <-summarySem }()
	case <-acquireCtx.Done():
		return "", fmt.Errorf("timed out waiting for generation slot: %w", acquireCtx.Err())
	}

	client := claude.NewClientWithPath(claudeBin)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(sectionExplainPrompt, srcRelPath, sectionTitle)
	out, err := client.SendSimpleWithRead(ctx, prompt, userDir)
	if err != nil {
		return "", fmt.Errorf("failed to generate section explain: %w", err)
	}
	return out, nil
}
