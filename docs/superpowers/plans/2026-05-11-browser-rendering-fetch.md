# Browser Rendering Fetch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add headless Chrome rendering via go-rod so URLs that require JavaScript (WeChat, SPA sites) return full content instead of empty shells or verification pages.

**Architecture:** A new `browser` package wraps go-rod with a connection pool (lazy init, idle shutdown, concurrency limit). `web.go` gains a domain-based routing table (`browserSites`) that sends known JS-dependent sites through the browser path instead of curl. The existing `uploadWeChat` is replaced by the generic `uploadBrowser` handler.

**Tech Stack:** Go, go-rod, goquery (existing)

**Spec:** `docs/superpowers/specs/2026-05-11-browser-rendering-fetch-design.md`

---

### Task 1: Add go-rod dependency

**Files:**
- Modify: `backend/go.mod`

- [ ] **Step 1: Add go-rod to go.mod**

```bash
cd backend && go get github.com/go-rod/rod@latest
```

- [ ] **Step 2: Tidy modules**

```bash
cd backend && go mod tidy
```

- [ ] **Step 3: Verify it compiles**

```bash
cd backend && go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 4: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "deps: add go-rod for headless browser rendering"
```

---

### Task 2: Create `browser` package — Pool and FetchRenderedHTML

**Files:**
- Create: `backend/browser/pool.go`

- [ ] **Step 1: Create `backend/browser/pool.go`**

```go
package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

type RenderOpts struct {
	WaitSelector string
	Timeout      time.Duration
}

type Pool struct {
	mu             sync.Mutex
	browser        *rod.Browser
	lastUsed       time.Time
	sem            chan struct{}
	maxConcurrency int
	idleTimeout    time.Duration
	stopIdle       chan struct{}
}

func NewPool(maxConcurrency int) *Pool {
	p := &Pool{
		sem:            make(chan struct{}, maxConcurrency),
		maxConcurrency: maxConcurrency,
		idleTimeout:    5 * time.Minute,
		stopIdle:       make(chan struct{}),
	}
	go p.idleReaper()
	return p
}

func (p *Pool) ensureBrowser() (*rod.Browser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.browser != nil {
		return p.browser, nil
	}

	u, err := launcher.New().Headless(true).Launch()
	if err != nil {
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connect to chromium: %w", err)
	}

	p.browser = b
	p.lastUsed = time.Now()
	return b, nil
}

func (p *Pool) FetchRenderedHTML(url string, opts RenderOpts) (string, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Acquire semaphore
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return "", fmt.Errorf("timeout waiting for browser slot")
	}

	b, err := p.ensureBrowser()
	if err != nil {
		return "", err
	}

	page, err := b.Page(rod.PageOptions{})
	if err != nil {
		return "", fmt.Errorf("create page: %w", err)
	}
	defer page.Close()

	page = page.Context(ctx)

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("navigate to %s: %w", url, err)
	}

	if err := page.WaitLoad(); err != nil {
		return "", fmt.Errorf("wait page load: %w", err)
	}

	if opts.WaitSelector != "" {
		_, err := page.Element(opts.WaitSelector)
		if err != nil {
			return "", fmt.Errorf("wait for selector %q: %w", opts.WaitSelector, err)
		}
	}

	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("get HTML: %w", err)
	}

	p.mu.Lock()
	p.lastUsed = time.Now()
	p.mu.Unlock()

	return html, nil
}

func (p *Pool) idleReaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			if p.browser != nil && time.Since(p.lastUsed) > p.idleTimeout {
				p.browser.Close()
				p.browser = nil
			}
			p.mu.Unlock()
		case <-p.stopIdle:
			return
		}
	}
}

func (p *Pool) Close() {
	close(p.stopIdle)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.browser != nil {
		p.browser.Close()
		p.browser = nil
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd backend && go build ./browser/...
```

Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add backend/browser/pool.go
git commit -m "feat: add browser pool with go-rod for headless rendering"
```

---

### Task 3: Write tests for `browser.Pool`

**Files:**
- Create: `backend/browser/pool_test.go`

- [ ] **Step 1: Write `pool_test.go`**

```go
package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRenderedHTML_StaticPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div id="content">Hello from test server</div></body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	html, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#content",
		Timeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("FetchRenderedHTML failed: %v", err)
	}
	if !strings.Contains(html, "Hello from test server") {
		t.Errorf("Expected HTML to contain test content, got: %s", html[:200])
	}
}

func TestFetchRenderedHTML_JSRendered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<div id="app"></div>
			<script>
				document.getElementById('app').innerHTML = '<p id="dynamic">JS rendered content</p>';
			</script>
		</body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	html, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#dynamic",
		Timeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("FetchRenderedHTML failed: %v", err)
	}
	if !strings.Contains(html, "JS rendered content") {
		t.Errorf("Expected JS-rendered content in HTML, got: %s", html[:200])
	}
}

func TestFetchRenderedHTML_TimeoutOnMissingSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div>No matching selector here</div></body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	_, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#nonexistent",
		Timeout:      3 * time.Second,
	})
	if err == nil {
		t.Fatal("Expected error for missing selector, got nil")
	}
}

func TestPool_Close(t *testing.T) {
	pool := NewPool(2)
	pool.Close()
	// Should not panic on double close
}
```

- [ ] **Step 2: Run the tests**

```bash
cd backend && go test ./browser/ -v -timeout 60s
```

Expected: all 4 tests pass. `TestFetchRenderedHTML_TimeoutOnMissingSelector` should fail with a context/timeout error (not panic).

- [ ] **Step 3: Commit**

```bash
git add backend/browser/pool_test.go
git commit -m "test: add browser pool unit tests"
```

---

### Task 4: Add `browserSites` config and `needsBrowser` to `web.go`

**Files:**
- Modify: `backend/api/web.go:27-30` (WebHandler struct)
- Modify: `backend/api/web.go:63-77` (near WeChat config area)

- [ ] **Step 1: Add `browser` import to `web.go`**

Add `"llm-knowledge/browser"` to the import block at the top of `backend/api/web.go`.

- [ ] **Step 2: Add `BrowserPool` field to `WebHandler`**

Change `WebHandler` (line 27-30) from:

```go
type WebHandler struct {
	DataDir   string
	ClaudeBin string
}
```

to:

```go
type WebHandler struct {
	DataDir     string
	ClaudeBin   string
	BrowserPool *browser.Pool
}
```

- [ ] **Step 3: Add `browserSiteConfig` type and `browserSites` map**

Insert after the `isWeChatURL` function (after line 77):

```go
type browserSiteConfig struct {
	WaitSelector  string
	Postprocess   func(doc *goquery.Document)
	ExtractAuthor func(doc *goquery.Document) string
	FetchMethod   string
	ImageHeaders  func(imgURL string) map[string]string
}

var browserSites = map[string]browserSiteConfig{
	"mp.weixin.qq.com": {
		WaitSelector:  "#js_content",
		Postprocess:   preprocessWeChatImages,
		ExtractAuthor: extractWeChatAuthor,
		FetchMethod:   "wechat",
		ImageHeaders: func(imgURL string) map[string]string {
			if strings.Contains(imgURL, "mmbiz.qpic.cn") {
				return weChatHeaders
			}
			return nil
		},
	},
	"www.bestblogs.dev": {
		WaitSelector: "article",
		FetchMethod:  "browser",
	},
}

func needsBrowser(urlStr string) (browserSiteConfig, bool) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return browserSiteConfig{}, false
	}
	cfg, ok := browserSites[strings.ToLower(u.Host)]
	return cfg, ok
}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd backend && go build ./...
```

Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add backend/api/web.go
git commit -m "feat: add browserSites config and needsBrowser routing"
```

---

### Task 5: Add `uploadBrowser` handler and update `UploadWeb` routing

**Files:**
- Modify: `backend/api/web.go` (UploadWeb function at line 866, add uploadBrowser)

- [ ] **Step 1: Add `uploadBrowser` method**

Insert before the `UploadWeb` function:

```go
func (h *WebHandler) uploadBrowser(c echo.Context, req WebUploadRequest, cfg browserSiteConfig) error {
	if h.BrowserPool == nil {
		return c.JSON(500, echo.Map{"error": "browser rendering not available"})
	}

	renderedHTML, err := h.BrowserPool.FetchRenderedHTML(req.URL, browser.RenderOpts{
		WaitSelector: cfg.WaitSelector,
		Timeout:      30 * time.Second,
	})
	if err != nil {
		log.Printf("[browser] rendering failed for %s: %v", req.URL, err)
		return c.JSON(500, echo.Map{"error": "browser rendering failed: " + err.Error()})
	}

	doc, err := ParseHTML(renderedHTML)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to parse rendered HTML"})
	}

	if cfg.Postprocess != nil {
		cfg.Postprocess(doc)
	}

	originalTitle := extractTitle(doc)
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	author := ""
	if cfg.ExtractAuthor != nil {
		author = cfg.ExtractAuthor(doc)
	}

	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	content := ExtractContent(doc)

	if cfg.ImageHeaders == nil {
		cfg.ImageHeaders = func(imgURL string) map[string]string { return nil }
	}

	fetchMethod := cfg.FetchMethod
	if fetchMethod == "" {
		fetchMethod = "browser"
	}

	return h.saveWebDocument(c, req, originalTitle, doc, publishedTime, content, webSaveConfig{
		Author:       author,
		FetchMethod:  fetchMethod,
		Language:     detectLanguage(content),
		ImageHeaders: cfg.ImageHeaders,
		SuccessMsg:   "Page saved successfully (browser rendered)",
		Metadata: map[string]string{
			"published":    publishedTime.Format(time.RFC3339),
			"author":       author,
			"fetch_method": fetchMethod,
		},
	})
}
```

- [ ] **Step 2: Update `UploadWeb` routing**

In the `UploadWeb` function, replace the WeChat block (lines 882-886):

```go
	// Check if this is a WeChat URL — needs special handling
	// because WeChat blocks bare HTTP GET and uses data-src for images
	if isWeChatURL(req.URL) {
		return h.uploadWeChat(c, req)
	}
```

with:

```go
	if cfg, ok := needsBrowser(req.URL); ok {
		return h.uploadBrowser(c, req, cfg)
	}
```

- [ ] **Step 3: Delete the `uploadWeChat` function**

Remove the entire `uploadWeChat` function (lines 1257-1310). The `weChatHeaders` variable, `isWeChatURL`, `preprocessWeChatImages`, and `extractWeChatAuthor` functions must remain — they are still referenced from `browserSites`.

- [ ] **Step 4: Verify it compiles**

```bash
cd backend && go build ./...
```

Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add backend/api/web.go
git commit -m "feat: add uploadBrowser handler, replace uploadWeChat"
```

---

### Task 6: Initialize `browser.Pool` in `main.go`

**Files:**
- Modify: `backend/main.go:138-141`

- [ ] **Step 1: Add browser import**

Add `"llm-knowledge/browser"` to the import block in `backend/main.go`.

- [ ] **Step 2: Create and wire the pool**

Change the WebHandler initialization (lines 138-141) from:

```go
	webH := &api.WebHandler{
		DataDir:   cfg.DataDir,
		ClaudeBin: cfg.ClaudeBin,
	}
```

to:

```go
	browserPool := browser.NewPool(2)
	defer browserPool.Close()

	webH := &api.WebHandler{
		DataDir:     cfg.DataDir,
		ClaudeBin:   cfg.ClaudeBin,
		BrowserPool: browserPool,
	}
```

- [ ] **Step 3: Verify it compiles**

```bash
cd backend && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add backend/main.go
git commit -m "feat: initialize browser pool in main.go"
```

---

### Task 7: Update `web_test.go` for new structure

**Files:**
- Modify: `backend/api/web_test.go`

- [ ] **Step 1: Add `TestNeedsBrowser`**

Append to `backend/api/web_test.go`:

```go
func TestNeedsBrowser(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
		method string
	}{
		{"https://mp.weixin.qq.com/s/some-article", true, "wechat"},
		{"https://mp.weixin.qq.com/s?__biz=MzI2&mid=123", true, "wechat"},
		{"https://www.bestblogs.dev/article/abc123", true, "browser"},
		{"https://go.dev/blog/some-article", false, ""},
		{"https://x.com/user/status/123", false, ""},
		{"https://example.com", false, ""},
		{"not-a-url", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			cfg, ok := needsBrowser(tt.url)
			if ok != tt.expect {
				t.Errorf("needsBrowser(%q) = %v, want %v", tt.url, ok, tt.expect)
			}
			if ok && cfg.FetchMethod != tt.method {
				t.Errorf("needsBrowser(%q).FetchMethod = %q, want %q", tt.url, cfg.FetchMethod, tt.method)
			}
		})
	}
}
```

- [ ] **Step 2: Fix any `WebHandler` literal compilation errors in existing tests**

Existing tests like `TestWebHandlerExists` (line 17) create `WebHandler{}` without the new `BrowserPool` field. Since it's a pointer field, zero value is `nil`, so no changes needed — but verify:

```bash
cd backend && go test ./api/ -run TestWebHandlerExists -v
```

Expected: PASS.

- [ ] **Step 3: Run all web tests**

```bash
cd backend && go test ./api/ -v -timeout 120s
```

Expected: all existing tests pass, plus the new `TestNeedsBrowser`.

- [ ] **Step 4: Commit**

```bash
git add backend/api/web_test.go
git commit -m "test: add needsBrowser tests, verify existing tests pass"
```

---

### Task 8: Manual end-to-end verification

- [ ] **Step 1: Start the dev server**

```bash
cd backend && go run .
```

- [ ] **Step 2: Test a WeChat article URL**

Use the app's web clipping UI (or curl the API) to submit a WeChat article URL. Verify:
- The response is successful (not a verification page error).
- The saved document contains the article title and body text.
- Images from `mmbiz.qpic.cn` are downloaded with correct Referer headers.

- [ ] **Step 3: Test a bestblogs.dev article URL**

Submit `https://www.bestblogs.dev/article/2adc9c3e`. Verify:
- The response is successful.
- The saved document contains the article title ("一文搞懂 Hermes") and body text.

- [ ] **Step 4: Test a normal URL (regression)**

Submit a normal URL like `https://go.dev/blog/type-construction-and-cycle-detection`. Verify:
- It still works via the curl path (no browser involved).

- [ ] **Step 5: Final commit if any fixes needed**

If manual testing reveals issues, fix and commit.
