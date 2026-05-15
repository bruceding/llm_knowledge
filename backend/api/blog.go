package api

import (
	"fmt"
	"llm-knowledge/blog"
	"llm-knowledge/config"
	"llm-knowledge/db"
	"llm-knowledge/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type BlogHandler struct {
	DataDir   string
	ClaudeBin string
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

	// Fetch index page
	htmlContent, err := blog.FetchIndexPage(req.IndexURL)
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

	return c.JSON(200, echo.Map{
		"feed":         feed,
		"platformType": rule.Name,
		"detected":     true,
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
	// Fetch index page
	htmlContent, err := blog.FetchIndexPage(feed.IndexURL)
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

	// First sync: take only first 5
	isFirstSync := feed.LastSyncAt.IsZero()
	if isFirstSync && len(links) > 5 {
		links = links[:5]
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

	for _, link := range links {
		// Check if article already exists (by source_url)
		var exists int64
		db.DB.Model(&db.Document{}).Where("source_url = ? AND blog_feed_id = ?", link.URL, feed.ID).Count(&exists)
		if exists > 0 {
			continue
		}

		// Fetch article content
		content, articleDate, err := blog.FetchArticleContent(link.URL, feed.ContentSelector)
		if err != nil {
			downloadErrors++
			continue
		}

		// For subsequent syncs: skip articles older than lastArticleDate
		if !isFirstSync && !articleDate.IsZero() && articleDate <= feed.LastArticleDate {
			continue
		}

		// Save article
		safeTitle := sanitizeFilename(link.Title)
		if safeTitle == "" {
			safeTitle = fmt.Sprintf("article-%d", time.Now().Unix())
		}

		rawPath := filepath.Join("raw", "blog", sanitizeFilename(feed.Name), safeTitle+".txt")
		fullPath := filepath.Join(userDir, rawPath)

		// Write content to file
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			downloadErrors++
			continue
		}

		// Create document
		doc := db.Document{
			Title:      link.Title,
			Slug:       safeTitle,
			SourceType: "blog",
			RawPath:    "u1/" + rawPath, // Add user prefix
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

		// Update max date
		if !articleDate.IsZero() && articleDate > maxDate {
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
		Message:        fmt.Sprintf("同步完成，新增 %d 篇文章", newArticles),
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
