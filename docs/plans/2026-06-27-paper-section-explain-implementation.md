# Paper Section Explain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-section "讲解" (explanation) view for PDF papers so users get each chapter explained upfront, instead of asking in doc-chat one question at a time; follow-up questions still go through the existing doc-chat.

**Architecture:** A new `ingest.SplitSections` parses the existing `paper.md` into sections by `##`/`###` headings (skipping `## Page N` page markers). A new `ingest.GenerateSectionExplain` reuses the `claude.Client.SendSimpleWithRead` `-p` path (blocking, no streaming) — same pattern as `GenerateSummary`. Explanations are cached as files under `raw/papers/<name>/sections/<slug>.md`. Two new routes on `DocHandler` expose list + lazy-generate. A new `PaperSectionsView` React component is wired into `DocDetail` as a `sections` view mode for PDF docs. The existing doc-chat tab is the follow-up path — no new chat wiring.

**Tech Stack:** Go (echo, gorm), Claude CLI `-p` mode, React + TypeScript + react-markdown, Playwright e2e.

---

## File Structure

**Backend (new):**
- `backend/ingest/sections.go` — `SplitSections`, `GenerateSectionExplain`, cache helpers (`LoadSectionExplain`, `SaveSectionExplain`). Pure-ish: split is pure file parsing; generate mirrors `GenerateSummary`.
- `backend/ingest/sections_test.go` — tests for split + cache (TDD).
- `backend/api/sections.go` — `(*DocHandler).ListSections`, `(*DocHandler).GenerateSection`. Thin glue over `ingest`.

**Backend (modified):**
- `backend/main.go` — register two new routes on `docH`.

**Frontend (new):**
- `frontend/src/components/PaperSectionsView.tsx` — section list (left nav) + explanations (right), lazy generate on click, empty-state guidance.

**Frontend (modified):**
- `frontend/src/types.ts` — add `PaperSection` type.
- `frontend/src/api.ts` — add `fetchPaperSections`, `generatePaperSection`.
- `frontend/src/components/DocDetail.tsx` — add `sections` view mode + tab button (PDF only) + render branch.
- `frontend/src/i18n/locales/zh.json`, `en.json` — add `paperSections` namespace.

**E2E (new):**
- `tests/e2e/test_paper_sections.py` — seeds a PDF, opens detail, clicks the tab, asserts empty-state renders.

---

## Task 1: Section splitting — `SplitSections`

**Files:**
- Create: `backend/ingest/sections.go`
- Create: `backend/ingest/sections_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/ingest/sections_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./ingest/ -run TestSplitSections -v`
Expected: FAIL — `SplitSections` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/ingest/sections.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./ingest/ -run TestSplitSections -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add backend/ingest/sections.go backend/ingest/sections_test.go
git commit -m "feat(ingest): add SplitSections for paper.md chapter parsing"
```

---

## Task 2: Cache helpers — round-trip test

**Files:**
- Modify: `backend/ingest/sections_test.go`
- (Implementation already in `backend/ingest/sections.go` from Task 1.)

- [ ] **Step 1: Write the failing test**

Append to `backend/ingest/sections_test.go`:

```go
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
}
```

- [ ] **Step 2: Run test to verify it passes (implementation already exists)**

Run: `cd backend && go test ./ingest/ -run TestSectionExplainCacheRoundTrip -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add backend/ingest/sections_test.go
git commit -m "test(ingest): cover section explanation cache round-trip"
```

---

## Task 3: Section explanation generation — `GenerateSectionExplain`

**Files:**
- Modify: `backend/ingest/sections.go`

No unit test — this is a thin wrapper around the Claude CLI, same as `GenerateSummary` which has no test. The prompt + wiring are exercised by the e2e flow (Task 8) and manually.

- [ ] **Step 1: Add the generator**

Append to `backend/ingest/sections.go`. Add these imports to the file's import block: `"context"`, `"llm-knowledge/claude"`, `"time"`.

```go
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
// summary/ingest generation.
func GenerateSectionExplain(userDir, paperRelPath, sectionTitle, claudeBin string) (string, error) {
	summarySem <- struct{}{}
	defer func() { <-summarySem }()

	client := claude.NewClientWithPath(claudeBin)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(sectionExplainPrompt, paperRelPath, sectionTitle)
	out, err := client.SendSimpleWithRead(ctx, prompt, userDir)
	if err != nil {
		return "", fmt.Errorf("failed to generate section explain: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `cd backend && go build ./...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add backend/ingest/sections.go
git commit -m "feat(ingest): add GenerateSectionExplain via Claude -p"
```

---

## Task 4: API handler + routes

**Files:**
- Create: `backend/api/sections.go`
- Modify: `backend/main.go` (route registration, near the existing `docH` routes)

- [ ] **Step 1: Create the handler**

Create `backend/api/sections.go`:

```go
package api

import (
	"net/http"
	"path/filepath"
	"strconv"

	"llm-knowledge/db"
	"llm-knowledge/ingest"

	"github.com/labstack/echo/v4"
)

type sectionDTO struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Explanation string `json:"explanation,omitempty"`
}

// ListSections returns the paper's chapter list with any cached explanations.
// GET /api/documents/:id/sections
// Response: { "sections": [...], "paperMdExists": bool }
func (h *DocHandler) ListSections(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}
	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents have sections"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")

	// Missing paper.md → empty list + flag so the UI can guide the user to
	// extract first, rather than showing a hard error.
	if _, err := os.Stat(paperMdPath); err != nil {
		return c.JSON(http.StatusOK, echo.Map{"sections": []sectionDTO{}, "paperMdExists": false})
	}

	sections, err := ingest.SplitSections(paperMdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to split sections: " + err.Error()})
	}

	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	out := make([]sectionDTO, 0, len(sections))
	for _, s := range sections {
		dto := sectionDTO{Index: s.Index, Title: s.Title, Slug: s.Slug}
		if exp, ok := ingest.LoadSectionExplain(sectionsDir, s.Slug); ok {
			dto.Explanation = exp
		}
		out = append(out, dto)
	}
	return c.JSON(http.StatusOK, echo.Map{"sections": out, "paperMdExists": true})
}

// GenerateSection generates (or regenerates) the explanation for one section
// by index, caches it, and returns it. Blocking -p call, no streaming.
// POST /api/documents/:id/sections/:index/generate
func (h *DocHandler) GenerateSection(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid section index"})
	}

	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}
	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents have sections"})
	}
	if h.ClaudeBin == "" {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "claude binary not configured"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")
	sections, err := ingest.SplitSections(paperMdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to split sections: " + err.Error()})
	}
	if idx < 0 || idx >= len(sections) {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "section index out of range"})
	}

	section := sections[idx]
	paperRelPath := filepath.ToSlash(filepath.Join(rawRelPath, "paper.md"))
	explanation, err := ingest.GenerateSectionExplain(userDir, paperRelPath, section.Title, h.ClaudeBin)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to generate explanation: " + err.Error()})
	}

	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	if err := ingest.SaveSectionExplain(sectionsDir, section.Slug, explanation); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to cache explanation: " + err.Error()})
	}

	return c.JSON(http.StatusOK, sectionDTO{
		Index:       section.Index,
		Title:       section.Title,
		Slug:        section.Slug,
		Explanation: explanation,
	})
}
```

Add `"os"` to the import block of `backend/api/sections.go`.

- [ ] **Step 2: Register routes**

In `backend/main.go`, find the line:

```go
	apiGroup.POST("/documents/:id/regenerate-summary", docH.RegenerateSummary)
```

Add immediately after it:

```go
	apiGroup.GET("/documents/:id/sections", docH.ListSections)
	apiGroup.POST("/documents/:id/sections/:index/generate", docH.GenerateSection)
```

- [ ] **Step 3: Build to verify it compiles**

Run: `cd backend && go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add backend/api/sections.go backend/main.go
git commit -m "feat(api): add paper section list + generate routes"
```

---

## Task 5: Frontend types + API client

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api.ts`

- [ ] **Step 1: Add the type**

In `frontend/src/types.ts`, append after the `Document` interface (after line 23):

```ts
export interface PaperSection {
  index: number
  title: string
  slug: string
  explanation?: string
}
```

- [ ] **Step 2: Add the API helpers**

In `frontend/src/api.ts`, update the import on line 1 to include `PaperSection`:

```ts
import type { Document, UpdateDocRequest, SSEEvent, UserSettings, GlobalSettings, Conversation, Message, LoginResponse, RegisterResponse, CaptchaResponse, IMAPConfigInput, IMAPConfigResponse, IMAPTestResult, IMAPFoldersResult, NewsletterSyncStatus, PaperSection } from './types'
```

Append at the end of the file:

```ts
// Paper Sections API (per-chapter explanation for PDF papers)
export async function fetchPaperSections(docId: number): Promise<{ sections: PaperSection[]; paperMdExists: boolean }> {
  const res = await authFetch(`${API_BASE}/documents/${docId}/sections`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch sections')
  return res.json()
}

export async function generatePaperSection(docId: number, index: number): Promise<PaperSection> {
  const res = await authFetch(`${API_BASE}/documents/${docId}/sections/${index}/generate`, {
    method: 'POST',
    headers: getHeaders(),
  })
  if (!res.ok) throw new Error('Failed to generate section explanation')
  return res.json()
}
```

- [ ] **Step 3: Verify it type-checks**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/types.ts frontend/src/api.ts
git commit -m "feat(frontend): add PaperSection type + section API client"
```

---

## Task 6: `PaperSectionsView` component

**Files:**
- Create: `frontend/src/components/PaperSectionsView.tsx`

- [ ] **Step 1: Create the component**

Create `frontend/src/components/PaperSectionsView.tsx`:

```tsx
import { useState, useEffect, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useTranslation } from 'react-i18next'
import { fetchPaperSections, generatePaperSection, type PaperSection } from '../api'

// PaperSectionsView: left chapter list + right per-chapter explanation.
// Explanations are lazy-generated on click (blocking -p on the backend),
// then cached to disk so re-opening the view is instant. See
// docs/plans/2026-06-27-paper-section-explain-design.md for the full layout.
export default function PaperSectionsView({ docId, summary, onAskPaper }: { docId: number; summary: string; onAskPaper: () => void }) {
  const { t } = useTranslation()
  const [sections, setSections] = useState<PaperSection[]>([])
  const [paperMdExists, setPaperMdExists] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [generatingIndex, setGeneratingIndex] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { sections, paperMdExists } = await fetchPaperSections(docId)
      setSections(sections)
      setPaperMdExists(paperMdExists)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load sections')
    } finally {
      setLoading(false)
    }
  }, [docId])

  useEffect(() => { load() }, [load])

  const handleGenerate = async (index: number) => {
    setGeneratingIndex(index)
    setError(null)
    try {
      const updated = await generatePaperSection(docId, index)
      setSections(prev => prev.map(s => (s.index === index ? updated : s)))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to generate')
    } finally {
      setGeneratingIndex(null)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-500" />
      </div>
    )
  }
  if (error) {
    return <div className="p-6 text-red-600">{error}</div>
  }
  if (!paperMdExists) {
    return (
      <div data-testid="paper-sections-empty" className="p-6 text-gray-500">
        {t('paperSections.noPaperMd')}
      </div>
    )
  }
  if (sections.length === 0) {
    return (
      <div data-testid="paper-sections-empty" className="p-6 text-gray-500">
        {t('paperSections.noSections')}
      </div>
    )
  }

  return (
    <div className="flex h-full">
      <nav className="w-56 shrink-0 border-r border-gray-200 overflow-auto p-2 space-y-1">
        {sections.map(s => (
          <button
            key={s.index}
            onClick={() => document.getElementById(`section-${s.index}`)?.scrollIntoView({ behavior: 'smooth' })}
            className={`block w-full text-left px-2 py-1.5 rounded text-sm ${s.explanation ? 'text-blue-700' : 'text-gray-600'} hover:bg-gray-100`}
          >
            {s.title}
          </button>
        ))}
      </nav>
      <div className="flex-1 flex flex-col overflow-hidden">
        {summary && (
          <div className="px-6 py-2 bg-gray-50 border-b border-gray-200 text-sm text-gray-600">
            📄 {summary}
          </div>
        )}
        <div className="flex-1 overflow-auto p-6 max-w-3xl prose prose-slate" data-testid="paper-sections-content">
          {sections.map(s => (
            <section key={s.index} id={`section-${s.index}`} className="mb-8">
              <h2 className="text-xl font-semibold mb-2">{s.title}</h2>
              {s.explanation ? (
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.explanation}</ReactMarkdown>
              ) : (
                <button
                  onClick={() => handleGenerate(s.index)}
                  disabled={generatingIndex !== null}
                  className="px-3 py-1.5 text-sm bg-blue-100 text-blue-700 rounded hover:bg-blue-200 disabled:opacity-50"
                >
                  {generatingIndex === s.index ? t('paperSections.generating') : t('paperSections.generate')}
                </button>
              )}
            </section>
          ))}
          <div className="mt-8">
            <button
              onClick={onAskPaper}
              className="px-3 py-1.5 text-sm bg-blue-100 text-blue-700 rounded hover:bg-blue-200"
            >
              💬 {t('paperSections.askPaper')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Verify it type-checks (component is not yet imported, so just lint)**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/PaperSectionsView.tsx
git commit -m "feat(frontend): add PaperSectionsView component"
```

---

## Task 7: Wire `PaperSectionsView` into `DocDetail`

**Files:**
- Modify: `frontend/src/components/DocDetail.tsx`

- [ ] **Step 1: Add the import**

In `frontend/src/components/DocDetail.tsx`, add to the imports near line 14 (after the `DualPDFViewer` import):

```tsx
import PaperSectionsView from './PaperSectionsView'
```

- [ ] **Step 2: Extend the `viewMode` union**

Find (line 80):

```tsx
  const [viewMode, setViewMode] = useState<'wiki' | 'translation' | 'bilingual' | 'pdf' | 'dual-pdf' | 'raw' | 'html'>('raw')
```

Replace with:

```tsx
  const [viewMode, setViewMode] = useState<'wiki' | 'translation' | 'bilingual' | 'pdf' | 'dual-pdf' | 'raw' | 'html' | 'sections'>('raw')
```

- [ ] **Step 3: Render the sections view**

In `renderContent`, find the first `if (mode === 'html' && htmlContent) {` line (line 669) and insert **before** it:

```tsx
    if (mode === 'sections') {
      return (
        <PaperSectionsView
          docId={document.id}
          summary={document.summary}
          onAskPaper={() => { setPanelHidden(false); setMetadataTab('chat') }}
        />
      )
    }
```

- [ ] **Step 4: Add the tab button (PDF only)**

Find the `isPDF ? (` view-mode button branch (around line 843). Replace the `isPDF ? (...)` block's first button with two buttons — the existing PDF button plus a new sections button. The current code is:

```tsx
            {isPDF ? (
              <button
                onClick={() => setViewMode('pdf')}
                className={`px-3 py-1.5 rounded-lg text-sm ${
                  viewMode === 'pdf' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                }`}
              >
                PDF
              </button>
            ) : (
```

Replace with:

```tsx
            {isPDF ? (
              <>
                <button
                  onClick={() => setViewMode('pdf')}
                  className={`px-3 py-1.5 rounded-lg text-sm ${
                    viewMode === 'pdf' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                  }`}
                >
                  PDF
                </button>
                <button
                  onClick={() => setViewMode('sections')}
                  data-testid="paper-sections-tab"
                  className={`px-3 py-1.5 rounded-lg text-sm ${
                    viewMode === 'sections' ? 'bg-blue-100 text-blue-700' : 'text-gray-600 hover:bg-gray-100'
                  }`}
                >
                  {t('paperSections.tab')}
                </button>
              </>
            ) : (
```

- [ ] **Step 5: Verify type-check + build**

Run: `cd frontend && npx tsc --noEmit && npm run build`
Expected: build succeeds.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/DocDetail.tsx
git commit -m "feat(frontend): wire PaperSectionsView into DocDetail for PDFs"
```

---

## Task 8: i18n strings

**Files:**
- Modify: `frontend/src/i18n/locales/zh.json`
- Modify: `frontend/src/i18n/locales/en.json`

- [ ] **Step 1: Add Chinese strings**

In `frontend/src/i18n/locales/zh.json`, find the closing of the `docDetail` object (the `}` on its own line before the `wikiView` key). Insert a new `paperSections` namespace immediately after the `docDetail` object's closing brace. Concretely, change:

```json
    "viewOriginal": "查看原文",
    "viewTranslation": "查看译文"
  },
  "wikiView": {
```

to:

```json
    "viewOriginal": "查看原文",
    "viewTranslation": "查看译文"
  },
  "paperSections": {
    "tab": "章节讲解",
    "generate": "生成讲解",
    "generating": "生成中...",
    "noSections": "未识别到章节。可尝试用「LLM 提取」重新生成带标题层级的论文内容后再来。",
    "noPaperMd": "尚未生成论文内容（paper.md）。请先在 PDF 视图旁的操作中提取论文。",
    "askPaper": "对这篇论文提问"
  },
  "wikiView": {
```

- [ ] **Step 2: Add English strings**

In `frontend/src/i18n/locales/en.json`, make the same structural change. Change:

```json
    "viewOriginal": "View Original",
    "viewTranslation": "View Translation"
  },
  "wikiView": {
```

to:

```json
    "viewOriginal": "View Original",
    "viewTranslation": "View Translation"
  },
  "paperSections": {
    "tab": "Sections",
    "generate": "Generate",
    "generating": "Generating...",
    "noSections": "No sections detected. Try re-extracting the paper with LLM extraction to get heading-level content.",
    "noPaperMd": "Paper content (paper.md) not generated yet. Please extract the paper first.",
    "askPaper": "Ask about this paper"
  },
  "wikiView": {
```

(Verify the exact `viewOriginal` / `viewTranslation` values in `en.json` match before editing; if the English values differ, keep the existing values and only insert the `paperSections` block between the `}` and `"wikiView":`.)

- [ ] **Step 3: Verify JSON is valid**

Run: `cd frontend && python3 -c "import json;json.load(open('src/i18n/locales/zh.json'));json.load(open('src/i18n/locales/en.json'));print('ok')"`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/i18n/locales/zh.json frontend/src/i18n/locales/en.json
git commit -m "i18n: add paperSections namespace (zh/en)"
```

---

## Task 9: E2E test

**Files:**
- Create: `tests/e2e/test_paper_sections.py`

This test seeds a real PDF via `/api/raw/pdf` (which produces a `paper.md` via `pdftotext` — no `##` headings, so zero sections), opens the doc detail, clicks the "章节讲解" tab, and asserts the empty-state guidance renders. It is deterministic and does not require the Claude CLI.

- [ ] **Step 1: Create the test**

Create `tests/e2e/test_paper_sections.py`:

```python
"""
E2E test for the paper section-explain feature.

Seeds a PDF via /api/raw/pdf (UploadPDF uses pdftotext, whose paper.md has no
## markdown headings), opens the doc detail page, clicks the "章节讲解" tab,
and asserts the empty-state guidance renders. This validates the full wiring
(tab button -> view -> GET /api/documents/:id/sections -> empty state)
without depending on the Claude CLI.
"""

from pathlib import Path

import pytest
from playwright.sync_api import Page

SAMPLE_PDF = Path(__file__).parent.parent.parent / "backend" / "ingest" / "testdata" / "sample.pdf"


def _upload_pdf(page: Page) -> int:
    token = page.evaluate("localStorage.getItem('token')")
    assert token, "not authenticated"
    data = SAMPLE_PDF.read_bytes()
    resp = page.request.post(
        "http://localhost:9090/api/raw/pdf",
        headers={"Authorization": f"Bearer {token}"},
        multipart={
            "file": {"filename": "sample.pdf", "mimeType": "application/pdf", "buffer": data},
        },
    )
    assert resp.ok, f"PDF upload failed: {resp.status} {resp.text()}"
    body = resp.json()
    return int(body["id"])


def _delete_document(page: Page, doc_id: int) -> None:
    page.evaluate(
        """async (id) => {
            const t = localStorage.getItem('token');
            await fetch('/api/documents/' + id, {
                method: 'DELETE',
                headers: {'Authorization': 'Bearer ' + t},
            });
        }""",
        doc_id,
    )


@pytest.mark.requires_auth
def test_pdf_sections_tab_shows_empty_state(authenticated_page: Page):
    page = authenticated_page
    doc_id = _upload_pdf(page)
    try:
        page.goto(f"http://localhost:9090/documents/{doc_id}")
        page.wait_for_load_state("networkidle")
        page.wait_for_selector("[data-testid='paper-sections-tab']", timeout=5000)
        page.click("[data-testid='paper-sections-tab']")
        # pdftotext output has no ## section headings -> empty-state guidance.
        page.wait_for_selector("[data-testid='paper-sections-empty']", timeout=5000)
        assert page.is_visible("[data-testid='paper-sections-empty']")
    finally:
        _delete_document(page, doc_id)
```

- [ ] **Step 2: Run the e2e test (requires the app running via start.sh + .venv)**

Run: `source .venv/bin/activate && pytest tests/e2e/test_paper_sections.py -v`
Expected: PASS. If pdftotext is not installed in the environment, the upload step fails — install dependencies first (the app's dependency checker) and re-run.

- [ ] **Step 3: Commit**

```bash
git add tests/e2e/test_paper_sections.py
git commit -m "test(e2e): cover paper sections tab + empty-state wiring"
```

---

## Task 10: Manual end-to-end verification

- [ ] **Step 1: Start the app**

Run: `./start.sh`

- [ ] **Step 2: Pick a PDF paper that has been through LLM extraction**

In the UI, open a PDF document whose `paper.md` was generated by "LLM 提取" (so it has real `##`/`###` headings). If none exists, upload a PDF and click "LLM 提取" first.

- [ ] **Step 3: Click "章节讲解"**

Verify: left nav lists the paper's chapters (Introduction / Method / …), `## Page N` markers do NOT appear as chapters.

- [ ] **Step 4: Generate one chapter's explanation**

Click "生成讲解" on one chapter. Verify: a markdown explanation renders (a paragraph + a "## 关键要点" list). Re-opening the view shows it instantly (cache hit, no regenerate).

- [ ] **Step 5: Follow up via doc-chat**

Switch to the Chat tab and ask a detail question about that chapter. Verify the existing doc-chat still works (it reads `paper.md` as before).

- [ ] **Step 6: Empty-state check**

Open a freshly uploaded PDF (only `pdftotext`, no LLM extract) → "章节讲解" → verify the "未识别到章节…" guidance shows.

---

## Self-Review

**Spec coverage:**
- Per-chapter explanation generated from `paper.md` → Task 3 (generate) + Task 4 (route) + Task 6 (UI).
- Chapter splitting → Task 1.
- "一段讲解为主 + 可选结构化要点" product form → Task 3 prompt (paragraph + `## 关键要点` list, rendered as one markdown block in Task 6).
- Lazy load + disk cache → Task 6 (click-to-generate) + Task 1/2 (cache helpers) + Task 4 (load cached on list).
- Blocking, no streaming → Task 3 uses `SendSimpleWithRead` (`-p`), no SSE.
- Reuse doc-chat for follow-up → explicitly no new chat wiring; Task 10 Step 5 verifies.
- PDF-only entry (auto-shown for PDF, not for others) → Task 4 (`SourceType != "pdf"` guard) + Task 7 (button in `isPDF` branch).
- Drop "回原文" anchor (v1) → not present in any task. ✓
- Manual-trigger entry → tab button in Task 7 (consistent with "PDF auto-shows the entry, user clicks to open").

**Placeholder scan:** No TBD/TODO/"add appropriate error handling". Every code step contains complete code. The one conditional instruction (Task 8 Step 2, "verify exact English values") is a verification guard, not a placeholder.

**Type consistency:**
- `Section{Index,Title,Slug,body}` / `Body()` — defined Task 1, used Task 3 (not directly) and Task 4 (`s.Index/s.Title/s.Slug`). `body` accessed only via `Body()` in tests; handler does not touch `body`. ✓
- `sectionDTO{Index,Title,Slug,Explanation}` — defined Task 4, matches `PaperSection{index,title,slug,explanation?}` in Task 5. ✓
- `GenerateSectionExplain(userDir, paperRelPath, sectionTitle, claudeBin)` — defined Task 3, called in Task 4 with `(userDir, paperRelPath, section.Title, h.ClaudeBin)`. ✓
- `fetchPaperSections` returns `{sections, paperMdExists}` — defined Task 5, consumed in Task 6 (`const { sections, paperMdExists } = ...`). ✓
- `PaperSectionsView` props `{ docId, summary, onAskPaper }` — declared Task 6, supplied in Task 7 (`docId={document.id} summary={document.summary} onAskPaper={() => { setPanelHidden(false); setMetadataTab('chat') }}`). `onAskPaper` switches to the existing Chat tab without modifying `DocumentChatPanel` — doc-chat + Save fully preserved (see design §5). ✓
- `generatePaperSection(docId, index)` returns `PaperSection` — defined Task 5, consumed in Task 6. ✓
- i18n key `paperSections.askPaper` — added Task 8 (zh + en), used in Task 6 ask button. ✓
- Routes `GET /documents/:id/sections` + `POST /documents/:id/sections/:index/generate` — registered Task 4, called Task 5, exercised Task 9. ✓
- `data-testid` IDs: `paper-sections-tab` (Task 7) + `paper-sections-content` / `paper-sections-empty` (Task 6) — match selectors in Task 9. ✓

No issues found.
