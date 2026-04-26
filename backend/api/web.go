package api

import (
	"fmt"
	"io"
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/echo/v4"
)

type WebHandler struct {
	DataDir   string
	ClaudeBin string
}

type WebUploadRequest struct {
	URL string `json:"url"`
}

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

func parseHTML(html string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(html))
}

func extractImageURLs(doc *goquery.Document) []string {
	var urls []string
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if exists && src != "" {
			urls = append(urls, src)
		}
	})
	return urls
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
		return c.JSON(500, echo.Map{"error": "failed to fetch URL: " + err.Error()})
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

	// Create Document record
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
		"id":      docRecord.ID,
		"title":   title,
		"path":    dir,
		"url":     req.URL,
		"images":  len(imgURLs),
		"message": "Web page fetched (content processing pending)",
	})
}