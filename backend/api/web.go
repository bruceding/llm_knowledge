package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"llm-knowledge/browser"
	"llm-knowledge/db"
	"llm-knowledge/fs"
	"llm-knowledge/ingest"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/echo/v4"
)

var inlineImageRe = regexp.MustCompile(`!\[([^\]]*)\]\((https?://[^\)]+)\)`)

type WebHandler struct {
	DataDir     string
	ClaudeBin   string
	BrowserPool *browser.Pool
}

type WebUploadRequest struct {
	URL string `json:"url"`
}

func fetchHTML(urlStr string, headers map[string]string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return "", err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
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

// WeChat article headers for fetching HTML and downloading CDN images
var weChatHeaders = map[string]string{
	"Referer":    "https://mp.weixin.qq.com",
	"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 MicroMessenger/7.0.20",
}

// isWeChatURL checks if a URL is a WeChat article on mp.weixin.qq.com
func isWeChatURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == "mp.weixin.qq.com"
}

type browserSiteConfig struct {
	WaitSelector   string
	VerifySelector string
	ScrollToLoad   bool
	Postprocess    func(doc *goquery.Document)
	ExtractAuthor  func(doc *goquery.Document) string
	FetchMethod    string
	ImageHeaders   func(imgURL string) map[string]string
}

var browserSites = map[string]browserSiteConfig{
	"mp.weixin.qq.com": {
		WaitSelector:   "#js_content",
		VerifySelector: "#js_verify",
		ScrollToLoad:   true,
		Postprocess:    preprocessWeChatImages,
		ExtractAuthor:  extractWeChatAuthor,
		FetchMethod:    "wechat",
		ImageHeaders: func(imgURL string) map[string]string {
			if strings.Contains(imgURL, "mmbiz.qpic.cn") {
				return weChatHeaders
			}
			return nil
		},
	},
}

func needsBrowser(urlStr string) (browserSiteConfig, bool) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return browserSiteConfig{}, false
	}
	cfg, ok := browserSites[strings.ToLower(u.Host)]
	return cfg, ok
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

	// Medium: "Article Title | by Author | Date | Publication" → "Article Title".
	// Custom-domain Medium publications (e.g. netflixtechblog.com) end with the
	// publication name instead of " | Medium" (issue #48), so detect via " | by "
	// rather than requiring a " | Medium" suffix.
	if idx := strings.Index(title, " | by "); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	} else if strings.HasSuffix(title, " | Medium") {
		// Plain "Article Title | Medium" with no author/date suffix.
		title = strings.TrimSpace(strings.TrimSuffix(title, " | Medium"))
	}

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

func ParseHTML(html string) (*goquery.Document, error) {
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
	Lang    string            `json:"lang"`
}

// fxtwitterArticle represents an X Article (long-form post)
type fxtwitterArticle struct {
	Title       string `json:"title"`
	PreviewText string `json:"preview_text"`
	Content     struct {
		Blocks    []fxtwitterBlock          `json:"blocks"`
		EntityMap []fxtwitterEntityMapEntry `json:"entityMap"`
	} `json:"content"`
	CoverMedia *struct {
		MediaInfo struct {
			OriginalImgURL string `json:"original_img_url"`
		} `json:"media_info"`
		AltText string `json:"alt_text"`
	} `json:"cover_media"`
	MediaEntities []fxtwitterMediaEntity `json:"media_entities"`
}

// fxtwitterEntityRange represents an entity reference within a block
type fxtwitterEntityRange struct {
	Key    json.Number `json:"key"`
	Offset int         `json:"offset"`
	Length int         `json:"length"`
}

// fxtwitterEntityMapEntry represents an entry in the article entityMap array
type fxtwitterEntityMapEntry struct {
	Key   string `json:"key"`
	Value struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	} `json:"value"`
}

// fxtwitterMediaEntity represents an image/media item in the article
type fxtwitterMediaEntity struct {
	MediaID   string `json:"media_id"`
	MediaInfo struct {
		OriginalImgURL    string `json:"original_img_url"`
		OriginalImgWidth  int    `json:"original_img_width"`
		OriginalImgHeight int    `json:"original_img_height"`
		AltText           string `json:"alt_text"`
	} `json:"media_info"`
}

// fxtwitterResponse represents the response from api.fxtwitter.com
type fxtwitterResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Tweet   fxtwitterTweet `json:"tweet"`
}

// fxtwitterBlock represents a content block in a Draft.js article
type fxtwitterBlock struct {
	Text              string                 `json:"text"`
	Type              string                 `json:"type"`
	Data              map[string]interface{} `json:"data"`
	EntityRanges      []fxtwitterEntityRange `json:"entityRanges"`
	InlineStyleRanges []struct {
		Offset int    `json:"offset"`
		Length int    `json:"length"`
		Style  string `json:"style"`
	} `json:"inlineStyleRanges"`
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

// convertArticleBlockToMarkdown converts a single fxtwitter article block to markdown.
// articleRef is needed to resolve entity references (images, links) from entityMap and mediaEntities.
func convertArticleBlockToMarkdown(block fxtwitterBlock, article *fxtwitterArticle) string {
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
		// Image/media block — resolve via entityRanges -> entityMap -> mediaEntities
		return resolveAtomicBlock(block, article)
	case "unstyled":
		// Apply inline styles (bold, italic) and entity links
		if len(block.InlineStyleRanges) > 0 {
			text = applyInlineStyles(text, block.InlineStyleRanges)
		}
		if len(block.EntityRanges) > 0 {
			text = applyEntityLinks(text, block, article)
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

// applyInlineStyles applies bold/italic markdown formatting based on style ranges.
// Draft.js offset/length are in Unicode characters (runes), not bytes.
func applyInlineStyles(text string, ranges []struct {
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Style  string `json:"style"`
}) string {
	runes := []rune(text)

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
		if end > len(runes) {
			end = len(runes)
		}
		if start > len(runes) {
			start = len(runes)
		}
		if start >= end {
			continue
		}

		snippet := string(runes[start:end])
		var wrapped string
		switch r.Style {
		case "Bold":
			wrapped = "**" + snippet + "**"
		case "Italic":
			wrapped = "*" + snippet + "*"
		case "CODE":
			wrapped = "`" + snippet + "`"
		default:
			continue
		}
		runes = append(runes[:start], append([]rune(wrapped), runes[end:]...)...)
	}
	return string(runes)
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

		// Cover image
		if tweet.Article.CoverMedia != nil && tweet.Article.CoverMedia.MediaInfo.OriginalImgURL != "" {
			altText := tweet.Article.CoverMedia.AltText
			if altText == "" {
				altText = "cover"
			}
			sb.WriteString(fmt.Sprintf("![%s](%s)\n\n", altText, tweet.Article.CoverMedia.MediaInfo.OriginalImgURL))
		}

		// Article blocks
		for _, block := range tweet.Article.Content.Blocks {
			md := convertArticleBlockToMarkdown(block, tweet.Article)
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

// resolveAtomicBlock resolves an atomic (image/media) block to markdown
func resolveAtomicBlock(block fxtwitterBlock, article *fxtwitterArticle) string {
	if article == nil || len(block.EntityRanges) == 0 {
		return ""
	}

	// Look up entity in entityMap by key
	for _, er := range block.EntityRanges {
		keyStr := er.Key.String()
		for _, em := range article.Content.EntityMap {
			if em.Key == keyStr && em.Value.Type == "MEDIA" {
				return resolveMediaEntity(em, article)
			}
		}
	}
	return ""
}

// resolveMediaEntity resolves a MEDIA entity to a markdown image
func resolveMediaEntity(em fxtwitterEntityMapEntry, article *fxtwitterArticle) string {
	// Extract mediaId from entity data
	mediaItems, ok := em.Value.Data["mediaItems"].([]interface{})
	if !ok || len(mediaItems) == 0 {
		return ""
	}
	firstItem, ok := mediaItems[0].(map[string]interface{})
	if !ok {
		return ""
	}
	mediaID, _ := firstItem["mediaId"].(string)
	if mediaID == "" {
		return ""
	}

	// Look up mediaID in mediaEntities to get the image URL
	for _, me := range article.MediaEntities {
		if me.MediaID == mediaID {
			altText := me.MediaInfo.AltText
			if altText == "" {
				altText = "image"
			}
			return fmt.Sprintf("![%s](%s)\n\n", altText, me.MediaInfo.OriginalImgURL)
		}
	}
	return ""
}

// applyEntityLinks resolves LINK entities in text to markdown links
func applyEntityLinks(text string, block fxtwitterBlock, article *fxtwitterArticle) string {
	if article == nil || len(article.Content.EntityMap) == 0 {
		return text
	}

	// Collect link entities for this block, sorted by offset descending
	type linkRange struct {
		offset int
		length int
		url    string
	}
	var links []linkRange
	for _, er := range block.EntityRanges {
		keyStr := er.Key.String()
		for _, em := range article.Content.EntityMap {
			if em.Key == keyStr && em.Value.Type == "LINK" {
				url, _ := em.Value.Data["url"].(string)
				if url != "" {
					links = append(links, linkRange{offset: er.Offset, length: er.Length, url: url})
				}
			}
		}
	}

	// Sort by offset descending (process from end to preserve offsets)
	sort.Slice(links, func(i, j int) bool {
		return links[i].offset > links[j].offset
	})

	runes := []rune(text)
	for _, l := range links {
		start := l.offset
		end := l.offset + l.length
		if end > len(runes) {
			end = len(runes)
		}
		if start > len(runes) {
			start = len(runes)
		}
		if start >= end {
			continue
		}
		linkText := string(runes[start:end])
		replacement := fmt.Sprintf("[%s](%s)", linkText, l.url)
		runes = append(runes[:start], append([]rune(replacement), runes[end:]...)...)
	}
	return string(runes)
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

// downloadInlineImages scans markdown for remote image URLs, downloads them
// to the assets directory, and replaces URLs with local relative paths.
func downloadInlineImages(md string, assetsDir string) (string, int) {
	re := inlineImageRe
	matches := re.FindAllStringSubmatch(md, -1)
	if len(matches) == 0 {
		return md, 0
	}

	// Deduplicate URLs
	urlToPath := make(map[string]string)
	for _, m := range matches {
		imgURL := m[2]
		if _, exists := urlToPath[imgURL]; exists {
			continue
		}
		ext := getImageExtension(imgURL)
		fileName := fmt.Sprintf("img_%d%s", len(urlToPath)+1, ext)
		localPath := filepath.Join(assetsDir, fileName)
		if err := downloadImage(imgURL, localPath, nil); err == nil {
			urlToPath[imgURL] = filepath.Join("assets", fileName)
		}
	}

	// Replace URLs in markdown
	for remoteURL, localRef := range urlToPath {
		md = strings.ReplaceAll(md, remoteURL, localRef)
	}
	return md, len(urlToPath)
}

// downloadImage downloads an image and saves it to the specified path.
// Optional headers can be set for CDN images that require Referer (e.g. WeChat's mmbiz.qpic.cn).
func downloadImage(imgURL, savePath string, headers map[string]string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", imgURL, nil)
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
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

// preprocessLazyImages fills in missing or placeholder <img src> attributes
// using data-src or sibling <source srcset> (Medium <picture>) so that
// lazy-loaded images survive the extension DOM snapshot (issue #48).
// Unlike preprocessWeChatImages, this only fills in when src is empty or
// a data-URI placeholder — real src values are preserved.
func preprocessLazyImages(doc *goquery.Document) {
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src != "" && !strings.HasPrefix(src, "data:") {
			return
		}

		if dataSrc, ok := s.Attr("data-src"); ok && dataSrc != "" {
			s.SetAttr("src", dataSrc)
			return
		}

		if srcset, ok := s.Attr("srcset"); ok && srcset != "" {
			if best := pickFromSrcset(srcset); best != "" {
				s.SetAttr("src", best)
				return
			}
		}

		parent := s.Parent()
		if parent.Length() > 0 && parent.Get(0).Data == "picture" {
			var best string
			parent.Find("source").Each(func(j int, src *goquery.Selection) {
				if ss, ok := src.Attr("srcset"); ok {
					if url := pickFromSrcset(ss); url != "" {
						best = url
					}
				}
			})
			if best != "" {
				s.SetAttr("src", best)
			}
		}
	})
}

// pickFromSrcset returns the URL with the highest width descriptor from a
// srcset attribute (e.g. "url1 640w, url2 1280w"). Falls back to the last
// URL when no width descriptors are present.
func pickFromSrcset(srcset string) string {
	var bestURL string
	var bestW int
	for _, part := range strings.Split(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		candidateURL := fields[0]
		w := 0
		if len(fields) > 1 {
			d := fields[1]
			if strings.HasSuffix(d, "w") {
				fmt.Sscanf(d, "%dw", &w)
			}
		}
		if w >= bestW {
			bestW = w
			bestURL = candidateURL
		}
	}
	return bestURL
}

// preprocessWeChatImages copies data-src to src for all <img> elements.
// WeChat uses data-src for lazy-loaded images; src is typically empty, a base64 placeholder,
// or a 1x1 pixel with wx_lazy=1. Always prefer data-src when present.
func preprocessWeChatImages(doc *goquery.Document) {
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		dataSrc, hasDataSrc := s.Attr("data-src")
		if !hasDataSrc || dataSrc == "" {
			return
		}
		s.SetAttr("src", dataSrc)
		s.RemoveAttr("data-src")
	})
}

// extractWeChatAuthor extracts the author name from a WeChat article page.
func extractWeChatAuthor(doc *goquery.Document) string {
	if author := doc.Find("#js_author_name").Text(); author != "" {
		return strings.TrimSpace(author)
	}
	if author := doc.Find(".rich_media_meta_nickname").Text(); author != "" {
		return strings.TrimSpace(author)
	}
	if author, exists := doc.Find("meta[name='author']").Attr("content"); exists && author != "" {
		return strings.TrimSpace(author)
	}
	return ""
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

// extractContent extracts clean text content from HTML for markdown.
// Strategy: try multiple selectors and pick the one with most text content.
func ExtractContent(doc *goquery.Document) string {
	// Remove script, style, nav, header, footer, sidebar elements
	// Also remove navigation links, social icons, and other non-content elements
	doc.Find("script, style, nav, header, footer, .Header, .Footer, .NavigationDrawer, aside, .sidebar, .navigation, .menu, .ads").Remove()

	// WeChat-specific cleanup: remove code line number lists
	doc.Find(".code-snippet__line-index").Remove()

	// Remove cookie notices and other non-content
	doc.Find(".Cookie-notice, .cookie-notice, .js-cookieNotice").Remove()

	// Medium-specific extraction: site uses obfuscated CSS classes that defeat the
	// generic selector list, causing fallback to <article> which contains byline,
	// follow/listen/share/more buttons, and image overlay text. Detect Medium and
	// extract from the container holding [data-selectable-paragraph] elements.
	if mediumContent := extractMediumContent(doc); mediumContent != "" {
		return mediumContent
	}

	// Content selectors ordered by specificity (platform-specific first, then generic)
	// Key insight: some sites have <article> for footer, <main> for content (e.g. Aliyun docs)
	// Solution: pick the selector with most text content, not first match
	selectors := []string{
		// Platform-specific: GitHub / markdown renderers
		".markdown-body",    // GitHub, Aliyun docs, many markdown-based sites
		".prose",            // Tailwind Typography (modern blogs)

		// Platform-specific: Go/blog
		".Article",
		".Blog-content",

		// Platform-specific: Medium/CMS style
		".section-content",  // Medium
		".article-content",  // Common CMS (WordPress, 少数派, etc.)
		".article-body",
		".post-content",
		".entry-content",    // WordPress standard

		// Platform-specific: Claude blog
		".u-rich-text-blog",

		// Platform-specific: documentation frameworks
		"#VPContent",        // VitePress (Vue docs)
		".document",         // Sphinx (Python docs)
		"div.body",          // Sphinx variant

		// Platform-specific: WeChat
		"#js_content",

		// Generic semantic HTML5
		"#content",
		"#main",
		".content",
		".post",
		"main",
		"article",
	}

	var bestContent string
	var bestTextLen int

	for _, sel := range selectors {
		matches := doc.Find(sel)
		if matches.Length() == 0 {
			continue
		}

		// Try each match and find the one with most text
		matches.Each(func(i int, s *goquery.Selection) {
			// Skip if this node contains only nav/sidebar elements
			if isNavOrFooter(s) {
				return
			}

			// Extract content
			var markdown strings.Builder
			s.Contents().Each(func(j int, child *goquery.Selection) {
				markdown.WriteString(convertNodeToMarkdown(child))
			})
			content := cleanExcessiveWhitespace(markdown.String())

			// Measure text length (excluding whitespace)
			textLen := len(strings.TrimSpace(content))
			if textLen > bestTextLen {
				bestTextLen = textLen
				bestContent = content
			}
		})
	}

	// Fallback to body if no selector found meaningful content
	if bestContent == "" {
		var markdown strings.Builder
		doc.Find("body").Contents().Each(func(i int, s *goquery.Selection) {
			markdown.WriteString(convertNodeToMarkdown(s))
		})
		bestContent = cleanExcessiveWhitespace(markdown.String())
	}

	// Merge table rows separated by blank lines
	bestContent = mergeTableRows(bestContent)

	return strings.TrimSpace(bestContent)
}

// extractMediumContent returns clean markdown for Medium articles, or "" if the
// document has no [data-selectable-paragraph] elements. Detection uses that
// attribute alone (not og:site_name) because the browser extension strips <head>
// meta tags before sending the DOM (issue #47).
//
// Approach:
//  1. Find the lowest common ancestor of all selectable paragraphs — this is the
//     content container. In production Medium DOM the byline is a SIBLING of the
//     paragraphs (not in a separate sub-tree), so just taking parent-of-first
//     does not exclude it.
//  2. Walk from the first paragraph up to that container and at each level remove
//     preceding siblings. This strips byline elements (avatar, author link,
//     "4 min read", "May 2, 2026", clap counts, follow button) regardless of how
//     deeply Medium nests them.
//  3. Drop Medium internal navigation links (href contains "source=post_page")
//     and all <button> elements (Listen/Share/More/Sign up + image overlays).
func extractMediumContent(doc *goquery.Document) string {
	paras := doc.Find("[data-selectable-paragraph]")
	if paras.Length() == 0 {
		return ""
	}

	container := mediumLCA(paras)
	if container.Length() == 0 {
		return ""
	}

	pruneBeforeFirstSelectable(container, paras.First())

	// Remove byline / publication-info subtrees. Walk each post_page link up
	// to its highest ancestor that does NOT contain a content <p data-
	// selectable-paragraph>, then remove that ancestor. This strips the
	// avatar + author + "X min read" + date block that lives between the h1
	// and the first body paragraph in custom-domain Medium publications
	// (issue #48) — the bare `a.Remove()` only removed the link itself and
	// left the surrounding wrapper full of byline text.
	containerNode := container.Get(0)
	container.Find(`a[href*="source=post_page"]`).Each(func(i int, a *goquery.Selection) {
		node := a
		for {
			parent := node.Parent()
			if parent.Length() == 0 || parent.Get(0) == containerNode {
				break
			}
			if parent.Find("p[data-selectable-paragraph]").Length() > 0 {
				break
			}
			node = parent
		}
		node.Remove()
	})

	container.Find("button").Remove()

	// Remove the "Press enter or click to view image in full size" overlay
	// regardless of which element wraps it (button/div/span vary by Medium
	// build). Match on exact text content of the leaf element.
	const figureOverlay = "Press enter or click to view image in full size"
	container.Find("*").Each(func(i int, s *goquery.Selection) {
		if strings.TrimSpace(s.Text()) == figureOverlay {
			s.Remove()
		}
	})

	var markdown strings.Builder
	container.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})
	content := cleanExcessiveWhitespace(markdown.String())
	content = mergeTableRows(content)
	return strings.TrimSpace(content)
}

// mediumLCA returns the lowest common ancestor that contains every paragraph in paras.
func mediumLCA(paras *goquery.Selection) *goquery.Selection {
	if paras.Length() == 0 {
		return nil
	}
	target := paras.Length()
	candidate := paras.First().Parent()
	for candidate.Length() > 0 {
		if candidate.Find("[data-selectable-paragraph]").Length() >= target {
			return candidate
		}
		candidate = candidate.Parent()
	}
	return candidate
}

// pruneBeforeFirstSelectable removes every node that precedes `first` in document
// order, walking up through ancestors until it reaches container. This eliminates
// the entire byline area regardless of how it is nested relative to the paragraphs.
func pruneBeforeFirstSelectable(container, first *goquery.Selection) {
	if container.Length() == 0 || first.Length() == 0 {
		return
	}
	containerNode := container.Get(0)
	node := first
	for node.Length() > 0 {
		node.PrevAll().Remove()
		parent := node.Parent()
		if parent.Length() == 0 || parent.Get(0) == containerNode {
			return
		}
		node = parent
	}
}

// isNavOrFooter checks if a node is likely navigation/footer rather than main content.
// Heuristic: if it contains footer/nav indicators or has very few text characters.
func isNavOrFooter(s *goquery.Selection) bool {
	// Check for footer/nav indicators in class or id
	class, _ := s.Attr("class")
	id, _ := s.Attr("id")

	footerIndicators := []string{
		"footer", "nav", "navigation", "sidebar", "menu",
		"comment", "reply", "discuss", "related",
		"share", "social", "breadcrumb",
		"copyright", "license",
	}
	for _, indicator := range footerIndicators {
		if strings.Contains(strings.ToLower(class), indicator) ||
			strings.Contains(strings.ToLower(id), indicator) {
			return true
		}
	}

	// Check text length heuristic: footer areas typically have < 100 chars
	text := strings.TrimSpace(s.Text())
	if len(text) < 100 {
		return true
	}

	return false
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

// mergeTableRows removes blank lines between consecutive markdown table rows.
// WeChat articles often wrap each table row in a separate <p>/<section>,
// producing blank lines that break markdown table syntax.
func mergeTableRows(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for i := 0; i < len(lines); i++ {
		result = append(result, lines[i])
		// If current line looks like a table row and next non-blank line also does,
		// skip the blank line between them
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "|") && strings.HasSuffix(strings.TrimSpace(lines[i]), "|") {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") && strings.HasSuffix(strings.TrimSpace(lines[j]), "|") {
				i = j - 1 // skip blank lines; loop increment moves to j
			}
		}
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

// fetchWeChatViaSogou searches Sogou WeChat index to find and fetch the article
// when direct access is blocked by captcha. Uses __biz + mid from the URL as search query.
func (h *WebHandler) fetchWeChatViaSogou(_ string, pageTitle string) (string, error) {
	if pageTitle == "" {
		return "", fmt.Errorf("no title available for sogou search")
	}

	searchQuery := url.QueryEscape(pageTitle)
	searchURL := "https://weixin.sogou.com/weixin?type=2&query=" + searchQuery
	log.Printf("[sogou] searching by title: %s", searchURL)

	searchHTML, err := h.BrowserPool.FetchRenderedHTML(searchURL, browser.RenderOpts{
		Timeout: 20 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("sogou search render: %w", err)
	}
	log.Printf("[sogou] search returned %d chars", len(searchHTML))

	if strings.Contains(searchHTML, "用户您好，您的访问过于频繁") || strings.Contains(searchHTML, "antispider") {
		return "", fmt.Errorf("sogou anti-spider triggered, try again later")
	}

	searchDoc, err := ParseHTML(searchHTML)
	if err != nil {
		return "", fmt.Errorf("parse sogou results: %w", err)
	}
	var redirectURL string
	searchDoc.Find("a[href*='/link?url=']").EachWithBreak(func(i int, s *goquery.Selection) bool {
		href, exists := s.Attr("href")
		if exists {
			redirectURL = "https://weixin.sogou.com" + href
			return false
		}
		return true
	})
	if redirectURL == "" {
		log.Printf("[sogou] no links found, page title: %s", searchDoc.Find("title").Text())
		return "", fmt.Errorf("no article links in sogou results (title: %s)", searchDoc.Find("title").Text())
	}
	log.Printf("[sogou] following redirect via browser")

	// Navigate directly to Sogou's redirect link. The browser will:
	// 1. Load the intermediate page with JS redirect
	// 2. Follow window.location.replace() to the signed WeChat URL
	// 3. Wait for #js_content on the final article page
	// The signed URL from Sogou bypasses WeChat's captcha for this visit.
	log.Printf("[sogou] redirect URL: %s", redirectURL)
	articleHTML, err := h.BrowserPool.FetchRenderedHTML(redirectURL, browser.RenderOpts{
		WaitRedirect: true,
		WaitSelector: "#js_content",
		ScrollToLoad: true,
		Timeout:      30 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("sogou redirect: %w", err)
	}

	return articleHTML, nil
}

func (h *WebHandler) uploadBrowser(c echo.Context, req WebUploadRequest, cfg browserSiteConfig) error {
	if h.BrowserPool == nil {
		return c.JSON(500, echo.Map{"error": "browser rendering not available"})
	}

	renderedHTML, err := h.BrowserPool.FetchRenderedHTML(req.URL, browser.RenderOpts{
		WaitSelector: cfg.WaitSelector,
		WaitStable:   2 * time.Second,
		ScrollToLoad: cfg.ScrollToLoad,
		Timeout:      30 * time.Second,
	})

	captchaDetected := false
	if err != nil {
		if cfg.VerifySelector != "" {
			fallbackHTML, fallbackErr := h.BrowserPool.FetchRenderedHTML(req.URL, browser.RenderOpts{
				WaitSelector: cfg.VerifySelector,
				Timeout:      10 * time.Second,
			})
			if fallbackErr == nil && strings.Contains(fallbackHTML, cfg.VerifySelector[1:]) {
				captchaDetected = true
				renderedHTML = fallbackHTML
			}
		}
		if !captchaDetected {
			log.Printf("[browser] rendering failed for %s: %v", req.URL, err)
			return c.JSON(500, echo.Map{"error": "browser rendering failed: " + err.Error()})
		}
	}

	doc, parseErr := ParseHTML(renderedHTML)
	if parseErr != nil && !captchaDetected {
		return c.JSON(500, echo.Map{"error": "failed to parse rendered HTML"})
	}

	if !captchaDetected && cfg.VerifySelector != "" && doc != nil && doc.Find(cfg.VerifySelector).Length() > 0 {
		captchaDetected = true
	}

	// Sogou fallback for WeChat captcha
	if captchaDetected && isWeChatURL(req.URL) {
		pageTitle := ""
		if doc != nil {
			pageTitle = strings.TrimSpace(doc.Find("title").Text())
		}
		log.Printf("[browser] captcha detected, trying sogou fallback for %s (title: %q)", req.URL, pageTitle)
		sogouHTML, sogouErr := h.fetchWeChatViaSogou(req.URL, pageTitle)
		if sogouErr != nil {
			log.Printf("[sogou] fallback failed: %v", sogouErr)
			return c.JSON(403, echo.Map{"error": "页面需要验证码，搜狗搜索也未找到文章，请在浏览器中手动打开链接完成验证后重试"})
		}
		renderedHTML = sogouHTML
		doc, parseErr = ParseHTML(renderedHTML)
		if parseErr != nil {
			return c.JSON(500, echo.Map{"error": "failed to parse sogou fallback HTML"})
		}
		captchaDetected = false
		cfg.FetchMethod = "sogou"
		log.Printf("[sogou] fallback succeeded for %s", req.URL)
	}

	if captchaDetected {
		return c.JSON(403, echo.Map{"error": "页面需要验证码，请在浏览器中手动打开链接完成验证后重试"})
	}

	if cfg.Postprocess != nil {
		cfg.Postprocess(doc)
	}

	originalTitle := extractTitle(doc)
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	author := ""
	if cfg.ExtractAuthor != nil {
		author = cfg.ExtractAuthor(doc)
	}

	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	content := ExtractContent(doc)

	if cfg.ImageHeaders == nil {
		cfg.ImageHeaders = func(imgURL string) map[string]string { return nil }
	}

	fetchMethod := cfg.FetchMethod
	if fetchMethod == "" {
		fetchMethod = "browser"
	}

	return h.saveWebDocument(c, req, originalTitle, doc, publishedTime, content, webSaveConfig{
		Author:       author,
		FetchMethod:  fetchMethod,
		Language:     detectLanguage(content),
		ImageHeaders: cfg.ImageHeaders,
		SuccessMsg:   "Page saved successfully (browser rendered)",
		Metadata: map[string]string{
			"published":    publishedTime.Format(time.RFC3339),
			"author":       author,
			"fetch_method": fetchMethod,
		},
	})
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

	if cfg, ok := needsBrowser(req.URL); ok {
		return h.uploadBrowser(c, req, cfg)
	}

	// Fetch HTML
	html, err := fetchHTML(req.URL, nil)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to fetch URL: " + err.Error()})
	}

	// Parse HTML
	doc, err := ParseHTML(html)
	if err != nil {
		return c.JSON(500, echo.Map{"error": "failed to parse HTML"})
	}

	// Extract title (prefers og:title over <title> to avoid platform noise)
	originalTitle := extractTitle(doc)
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	content := ExtractContent(doc)

	return h.saveWebDocument(c, req, originalTitle, doc, publishedTime, content, webSaveConfig{
		Language:     detectLanguage(content),
		ImageHeaders: func(imgURL string) map[string]string { return nil },
		SuccessMsg:   "Web page saved successfully",
		Metadata:     map[string]string{"published": publishedTime.Format(time.RFC3339)},
	})
}

type WebClipRequest struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

func (h *WebHandler) ClipWeb(c echo.Context) error {
	var req WebClipRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(400, echo.Map{"error": "invalid request"})
	}

	if req.HTML == "" {
		return c.JSON(400, echo.Map{"error": "html is required"})
	}
	if req.URL == "" {
		return c.JSON(400, echo.Map{"error": "url is required"})
	}

	doc, err := ParseHTML(req.HTML)
	if err != nil {
		return c.JSON(400, echo.Map{"error": "failed to parse HTML"})
	}

	// Platform-specific config (e.g. WeChat)
	cfg, hasCfg := needsBrowser(req.URL)
	if hasCfg && cfg.Postprocess != nil {
		cfg.Postprocess(doc)
	}

	// Browser extensions typically send document.title verbatim, which on Medium
	// includes the " | by Author | Date | Medium" suffix. Run it through cleanTitle
	// to strip platform noise (issue #47).
	originalTitle := cleanTitle(req.Title)
	if originalTitle == "" {
		originalTitle = extractTitle(doc)
	}
	if originalTitle == "" {
		originalTitle = "untitled"
	}

	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	content := ExtractContent(doc)

	imageHeaders := func(imgURL string) map[string]string { return nil }
	fetchMethod := "extension"
	if hasCfg && cfg.ImageHeaders != nil {
		imageHeaders = cfg.ImageHeaders
		fetchMethod = "extension-" + cfg.FetchMethod
	}

	uploadReq := WebUploadRequest{URL: req.URL}
	return h.saveWebDocument(c, uploadReq, originalTitle, doc, publishedTime, content, webSaveConfig{
		FetchMethod:  fetchMethod,
		Language:     detectLanguage(content),
		ImageHeaders: imageHeaders,
		SuccessMsg:   "Web page clipped successfully",
		Metadata: map[string]string{
			"published":    publishedTime.Format(time.RFC3339),
			"fetch_method": fetchMethod,
		},
	})
}

// webSaveConfig holds platform-specific parameters for saving a web document.
// The shared pipeline (dedup, collision, dirs, images, HTML/md save, DB, summary) is
// identical across UploadWeb and uploadWeChat; only these fields vary.
type webSaveConfig struct {
	Author       string
	FetchMethod  string
	Language     string
	Metadata     map[string]string
	ImageHeaders func(imgURL string) map[string]string
	SuccessMsg   string
}

// saveWebDocument runs the shared pipeline: dedup, collision, directory creation,
// image download + localization, HTML/md save, DB record, async summary.
func (h *WebHandler) saveWebDocument(c echo.Context, req WebUploadRequest, originalTitle string, doc *goquery.Document, publishedTime time.Time, content string, cfg webSaveConfig) error {
	title := slugify(originalTitle)
	userId := GetCurrentUserId(c)
	userDir := GetUserDir(c)
	userIdStr := strconv.FormatUint(uint64(userId), 10)

	// Ensure user directory exists
	fs.InitUserDirs(h.DataDir, userId)

	// Dedup: hard-delete any soft-deleted records with the same source_url for this user
	// This prevents "ghost" records from causing confusion and accidental deletion of re-imported files
	var staleDocs []db.Document
	if err := db.DB.Unscoped().Where("source_url = ? AND user_id = ? AND deleted_at IS NOT NULL", req.URL, userId).Find(&staleDocs).Error; err == nil {
		for _, stale := range staleDocs {
			db.DB.Unscoped().Delete(&stale)
			fmt.Printf("[web] Hard-deleted stale soft-deleted record id=%d for re-import: %s\n", stale.ID, req.URL)
		}
	}

	// Check if an active document with the same source_url already exists for this user
	var existingDoc db.Document
	if err := db.DB.Where("source_url = ? AND user_id = ?", req.URL, userId).First(&existingDoc).Error; err == nil {
		return c.JSON(200, echo.Map{
			"id":      existingDoc.ID,
			"title":   existingDoc.Title,
			"path":    filepath.Join(userDir, StripUserPrefix(existingDoc.RawPath)),
			"url":     req.URL,
			"message": "Document already exists",
		})
	}

	// Resolve raw_path collision: different URLs may produce the same title/slug.
	// If another active document already uses this raw_path, append a numeric suffix.
	rawRelPath := filepath.Join("users", userIdStr, "raw", "web", title)
	var collisionCount int64
	db.DB.Model(&db.Document{}).Where("raw_path = ? AND source_url != ?", rawRelPath, req.URL).Count(&collisionCount)
	if collisionCount > 0 {
		title = fmt.Sprintf("%s-%d", title, collisionCount+1)
		rawRelPath = filepath.Join("users", userIdStr, "raw", "web", title)
	}

	// Create directory
	dir := filepath.Join(userDir, "raw", "web", title)
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directory"})
	}

	// Fill in lazy-loaded image src values (Medium, etc.) before extraction
	// so that real CDN URLs end up in extractImageURLs / inline markdown.
	preprocessLazyImages(doc)

	// Re-extract content now that img src values may have been populated.
	content = ExtractContent(doc)

	// Download images and localize URLs
	imgURLs := extractImageURLs(doc)
	downloadedImages := 0
	imgMap := make(map[string]string) // original URL -> local path

	for i, imgURL := range imgURLs {
		absoluteURL := resolveURL(imgURL, req.URL)
		ext := getImageExtension(absoluteURL)
		localName := fmt.Sprintf("img_%d%s", i+1, ext)
		localPath := filepath.Join(assetsDir, localName)
		localRelPath := filepath.Join("assets", localName)

		headers := cfg.ImageHeaders(absoluteURL)
		if err := downloadImage(absoluteURL, localPath, headers); err == nil {
			imgMap[imgURL] = localRelPath
			downloadedImages++
		}
	}

	// Replace image URLs in HTML with local paths, then re-extract markdown
	// so that paper.md gets local image paths instead of remote URLs.
	if len(imgMap) > 0 {
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("src")
			if exists && imgMap[src] != "" {
				s.SetAttr("src", imgMap[src])
			}
		})
		content = ExtractContent(doc)
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

	// Build markdown frontmatter
	frontmatter := fmt.Sprintf("source_url: %s\nsource_type: web\ntitle: %s\ndate: %s",
		req.URL, yamlQuote(originalTitle), publishedTime.Format("2006-01-02"))
	if cfg.FetchMethod != "" {
		frontmatter += fmt.Sprintf("\nfetch_method: %s", cfg.FetchMethod)
	}
	if cfg.Author != "" {
		frontmatter += fmt.Sprintf("\nauthor: %s", yamlQuote(cfg.Author))
	}

	mdContent := fmt.Sprintf("---\n%s\n---\n\n%s", frontmatter, content)
	mdPath := filepath.Join(dir, "paper.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to save markdown"})
	}

	metadataJSON, _ := json.Marshal(cfg.Metadata)

	docRecord := db.Document{
		UserID:     userId,
		Title:      originalTitle,
		Slug:       title,
		SourceType: "web",
		RawPath:    rawRelPath,
		SourceURL:  req.URL,
		Language:   cfg.Language,
		Status:     "inbox",
		Metadata:   string(metadataJSON),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.DB.Create(&docRecord).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create document"})
	}

	docID := docRecord.ID

	// Trigger async summary generation if ClaudeBin is configured
	if h.ClaudeBin != "" {
		go func() {
			summary, err := ingest.GenerateSummary(userDir, "raw/web/"+title, h.ClaudeBin)
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
		"message":  cfg.SuccessMsg,
	})
}

// yamlQuote wraps a string in YAML double quotes, escaping internal quotes and backslashes.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return "\"" + s + "\""
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
	userDir := GetUserDir(c)
	userIdStr := strconv.FormatUint(uint64(userId), 10)

	// Ensure user directory exists
	fs.InitUserDirs(h.DataDir, userId)

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
			"path":    filepath.Join(userDir, StripUserPrefix(existingDoc.RawPath)),
			"url":     req.URL,
			"message": "Document already exists",
		})
	}

	rawRelPath := filepath.Join("users", userIdStr, "raw", "web", title)
	var collisionCount int64
	db.DB.Model(&db.Document{}).Where("raw_path = ? AND source_url != ?", rawRelPath, req.URL).Count(&collisionCount)
	if collisionCount > 0 {
		title = fmt.Sprintf("%s-%d", title, collisionCount+1)
		rawRelPath = filepath.Join("users", userIdStr, "raw", "web", title)
	}

	dir := filepath.Join(userDir, "raw", "web", title)
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create directory"})
	}

	// Convert article to markdown (includes cover image and inline images)
	content := xTwitterArticleToMarkdown(tweet)

	// Download all images from markdown (cover + inline) and replace with local paths
	content, downloadedImages := downloadInlineImages(content, assetsDir)

	// Published time
	publishedTime := xTwitterPublishedTime(tweet)
	if publishedTime.IsZero() {
		publishedTime = time.Now()
	}

	// Save paper.md with frontmatter
	mdPath := filepath.Join(dir, "paper.md")
	mdContent := fmt.Sprintf("---\nsource_url: %s\nsource_type: web\ntitle: %s\ndate: %s\nauthor: %s (@%s)\n---\n\n%s",
		req.URL,
		yamlQuote(originalTitle),
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
		if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
			log.Printf("[web] failed to save fxtwitter.json: %v", err)
		}
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
		Language: detectLanguage(func() string {
			if tweet.Article != nil && tweet.Article.Title != "" {
				return tweet.Article.Title + " " + tweet.Article.PreviewText
			}
			return tweet.Text
		}()),
		Status:    "inbox",
		Metadata:  string(metadataJSON),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.DB.Create(&docRecord).Error; err != nil {
		return c.JSON(500, echo.Map{"error": "failed to create document"})
	}

	docID := docRecord.ID

	// Async summary
	if h.ClaudeBin != "" {
		go func() {
			summary, err := ingest.GenerateSummary(userDir, "raw/web/"+title, h.ClaudeBin)
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

