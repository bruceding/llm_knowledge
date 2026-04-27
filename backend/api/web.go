package api

import (
	"fmt"
	"io"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net/http"
	"net/url"
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

func fetchHTML(urlStr string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(urlStr)
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

// extractImageURLs extracts all image URLs from an HTML document
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

// resolveURL resolves a potentially relative URL against a base URL
func resolveURL(imgURL, baseURL string) string {
	if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
		return imgURL
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return imgURL
	}

	img, err := url.Parse(imgURL)
	if err != nil {
		return imgURL
	}

	return base.ResolveReference(img).String()
}

// downloadImage downloads an image and saves it to the specified path
func downloadImage(imgURL, savePath string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(imgURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return os.WriteFile(savePath, data, 0644)
}

// getImageExtension extracts extension from URL or content-type
func getImageExtension(imgURL string) string {
	// Try to get extension from URL path
	u, err := url.Parse(imgURL)
	if err == nil {
		path := u.Path
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"} {
			if strings.HasSuffix(strings.ToLower(path), ext) {
				return ext
			}
		}
	}
	// Default to .png
	return ".png"
}

// extractContent extracts clean text content from HTML for markdown
func extractContent(doc *goquery.Document) string {
	// Remove script, style, nav, header, footer, sidebar elements
	doc.Find("script, style, nav, header, footer, aside, .sidebar, .navigation, .menu, .ads").Remove()

	// Try to find main content area
	var content string
	mainContent := doc.Find("main, article, .content, .post, .article, #content, #main")
	if mainContent.Length() > 0 {
		content = mainContent.Text()
	} else {
		content = doc.Find("body").Text()
	}

	// Clean up whitespace
	content = strings.TrimSpace(content)
	// Replace multiple whitespace with single space
	content = strings.Join(strings.Fields(content), " ")

	return content
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

	// Extract title
	title := doc.Find("title").Text()
	if title == "" {
		title = "untitled"
	}
	// Clean title for filesystem
	title = strings.ReplaceAll(title, "/", "-")
	title = strings.ReplaceAll(title, ":", "-")
	title = strings.ReplaceAll(title, " ", "-")
	// Remove other problematic characters
	title = strings.ReplaceAll(title, "?", "")
	title = strings.ReplaceAll(title, "*", "")
	title = strings.ReplaceAll(title, "|", "")

	// Create directory
	dir := filepath.Join(h.DataDir, "raw", "web", title)
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directory"})
	}

	// Download images and replace URLs in HTML
	imgURLs := extractImageURLs(doc)
	downloadedImages := 0
	imgMap := make(map[string]string) // original URL -> local path

	for i, imgURL := range imgURLs {
		// Resolve relative URLs
		absoluteURL := resolveURL(imgURL, req.URL)

		// Generate local filename
		ext := getImageExtension(absoluteURL)
		localName := fmt.Sprintf("img_%d%s", i+1, ext)
		localPath := filepath.Join(assetsDir, localName)
		localRelPath := filepath.Join("assets", localName)

		// Download image
		if err := downloadImage(absoluteURL, localPath); err == nil {
			imgMap[imgURL] = localRelPath
			downloadedImages++
		}
	}

	// Replace image URLs in HTML with local paths
	if len(imgMap) > 0 {
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("src")
			if exists && imgMap[src] != "" {
				s.SetAttr("src", imgMap[src])
			}
		})
	}

	// Save modified HTML to index.html
	modifiedHTML, err := doc.Html()
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to generate modified HTML"})
	}
	htmlPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(modifiedHTML), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save HTML"})
	}

	// Extract content and save to paper.md
	content := extractContent(doc)
	mdPath := filepath.Join(dir, "paper.md")

	// Build markdown content with metadata header
	mdContent := fmt.Sprintf("---\nsource_url: %s\nsource_type: web\ntitle: %s\ndate: %s\n---\n\n%s",
		req.URL,
		doc.Find("title").Text(),
		time.Now().Format("2006-01-02"),
		content)

	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save markdown"})
	}

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

	// Capture docID before goroutine
	docID := docRecord.ID

	// Trigger async summary generation if ClaudeBin is configured
	if h.ClaudeBin != "" {
		go func() {
			summary, err := ingest.GenerateSummary(h.DataDir, rawRelPath, h.ClaudeBin)
			if err != nil {
				fmt.Printf("[api] summary generation failed for %s: %v\n", title, err)
			} else {
				db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
				fmt.Printf("[api] summary generated for %s\n", title)
			}
		}()
	}

	return c.JSON(200, echo.Map{
		"id":       docRecord.ID,
		"title":    title,
		"path":     dir,
		"url":      req.URL,
		"images":   downloadedImages,
		"htmlPath": filepath.Join(rawRelPath, "index.html"),
		"mdPath":   filepath.Join(rawRelPath, "paper.md"),
		"message":  "Web page saved successfully",
	})
}