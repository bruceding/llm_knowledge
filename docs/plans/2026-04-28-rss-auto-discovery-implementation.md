# RSS Feed Auto-Discovery Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add RSS feed auto-discovery when user inputs a blog URL instead of direct RSS URL.

**Architecture:** Extend `AddFeed` method with three-stage detection: direct RSS parse → HTML `<head>` link tags → common path probing. All using existing `gofeed` and `goquery` libraries.

**Tech Stack:** Go, gofeed (RSS parsing), goquery (HTML parsing), echo (HTTP)

---

### Task 1: Add RSS discovery helper functions

**Files:**
- Modify: `backend/api/rss.go` (add new functions after `sanitizeFilename`)

**Step 1: Add `discoverRSSFromHTML` function**

Add after line 369 (after `sanitizeFilename` function):

```go
// discoverRSSFromHTML parses HTML and finds RSS/Atom feed links in <head>
func discoverRSSFromHTML(htmlURL string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(htmlURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Find RSS/Atom link tags
	var feedURL string
	doc.Find("link[type='application/rss+xml'], link[type='application/atom+xml']").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists && href != "" && feedURL == "" {
			// Resolve relative URL
			base, err := url.Parse(htmlURL)
			if err == nil {
				rel, err := url.Parse(href)
				if err == nil {
					feedURL = base.ResolveReference(rel).String()
				}
			}
		}
	})

	if feedURL == "" {
		return "", fmt.Errorf("no RSS link found in HTML")
	}
	return feedURL, nil
}
```

**Step 2: Add `probeCommonRSSPaths` function**

Add after `discoverRSSFromHTML`:

```go
// probeCommonRSSPaths tries common RSS path patterns
func probeCommonRSSPaths(baseURL string) (string, error) {
	paths := []string{"feed", "rss", "rss.xml", "atom.xml", "feed.xml"}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, path := range paths {
		rel, _ := url.Parse(path)
		candidateURL := base.ResolveReference(rel).String()

		_, err := fp.ParseURLWithContext(candidateURL, ctx)
		if err == nil {
			return candidateURL, nil
		}
	}

	return "", fmt.Errorf("no valid RSS found at common paths")
}
```

**Step 3: Add `tryParseAsRSS` function**

Add after `probeCommonRSSPaths`:

```go
// tryParseAsRSS attempts to parse URL directly as RSS feed
func tryParseAsRSS(feedURL string) (*gofeed.Feed, error) {
	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return fp.ParseURLWithContext(feedURL, ctx)
}
```

**Step 4: Commit helper functions**

```bash
git add backend/api/rss.go
git commit -m "feat: add RSS discovery helper functions"
```

---

### Task 2: Refactor AddFeed to use auto-discovery

**Files:**
- Modify: `backend/api/rss.go:36-86` (AddFeed method)

**Step 1: Replace AddFeed implementation**

Replace the entire `AddFeed` function (lines 36-86) with:

```go
func (h *RSSHandler) AddFeed(c echo.Context) error {
	var req AddRSSFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	inputURL := req.URL
	feedURL := inputURL
	var triedPaths []string

	// Stage 1: Try direct RSS parse
	rssFeed, err := tryParseAsRSS(inputURL)
	if err != nil {
		// Stage 2: Parse HTML to find RSS link
		triedPaths = append(triedPaths, inputURL)
		discoveredURL, err := discoverRSSFromHTML(inputURL)
		if err != nil {
			// Stage 3: Probe common paths
			tried := []string{"feed", "rss", "rss.xml", "atom.xml", "feed.xml"}
			triedPaths = append(triedPaths, tried...)
			discoveredURL, err = probeCommonRSSPaths(inputURL)
			if err != nil {
				return c.JSON(404, echo.Map{
					"error": fmt.Sprintf("未找到 RSS feed。尝试路径：%s", strings.Join(triedPaths, ", ")),
				})
			}
			feedURL = discoveredURL
		} else {
			feedURL = discoveredURL
		}

		// Verify discovered URL is valid RSS
		rssFeed, err = tryParseAsRSS(feedURL)
		if err != nil {
			return c.JSON(404, echo.Map{
				"error": fmt.Sprintf("发现的 feed URL 无效：%s", feedURL),
			})
		}
	}

	// Get feed name
	feedName := req.Name
	if feedName == "" {
		feedName = rssFeed.Title
		if feedName == "" {
			u, _ := url.Parse(feedURL)
			feedName = strings.TrimPrefix(u.Host, "www.")
		}
	}
	feedName = sanitizeFilename(feedName)

	feed := db.RSSFeed{
		Name:      feedName,
		URL:       feedURL,
		AutoSync:  req.AutoSync,
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	return c.JSON(200, feed)
}
```

**Step 2: Commit AddFeed refactor**

```bash
git add backend/api/rss.go
git commit -m "feat: integrate RSS auto-discovery into AddFeed"
```

---

### Task 3: Manual testing

**Files:**
- None (testing only)

**Step 1: Start the backend server**

Run: `cd backend && go run main.go`
Expected: Server starts on port 8080

**Step 2: Test direct RSS URL**

Run curl:
```bash
curl -X POST http://localhost:8080/api/rss \
  -H "Content-Type: application/json" \
  -d '{"url": "https://go.dev/blog/feed.atom"}'
```
Expected: Feed added successfully with name "The Go Blog"

**Step 3: Test blog URL with auto-discovery**

Run curl:
```bash
curl -X POST http://localhost:8080/api/rss \
  -H "Content-Type: application/json" \
  -d '{"url": "https://go.dev/blog/"}'
```
Expected: Feed added successfully, discovered feed URL in response

**Step 4: Test invalid URL**

Run curl:
```bash
curl -X POST http://localhost:8080/api/rss \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/not-a-blog"}'
```
Expected: 404 error with message listing tried paths

---

### Task 4: Final commit and cleanup

**Step 1: Check git status**

Run: `git status`
Expected: All changes committed

**Step 2: Update implementation doc**

Append testing results to implementation plan if needed.