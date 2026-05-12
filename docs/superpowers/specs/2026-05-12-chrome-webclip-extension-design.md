# Chrome Web Clip Extension Design

**Date:** 2026-05-12

## Overview

A Chrome extension that allows users to clip any web page to the wiki system (e.g. wiki.bruceding.me) with a single toolbar click. The extension extracts the page's DOM on the client side and pushes it to the backend, bypassing authentication/captcha issues on platforms like WeChat.

## Goals

- One-click save of any web page from Chrome toolbar to the wiki system
- Client-side DOM extraction to bypass platform restrictions (WeChat captcha, login walls)
- Reuse the existing backend content processing pipeline (goquery, image download, markdown generation)
- Toast notification feedback (success/failure) similar to Readwise Highlighter

## Non-Goals

- Highlighting or annotation features
- Popup UI with category/tag selection before saving
- Offline support or queued uploads
- Chrome Web Store publishing (manual install for now)

## Architecture

### Chrome Extension (Manifest V3, pure JS)

```
extension/
├── manifest.json          # Manifest V3 config
├── icons/
│   ├── icon16.png
│   ├── icon48.png
│   └── icon128.png
├── background.js          # Service Worker: handle click, call API
├── content.js             # Content Script: extract DOM, show toast
├── options.html           # Settings page: wiki URL + credentials
├── options.js
└── options.css
```

### Interaction Flow

```
User clicks toolbar icon
  → chrome.action.onClicked (background.js)
  → Send message to content.js: "extractContent"
  → content.js: clone DOM, lightweight cleanup, return {url, title, html}
  → background.js: POST /api/raw/web-clip with Bearer token
  → On response: send message to content.js: "showToast"
  → content.js: inject toast element, auto-remove after 2.5s
```

### Data Flow

```
Chrome Extension                          Backend
─────────────                          ───────
content.js                             web.go
  ├─ clone DOM                         ClipWeb()
  ├─ remove: script, style,             ├─ receive HTML string
  │   nav, header, footer,              ├─ goquery parse
  │   ads, sidebar, iframe              ├─ needsBrowser() → platform postprocess
  ├─ outerHTML                           │   (e.g. preprocessWeChatImages)
  └─ send to background.js              ├─ extractTitle()
       │                                 ├─ saveWebDocument()
       │  POST /api/raw/web-clip         │   ├─ dedup check
       │  {url, title, html}             │   ├─ image download + localize
       └─────────────────────────────►   │   ├─ markdown generation
                                         │   ├─ DB record (status=inbox)
                                         │   └─ async summary generation
                                         └─ return {id, title, images, message}
```

## Components

### 1. Content Script (content.js)

Listens for messages from background.js. Two responsibilities:

**DOM Extraction:**
- Clone `document` to avoid mutating the live page
- Remove non-content elements from the clone:
  ```
  script, style, iframe, noscript, nav, header, footer,
  .ads, .ad, .sidebar, .navigation, .menu, .cookie-notice,
  [role="banner"], [role="navigation"], [role="complementary"]
  ```
- Return `{ url: location.href, title: document.title, html: clone.documentElement.outerHTML }`

**Toast Display:**
- Inject a fixed-position element at bottom-center of page
- Black background, white text, rounded corners
- Success: green checkmark + "已收藏"
- Failure: red X + error message
- Auto fade-out after 2.5 seconds
- Use `all: initial` on container to prevent page CSS interference
- z-index: 2147483647 (max)

### 2. Background Service Worker (background.js)

Handles `chrome.action.onClicked` event:

1. Read config from `chrome.storage.local` (wiki URL, token)
2. If no config → open options page
3. Send `extractContent` message to active tab's content script
4. Receive extracted data
5. POST to `{wikiUrl}/api/raw/web-clip` with Bearer token
6. If 401 → badge shows error, user must re-login in options
7. On success/failure → send `showToast` message to content script
8. Update badge briefly (green check or red X)

### 3. Options Page (options.html + options.js)

Simple form with three fields:
- **Wiki URL**: text input (e.g. `https://wiki.bruceding.me`)
- **Username**: text input
- **Password**: password input

**Save flow:**
1. Validate URL format
2. Call `POST {wikiUrl}/api/auth/login` with `{ username, password, clientType: "extension" }` (no captcha needed)
3. On success: store `{ wikiUrl, token }` in `chrome.storage.local` (password NOT stored)
4. Show "连接成功" confirmation
5. On failure: show error, don't save

**Token refresh:** Not automatic. If token expires (401), user must re-open options and login again.

### 4. Backend: New API Endpoint

**File:** `backend/api/web.go`

**Endpoint:** `POST /api/raw/web-clip` (protected, requires auth)

**Request body:**
```json
{
  "url": "https://mp.weixin.qq.com/s/xxx",
  "title": "Article Title",
  "html": "<html>...</html>"
}
```

**Response (200):**
```json
{
  "id": 123,
  "title": "Article Title",
  "url": "https://mp.weixin.qq.com/s/xxx",
  "images": 5,
  "message": "Web page clipped successfully"
}
```

**Implementation (`ClipWeb` method on `WebHandler`):**
1. Parse `html` field with `goquery.NewDocumentFromReader`
2. Check `needsBrowser(url)` for platform-specific postprocessing (e.g. `preprocessWeChatImages` for WeChat)
3. Use client-provided `title` if non-empty, otherwise call `extractTitle(doc)`
4. Call `extractPublishedTime(doc)` for publication date
5. Call `ExtractContent(doc)` for markdown content
6. Determine `ImageHeaders` from platform config (e.g. WeChat Referer for mmbiz.qpic.cn)
7. Call existing `saveWebDocument()` with appropriate `webSaveConfig`

**Route registration in `main.go`:**
```go
apiGroup.POST("/raw/web-clip", webH.ClipWeb, middleware.BodyLimit("10M"))
```

**Body limit:** 10MB to handle large pages with inline content. Images are URLs (not base64), so typical payloads are 500KB-2MB.

### 5. Backend: Auth Change

**File:** `backend/api/auth.go`

Modify the `Login` handler to accept optional `clientType` field:
```go
type LoginRequest struct {
    Username      string `json:"username"`
    Password      string `json:"password"`
    CaptchaKey    string `json:"captchaKey"`
    CaptchaAnswer string `json:"captchaAnswer"`
    ClientType    string `json:"clientType"` // "extension" skips captcha
}
```

When `clientType == "extension"`, skip the captcha validation step. All other validation (username, password hash check, session creation) remains the same.

### 6. CORS Configuration

The extension makes cross-origin requests from `chrome-extension://<id>` to `wiki.bruceding.me`. The backend already uses `middleware.CORS()` with default settings (allows all origins). No CORS changes needed.

## Testing Strategy

### Backend Tests

- `TestClipWebBasic`: POST HTML string, verify document created with correct title/url/status
- `TestClipWebWeChat`: POST WeChat HTML with data-src images, verify preprocessWeChatImages runs and images are downloaded
- `TestClipWebDedup`: POST same URL twice, verify second returns existing document
- `TestClipWebEmptyHTML`: POST with empty html field, verify 400 error
- `TestLoginExtensionSkipCaptcha`: Login with `clientType: "extension"`, verify no captcha needed

### Extension Manual Testing

- Install unpacked extension in Chrome
- Configure wiki URL + credentials in options
- Test on: normal blog post, WeChat article, X/Twitter page
- Verify: toast shows, document appears in wiki inbox
- Test edge cases: no config set, wrong password, network error, duplicate clip

## Implementation Phases

1. **Phase 1: Backend** — Add `ClipWeb` endpoint + auth `clientType` skip
2. **Phase 2: Extension skeleton** — manifest.json, background.js, content.js (extract + toast)
3. **Phase 3: Options page** — settings UI, login flow, token storage
4. **Phase 4: Integration + Polish** — end-to-end testing, error handling, icon states
