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
	"time"

	"github.com/labstack/echo/v4"

	"llm-knowledge/blog"
	"llm-knowledge/browser"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/fs"
)

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

	// If autoSync enabled and platform detected, trigger first sync
	var syncResult BlogSyncResult
	if req.AutoSync {
		syncResult = h.syncFeedInternal(&feed)
	}

	return c.JSON(200, echo.Map{
		"feed":         feed,
		"platformType": rule.Name,
		"detected":     true,
		"syncResult":   syncResult,
	})
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

	// For first sync: fetch dates for all links, sort by date, take most recent 5
	// For subsequent syncs: only fetch links that might be newer than lastArticleDate
	var linksToSync []blog.ArticleLink

	if isFirstSync {
		// Fetch publication dates for all links to sort by recency
		type linkWithDate struct {
			link blog.ArticleLink
			date time.Time
		}
		var linksWithDates []linkWithDate

		for _, link := range links {
			// Check if article already exists (skip if already imported)
			var exists int64
			db.DB.Unscoped().Model(&db.Document{}).Where("source_url = ? AND blog_feed_id = ?", link.URL, feed.ID).Count(&exists)
			if exists > 0 {
				continue
			}

			// Fetch just the date (lighter than full content)
			_, articleDate, _, err := fetcher.FetchArticle(link.URL, feed.ContentSelector)
			if err != nil {
				linksWithDates = append(linksWithDates, linkWithDate{link: link, date: time.Time{}})
			} else {
				linksWithDates = append(linksWithDates, linkWithDate{link: link, date: articleDate})
			}
		}

		// Sort by date (most recent first), articles with no date go to end
		sort.Slice(linksWithDates, func(i, j int) bool {
			if linksWithDates[i].date.IsZero() && !linksWithDates[j].date.IsZero() {
				return false
			}
			if !linksWithDates[i].date.IsZero() && linksWithDates[j].date.IsZero() {
				return true
			}
			return linksWithDates[i].date.After(linksWithDates[j].date)
		})

		// Take most recent 5 for first sync
		maxArticles := 5
		if len(linksWithDates) > maxArticles {
			linksWithDates = linksWithDates[:maxArticles]
		}
		for _, lwd := range linksWithDates {
			linksToSync = append(linksToSync, lwd.link)
		}
	} else {
		// Subsequent syncs: use all links (already imported ones will be skipped in loop)
		linksToSync = links
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

	for _, link := range linksToSync {
		// Check if article already exists (by source_url), including soft-deleted
		// records so a user-deleted article is not re-imported on the next sync.
		var exists int64
		db.DB.Unscoped().Model(&db.Document{}).Where("source_url = ? AND blog_feed_id = ?", link.URL, feed.ID).Count(&exists)
		if exists > 0 {
			continue
		}

		// Fetch article (HTML inner of content node + metadata, with browser fallback for SPA)
		contentHTML, articleDate, h1Title, err := fetcher.FetchArticle(link.URL, feed.ContentSelector)
		if err != nil {
			downloadErrors++
			continue
		}

		// Determine title: prefer h1 from article page, fall back to link text
		articleTitle := h1Title
		if articleTitle == "" {
			articleTitle = link.Title
		}

		if contentHTML == "" {
			log.Printf("[blog] no content matched selector %q for %s — skipping", feed.ContentSelector, link.URL)
			downloadErrors++
			continue
		}

		// Convert HTML → Markdown, downloading images into assetsDir
		markdownBody, _, imgErrs := processHTMLToMarkdown(contentHTML, assetsDir, link.URL)
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

		// Save article - use URL slug for filename to avoid collision
		urlSlug := extractURLSlug(link.URL)
		fileBase := urlSlug
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

// StartAutoSyncScheduler starts a background scheduler that syncs feeds with autoSync enabled
// It checks every hour and syncs feeds that haven't been synced in the last hour
func (h *BlogHandler) StartAutoSyncScheduler() {
	go func() {
		// Check every hour
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		log.Println("[blog] Auto-sync scheduler started, checking every hour")

		for range ticker.C {
			h.syncAutoSyncFeeds()
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
