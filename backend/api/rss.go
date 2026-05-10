package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/echo/v4"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

type RSSHandler struct {
	DataDir   string
	ClaudeBin string
}

type AddRSSFeedRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	AutoSync bool   `json:"autoSync"`
}

func (h *RSSHandler) AddFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)

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
			tried := []string{"/feed", "/rss", "/rss.xml", "/atom.xml", "/feed.xml"}
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
			u, err := url.Parse(feedURL)
			if err == nil {
				feedName = strings.TrimPrefix(u.Host, "www.")
			} else {
				feedName = "RSS Feed"
			}
		}
	}
	feedName = sanitizeFilename(feedName)

	feed := db.RSSFeed{
		Name:      feedName,
		URL:       feedURL,
		AutoSync:  req.AutoSync,
		UserID:    userId,
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	return c.JSON(200, feed)
}

func (h *RSSHandler) ListFeeds(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var feeds []db.RSSFeed
	result := db.DB.Where("user_id = ?", userId).Order("created_at desc").Find(&feeds)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

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

func (h *RSSHandler) DeleteFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	// Get feed to determine directory path, check ownership
	var feed db.RSSFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	// Find all inbox documents for this feed AND this user
	var inboxDocs []db.Document
	if err := db.DB.Where("rss_feed_id = ? AND user_id = ? AND status = ?", id, userId, "inbox").Find(&inboxDocs).Error; err != nil {
		return c.JSON(500, echo.Map{"error": err.Error()})
	}

	// Delete physical files for inbox documents only
	for _, doc := range inboxDocs {
		if doc.RawPath != "" {
			fullPath := filepath.Join(h.DataDir, doc.RawPath)
			os.Remove(fullPath)
		}
	}

	// Delete inbox documents from database
	result := db.DB.Where("rss_feed_id = ? AND user_id = ? AND status = ?", id, userId, "inbox").Delete(&db.Document{})
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	// Delete the feed itself
	result = db.DB.Delete(&db.RSSFeed{}, id)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	return c.JSON(200, echo.Map{"id": id, "message": "feed deleted", "deletedDocs": len(inboxDocs)})
}

func (h *RSSHandler) SyncFeed(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var feed db.RSSFeed
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&feed).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	result := h.syncFeedInternal(&feed)
	return c.JSON(200, result)
}

// SyncResult represents the result of a feed sync operation
type SyncResult struct {
	FeedID         uint   `json:"feedId"`
	FeedName       string `json:"feedName"`
	NewArticles    int    `json:"newArticles"`
	Total          int    `json:"total"`
	DownloadErrors int    `json:"downloadErrors"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
}

// syncFeedInternal performs the actual sync without HTTP context
func (h *RSSHandler) syncFeedInternal(feed *db.RSSFeed) SyncResult {
	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rssFeed, err := fp.ParseURLWithContext(feed.URL, ctx)
	if err != nil {
		return SyncResult{
			FeedID:   feed.ID,
			FeedName: feed.Name,
			Error:    "failed to parse RSS feed: " + err.Error(),
		}
	}

	feedDir := filepath.Join(h.DataDir, "raw", "rss", sanitizeFilename(feed.Name))
	assetsDir := filepath.Join(feedDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return SyncResult{
			FeedID:   feed.ID,
			FeedName: feed.Name,
			Error:    "failed to create directories",
		}
	}

	// For first sync, sort by published date and limit to 5 most recent articles
	items := rssFeed.Items
	isFirstSync := feed.LastSyncAt.IsZero()
	if isFirstSync && len(items) > 5 {
		// Sort items by published date (most recent first)
		sortItemsByDate(items)
		items = items[:5]
	}

	newArticles := 0
	downloadErrors := 0
	total := len(items)

	for _, item := range items {
		normalizedURL := normalizeSourceURL(item.Link)
		guid := item.GUID
		if guid == "" {
			guid = normalizedURL
		}

		// Check dedup including soft-deleted records to prevent re-fetching deleted articles
		// Match both normalized URL and original URL (legacy records may have fragment)
		// Must scope by user_id so different users can subscribe to the same feed
		var existingDoc db.Document
		if db.DB.Unscoped().Where("(source_url = ? OR source_url = ? OR (source_guid != '' AND source_guid = ?)) AND user_id = ?", normalizedURL, item.Link, guid, feed.UserID).First(&existingDoc).Error == nil {
			continue
		}

		title := item.Title
		if title == "" {
			title = "untitled"
		}
		title = sanitizeFilename(title)

		// Download images and build content
		content, imgCount, imgErrors := buildArticleContentWithImages(item, feed.Name, assetsDir, item.Link)
		downloadErrors += imgErrors

		articlePath := filepath.Join(feedDir, title+".md")
		if err := os.WriteFile(articlePath, []byte(content), 0644); err != nil {
			continue
		}

		authorName := ""
		if item.Author != nil {
			authorName = item.Author.Name
		}
		metadata := map[string]string{
			"feedName":   feed.Name,
			"feedUrl":    feed.URL,
			"author":     authorName,
			"published":  item.Published,
			"categories": strings.Join(item.Categories, ","),
			"images":     strconv.Itoa(imgCount),
		}
		metadataJSON, _ := json.Marshal(metadata)

		// Use published date as created_at for RSS articles
		publishedTime := parsePublishedTime(item.Published)
		if publishedTime.IsZero() {
			publishedTime = time.Now()
		}

		doc := db.Document{
			UserID:     feed.UserID,
			Title:      item.Title,
			Slug:       title,
			SourceType: "rss",
			RawPath:    filepath.Join("raw", "rss", sanitizeFilename(feed.Name), title+".md"),
			SourceURL:  normalizedURL,
			SourceGUID: guid,
			RSSFeedID:  feed.ID,
			Language:   "en",
			Status:     "inbox",
			Metadata:   string(metadataJSON),
			CreatedAt:  publishedTime,
			UpdatedAt:  time.Now(),
		}

		if err := db.DB.Create(&doc).Error; err != nil {
			continue
		}

		// Auto-create tags from RSS categories
		if len(item.Categories) > 0 {
			for _, catName := range item.Categories {
				catName = strings.TrimSpace(catName)
				if catName == "" {
					continue
				}
				var tag db.Tag
				result := db.DB.Where("name = ? AND user_id = ?", catName, feed.UserID).First(&tag)
				if result.Error != nil {
					tag = db.Tag{
						Name:   catName,
						Color:  "#808080",
						UserID: feed.UserID,
					}
					if err := db.DB.Create(&tag).Error; err != nil {
						continue
					}
				}
				docTag := db.DocumentTag{
					DocumentID: doc.ID,
					TagID:      tag.ID,
				}
				db.DB.Create(&docTag) // Ignore error for duplicate association
			}
		}

		// Generate summary asynchronously if ClaudeBin is configured
		if h.ClaudeBin != "" {
			docID := doc.ID
			rawPath := doc.RawPath
			go func() {
				summary, err := ingest.GenerateSummary(h.DataDir, rawPath, h.ClaudeBin)
				if err != nil {
					fmt.Printf("[api] summary generation failed for RSS article %d: %v\n", docID, err)
				} else {
					db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
					fmt.Printf("[api] summary generated for RSS article %d\n", docID)
				}
			}()
		}

		newArticles++
	}

	feed.LastSyncAt = time.Now()
	db.DB.Save(&feed)

	msg := fmt.Sprintf("Synced %d new articles from %s", newArticles, feed.Name)
	if downloadErrors > 0 {
		msg += fmt.Sprintf(" (%d image download errors)", downloadErrors)
	}

	return SyncResult{
		FeedID:         feed.ID,
		FeedName:       feed.Name,
		NewArticles:    newArticles,
		Total:          total,
		DownloadErrors: downloadErrors,
		Message:        msg,
	}
}

// StartAutoSyncScheduler starts a background scheduler that syncs feeds with autoSync enabled
// It checks every hour and syncs feeds that haven't been synced in the last hour
func (h *RSSHandler) StartAutoSyncScheduler() {
	go func() {
		// Check every hour
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		fmt.Println("[rss] Auto-sync scheduler started, checking every hour")

		for range ticker.C {
			h.syncAutoSyncFeeds()
		}
	}()
}

// syncAutoSyncFeeds syncs all feeds that have autoSync enabled and need syncing
func (h *RSSHandler) syncAutoSyncFeeds() {
	var feeds []db.RSSFeed
	if err := db.DB.Where("auto_sync = ?", true).Find(&feeds).Error; err != nil {
		fmt.Printf("[rss] Failed to query auto-sync feeds: %v\n", err)
		return
	}

	if len(feeds) == 0 {
		return
	}

	fmt.Printf("[rss] Checking %d auto-sync feeds...\n", len(feeds))

	minSyncInterval := 1 * time.Hour
	for _, feed := range feeds {
		// Skip if synced recently (within the last hour)
		if !feed.LastSyncAt.IsZero() && time.Since(feed.LastSyncAt) < minSyncInterval {
			continue
		}

		fmt.Printf("[rss] Auto-syncing feed: %s (%s)\n", feed.Name, feed.URL)
		result := h.syncFeedInternal(&feed)
		if result.Error != "" {
			fmt.Printf("[rss] Auto-sync failed for %s: %s\n", feed.Name, result.Error)
		} else {
			fmt.Printf("[rss] Auto-sync completed for %s: %s\n", feed.Name, result.Message)
		}
	}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "*", "-")
	name = strings.ReplaceAll(name, "?", "-")
	name = strings.ReplaceAll(name, "\"", "-")
	name = strings.ReplaceAll(name, "<", "-")
	name = strings.ReplaceAll(name, ">", "-")
	name = strings.ReplaceAll(name, "|", "-")
	name = strings.ReplaceAll(name, "'", "")
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// normalizeSourceURL removes tracking parameters and fragments from a URL
// so that the same article with different tracking params is recognized as the same
func normalizeSourceURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	trackingParams := []string{
		"utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term",
		"fbclid", "gclid", "gclsrc",
		"ref", "ref_src", "ref_url",
		"ts", "_ts", "timestamp",
		"share_token", "ncid", "icmp",
		"spm",        // Alibaba tracking
		"from",       // WeChat/QQ tracking
		"isappinstalled",
		"nsukey",
	}
	q := u.Query()
	for _, param := range trackingParams {
		q.Del(param)
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""

	return u.String()
}

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

// tryParseAsRSS attempts to parse URL directly as RSS feed
func tryParseAsRSS(feedURL string) (*gofeed.Feed, error) {
	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return fp.ParseURLWithContext(feedURL, ctx)
}

// sortItemsByDate sorts RSS items by published date (most recent first)
func sortItemsByDate(items []*gofeed.Item) {
	// Sort in descending order (most recent first)
	sort.Slice(items, func(i, j int) bool {
		// Parse published dates
		timeI := parsePublishedTime(items[i].Published)
		timeJ := parsePublishedTime(items[j].Published)
		return timeI.After(timeJ) // Most recent first
	})
}

// parsePublishedTime parses various RSS date formats
func parsePublishedTime(dateStr string) time.Time {
	if dateStr == "" {
		return time.Time{} // Zero time for items without date
	}

	// Common RSS date formats
	formats := []string{
		time.RFC3339,
		"Mon, 02 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05+00:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{} // Return zero time if parsing fails
}

func buildArticleContentWithImages(item *gofeed.Item, feedName, assetsDir, articleURL string) (string, int, int) {
	var content strings.Builder
	imgCount := 0
	imgErrors := 0

	content.WriteString(fmt.Sprintf("# %s\n\n", item.Title))
	content.WriteString(fmt.Sprintf("**Source:** %s\n", feedName))
	if item.Author != nil && item.Author.Name != "" {
		content.WriteString(fmt.Sprintf("**Author:** %s\n", item.Author.Name))
	}
	if item.Published != "" {
		content.WriteString(fmt.Sprintf("**Published:** %s\n", item.Published))
	}
	content.WriteString(fmt.Sprintf("**Link:** %s\n\n", item.Link))

	// Download cover image if exists
	if item.Image != nil && item.Image.URL != "" {
		localPath, err := downloadImageToAssets(item.Image.URL, assetsDir, articleURL)
		if err == nil {
			content.WriteString(fmt.Sprintf("![%s](assets/%s)\n\n", item.Image.Title, filepath.Base(localPath)))
			imgCount++
		} else {
			imgErrors++
		}
	}

	// Download image enclosures
	for _, enc := range item.Enclosures {
		if strings.HasPrefix(enc.Type, "image/") {
			localPath, err := downloadImageToAssets(enc.URL, assetsDir, articleURL)
			if err == nil {
				content.WriteString(fmt.Sprintf("![image](assets/%s)\n\n", filepath.Base(localPath)))
				imgCount++
			} else {
				imgErrors++
			}
		}
	}

	// Determine feed content for length check
	feedContentHTML := ""
	if item.Content != "" {
		feedContentHTML = item.Content
	} else if item.Description != "" {
		feedContentHTML = item.Description
	}

	// Always try to fetch full article if Link exists
	// Many feeds provide excerpts that look substantial but are incomplete
	if item.Link != "" {
		fullContent, imgs, errs, err := fetchFullArticleContent(item.Link, assetsDir)
		if err == nil {
			fullText := stripHTML(fullContent)
			feedText := stripHTML(feedContentHTML)
			// Use full article if it's at least 50% longer than feed content
			// This catches cases where feed excerpt is 700 chars but full article is 10KB+
			if len(fullText) > len(feedText)*3/2 {
				content.WriteString("## Content\n\n")
				content.WriteString(fullContent)
				content.WriteString("\n\n")
				imgCount += imgs
				imgErrors += errs
				return content.String(), imgCount, imgErrors
			}
		}
		// Fall through to use feed content if fetch fails or full article not substantially longer
	}

	// Use feed content (Content or Description)
	if feedContentHTML != "" {
		content.WriteString("## Content\n\n")
		processedContent, imgs, errs := processHTMLImages(feedContentHTML, assetsDir, articleURL)
		content.WriteString(processedContent)
		content.WriteString("\n\n")
		imgCount += imgs
		imgErrors += errs
	}

	return content.String(), imgCount, imgErrors
}

func downloadImageToAssets(imgURL, assetsDir, articleURL string) (string, error) {
	// Resolve relative URLs
	if !strings.HasPrefix(imgURL, "http://") && !strings.HasPrefix(imgURL, "https://") {
		base, err := url.Parse(articleURL)
		if err != nil {
			return "", err
		}
		rel, err := url.Parse(imgURL)
		if err != nil {
			return "", err
		}
		imgURL = base.ResolveReference(rel).String()
	}

	// Generate filename from URL
	filename := filepath.Base(imgURL)
	filename = sanitizeFilename(filename)
	if filename == "" || filename == "." {
		filename = fmt.Sprintf("image_%d", time.Now().UnixNano())
	}
	// Ensure unique filename
	localPath := filepath.Join(assetsDir, filename)
	if _, err := os.Stat(localPath); err == nil {
		ext := filepath.Ext(filename)
		filename = fmt.Sprintf("%s_%d%s", strings.TrimSuffix(filename, ext), time.Now().UnixNano(), ext)
		localPath = filepath.Join(assetsDir, filename)
	}

	// SSRF: validate resolved IPs before downloading
	parsed, err := url.Parse(imgURL)
	if err != nil {
		return "", err
	}
	ips, err := net.LookupIP(parsed.Hostname())
	if err != nil {
		return "", fmt.Errorf("cannot resolve host")
	}
	for _, ip := range ips {
		if isPrivateIP(ip.To4()) || isPrivateIP(ip.To16()) {
			return "", fmt.Errorf("download from private network is not allowed")
		}
	}

	// Download with timeout
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(imgURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Write file
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return "", err
	}

	return localPath, nil
}

func processHTMLToMarkdown(htmlContent, assetsDir, articleURL string) (string, int, int) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent, 0, 0
	}

	imgCount := 0
	imgErrors := 0

	// Download images first and replace with markdown syntax
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if !exists || src == "" {
			return
		}

		alt, _ := s.Attr("alt")
		if alt == "" {
			alt = "image"
		}

		// Skip images already saved locally (e.g. cid: attachments replaced by extractHTMLFromEmail)
		if strings.HasPrefix(src, "assets/") {
			mdImg := fmt.Sprintf("![%s](%s)", alt, src)
			s.ReplaceWithHtml(mdImg)
			imgCount++
			return
		}

		localPath, err := downloadImageToAssets(src, assetsDir, articleURL)
		if err != nil {
			imgErrors++
			s.Remove()
			return
		}

		imgCount++
		mdImg := fmt.Sprintf("![%s](assets/%s)", alt, filepath.Base(localPath))
		s.ReplaceWithHtml(mdImg)
	})

	// Convert HTML to markdown by processing the body content
	var markdown strings.Builder
	doc.Find("body").Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	result := markdown.String()
	// Clean up excessive whitespace
	result = strings.TrimSpace(result)
	return result, imgCount, imgErrors
}

// isInsideLink checks if the element is nested inside an <a> tag
func isInsideLink(s *goquery.Selection) bool {
	parent := s.Parent()
	for parent.Length() > 0 {
		if parent.Nodes[0].Data == "a" {
			return true
		}
		parent = parent.Parent()
	}
	return false
}

func convertNodeToMarkdown(s *goquery.Selection) string {
	node := s.Nodes[0]
	if node.Type == html.TextNode {
		text := node.Data
		// Collapse multiple spaces/tabs into single space
		text = strings.ReplaceAll(text, "\t", " ")
		for strings.Contains(text, "  ") {
			text = strings.ReplaceAll(text, "  ", " ")
		}
		// Remove newlines within text
		text = strings.ReplaceAll(text, "\n", " ")
		// Trim only if it's all whitespace
		if strings.TrimSpace(text) == "" {
			return ""
		}
		return text
	}
	if node.Type != html.ElementNode {
		return ""
	}

	tag := node.Data

	// Handle img tag specially - it has no children
	if tag == "img" {
		src, _ := s.Attr("src")
		alt, _ := s.Attr("alt")
		if src == "" {
			return ""
		}
		if alt == "" {
			alt = "image"
		}
		return fmt.Sprintf("![%s](%s)\n\n", alt, src)
	}

	// Determine if this is an inline element
	inlineElements := []string{"code", "strong", "b", "em", "i", "a", "span"}
	isInline := false
	for _, inline := range inlineElements {
		if tag == inline {
			isInline = true
			break
		}
	}

	// Check if we're inside a link - elements inside links should be treated as inline for spacing
	isInsideLinkContext := isInsideLink(s)
	// If this element is a link itself, its children should be treated as inline for spacing
	isLinkElement := tag == "a"

	innerContent := ""
	// Process children, preserving spaces between inline elements
	children := s.Contents()
	childrenCount := children.Length()
	children.Each(func(i int, child *goquery.Selection) {
		childNode := child.Nodes[0]
		childText := convertNodeToMarkdown(child)

		// If this is a whitespace-only text node between two inline elements, preserve a space
		if childNode.Type == html.TextNode && strings.TrimSpace(childNode.Data) == "" {
			// Check if previous sibling is inline
			prevIsInline := false
			if i > 0 {
				prevNode := children.Eq(i - 1).Nodes[0]
				if prevNode.Type == html.ElementNode {
					if isInsideLinkContext || isLinkElement {
						prevIsInline = true // All elements inside link are inline for spacing
					} else {
						for _, inline := range inlineElements {
							if prevNode.Data == inline {
								prevIsInline = true
								break
							}
						}
					}
				}
			}
			// Check if next sibling is inline
			nextIsInline := false
			if i < childrenCount - 1 {
				nextNode := children.Eq(i + 1).Nodes[0]
				if nextNode.Type == html.ElementNode {
					if isInsideLinkContext || isLinkElement {
						nextIsInline = true // All elements inside link are inline for spacing
					} else {
						for _, inline := range inlineElements {
							if nextNode.Data == inline {
								nextIsInline = true
								break
							}
						}
					}
				}
			}
			// Preserve space between inline elements
			if prevIsInline && nextIsInline {
				childText = " "
			}
		}

		innerContent += childText

			// If this child is an inline element with content and next sibling is also inline,
			// add a space (CSS flex gap or similar visual separation has no text node)
			if childNode.Type == html.ElementNode && childText != "" {
				thisIsInline := false
				if isInsideLinkContext || isLinkElement {
					thisIsInline = true // All elements inside link are inline for spacing
				} else {
					for _, inline := range inlineElements {
						if childNode.Data == inline {
							thisIsInline = true
							break
						}
					}
				}
				if thisIsInline && i < childrenCount - 1 {
					nextNode := children.Eq(i + 1).Nodes[0]
					if nextNode.Type == html.ElementNode {
						nextIsInline := false
						if isInsideLinkContext || isLinkElement {
							nextIsInline = true // All elements inside link are inline for spacing
						} else {
							for _, inline := range inlineElements {
								if nextNode.Data == inline {
									nextIsInline = true
									break
								}
							}
						}
						if nextIsInline {
							innerContent += " "
						}
					} else if nextNode.Type == html.TextNode && strings.TrimSpace(nextNode.Data) != "" {
						// Next sibling is a text node with content - add space if it doesn't already start with space
						if len(nextNode.Data) > 0 && nextNode.Data[0] != ' ' && nextNode.Data[0] != '\n' {
							innerContent += " "
						}
					}
				}
			}
		})

	// For inline elements, don't trim - preserve spaces around them
	// For block elements, trim to clean up whitespace
	// When inside a link, treat all elements as inline for spacing/trimming purposes
	if !isInline && !isInsideLinkContext {
		innerContent = strings.TrimSpace(innerContent)
	}

	switch tag {
	case "p":
		return innerContent + "\n\n"
	case "br":
		return "\n"
	case "h1":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		// WeChat uses h1/h2 as decorative containers for long paragraphs.
		// Treat as paragraph if content exceeds 80 chars (not a real heading).
		if len([]rune(trimmed)) > 80 {
			return trimmed + "\n\n"
		}
		return "# " + trimmed + "\n\n"
	case "h2":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		if len([]rune(trimmed)) > 80 {
			return trimmed + "\n\n"
		}
		return "## " + trimmed + "\n\n"
	case "h3":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		if len([]rune(trimmed)) > 80 {
			return trimmed + "\n\n"
		}
		return "### " + trimmed + "\n\n"
	case "h4":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		return "#### " + trimmed + "\n\n"
	case "h5":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		return "##### " + trimmed + "\n\n"
	case "h6":
		if isInsideLink(s) {
			return innerContent
		}
		trimmed := strings.TrimSpace(innerContent)
		if trimmed == "" {
			return ""
		}
		return "###### " + trimmed + "\n\n"
	case "strong", "b":
		return "**" + innerContent + "**"
	case "em", "i":
		return "*" + innerContent + "*"
	case "code":
		// Check if this code is inside a pre tag - if so, don't wrap with backticks
		// The pre tag handler will add the code block syntax
		parent := s.Parent()
		if parent.Length() > 0 && parent.Nodes[0].Data == "pre" {
			return innerContent
		}
		return "`" + innerContent + "`"
	case "pre":
		// Check if pre contains a code tag with language class
		codeEl := s.Find("code")
		language := ""
		if codeEl.Length() > 0 {
			// Look for language class like "language-go" or "go"
			classes, _ := codeEl.Attr("class")
			for _, class := range strings.Split(classes, " ") {
				if strings.HasPrefix(class, "language-") {
					language = strings.TrimPrefix(class, "language-")
					break
				}
				// Common language class names without prefix
				if class != "" && !strings.HasPrefix(class, "hljs") && class != "highlight" {
					language = class
				}
			}
		}
		// Fallback: check <pre> tag's own class for language
		if language == "" {
			preClasses, _ := s.Attr("class")
			for _, class := range strings.Split(preClasses, " ") {
				if strings.HasPrefix(class, "language-") {
					lang := strings.TrimPrefix(class, "language-")
					if lang != "" {
						language = lang
						break
					}
				}
			}
		}
		// Get raw text content preserving newlines (don't use convertNodeToMarkdown)
		// For code inside pre, we need to preserve the original formatting
		codeContent := ""
		if codeEl.Length() > 0 {
			// Get text from the code element directly
			codeContent = codeEl.Text()
		} else {
			// Get text from pre directly
			codeContent = s.Text()
		}
		// Strip common leading indentation while preserving relative indentation
		// Find the minimum indentation among non-empty lines
		lines := strings.Split(codeContent, "\n")
		minIndent := -1
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue // Skip empty lines
			}
			indent := 0
			for _, ch := range line {
				if ch == ' ' || ch == '\t' {
					indent++
				} else {
					break
				}
			}
			if minIndent == -1 || indent < minIndent {
				minIndent = indent
			}
		}
		// Remove the common minimum indentation from each line
		if minIndent > 0 {
			for i, line := range lines {
				if strings.TrimSpace(line) != "" && len(line) >= minIndent {
					lines[i] = line[minIndent:]
				}
			}
		}
		codeContent = strings.Join(lines, "\n")
		return fmt.Sprintf("\n```%s\n%s\n```\n\n", language, strings.TrimSpace(codeContent))
	case "blockquote":
		lines := strings.Split(innerContent, "\n")
		var result strings.Builder
		for _, line := range lines {
			result.WriteString("> " + line + "\n")
		}
		return result.String() + "\n"
	case "a":
		href, _ := s.Attr("href")
		if href == "" {
			return innerContent
		}
		// Skip empty links (anchor links without text content)
		if strings.TrimSpace(innerContent) == "" {
			return ""
		}
		return "[" + innerContent + "](" + href + ")"
	case "ul":
		var result strings.Builder
		s.Children().Each(func(i int, li *goquery.Selection) {
			liContent := ""
			li.Contents().Each(func(j int, child *goquery.Selection) {
				liContent += convertNodeToMarkdown(child)
			})
			result.WriteString("- " + strings.TrimSpace(liContent) + "\n")
		})
		return result.String() + "\n"
	case "ol":
		var result strings.Builder
		s.Children().Each(func(i int, li *goquery.Selection) {
			liContent := ""
			li.Contents().Each(func(j int, child *goquery.Selection) {
				liContent += convertNodeToMarkdown(child)
			})
			result.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(liContent)))
		})
		return result.String() + "\n"
	case "li":
		// Already handled by ul/ol
		return ""
	case "div", "section", "article":
		// Double newline for paragraph separation between blocks
		if innerContent != "" {
			return innerContent + "\n\n"
		}
		return ""
	case "span":
		// Span is inline, don't add line break
		return innerContent
	case "hr":
		return "\n---\n\n"
	case "small":
		return innerContent
	case "details":
		return innerContent + "\n"
	case "summary":
		return "**" + innerContent + "**\n\n"
	case "table":
		// Detect if this is a layout table (email newsletter style) vs data table
		// Layout tables typically have: no thead, single column, cellpadding="0", used for spacing
		// Data tables have: thead/th elements, multiple columns with actual data

		// Check for layout table indicators
		thead := s.Find("thead")
		hasThead := thead.Length() > 0

		// Check if any row has th elements (indicates data table)
		hasThElements := s.Find("th").Length() > 0

		// Count columns in first data row
		allRows := s.Find("tr")
		colCount := 0
		if allRows.Length() > 0 {
			colCount = allRows.First().Find("td, th").Length()
		}

		// Check for cellpadding="0" cellspacing="0" (common in layout tables)
		cellpadding, _ := s.Attr("cellpadding")
		cellspacing, _ := s.Attr("cellspacing")
		isLayoutStyle := cellpadding == "0" && cellspacing == "0"

		// Determine if this is a layout table
		// Layout table: no thead, no th, single column, or explicit layout style
		isLayoutTable := !hasThead && !hasThElements && (colCount <= 1 || isLayoutStyle)

		if isLayoutTable {
			// Layout table: just extract text content, don't format as markdown table
			return innerContent + "\n\n"
		}

		// Data table: convert to markdown format
		var result strings.Builder
		var headerCells []string
		var bodyRows [][]string

		// tableCellText extracts text from a cell, replacing newlines with <br>
		// so markdown table rows stay on a single line
		tableCellText := func(cell *goquery.Selection) string {
			text := strings.TrimSpace(cell.Text())
			text = strings.ReplaceAll(text, "\n", "<br>")
			return text
		}

		// Extract header cells from <thead> or first <tr>
		if hasThead {
			thead.Find("tr th, tr td").Each(func(i int, cell *goquery.Selection) {
				headerCells = append(headerCells, tableCellText(cell))
			})
		} else {
			// Check if first row has th elements
			firstRow := s.Find("tr").First()
			if firstRow.Length() > 0 {
				firstRow.Find("th").Each(func(i int, cell *goquery.Selection) {
					headerCells = append(headerCells, tableCellText(cell))
				})
				// If no th found, check for td (some tables use td for headers)
				if len(headerCells) == 0 {
					firstRow.Find("td").Each(func(i int, cell *goquery.Selection) {
						headerCells = append(headerCells, tableCellText(cell))
					})
				}
			}
		}

		// Extract body rows
		if hasThead {
			// Explicit thead: body rows are only in tbody (implicit or explicit)
			s.Find("tbody tr").Each(func(i int, row *goquery.Selection) {
				var cells []string
				row.Find("td, th").Each(func(j int, cell *goquery.Selection) {
					cells = append(cells, tableCellText(cell))
				})
				if len(cells) > 0 {
					bodyRows = append(bodyRows, cells)
				}
			})
		} else {
			// No explicit thead: first row is header, rest are body
			// Use Slice to skip first row regardless of tbody presence
			allRows.Slice(1, allRows.Length()).Each(func(i int, row *goquery.Selection) {
				var cells []string
				row.Find("td, th").Each(func(j int, cell *goquery.Selection) {
					cells = append(cells, tableCellText(cell))
				})
				if len(cells) > 0 {
					bodyRows = append(bodyRows, cells)
				}
			})
		}

		// Build markdown table
		if len(headerCells) > 0 {
			// Header row
			result.WriteString("|")
			for _, cell := range headerCells {
				result.WriteString(" " + cell + " |")
			}
			result.WriteString("\n")

			// Separator row
			result.WriteString("|")
			for range headerCells {
				result.WriteString("---|")
			}
			result.WriteString("\n")
		}

		// Body rows
		for _, row := range bodyRows {
			result.WriteString("|")
			for _, cell := range row {
				result.WriteString(" " + cell + " |")
			}
			result.WriteString("\n")
		}

		return result.String() + "\n"
	default:
		// Unknown tags: keep content if non-empty
		if innerContent != "" {
			return innerContent
		}
		return ""
	}
}

// stripHTML removes HTML tags and returns plain text for length checking
func stripHTML(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}
	return doc.Text()
}

// fetchFullArticleContent fetches the full article HTML from URL,
// extracts main content, downloads images, and converts to markdown
func fetchFullArticleContent(articleURL, assetsDir string) (string, int, int, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(articleURL)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, 0, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", 0, 0, err
	}

	// Remove non-content elements
	doc.Find("script, style, nav, .Header, .Footer, .NavigationDrawer, aside, .sidebar, .navigation, .menu, .ads").Remove()
	doc.Find(".Cookie-notice, .cookie-notice, .js-cookieNotice").Remove()

	// Find main content area (reuse extractContent selectors from web.go)
	var contentNode *goquery.Selection
	selectors := []string{
		".Article",
		".Blog-content",
		"article",
		"main",
		".content",
		".post",
		"#content",
		"#main",
	}
	for _, sel := range selectors {
		if doc.Find(sel).Length() > 0 {
			contentNode = doc.Find(sel).First()
			break
		}
	}
	if contentNode == nil {
		contentNode = doc.Find("body")
	}

	// Get HTML of content node and process
	contentHTML, err := contentNode.Html()
	if err != nil {
		return "", 0, 0, err
	}

	// Use existing processHTMLToMarkdown (handles images + conversion)
	markdown, imgs, errs := processHTMLToMarkdown(contentHTML, assetsDir, articleURL)
	return markdown, imgs, errs, nil
}

// processHTMLImages is kept for backward compatibility but now uses the markdown converter
func processHTMLImages(htmlContent, assetsDir, articleURL string) (string, int, int) {
	return processHTMLToMarkdown(htmlContent, assetsDir, articleURL)
}