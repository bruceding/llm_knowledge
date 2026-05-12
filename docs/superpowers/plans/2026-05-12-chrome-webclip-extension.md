# Chrome Web Clip Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Chrome extension that clips any web page to the wiki system with one toolbar click, plus the backend API to receive pushed HTML content.

**Architecture:** Chrome Manifest V3 extension extracts DOM client-side, pushes to new `POST /api/raw/web-clip` endpoint. Backend parses HTML with goquery, runs existing `saveWebDocument` pipeline (dedup, image download, markdown, DB, summary). Auth via existing login flow with captcha skip for extension clients.

**Tech Stack:** Pure JS Chrome Extension (Manifest V3), Go backend (Echo, goquery, GORM)

---

## File Structure

### Backend (modify existing)
- `backend/api/web.go` — Add `WebClipRequest` struct and `ClipWeb` method (new endpoint reusing `saveWebDocument`)
- `backend/api/auth.go` — Add `ClientType` field to `LoginRequest`, skip captcha when `"extension"`
- `backend/main.go` — Register `/raw/web-clip` route with 10M body limit

### Backend (new tests)
- `backend/api/web_test.go` — Add `TestClipWeb*` tests
- `backend/api/auth_test.go` — Add `TestLoginExtensionSkipsCaptcha` (unit test for struct)

### Extension (all new)
- `extension/manifest.json` — Manifest V3 config
- `extension/background.js` — Service worker: click handler, API calls
- `extension/content.js` — DOM extraction, toast display
- `extension/options.html` — Settings page markup
- `extension/options.js` — Settings page logic
- `extension/options.css` — Settings page styles
- `extension/icons/` — Extension icons (16, 48, 128px)

---

## Phase 1: Backend

### Task 1: Add `ClipWeb` Endpoint

**Files:**
- Modify: `backend/api/web.go:1125` (after `UploadWeb`, before `webSaveConfig`)
- Modify: `backend/api/web_test.go`

- [ ] **Step 1: Write the failing test**

Add to `backend/api/web_test.go`:

```go
func TestClipWebParseAndExtract(t *testing.T) {
	html := `<html><head><title>Test Article</title>
	<meta property="article:published_time" content="2026-05-01"/>
	</head><body>
	<nav>Navigation</nav>
	<article><h1>Test Article</h1><p>Hello world.</p>
	<img src="https://example.com/img.png"/></article>
	<footer>Footer</footer></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("ParseHTML failed: %v", err)
	}

	title := extractTitle(doc)
	if title != "Test Article" {
		t.Errorf("Expected title 'Test Article', got %q", title)
	}

	content := ExtractContent(doc)
	if !strings.Contains(content, "Hello world") {
		t.Errorf("Expected content to contain 'Hello world', got %q", content)
	}
	if strings.Contains(content, "Navigation") {
		t.Errorf("Expected nav to be stripped from content")
	}

	pubTime := extractPublishedTime(doc)
	if pubTime.IsZero() {
		t.Error("Expected published time to be extracted")
	}
}
```

- [ ] **Step 2: Run test to verify it passes (this tests existing functions)**

Run: `cd backend && go test ./api -v -run TestClipWebParseAndExtract`
Expected: PASS (all functions already exist)

- [ ] **Step 3: Write the `ClipWeb` method**

Add to `backend/api/web.go` after the `UploadWeb` function (before the `webSaveConfig` struct at line 1127):

```go
type WebClipRequest struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

func (h *WebHandler) ClipWeb(c echo.Context) error {
	var req WebClipRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.HTML == "" {
		return c.JSON(400, echo.Map{"error": "html is required"})
	}
	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "url is required"})
	}

	doc, err := ParseHTML(req.HTML)
	if err != nil {
		return c.JSON(400, echo.Map{"error": "failed to parse HTML"})
	}

	// Apply platform-specific postprocessing (e.g. WeChat data-src → src)
	if cfg, ok := needsBrowser(req.URL); ok && cfg.Postprocess != nil {
		cfg.Postprocess(doc)
	}

	originalTitle := req.Title
	if originalTitle == "" {
		originalTitle = extractTitle(doc)
	}
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	content := ExtractContent(doc)

	// Determine image headers from platform config
	imageHeaders := func(imgURL string) map[string]string { return nil }
	fetchMethod := "extension"
	if cfg, ok := needsBrowser(req.URL); ok && cfg.ImageHeaders != nil {
		imageHeaders = cfg.ImageHeaders
		fetchMethod = "extension-" + cfg.FetchMethod
	}

	uploadReq := WebUploadRequest{URL: req.URL}
	return h.saveWebDocument(c, uploadReq, originalTitle, doc, publishedTime, content, webSaveConfig{
		FetchMethod:  fetchMethod,
		Language:     detectLanguage(content),
		ImageHeaders: imageHeaders,
		SuccessMsg:   "Web page clipped successfully",
		Metadata: map[string]string{
			"published":    publishedTime.Format(time.RFC3339),
			"fetch_method": fetchMethod,
		},
	})
}
```

- [ ] **Step 4: Write a unit test for ClipWeb validation**

Add to `backend/api/web_test.go`:

```go
func TestClipWebValidation(t *testing.T) {
	tests := []struct {
		name string
		req  WebClipRequest
		err  string
	}{
		{"empty html", WebClipRequest{URL: "https://example.com", HTML: ""}, "html is required"},
		{"empty url", WebClipRequest{URL: "", HTML: "<html></html>"}, "url is required"},
		{"valid", WebClipRequest{URL: "https://example.com", Title: "Test", HTML: "<html><body>Hello</body></html>"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err != "" {
				if tt.req.HTML == "" && tt.err == "html is required" {
					// Validation check
				}
				if tt.req.URL == "" && tt.err == "url is required" {
					// Validation check
				}
			}
		})
	}
}

func TestClipWebWeChatPostprocess(t *testing.T) {
	// Verify WeChat data-src preprocessing is applied
	html := `<html><body><div id="js_content">
	<img data-src="https://mmbiz.qpic.cn/test.png" src=""/>
	<p>WeChat article content</p>
	</div></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	// Before postprocess: img src is empty
	src, _ := doc.Find("img").Attr("src")
	if src != "" {
		t.Errorf("Before postprocess, src should be empty, got %q", src)
	}

	// Apply WeChat postprocessing
	preprocessWeChatImages(doc)

	// After postprocess: img src should have the data-src value
	src, _ = doc.Find("img").Attr("src")
	if src != "https://mmbiz.qpic.cn/test.png" {
		t.Errorf("After postprocess, src should be data-src value, got %q", src)
	}
}
```

- [ ] **Step 5: Run tests**

Run: `cd backend && go test ./api -v -run "TestClipWeb"`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/api/web.go backend/api/web_test.go
git commit -m "feat: add ClipWeb endpoint for Chrome extension web clipping"
```

---

### Task 2: Register Route in main.go

**Files:**
- Modify: `backend/main.go:147` (after existing `apiGroup.POST("/raw/web", ...)`)

- [ ] **Step 1: Add route registration**

Add after line 147 in `backend/main.go` (`apiGroup.POST("/raw/web", webH.UploadWeb)`):

```go
	apiGroup.POST("/raw/web-clip", webH.ClipWeb, middleware.BodyLimit("10M"))
```

- [ ] **Step 2: Verify build**

Run: `cd backend && go build ./...`
Expected: Build succeeds with no errors

- [ ] **Step 3: Commit**

```bash
git add backend/main.go
git commit -m "feat: register /raw/web-clip route with 10M body limit"
```

---

### Task 3: Skip Captcha for Extension Login

**Files:**
- Modify: `backend/api/auth.go:168-182`
- Test: `backend/api/auth_test.go`

- [ ] **Step 1: Write the failing test**

Add to `backend/api/auth_test.go`:

```go
func TestLoginRequestClientType(t *testing.T) {
	req := LoginRequest{
		Username:   "testuser",
		Password:   "test123",
		ClientType: "extension",
	}
	if req.ClientType != "extension" {
		t.Errorf("Expected ClientType 'extension', got %q", req.ClientType)
	}

	// When ClientType is "extension", captcha fields can be empty
	if req.CaptchaKey != "" || req.CaptchaAnswer != "" {
		t.Error("Extension login should not require captcha fields")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./api -v -run TestLoginRequestClientType`
Expected: FAIL with `unknown field ClientType`

- [ ] **Step 3: Add ClientType to LoginRequest and update Login handler**

Modify `backend/api/auth.go` — update the `LoginRequest` struct (line 168):

```go
type LoginRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaKey    string `json:"captchaKey"`
	CaptchaAnswer string `json:"captchaAnswer"`
	ClientType    string `json:"clientType"`
}
```

Modify the `Login` function (line 175) — replace the captcha validation block (lines 181-184):

```go
func (h *AuthHandler) Login(c echo.Context) error {
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "无效的请求体"})
	}

	// Skip captcha for extension clients
	if req.ClientType != "extension" {
		if !verifyCaptcha(req.CaptchaKey, req.CaptchaAnswer) {
			return c.JSON(400, echo.Map{"error": "验证码错误或已过期"})
		}
	}

	// Find user (rest of function unchanged)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./api -v -run TestLoginRequestClientType`
Expected: PASS

- [ ] **Step 5: Run all auth tests**

Run: `cd backend && go test ./api -v -run "TestValidate|TestLogin"`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add backend/api/auth.go backend/api/auth_test.go
git commit -m "feat: skip captcha for extension client login"
```

---

## Phase 2: Extension Skeleton

### Task 4: Create Manifest and Directory Structure

**Files:**
- Create: `extension/manifest.json`
- Create: `extension/icons/` (placeholder icons)

- [ ] **Step 1: Create extension directory**

```bash
mkdir -p extension/icons
```

- [ ] **Step 2: Create manifest.json**

Create `extension/manifest.json`:

```json
{
  "manifest_version": 3,
  "name": "Wiki Web Clipper",
  "version": "1.0.0",
  "description": "Clip web pages to your wiki with one click",
  "permissions": ["storage", "activeTab", "scripting"],
  "action": {
    "default_icon": {
      "16": "icons/icon16.png",
      "48": "icons/icon48.png",
      "128": "icons/icon128.png"
    },
    "default_title": "Clip to Wiki"
  },
  "background": {
    "service_worker": "background.js"
  },
  "options_page": "options.html",
  "icons": {
    "16": "icons/icon16.png",
    "48": "icons/icon48.png",
    "128": "icons/icon128.png"
  }
}
```

- [ ] **Step 3: Create placeholder icons**

Generate simple SVG-based PNG icons. For now, create a simple script to generate them, or use placeholder files:

```bash
# Create minimal 1x1 placeholder PNGs (replace with real icons later)
# Using base64-encoded minimal PNG data
python3 -c "
import base64, struct, zlib
def make_png(size):
    # Simple green square PNG
    raw = b''
    for y in range(size):
        raw += b'\x00'  # filter byte
        for x in range(size):
            raw += b'\x4c\xaf\x50\xff'  # green RGBA
    
    def chunk(ctype, data):
        c = ctype + data
        return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)
    
    ihdr = struct.pack('>IIBBBBB', size, size, 8, 6, 0, 0, 0)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b'')

for s in [16, 48, 128]:
    with open(f'extension/icons/icon{s}.png', 'wb') as f:
        f.write(make_png(s))
print('Icons created')
"
```

- [ ] **Step 4: Commit**

```bash
git add extension/manifest.json extension/icons/
git commit -m "feat: add Chrome extension manifest and placeholder icons"
```

---

### Task 5: Create Background Service Worker

**Files:**
- Create: `extension/background.js`

- [ ] **Step 1: Create background.js**

Create `extension/background.js`:

```js
// Handle toolbar icon click
chrome.action.onClicked.addListener(async (tab) => {
  const config = await chrome.storage.local.get(['wikiUrl', 'token']);

  if (!config.wikiUrl || !config.token) {
    chrome.runtime.openOptionsPage();
    return;
  }

  // Set badge to show clipping in progress
  chrome.action.setBadgeText({ text: '...', tabId: tab.id });
  chrome.action.setBadgeBackgroundColor({ color: '#888888', tabId: tab.id });

  try {
    // Inject content script and extract page content
    const results = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      files: ['content.js']
    });

    // Send extract message to content script
    const response = await chrome.tabs.sendMessage(tab.id, { action: 'extractContent' });

    if (!response || !response.html) {
      throw new Error('Failed to extract page content');
    }

    // Send to wiki API
    const apiResponse = await fetch(`${config.wikiUrl}/api/raw/web-clip`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${config.token}`
      },
      body: JSON.stringify({
        url: response.url,
        title: response.title,
        html: response.html
      })
    });

    if (apiResponse.status === 401) {
      // Token expired
      await chrome.storage.local.remove('token');
      chrome.action.setBadgeText({ text: '!', tabId: tab.id });
      chrome.action.setBadgeBackgroundColor({ color: '#EF4444', tabId: tab.id });
      chrome.tabs.sendMessage(tab.id, {
        action: 'showToast',
        success: false,
        message: '登录已过期，请重新设置'
      });
      setTimeout(() => chrome.action.setBadgeText({ text: '', tabId: tab.id }), 3000);
      return;
    }

    const data = await apiResponse.json();

    if (!apiResponse.ok) {
      throw new Error(data.error || 'Clip failed');
    }

    // Success
    chrome.action.setBadgeText({ text: '✓', tabId: tab.id });
    chrome.action.setBadgeBackgroundColor({ color: '#22C55E', tabId: tab.id });
    chrome.tabs.sendMessage(tab.id, {
      action: 'showToast',
      success: true,
      message: '已收藏'
    });
  } catch (err) {
    // Failure
    chrome.action.setBadgeText({ text: '✗', tabId: tab.id });
    chrome.action.setBadgeBackgroundColor({ color: '#EF4444', tabId: tab.id });
    chrome.tabs.sendMessage(tab.id, {
      action: 'showToast',
      success: false,
      message: err.message || '收藏失败'
    });
  }

  // Clear badge after 3 seconds
  setTimeout(() => {
    chrome.action.setBadgeText({ text: '', tabId: tab.id });
  }, 3000);
});
```

- [ ] **Step 2: Commit**

```bash
git add extension/background.js
git commit -m "feat: add background service worker for clip handling"
```

---

### Task 6: Create Content Script

**Files:**
- Create: `extension/content.js`

- [ ] **Step 1: Create content.js**

Create `extension/content.js`:

```js
// Selectors for non-content elements to remove before clipping
const REMOVE_SELECTORS = [
  'script', 'style', 'iframe', 'noscript',
  'nav', 'header', 'footer',
  '.ads', '.ad', '.advertisement', '.adsbygoogle',
  '.sidebar', '.navigation', '.menu',
  '.cookie-notice', '.cookie-banner', '.Cookie-notice',
  '[role="banner"]', '[role="navigation"]', '[role="complementary"]'
].join(', ');

// Extract cleaned HTML from the page
function extractContent() {
  const clone = document.documentElement.cloneNode(true);

  // Remove non-content elements
  clone.querySelectorAll(REMOVE_SELECTORS).forEach(function(el) {
    el.remove();
  });

  return {
    url: location.href,
    title: document.title,
    html: clone.outerHTML
  };
}

// Show a toast notification at the bottom of the page
function showToast(success, message) {
  // Remove any existing toast
  var existing = document.getElementById('wiki-clipper-toast');
  if (existing) {
    existing.remove();
  }

  var toast = document.createElement('div');
  toast.id = 'wiki-clipper-toast';
  toast.setAttribute('style', [
    'all: initial',
    'position: fixed',
    'bottom: 24px',
    'left: 50%',
    'transform: translateX(-50%)',
    'background: #1a1a1a',
    'color: #ffffff',
    'font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
    'font-size: 14px',
    'padding: 12px 24px',
    'border-radius: 8px',
    'z-index: 2147483647',
    'display: flex',
    'align-items: center',
    'gap: 8px',
    'box-shadow: 0 4px 12px rgba(0,0,0,0.3)',
    'opacity: 1',
    'transition: opacity 0.3s ease'
  ].join('; '));

  var icon = success ? '✅' : '❌';
  toast.textContent = icon + ' ' + message;

  document.body.appendChild(toast);

  // Fade out and remove after 2.5 seconds
  setTimeout(function() {
    toast.style.opacity = '0';
    setTimeout(function() {
      toast.remove();
    }, 300);
  }, 2500);
}

// Listen for messages from background script
chrome.runtime.onMessage.addListener(function(msg, sender, sendResponse) {
  if (msg.action === 'extractContent') {
    sendResponse(extractContent());
  } else if (msg.action === 'showToast') {
    showToast(msg.success, msg.message);
  }
  return true;
});
```

- [ ] **Step 2: Commit**

```bash
git add extension/content.js
git commit -m "feat: add content script with DOM extraction and toast"
```

---

## Phase 3: Options Page

### Task 7: Create Options Page HTML and CSS

**Files:**
- Create: `extension/options.html`
- Create: `extension/options.css`

- [ ] **Step 1: Create options.html**

Create `extension/options.html`:

```html
<!DOCTYPE html>
<html lang="zh">
<head>
  <meta charset="UTF-8">
  <title>Wiki Web Clipper 设置</title>
  <link rel="stylesheet" href="options.css">
</head>
<body>
  <div class="container">
    <h1>Wiki Web Clipper</h1>

    <div class="form-group">
      <label for="wikiUrl">Wiki 地址</label>
      <input type="url" id="wikiUrl" placeholder="https://wiki.bruceding.me">
    </div>

    <div class="form-group">
      <label for="username">用户名</label>
      <input type="text" id="username" placeholder="用户名">
    </div>

    <div class="form-group">
      <label for="password">密码</label>
      <input type="password" id="password" placeholder="密码">
    </div>

    <button id="saveBtn">保存并连接</button>

    <div id="status" class="status"></div>
  </div>

  <script src="options.js"></script>
</body>
</html>
```

- [ ] **Step 2: Create options.css**

Create `extension/options.css`:

```css
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  background: #f5f5f5;
  min-height: 100vh;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 40px 20px;
}

.container {
  background: #fff;
  border-radius: 12px;
  padding: 32px;
  width: 100%;
  max-width: 400px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

h1 {
  font-size: 20px;
  font-weight: 600;
  margin-bottom: 24px;
  color: #1a1a1a;
}

.form-group {
  margin-bottom: 16px;
}

label {
  display: block;
  font-size: 14px;
  font-weight: 500;
  color: #333;
  margin-bottom: 6px;
}

input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #ddd;
  border-radius: 8px;
  font-size: 14px;
  outline: none;
  transition: border-color 0.2s;
}

input:focus {
  border-color: #4c8bf5;
}

button {
  width: 100%;
  padding: 12px;
  background: #4c8bf5;
  color: #fff;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  margin-top: 8px;
  transition: background 0.2s;
}

button:hover {
  background: #3a7ae0;
}

button:disabled {
  background: #a0c4ff;
  cursor: not-allowed;
}

.status {
  margin-top: 16px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 13px;
  display: none;
}

.status.success {
  display: block;
  background: #f0fdf4;
  color: #166534;
  border: 1px solid #bbf7d0;
}

.status.error {
  display: block;
  background: #fef2f2;
  color: #991b1b;
  border: 1px solid #fecaca;
}
```

- [ ] **Step 3: Commit**

```bash
git add extension/options.html extension/options.css
git commit -m "feat: add options page HTML and CSS"
```

---

### Task 8: Create Options Page Logic

**Files:**
- Create: `extension/options.js`

- [ ] **Step 1: Create options.js**

Create `extension/options.js`:

```js
var wikiUrlInput = document.getElementById('wikiUrl');
var usernameInput = document.getElementById('username');
var passwordInput = document.getElementById('password');
var saveBtn = document.getElementById('saveBtn');
var statusDiv = document.getElementById('status');

// Load saved wiki URL on page open
chrome.storage.local.get(['wikiUrl', 'token'], function(config) {
  if (config.wikiUrl) {
    wikiUrlInput.value = config.wikiUrl;
  }
  if (config.token) {
    showStatus('success', '已连接');
  }
});

saveBtn.addEventListener('click', async function() {
  var wikiUrl = wikiUrlInput.value.trim().replace(/\/+$/, '');
  var username = usernameInput.value.trim();
  var password = passwordInput.value;

  // Validate
  if (!wikiUrl) {
    showStatus('error', '请输入 Wiki 地址');
    return;
  }
  if (!username) {
    showStatus('error', '请输入用户名');
    return;
  }
  if (!password) {
    showStatus('error', '请输入密码');
    return;
  }

  // Validate URL format
  try {
    new URL(wikiUrl);
  } catch (e) {
    showStatus('error', '请输入有效的 URL 地址');
    return;
  }

  saveBtn.disabled = true;
  saveBtn.textContent = '连接中...';
  statusDiv.className = 'status';
  statusDiv.style.display = 'none';

  try {
    // Login with extension client type (skips captcha)
    var loginRes = await fetch(wikiUrl + '/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: username,
        password: password,
        clientType: 'extension'
      })
    });

    var loginData = await loginRes.json();

    if (!loginRes.ok) {
      throw new Error(loginData.error || '登录失败');
    }

    // Save wiki URL and token (NOT password)
    await chrome.storage.local.set({
      wikiUrl: wikiUrl,
      token: loginData.token
    });

    // Clear password field
    passwordInput.value = '';

    showStatus('success', '连接成功');
  } catch (err) {
    showStatus('error', err.message || '连接失败');
  } finally {
    saveBtn.disabled = false;
    saveBtn.textContent = '保存并连接';
  }
});

function showStatus(type, message) {
  statusDiv.className = 'status ' + type;
  statusDiv.textContent = message;
  statusDiv.style.display = 'block';
}
```

- [ ] **Step 2: Commit**

```bash
git add extension/options.js
git commit -m "feat: add options page login logic"
```

---

## Phase 4: Integration Testing

### Task 9: Manual End-to-End Test

- [ ] **Step 1: Start the backend**

```bash
cd backend && go run main.go
```

- [ ] **Step 2: Load extension in Chrome**

1. Open `chrome://extensions/`
2. Enable "Developer mode" (top right toggle)
3. Click "Load unpacked"
4. Select the `extension/` directory
5. Verify the extension icon appears in the toolbar

- [ ] **Step 3: Configure the extension**

1. Right-click extension icon → "Options" (or click extension → gear icon)
2. Enter wiki URL: `http://localhost:8080` (or the deployed URL)
3. Enter username and password
4. Click "保存并连接"
5. Verify: status shows "连接成功"

- [ ] **Step 4: Test on a normal blog post**

1. Open `https://go.dev/blog/type-construction-and-cycle-detection` in a new tab
2. Click the extension icon
3. Verify: toast shows "已收藏" at bottom of page
4. Verify: badge briefly shows green ✓
5. Open wiki inbox and verify document appears with correct title

- [ ] **Step 5: Test on a WeChat article**

1. Open a WeChat article (`mp.weixin.qq.com`) in the browser
2. Click the extension icon
3. Verify: toast shows "已收藏"
4. Open wiki inbox and verify:
   - Document appears with correct title
   - Images are downloaded (WeChat mmbiz.qpic.cn images localized)
   - data-src images were correctly processed

- [ ] **Step 6: Test duplicate clip**

1. Click the extension icon on the same page again
2. Verify: toast shows "已收藏" (returns existing document, no duplicate)

- [ ] **Step 7: Test error cases**

1. Remove token from storage (`chrome.storage.local.remove('token')`)
2. Click extension icon
3. Verify: toast shows error about expired login
4. Test with a chrome:// page or extension page
5. Verify: graceful error (not a crash)

- [ ] **Step 8: Commit any fixes from testing**

```bash
git add -A
git commit -m "fix: address issues found during integration testing"
```

---

## Notes

- Extension icons are placeholder green squares. Replace with proper designed icons when available.
- The `content.js` is injected via `chrome.scripting.executeScript` on each click, which works with `activeTab` permission — no need for broad host permissions.
- Body limit is 10MB for the web-clip endpoint. Typical HTML payloads (after lightweight cleanup) are 500KB-2MB.
- The extension does NOT store the password — only the wiki URL and auth token. Token expires after 7 days (matching the backend session config).
