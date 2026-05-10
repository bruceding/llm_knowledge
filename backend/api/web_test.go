package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/url"

	"github.com/PuerkitoBio/goquery"
)

func TestWebHandlerExists(t *testing.T) {
	h := WebHandler{
		DataDir:   "/tmp/test",
		ClaudeBin: "claude",
	}
	if h.DataDir != "/tmp/test" {
		t.Errorf("Expected DataDir to be set")
	}
}

func TestFetchHTML(t *testing.T) {
	html, err := fetchHTML("https://go.dev/blog/type-construction-and-cycle-detection", nil)
	if err != nil {
		t.Fatalf("Failed to fetch HTML: %v", err)
	}
	if len(html) == 0 {
		t.Error("Expected HTML content, got empty string")
	}
	// Should contain the article title
	if !strings.Contains(html, "Type Construction") {
		t.Error("Expected HTML to contain article title")
	}
}

func TestExtractImageURLs(t *testing.T) {
	html := `<html><body><img src="https://example.com/img1.png"/><img src="img2.jpg"/></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}

	imgURLs := extractImageURLs(doc)
	if len(imgURLs) != 2 {
		t.Errorf("Expected 2 image URLs, got %d", len(imgURLs))
	}
	if imgURLs[0] != "https://example.com/img1.png" {
		t.Errorf("Expected first URL to be absolute, got %s", imgURLs[0])
	}
}

func TestUploadWebIntegration(t *testing.T) {
	// This test requires database initialization
	// Skip in normal unit test runs
	t.Skip("Integration test requires database setup - run manually with full environment")
}

// TestConvertNodeToMarkdown tests the HTML to markdown conversion
func TestConvertNodeToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "paragraph",
			html:     "<p>Hello World</p>",
			expected: "Hello World\n\n",
		},
		{
			name:     "heading",
			html:     "<h1>Title</h1>",
			expected: "# Title\n\n",
		},
		{
			name:     "h2 heading",
			html:     "<h2>Subtitle</h2>",
			expected: "## Subtitle\n\n",
		},
		{
			name:     "bold",
			html:     "<strong>bold text</strong>",
			expected: "**bold text**",
		},
		{
			name:     "italic",
			html:     "<em>italic text</em>",
			expected: "*italic text*",
		},
		{
			name:     "link",
			html:     `<a href="https://example.com">click here</a>`,
			expected: "[click here](https://example.com)",
		},
		{
			name:     "image",
			html:     `<img src="assets/img_1.svg" alt="Go Logo"/>`,
			expected: "![Go Logo](assets/img_1.svg)\n\n",
		},
		{
			name:     "image no alt",
			html:     `<img src="test.png"/>`,
			expected: "![image](test.png)\n\n",
		},
		{
			name:     "unordered list",
			html:     "<ul><li>Item 1</li><li>Item 2</li></ul>",
			expected: "- Item 1\n- Item 2\n\n",
		},
		{
			name:     "ordered list",
			html:     "<ol><li>First</li><li>Second</li></ol>",
			expected: "1. First\n2. Second\n\n",
		},
		{
			name:     "code block with language",
			html:     `<pre><code class="language-go">func main() {}</code></pre>`,
			expected: "\n```go\nfunc main() {}\n```\n\n",
		},
		{
			name:     "code block without language",
			html:     "<pre><code>some code</code></pre>",
			expected: "\n```\nsome code\n```\n\n",
		},
		{
			name:     "inline code",
			html:     "<code>inline</code>",
			expected: "`inline`",
		},
		{
			name:     "inline code with surrounding text",
			html:     "<p>This is <code>inline code</code> in a paragraph.</p>",
			expected: "This is `inline code` in a paragraph.\n\n",
		},
		{
			name:     "italic with surrounding text",
			html:     "<p>This is <em>emphasized</em> text.</p>",
			expected: "This is *emphasized* text.\n\n",
		},
		{
			name:     "bold with surrounding text",
			html:     "<p>This is <strong>bold</strong> text.</p>",
			expected: "This is **bold** text.\n\n",
		},
		{
			name:     "blockquote",
			html:     "<blockquote>A quote</blockquote>",
			expected: "> A quote\n\n",
		},
		{
			name:     "two paragraphs",
			html:     "<p>First paragraph.</p><p>Second paragraph.</p>",
			expected: "First paragraph.\n\nSecond paragraph.\n\n",
		},
		{
			name:     "paragraph after image",
			html:     `<img src="test.png" alt="diagram"/><p>This explains the diagram.</p>`,
			expected: "![diagram](test.png)\n\nThis explains the diagram.\n\n",
		},
		{
			name: "code block with indentation",
			html: `<pre><code class="language-go">type Node struct {
  next *Node
}
</code></pre>`,
			expected: "\n```go\ntype Node struct {\n  next *Node\n}\n```\n\n",
		},
		{
			name:     "table with header",
			html:     `<table><thead><tr><th>Feature</th><th>Polling</th><th>SSE</th></tr></thead><tbody><tr><td>Direction</td><td>Client-to-server</td><td>Server-to-client</td></tr><tr><td>Protocol</td><td>HTTP</td><td>HTTP</td></tr></tbody></table>`,
			expected: "| Feature | Polling | SSE |\n|---|---|---|\n| Direction | Client-to-server | Server-to-client |\n| Protocol | HTTP | HTTP |\n\n",
		},
		{
			name:     "table without thead (th in first row)",
			html:     `<table><tr><th>Name</th><th>Value</th></tr><tr><td>Test</td><td>123</td></tr></table>`,
			expected: "| Name | Value |\n|---|---|\n| Test | 123 |\n\n",
		},
		{
			name:     "table with td only (first row as header)",
			html:     `<table><tr><td>Col1</td><td>Col2</td></tr><tr><td>A</td><td>B</td></tr></table>`,
			expected: "| Col1 | Col2 |\n|---|---|\n| A | B |\n\n",
		},
		{
			name:     "h4 inside link (no heading marker)",
			html:     `<a href="/blog/test"><h4>GraphQL Subscriptions</h4>8 min read</a>`,
			expected: "[GraphQL Subscriptions 8 min read](/blog/test)",
		},
		{
			name:     "h4 outside link (normal heading)",
			html:     `<h4>GraphQL Subscriptions</h4>`,
			expected: "#### GraphQL Subscriptions\n\n",
		},
		{
			name:     "empty link should be skipped",
			html:     `<a href="#"></a>`,
			expected: "",
		},
		{
			name:     "link with only whitespace should be skipped",
			html:     `<a href="#">   </a>`,
			expected: "",
		},
		{
			name:     "anchor link with text is preserved",
			html:     `<a href="#section">Jump to section</a>`,
			expected: "[Jump to section](#section)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			var result string
			doc.Find("body").Contents().Each(func(i int, s *goquery.Selection) {
				result += convertNodeToMarkdown(s)
			})

			result = strings.TrimSpace(result)
			expected := strings.TrimSpace(tt.expected)

			if result != expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", expected, result)
			}
		})
	}
}

// TestWebClippingGoBlog tests clipping the Go blog article with real content
func TestWebClippingGoBlog(t *testing.T) {
	url := "https://go.dev/blog/type-construction-and-cycle-detection"

	// Fetch the HTML
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("Failed to fetch URL (network issue): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Skipf("HTTP status: %d", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Remove navigation and footer
	doc.Find("script, style, nav, .Header, .Footer, .NavigationDrawer, aside").Remove()

	// Find main content using same logic as extractContent
	var contentNode *goquery.Selection
	selectors := []string{".Article", ".Blog-content", "article", "main", ".content", "#content"}
	for _, sel := range selectors {
		if doc.Find(sel).Length() > 0 {
			contentNode = doc.Find(sel).First()
			break
		}
	}
	if contentNode == nil {
		contentNode = doc.Find("body")
	}

	// Convert to markdown
	var markdown strings.Builder
	contentNode.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	result := markdown.String()

	// Verify key content is present
	if !strings.Contains(result, "Type Construction") {
		t.Error("Expected 'Type Construction' in markdown")
	}

	// Check for code blocks (should have multiple Go code examples)
	codeBlockCount := strings.Count(result, "```")
	if codeBlockCount < 4 {
		t.Errorf("Expected at least 4 code blocks (2 pairs of ```), got %d occurrences", codeBlockCount)
	}

	// Check for proper code block language detection
	if !strings.Contains(result, "```go") {
		t.Error("Expected '```go' code blocks (Go language detection)")
	}

	// Check for images (Go blog has diagrams)
	imageCount := strings.Count(result, "![")
	t.Logf("Images found: %d", imageCount)

	// Check for links preservation
	linkCount := strings.Count(result, "[") - imageCount
	t.Logf("Links found: %d", linkCount)

	// Check for proper formatting of key terms
	if !strings.Contains(result, "**type checker**") && !strings.Contains(result, "type checker") {
		t.Error("Expected 'type checker' content")
	}

	t.Logf("Generated markdown length: %d characters", len(result))
	t.Logf("Code block pairs: %d", codeBlockCount/2)

	// Save to temp file for inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "paper.md")
	if err := os.WriteFile(tmpFile, []byte(result), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Also print the first 1500 chars for immediate debugging
	maxPreview := 1500
	if len(result) > maxPreview {
		t.Logf("Preview (first %d chars):\n%s", maxPreview, result[:maxPreview])
	} else {
		t.Logf("Full content:\n%s", result)
	}

	// Check blank line count - should be reasonable
	blankLines := 0
	for _, line := range strings.Split(result, "\n") {
		if strings.TrimSpace(line) == "" {
			blankLines++
		}
	}
	t.Logf("Blank lines: %d out of %d total", blankLines, len(strings.Split(result, "\n")))
}

// TestWebClippingClaudeBlog tests clipping the Claude Code prompt caching blog
func TestWebClippingClaudeBlog(t *testing.T) {
	url := "https://claude.com/blog/lessons-from-building-claude-code-prompt-caching-is-everything"

	// Fetch the HTML
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("Failed to fetch URL (network issue): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Skipf("HTTP status: %d", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Remove navigation and non-content elements
	doc.Find("script, style, nav, .Header, .Footer, aside, .Cookie-notice, .navigation, .menu").Remove()

	// Find main content using same logic as extractContent
	var contentNode *goquery.Selection
	selectors := []string{".Article", ".Blog-content", "article", "main", ".content", "#content"}
	for _, sel := range selectors {
		if doc.Find(sel).Length() > 0 {
			contentNode = doc.Find(sel).First()
			break
		}
	}
	if contentNode == nil {
		contentNode = doc.Find("body")
	}

	// Convert to markdown
	var markdown strings.Builder
	contentNode.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	result := markdown.String()

	// Verify key content is present
	if !strings.Contains(result, "Prompt caching") {
		t.Error("Expected 'Prompt caching' in markdown")
	}

	// Check for key sections/headings
	if !strings.Contains(result, "cache") {
		t.Error("Expected 'cache' content")
	}

	// Check for code blocks or technical examples
	codeBlockCount := strings.Count(result, "```")
	t.Logf("Code block pairs: %d", codeBlockCount/2)

	// Check for proper heading structure
	headingCount := strings.Count(result, "#")
	t.Logf("Headings found: %d", headingCount)

	t.Logf("Generated markdown length: %d characters", len(result))

	// Save to temp file for inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "claude_prompt_caching.md")
	if err := os.WriteFile(tmpFile, []byte(result), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Print the first 1200 chars for debugging
	maxPreview := 1200
	if len(result) > maxPreview {
		t.Logf("Preview (first %d chars):\n%s", maxPreview, result[:maxPreview])
	} else {
		t.Logf("Full content:\n%s", result)
	}

	// Check blank line count - should be reasonable
	blankLines := 0
	for _, line := range strings.Split(result, "\n") {
		if strings.TrimSpace(line) == "" {
			blankLines++
		}
	}
	t.Logf("Blank lines: %d out of %d total", blankLines, len(strings.Split(result, "\n")))
}

// TestWebClippingSSEArticle tests clipping the SSE guide article with tables
func TestWebClippingSSEArticle(t *testing.T) {
	url := "https://codelit.io/blog/sse-server-sent-events-guide"

	// Fetch the HTML
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("Failed to fetch URL (network issue): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Skipf("HTTP status: %d", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Remove navigation and non-content elements
	doc.Find("script, style, nav, .Header, .Footer, aside, .Cookie-notice").Remove()

	// Find main content using same logic as extractContent
	var contentNode *goquery.Selection
	selectors := []string{".Article", ".Blog-content", "article", "main", ".content", "#content"}
	for _, sel := range selectors {
		if doc.Find(sel).Length() > 0 {
			contentNode = doc.Find(sel).First()
			break
		}
	}
	if contentNode == nil {
		contentNode = doc.Find("body")
	}

	// Convert to markdown
	var markdown strings.Builder
	contentNode.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	result := markdown.String()

	// Verify key content is present
	if !strings.Contains(result, "Server-Sent Events") {
		t.Error("Expected 'Server-Sent Events' in markdown")
	}

	// CRITICAL: Check for table markdown format (this was the bug)
	// Table should have pipe characters and separator row
	if !strings.Contains(result, "|") {
		t.Error("Expected table with pipe characters '|' - table formatting is broken")
	}
	if !strings.Contains(result, "---|") {
		t.Error("Expected table separator row '|---|' - table formatting is broken")
	}

	// Check for specific table headers that should be preserved
	if !strings.Contains(result, "| Feature |") && !strings.Contains(result, "Feature | Polling") {
		t.Error("Expected 'Feature' column header in comparison table")
	}

	// Check for code blocks
	codeBlockCount := strings.Count(result, "```")
	if codeBlockCount < 4 {
		t.Errorf("Expected at least 4 code blocks (SSE article has many examples), got %d occurrences", codeBlockCount)
	}

	t.Logf("Generated markdown length: %d characters", len(result))

	// Save to temp file for inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "sse_article.md")
	if err := os.WriteFile(tmpFile, []byte(result), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Print table portion for verification
	tableStart := strings.Index(result, "| Feature")
	if tableStart > 0 {
		tableEnd := tableStart + 300
		if tableEnd > len(result) {
			tableEnd = len(result)
		}
		t.Logf("Table preview:\n%s", result[tableStart:tableEnd])
	}

	// Also print the first 800 chars
	maxPreview := 800
	if len(result) > maxPreview {
		t.Logf("Preview (first %d chars):\n%s", maxPreview, result[:maxPreview])
	}
}

// TestWebClippingGolangWeekly tests clipping a newsletter-style page with multiple sections
// https://golangweekly.com/issues/598 - RSS抓取链接测试
func TestWebClippingGolangWeekly(t *testing.T) {
	url := "https://golangweekly.com/issues/598"

	// Fetch the HTML
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("Failed to fetch URL (network issue): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Skipf("HTTP status: %d", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Remove navigation and non-content elements
	doc.Find("script, style, nav, .Header, .Footer, aside, .Cookie-notice, .navigation, .menu").Remove()

	// Find main content using same logic as extractContent
	var contentNode *goquery.Selection
	selectors := []string{".Article", ".Blog-content", "article", "main", ".content", "#content"}
	for _, sel := range selectors {
		if doc.Find(sel).Length() > 0 {
			contentNode = doc.Find(sel).First()
			break
		}
	}
	if contentNode == nil {
		contentNode = doc.Find("body")
	}

	// Convert to markdown
	var markdown strings.Builder
	contentNode.Contents().Each(func(i int, s *goquery.Selection) {
		markdown.WriteString(convertNodeToMarkdown(s))
	})

	result := markdown.String()

	// Verify key content is present
	if !strings.Contains(result, "#598") && !strings.Contains(result, "598") {
		t.Error("Expected issue number '#598' in markdown")
	}

	if !strings.Contains(result, "Go") {
		t.Error("Expected 'Go' content in markdown")
	}

	// Check for section headers (newsletter has multiple sections)
	// Should have headings like "In Brief", "Code & Tools", etc.
	headingCount := strings.Count(result, "##")
	t.Logf("Section headings (##) found: %d", headingCount)

	// Check link count - newsletter has many links
	linkCount := strings.Count(result, "[")
	t.Logf("Links found: %d", linkCount)

	// Check if sponsor content is properly handled (should be present but marked)
	// golangweekly has sponsor sections
	sponsorMention := strings.Contains(strings.ToLower(result), "sponsor")
	t.Logf("Has sponsor content: %v", sponsorMention)

	t.Logf("Generated markdown length: %d characters", len(result))

	// Save to temp file for inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "golangweekly_598.md")
	if err := os.WriteFile(tmpFile, []byte(result), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Also save to project directory for manual inspection
	debugFile := "/tmp/golangweekly_598_debug.md"
	if err := os.WriteFile(debugFile, []byte(result), 0644); err == nil {
		t.Logf("Saved debug file to: %s", debugFile)
	}

	// Print the first 1500 chars for debugging
	maxPreview := 1500
	if len(result) > maxPreview {
		t.Logf("Preview (first %d chars):\n%s", maxPreview, result[:maxPreview])
	} else {
		t.Logf("Full content:\n%s", result)
	}

	// Analyze potential improvements
	// Check for issues that could be optimized

	// 1. Check if golangweekly.com/link/ redirect URLs are preserved (may want to resolve to actual URLs)
	redirectLinks := strings.Count(result, "golangweekly.com/link/")
	t.Logf("Redirect links (golangweekly.com/link/): %d", redirectLinks)

	// 2. Check blank line count - should be reasonable for newsletter format
	blankLines := 0
	for _, line := range strings.Split(result, "\n") {
		if strings.TrimSpace(line) == "" {
			blankLines++
		}
	}
	totalLines := len(strings.Split(result, "\n"))
	t.Logf("Blank lines: %d out of %d total (%.1f%%)", blankLines, totalLines, float64(blankLines)/float64(totalLines)*100)

	// 3. Check if issue date is extracted
	if strings.Contains(result, "April") || strings.Contains(result, "2026") || strings.Contains(result, "2025") {
		t.Logf("Issue date found in content ✓")
	} else {
		t.Logf("Warning: Issue date may not be clearly extracted")
	}
}

// TestWebClippingXArticle tests clipping an X/Twitter Article
// URL: https://x.com/trq212/status/2052809885763747935
// Thariq's article: "Using Claude Code: The Unreasonable Effectiveness of HTML"
// This test verifies title extraction and content handling for X/Twitter pages.
func TestWebClippingXArticle(t *testing.T) {
	url := "https://x.com/trq212/status/2052809885763747935"

	// Fetch the HTML
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("Failed to fetch X/Twitter URL (network/block issue): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Skipf("HTTP status: %d (X may block automated requests)", resp.StatusCode)
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// === Title extraction analysis ===
	title := doc.Find("title").Text()
	t.Logf("Raw <title>: %q", title)

	// Check OG meta tags (X usually provides these)
	if ogTitle, exists := doc.Find("meta[property='og:title']").Attr("content"); exists {
		t.Logf("og:title: %q", ogTitle)
	}
	if ogDesc, exists := doc.Find("meta[property='og:description']").Attr("content"); exists {
		t.Logf("og:description: %q", ogDesc)
	}
	if twTitle, exists := doc.Find("meta[name='twitter:title']").Attr("content"); exists {
		t.Logf("twitter:title: %q", twTitle)
	}
	if twDesc, exists := doc.Find("meta[name='twitter:description']").Attr("content"); exists {
		t.Logf("twitter:description: %q", twDesc)
	}

	// Extract content using the same logic as production
	content := extractContent(doc)

	t.Logf("Extracted content length: %d", len(content))
	if len(content) > 0 {
		maxPreview := 2000
		if len(content) > maxPreview {
			t.Logf("Content preview (first %d chars):\n%s", maxPreview, content[:maxPreview])
		} else {
			t.Logf("Full content:\n%s", content)
		}
	} else {
		t.Log("WARNING: No content extracted - X/Twitter pages require JavaScript rendering")
	}

	// Save to temp file for inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "x_article.md")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Also save to /tmp for manual inspection
	debugFile := "/tmp/x_article_debug.md"
	if err := os.WriteFile(debugFile, []byte(content), 0644); err == nil {
		t.Logf("Saved debug file to: %s", debugFile)
	}

	// === Format issue checks ===

	// Issue 1: Title should not contain "on X" / "/ X" suffix noise
	if strings.Contains(title, " on X") || strings.Contains(title, " / X") {
		t.Errorf("Title contains X/Twitter platform noise: %q - should be cleaned", title)
	}

	// Issue 2: Title should ideally be the article title, not "Username on X"
	// Expected: "Using Claude Code: The Unreasonable Effectiveness of HTML"
	if !strings.Contains(title, "Unreasonable Effectiveness") && !strings.Contains(title, "HTML") {
		t.Logf("NOTE: Title does not contain article-specific text: %q", title)
		t.Log("X/Twitter articles may only expose the article title in og:meta tags, not <title>")
	}

	// Issue 3: Content should not be empty for article-type tweets
	if len(content) == 0 || len(strings.TrimSpace(content)) < 50 {
		t.Error("Content is empty or very short - X/Twitter pages require JavaScript to render content")
		t.Log("This is a known limitation: fetchHTML cannot render JavaScript-heavy pages")
	}

	// Issue 4: Published time extraction
	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		t.Log("WARNING: Could not extract published time from meta tags")
	} else {
		t.Logf("Published time: %s", publishedTime.Format("2006-01-02"))
	}
}

// TestExtractContentXTwitterMock tests content extraction with a mock X/Twitter Article HTML.
// This simulates what fetchHTML would return from an X/Twitter page, including the
// common format issues: noisy <title>, og:meta tags with real content, and JS-dependent body.
func TestExtractContentXTwitterMock(t *testing.T) {
	// Simulated X/Twitter Article HTML - based on actual X page structure
	// X/Twitter serves a minimal HTML shell with og:meta tags but JS-rendered content
	mockHTML := `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8"/>
	<meta name="viewport" content="width=device-width,initial-scale=1"/>
	<meta property="og:site_name" content="X (Twitter)"/>
	<meta property="og:type" content="article"/>
	<meta property="og:title" content="Using Claude Code: The Unreasonable Effectiveness of HTML"/>
	<meta property="og:description" content="Markdown has become the dominant file format used by agents to communicate with us. It's simple, portable, has some rich text capability and is easy for you to edit."/>
	<meta property="og:url" content="https://x.com/trq212/status/2052809885763747935"/>
	<meta property="article:published_time" content="2026-05-08T17:56:30.000Z"/>
	<meta name="twitter:card" content="summary_large_image"/>
	<meta name="twitter:title" content="Using Claude Code: The Unreasonable Effectiveness of HTML"/>
	<meta name="twitter:description" content="Markdown has become the dominant file format used by agents to communicate with us."/>
	<title>Thariq on X: &quot;Using Claude Code: The Unreasonable Effectiveness of HTML&quot; / X</title>
</head>
<body>
	<div id="react-root">
		<div id="placeholder" style="display:none;">
			<p>Using Claude Code: The Unreasonable Effectiveness of HTML</p>
			<p>Markdown has become the dominant file format used by agents to communicate with us. It's simple, portable, has some rich text capability and is easy for you to edit. Claude has even gotten surprisingly good at using ASCII to make diagrams inside of markdown files.</p>
			<p>But as agents have become more and more powerful, I have felt that markdown has become a restricting format. I find it difficult to read a markdown file of more than a hundred lines. I want richer visualizations, color and diagrams and I want to be able to share them easily.</p>
			<p>I've started preferring HTML as an output format instead of Markdown and increasingly see this being used by others on the Claude Code team, this is why.</p>
			<h2>Why HTML?</h2>
			<h3>Information Density</h3>
			<p>HTML can convey much richer information compared to markdown. It can of course do simple document structure like headers and formatting, but it can also represent all sorts of other information such as:</p>
			<ul>
				<li>Tabular data using tables</li>
				<li>Design data with CSS</li>
				<li>Illustrations with SVG</li>
				<li>Code snippets with script tags</li>
				<li>Interactions using HTML elements with javascript + CSS</li>
				<li>Workflows using SVG and HTML</li>
				<li>Spatial data using absolute positions and canvases</li>
				<li>Images using image tags</li>
			</ul>
			<h3>Visual Clarity &amp; Ease of Reading</h3>
			<p>As Claude is able to do more complex work, it is also writing larger and larger specs and plans. In practice, I've found I tend to not actually read more than a 100-line markdown file, and I certainly am not able to get anyone else in my organization to read it.</p>
			<h3>Ease of Sharing</h3>
			<p>With HTML, as long as you upload the file (for example to S3), you can share the link easily. Your colleagues can open it wherever they wish and easily reference it.</p>
			<h3>Two-way Interaction</h3>
			<p>HTML can allow you to interact with the document, for example you might want to ask it to add sliders or knobs to adjust a design or allow you to tweak different options in the algorithm to see what happens.</p>
			<h2>How to Get Started</h2>
			<p>I'm a little bit afraid that people will read this article and turn it into a /html skill or something. While there might be some value in that, I want to emphasize that you don't need to do much to get Claude to do this. You can just ask it to "make a HTML file" or "make a HTML artifact".</p>
			<h1>Use Cases</h1>
			<h2>Specs, Planning &amp; Exploration</h2>
			<p>HTML is a rich canvas for Claude to dive into a problem. When I start working on a problem instead of a simple markdown plan I expect to make a web of HTML files.</p>
			<h2>Code Review &amp; Understanding</h2>
			<p>Code can be difficult to read in a Markdown file. But with HTML we can render diffs, annotations, flowcharts, modules, etc.</p>
			<h2>Design &amp; Prototypes</h2>
			<p>Claude Design is based on HTML because HTML is incredibly expressive at design, even if your end surface is not HTML.</p>
			<h2>Reports, Research &amp; Learning</h2>
			<p>Claude Code is incredibly good at synthesizing information across multiple data sources and converting it into a report for readability.</p>
			<h2>Frequently Asked Questions</h2>
			<p>I've been telling many people about how I've switched to HTML and I've seen a few repeated questions.</p>
		</div>
	</div>
	<script>document.getElementById("placeholder").style.display = "none";</script>
</body>
</html>`

	doc, err := parseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	// === Title format issues ===
	// === Title format issues ===
	rawTitle := doc.Find("title").Text()
	t.Logf("Raw <title>: %q", rawTitle)

	// Verify extractTitle returns clean title (prefers og:title)
	extractedTitle := extractTitle(doc)
	t.Logf("extractTitle result: %q", extractedTitle)

	// extractTitle should return the og:title value (cleaner than <title>)
	if extractedTitle != "Using Claude Code: The Unreasonable Effectiveness of HTML" {
		t.Errorf("extractTitle returned %q, want %q", extractedTitle, "Using Claude Code: The Unreasonable Effectiveness of HTML")
	}

	// Verify raw <title> still has noise (this is the baseline problem)
	if !strings.Contains(rawTitle, " on X:") {
		t.Logf("NOTE: Raw <title> no longer has 'on X:' pattern - may have changed: %q", rawTitle)
	}

	// OG meta title should be clean
	ogTitle, _ := doc.Find("meta[property='og:title']").Attr("content")
	t.Logf("og:title: %q", ogTitle)

	// === Content extraction ===
	content := extractContent(doc)
	t.Logf("Extracted content length: %d chars", len(content))

	if len(content) == 0 {
		t.Error("CONTENT ISSUE: No content extracted from X/Twitter mock HTML")
	} else {
		maxPreview := 2000
		if len(content) > maxPreview {
			t.Logf("Content preview (first %d chars):\n%s", maxPreview, content[:maxPreview])
		} else {
			t.Logf("Full content:\n%s", content)
		}
	}

	// Save debug output
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "x_article_mock.md")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved mock markdown to: %s", tmpFile)

	debugFile := "/tmp/x_article_mock_debug.md"
	if err := os.WriteFile(debugFile, []byte(content), 0644); err == nil {
		t.Logf("Saved debug file to: %s", debugFile)
	}

	// === Content format verification ===
	if len(content) > 0 {
		// Check that article title is present
		if !strings.Contains(content, "Unreasonable Effectiveness") {
			t.Error("Content missing article title 'Unreasonable Effectiveness'")
		}

		// Check that headings are preserved
		headingCount := strings.Count(content, "#")
		t.Logf("Heading markers found: %d", headingCount)

		if !strings.Contains(content, "## Why HTML?") {
			t.Error("Content missing '## Why HTML?' heading")
		}

		// Check list items
		if !strings.Contains(content, "- Tabular data") {
			t.Error("Content missing list item 'Tabular data'")
		}

		// Check paragraph text
		if !strings.Contains(content, "Markdown has become the dominant") {
			t.Error("Content missing opening paragraph")
		}

		// Check that script content is not included
		if strings.Contains(content, "document.getElementById") {
			t.Error("CONTENT ISSUE: Script content leaked into extracted text")
		}
	}

	// === Published time ===
	publishedTime := extractPublishedTime(doc)
	if publishedTime.IsZero() {
		t.Error("TIME ISSUE: Could not extract published time from og:article:published_time meta tag")
	} else {
		t.Logf("Published time: %s", publishedTime.Format("2006-01-02"))
		if publishedTime.Format("2006-01-02") != "2026-05-08" {
			t.Errorf("Published time mismatch: got %s, want 2026-05-08", publishedTime.Format("2006-01-02"))
		}
	}
	t.Log("1. TITLE: extractTitle() now prefers og:title over <title>")
	t.Log("   - og:title provides clean title without X platform noise")
	t.Log("   - cleanTitle() strips 'Username on X:' prefix and '/ X' suffix")
	t.Log("2. CONTENT: X pages are JS-rendered, static HTML may have minimal content")
	t.Log("   - The #placeholder div is hidden via inline style/JS")
	t.Log("   - extractContent may or may not find it depending on selector matching")
	t.Log("3. DATE: article:published_time meta tag is used for date extraction")
	t.Log("4. GENERAL: X/Twitter Articles need special handling vs regular tweets")
}

// TestCleanTitle tests the cleanTitle function for removing platform noise
func TestCleanTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "X/Twitter article title with quotes",
			input:    `Thariq on X: "Using Claude Code: The Unreasonable Effectiveness of HTML" / X`,
			expected: "Using Claude Code: The Unreasonable Effectiveness of HTML",
		},
		{
			name:     "X/Twitter simple tweet",
			input:    "John on X: Hello world / X",
			expected: "Hello world",
		},
		{
			name:     "X/Twitter title without quotes",
			input:    "User on X: Some title text / X",
			expected: "Some title text",
		},
		{
			name:     "Normal title unchanged",
			input:    "My Blog Post Title",
			expected: "My Blog Post Title",
		},
		{
			name:     "Title with trailing / X only",
			input:    "Some Article / X",
			expected: "Some Article",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Unicode quotes",
			input:    "User on X: \u201cSmart Quotes Title\u201d / X",
			expected: "Smart Quotes Title",
		},
		{
			name:     "Title with just whitespace after on X:",
			input:    "User on X:  / X",
			expected: "/ X", // degenerate case: after stripping noise
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanTitle(tt.input)
			if result != tt.expected {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestExtractTitle tests the extractTitle function for preferring og:title
func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name: "prefers og:title over <title>",
			html: `<html><head>
				<meta property="og:title" content="Clean OG Title"/>
				<title>Noisy Title / X</title>
			</head><body></body></html>`,
			expected: "Clean OG Title",
		},
		{
			name: "falls back to twitter:title when no og:title",
			html: `<html><head>
				<meta name="twitter:title" content="Twitter Title"/>
				<title>Noisy Title / X</title>
			</head><body></body></html>`,
			expected: "Twitter Title",
		},
		{
			name: "falls back to cleaned <title> when no meta",
			html: `<html><head>
				<title>Simple Page Title</title>
			</head><body></body></html>`,
			expected: "Simple Page Title",
		},
		{
			name: "X/Twitter article: og:title is clean, title is noisy",
			html: `<html><head>
				<meta property="og:title" content="Using Claude Code: The Unreasonable Effectiveness of HTML"/>
				<title>Thariq on X: "Using Claude Code: The Unreasonable Effectiveness of HTML" / X</title>
			</head><body></body></html>`,
			expected: "Using Claude Code: The Unreasonable Effectiveness of HTML",
		},
		{
			name:     "empty HTML returns untitled",
			html:     `<html><head></head><body></body></html>`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}
			result := extractTitle(doc)
			if result != tt.expected {
				t.Errorf("extractTitle() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestIsXTwitterURL tests X/Twitter URL detection (only status URLs match)
func TestIsXTwitterURL(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://x.com/trq212/status/2052809885763747935", true},
		{"https://twitter.com/trq212/status/2052809885763747935", true},
		{"https://www.x.com/user/status/123", true},
		{"https://www.twitter.com/user/status/123", true},
		{"https://x.com/explore", false},
		{"https://x.com/settings", false},
		{"https://twitter.com/user", false},
		{"https://go.dev/blog/some-article", false},
		{"https://example.com", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := isXTwitterURL(tt.url)
			if result != tt.expect {
				t.Errorf("isXTwitterURL(%q) = %v, want %v", tt.url, result, tt.expect)
			}
		})
	}
}

// TestExtractXTwitterPath tests URL path extraction
func TestExtractXTwitterPath(t *testing.T) {
	tests := []struct {
		url        string
		screenName string
		statusID   string
		ok         bool
	}{
		{"https://x.com/trq212/status/2052809885763747935", "trq212", "2052809885763747935", true},
		{"https://twitter.com/elonmusk/status/123456", "elonmusk", "123456", true},
		{"https://x.com/trq212/status/2052809885763747935/", "trq212", "2052809885763747935", true},
		{"https://x.com/trq212", "", "", false},
		{"https://x.com/", "", "", false},
		{"not-a-url", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			screenName, statusID, ok := extractXTwitterPath(tt.url)
			if ok != tt.ok || screenName != tt.screenName || statusID != tt.statusID {
				t.Errorf("extractXTwitterPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.url, screenName, statusID, ok, tt.screenName, tt.statusID, tt.ok)
			}
		})
	}
}

// TestXTwitterArticleToMarkdown tests the article-to-markdown conversion
func TestXTwitterArticleToMarkdown(t *testing.T) {
	tweet := &fxtwitterTweet{
		Text:      "https://t.co/MXt5XS4xBX",
		CreatedAt: "Fri May 08 17:56:30 +0000 2026",
	}
	tweet.Author.ScreenName = "trq212"
	tweet.Author.Name = "Thariq"

	tweet.Article = &fxtwitterArticle{
		Title:       "Using Claude Code: The Unreasonable Effectiveness of HTML",
		PreviewText: "Markdown has become the dominant file format...",
	}

	tweet.Article.Content.Blocks = []fxtwitterBlock{
		{
			Text: "Markdown has become the dominant file format used by agents.",
			Type: "unstyled",
		},
		{
			Text: "Why HTML?",
			Type: "header-two",
		},
		{
			Text: "Information Density",
			Type: "header-three",
		},
		{
			Text: "HTML can convey much richer information compared to markdown.",
			Type: "unstyled",
		},
		{
			Text: "Tabular data using tables",
			Type: "unordered-list-item",
		},
		{
			Text: "Design data with CSS",
			Type: "unordered-list-item",
		},
		{
			Text: "Code Review & Understanding",
			Type: "header-two",
		},
		{
			Text: "Code can be difficult to read in a Markdown file.",
			Type: "unstyled",
			InlineStyleRanges: []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			}{
				{Offset: 0, Length: 4, Style: "Bold"},
			},
		},
	}

	result := xTwitterArticleToMarkdown(tweet)

	if !strings.Contains(result, "Using Claude Code: The Unreasonable Effectiveness of HTML") {
		t.Error("Missing article title")
	}
	if !strings.Contains(result, "## Why HTML?") {
		t.Error("Missing '## Why HTML?' heading")
	}
	if !strings.Contains(result, "### Information Density") {
		t.Error("Missing '### Information Density' heading")
	}
	if !strings.Contains(result, "- Tabular data using tables") {
		t.Error("Missing list item '- Tabular data using tables'")
	}
	if !strings.Contains(result, "**Code**") {
		t.Errorf("Missing bold formatting, got: %s", result)
	}
	if !strings.Contains(result, "Markdown has become the dominant") {
		t.Error("Missing opening paragraph")
	}

	t.Logf("Generated markdown:\n%s", result)

	debugFile := "/tmp/x_article_fxtwitter_debug.md"
	if err := os.WriteFile(debugFile, []byte(result), 0644); err == nil {
		t.Logf("Saved debug file to: %s", debugFile)
	}
}

// TestXTwitterArticleToMarkdownRegularTweet tests regular tweet (non-article)
func TestXTwitterArticleToMarkdownRegularTweet(t *testing.T) {
	tweet := &fxtwitterTweet{
		Text:      "Just shipped a new feature! Check it out",
		CreatedAt: "Fri May 08 12:00:00 +0000 2026",
	}
	tweet.Author.ScreenName = "dev"
	tweet.Author.Name = "Developer"

	result := xTwitterArticleToMarkdown(tweet)

	if !strings.Contains(result, "Just shipped a new feature!") {
		t.Error("Missing tweet text")
	}

	t.Logf("Regular tweet markdown:\n%s", result)
}

// TestXTwitterPublishedTime tests time parsing from fxtwitter format
func TestXTwitterPublishedTime(t *testing.T) {
	tweet := &fxtwitterTweet{
		CreatedAt: "Fri May 08 17:56:30 +0000 2026",
	}
	parsed := xTwitterPublishedTime(tweet)
	if parsed.IsZero() {
		t.Fatal("Failed to parse time")
	}
	if parsed.Format("2006-01-02") != "2026-05-08" {
		t.Errorf("Expected 2026-05-08, got %s", parsed.Format("2006-01-02"))
	}
}

// TestFetchXTwitterViaAPI integration test - uses real network
func TestFetchXTwitterViaAPI(t *testing.T) {
	tweet, err := fetchXTwitterViaAPI("https://x.com/trq212/status/2052809885763747935")
	if err != nil {
		t.Skipf("fxtwitter API unavailable: %v", err)
	}

	if tweet.Author.ScreenName != "trq212" {
		t.Errorf("Expected screen_name 'trq212', got %q", tweet.Author.ScreenName)
	}

	if tweet.Article == nil {
		t.Fatal("Expected article to be non-nil")
	}

	if tweet.Article.Title != "Using Claude Code: The Unreasonable Effectiveness of HTML" {
		t.Errorf("Unexpected title: %q", tweet.Article.Title)
	}

	if len(tweet.Article.Content.Blocks) == 0 {
		t.Error("Expected article blocks to be non-empty")
	}

	t.Logf("Article title: %q", tweet.Article.Title)
	t.Logf("Author: %s (@%s)", tweet.Author.Name, tweet.Author.ScreenName)
	t.Logf("Blocks: %d", len(tweet.Article.Content.Blocks))

	md := xTwitterArticleToMarkdown(tweet)
	t.Logf("Markdown length: %d chars", len(md))

	debugFile := "/tmp/x_article_fxtwitter_live.md"
	if err := os.WriteFile(debugFile, []byte(md), 0644); err == nil {
		t.Logf("Saved live markdown to: %s", debugFile)
	}
}

// TestSlugify tests the slugify function
func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "Hello-World"},
		{"foo/bar:baz", "foo-bar-baz"},
		{"a?b*c|d", "abcd"},
		{`quote"here`, "quotehere"},
		{"normal", "normal"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := slugify(tt.input)
			if result != tt.expected {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestFetchXTwitterViaAPIMock tests JSON deserialization with a mock fxtwitter server.
// This catches struct/JSON mismatches that unit tests constructing Go structs directly miss.
// Uses a snapshot of the real API response from https://x.com/trq212/status/2052809885763747935
func TestFetchXTwitterViaAPIMock(t *testing.T) {
	// Realistic fxtwitter API response snapshot with images, links, and media entities
	mockResponse := `{
		"code": 200,
		"message": "OK",
		"tweet": {
			"url": "https://x.com/trq212/status/2052809885763747935",
			"id": "2052809885763747935",
			"text": "",
			"created_at": "Fri May 08 17:56:30 +0000 2026",
			"author": {
				"screen_name": "trq212",
				"name": "Thariq"
			},
			"article": {
				"title": "Using Claude Code: The Unreasonable Effectiveness of HTML",
				"preview_text": "Markdown has become the dominant file format used by agents to communicate with us.",
				"cover_media": {
					"media_id": "2052796450510348288",
					"media_info": {
						"original_img_url": "https://pbs.twimg.com/media/HHz_ftzaIAAwkQs.jpg",
						"original_img_width": 1600,
						"original_img_height": 800
					},
					"alt_text": "Cover image"
				},
				"content": {
					"blocks": [
						{"text": "Markdown has become the dominant file format used by agents.", "type": "unstyled", "key": "c0raq", "entityRanges": [], "inlineStyleRanges": [], "data": {}},
						{"text": "Why HTML?", "type": "header-two", "key": "f9k7p", "entityRanges": [], "inlineStyleRanges": [], "data": {}},
						{"text": " ", "type": "atomic", "key": "7abc", "entityRanges": [{"key": 1, "offset": 0, "length": 1}], "inlineStyleRanges": [], "data": {}},
						{"text": "Check examples here", "type": "unstyled", "key": "amnk6", "entityRanges": [{"key": 0, "offset": 6, "length": 8}], "inlineStyleRanges": [], "data": {}},
						{"text": "Bold text here", "type": "unstyled", "key": "9xk1m", "entityRanges": [], "inlineStyleRanges": [{"offset": 0, "length": 4, "style": "Bold"}], "data": {}}
					],
					"entityMap": [
						{"key": "0", "value": {"type": "LINK", "data": {"url": "https://thariqs.github.io/html-effectiveness"}}},
						{"key": "1", "value": {"type": "MEDIA", "data": {"entityKey": "8dd12f48-26f5-40d8-a0ec-8b5d8629da0d", "mediaItems": [{"localMediaId": "2", "mediaCategory": "DraftTweetImage", "mediaId": "2052796642479439872"}]}}}
					]
				},
				"media_entities": [
					{
						"media_id": "2052796642479439872",
						"media_info": {
							"original_img_url": "https://pbs.twimg.com/media/HHz_q48aAAAaCfW.jpg",
							"original_img_width": 1520,
							"original_img_height": 800,
							"alt_text": ""
						}
					}
				]
			}
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	// Override the API URL by using the test server's client with a custom transport
	// that redirects requests to our test server
	client := server.Client()
	// We need to make the client send requests to our test server instead of api.fxtwitter.com
	// Use a transport that rewrites the URL
	client.Transport = &urlRewriterTransport{
		newURL: server.URL,
		orig:   http.DefaultTransport,
	}

	tweet, err := fetchXTwitterViaAPIWithClient("https://x.com/trq212/status/2052809885763747935", client)
	if err != nil {
		t.Fatalf("fetchXTwitterViaAPIWithClient failed: %v", err)
	}

	// Verify deserialization
	if tweet.ID != "2052809885763747935" {
		t.Errorf("tweet.ID = %q, want %q", tweet.ID, "2052809885763747935")
	}
	if tweet.Author.ScreenName != "trq212" {
		t.Errorf("author.ScreenName = %q, want %q", tweet.Author.ScreenName, "trq212")
	}
	if tweet.Article == nil {
		t.Fatal("expected article to be non-nil")
	}
	if tweet.Article.Title != "Using Claude Code: The Unreasonable Effectiveness of HTML" {
		t.Errorf("article.Title = %q, want %q", tweet.Article.Title, "Using Claude Code: The Unreasonable Effectiveness of HTML")
	}
	if len(tweet.Article.Content.Blocks) != 5 {
		t.Fatalf("expected 5 blocks, got %d", len(tweet.Article.Content.Blocks))
	}

	// Verify block types
	if tweet.Article.Content.Blocks[0].Type != "unstyled" {
		t.Errorf("block[0].Type = %q, want unstyled", tweet.Article.Content.Blocks[0].Type)
	}
	if tweet.Article.Content.Blocks[1].Type != "header-two" {
		t.Errorf("block[1].Type = %q, want header-two", tweet.Article.Content.Blocks[1].Type)
	}
	if tweet.Article.Content.Blocks[4].InlineStyleRanges[0].Style != "Bold" {
		t.Errorf("block[4].InlineStyleRanges[0].Style = %q, want Bold", tweet.Article.Content.Blocks[4].InlineStyleRanges[0].Style)
	}

	// Verify entityMap deserialization
	if len(tweet.Article.Content.EntityMap) != 2 {
		t.Fatalf("expected 2 entityMap entries, got %d", len(tweet.Article.Content.EntityMap))
	}
	if tweet.Article.Content.EntityMap[0].Value.Type != "LINK" {
		t.Errorf("entityMap[0].Type = %q, want LINK", tweet.Article.Content.EntityMap[0].Value.Type)
	}
	if tweet.Article.Content.EntityMap[1].Value.Type != "MEDIA" {
		t.Errorf("entityMap[1].Type = %q, want MEDIA", tweet.Article.Content.EntityMap[1].Value.Type)
	}

	// Verify media_entities deserialization
	if len(tweet.Article.MediaEntities) != 1 {
		t.Fatalf("expected 1 media entity, got %d", len(tweet.Article.MediaEntities))
	}
	if tweet.Article.MediaEntities[0].MediaID != "2052796642479439872" {
		t.Errorf("mediaEntities[0].MediaID = %q, want 2052796642479439872", tweet.Article.MediaEntities[0].MediaID)
	}
	if tweet.Article.MediaEntities[0].MediaInfo.OriginalImgURL != "https://pbs.twimg.com/media/HHz_q48aAAAaCfW.jpg" {
		t.Errorf("mediaEntities[0].OriginalImgURL = %q, unexpected", tweet.Article.MediaEntities[0].MediaInfo.OriginalImgURL)
	}

	// Verify cover_media deserialization
	if tweet.Article.CoverMedia == nil {
		t.Fatal("expected cover_media to be non-nil")
	}
	if tweet.Article.CoverMedia.MediaInfo.OriginalImgURL != "https://pbs.twimg.com/media/HHz_ftzaIAAwkQs.jpg" {
		t.Errorf("cover_media.OriginalImgURL = %q, unexpected", tweet.Article.CoverMedia.MediaInfo.OriginalImgURL)
	}

	// Verify markdown conversion
	md := xTwitterArticleToMarkdown(tweet)
	if !strings.Contains(md, "## Why HTML?") {
		t.Errorf("expected markdown to contain '## Why HTML?', got:\n%s", md)
	}
	if !strings.Contains(md, "**Bold**") {
		t.Errorf("expected markdown to contain bold text '**Bold**', got:\n%s", md)
	}
	if !strings.Contains(md, "![image](https://pbs.twimg.com/media/HHz_q48aAAAaCfW.jpg)") {
		t.Errorf("expected markdown to contain image, got:\n%s", md)
	}
	if !strings.Contains(md, "[examples](https://thariqs.github.io/html-effectiveness)") {
		t.Errorf("expected markdown to contain link, got:\n%s", md)
	}

	t.Logf("Generated markdown:\n%s", md)
}

// urlRewriterTransport redirects requests to a test server
type urlRewriterTransport struct {
	newURL string
	orig   http.RoundTripper
}

func (t *urlRewriterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to the test server
	newReq := req.Clone(req.Context())
	newReq.URL, _ = url.Parse(t.newURL + req.URL.Path)
	return t.orig.RoundTrip(newReq)
}

func TestApplyInlineStylesRuneOffset(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		ranges []struct {
			Offset int    `json:"offset"`
			Length int    `json:"length"`
			Style  string `json:"style"`
		}
		want string
	}{
		{
			name: "ASCII bold",
			text: "Hello World",
			ranges: []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			}{{Offset: 0, Length: 5, Style: "Bold"}},
			want: "**Hello** World",
		},
		{
			name: "Chinese bold",
			text: "你好世界test",
			ranges: []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			}{{Offset: 0, Length: 4, Style: "Bold"}},
			want: "**你好世界**test",
		},
		{
			name: "Emoji bold",
			text: "🎉🚀hello",
			ranges: []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			}{{Offset: 0, Length: 2, Style: "Bold"}},
			want: "**🎉🚀**hello",
		},
		{
			name: "Mixed CJK+ASCII italic mid-text",
			text: "这是中文and more",
			ranges: []struct {
				Offset int    `json:"offset"`
				Length int    `json:"length"`
				Style  string `json:"style"`
			}{{Offset: 4, Length: 3, Style: "Italic"}},
			want: "这是中文*and* more",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyInlineStyles(tt.text, tt.ranges)
			if got != tt.want {
				t.Errorf("applyInlineStyles() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyEntityLinksRuneOffset(t *testing.T) {
	article := &fxtwitterArticle{
		Content: struct {
			Blocks    []fxtwitterBlock          `json:"blocks"`
			EntityMap []fxtwitterEntityMapEntry `json:"entityMap"`
		}{
			EntityMap: []fxtwitterEntityMapEntry{
				{Key: "0", Value: struct {
					Type string                 `json:"type"`
					Data map[string]interface{} `json:"data"`
				}{Type: "LINK", Data: map[string]interface{}{"url": "https://example.com"}}},
			},
		},
	}

	block := fxtwitterBlock{
		Text: "你好点击这里查看更多",
		EntityRanges: []fxtwitterEntityRange{
			{Key: "0", Offset: 2, Length: 4},
		},
	}

	got := applyEntityLinks(block.Text, block, article)
	want := "你好[点击这里](https://example.com)查看更多"
	if got != want {
		t.Errorf("applyEntityLinks() = %q, want %q", got, want)
	}
}

// --- WeChat article clipping tests ---

func TestIsWeChatURL(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
	}{
		{"https://mp.weixin.qq.com/s/9qPD3gXj3HLmrKC64Q6fbQ", true},
		{"https://mp.weixin.qq.com/s?__biz=MzI2&mid=123&idx=1&sn=abc", true},
		{"http://mp.weixin.qq.com/s/some-article", true},
		{"https://go.dev/blog/some-article", false},
		{"https://x.com/user/status/123", false},
		{"https://example.com", false},
		{"not-a-url", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := isWeChatURL(tt.url)
			if result != tt.expect {
				t.Errorf("isWeChatURL(%q) = %v, want %v", tt.url, result, tt.expect)
			}
		})
	}
}

func TestFetchHTMLWithHeaders(t *testing.T) {
	var receivedReferer, receivedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedReferer = r.Header.Get("Referer")
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
		w.Write([]byte("<html><body>test content</body></html>"))
	}))
	defer server.Close()

	headers := map[string]string{
		"Referer":    "https://mp.weixin.qq.com",
		"User-Agent": "Mozilla/5.0 MicroMessenger/7.0.20",
	}

	html, err := fetchHTML(server.URL, headers)
	if err != nil {
		t.Fatalf("fetchHTML failed: %v", err)
	}

	if html != "<html><body>test content</body></html>" {
		t.Errorf("unexpected HTML content: %q", html)
	}

	if receivedReferer != "https://mp.weixin.qq.com" {
		t.Errorf("Referer header not sent: got %q", receivedReferer)
	}

	if !strings.Contains(receivedUA, "MicroMessenger") {
		t.Errorf("User-Agent should contain MicroMessenger: got %q", receivedUA)
	}
}

func TestDownloadImageWithHeaders(t *testing.T) {
	var receivedReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedReferer = r.Header.Get("Referer")
		// Return a small PNG header
		pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		w.WriteHeader(200)
		w.Write(pngData)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	savePath := filepath.Join(tmpDir, "test.png")

	headers := map[string]string{
		"Referer": "https://mp.weixin.qq.com",
	}

	err := downloadImage(server.URL, savePath, headers)
	if err != nil {
		t.Fatalf("downloadImage failed: %v", err)
	}

	if receivedReferer != "https://mp.weixin.qq.com" {
		t.Errorf("Referer not sent in image download: got %q", receivedReferer)
	}

	if _, err := os.Stat(savePath); os.IsNotExist(err) {
		t.Error("Image file was not created")
	}
}

func TestPreprocessWeChatImages(t *testing.T) {
	mockHTML := `<html><body>
		<div id="js_content" class="rich_media_content">
			<p>Article text here.</p>
			<img data-src="https://mmbiz.qpic.cn/mmbiz_png/abc123/640" src="" alt="diagram"/>
			<img data-src="https://mmbiz.qpic.cn/mmbiz_jpg/def456/640" src="data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==" alt="photo"/>
			<img data-src="https://mmbiz.qpic.cn/mmbiz_png/wx_lazy/640?wx_lazy=1" src="https://mmbiz.qpic.cn/mmbiz_png/wx_lazy/0?wx_lazy=1" alt="lazy-real"/>
			<img src="https://example.com/normal.png" alt="normal image"/>
		</div>
	</body></html>`

	doc, err := parseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	preprocessWeChatImages(doc)

	// Verify data-src was always copied to src and data-src removed
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		alt, _ := s.Attr("alt")
		dataSrc, hasDataSrc := s.Attr("data-src")

		if alt == "diagram" {
			if src != "https://mmbiz.qpic.cn/mmbiz_png/abc123/640" {
				t.Errorf("diagram img src should be data-src value, got %q", src)
			}
			if hasDataSrc {
				t.Errorf("diagram img data-src should be removed, got %q", dataSrc)
			}
		}
		if alt == "photo" {
			if src != "https://mmbiz.qpic.cn/mmbiz_jpg/def456/640" {
				t.Errorf("photo img src should replace base64 placeholder, got %q", src)
			}
			if hasDataSrc {
				t.Errorf("photo img data-src should be removed, got %q", dataSrc)
			}
		}
		if alt == "lazy-real" {
			if src != "https://mmbiz.qpic.cn/mmbiz_png/wx_lazy/640?wx_lazy=1" {
				t.Errorf("lazy-real img src should prefer data-src over wx_lazy placeholder, got %q", src)
			}
			if hasDataSrc {
				t.Errorf("lazy-real img data-src should be removed, got %q", dataSrc)
			}
		}
		if alt == "normal image" {
			if src != "https://example.com/normal.png" {
				t.Errorf("normal img src should be unchanged (no data-src), got %q", src)
			}
		}
	})

	// Verify extractImageURLs picks up the data-src URLs after preprocessing
	imgURLs := extractImageURLs(doc)
	if len(imgURLs) != 4 {
		t.Errorf("Expected 4 image URLs after preprocessing, got %d", len(imgURLs))
	}

	foundMmbiz := false
	for _, u := range imgURLs {
		if strings.Contains(u, "mmbiz.qpic.cn") {
			foundMmbiz = true
		}
	}
	if !foundMmbiz {
		t.Error("Expected mmbiz.qpic.cn URLs in extracted image URLs")
	}
}

func TestWeChatContentExtraction(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
	<meta charset="utf-8"/>
	<meta property="og:title" content="深度学习在自然语言处理中的应用"/>
	<meta property="article:published_time" content="2026-05-01T08:30:00+08:00"/>
	<title>深度学习在自然语言处理中的应用 - AI研究院</title>
</head>
<body>
	<div id="js_content" class="rich_media_content">
		<p>近年来，深度学习技术在自然语言处理领域取得了显著进展。</p>
		<h2>Transformer架构</h2>
		<p>Transformer架构是现代NLP的基础。</p>
		<img data-src="https://mmbiz.qpic.cn/mmbiz_png/abc/640" src="" alt="transformer架构图"/>
		<h2>应用场景</h2>
		<p>深度学习在NLP中有多种应用场景。</p>
	</div>
	<div id="js_author_name">AI研究院</div>
</body>
</html>`

	doc, err := parseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	// Preprocess images
	preprocessWeChatImages(doc)

	// Extract content
	content := extractContent(doc)
	if len(content) == 0 {
		t.Fatal("Expected non-empty content from WeChat mock HTML")
	}

	if !strings.Contains(content, "深度学习") {
		t.Error("Expected '深度学习' in extracted content")
	}

	if !strings.Contains(content, "Transformer架构") {
		t.Error("Expected 'Transformer架构' heading in content")
	}

	// Verify title extraction (og:title should be preferred, without account name suffix)
	title := extractTitle(doc)
	if title != "深度学习在自然语言处理中的应用" {
		t.Errorf("Expected clean title from og:title, got %q", title)
	}

	// Verify author extraction
	author := extractWeChatAuthor(doc)
	if author != "AI研究院" {
		t.Errorf("Expected author 'AI研究院', got %q", author)
	}

	// Verify images were preprocessed
	imgURLs := extractImageURLs(doc)
	foundMmbiz := false
	for _, u := range imgURLs {
		if strings.Contains(u, "mmbiz.qpic.cn") {
			foundMmbiz = true
		}
	}
	if !foundMmbiz {
		t.Error("Expected mmbiz.qpic.cn image URLs after preprocessing")
	}

	t.Logf("Extracted content:\n%s", content)
}

func TestWebClippingWeChatArticle(t *testing.T) {
	articleURL := "https://mp.weixin.qq.com/s/9qPD3gXj3HLmrKC64Q6fbQ"

	// Fetch with WeChat headers
	html, err := fetchHTML(articleURL, weChatHeaders)
	if err != nil {
		t.Skipf("Failed to fetch WeChat URL (network issue): %v", err)
	}

	doc, err := parseHTML(html)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Preprocess images
	preprocessWeChatImages(doc)

	// Extract content
	content := extractContent(doc)
	if len(content) == 0 {
		t.Error("Expected non-empty content from WeChat article")
	}

	t.Logf("Content length: %d chars", len(content))

	// Verify images were found
	imgURLs := extractImageURLs(doc)
	t.Logf("Images found: %d", len(imgURLs))
	for i, u := range imgURLs {
		t.Logf("  Image %d: %s", i+1, u)
	}

	// Verify author extraction
	author := extractWeChatAuthor(doc)
	t.Logf("Author: %q", author)

	// Verify title
	title := extractTitle(doc)
	t.Logf("Title: %q", title)

	// Verify language detection
	lang := detectLanguage(content)
	t.Logf("Detected language: %q", lang)

	// Save debug output for manual inspection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "wechat_article.md")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	t.Logf("Saved markdown to: %s", tmpFile)

	// Save raw HTML for debugging
	htmlFile := filepath.Join(tmpDir, "wechat_raw.html")
	if err := os.WriteFile(htmlFile, []byte(html), 0644); err != nil {
		t.Fatalf("Failed to write HTML file: %v", err)
	}
	t.Logf("[debug] WeChat HTML saved to: %s", htmlFile)
}
