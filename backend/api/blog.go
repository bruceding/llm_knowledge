package api

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"

	"llm-knowledge/blog"
	"llm-knowledge/browser"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/fs"
)

// firstSyncCandidateLimit caps the number of articles fetched when seeding a
// new feed. We need to fetch each candidate to read its publication date for
// sorting, so this cap bounds worst-case browser-pool occupation: at 2s
// WaitStable per SPA render, 20 candidates ≈ 40s of pool time. Without a cap,
// a 100-article SPA blog would block the pool for several minutes on first
// sync.
const firstSyncCandidateLimit = 20

// firstSyncTakeRecent is how many of the (sorted) candidates actually become
// inbox documents. Matches the pre-PR-55 behaviour of taking the 5 most
// recent articles.
const firstSyncTakeRecent = 5

type BlogHandler struct {
	DataDir     string
	ClaudeBin   string
	BrowserPool *browser.Pool
}

type AddBlogFeedRequest struct {
	Name     string `json:"name"`
	IndexURL string `json:"indexUrl"`
	AutoSync bool   `json:"autoSync"`
}

type ConfigBlogFeedRequest struct {
	LinkSelector    string `json:"linkSelector"`
	ContentSelector string `json:"contentSelector"`
	LinkExclude     string `json:"linkExclude"`
}

type BlogSyncResult struct {
	FeedID         uint   `json:"feedId"`
	FeedName       string `json:"feedName"`
	NewArticles    int    `json:"newArticles"`
	Total          int    `json:"total"`
	DownloadErrors int    `json:"downloadErrors"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
	NeedConfig     bool   `json:"needConfig,omitempty"`
	PlatformType   string `json:"platformType,omitempty"`
}

// AddFeed adds a new blog feed with auto-detection
func (h *BlogHandler) AddFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req AddBlogFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.IndexURL == "" {
		return c.JSON(400, echo.Map{"error": "indexUrl is required"})
	}

	// Fetch index page (with browser fallback for SPA sites)
	fetcher := &blog.Fetcher{Pool: h.BrowserPool}
	htmlContent, err := fetcher.FetchIndex(req.IndexURL, "")
	if err != nil {
		return c.JSON(400, echo.Map{"error": "failed to fetch index page: " + err.Error()})
	}

	// Try to detect platform
	rule := blog.DetectPlatform(htmlContent, req.IndexURL)

	// Get feed name
	feedName := req.Name
	if feedName == "" {
		u, err := url.Parse(req.IndexURL)
		if err == nil {
			feedName = strings.TrimPrefix(u.Host, "www.")
		} else {
			feedName = "Blog Feed"
		}
	}
	feedName = sanitizeFilename(feedName)

	// Create feed
	feed := db.BlogFeed{
		UserID:    userId,
		Name:      feedName,
		IndexURL:  req.IndexURL,
		AutoSync:  req.AutoSync,
		CreatedAt: time.Now(),
	}

	if rule != nil {
		feed.PlatformType = rule.Name
		feed.LinkSelector = rule.LinkSelector
		feed.ContentSelector = rule.ContentSelector
		feed.LinkExclude = rule.LinkExclude
	} else {
		feed.PlatformType = "custom"
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	if rule == nil {
		return c.JSON(200, echo.Map{
			"feed":       feed,
			"needConfig": true,
			"message":    "无法自动识别站点，请手动配置",
		})
	}

	resp := echo.Map{
		"feed":         feed,
		"platformType": rule.Name,
		"detected":     true,
	}
	// Only attach syncResult when an actual sync ran. The frontend distinguishes
	// detected-only ("Detected platform X") from synced ("N new articles") by
	// the presence of this field — emitting an empty object collapses both
	// branches into the synced one.
	if req.AutoSync {
		result := h.syncFeedInternal(&feed)
		resp["syncResult"] = result
	}
	return c.JSON(200, resp)
}

// ConfigFeed configures selectors for a feed (when auto-detection failed)
func (h *BlogHandler) ConfigFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var feed db.BlogFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	var req ConfigBlogFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.LinkSelector == "" || req.ContentSelector == "" {
		return c.JSON(400, echo.Map{"error": "linkSelector and contentSelector are required"})
	}

	feed.LinkSelector = req.LinkSelector
	feed.ContentSelector = req.ContentSelector
	feed.LinkExclude = req.LinkExclude
	feed.PlatformType = "custom"

	if err := db.DB.Save(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save feed"})
	}

	return c.JSON(200, feed)
}

// ListFeeds lists all blog feeds for the user
func (h *BlogHandler) ListFeeds(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var feeds []db.BlogFeed
	result := db.DB.Where("user_id = ?", userId).Order("created_at desc").Find(&feeds)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	response := make([]echo.Map, len(feeds))
	for i, feed := range feeds {
		var count int64
		db.DB.Model(&db.Document{}).Where("blog_feed_id = ?", feed.ID).Count(&count)
		response[i] = echo.Map{
			"id":              feed.ID,
			"name":            feed.Name,
			"indexUrl":        feed.IndexURL,
			"platformType":    feed.PlatformType,
			"linkSelector":    feed.LinkSelector,
			"contentSelector": feed.ContentSelector,
			"autoSync":        feed.AutoSync,
			"lastArticleDate": feed.LastArticleDate,
			"lastSyncAt":      feed.LastSyncAt,
			"createdAt":       feed.CreatedAt,
			"articleCount":    count,
		}
	}

	return c.JSON(200, response)
}

// DeleteFeed deletes a blog feed and its inbox documents
func (h *BlogHandler) DeleteFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var feed db.BlogFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	// Find and delete inbox documents
	var inboxDocs []db.Document
	if err := db.DB.Where("blog_feed_id = ? AND user_id = ? AND status = ?", id, userId, "inbox").Find(&inboxDocs).Error; err != nil {
		return c.JSON(500, echo.Map{"error": err.Error()})
	}

	for _, doc := range inboxDocs {
		if doc.RawPath != "" {
			fullPath := filepath.Join(config.GetUserDir(h.DataDir, userId), StripUserPrefix(doc.RawPath))
			os.Remove(fullPath)
		}
	}

	// Delete inbox documents from database
	db.DB.Where("blog_feed_id = ? AND user_id = ? AND status = ?", id, userId, "inbox").Delete(&db.Document{})

	// Delete the feed
	db.DB.Delete(&db.BlogFeed{}, id)

	return c.JSON(200, echo.Map{"id": id, "message": "feed deleted", "deletedDocs": len(inboxDocs)})
}

// SyncFeed syncs a blog feed to fetch new articles
func (h *BlogHandler) SyncFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var feed db.BlogFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	result := h.syncFeedInternal(&feed)
	return c.JSON(200, result)
}

// syncFeedInternal performs the actual sync
func (h *BlogHandler) syncFeedInternal(feed *db.BlogFeed) BlogSyncResult {
	// Fetch index page (with browser fallback for SPA sites)
	fetcher := &blog.Fetcher{Pool: h.BrowserPool}
	htmlContent, err := fetcher.FetchIndex(feed.IndexURL, feed.LinkSelector)
	if err != nil {
		return BlogSyncResult{
			FeedID:   feed.ID,
			FeedName: feed.Name,
			Error:    "failed to fetch index page: " + err.Error(),
		}
	}

	// Extract article links
	links, err := blog.ExtractArticleLinks(htmlContent, feed.IndexURL, feed.LinkSelector, feed.LinkExclude)
	if err != nil {
		return BlogSyncResult{
			FeedID:   feed.ID,
			FeedName: feed.Name,
			Error:    "failed to extract links: " + err.Error(),
		}
	}

	isFirstSync := feed.LastSyncAt.IsZero()

	// Filter out already-imported links up front (matters for both branches).
	links = filterUnimported(links, feed.ID)

	// On first sync we cap how many articles we'll fetch (each fetch is
	// expensive when the SPA fallback kicks in), then keep the most recent.
	// Each candidate is fetched exactly once — the result is reused below
	// so we don't pay the SPA-render tax twice per link.
	type fetchedArticle struct {
		link        blog.ArticleLink
		contentHTML string
		date        time.Time
		h1Title     string
		fetchErr    error
	}

	var candidates []fetchedArticle
	if isFirstSync {
		probeLinks := links
		if len(probeLinks) > firstSyncCandidateLimit {
			probeLinks = probeLinks[:firstSyncCandidateLimit]
		}
		for _, link := range probeLinks {
			contentHTML, articleDate, h1Title, err := fetcher.FetchArticle(link.URL, feed.ContentSelector)
			candidates = append(candidates, fetchedArticle{
				link:        link,
				contentHTML: contentHTML,
				date:        articleDate,
				h1Title:     h1Title,
				fetchErr:    err,
			})
		}

		// Most recent first; missing dates sink to the end.
		sort.SliceStable(candidates, func(i, j int) bool {
			a, b := candidates[i].date, candidates[j].date
			if a.IsZero() && !b.IsZero() {
				return false
			}
			if !a.IsZero() && b.IsZero() {
				return true
			}
			return a.After(b)
		})
		if len(candidates) > firstSyncTakeRecent {
			candidates = candidates[:firstSyncTakeRecent]
		}
	} else {
		// Subsequent syncs: fetch each link once and skip below the watermark.
		for _, link := range links {
			contentHTML, articleDate, h1Title, err := fetcher.FetchArticle(link.URL, feed.ContentSelector)
			if err == nil && !articleDate.IsZero() && !articleDate.After(feed.LastArticleDate) {
				continue
			}
			candidates = append(candidates, fetchedArticle{
				link:        link,
				contentHTML: contentHTML,
				date:        articleDate,
				h1Title:     h1Title,
				fetchErr:    err,
			})
		}
	}

	// Setup directories
	userDir := config.GetUserDir(h.DataDir, feed.UserID)
	fs.InitUserDirs(h.DataDir, feed.UserID)

	feedDir := filepath.Join(userDir, "raw", "blog", sanitizeFilename(feed.Name))
	assetsDir := filepath.Join(feedDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return BlogSyncResult{
			FeedID:   feed.ID,
			FeedName: feed.Name,
			Error:    "failed to create directories",
		}
	}

	newArticles := 0
	downloadErrors := 0
	maxDate := feed.LastArticleDate

	for _, cand := range candidates {
		link := cand.link
		if cand.fetchErr != nil {
			downloadErrors++
			continue
		}
		articleDate := cand.date

		// Determine title: prefer h1 from article body, fall back to link text
		articleTitle := cand.h1Title
		if articleTitle == "" {
			articleTitle = link.Title
		}

		if cand.contentHTML == "" {
			log.Printf("[blog] no content matched selector %q for %s — skipping", feed.ContentSelector, link.URL)
			downloadErrors++
			continue
		}

		// Convert HTML → Markdown, downloading images into assetsDir
		markdownBody, _, imgErrs := processHTMLToMarkdown(cand.contentHTML, assetsDir, link.URL)
		downloadErrors += imgErrs

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

		// Save article - prefer URL slug for filename, but always sanitize: the
		// URL path arrives percent-decoded and may contain spaces, CJK chars,
		// or shell-unsafe punctuation that breaks the filesystem path.
		fileBase := sanitizeFilename(extractURLSlug(link.URL))
		if fileBase == "" {
			fileBase = sanitizeFilename(articleTitle)
		}
		if fileBase == "" {
			fileBase = fmt.Sprintf("article-%d", time.Now().Unix())
		}

		userIdStr := strconv.FormatUint(uint64(feed.UserID), 10)
		rawPath := filepath.Join("users", userIdStr, "raw", "blog", sanitizeFilename(feed.Name), fileBase+".md")

		// Resolve raw_path collision (same logic as before, .md suffix)
		var collisionCount int64
		db.DB.Unscoped().Model(&db.Document{}).
			Where("raw_path = ? AND source_url != ?", rawPath, link.URL).
			Count(&collisionCount)
		if collisionCount > 0 {
			fileBase = fmt.Sprintf("%s-%d", fileBase, collisionCount+1)
			rawPath = filepath.Join("users", userIdStr, "raw", "blog", sanitizeFilename(feed.Name), fileBase+".md")
		}

		fullPath := filepath.Join(h.DataDir, rawPath)

		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			downloadErrors++
			continue
		}

		doc := db.Document{
			Title:      articleTitle,
			Slug:       fileBase,
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
	}

	// Update feed
	feed.LastArticleDate = maxDate
	feed.LastSyncAt = time.Now()
	db.DB.Save(feed)

	return BlogSyncResult{
		FeedID:         feed.ID,
		FeedName:       feed.Name,
		NewArticles:    newArticles,
		Total:          len(links),
		DownloadErrors: downloadErrors,
		Message:        fmt.Sprintf("Sync completed, %d new articles added", newArticles),
	}
}

// SyncAllFeeds syncs all feeds with autoSync enabled
func (h *BlogHandler) SyncAllFeeds(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var feeds []db.BlogFeed
	db.DB.Where("user_id = ? AND auto_sync = ?", userId, true).Find(&feeds)

	results := make([]BlogSyncResult, len(feeds))
	for i, feed := range feeds {
		results[i] = h.syncFeedInternal(&feed)
	}

	return c.JSON(200, results)
}

// GetFeed gets a single feed by ID
func (h *BlogHandler) GetFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var feed db.BlogFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	var count int64
	db.DB.Model(&db.Document{}).Where("blog_feed_id = ?", feed.ID).Count(&count)

	return c.JSON(200, echo.Map{
		"feed":         feed,
		"articleCount": count,
	})
}

// filterUnimported drops links whose source_url already exists for this feed
// (including soft-deleted rows, so a user-deleted article isn't re-imported).
func filterUnimported(links []blog.ArticleLink, feedID uint) []blog.ArticleLink {
	out := make([]blog.ArticleLink, 0, len(links))
	for _, link := range links {
		var exists int64
		db.DB.Unscoped().Model(&db.Document{}).
			Where("source_url = ? AND blog_feed_id = ?", link.URL, feedID).
			Count(&exists)
		if exists == 0 {
			out = append(out, link)
		}
	}
	return out
}

// extractURLSlug extracts the last path segment from a URL for use as filename
func extractURLSlug(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	// Get last path segment, removing any trailing slash
	path := strings.TrimSuffix(u.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		slug := parts[len(parts)-1]
		// Remove common suffixes like .html, .php
		for _, suffix := range []string{".html", ".php", ".aspx"} {
			slug = strings.TrimSuffix(slug, suffix)
		}
		return slug
	}
	return ""
}

// blogSchedulerStarted is a process-wide latch — calling StartAutoSyncScheduler
// twice (test wiring, refactor mistake, future hot-reload) must not start two
// tickers competing for the browser pool.
var blogSchedulerStarted atomic.Bool

// StartAutoSyncScheduler starts a background scheduler that syncs feeds with autoSync enabled
// It checks every hour and syncs feeds that haven't been synced in the last hour
func (h *BlogHandler) StartAutoSyncScheduler() {
	if !blogSchedulerStarted.CompareAndSwap(false, true) {
		log.Println("[blog] Auto-sync scheduler already running, ignoring duplicate start")
		return
	}
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		log.Println("[blog] Auto-sync scheduler started, checking every hour")

		for range ticker.C {
			// Per-tick recover: a panic in a single feed sync (nil deref in
			// fetcher fallback, GORM driver oddity, etc.) must not kill the
			// whole server. Log it and continue with the next tick.
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[blog] auto-sync tick panicked: %v", r)
					}
				}()
				h.syncAutoSyncFeeds()
			}()
		}
	}()
}

// syncAutoSyncFeeds syncs all feeds that have autoSync enabled and need syncing
func (h *BlogHandler) syncAutoSyncFeeds() {
	var feeds []db.BlogFeed
	if err := db.DB.Where("auto_sync = ?", true).Find(&feeds).Error; err != nil {
		log.Printf("[blog] Failed to query auto-sync feeds: %v\n", err)
		return
	}

	if len(feeds) == 0 {
		return
	}

	log.Printf("[blog] Checking %d auto-sync feeds...\n", len(feeds))

	minSyncInterval := 1 * time.Hour
	for _, feed := range feeds {
		// Skip if synced recently (within the last hour)
		if !feed.LastSyncAt.IsZero() && time.Since(feed.LastSyncAt) < minSyncInterval {
			continue
		}

		log.Printf("[blog] Auto-syncing feed: %s (%s)\n", feed.Name, feed.IndexURL)
		result := h.syncFeedInternal(&feed)
		if result.Error != "" {
			log.Printf("[blog] Auto-sync failed for %s: %s\n", feed.Name, result.Error)
		} else {
			log.Printf("[blog] Auto-sync completed for %s: %s\n", feed.Name, result.Message)
		}
	}
}
