package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
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
	var req AddRSSFeedRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	// If name not provided, parse RSS feed to get title
	feedName := req.Name
	if feedName == "" {
		fp := gofeed.NewParser()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		rssFeed, err := fp.ParseURLWithContext(req.URL, ctx)
		if err != nil {
			// Fallback: extract domain name from URL
			u, parseErr := url.Parse(req.URL)
			if parseErr != nil {
				feedName = "RSS Feed"
			} else {
				feedName = u.Host
				// Remove common prefixes like www.
				feedName = strings.TrimPrefix(feedName, "www.")
			}
		} else {
			feedName = rssFeed.Title
			if feedName == "" {
				u, _ := url.Parse(req.URL)
				feedName = strings.TrimPrefix(u.Host, "www.")
			}
		}
	}
	feedName = sanitizeFilename(feedName)

	feed := db.RSSFeed{
		Name:      feedName,
		URL:       req.URL,
		AutoSync:  req.AutoSync,
		CreatedAt: time.Now(),
	}

	if err := db.DB.Create(&feed).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create feed"})
	}

	return c.JSON(200, feed)
}

func (h *RSSHandler) ListFeeds(c echo.Context) error {
	var feeds []db.RSSFeed
	result := db.DB.Order("created_at desc").Find(&feeds)
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
	id := c.Param("id")

	// Get feed to determine directory path
	var feed db.RSSFeed
	if err := db.DB.First(&feed, id).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	// Find all inbox documents for this feed
	var inboxDocs []db.Document
	if err := db.DB.Where("rss_feed_id = ? AND status = ?", id, "inbox").Find(&inboxDocs).Error; err != nil {
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
	result := db.DB.Where("rss_feed_id = ? AND status = ?", id, "inbox").Delete(&db.Document{})
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
	id := c.Param("id")

	var feed db.RSSFeed
	if err := db.DB.First(&feed, id).Error; err != nil {
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
		var existingDoc db.Document
		if db.DB.Where("source_url = ?", item.Link).First(&existingDoc).Error == nil {
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
			Title:      item.Title,
			SourceType: "rss",
			RawPath:    filepath.Join("raw", "rss", sanitizeFilename(feed.Name), title+".md"),
			SourceURL:  item.Link,
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
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "*", "-")
	name = strings.ReplaceAll(name, "?", "-")
	name = strings.ReplaceAll(name, "\"", "-")
	name = strings.ReplaceAll(name, "<", "-")
	name = strings.ReplaceAll(name, ">", "-")
	name = strings.ReplaceAll(name, "|", "-")
	name = strings.ReplaceAll(name, "'", "")  // Remove single quotes (not replace with -)
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		name = name[:100]
	}
	return name
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

	if item.Description != "" {
		content.WriteString("## Summary\n\n")
		processedDesc, imgs, errs := processHTMLImages(item.Description, assetsDir, articleURL)
		content.WriteString(processedDesc)
		content.WriteString("\n\n")
		imgCount += imgs
		imgErrors += errs
	}

	if item.Content != "" {
		content.WriteString("## Content\n\n")
		processedContent, imgs, errs := processHTMLImages(item.Content, assetsDir, articleURL)
		content.WriteString(processedContent)
		imgCount += imgs
		imgErrors += errs
	} else if item.Description != "" {
		content.WriteString("## Content\n\n")
		processedDesc, imgs, errs := processHTMLImages(item.Description, assetsDir, articleURL)
		content.WriteString(processedDesc)
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
					for _, inline := range inlineElements {
						if prevNode.Data == inline {
							prevIsInline = true
							break
						}
					}
				}
			}
			// Check if next sibling is inline
			nextIsInline := false
			if i < childrenCount - 1 {
				nextNode := children.Eq(i + 1).Nodes[0]
				if nextNode.Type == html.ElementNode {
					for _, inline := range inlineElements {
						if nextNode.Data == inline {
							nextIsInline = true
							break
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
	})

	// For inline elements, don't trim - preserve spaces around them
	// For block elements, trim to clean up whitespace
	if !isInline {
		innerContent = strings.TrimSpace(innerContent)
	}

	switch tag {
	case "p":
		return innerContent + "\n\n"
	case "br":
		return "\n"
	case "h1":
		return "# " + innerContent + "\n\n"
	case "h2":
		return "## " + innerContent + "\n\n"
	case "h3":
		return "### " + innerContent + "\n\n"
	case "h4":
		return "#### " + innerContent + "\n\n"
	case "h5":
		return "##### " + innerContent + "\n\n"
	case "h6":
		return "###### " + innerContent + "\n\n"
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
				if class != "" && !strings.HasPrefix(class, "hljs") {
					language = class
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
		// Only add newline if there's actual content
		if innerContent != "" {
			return innerContent + "\n"
		}
		return ""
	case "span":
		// Span is inline, don't add line break
		return innerContent
	case "hr":
		return "\n---\n\n"
	case "small":
		return innerContent
	default:
		// Unknown tags: keep content if non-empty
		if innerContent != "" {
			return innerContent
		}
		return ""
	}
}

// processHTMLImages is kept for backward compatibility but now uses the markdown converter
func processHTMLImages(htmlContent, assetsDir, articleURL string) (string, int, int) {
	return processHTMLToMarkdown(htmlContent, assetsDir, articleURL)
}