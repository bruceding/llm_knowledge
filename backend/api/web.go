package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

// extractTitle extracts the page title, preferring og:title over <title>
// because platforms like X/Twitter add noise to <title> (e.g. "Username on X: ... / X")
func extractTitle(doc *goquery.Document) string {
	// Prefer og:title — usually cleaner and doesn't contain platform noise
	if ogTitle, exists := doc.Find("meta[property='og:title']").Attr("content"); exists && ogTitle != "" {
		return cleanTitle(ogTitle)
	}

	// Fallback to twitter:title
	if twTitle, exists := doc.Find("meta[name='twitter:title']").Attr("content"); exists && twTitle != "" {
		return cleanTitle(twTitle)
	}

	// Fallback to <title>
	return cleanTitle(doc.Find("title").Text())
}

// cleanTitle removes common platform noise from page titles
func cleanTitle(title string) string {
	title = strings.TrimSpace(title)

	// X/Twitter: "Username on X: \"Title\" / X" → "Title"
	if strings.Contains(title, " on X:") || strings.Contains(title, " on X: ") {
		// Pattern: "Username on X: \"Title\" / X" or "Username on X: Title / X"
		parts := strings.SplitN(title, " on X:", 2)
		if len(parts) == 2 {
			rest := strings.TrimSpace(parts[1])
			// Remove trailing " / X" or "/X" first, then quotes
			rest = strings.TrimSuffix(rest, " / X")
			rest = strings.TrimSuffix(rest, "/X")
			rest = strings.TrimSpace(rest)
			// Remove surrounding quotes if present
			rest = strings.TrimPrefix(rest, "\"")
			rest = strings.TrimPrefix(rest, "\u201c") // left double quotation mark
			rest = strings.TrimSuffix(rest, "\"")
			rest = strings.TrimSuffix(rest, "\u201d") // right double quotation mark
			rest = strings.TrimSpace(rest)
			if rest != "" {
				return rest
			}
		}
	}

	// Remove trailing " / X" suffix (can appear without "on X:" prefix)
	if strings.HasSuffix(title, " / X") {
		title = strings.TrimSuffix(title, " / X")
		title = strings.TrimSpace(title)
	}

	// Remove surrounding quotes (some platforms wrap titles in quotes)
	if (strings.HasPrefix(title, "\"") && strings.HasSuffix(title, "\"")) ||
		(strings.HasPrefix(title, "\u201c") && strings.HasSuffix(title, "\u201d")) {
		title = title[1 : len(title)-1]
		title = strings.TrimSpace(title)
	}

	return title
}

func parseHTML(html string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(html))
}

// isXTwitterURL checks if a URL is an x.com or twitter.com status URL
func isXTwitterURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	if host != "x.com" && host != "twitter.com" && host != "www.x.com" && host != "www.twitter.com" {
		return false
	}
	// Only route status URLs (e.g. /user/status/123) to the fxtwitter handler
	_, _, ok := extractXTwitterPath(urlStr)
	return ok
}

// extractXTwitterPath extracts "/screen_name/status/id" from an X/Twitter URL
func extractXTwitterPath(urlStr string) (screenName, statusID string, ok bool) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", "", false
	}
	// Expected path: /screen_name/status/1234567890
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	// parts: ["", "screen_name", "status", "id"]
	if len(parts) >= 4 && parts[2] == "status" {
		return parts[1], parts[3], true
	}
	return "", "", false
}

// fxtwitterTweet represents the tweet data from fxtwitter API
type fxtwitterTweet struct {
	URL       string `json:"url"`
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	Author    struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
	} `json:"author"`
	Article *fxtwitterArticle `json:"article"`
}

// fxtwitterArticle represents an X Article (long-form post)
type fxtwitterArticle struct {
	Title       string `json:"title"`
	PreviewText string `json:"preview_text"`
	Content     struct {
		Blocks []struct {
			Text         string                 `json:"text"`
			Type         string                 `json:"type"`
			Data         map[string]interface{} `json:"data"`
			EntityRanges []struct {
				Key    json.Number `json:"key"`
				Offset int         `json:"offset"`
				Length int         `json:"length"`
			} `json:"entityRanges"`
			InlineStyleRanges []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			} `json:"inlineStyleRanges"`
		} `json:"blocks"`
		EntityMap json.RawMessage `json:"entityMap"`
	} `json:"content"`
	CoverMedia *struct {
		MediaInfo struct {
			OriginalImgURL string `json:"original_img_url"`
		} `json:"media_info"`
	} `json:"cover_media"`
}

// fxtwitterResponse represents the response from api.fxtwitter.com
type fxtwitterResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Tweet   fxtwitterTweet `json:"tweet"`
}

// fetchXTwitterViaAPI fetches tweet data using the fxtwitter API
// which works without JavaScript rendering
func fetchXTwitterViaAPI(urlStr string) (*fxtwitterTweet, error) {
	return fetchXTwitterViaAPIWithClient(urlStr, &http.Client{Timeout: 15 * time.Second})
}

// fetchXTwitterViaAPIWithClient fetches tweet data with a configurable HTTP client (for testing)
func fetchXTwitterViaAPIWithClient(urlStr string, client *http.Client) (*fxtwitterTweet, error) {
	screenName, statusID, ok := extractXTwitterPath(urlStr)
	if !ok {
		return nil, fmt.Errorf("invalid X/Twitter URL format")
	}

	apiURL := fmt.Sprintf("https://api.fxtwitter.com/%s/status/%s", screenName, statusID)

	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fxtwitter API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fxtwitter API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read fxtwitter response: %w", err)
	}

	var fxResp fxtwitterResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&fxResp); err != nil {
		return nil, fmt.Errorf("failed to parse fxtwitter response: %w", err)
	}

	if fxResp.Code != 200 {
		return nil, fmt.Errorf("fxtwitter API error: %s", fxResp.Message)
	}

	if fxResp.Tweet.ID == "" {
		return nil, fmt.Errorf("fxtwitter returned empty tweet data (tweet may be deleted or restricted)")
	}

	return &fxResp.Tweet, nil
}

// convertArticleBlockToMarkdown converts a single fxtwitter article block to markdown
func convertArticleBlockToMarkdown(block struct {
	Text         string                 `json:"text"`
	Type         string                 `json:"type"`
	Data         map[string]interface{} `json:"data"`
	EntityRanges []struct {
		Key    json.Number `json:"key"`
		Offset int         `json:"offset"`
		Length int         `json:"length"`
	} `json:"entityRanges"`
	InlineStyleRanges []struct {
		Offset int    `json:"offset"`
		Length int    `json:"length"`
		Style  string `json:"style"`
	} `json:"inlineStyleRanges"`
}) string {
	text := block.Text

	switch block.Type {
	case "header-one":
		return "# " + text + "\n\n"
	case "header-two":
		return "## " + text + "\n\n"
	case "header-three":
		return "### " + text + "\n\n"
	case "header-four":
		return "#### " + text + "\n\n"
	case "header-five":
		return "##### " + text + "\n\n"
	case "header-six":
		return "###### " + text + "\n\n"
	case "unordered-list-item":
		return "- " + text + "\n"
	case "ordered-list-item":
		return "1. " + text + "\n"
	case "blockquote":
		return "> " + text + "\n\n"
	case "code-block":
		language := ""
		if lang, ok := block.Data["language"].(string); ok && lang != "" {
			language = lang
		}
		return fmt.Sprintf("```%s\n%s\n```\n\n", language, text)
	case "atomic":
		// Media/iframe placeholder — skip, we handle images separately
		return ""
	case "unstyled":
		// Apply inline styles (bold, italic)
		if len(block.InlineStyleRanges) > 0 {
			text = applyInlineStyles(text, block.InlineStyleRanges)
		}
		return text + "\n\n"
	default:
		return text + "\n\n"
	}
}

// inlineStyleRange represents a text style range within a block
type inlineStyleRange struct {
	Offset int
	Length int
	Style  string
}

// applyInlineStyles applies bold/italic markdown formatting based on style ranges
func applyInlineStyles(text string, ranges []struct {
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Style  string `json:"style"`
}) string {
	// Convert to simpler type for sorting
	sorted := make([]inlineStyleRange, len(ranges))
	for i, r := range ranges {
		sorted[i] = inlineStyleRange{Offset: r.Offset, Length: r.Length, Style: r.Style}
	}

	// Sort by offset descending (process from end to preserve offsets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Offset > sorted[j].Offset
	})

	for _, r := range sorted {
		start := r.Offset
		end := r.Offset + r.Length
		if end > len(text) {
			end = len(text)
		}
		if start > len(text) {
			start = len(text)
		}
		if start >= end {
			continue
		}

		snippet := text[start:end]
		switch r.Style {
		case "Bold":
			text = text[:start] + "**" + snippet + "**" + text[end:]
		case "Italic":
			text = text[:start] + "*" + snippet + "*" + text[end:]
		case "CODE":
			text = text[:start] + "`" + snippet + "`" + text[end:]
		}
	}
	return text
}

// xTwitterArticleToMarkdown converts fxtwitter article data to markdown
func xTwitterArticleToMarkdown(tweet *fxtwitterTweet) string {
	var sb strings.Builder

	if tweet.Article != nil {
		// Article title
		if tweet.Article.Title != "" {
			sb.WriteString("# ")
			sb.WriteString(tweet.Article.Title)
			sb.WriteString("\n\n")
		}

		// Article blocks
		for _, block := range tweet.Article.Content.Blocks {
			md := convertArticleBlockToMarkdown(block)
			if md != "" {
				sb.WriteString(md)
			}
		}
	} else {
		// Regular tweet — just the text
		sb.WriteString(tweet.Text)
		sb.WriteString("\n")
	}

	return cleanExcessiveWhitespace(sb.String())
}

// xTwitterPublishedTime parses the created_at field from fxtwitter
func xTwitterPublishedTime(tweet *fxtwitterTweet) time.Time {
	// Format: "Fri May 08 17:56:30 +0000 2026"
	t, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", tweet.CreatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
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
	doc.Find("script, style, nav, header, .Header, .Footer, .NavigationDrawer, aside, .sidebar, .navigation, .menu, .ads").Remove()

	// Remove cookie notices and other non-content
	doc.Find(".Cookie-notice, .cookie-notice, .js-cookieNotice").Remove()

	// Try to find main content area - prioritize specific selectors
	var contentNode *goquery.Selection
	// Try multiple selectors in order of specificity
	selectors := []string{
		".Article",          // Go blog
		".Blog-content",     // Go blog alternative
		".u-rich-text-blog", // Claude blog
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

// slugify converts a title to a filesystem-safe slug
func slugify(title string) string {
	title = strings.ReplaceAll(title, "/", "-")
	title = strings.ReplaceAll(title, ":", "-")
	title = strings.ReplaceAll(title, " ", "-")
	title = strings.ReplaceAll(title, "\\", "-")
	title = strings.ReplaceAll(title, "?", "")
	title = strings.ReplaceAll(title, "*", "")
	title = strings.ReplaceAll(title, "|", "")
	title = strings.ReplaceAll(title, "\"", "")
	title = strings.ReplaceAll(title, "<", "")
	title = strings.ReplaceAll(title, ">", "")
	return title
}

func (h *WebHandler) UploadWeb(c echo.Context) error {
	var req WebUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "URL is required"})
	}

	// Check if this is an X/Twitter URL — needs special handling
	// because X serves a JS-required shell to non-browser clients
	if isXTwitterURL(req.URL) {
		return h.uploadXTwitter(c, req)
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

	// Extract title using extractTitle (prefers og:title over <title> to avoid platform noise)
	originalTitle := extractTitle(doc)
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	// Clean title for filesystem (slug)
	title := slugify(originalTitle)

	userId := GetCurrentUserId(c)

	// Dedup: hard-delete any soft-deleted records with the same source_url for this user
	// This prevents "ghost" records from causing confusion and accidental deletion of re-imported files
	var staleDocs []db.Document
	if err := db.DB.Unscoped().Where("source_url = ? AND user_id = ? AND deleted_at IS NOT NULL", req.URL, userId).Find(&staleDocs).Error; err == nil {
		for _, stale := range staleDocs {
			db.DB.Unscoped().Delete(&stale) // hard delete
			fmt.Printf("[web] Hard-deleted stale soft-deleted record id=%d for re-import: %s\n", stale.ID, req.URL)
		}
	}

	// Check if an active document with the same source_url already exists for this user
	var existingDoc db.Document
	if err := db.DB.Where("source_url = ? AND user_id = ?", req.URL, userId).First(&existingDoc).Error; err == nil {
		return c.JSON(200, echo.Map{
			"id":      existingDoc.ID,
			"title":   existingDoc.Title,
			"path":    filepath.Join(h.DataDir, existingDoc.RawPath),
			"url":     req.URL,
			"message": "Document already exists",
		})
	}

	// Resolve raw_path collision: different URLs may produce the same title/slug.
	// If another active document already uses this raw_path, append a numeric suffix.
	rawRelPath := filepath.Join("raw", "web", title)
	var collisionCount int64
	db.DB.Model(&db.Document{}).Where("raw_path = ? AND source_url != ?", rawRelPath, req.URL).Count(&collisionCount)
	if collisionCount > 0 {
		title = fmt.Sprintf("%s-%d", title, collisionCount+1)
		rawRelPath = filepath.Join("raw", "web", title)
	}

	// Create directory
	dir := filepath.Join(h.DataDir, rawRelPath)
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
		originalTitle,
		publishedTime.Format("2006-01-02"),
		content)

	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save markdown"})
	}

	// Store original published date in metadata
	metadata := map[string]string{
		"published": publishedTime.Format(time.RFC3339),
	}
	metadataJSON, _ := json.Marshal(metadata)

	docRecord := db.Document{
		UserID:     userId,
		Title:      originalTitle,
		Slug:       title,
		SourceType: "web",
		RawPath:    rawRelPath,
		SourceURL:  req.URL,
		Language:   "en",
		Status:     "inbox",
		Metadata:   string(metadataJSON),
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
		"title":    originalTitle,
		"path":     dir,
		"url":      req.URL,
		"images":   downloadedImages,
		"htmlPath": filepath.Join(rawRelPath, "index.html"),
		"mdPath":   filepath.Join(rawRelPath, "paper.md"),
		"message":  "Web page saved successfully",
	})
}

// uploadXTwitter handles X/Twitter URLs using the fxtwitter API
// because x.com serves a JS-required shell to non-browser clients
func (h *WebHandler) uploadXTwitter(c echo.Context, req WebUploadRequest) error {
	// Fetch tweet data via fxtwitter API
	tweet, err := fetchXTwitterViaAPI(req.URL)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to fetch X/Twitter content: " + err.Error()})
	}

	// Determine title
	originalTitle := ""
	if tweet.Article != nil && tweet.Article.Title != "" {
		originalTitle = tweet.Article.Title
	} else {
		// For regular tweets, use author + first line
		originalTitle = tweet.Text
		if len(originalTitle) > 100 {
			originalTitle = originalTitle[:100] + "..."
		}
		if originalTitle == "" {
			originalTitle = "tweet-" + tweet.ID
		}
	}

	// Slug
	title := slugify(originalTitle)

	userId := GetCurrentUserId(c)

	// Dedup
	var staleDocs []db.Document
	if err := db.DB.Unscoped().Where("source_url = ? AND user_id = ? AND deleted_at IS NOT NULL", req.URL, userId).Find(&staleDocs).Error; err == nil {
		for _, stale := range staleDocs {
			db.DB.Unscoped().Delete(&stale)
		}
	}

	var existingDoc db.Document
	if err := db.DB.Where("source_url = ? AND user_id = ?", req.URL, userId).First(&existingDoc).Error; err == nil {
		return c.JSON(200, echo.Map{
			"id":      existingDoc.ID,
			"title":   existingDoc.Title,
			"path":    filepath.Join(h.DataDir, existingDoc.RawPath),
			"url":     req.URL,
			"message": "Document already exists",
		})
	}

	rawRelPath := filepath.Join("raw", "web", title)
	var collisionCount int64
	db.DB.Model(&db.Document{}).Where("raw_path = ? AND source_url != ?", rawRelPath, req.URL).Count(&collisionCount)
	if collisionCount > 0 {
		title = fmt.Sprintf("%s-%d", title, collisionCount+1)
		rawRelPath = filepath.Join("raw", "web", title)
	}

	dir := filepath.Join(h.DataDir, rawRelPath)
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directory"})
	}

	// Download cover image if available
	downloadedImages := 0
	if tweet.Article != nil && tweet.Article.CoverMedia != nil {
		coverURL := tweet.Article.CoverMedia.MediaInfo.OriginalImgURL
		if coverURL != "" {
			ext := getImageExtension(coverURL)
			localPath := filepath.Join(assetsDir, "img_1"+ext)
			if err := downloadImage(coverURL, localPath); err == nil {
				downloadedImages++
			}
		}
	}

	// Convert article to markdown
	content := xTwitterArticleToMarkdown(tweet)

	// Published time
	publishedTime := xTwitterPublishedTime(tweet)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	// Save paper.md with frontmatter
	mdPath := filepath.Join(dir, "paper.md")
	mdContent := fmt.Sprintf("---\nsource_url: %s\nsource_type: web\ntitle: %s\ndate: %s\nauthor: %s (@%s)\n---\n\n%s",
		req.URL,
		originalTitle,
		publishedTime.Format("2006-01-02"),
		tweet.Author.Name,
		tweet.Author.ScreenName,
		content)

	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save markdown"})
	}

	// Save a simple HTML version for reference
	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title></head>
<body><article><h1>%s</h1><p>By %s (@%s) &mdash; %s</p><hr>%s</article></body></html>`,
		html.EscapeString(originalTitle), html.EscapeString(originalTitle),
		html.EscapeString(tweet.Author.Name), html.EscapeString(tweet.Author.ScreenName),
		publishedTime.Format("2006-01-02"), html.EscapeString(strings.Replace(content, "\n", "<br>\n", -1)))

	htmlPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save HTML"})
	}

	// Save raw JSON for future re-processing
	jsonPath := filepath.Join(dir, "fxtwitter.json")
	if jsonData, err := json.MarshalIndent(tweet, "", "  "); err == nil {
		os.WriteFile(jsonPath, jsonData, 0644)
	}

	// Metadata
	metadata := map[string]string{
		"published":    publishedTime.Format(time.RFC3339),
		"author":       tweet.Author.ScreenName,
		"tweet_id":     tweet.ID,
		"fetch_method": "fxtwitter",
	}
	metadataJSON, _ := json.Marshal(metadata)

	docRecord := db.Document{
		UserID:     userId,
		Title:      originalTitle,
		Slug:       title,
		SourceType: "web",
		RawPath:    rawRelPath,
		SourceURL:  req.URL,
		Language:   "en",
		Status:     "inbox",
		Metadata:   string(metadataJSON),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.DB.Create(&docRecord).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create document"})
	}

	docID := docRecord.ID

	// Async summary
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
		"title":    originalTitle,
		"path":     dir,
		"url":      req.URL,
		"images":   downloadedImages,
		"htmlPath": filepath.Join(rawRelPath, "index.html"),
		"mdPath":   filepath.Join(rawRelPath, "paper.md"),
		"message":  "X/Twitter content saved successfully",
	})
}
