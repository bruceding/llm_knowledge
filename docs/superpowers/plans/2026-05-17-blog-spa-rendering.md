# Blog Feed SPA 渲染 + Markdown 提取 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 issue #54 — blog feed 抓取支持 SPA 渲染，文章内容输出为 Markdown(.md) 并下载图片到 assets/。

**Architecture:**
- `backend/blog/extract.go` 引入 `Fetcher` 结构持有 `*browser.Pool`，提供 `FetchIndex` / `FetchArticle` 方法。先用 `http.Client` 抓取，检测到 SPA 空壳（`<div id="__next">` / `<div id="root">` 且 selector 匹配 0 个）则 fallback 到 browser pool，`WaitStable: 2s`。
- `FetchArticle` 不再返回纯文本，而是返回选中内容节点的 inner HTML + 发布时间 + 标题。
- `backend/api/blog.go` 在 sync 时复用 `processHTMLToMarkdown` 处理 HTML → Markdown 并下载图片到 `assets/`，文件后缀改为 `.md`。
- `BlogHandler` 新增 `BrowserPool *browser.Pool` 字段，由 main.go 注入。

**Tech Stack:** Go, go-rod (已有), goquery (已有), html-to-markdown (通过 api 包内部 `processHTMLToMarkdown` 复用)。

---

## File Structure

**Create:**
- `backend/blog/fetcher.go` — `Fetcher` 结构，封装 http + browser pool fallback，SPA 检测
- `backend/blog/fetcher_test.go` — Fetcher 单元测试（SPA 检测、http→browser fallback 切换）

**Modify:**
- `backend/blog/extract.go` — 把 `FetchIndexPage` 和 `FetchArticleContent` 改为薄封装，调用 `Fetcher`；`FetchArticleContent` 改返回 `(contentHTML string, publishedAt time.Time, title string, err error)`
- `backend/blog/blog_test.go` — 更新调用 `FetchArticleContent` 的预期签名（如有）
- `backend/api/blog.go` — `BlogHandler` 加 `BrowserPool`；`syncFeedInternal` 调用 `Fetcher`，用 `processHTMLToMarkdown` 转换并写 `.md`；`AddFeed` 也走 Fetcher
- `backend/main.go` — 把 `browserPool` 传给 `BlogHandler`

**Delete (after migration):**
- 无（保留旧 API 函数，最后一个任务删除未用代码）

---

## Task 1: Add SPA detection helper

**Files:**
- Create: `backend/blog/fetcher.go`
- Test: `backend/blog/fetcher_test.go`

- [ ] **Step 1: Write failing test for SPA detection**

`backend/blog/fetcher_test.go`:
```go
package blog

import "testing"

func TestIsSPAShell(t *testing.T) {
	tests := []struct {
		name      string
		html      string
		selector  string
		wantSPA   bool
	}{
		{
			name:     "next.js empty shell, no selector matches",
			html:     `<html><body><div id="__next"></div><script src="/_next/x.js"></script></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  true,
		},
		{
			name:     "react root empty shell",
			html:     `<html><body><div id="root"></div></body></html>`,
			selector: "article",
			wantSPA:  true,
		},
		{
			name:     "static HTML with content matches selector",
			html:     `<html><body><a href="/blog/foo">foo</a></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
		{
			name:     "next.js shell but selector matches inside",
			html:     `<html><body><div id="__next"><a href="/blog/foo">foo</a></div></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
		{
			name:     "no shell marker, no selector match (treat as non-SPA)",
			html:     `<html><body><p>nothing here</p></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSPAShell(tt.html, tt.selector)
			if got != tt.wantSPA {
				t.Errorf("IsSPAShell() = %v, want %v", got, tt.wantSPA)
			}
		})
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `cd backend && go test ./blog/ -run TestIsSPAShell -v`
Expected: FAIL with `undefined: IsSPAShell`

- [ ] **Step 3: Implement IsSPAShell**

`backend/blog/fetcher.go`:
```go
package blog

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// IsSPAShell returns true when the HTML looks like an unhydrated SPA shell:
// it contains a known SPA root (#__next, #root, #app) and the user-provided
// selector matches zero elements. When the selector already matches, the page
// is treated as already-rendered (CSR or SSR) regardless of root markers.
func IsSPAShell(html, selector string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return false
	}

	// If selector matches anything, content is already in DOM — not a shell.
	if selector != "" && doc.Find(selector).Length() > 0 {
		return false
	}

	shellSelectors := []string{"#__next", "#root", "#app"}
	for _, sel := range shellSelectors {
		if doc.Find(sel).Length() > 0 {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test, verify it passes**

Run: `cd backend && go test ./blog/ -run TestIsSPAShell -v`
Expected: PASS (5 sub-cases)

- [ ] **Step 5: Commit**

```bash
git add backend/blog/fetcher.go backend/blog/fetcher_test.go
git commit -m "feat(blog): add SPA shell detector"
```

---

## Task 2: Add Fetcher with http + browser fallback

**Files:**
- Modify: `backend/blog/fetcher.go`
- Modify: `backend/blog/fetcher_test.go`

- [ ] **Step 1: Write failing test for Fetcher.fetchHTTP**

Append to `backend/blog/fetcher_test.go`:
```go
import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetcher_FetchHTTPOnly_NonSPA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><a href="/blog/foo">foo</a></body></html>`))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil} // nil pool: must not be used for non-SPA
	html, usedBrowser, err := f.fetchWithFallback(srv.URL, "a[href^='/blog/']")
	if err != nil {
		t.Fatalf("fetchWithFallback: %v", err)
	}
	if usedBrowser {
		t.Error("expected http path, got browser")
	}
	if !contains(html, "/blog/foo") {
		t.Errorf("expected http body, got %q", html)
	}
}
```

(`contains` already exists in blog_test.go — same package.)

- [ ] **Step 2: Run test, verify it fails**

Run: `cd backend && go test ./blog/ -run TestFetcher_FetchHTTPOnly -v`
Expected: FAIL with `undefined: Fetcher`

- [ ] **Step 3: Implement Fetcher with fallback**

Append to `backend/blog/fetcher.go`:
```go
import (
	"fmt"
	"io"
	"llm-knowledge/browser"
	"llm-knowledge/ssrf"
	"net/http"
	"time"
)

type Fetcher struct {
	Pool *browser.Pool
}

// fetchWithFallback fetches indexURL via http.Client. If the response is
// detected as an SPA shell (and a browser pool is available), it re-fetches
// using browser rendering with WaitStable 2s. The selector is the link or
// content selector used to decide whether the static HTML is sufficient.
//
// Returns (html, usedBrowser, error). usedBrowser is true when the rendered
// HTML came from the browser pool.
func (f *Fetcher) fetchWithFallback(targetURL, selector string) (string, bool, error) {
	if err := ssrf.ValidateURLHost(targetURL); err != nil {
		return "", false, err
	}

	httpHTML, httpErr := fetchHTTP(targetURL)
	httpOK := httpErr == nil

	if httpOK && !IsSPAShell(httpHTML, selector) {
		return httpHTML, false, nil
	}

	// Need browser. If pool unavailable, return whatever http gave us
	// (may be empty or a shell — caller will fail to extract).
	if f.Pool == nil {
		if httpOK {
			return httpHTML, false, nil
		}
		return "", false, httpErr
	}

	rendered, rerr := f.Pool.FetchRenderedHTML(targetURL, browser.RenderOpts{
		WaitStable: 2 * time.Second,
		Timeout:    30 * time.Second,
	})
	if rerr != nil {
		if httpOK {
			// Browser failed but we have http content — return it as a degraded fallback.
			return httpHTML, false, nil
		}
		return "", false, fmt.Errorf("browser render: %w", rerr)
	}
	return rendered, true, nil
}

func fetchHTTP(targetURL string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
```

Note: there will be duplicate import blocks after this edit; merge them into a single `import (...)` block at the top of `fetcher.go`.

- [ ] **Step 4: Run test, verify it passes**

Run: `cd backend && go test ./blog/ -run TestFetcher_FetchHTTPOnly -v`
Expected: PASS

- [ ] **Step 5: Add a test for SPA-shell-without-pool returning shell HTML**

Append to `fetcher_test.go`:
```go
func TestFetcher_NoPool_SPAShellPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="__next"></div></body></html>`))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil}
	html, usedBrowser, err := f.fetchWithFallback(srv.URL, "a[href^='/blog/']")
	if err != nil {
		t.Fatalf("fetchWithFallback: %v", err)
	}
	if usedBrowser {
		t.Error("expected http path (pool nil), got browser")
	}
	if !contains(html, "__next") {
		t.Errorf("expected shell HTML, got %q", html)
	}
}
```

- [ ] **Step 6: Run test, verify it passes**

Run: `cd backend && go test ./blog/ -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add backend/blog/fetcher.go backend/blog/fetcher_test.go
git commit -m "feat(blog): add Fetcher with browser pool fallback for SPA pages"
```

---

## Task 3: Add Fetcher methods FetchIndex and FetchArticle

**Files:**
- Modify: `backend/blog/fetcher.go`
- Modify: `backend/blog/extract.go`
- Modify: `backend/blog/fetcher_test.go`

**Goal:** expose two Fetcher methods used by the api layer.
- `FetchIndex(indexURL, linkSelector string) (html string, err error)` — returns rendered HTML.
- `FetchArticle(articleURL, contentSelector string) (contentHTML string, publishedAt time.Time, title string, err error)` — returns inner HTML of the matched content node, plus title (`<h1>`) and published time. The caller will convert HTML → Markdown.

- [ ] **Step 1: Write failing test for FetchArticle returning inner HTML**

Append to `backend/blog/fetcher_test.go`:
```go
import "time"

func TestFetcher_FetchArticle_ReturnsInnerHTML(t *testing.T) {
	body := `<html><head>
<meta property="article:published_time" content="2025-01-15T10:00:00Z">
</head><body>
<h1>Hello World</h1>
<article><p>Para <strong>one</strong>.</p><p>Para two.</p></article>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil}
	html, pub, title, err := f.FetchArticle(srv.URL, "article")
	if err != nil {
		t.Fatalf("FetchArticle: %v", err)
	}
	if title != "Hello World" {
		t.Errorf("title = %q, want %q", title, "Hello World")
	}
	if !contains(html, "<strong>one</strong>") {
		t.Errorf("expected inner HTML preserved, got %q", html)
	}
	want := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	if !pub.Equal(want) {
		t.Errorf("pub = %v, want %v", pub, want)
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `cd backend && go test ./blog/ -run TestFetcher_FetchArticle -v`
Expected: FAIL with `f.FetchArticle undefined`

- [ ] **Step 3: Implement FetchIndex and FetchArticle**

Append to `backend/blog/fetcher.go`:
```go
import (
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// FetchIndex fetches the index page HTML (with browser fallback for SPA).
// linkSelector is the user-configured link selector; passing it allows the
// SPA detector to recognise pages that already contain the links in static HTML.
func (f *Fetcher) FetchIndex(indexURL, linkSelector string) (string, error) {
	html, _, err := f.fetchWithFallback(indexURL, linkSelector)
	return html, err
}

// FetchArticle fetches an article and returns the inner HTML of the matched
// content node, the published time (from meta tags or <time>), and the page
// title (from <h1>). Browser fallback is triggered when the static HTML is
// an SPA shell. Falls back to common selectors (article, main, .content,
// .post-content) if contentSelector matches nothing.
func (f *Fetcher) FetchArticle(articleURL, contentSelector string) (string, time.Time, string, error) {
	html, _, err := f.fetchWithFallback(articleURL, contentSelector)
	if err != nil {
		return "", time.Time{}, "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", time.Time{}, "", err
	}

	contentNode := doc.Find(contentSelector)
	if contentNode.Length() == 0 {
		fallbacks := []string{"article", "main", ".content", ".post-content"}
		for _, sel := range fallbacks {
			if doc.Find(sel).Length() > 0 {
				contentNode = doc.Find(sel).First()
				break
			}
		}
	}

	contentHTML := ""
	if contentNode.Length() > 0 {
		inner, herr := contentNode.First().Html()
		if herr == nil {
			contentHTML = inner
		}
	}

	title := strings.TrimSpace(doc.Find("h1").First().Text())
	publishedTime := extractPublishedTime(doc)

	return contentHTML, publishedTime, title, nil
}
```

(Merge import blocks into one at top of fetcher.go.)

- [ ] **Step 4: Run test, verify it passes**

Run: `cd backend && go test ./blog/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add backend/blog/fetcher.go backend/blog/fetcher_test.go
git commit -m "feat(blog): add Fetcher.FetchIndex and Fetcher.FetchArticle"
```

---

## Task 4: Wire BrowserPool into BlogHandler

**Files:**
- Modify: `backend/api/blog.go`
- Modify: `backend/main.go`

- [ ] **Step 1: Add BrowserPool to BlogHandler struct**

In `backend/api/blog.go`, change the struct (lines 19-22):

```go
import (
	// ...existing...
	"llm-knowledge/browser"
)

type BlogHandler struct {
	DataDir     string
	ClaudeBin   string
	BrowserPool *browser.Pool
}
```

- [ ] **Step 2: Wire pool in main.go**

In `backend/main.go` around line 358, change:
```go
blogH := &api.BlogHandler{
	DataDir:   cfg.DataDir,
	ClaudeBin: cfg.ClaudeBin,
}
```
to:
```go
blogH := &api.BlogHandler{
	DataDir:     cfg.DataDir,
	ClaudeBin:   cfg.ClaudeBin,
	BrowserPool: browserPool,
}
```

- [ ] **Step 3: Build to confirm wiring compiles**

Run: `cd backend && go build ./...`
Expected: builds clean (Fetcher not yet used by handler — that's Task 5).

- [ ] **Step 4: Commit**

```bash
git add backend/api/blog.go backend/main.go
git commit -m "feat(blog): inject BrowserPool into BlogHandler"
```

---

## Task 5: Switch syncFeedInternal to Fetcher + Markdown + .md output

**Files:**
- Modify: `backend/api/blog.go`

- [ ] **Step 1: Replace AddFeed's index fetch to use Fetcher**

In `backend/api/blog.go` `AddFeed`, replace:
```go
htmlContent, err := blog.FetchIndexPage(req.IndexURL)
```
with:
```go
fetcher := &blog.Fetcher{Pool: h.BrowserPool}
// AddFeed has no link selector yet (selector chosen by DetectPlatform after fetch),
// so SPA detector falls back to root-marker check only.
htmlContent, err := fetcher.FetchIndex(req.IndexURL, "")
```

- [ ] **Step 2: Rewrite syncFeedInternal — fetch index via Fetcher**

In `syncFeedInternal` (around line 230), replace:
```go
htmlContent, err := blog.FetchIndexPage(feed.IndexURL)
```
with:
```go
fetcher := &blog.Fetcher{Pool: h.BrowserPool}
htmlContent, err := fetcher.FetchIndex(feed.IndexURL, feed.LinkSelector)
```

- [ ] **Step 3: Rewrite article fetch + write loop to produce Markdown .md files**

In `syncFeedInternal`, replace the per-link loop body (the section starting at the `// Fetch article content` comment, lines ~283-345) with:

```go
		// Fetch article content (HTML inner of content node + metadata)
		contentHTML, articleDate, h1Title, err := fetcher.FetchArticle(link.URL, feed.ContentSelector)
		if err != nil {
			downloadErrors++
			continue
		}

		// For subsequent syncs: skip articles older than lastArticleDate
		if !isFirstSync && !articleDate.IsZero() && !articleDate.After(feed.LastArticleDate) {
			continue
		}

		// Determine title: prefer link title, fall back to <h1>
		articleTitle := link.Title
		if articleTitle == "" {
			articleTitle = h1Title
		}

		// Convert HTML → Markdown, downloading images into assetsDir
		markdownBody, _, _ := processHTMLToMarkdown(contentHTML, assetsDir, link.URL)

		// Final markdown: heading + metadata + body
		var sb strings.Builder
		sb.WriteString("# " + articleTitle + "\n\n")
		sb.WriteString("**Source:** " + feed.Name + "\n")
		sb.WriteString("**Link:** " + link.URL + "\n")
		if !articleDate.IsZero() {
			sb.WriteString("**Published:** " + articleDate.Format(time.RFC3339) + "\n")
		}
		sb.WriteString("\n## Content\n\n")
		sb.WriteString(markdownBody)
		sb.WriteString("\n")
		content := sb.String()

		// Save article
		safeTitle := sanitizeFilename(articleTitle)
		if safeTitle == "" {
			safeTitle = fmt.Sprintf("article-%d", time.Now().Unix())
		}

		userIdStr := strconv.FormatUint(uint64(feed.UserID), 10)
		rawPath := filepath.Join("users", userIdStr, "raw", "blog", sanitizeFilename(feed.Name), safeTitle+".md")

		// Resolve raw_path collision (same logic as before, .md suffix)
		var collisionCount int64
		db.DB.Unscoped().Model(&db.Document{}).
			Where("raw_path = ? AND source_url != ?", rawPath, link.URL).
			Count(&collisionCount)
		if collisionCount > 0 {
			safeTitle = fmt.Sprintf("%s-%d", safeTitle, collisionCount+1)
			rawPath = filepath.Join("users", userIdStr, "raw", "blog", sanitizeFilename(feed.Name), safeTitle+".md")
		}

		fullPath := filepath.Join(h.DataDir, rawPath)

		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			downloadErrors++
			continue
		}

		doc := db.Document{
			Title:      articleTitle,
			Slug:       safeTitle,
			SourceType: "blog",
			RawPath:    rawPath,
			SourceURL:  link.URL,
			UserID:     feed.UserID,
			BlogFeedID: feed.ID,
			Status:     "inbox",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		if !articleDate.IsZero() {
			doc.CreatedAt = articleDate
		}

		if err := db.DB.Create(&doc).Error; err != nil {
			downloadErrors++
			os.Remove(fullPath)
			continue
		}

		newArticles++

		if !articleDate.IsZero() && articleDate.After(maxDate) {
			maxDate = articleDate
		}
```

- [ ] **Step 4: Build to confirm compile**

Run: `cd backend && go build ./...`
Expected: clean build.

- [ ] **Step 5: Run all backend tests**

Run: `cd backend && go test ./...`
Expected: PASS (existing tests should not regress; blog tests still pass).

- [ ] **Step 6: Commit**

```bash
git add backend/api/blog.go
git commit -m "feat(blog): output markdown with images, render SPA pages via browser pool

Closes #54"
```

---

## Task 6: Remove now-unused blog.FetchIndexPage / FetchArticleContent

**Files:**
- Modify: `backend/blog/extract.go`
- Modify: `backend/blog/blog_test.go`

- [ ] **Step 1: Confirm no callers remain**

Run: `cd backend && grep -rn "blog.FetchIndexPage\|blog.FetchArticleContent" --include="*.go"`
Expected: no matches outside `backend/blog/extract.go` itself.

- [ ] **Step 2: Delete `FetchIndexPage` and `FetchArticleContent` from extract.go**

Keep: `ArticleLink`, `ExtractArticleLinks`, `extractPublishedTime`, `parseWebDate`. Remove `FetchIndexPage` (lines 20-42) and `FetchArticleContent` (lines 105-156). Also drop now-unused imports (`io`, `net/http`, `time` if not referenced elsewhere — check after edit).

- [ ] **Step 3: Update / remove TestFetchIndexPage**

Remove `TestFetchIndexPage` from `backend/blog/blog_test.go` (lines 58-77) and the now-unused `contains` helper IF nothing else in the test file uses it. Note: `fetcher_test.go` uses `contains`, so keep it.

- [ ] **Step 4: Run all backend tests**

Run: `cd backend && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/blog/extract.go backend/blog/blog_test.go
git commit -m "refactor(blog): remove obsolete http-only fetch helpers"
```

---

## Task 7: Manual end-to-end verification

- [ ] **Step 1: Start the backend (already started? check)**

Run: `./start.sh` (or whatever the project uses; if backend is already running via the dev workflow, restart it to pick up the new binary).

- [ ] **Step 2: Add `https://claude.com/blog` as a feed via the UI**

Open the frontend, add the blog feed. Confirm auto-detection finds the `claude` platform.

- [ ] **Step 3: Sync the feed**

Click sync. Wait for completion.

- [ ] **Step 4: Verify the result**

```bash
ls -la <DATA_DIR>/users/<your-user-id>/raw/blog/claude.com/
```

Expected:
- One or more `.md` files (NOT `.txt`)
- An `assets/` subdirectory containing downloaded images
- Each `.md` opens with `# <Title>` followed by metadata, then `## Content` with Markdown formatting (headings, lists, links, images referenced as `assets/xxx`)

- [ ] **Step 5: Spot-check a known SPA site**

If `claude.com/blog` works, also try one obvious static site (e.g., a Hugo/Jekyll blog) to confirm the http path still works without unnecessary browser invocation. Watch backend logs for any `[browser]` messages — they should appear only on SPA targets.

- [ ] **Step 6: Document the verification in the issue and close**

Comment on issue #54 summarising:
- Sample feed URL tested
- File suffix change (.txt → .md)
- Image count downloaded for one article

```bash
gh issue comment 54 --body "Verified fix: synced https://claude.com/blog, got N markdown files with images in assets/. Closing via PR."
```

Then push branch and open PR. Issue auto-closes via the "Closes #54" trailer in Task 5's commit.

---

## Self-Review Notes

- **Spec coverage:**
  - Decision 1B (fallback): Task 2 fetchWithFallback. ✓
  - Decision 2 (markdown + images, .md output): Task 5 uses processHTMLToMarkdown + .md suffix. ✓
  - Decision 3 (skip existing data): no migration step — confirmed. ✓
  - Decision 4 (WaitStable 2s): Task 2 fetchWithFallback uses `WaitStable: 2 * time.Second`. ✓
- **Cross-package access:** `processHTMLToMarkdown` lives in package `api` and is unexported, but `syncFeedInternal` is in the same package, so direct call is fine. No exporting needed.
- **Pool nil safety:** Fetcher handles `Pool == nil` by passing through HTTP result so unit tests can run without browser.
- **SSRF:** validated in `fetchWithFallback`. Image SSRF reused via `processHTMLToMarkdown` → `downloadImageToAssets`.
- **Existing collision logic preserved:** the rewrite in Task 5 keeps `db.Unscoped()` collision check on `.md` paths.
