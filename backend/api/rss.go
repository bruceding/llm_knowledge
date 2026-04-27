package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-knowledge/db"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/echo/v4"
	"github.com/mmcdole/gofeed"
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

	feed := db.RSSFeed{
		Name:      req.Name,
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

	result := db.DB.Delete(&db.RSSFeed{}, id)
	if result.Error != nil {
		return c.JSON(500, echo.Map{"error": result.Error.Error()})
	}

	return c.JSON(200, echo.Map{"id": id, "message": "feed deleted"})
}

func (h *RSSHandler) SyncFeed(c echo.Context) error {
	id := c.Param("id")

	var feed db.RSSFeed
	if err := db.DB.First(&feed, id).Error; err != nil {
		return c.JSON(404, echo.Map{"error": "feed not found"})
	}

	fp := gofeed.NewParser()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rssFeed, err := fp.ParseURLWithContext(feed.URL, ctx)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to parse RSS feed: " + err.Error()})
	}

	feedDir := filepath.Join(h.DataDir, "raw", "rss", sanitizeFilename(feed.Name))
	assetsDir := filepath.Join(feedDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directories"})
	}

	newArticles := 0
	downloadErrors := 0
	total := len(rssFeed.Items)

	for _, item := range rssFeed.Items {
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

		doc := db.Document{
			Title:      item.Title,
			SourceType: "rss",
			RawPath:    filepath.Join("raw", "rss", sanitizeFilename(feed.Name), title+".md"),
			SourceURL:  item.Link,
			RSSFeedID:  feed.ID,
			Language:   "en",
			Status:     "inbox",
			Metadata:   string(metadataJSON),
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		if err := db.DB.Create(&doc).Error; err != nil {
			continue
		}
		newArticles++
	}

	feed.LastSyncAt = time.Now()
	db.DB.Save(&feed)

	msg := fmt.Sprintf("Synced %d new articles from %s", newArticles, feed.Name)
	if downloadErrors > 0 {
		msg += fmt.Sprintf(" (%d image download errors)", downloadErrors)
	}

	return c.JSON(200, echo.Map{
		"feedId":          feed.ID,
		"feedName":        feed.Name,
		"newArticles":     newArticles,
		"total":           total,
		"downloadErrors":  downloadErrors,
		"message":         msg,
	})
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
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		name = name[:100]
	}
	return name
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

func processHTMLImages(htmlContent, assetsDir, articleURL string) (string, int, int) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		// If parsing fails, return original content
		return htmlContent, 0, 0
	}

	imgCount := 0
	imgErrors := 0

	// Find and replace img tags
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
			return
		}

		imgCount++
		// Replace img tag with markdown
		mdImg := fmt.Sprintf("![%s](assets/%s)", alt, filepath.Base(localPath))
		s.ReplaceWithHtml(mdImg)
	})

	// Convert back to text (markdown-friendly)
	processedContent, err := doc.Html()
	if err != nil {
		return htmlContent, imgCount, imgErrors
	}

	// Clean up extra HTML artifacts
	processedContent = strings.ReplaceAll(processedContent, "<html><head></head><body>", "")
	processedContent = strings.ReplaceAll(processedContent, "</body></html>", "")

	return processedContent, imgCount, imgErrors
}