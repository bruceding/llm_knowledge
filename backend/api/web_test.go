package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	html, err := fetchHTML("https://go.dev/blog/type-construction-and-cycle-detection")
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
			name:     "code block with indentation",
			html:     `<pre><code class="language-go">type Node struct {
  next *Node
}
</code></pre>`,
			expected: "\n```go\ntype Node struct {\n  next *Node\n}\n```\n\n",
		},
		{
			name: "table with header",
			html: `<table><thead><tr><th>Feature</th><th>Polling</th><th>SSE</th></tr></thead><tbody><tr><td>Direction</td><td>Client-to-server</td><td>Server-to-client</td></tr><tr><td>Protocol</td><td>HTTP</td><td>HTTP</td></tr></tbody></table>`,
			expected: "| Feature | Polling | SSE |\n|---|---|---|\n| Direction | Client-to-server | Server-to-client |\n| Protocol | HTTP | HTTP |\n\n",
		},
		{
			name: "table without thead (th in first row)",
			html: `<table><tr><th>Name</th><th>Value</th></tr><tr><td>Test</td><td>123</td></tr></table>`,
			expected: "| Name | Value |\n|---|---|\n| Test | 123 |\n\n",
		},
		{
			name: "table with td only (first row as header)",
			html: `<table><tr><td>Col1</td><td>Col2</td></tr><tr><td>A</td><td>B</td></tr></table>`,
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