# Web Clipping & RSS Import Design

**Date:** 2026-04-26

## Overview

Add web clipping and RSS subscription features to the LLM Knowledge system, enabling users to import online articles and subscribe to RSS feeds with automatic synchronization.

## Goals

- Import web pages (technical blogs, news, documentation) with intelligent content extraction
- Download and localize relevant images from web pages
- Subscribe to RSS feeds with manual and automatic synchronization options
- Unify import workflow: all sources use the same flow (import → inbox → publish → wiki)

## Non-Goals

- JavaScript-rendered dynamic pages (can be added later via chromedp if needed)
- Video/multimedia content handling beyond basic image localization
- Complex RSS feed parsing (nested feeds, enclosures beyond images)

## Architecture

### Data Model Changes

```go
type Document struct {
    // Existing fields...
    SourceType string  // "pdf", "web", "rss"
    SourceURL  string  // Original URL for web/rss sources

    // RSS-related
    RSSFeedID  uint    // Associated RSS feed (optional)
}

type RSSFeed struct {
    ID          uint
    Name        string
    URL         string
    AutoSync    bool      // Whether to auto-sync
    LastSyncAt  time.Time
    CreatedAt   time.Time
}
```

### Storage Structure

```
data/
  raw/
    papers/{name}/         # PDF uploads (existing)
    web/{title}/           # Web clipping (new)
      ├── paper.md
      ├── assets/
      │   ├── img_1.png
      │   └── img_2.png
      └── metadata.json    # Original URL, title, fetch time
    rss/{feed-name}/       # RSS feeds (new)
      ├── feed.json        # Feed metadata
      └── articles/
          ├── {article-1}/
          ├── {article-2}/
```

## Components

### 1. Web Clipping

**API Endpoint:**
```
POST /api/raw/web
Request: { "url": "https://go.dev/blog/type-construction" }
Response: {
  "id": 123,
  "path": "raw/web/...",
  "message": "Web page clipped",
  "title": "...",
  "images": 3
}
```

**Processing Flow:**
1. `http.Get(url)` - Fetch raw HTML
2. `goquery` parsing - Extract `<img>` URLs, remove `<script>`, `<style>`, ad elements
3. Create directory: `raw/web/{title}/assets/`
4. Claude CLI processing:
   - Extract main content
   - Determine image relevance (filter ads/decorative images)
   - Output Markdown + relevant image URLs
5. Download relevant images → `assets/`
6. Replace image URLs in Markdown → local paths
7. Save `paper.md`
8. Create Document record: `SourceType="web", SourceURL=url, status="inbox"`
9. Async: Generate summary

**Dependencies:**
- Standard library `net/http` for fetching
- `github.com/PuerkitoBio/goquery` for HTML parsing
- Claude CLI for intelligent extraction

### 2. RSS Subscription

**API Endpoints:**
```
POST /api/rss/feeds
Request: { "name": "Go Blog", "url": "...", "autoSync": false }
Response: { "id": 1, "name": "...", "url": "..." }

GET /api/rss/feeds
Response: [{
  "id": 1,
  "name": "Go Blog",
  "url": "...",
  "autoSync": false,
  "lastSyncAt": "2026-04-26T...",
  "articleCount": 10
}]

DELETE /api/rss/feeds/:id

POST /api/rss/feeds/:id/sync
Response: { "newArticles": 3, "total": 10 }
```

**Manual Sync Flow:**
1. Fetch RSS XML via `http.Get(feed.URL)`
2. Parse XML with `encoding/xml` - extract article titles, links, pubDate
3. Compare with existing articles in DB - identify new ones
4. For each new article:
   - Call web clipping flow with article URL
   - Set `RSSFeedID` on Document record
5. Update `LastSyncAt` on RSSFeed

**Auto Sync Flow:**
- Background goroutine runs every 1 hour
- Checks RSS feeds where `AutoSync=true`
- Executes manual sync logic for each

**Dependencies:**
- Standard library `encoding/xml` for RSS parsing
- Same web clipping logic for article processing

### 3. Publish Workflow Change

**Current Behavior:**
- PDF upload → async wiki ingest (unwanted)

**New Behavior (All Sources):**
- Import → status="inbox", async summary generation
- Publish button → status="published" + trigger wiki ingest + update `wiki_path`

**API Change:**
```
POST /api/documents/:id/publish
Response: {
  "id": 123,
  "status": "published",
  "wikiPath": "wiki/xxx.md",
  "message": "Published and imported to wiki"
}
```

**Backend Logic:**
```go
func (h *DocHandler) Publish(c echo.Context) error {
    // 1. Update status = "published"
    // 2. Trigger wiki ingest pipeline
    // 3. Update wiki_path
    // 4. Return result
}
```

### 4. Frontend Changes

**ImportView - Web Clipping:**
- Activate existing placeholder URL input
- Call `POST /api/raw/web` on "Clip" button
- Show progress → success message → link to document detail

**ImportView - RSS Feeds:**
- Add "Auto Sync" checkbox when adding feed
- Feed list shows: name, URL, article count, last sync time
- "Sync" button per feed (manual trigger)
- "Delete" button per feed
- Click feed name → view feed's articles (reuse DocumentsList component)

**Document Detail:**
- Publish button triggers wiki ingest (shows progress indicator)
- Success → wiki content available in view mode toggle

## Testing Strategy

### Modified Tests

- `backend/api/api_test.go`:
  - Update `TestPublishDocument` to verify wiki ingest is triggered
  - Mock wiki ingest for test isolation

- `backend/ingest/pdf_test.go`:
  - Ensure tests don't expect automatic wiki ingest

### New Tests

```go
// backend/api/web_test.go
TestUploadWebPage           # Basic web clipping
TestWebPageWithImages       # Image download and localization
TestWebPageImageFilter      # Claude filters ad images (mocked)

// backend/api/rss_test.go
TestAddRSSFeed              # Add feed
TestSyncRSSFeed             # Manual sync
TestAutoSyncRSSFeed         # Auto sync logic (time-based)
TestRSSFeedWithWebClipping  # Article clipping integration

// backend/api/documents_test.go
TestPublishTriggersWikiIngest  # Wiki ingest on publish
```

### Test Data

- Static HTML files in `backend/testdata/web/` - simulate web pages
- RSS XML files in `backend/testdata/rss/` - simulate feeds
- Mock Claude CLI responses for deterministic tests

## Implementation Phases

1. **Phase 1: Web Clipping Core**
   - Add `SourceURL` field to Document model
   - Implement `POST /api/raw/web` endpoint
   - Add `goquery` dependency
   - Claude CLI integration for content extraction
   - Frontend activation of URL input

2. **Phase 2: Publish Workflow Change**
   - Modify `Publish` endpoint to trigger wiki ingest
   - Remove auto wiki ingest from PDF upload
   - Update frontend Publish button (progress indicator)

3. **Phase 3: RSS Subscription**
   - Add `RSSFeed` model and `RSSFeedID` field
   - Implement RSS feed CRUD endpoints
   - Implement manual sync endpoint
   - Frontend RSS feed management UI
   - Background auto-sync goroutine

4. **Phase 4: Testing & Polish**
   - Write/modify tests for all phases
   - Handle edge cases (failed downloads, invalid URLs, malformed HTML)
   - Add error handling and user feedback

## Open Questions

- **Image format handling**: Currently assumes PNG/JPG. Should we handle other formats (SVG, WebP)?
- **RSS feed deduplication**: How to handle same article appearing in multiple feeds?
- **Auto sync interval**: 1 hour default. Should this be configurable per feed?
- **Claude CLI timeout**: Web clipping may take longer than PDF. Need timeout handling.

## Risks

- **Website changes**: HTML structure may change, breaking extraction
- **Rate limiting**: Frequent RSS sync may hit rate limits
- **Claude API cost**: Web clipping uses Claude for every page (mitigation: batch processing, caching)
- **Image download failures**: Network errors, missing images, CORS issues

## Success Criteria

- User can clip technical blog articles with images localized
- RSS feeds can be added and synced manually
- Auto-sync works reliably without hitting rate limits
- All existing tests pass, new tests cover core flows
- Publish triggers wiki ingest for all source types