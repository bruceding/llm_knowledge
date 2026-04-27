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