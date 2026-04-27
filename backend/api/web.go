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
	// Also remove navigation links, social icons, and other non-content elements
	doc.Find("script, style, nav, .Header, .Footer, .NavigationDrawer, aside, .sidebar, .navigation, .menu, .ads").Remove()

	// Remove cookie notices and other non-content
	doc.Find(".Cookie-notice, .cookie-notice, .js-cookieNotice").Remove()

	// Try to find main content area - prioritize specific selectors
	var contentNode *goquery.Selection
	// Try multiple selectors in order of specificity
	selectors := []string{
		".Article",           // Go blog
		".Blog-content",      // Go blog alternative
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

	// Convert HTML to markdown using the same logic as RSS
	var markdown strings.Builder
	contentNode.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	content := markdown.String()

	// Clean up excessive blank lines (more than 2 consecutive)
	content = cleanExcessiveWhitespace(content)

	return strings.TrimSpace(content)
}

// extractPublishedTime extracts publication time from HTML meta tags
func extractPublishedTime(doc *goquery.Document) time.Time {
	// Common meta tag names for publication time
	metaNames := []string{
		"article:published_time",
		"datePublished",
		"publish-date",
		"published",
		"date",
		"article:modified_time",
		"dateModified",
		"last-modified",
	}

	for _, name := range metaNames {
		// Try meta tag with property attribute
		if val, exists := doc.Find(fmt.Sprintf("meta[property=\"%s\"]", name)).Attr("content"); exists && val != "" {
			if t := parseWebDate(val); !t.IsZero() {
				return t
			}
		}
		// Try meta tag with name attribute
		if val, exists := doc.Find(fmt.Sprintf("meta[name=\"%s\"]", name)).Attr("content"); exists && val != "" {
			if t := parseWebDate(val); !t.IsZero() {
				return t
			}
		}
	}

	// Try to find time element with datetime attribute
	if val, exists := doc.Find("time[datetime]").Attr("datetime"); exists && val != "" {
		if t := parseWebDate(val); !t.IsZero() {
			return t
		}
	}

	return time.Time{}
}

// parseWebDate parses various web date formats
func parseWebDate(dateStr string) time.Time {
	if dateStr == "" {
		return time.Time{}
	}

	// Common web date formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05+00:00",
		"2006-01-02",
		"Mon, 02 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"January 02, 2006",
		"Jan 02, 2006",
		"02 Jan 2006",
		"2006/01/02",
		"01/02/2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{}
}

// cleanExcessiveWhitespace removes excessive blank lines, trailing whitespace
// but preserves indentation inside code blocks
func cleanExcessiveWhitespace(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	blankCount := 0
	inCodeBlock := false

	for _, line := range lines {
		// Check if entering or leaving a code block
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			// Remove trailing whitespace from the ``` line
			line = strings.TrimRight(line, " \t")
			blankCount = 0
			result = append(result, line)
			continue
		}

		// Inside code block: only trim trailing whitespace, preserve indentation
		if inCodeBlock {
			line = strings.TrimRight(line, " \t")
			result = append(result, line)
			continue
		}

		// Outside code block: trim both leading and trailing whitespace
		line = strings.TrimLeft(line, " \t")
		line = strings.TrimRight(line, " \t")

		if strings.TrimSpace(line) == "" {
			blankCount++
			// Only keep 1 blank line between content
			if blankCount <= 1 {
				result = append(result, "")
			}
		} else {
			blankCount = 0
			result = append(result, line)
		}
	}

	// Remove leading/trailing blank lines
	for len(result) > 0 && strings.TrimSpace(result[0]) == "" {
		result = result[1:]
	}
	for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
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

	// Extract published time from meta tags
	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	// Build markdown content with metadata header
	mdContent := fmt.Sprintf("---\nsource_url: %s\nsource_type: web\ntitle: %s\ndate: %s\n---\n\n%s",
		req.URL,
		doc.Find("title").Text(),
		publishedTime.Format("2006-01-02"),
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
		CreatedAt:  publishedTime,
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