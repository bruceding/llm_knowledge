# Web Clipping & RSS Import Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add web clipping and RSS subscription features with unified publish workflow.

**Architecture:** HTTP fetch + goquery parsing + Claude CLI extraction → Markdown with local images → Document DB record → Publish triggers wiki ingest.

**Tech Stack:** Go net/http, goquery, Claude CLI, React frontend, SQLite DB

---

## Phase 1: Web Clipping Core

### Task 1: Add SourceURL Field to Document Model

**Files:**
- Modify: `backend/db/models.go:5-17`
- Test: `backend/db/db_test.go` (create if needed)

**Step 1: Write the failing test**

Create `backend/db/db_test.go`:

```go
package db

import (
	"testing"
	"time"
)

func TestDocumentSourceURL(t *testing.T) {
	doc := Document{
		Title:      "Test",
		SourceType: "web",
		SourceURL:  "https://example.com/article",
		Status:     "inbox",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if doc.SourceURL != "https://example.com/article" {
		t.Errorf("Expected SourceURL to be set, got %s", doc.SourceURL)
	}
	if doc.SourceType != "web" {
		t.Errorf("Expected SourceType to be web, got %s", doc.SourceType)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./db -v`
Expected: FAIL with "Document.SourceURL undefined"

**Step 3: Add SourceURL field to Document model**

Modify `backend/db/models.go`:

```go
type Document struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Title      string    `json:"title"`
	SourceType string    `json:"sourceType"` // pdf, rss, web, manual
	RawPath    string    `json:"rawPath"`
	WikiPath   string    `json:"wikiPath"`
	Summary    string    `json:"summary"`
	Language   string    `json:"language"`
	Status     string    `gorm:"default:inbox" json:"status"`
	Metadata   string    `json:"metadata"`
	SourceURL  string    `json:"sourceUrl"` // NEW: Original URL for web/rss
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Tags       []Tag     `gorm:"many2many:document_tags;" json:"tags"`
}
```

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./db -v`
Expected: PASS

**Step 5: Commit**

```bash
git add backend/db/models.go backend/db/db_test.go
git commit -m "feat: add SourceURL field to Document model"
```

---

### Task 2: Install goquery Dependency

**Files:**
- Modify: `backend/go.mod`

**Step 1: Add goquery to go.mod**

Run: `cd backend && go get github.com/PuerkitoBio/goquery`

**Step 2: Verify dependency is added**

Run: `cd backend && cat go.mod | grep goquery`
Expected: `github.com/PuerkitoBio/goquery v1.x.x`

**Step 3: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "feat: add goquery dependency for HTML parsing"
```

---

### Task 3: Create Web Clipping Handler Structure

**Files:**
- Create: `backend/api/web.go`
- Test: `backend/api/web_test.go`

**Step 1: Write the failing test**

Create `backend/api/web_test.go`:

```go
package api

import (
	"testing"
)

func TestWebHandlerExists(t *testing.T) {
	h := WebHandler{
		DataDir:   "/tmp/test",
		ClaudeBin: "claude",
	}
	if h.DataDir != "/tmp/test" {
		t.Errorf("Expected DataDir to be set")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./api -v -run TestWebHandlerExists`
Expected: FAIL with "WebHandler undefined"

**Step 3: Create WebHandler structure**

Create `backend/api/web.go`:

```go
package api

type WebHandler struct {
	DataDir   string
	ClaudeBin string
}
```

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./api -v -run TestWebHandlerExists`
Expected: PASS

**Step 5: Commit**

```bash
git add backend/api/web.go backend/api/web_test.go
git commit -m "feat: create WebHandler structure"
```

---

### Task 4: Implement HTML Fetch Function

**Files:**
- Modify: `backend/api/web.go`
- Test: `backend/api/web_test.go`

**Step 1: Write the failing test**

Add to `backend/api/web_test.go`:

```go
func TestFetchHTML(t *testing.T) {
	html, err := fetchHTML("https://go.dev/blog/type-construction-and-cycle-detection")
	if err != nil {
		t.Fatalf("Failed to fetch HTML: %v", err)
	}
	if len(html) == 0 {
		t.Error("Expected HTML content, got empty string")
	}
	// Should contain the article title
	if !strings.Contains(html, "Type Construction") {
		t.Error("Expected HTML to contain article title")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./api -v -run TestFetchHTML`
Expected: FAIL with "fetchHTML undefined"

**Step 3: Implement fetchHTML function**

Add to `backend/api/web.go`:

```go
import (
	"io"
	"net/http"
	"strings"
	"time"
)

func fetchHTML(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./api -v -run TestFetchHTML`
Expected: PASS (may be slow due to network call)

**Step 5: Commit**

```bash
git add backend/api/web.go backend/api/web_test.go
git commit -m "feat: implement HTML fetch function"
```

---

### Task 5: Implement HTML Parsing with goquery

**Files:**
- Modify: `backend/api/web.go`
- Test: `backend/api/web_test.go`

**Step 1: Write the failing test**

Add to `backend/api/web_test.go`:

```go
import (
	"github.com/PuerkitoBio/goquery"
)

func TestExtractImageURLs(t *testing.T) {
	html := `<html><body><img src="https://example.com/img1.png"/><img src="img2.jpg"/></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}

	imgURLs := extractImageURLs(doc)
	if len(imgURLs) != 2 {
		t.Errorf("Expected 2 image URLs, got %d", len(imgURLs))
	}
	if imgURLs[0] != "https://example.com/img1.png" {
		t.Errorf("Expected first URL to be absolute, got %s", imgURLs[0])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./api -v -run TestExtractImageURLs`
Expected: FAIL with "extractImageURLs undefined"

**Step 3: Implement extractImageURLs function**

Add to `backend/api/web.go`:

```go
import (
	"github.com/PuerkitoBio/goquery"
)

func extractImageURLs(doc *goquery.Document) []string {
	var urls []string
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if exists && src != "" {
			// Handle relative URLs (basic case)
			if !strings.HasPrefix(src, "http") {
				// Will be resolved later with base URL
			}
			urls = append(urls, src)
		}
	})
	return urls
}

func parseHTML(html string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(html))
}
```

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./api -v -run TestExtractImageURLs`
Expected: PASS

**Step 5: Commit**

```bash
git add backend/api/web.go backend/api/web_test.go
git commit -m "feat: implement HTML parsing and image URL extraction"
```

---

### Task 6: Create Web Upload Endpoint

**Files:**
- Modify: `backend/api/web.go`
- Modify: `backend/main.go:97`

**Step 1: Implement UploadWeb endpoint**

Add to `backend/api/web.go`:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WebUploadRequest struct {
	URL string `json:"url"`
}

func (h *WebHandler) UploadWeb(c echo.Context) error {
	var req WebUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	// Fetch HTML
	html, err := fetchHTML(req.URL)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to fetch URL"})
	}

	// Parse HTML
	doc, err := parseHTML(html)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to parse HTML"})
	}

	// Extract title (basic, will be refined by Claude)
	title := doc.Find("title").Text()
	if title == "" {
		title = "untitled"
	}
	// Clean title for filesystem
	title = strings.ReplaceAll(title, "/", "-")
	title = strings.ReplaceAll(title, " ", "-")

	// Create directory
	dir := filepath.Join(h.DataDir, "raw", "web", title)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directory"})
	}

	// Extract image URLs (placeholder - will refine with Claude)
	imgURLs := extractImageURLs(doc)

	// Create Document record (placeholder - minimal implementation)
	rawRelPath := filepath.Join("raw", "web", title)
	docRecord := db.Document{
		Title:      title,
		SourceType: "web",
		RawPath:    rawRelPath,
		SourceURL:  req.URL,
		Language:   "en",
		Status:     "inbox",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.DB.Create(&docRecord).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create document"})
	}

	return c.JSON(200, echo.Map{
		"id":     docRecord.ID,
		"title":  title,
		"path":   dir,
		"url":    req.URL,
		"images": len(imgURLs),
		"message": "Web page fetched (content processing pending)",
	})
}
```

**Step 2: Register endpoint in main.go**

Add after line 97 in `backend/main.go`:

```go
// Web clipping API
webH := &api.WebHandler{
	DataDir:   cfg.DataDir,
	ClaudeBin: cfg.ClaudeBin,
}
e.POST("/api/raw/web", webH.UploadWeb)
```

**Step 3: Test endpoint manually**

Run: `cd backend && go run main.go`
Then: `curl -X POST http://localhost:8080/api/raw/web -H "Content-Type: application/json" -d '{"url":"https://go.dev/blog/type-construction-and-cycle-detection"}'`

Expected: JSON response with id, title, images count

**Step 4: Commit**

```bash
git add backend/api/web.go backend/main.go
git commit -m "feat: add /api/raw/web endpoint (basic implementation)"
```

---

### Task 7: Add Frontend Web Clipping Activation

**Files:**
- Modify: `frontend/src/components/ImportView.tsx:80-95`
- Modify: `frontend/src/api.ts`

**Step 1: Add clipWeb API function**

Add to `frontend/src/api.ts`:

```typescript
export async function clipWeb(url: string): Promise<{ id: number; title: string; path: string; images: number; message: string }> {
  const res = await fetch('/api/raw/web', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  const data = await res.json()
  if (!res.ok) {
    throw new Error(data.error || 'Failed to clip web page')
  }
  return data
}
```

**Step 2: Update ImportView handleClipUrl**

Modify `frontend/src/components/ImportView.tsx:80-95`:

```typescript
import { uploadPDF, clipWeb } from '../api'

const handleClipUrl = async () => {
  if (!urlInput.trim()) return

  setClippingUrl(true)
  setError(null)
  setUploadResult(null)

  try {
    const result = await clipWeb(urlInput)
    setUploadResult({
      id: result.id,
      path: result.path,
      message: result.message,
      pages: result.images, // Reuse pages field for image count
    })
    setUrlInput('')
  } catch (err) {
    setError(err instanceof Error ? err.message : 'Failed to clip URL')
  } finally {
    setClippingUrl(false)
  }
}
```

**Step 3: Test in browser**

Run: `cd backend && go run main.go`
Open: `http://localhost:8080/import`
Enter: `https://go.dev/blog/type-construction-and-cycle-detection`
Click: "Clip"

Expected: Success message with document ID and image count

**Step 4: Commit**

```bash
git add frontend/src/api.ts frontend/src/components/ImportView.tsx
git commit -m "feat: activate web clipping UI"
```

---

## Phase 2: Publish Workflow Change

### Task 8: Remove Auto Wiki Ingest from PDF Upload

**Files:**
- Modify: `backend/api/raw.go:113-124`

**Step 1: Remove auto wiki ingest goroutine**

In `backend/api/raw.go`, delete lines 113-124:

```go
// DELETE THIS BLOCK:
go func() {
    wikiDir := filepath.Join(h.DataDir, "wiki")
    p := ingest.NewPipeline(wikiDir, h.ClaudeBin)
    if err := p.Ingest(context.Background(), mdPath, name, docID); err != nil {
        log.Printf("[api] ingest failed for %s: %v", name, err)
    } else {
        wikiRelPath := filepath.Join("wiki", name+".md")
        db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("wiki_path", wikiRelPath)
    }
}()
```

Keep only the summary generation goroutine.

**Step 2: Verify PDF upload still works**

Run: `cd backend && go test ./api -v -run TestUploadPDF` (if exists)
Or manual test: Upload PDF via frontend

Expected: PDF uploads, summary generates, no wiki content yet

**Step 3: Commit**

```bash
git add backend/api/raw.go
git commit -m "refactor: remove auto wiki ingest from PDF upload"
```

---

### Task 9: Implement Publish with Wiki Ingest

**Files:**
- Modify: `backend/api/documents.go:137-157`

**Step 1: Add wiki ingest to Publish handler**

Modify `backend/api/documents.go` Publish function (lines 137-157):

```go
func (h *DocHandler) Publish(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(404, echo.Map{"error": "document not found"})
	}

	// Update status to published
	doc.Status = "published"
	doc.UpdatedAt = time.Now()
	if err := db.DB.Save(&doc).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to publish document"})
	}

	// Trigger wiki ingest if raw content exists
	if doc.RawPath != "" && h.ClaudeBin != "" {
		wikiDir := filepath.Join(h.DataDir, "wiki")
		mdPath := filepath.Join(h.DataDir, doc.RawPath, "paper.md")

		// Find markdown file
		if _, err := os.Stat(mdPath); err == nil {
			p := ingest.NewPipeline(wikiDir, h.ClaudeBin)
			ctx := context.Background()
			name := doc.Title
			if err := p.Ingest(ctx, mdPath, name, doc.ID); err != nil {
				log.Printf("[api] wiki ingest failed for %d: %v", doc.ID, err)
				// Still return success - status was updated
			} else {
				wikiRelPath := filepath.Join("wiki", name+".md")
				db.DB.Model(&doc).Update("wiki_path", wikiRelPath)
			}
		}
	}

	return c.JSON(200, echo.Map{
		"id":       doc.ID,
		"status":   "published",
		"wikiPath": doc.WikiPath,
		"message":  "Published and imported to wiki",
	})
}
```

**Step 2: Add imports**

Add to imports in `backend/api/documents.go`:

```go
import (
	"context"
	"llm-knowledge/ingest"
	// ... existing imports
)
```

**Step 3: Test manually**

Upload PDF → Click Publish → Check wiki content appears

Expected: Wiki tab shows content after publish

**Step 4: Commit**

```bash
git add backend/api/documents.go
git commit -m "feat: trigger wiki ingest on publish"
```

---

### Task 10: Add Publish Progress Indicator in Frontend

**Files:**
- Modify: `frontend/src/components/DocDetail.tsx`

**Step 1: Add publishing state**

Add state in DocDetail component:

```typescript
const [publishing, setPublishing] = useState(false)
```

**Step 2: Update handlePublish**

Modify handlePublish function:

```typescript
const handlePublish = async () => {
  if (!document) return
  setPublishing(true)
  try {
    const res = await fetch(`/api/documents/${document.id}/publish`, {
      method: 'POST',
    })
    const data = await res.json()
    if (!res.ok) {
      throw new Error(data.error || 'Failed to publish')
    }
    await loadDocument() // Refresh to get wikiPath
  } catch (err) {
    setError(err instanceof Error ? err.message : 'Failed to publish')
  } finally {
    setPublishing(false)
  }
}
```

**Step 3: Update Publish button**

Modify button in action buttons section:

```typescript
<button
  onClick={handlePublish}
  disabled={publishing}
  className="w-full px-3 py-1.5 text-sm bg-green-500 text-white rounded hover:bg-green-600 disabled:opacity-50 transition-colors"
>
  {publishing ? 'Publishing...' : t('docDetail.publish')}
</button>
```

**Step 4: Test in browser**

Upload document → Click Publish → Watch progress → Verify wiki appears

**Step 5: Commit**

```bash
git add frontend/src/components/DocDetail.tsx
git commit -m "feat: add publish progress indicator"
```

---

## Phase 3: RSS Subscription

### Task 11: Create RSSFeed Model

**Files:**
- Modify: `backend/db/models.go`
- Test: `backend/db/db_test.go`

**Step 1: Write failing test**

Add to `backend/db/db_test.go`:

```go
func TestRSSFeedModel(t *testing.T) {
	feed := RSSFeed{
		Name:     "Go Blog",
		URL:      "https://go.dev/blog/feed.atom",
		AutoSync: false,
	}
	if feed.Name != "Go Blog" {
		t.Error("Expected Name to be set")
	}
	if feed.AutoSync != false {
		t.Error("Expected AutoSync to be false")
	}
}
```

**Step 2: Run test**

Run: `cd backend && go test ./db -v -run TestRSSFeedModel`
Expected: FAIL with "RSSFeed undefined"

**Step 3: Add RSSFeed model**

Add to `backend/db/models.go`:

```go
type RSSFeed struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Name       string    `json:"name"`
	URL        string    `gorm:"unique" json:"url"`
	AutoSync   bool      `gorm:"default:false" json:"autoSync"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	CreatedAt  time.Time `json:"createdAt"`
}
```

**Step 4: Add to DB init**

Modify `backend/db/db.go` to auto-migrate RSSFeed:

```go
db.AutoMigrate(&Document{}, &Tag{}, &DocumentTag{}, &Conversation{}, &ConversationMessage{}, &RSSFeed{})
```

**Step 5: Run test**

Run: `cd backend && go test ./db -v -run TestRSSFeedModel`
Expected: PASS

**Step 6: Commit**

```bash
git add backend/db/models.go backend/db/db.go backend/db/db_test.go
git commit -m "feat: add RSSFeed model"
```

---

### Task 12: Add RSSFeedID to Document

**Files:**
- Modify: `backend/db/models.go`

**Step 1: Add RSSFeedID field**

Modify Document struct:

```go
type Document struct {
	// ... existing fields
	SourceURL  string    `json:"sourceUrl"`
	RSSFeedID  uint      `json:"rssFeedId"` // NEW
	// ...
}
```

**Step 2: Run existing tests**

Run: `cd backend && go test ./db -v`
Expected: PASS

**Step 3: Commit**

```bash
git add backend/db/models.go
git commit -m "feat: add RSSFeedID to Document"
```

---

### Task 13: Create RSS Handler Structure

**Files:**
- Create: `backend/api/rss.go`
- Test: `backend/api/rss_test.go`

**Step 1: Create RSSHandler**

Create `backend/api/rss.go`:

```go
package api

type RSSHandler struct {
	DataDir   string
	ClaudeBin string
}
```

**Step 2: Create basic test**

Create `backend/api/rss_test.go`:

```go
package api

import "testing"

func TestRSSHandlerExists(t *testing.T) {
	h := RSSHandler{DataDir: "/tmp"}
	if h.DataDir != "/tmp" {
		t.Error("Expected DataDir")
	}
}
```

**Step 3: Run test**

Run: `cd backend && go test ./api -v -run TestRSSHandlerExists`
Expected: PASS

**Step 4: Commit**

```bash
git add backend/api/rss.go backend/api/rss_test.go
git commit -m "feat: create RSSHandler"
```

---

### Task 14: Implement RSS Feed CRUD Endpoints

**Files:**
- Modify: `backend/api/rss.go`
- Modify: `backend/main.go`

**Step 1: Implement AddFeed endpoint**

Add to `backend/api/rss.go`:

```go
import (
	"llm-knowledge/db"
	"time"
)

type AddRSSFeedRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	AutoSync bool   `json:"autoSync"`
}

func (h *RSSHandler) AddFeed(c echo.Context) error {
	var req AddRSSFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	feed := db.RSSFeed{
		Name:     req.Name,
		URL:      req.URL,
		AutoSync: req.AutoSync,
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	return c.JSON(200, feed)
}
```

**Step 2: Implement ListFeeds endpoint**

```go
func (h *RSSHandler) ListFeeds(c echo.Context) error {
	var feeds []db.RSSFeed
	result := db.DB.Order("created_at desc").Find(&feeds)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	// Add article count for each feed
	response := make([]echo.Map, len(feeds))
	for i, feed := range feeds {
		var count int64
		db.DB.Model(&db.Document{}).Where("rss_feed_id = ?", feed.ID).Count(&count)
		response[i] = echo.Map{
			"id":           feed.ID,
			"name":         feed.Name,
			"url":          feed.URL,
			"autoSync":     feed.AutoSync,
			"lastSyncAt":   feed.LastSyncAt,
			"createdAt":    feed.CreatedAt,
			"articleCount": count,
		}
	}

	return c.JSON(200, response)
}
```

**Step 3: Implement DeleteFeed endpoint**

```go
func (h *RSSHandler) DeleteFeed(c echo.Context) error {
	id := c.Param("id")

	result := db.DB.Delete(&db.RSSFeed{}, id)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	return c.JSON(200, echo.Map{"id": id, "message": "feed deleted"})
}
```

**Step 4: Register endpoints in main.go**

Add after web endpoints in `backend/main.go`:

```go
// RSS API
rssH := &api.RSSHandler{
	DataDir:   cfg.DataDir,
	ClaudeBin: cfg.ClaudeBin,
}
e.POST("/api/rss/feeds", rssH.AddFeed)
e.GET("/api/rss/feeds", rssH.ListFeeds)
e.DELETE("/api/rss/feeds/:id", rssH.DeleteFeed)
```

**Step 5: Test manually**

```bash
# Add feed
curl -X POST http://localhost:8080/api/rss/feeds \
  -H "Content-Type: application/json" \
  -d '{"name":"Go Blog","url":"https://go.dev/blog/feed.atom","autoSync":false}'

# List feeds
curl http://localhost:8080/api/rss/feeds

# Delete feed
curl -X DELETE http://localhost:8080/api/rss/feeds/1
```

**Step 6: Commit**

```bash
git add backend/api/rss.go backend/main.go
git commit -m "feat: add RSS feed CRUD endpoints"
```

---

### Task 15: Implement RSS Sync Endpoint

**Files:**
- Modify: `backend/api/rss.go`
- Test: `backend/api/rss_test.go`

**Step 1: Implement SyncFeed endpoint (placeholder)**

Add to `backend/api/rss.go`:

```go
func (h *RSSHandler) SyncFeed(c echo.Context) error {
	id := c.Param("id")

	var feed db.RSSFeed
	if err := db.DB.First(&feed, id).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	// Placeholder: fetch RSS XML (will implement in next task)
	// For now, just update LastSyncAt
	feed.LastSyncAt = time.Now()
	db.DB.Save(&feed)

	return c.JSON(200, echo.Map{
		"feedId":      feed.ID,
		"newArticles": 0,
		"total":       0,
		"message":     "Sync placeholder (XML parsing pending)",
	})
}
```

**Step 2: Register endpoint**

Add to `backend/main.go`:

```go
e.POST("/api/rss/feeds/:id/sync", rssH.SyncFeed)
```

**Step 3: Commit**

```bash
git add backend/api/rss.go backend/main.go
git commit -m "feat: add RSS sync endpoint (placeholder)"
```

---

## Phase 4: Testing & Polish

### Task 16: Write Web Clipping Integration Test

**Files:**
- Modify: `backend/api/web_test.go`

**Step 1: Add integration test**

Add to `backend/api/web_test.go`:

```go
func TestUploadWebIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup test server
	e := echo.New()
	h := WebHandler{
		DataDir:   "/tmp/test-web-clipping",
		ClaudeBin: "",
	}
	e.POST("/api/raw/web", h.UploadWeb)

	// Test request
	body := `{"url":"https://go.dev/blog/type-construction-and-cycle-detection"}`
	req := httptest.NewRequest("POST", "/api/raw/web", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Parse response
	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)

	if result["id"] == nil {
		t.Error("Expected document ID")
	}
	if result["title"] == "" {
		t.Error("Expected title")
	}
}
```

**Step 2: Run test**

Run: `cd backend && go test ./api -v -run TestUploadWebIntegration`
Expected: PASS

**Step 3: Commit**

```bash
git add backend/api/web_test.go
git commit -m "test: add web clipping integration test"
```

---

### Task 17: Update Frontend RSS UI

**Files:**
- Modify: `frontend/src/components/ImportView.tsx:98-117`

**Step 1: Add RSS API functions**

Add to `frontend/src/api.ts`:

```typescript
export async function addRSSFeed(name: string, url: string, autoSync: boolean): Promise<RSSFeed> {
  const res = await fetch('/api/rss/feeds', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, url, autoSync }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error)
  return data
}

export async function listRSSFeeds(): Promise<RSSFeed[]> {
  const res = await fetch('/api/rss/feeds')
  return res.json()
}

export async function deleteRSSFeed(id: number): Promise<void> {
  await fetch(`/api/rss/feeds/${id}`, { method: 'DELETE' })
}

export async function syncRSSFeed(id: number): Promise<{ newArticles: number }> {
  const res = await fetch(`/api/rss/feeds/${id}/sync`, { method: 'POST' })
  return res.json()
}

interface RSSFeed {
  id: number
  name: string
  url: string
  autoSync: boolean
  lastSyncAt: string
  createdAt: string
  articleCount: number
}
```

**Step 2: Update ImportView RSS section**

Replace placeholder RSS code in `ImportView.tsx` with real implementation.

**Step 3: Test in browser**

Add RSS feed → List feeds → Sync feed

**Step 4: Commit**

```bash
git add frontend/src/api.ts frontend/src/components/ImportView.tsx
git commit -m "feat: implement RSS frontend UI"
```

---

## Final Integration Test

### Task 18: Full Workflow Test

**Manual test sequence:**

1. **Web Clipping:**
   - Import `https://go.dev/blog/type-construction-and-cycle-detection`
   - Verify: Document appears in Inbox with summary
   - Click Publish → Verify: Wiki content appears

2. **PDF Upload:**
   - Upload PDF → Verify: Inbox, no wiki yet
   - Click Publish → Verify: Wiki appears

3. **RSS Feed:**
   - Add RSS feed with manual sync
   - Sync feed → Verify: New articles appear in Inbox
   - Publish one → Verify: Wiki appears

**Expected:** All workflows work consistently

---

## Notes

- All web clipping currently uses basic title extraction. Claude CLI integration for intelligent extraction will be added in refinement phase.
- Image localization and Claude image filtering pending - current implementation just extracts URLs.
- Auto-sync background goroutine not implemented yet - manual sync only for Phase 1-3.
- Test coverage should be expanded for error cases (invalid URLs, network failures, malformed HTML).

---

**Plan Complete. Two execution options:**

**1. Subagent-Driven (this session)** - I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** - Open new session with executing-plans, batch execution with checkpoints

**Which approach?**