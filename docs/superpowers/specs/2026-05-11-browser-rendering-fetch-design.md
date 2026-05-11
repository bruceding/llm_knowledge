# Browser Rendering Fetch Design

## Problem

Two categories of URLs fail with the current curl-based fetching:

1. **WeChat articles** (`mp.weixin.qq.com`): WeChat upgraded anti-scraping — the `MicroMessenger` User-Agent trick no longer works. Curl returns a verification page (`secitptpage/verify`) instead of the article content.
2. **SPA sites** (e.g. `bestblogs.dev`): Content is rendered client-side via JavaScript (Next.js RSC). Curl gets only the shell HTML with meta tags; the article body is loaded dynamically.

Both require a real browser environment to render JavaScript and obtain the full page content.

## Solution

Add a `browser` package using **go-rod** to render pages in a headless Chrome instance. Integrate it into `web.go` as a new fetch path alongside the existing curl and fxtwitter paths.

## Architecture

```
UploadWeb(url)
  ├── isXTwitterURL?    → uploadXTwitter()     (fxtwitter API, unchanged)
  ├── needsBrowser?     → uploadBrowser()       (NEW)
  │     └── browser.Pool.FetchRenderedHTML(url, opts)
  │           └── go-rod: new tab → navigate → wait for selector → return HTML
  │     └── ParseHTML → extractTitle → ExtractContent → saveWebDocument
  └── default           → fetchHTML() + curl    (unchanged)
```

### Trigger: `needsBrowser(url)`

A domain-based lookup in `web.go`. Known sites that require browser rendering:

| Domain | Wait Selector | Post-processing |
|--------|--------------|-----------------|
| `mp.weixin.qq.com` | `#js_content` | `preprocessWeChatImages`, `extractWeChatAuthor` |
| `www.bestblogs.dev` | `article, main, #main-content` | none |

New domains can be added by appending to the config table. The existing `isWeChatURL` check in `UploadWeb` will be replaced by the more general `needsBrowser` check.

## `browser` Package

Location: `backend/browser/`

### `Pool`

Manages a single shared headless Chrome instance.

```go
type Pool struct {
    mu       sync.Mutex
    browser  *rod.Browser
    lastUsed time.Time
    sem      chan struct{} // concurrency limiter
}

func NewPool(maxConcurrency int) *Pool
func (p *Pool) FetchRenderedHTML(url string, opts RenderOpts) (string, error)
func (p *Pool) Close()
```

Behavior:
- **Lazy init**: Chrome is not started until the first `FetchRenderedHTML` call.
- **Idle shutdown**: A background goroutine checks every minute; if `lastUsed` is older than 5 minutes, it closes the browser. The next call will re-launch it.
- **Concurrency limit**: A semaphore (buffered channel) limits concurrent tabs to `maxConcurrency` (default: 2). Requests beyond the limit block until a slot opens or the context times out.
- **go-rod launcher**: Uses `launcher.New().Headless(true).MustLaunch()` which auto-downloads Chromium if not present.

### `RenderOpts`

```go
type RenderOpts struct {
    WaitSelector string        // CSS selector to wait for before extracting HTML (e.g. "#js_content")
    Timeout      time.Duration // per-page timeout, default 30s
}
```

### `FetchRenderedHTML` Flow

1. Acquire semaphore slot (or block/timeout).
2. Ensure browser is running (lazy init).
3. Open a new tab (`browser.MustPage()`).
4. Navigate to URL.
5. Wait for `WaitSelector` to appear (or timeout).
6. Extract full rendered HTML via `page.HTML()`.
7. Close the tab.
8. Update `lastUsed` timestamp.
9. Release semaphore slot.
10. Return HTML string.

Error handling: if navigation or wait times out, return an error. The caller (`uploadBrowser`) translates this to an HTTP 500 with a descriptive message.

## `web.go` Changes

### New: `browserSiteConfig` and `needsBrowser`

```go
type browserSiteConfig struct {
    WaitSelector   string
    Postprocess    func(doc *goquery.Document)
    ExtractAuthor  func(doc *goquery.Document) string
    FetchMethod    string
    ImageHeaders   func(imgURL string) map[string]string
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
    u, _ := url.Parse(urlStr)
    cfg, ok := browserSites[strings.ToLower(u.Host)]
    return cfg, ok
}
```

### New: `uploadBrowser`

```go
func (h *WebHandler) uploadBrowser(c echo.Context, req WebUploadRequest, cfg browserSiteConfig) error {
    html, err := h.browserPool.FetchRenderedHTML(req.URL, browser.RenderOpts{
        WaitSelector: cfg.WaitSelector,
        Timeout:      30 * time.Second,
    })
    if err != nil {
        return c.JSON(500, echo.Map{"error": "browser rendering failed: " + err.Error()})
    }

    doc, err := ParseHTML(html)
    if err != nil {
        return c.JSON(500, echo.Map{"error": "failed to parse rendered HTML"})
    }

    if cfg.Postprocess != nil {
        cfg.Postprocess(doc)
    }

    title := extractTitle(doc)
    if title == "" {
        title = "untitled"
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

    return h.saveWebDocument(c, req, title, doc, publishedTime, content, webSaveConfig{
        Author:       author,
        FetchMethod:  fetchMethod,
        Language:     detectLanguage(content),
        ImageHeaders: cfg.ImageHeaders, // nil-safe: uploadBrowser defaults nil to no-op below
        SuccessMsg:   "Page saved successfully (browser rendered)",
        Metadata: map[string]string{
            "published":    publishedTime.Format(time.RFC3339),
            "author":       author,
            "fetch_method": fetchMethod,
        },
    })
}
```

### Modified: `UploadWeb` routing

```go
func (h *WebHandler) UploadWeb(c echo.Context) error {
    // ... bind & validate ...

    if isXTwitterURL(req.URL) {
        return h.uploadXTwitter(c, req)
    }

    if cfg, ok := needsBrowser(req.URL); ok {
        return h.uploadBrowser(c, req, cfg)
    }

    // ... existing curl path ...
}
```

### Removed: `uploadWeChat`

The standalone `uploadWeChat` function is removed. WeChat is now handled by `uploadBrowser` with the WeChat-specific config in `browserSites`. The `weChatHeaders`, `preprocessWeChatImages`, and `extractWeChatAuthor` functions remain unchanged — they are referenced from the config.

### `WebHandler` gets a `browserPool` field

```go
type WebHandler struct {
    DataDir     string
    ClaudeBin   string
    BrowserPool *browser.Pool
}
```

Initialized in `main.go`:

```go
bp := browser.NewPool(2)
defer bp.Close()
webHandler := &api.WebHandler{DataDir: dataDir, ClaudeBin: claudeBin, BrowserPool: bp}
```

## Resource Constraints

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Max concurrent tabs | 2 | Prevents memory spikes; web scraping is infrequent |
| Per-page timeout | 30s | Generous for slow pages; prevents hung tabs |
| Idle browser shutdown | 5 min | Free resources when not scraping |
| Chrome mode | Headless | No GUI needed |

Approximate per-request cost: ~100-200MB RAM, 2-5s wall time. Chrome is only alive when there are recent scraping requests.

## Dependencies

- `github.com/go-rod/rod` — headless Chrome control via DevTools Protocol
- Chromium binary — auto-downloaded by go-rod's launcher on first use (~150MB, cached in `~/.cache/rod/`)

## Files Changed

| File | Change |
|------|--------|
| `backend/browser/pool.go` | **New** — `Pool`, `RenderOpts`, `FetchRenderedHTML` |
| `backend/api/web.go` | Add `browserSites` config, `needsBrowser`, `uploadBrowser`; remove `uploadWeChat`; add `BrowserPool` to `WebHandler` |
| `backend/main.go` | Initialize `browser.Pool`, pass to `WebHandler` |
| `backend/go.mod` | Add `github.com/go-rod/rod` dependency |

## Testing

- Unit test `needsBrowser` with known and unknown domains.
- Integration test: call `FetchRenderedHTML` on a known static page to verify go-rod works.
- Manual test: upload a WeChat article URL and a bestblogs URL through the UI, verify content is extracted correctly.
