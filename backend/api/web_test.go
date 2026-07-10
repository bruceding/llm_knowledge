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
		{
			name:     "empty list items are skipped",
			html:     `<ul><li>Real item</li><li><section><span><br/></span></section></li><li></li><li>Another item</li></ul>`,
			expected: "- Real item\n- Another item\n",
		},
		{
			name:     "ordered list skips empty items and renumbers",
			html:     `<ol><li>First</li><li></li><li>Third</li></ol>`,
			expected: "1. First\n2. Third\n",
		},
		{
			name:     "no-break space replaced with regular space",
			html:     "<p>hello world</p>",
			expected: "hello world",
		},
		{
			name:     "no-break space in code block cleaned",
			html:     "<pre><code>func main() {}</code></pre>",
			expected: "```\nfunc main() {}\n```",
		},
		{
			name:     "code block with per-line child elements",
			html:     `<pre><code><span>SKILLS = (</span><span>    "line1",</span><span>    "line2"</span><span>)</span></code></pre>`,
			expected: "```\nSKILLS = (\n    \"line1\",\n    \"line2\"\n)\n```",
		},
		{
			// Issue #47: Medium uses <pre><span data-selectable-paragraph><br/> structure,
			// no <code> child — newlines come from <br/> elements between text runs.
			name:     "Medium-style pre with span and br (no code child)",
			html:     `<pre><span data-selectable-paragraph>status := "active"<br/>age := 30<br/>payload := UserUpdate{<br/>    Status: &amp;status,<br/>    Age:    &amp;age,<br/>}</span></pre>`,
			expected: "```\nstatus := \"active\"\nage := 30\npayload := UserUpdate{\n    Status: &status,\n    Age:    &age,\n}\n```",
		},
		{
			name:     "Medium-style pre comment + func with br lines",
			html:     `<pre><span data-selectable-paragraph>// The generic helper found in almost every Go codebase<br/>func Ptr[T any](v T) *T {<br/>    return &amp;v<br/>}</span></pre>`,
			expected: "```\n// The generic helper found in almost every Go codebase\nfunc Ptr[T any](v T) *T {\n    return &v\n}\n```",
		},
		{
			// Issue #63: JetBrains blog uses EnlighterJS which renders a triple
			// payload (toolbar + raw textarea + highlighted spans). Without a
			// dedicated handler, each block appears 3× plus 4 toolbar lines.
			// Detect the wrapper, extract once from .enlighter-raw, return early.
			name: "Enlighter wrapper with raw + highlighted code (JetBrains blog)",
			html: `<div class="EnlighterJSWrapper"><div class="enlighter-toolbar-top"><a>Plain text</a><a>Copy to clipboard</a><a>Open code in new window</a><a>EnlighterJS 3 Syntax Highlighter</a></div><ol class="enlighter enlighter-default enlighter-l-go"><li><span class="enlighter-text">func main() {}</span></li></ol><div class="enlighter-raw">func main() {}</div></div>`,
			expected: "```go\nfunc main() {}\n```",
		},
		{
			// Multi-line Enlighter block: verify raw text is preserved verbatim
			// (no per-token concatenation, no toolbar bleed-through).
			name: "Enlighter multi-line code preserves newlines",
			html: `<div class="EnlighterJSWrapper"><div class="enlighter-toolbar-top"><a>Plain text</a></div><ol class="enlighter enlighter-default enlighter-l-go"><li>noise</li></ol><div class="enlighter-raw">package main

func main() {
    println("hi")
}</div></div>`,
			expected: "```go\npackage main\n\nfunc main() {\n    println(\"hi\")\n}\n```",
		},
		{
			// Fallback path: when .enlighter-raw is missing, derive code from
			// the .enlighter-default highlighted box's text (still single copy).
			name: "Enlighter without raw falls back to highlighted box text",
			html: `<div class="EnlighterJSWrapper"><ol class="enlighter enlighter-default enlighter-l-python"><li>print('x')</li></ol></div>`,
			expected: "```python\nprint('x')\n```",
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

// TestExtractContentJetBrainsBlog reproduces the JetBrains Go blog structure
// from issue #63: data-clarity-region="article" scopes the body, Copy-heading-link
// buttons leak into <h2>, and Enlighter wrappers emit code in triplicate plus a
// 4-line toolbar. Verifies that ExtractContent + convertNodeToMarkdown together:
//   - drop nav/footer/Subscribe/Discover-more (P0-B selector scope)
//   - strip Copy-heading-link / sr-only noise (P1-A removal pass)
//   - render each Enlighter block exactly once with the right language fence (P0-A)
func TestExtractContentJetBrainsBlog(t *testing.T) {
	html := `<!doctype html><html><body>
<nav class="Header">SITE NAV — should be dropped</nav>
<main>
<div data-clarity-region="article">
<h1>Golang Profiling Guide<button class="copy-button" aria-label="Copy heading link"><span class="sr-only">Copy heading link</span></button></h1>
<p>Intro paragraph that explains profiling.</p>
<h2>CPU profiles<button class="copy-button" aria-label="Copy heading link"><span class="sr-only">Copy heading link</span></button></h2>
<p>Use the runtime/pprof package.</p>
<div class="EnlighterJSWrapper">
  <div class="enlighter-toolbar-top">
    <a>Plain text</a><a>Copy to clipboard</a><a>Open code in new window</a><a>EnlighterJS 3 Syntax Highlighter</a>
  </div>
  <ol class="enlighter enlighter-default enlighter-l-go">
    <li>noise highlight tokens here</li>
  </ol>
  <div class="enlighter-raw">import "runtime/pprof"

func main() {
    pprof.StartCPUProfile(nil)
}</div>
</div>
<p>Trailing paragraph.</p>
</div>
</main>
<footer>FOOTER — should be dropped</footer>
<section class="Subscribe">Subscribe form — should be dropped</section>
<section class="DiscoverMore">Discover more — should be dropped</section>
</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := ExtractContent(doc)

	// UI noise must be gone (regions outside data-clarity-region).
	for _, banned := range []string{
		"SITE NAV",
		"FOOTER",
		"Subscribe form",
		"Discover more",
		"Copy heading link",
		"Plain text",
		"Copy to clipboard",
		"Open code in new window",
		"EnlighterJS 3 Syntax Highlighter",
		"noise highlight tokens",
	} {
		if strings.Contains(result, banned) {
			t.Errorf("expected %q to be stripped, still present in:\n%s", banned, result)
		}
	}

	// Code block must appear exactly once, with the go fence.
	if got := strings.Count(result, "```go"); got != 1 {
		t.Errorf("expected exactly 1 ```go fence, got %d in:\n%s", got, result)
	}
	if !strings.Contains(result, "import \"runtime/pprof\"") {
		t.Errorf("expected raw code preserved verbatim, got:\n%s", result)
	}
	// Three identical bodies would indicate the duplication bug returned.
	if strings.Count(result, "pprof.StartCPUProfile(nil)") != 1 {
		t.Errorf("code body should appear once, got %d in:\n%s",
			strings.Count(result, "pprof.StartCPUProfile(nil)"), result)
	}

	// Headings should be clean (no Copy heading link suffix).
	if !strings.Contains(result, "# Golang Profiling Guide\n") {
		t.Errorf("expected clean h1, got:\n%s", result)
	}
	if !strings.Contains(result, "## CPU profiles\n") {
		t.Errorf("expected clean h2, got:\n%s", result)
	}
}

// TestEnlighterLanguageFromLClass verifies that the language extracted from an
// Enlighter wrapper is the value of the "enlighter-l-{lang}" class (the actual
// language marker), not the "enlighter-v-{variant}" theme/variant class.
// Regression for issue #65 (real JetBrains markup carries both classes; the old
// findLang loop returned "v-standard" because it accepted the first non-blacklisted
// "enlighter-*" prefix it saw).
func TestEnlighterLanguageFromLClass(t *testing.T) {
	html := `<div class="EnlighterJSWrapper">` +
		`<ol class="enlighter enlighter-default enlighter-v-standard enlighter-l-go enlighter-t-classic">` +
		`<li>noise</li></ol>` +
		`<div class="enlighter-raw">package main</div></div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var result string
	doc.Find("body").Contents().Each(func(_ int, s *goquery.Selection) {
		result += convertNodeToMarkdown(s)
	})
	result = strings.TrimSpace(result)
	want := "```go\npackage main\n```"
	if result != want {
		t.Errorf("expected\n%s\ngot\n%s", want, result)
	}
}

// TestEnlighterLanguageFromDataAttr verifies the data-enlighter-language fallback
// when no enlighter-l-{lang} class is present (some JetBrains pages put the
// language on the original <pre> via data-enlighter-language="golang" only).
func TestEnlighterLanguageFromDataAttr(t *testing.T) {
	html := `<div class="EnlighterJSWrapper" data-enlighter-language="golang">` +
		`<ol class="enlighter enlighter-default"><li>noise</li></ol>` +
		`<div class="enlighter-raw">package main</div></div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var result string
	doc.Find("body").Contents().Each(func(_ int, s *goquery.Selection) {
		result += convertNodeToMarkdown(s)
	})
	result = strings.TrimSpace(result)
	want := "```go\npackage main\n```"
	if result != want {
		t.Errorf("expected\n%s\ngot\n%s", want, result)
	}
}

// TestPreEmptyProducesNoFence ensures an empty <pre> does not emit a stray
// empty fenced code block. Reproduces issue #65 bug 4 where every real
// code block was preceded by an empty ``` ``` (origin: empty Enlighter source
// <pre> or .enlighter-code render box after Enlighter init).
//
// Two defenses are exercised here: the EnlighterJSRAW short-circuit at the
// top of `case "pre":` (skips before any extraction), and the empty-content
// guard at the bottom (catches every other empty/whitespace shape).
func TestPreEmptyProducesNoFence(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"truly empty pre", `<pre></pre>`},
		{"pre with whitespace only", `<pre>

	</pre>`},
		{"pre with empty code child", `<pre><code></code></pre>`},
		// Short-circuited by the EnlighterJSRAW class check (path 1).
		{"pre with EnlighterJSRAW class", `<pre class="EnlighterJSRAW" data-enlighter-language="golang"></pre>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tc.html))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var result string
			doc.Find("body").Contents().Each(func(_ int, s *goquery.Selection) {
				result += convertNodeToMarkdown(s)
			})
			if strings.Contains(result, "```") {
				t.Errorf("expected no fenced block, got:\n%q", result)
			}
		})
	}
}

// TestEnlighterJSRAWSkippedBeforeWrapper verifies that the original
// <pre class="EnlighterJSRAW"> source block (which Enlighter renders via the
// adjacent .EnlighterJSWrapper) does not also emit a duplicate or empty
// fenced block.
func TestEnlighterJSRAWSkippedBeforeWrapper(t *testing.T) {
	html := `<pre class="EnlighterJSRAW" data-enlighter-language="golang">package main</pre>` +
		`<div class="EnlighterJSWrapper">` +
		`<ol class="enlighter enlighter-default enlighter-l-go"><li>noise</li></ol>` +
		`<div class="enlighter-raw">package main</div></div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var result string
	doc.Find("body").Contents().Each(func(_ int, s *goquery.Selection) {
		result += convertNodeToMarkdown(s)
	})
	if got := strings.Count(result, "```go"); got != 1 {
		t.Errorf("expected exactly 1 ```go fence, got %d in:\n%s", got, result)
	}
	if strings.Count(result, "package main") != 1 {
		t.Errorf("expected exactly 1 'package main', got %d in:\n%s",
			strings.Count(result, "package main"), result)
	}
}

// TestExtractContentJetBrainsBlogNoise reproduces issue #65: the JetBrains
// blog injects header (site logo / social-follow row / download button /
// category nav) and footer (tags / share / subscribe / recommended cards /
// table-of-contents) noise INSIDE the data-clarity-region article wrapper.
// Verifies these class-based UI regions are removed.
//
// Issue #67 follow-up: the JetBrains article BODY lives inside
// "<div class='content js-toc-content'>" — "js-toc-content" is a TOC-aware
// content region (the floating TOC tracks its scroll position), NOT the TOC
// itself. The real TOC lives in "<div class='js-toc'>" and is opened via
// "<a class='toc-opener'>". The previous fix mistakenly stripped
// .js-toc-content and erased every JetBrains article body; this test now
// reflects the real markup so the regression cannot return.
func TestExtractContentJetBrainsBlogNoise(t *testing.T) {
	html := `<!doctype html><html><body>
<div data-clarity-region="article">
<div class="product-header">
  <a href="/go/"><img src="logo.svg" alt="Go logo"/> GoLand</a>
  <p>The IDE for professional development in Go</p>
  <ul class="social-links"><li><a href="#">Follow</a></li><li><a href="#">X</a></li></ul>
  <a class="download-button" href="/download/">Download</a>
  <nav class="category-nav"><a href="/category/goland/">GoLand</a></nav>
</div>
<div class="content js-toc-content">
  <h1>A Practical Guide to Profiling in Go</h1>
  <p>Intro.</p>
  <p>Happy coding!<br>The GoLand Team</p>
</div>
<a class="toc-opener" href="#">Open TOC</a>
<div class="js-toc"><ol class="toc-list"><li>TOC item</li></ol></div>
<div class="post-tags"><a href="/tag/cpu-profiling/">CPU profiling</a></div>
<div class="article-share"><a href="#">Share</a><a href="#">Facebook</a></div>
<aside class="post-navigation"><a href="#">Prev post</a></aside>
<form class="subscribe-form">Subscribe to GoLang Blog updates<input/></form>
<div class="discover-more"><h3>Discover more</h3><a href="#">Article A</a></div>
</div>
</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result := ExtractContent(doc)

	// Body (inside .js-toc-content) must survive — this is the regression
	// guard for issue #67.
	if !strings.Contains(result, "A Practical Guide to Profiling in Go") {
		t.Errorf("expected article body, got:\n%s", result)
	}
	if !strings.Contains(result, "Intro.") {
		t.Errorf("expected body paragraph, got:\n%s", result)
	}
	if !strings.Contains(result, "The GoLand Team") {
		t.Errorf("expected author byline, got:\n%s", result)
	}

	// Header-noise classes must be stripped.
	for _, banned := range []string{
		"Go logo",
		"The IDE for professional development",
		"Follow",
		"Download",
		"category/goland",
	} {
		if strings.Contains(result, banned) {
			t.Errorf("header noise %q leaked, full output:\n%s", banned, result)
		}
	}

	// Footer-noise classes must be stripped — including the REAL TOC
	// (".js-toc" + "a.toc-opener"), not the body container.
	for _, banned := range []string{
		"CPU profiling",
		"Facebook",
		"Prev post",
		"Subscribe to GoLang Blog",
		"Discover more",
		"Article A",
		"Open TOC",
		"TOC item",
	} {
		if strings.Contains(result, banned) {
			t.Errorf("footer noise %q leaked, full output:\n%s", banned, result)
		}
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

	// Use ExtractContent function (same as production code)
	result := ExtractContent(doc)

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
	content := ExtractContent(doc)

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

	doc, err := ParseHTML(mockHTML)
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
	content := ExtractContent(doc)
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

// TestExtractContentMediumMock verifies issue #47:
// Medium articles imported via browser extension should not include byline/UI noise
// (avatar, follow button, read time, claps, Listen/Share/More, image overlay text).
//
// CRITICAL: simulates the browser-extension scenario where <head> is EMPTY (the
// extension strips all meta tags before POSTing the DOM). Detection must rely on
// [data-selectable-paragraph], not og:site_name, since meta tags are not present.
func TestExtractContentMediumMock(t *testing.T) {
	// Empty <head> mirrors what the wiki browser extension actually sends — see
	// issue #47 thread: "POST /api/raw/web-clip bytes_in:38197" is a stripped DOM,
	// not the full 200KB+ Medium page. og:site_name etc. are gone. The only Medium
	// signal that survives is [data-selectable-paragraph] on body paragraphs.
	// Production DOM (per issue #47 follow-up): byline elements (avatar link,
	// author link, "4 min read", "May 2, 2026", clap counts, Follow/Listen/Share/
	// More buttons) are DIRECT SIBLINGS of the <p data-selectable-paragraph>
	// content paragraphs inside <article>. They are NOT enclosed in a separate
	// sub-container, so taking parent-of-first-paragraph alone is not enough —
	// extraction must walk up and prune preceding siblings at each ancestor level.
	mockHTML := `<!DOCTYPE html>
<html lang="en">
<head></head>
<body>
	<div id="root">
		<article>
			<h1 class="pw-post-title">Golang 1.26 Update: Stop Writing Pointer Helpers with new(expr)</h1>
			<a href="/?source=post_page---byline--7d8296e15fb8---------------------------------------"><img alt="ElAmir Mansour" src="https://miro.medium.com/v2/avatar.jpeg"/></a>
			<a href="/?source=post_page---byline--7d8296e15fb8---------------------------------------">ElAmir Mansour</a>
			<button>Follow</button>
			<span>4 min read</span>
			<span>·</span>
			<span>May 2, 2026</span>
			<span>67</span>
			<span>1</span>
			<button aria-label="Listen">Listen</button>
			<button aria-label="Share">Share</button>
			<button aria-label="More">More</button>
			<p data-selectable-paragraph>Go 1.26 introduces a small but very practical addition: a new built-in called <code>new(expr)</code> that lets you create a pointer to a value in one step.</p>
			<p data-selectable-paragraph>Before Go 1.26, you needed a helper function to create a pointer to a literal value:</p>
			<pre><code>func ptr[T any](v T) *T { return &amp;v }</code></pre>
			<h2 data-selectable-paragraph>The new way</h2>
			<p data-selectable-paragraph>With Go 1.26, you can simply write <code>new(42)</code> to get an <code>*int</code> pointing to 42.</p>
			<figure>
				<picture><img alt="Code example" src="https://miro.medium.com/v2/code-example.png"/></picture>
				<button>Press enter or click to view image in full size</button>
				<figcaption>Example usage</figcaption>
			</figure>
			<p data-selectable-paragraph>This is a welcome ergonomic improvement for everyday Go programming.</p>
			<a href="/?source=post_page---post_publication_info--7d8296e15fb8---------------------------------------">Published in Some Publication</a>
			<button>Sign up</button>
		</article>
	</div>
</body>
</html>`

	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	content := ExtractContent(doc)
	t.Logf("Extracted Medium content (%d chars):\n%s", len(content), content)

	// === The actual article content MUST be present ===
	wantPresent := []string{
		"Go 1.26 introduces a small but very practical addition",
		"Before Go 1.26, you needed a helper function",
		"The new way",
		"With Go 1.26, you can simply write",
		"This is a welcome ergonomic improvement",
	}
	for _, s := range wantPresent {
		if !strings.Contains(content, s) {
			t.Errorf("expected content to contain %q, but it did not", s)
		}
	}

	// === Byline / UI noise MUST NOT be present ===
	wantAbsent := []string{
		"ElAmir Mansour",                                  // author name in byline
		"Follow",                                          // follow button
		"4 min read",                                      // read time
		"May 2, 2026",                                     // publish date span (issue #47 screenshot)
		"Listen",                                          // listen button
		"Share",                                           // share button
		"Press enter or click to view image in full size", // image overlay button text
		"Sign up",                                         // footer button
		"Published in Some Publication",                   // footer publication link
		"miro.medium.com/v2/avatar.jpeg",                  // author avatar URL
		"source=post_page---byline",                       // medium internal byline link
	}
	for _, s := range wantAbsent {
		if strings.Contains(content, s) {
			t.Errorf("expected content NOT to contain %q, but it did", s)
		}
	}
}

// TestExtractContentMediumIssue48 reproduces the four problems reported for
// Medium custom-domain articles (issue #48 — Netflix TechBlog):
//  1. byline (avatar + author link + read-time + date + actions) sits BETWEEN
//     the <h1> and the first <p data-selectable-paragraph>, so the existing
//     "prune-before-first-selectable" logic (which targets the h1) doesn't
//     catch it.
//  2. "Press enter or click to view image in full size" overlay is in a <div>
//     rather than a <button> in some builds.
//  3. The hero image lives inside an <a> wrapper which used to produce
//     "[![alt](src)\n\n](url)" — broken markdown.
//  4. Article images are lazy-loaded: <img> has no usable src, real URL is in
//     a sibling <source srcset> inside a <picture>.
func TestExtractContentMediumIssue48(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html lang="en">
<head></head>
<body>
	<article>
		<h1 data-selectable-paragraph>Democratizing Machine Learning at Netflix: Building the Model Lifecycle Graph</h1>
		<div class="byline-wrapper">
			<div class="author-block">
				<a href="https://netflixtechblog.medium.com/?source=post_page---byline--5cc6d5828bb1---------------------------------------"><img alt="Netflix Technology Blog" src="https://miro.medium.com/v2/avatar.jpeg"/></a>
				<a href="https://netflixtechblog.medium.com/?source=post_page---byline--5cc6d5828bb1---------------------------------------">Netflix Technology Blog</a>
			</div>
			<div class="meta-block">
				<span>14 min read</span>
				<span>·</span>
				<span>May 4, 2026</span>
			</div>
			<div class="actions">
				<span>2</span>
				<button>Listen</button>
				<button>Share</button>
				<button>More</button>
			</div>
			<p>Saish Sali, Nipun Kumar, Sura Elamurugu</p>
		</div>
		<p data-selectable-paragraph>At Netflix we build hundreds of ML models in production, and managing their lifecycle is hard.</p>
		<figure>
			<picture>
				<source srcset="https://miro.medium.com/v2/resize:fit:640/1*hero.png 640w, https://miro.medium.com/v2/resize:fit:1280/1*hero.png 1280w"/>
				<img alt="Architecture overview" src=""/>
			</picture>
			<div>Press enter or click to view image in full size</div>
			<figcaption>Figure 1: Architecture overview</figcaption>
		</figure>
		<p data-selectable-paragraph>The Model Lifecycle Graph captures dependencies between training, evaluation, and deployment.</p>
	</article>
</body>
</html>`

	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	// Simulate the lazy-image preprocessing that runs in saveWebDocument
	// before extraction. Without it, the hero <img src=""> is unusable.
	preprocessLazyImages(doc)

	content := ExtractContent(doc)
	t.Logf("Extracted Medium content (%d chars):\n%s", len(content), content)

	wantPresent := []string{
		"At Netflix we build hundreds of ML models",
		"The Model Lifecycle Graph captures dependencies",
		// Problem 4: hero image URL was lifted from <source srcset>
		"https://miro.medium.com/v2/resize:fit:1280/1*hero.png",
	}
	for _, s := range wantPresent {
		if !strings.Contains(content, s) {
			t.Errorf("expected content to contain %q, but it did not", s)
		}
	}

	wantAbsent := []string{
		// Problem 2: byline noise (avatar, author, date, actions, contributor line)
		"Netflix Technology Blog",
		"14 min read",
		"May 4, 2026",
		"Listen",
		"Share",
		"Saish Sali, Nipun Kumar, Sura Elamurugu",
		"miro.medium.com/v2/avatar.jpeg",
		"source=post_page---byline",
		// Problem 2: figure overlay
		"Press enter or click to view image in full size",
	}
	for _, s := range wantAbsent {
		if strings.Contains(content, s) {
			t.Errorf("expected content NOT to contain %q, but it did", s)
		}
	}

	// Problem 3: image wrapped in <a> must not split markdown across
	// blank lines. The broken pattern is "](" preceded by "\n\n".
	if strings.Contains(content, "\n\n](") {
		t.Errorf("image markdown is broken across blank lines: %q", content)
	}
}

// TestPreprocessLazyImages verifies that empty/placeholder <img src> values
// are filled in from data-src, srcset, or <picture><source srcset>.
func TestPreprocessLazyImages(t *testing.T) {
	html := `<html><body>
		<img id="a" src="" data-src="https://cdn.example.com/a.jpg"/>
		<img id="b" src="data:image/gif;base64,R0lGODlh" srcset="https://cdn.example.com/b-small.jpg 640w, https://cdn.example.com/b-large.jpg 1280w"/>
		<picture>
			<source srcset="https://cdn.example.com/c-small.jpg 640w, https://cdn.example.com/c-large.jpg 1280w"/>
			<img id="c" src=""/>
		</picture>
		<img id="d" src="https://cdn.example.com/d.jpg" data-src="https://cdn.example.com/should-not-override.jpg"/>
	</body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	preprocessLazyImages(doc)

	cases := map[string]string{
		"#a": "https://cdn.example.com/a.jpg",
		"#b": "https://cdn.example.com/b-large.jpg",
		"#c": "https://cdn.example.com/c-large.jpg",
		"#d": "https://cdn.example.com/d.jpg",
	}
	for sel, want := range cases {
		got, _ := doc.Find(sel).Attr("src")
		if got != want {
			t.Errorf("%s: got src=%q, want %q", sel, got, want)
		}
	}
}

// TestConvertImageInsideLink verifies that an <img> wrapped in <a> renders
// as "[![alt](src)](url)" — not "[![alt](src)\n\n](url)" which breaks
// markdown parsers (issue #48 problem 3).
func TestConvertImageInsideLink(t *testing.T) {
	html := `<html><body><div><a href="https://example.com/post"><img alt="hero" src="https://cdn.example.com/hero.jpg"/></a></div></body></html>`
	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out strings.Builder
	doc.Find("div").Contents().Each(func(i int, s *goquery.Selection) {
		out.WriteString(convertNodeToMarkdown(s))
	})
	got := out.String()
	if strings.Contains(got, "\n\n](") {
		t.Errorf("image-in-link markdown broken across blank lines: %q", got)
	}
	want := "[![hero](https://cdn.example.com/hero.jpg)](https://example.com/post)"
	if !strings.Contains(got, want) {
		t.Errorf("got %q, want it to contain %q", got, want)
	}
}

// TestAbsolutizeLinks covers issue #89 item 5: relative markdown link targets
// must be resolved to absolute URLs against the source page, while image links,
// fragments, non-http schemes, and already-absolute URLs are left untouched.
func TestAbsolutizeLinks(t *testing.T) {
	const base = "https://openai.com/index/separating-signal-from-noise-coding-evaluations/"
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "root-relative link",
			content: "See [Research](/news/research/) for details.",
			want:    "See [Research](https://openai.com/news/research/) for details.",
		},
		{
			name:    "relative link resolved against page dir",
			content: "[next](../other/)",
			want:    "[next](https://openai.com/index/other/)",
		},
		{
			name:    "image link left untouched (already localized)",
			content: "![cover](assets/img_2.png)",
			want:    "![cover](assets/img_2.png)",
		},
		{
			name:    "absolute link untouched",
			content: "[home](https://example.com/x)",
			want:    "[home](https://example.com/x)",
		},
		{
			name:    "fragment untouched",
			content: "[top](#section)",
			want:    "[top](#section)",
		},
		{
			name:    "mailto untouched",
			content: "[mail](mailto:a@b.com)",
			want:    "[mail](mailto:a@b.com)",
		},
		{
			name:    "mixed link and image on one line",
			content: "![c](assets/img_1.png) and [Research](/news/research/)",
			want:    "![c](assets/img_1.png) and [Research](https://openai.com/news/research/)",
		},
		{
			// Regression: image-in-link ([![alt](src)](url)) must not have its
			// already-localized asset src absolutized (Medium hero image, issue #48).
			name:    "image wrapped in link left untouched",
			content: "[![hero](assets/img_1.png)](https://example.com/post)",
			want:    "[![hero](assets/img_1.png)](https://example.com/post)",
		},
		{
			// Inline image inside link text must not be corrupted either.
			name:    "inline image within link text",
			content: "[see ![icon](assets/i.png) here](https://example.com/x)",
			want:    "[see ![icon](assets/i.png) here](https://example.com/x)",
		},
		{
			// Regression: code inside a fence (arr[i](x) shape) must not be rewritten.
			name:    "code fence call expression not corrupted",
			content: "```js\nhandlers[type](event);\n```",
			want:    "```js\nhandlers[type](event);\n```",
		},
		{
			// Regression: a markdown-link example inside a fence must survive.
			name:    "markdown link example inside fence not corrupted",
			content: "```\n[click here](/docs/intro)\n```",
			want:    "```\n[click here](/docs/intro)\n```",
		},
		{
			// A call expression on a prose line: href is a bare token, not a path.
			name:    "bare token call on prose line not corrupted",
			content: "Use funcs[i](arg) to dispatch.",
			want:    "Use funcs[i](arg) to dispatch.",
		},
		{
			// Real link on a prose line after a preceding fence still gets rewritten.
			name:    "link after fence still absolutized",
			content: "```\ncode[x](y)\n```\n\nSee [Docs](/docs/).",
			want:    "```\ncode[x](y)\n```\n\nSee [Docs](https://openai.com/docs/).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := absolutizeLinks(tt.content, base); got != tt.want {
				t.Errorf("absolutizeLinks(%q)\n got: %q\nwant: %q", tt.content, got, tt.want)
			}
		})
	}

	// Empty base URL is a no-op (defensive: never fabricate links).
	if got := absolutizeLinks("[x](/y)", ""); got != "[x](/y)" {
		t.Errorf("empty base should be no-op, got %q", got)
	}
}

// TestExtractContentOpenAICodeBlock covers issue #89 item 1: OpenAI blog code
// blocks use a custom <div class="rich-text-code-example"> structure with an
// <h4>None</h4> language label and per-line rows (<span>N</span> + content div)
// that the generic <pre>/<code> handlers mangle into "#### None" + a single
// backtick blob with merged line numbers. The special-case must emit a clean
// fenced block, dropping the line numbers and the "None" label, and preserve
// OpenAI's literal "[space]" placeholder (their authored content, not our bug).
func TestExtractContentOpenAICodeBlock(t *testing.T) {
	mockHTML := `<html><body><article>
	<div class="not-prose border-primary-12 rich-text-code-example mb-12 overflow-hidden">
	  <div class="grid grid-cols-12">
	    <div class="col-span-full flex flex-col gap-3">
	      <div class="flex flex-col overflow-hidden rounded-md bg-tertiary-100">
	        <div class="relative z-1 flex justify-between p-5">
	          <h4 class="text-primary-100 text-p2 font-bold capitalize">None</h4>
	          <button type="button" aria-label="Copy code block"><svg viewBox="0 0 18 18"><path d="M0 0"></path></svg></button>
	        </div>
	        <div dir="ltr" class="relative flex items-stretch gap-4">
	          <code class="text-primary-100 text-code-snippet flex-1 font-mono CodeBlock-module__syntaxHighlight">
	            <pre class="flex flex-col pe-5"><div class="flex flex-row gap-4"><span class="text-primary-44 ms-5 min-w-5 text-end">1</span><div class="flex-1">"[space]| Chapter 1 | 1"</div></div><div class="flex flex-row gap-4"><span class="text-primary-44 ms-5 min-w-5 text-end">2</span><div class="flex-1">"**[space]| Chapter 1 | 1"</div></div><div class="flex flex-row gap-4"><span class="text-primary-44 ms-5 min-w-5 text-end">3</span><div class="flex-1">"[space]| Just title | "</div></div></pre>
	          </code>
	        </div>
	      </div>
	    </div>
	  </div>
	</div>
	</article></body></html>`
	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := ExtractContent(doc)

	// The "None" language label must not leak as a heading.
	if strings.Contains(got, "None") {
		t.Errorf("language label 'None' leaked into output:\n%s", got)
	}
	// Line-number gutter (1/2/3) must be dropped, not merged into content.
	if strings.Contains(got, "1\"[space]") || strings.Contains(got, "1 | 1\"2") {
		t.Errorf("line numbers merged into code:\n%s", got)
	}
	// Proper fenced block with one line per row, "[space]" preserved verbatim.
	want := "```\n\"[space]| Chapter 1 | 1\"\n\"**[space]| Chapter 1 | 1\"\n\"[space]| Just title | \"\n```"
	if !strings.Contains(got, want) {
		t.Errorf("code block not converted to a clean fence.\n got: %q\nwant contains: %q", got, want)
	}
}

// TestExtractContentOpenAICodeBlockWithLanguage verifies the <h4> label is used
// as the fence language when it is not the "None" placeholder.
func TestExtractContentOpenAICodeBlockWithLanguage(t *testing.T) {
	mockHTML := `<html><body><article>
	<div class="rich-text-code-example">
	  <div class="flex flex-col bg-tertiary-100">
	    <div class="flex justify-between p-5"><h4 class="capitalize">Python</h4></div>
	    <code class="CodeBlock-module__syntaxHighlight"><pre class="flex flex-col"><div class="flex flex-row"><span class="min-w-5">1</span><div class="flex-1">print("hi")</div></div></pre></code>
	  </div>
	</div>
	</article></body></html>`
	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := ExtractContent(doc)
	if !strings.Contains(got, "```Python\nprint(\"hi\")\n```") {
		t.Errorf("expected fenced block with language Python, got:\n%s", got)
	}
}

// TestCleanOpenAINoise covers issue #89 items 3 & 4: OpenAI's client-rendered
// audio/share widgets and the trailing "Keep reading" recommendation block must
// be stripped from the extracted markdown, while real body content is preserved.
func TestCleanOpenAINoise(t *testing.T) {
	input := strings.Join([]string{
		"# Separating signal from noise in coding evaluations",
		"",
		"Through a detailed audit, we find widespread task issues.",
		"",
		"Listen to article 7:06",
		"",
		"Share",
		"",
		"Accurately measuring our models' capabilities is important.",
		"",
		"## Keep reading",
		"",
		"[View all](https://openai.com/news/)",
		"",
		"![cover](assets/img_2.png)",
		"",
		"[Introducing GeneBench-Pro](https://openai.com/index/introducing-genebench-pro/)",
	}, "\n")

	got := cleanOpenAINoise(input)

	// Removed noise.
	for _, gone := range []string{"Listen to article", "## Keep reading", "GeneBench-Pro", "View all"} {
		if strings.Contains(got, gone) {
			t.Errorf("expected %q to be stripped, got:\n%s", gone, got)
		}
	}
	// The adjacent share button is gone, but real body content stays.
	if strings.Contains(got, "\nShare\n") || strings.HasSuffix(got, "\nShare") {
		t.Errorf("adjacent Share button not stripped, got:\n%s", got)
	}
	for _, keep := range []string{
		"# Separating signal from noise in coding evaluations",
		"Through a detailed audit",
		"Accurately measuring our models' capabilities",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("expected %q to be preserved, got:\n%s", keep, got)
		}
	}
}

// TestCleanOpenAINoiseKeepsUnrelatedShare ensures a bare "Share" line that is
// NOT preceded by a "Listen to article" line is left untouched (guards against
// stripping legitimate body content).
func TestCleanOpenAINoiseKeepsUnrelatedShare(t *testing.T) {
	input := "Some paragraph.\n\nShare\n\nMore text."
	if got := cleanOpenAINoise(input); !strings.Contains(got, "Share") {
		t.Errorf("unrelated Share line should be preserved, got:\n%s", got)
	}
}

func TestIsOpenAIURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://openai.com/index/separating-signal-from-noise-coding-evaluations/", true},
		{"https://www.openai.com/news/", true},
		{"https://medium.com/some-article", false},
		{"https://blog.golang.org/x", false},
		{"not a url", false},
	}
	for _, tt := range tests {
		if got := isOpenAIURL(tt.url); got != tt.want {
			t.Errorf("isOpenAIURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// TestExtractContentMediumNetflixSnapshot mirrors the real extension snapshot
// reported in issue #48 update: Netflix TechBlog (custom-domain Medium
// publication) where the snapshot has NO `data-selectable-paragraph`
// attributes — so the original Medium-only path never ran.
//
// Verifies that:
//   - byline wrapper (avatar link + author link + read time + date + actions
//     + contributors line) is stripped via doc-level cleanMediumNoise
//   - "Press enter or click to view image in full size" overlay is removed
//     for all wrapping tags (button / div / span)
//   - hero image wrapped as <a><div><img/></div></a> renders as
//     "[![alt](src)](url)" without splitting across blank lines
//   - real article body content (h2 headings, paragraphs after byline) is
//     preserved
func TestExtractContentMediumNetflixSnapshot(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html><head></head>
<body>
	<article>
		<p><strong>Democratizing Machine Learning at Netflix: Building the Model Lifecycle Graph</strong></p>
		<div class="byline">
			<div>
				<a href="https://netflixtechblog.medium.com/?source=post_page---byline--5cc6d5828bb1---------------------------------------"><div><img alt="Netflix Technology Blog" src="https://miro.medium.com/avatar.jpeg"/></div></a>
				<a href="https://netflixtechblog.medium.com/?source=post_page---byline--5cc6d5828bb1---------------------------------------">Netflix Technology Blog</a>
			</div>
			<div>
				<span>14 min read</span>
				<span>·</span>
				<span>May 4, 2026</span>
			</div>
			<div>
				<span>--</span>
				<span>2</span>
				<button aria-label="Listen">Listen</button>
				<button aria-label="Share">Share</button>
				<button aria-label="More">More</button>
			</div>
			<div>
				<a href="https://linkedin.com/in/saishsali/">Saish Sali</a>,
				<a href="https://linkedin.com/in/nipunk/">Nipun Kumar</a>,
				<a href="https://linkedin.com/in/suraelamurugu/">Sura Elamurugu</a>
			</div>
		</div>
		<h2>Introduction</h2>
		<p>As Netflix has grown, machine learning continues to support our ability to deliver value to members and drive excellence across multiple areas of our business. When Netflix began investing in machine learning over a decade ago, it was primarily focused on a single domain: personalization. This paragraph is intentionally long to exceed the 200-char threshold used by the byline-pruning heuristic to identify article-body containers.</p>
		<h2>The Challenge</h2>
		<figure>
			<picture>
				<source srcset="https://miro.medium.com/v2/img2-1280.png 1280w"/>
				<img alt="" src=""/>
			</picture>
			<div>Press enter or click to view image in full size</div>
		</figure>
		<p>The next paragraph after the figure. Should appear in extracted content. Filler text to keep this paragraph above the 200-char content-paragraph threshold used by the heuristic. Filler filler filler filler filler.</p>
	</article>
</body>
</html>`

	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	preprocessLazyImages(doc)
	content := ExtractContent(doc)
	t.Logf("content:\n%s", content)

	wantPresent := []string{
		"## Introduction",
		"As Netflix has grown",
		"## The Challenge",
		"next paragraph after the figure",
		"https://miro.medium.com/v2/img2-1280.png", // lazy-loaded srcset picked up
	}
	for _, s := range wantPresent {
		if !strings.Contains(content, s) {
			t.Errorf("expected content to contain %q, not found", s)
		}
	}

	wantAbsent := []string{
		"Netflix Technology Blog",
		"14 min read",
		"May 4, 2026",
		"Listen",
		"Share",
		"Saish Sali",
		"miro.medium.com/avatar.jpeg",
		"Press enter or click to view image in full size",
		"source=post_page---byline",
	}
	for _, s := range wantAbsent {
		if strings.Contains(content, s) {
			t.Errorf("expected content NOT to contain %q, but it was present", s)
		}
	}

	if strings.Contains(content, "\n\n](") {
		t.Errorf("broken image-in-link markdown (\\n\\n before ](url)):\n%s", content)
	}
}

// TestExtractContentHackerNoonIssue87 reproduces issue #87: HackerNoon articles
// captured via the browser extension leak page noise into paper.md. The article
// body lives in `.story-body`, while Comments / Related Stories / TOPICS /
// Featured / About-Author / prev-next sections are SIBLINGS outside it, all
// under <main>. Since PR #84 ExtractContent picks the LONGEST-text selector, so
// the noisy <main> (which here is deliberately larger than the body) would win
// without the `.story-body` short-circuit. Mirrors the real page
// https://hackernoon.com/go-execution-traces-have-become-more-powerful.
func TestExtractContentHackerNoonIssue87(t *testing.T) {
	// Related Stories is intentionally bulky so <main> beats `.story-body` on
	// text length — proving the fix comes from the short-circuit, not longest-wins.
	relatedCards := ""
	for i := 0; i < 12; i++ {
		relatedCards += `<a href="/some-recommended-article">Some Recommended Article Title That Is Fairly Long To Add Bulk And Outweigh The Real Article Body Content Length</a>`
	}

	mockHTML := `<!DOCTYPE html>
<html lang="en">
<head></head>
<body>
	<main>
		<div class=" story-body font-sans flex items-center justify-center ">
			<p>The runtime/trace package contains a powerful tool for understanding and troubleshooting Go programs.</p>
			<p>Inside the runtime, execution traces are just a bunch of relatively low-cost events, with structure emerging via connections between them.</p>
			<h2>Faster execution traces</h2>
			<p>Go 1.21 restructured how the runtime coordinates tracing, dramatically lowering the cost of collecting a trace.</p>
			<p>This article is available on The Go Blog under a CC BY 4.0 DEED license.</p>
		</div>
		<div class="community-engagement">Community Engagement — 19 Likes, 5 Linkbacks</div>
		<section class="topics"><h3>TOPICS</h3><a href="/tagged/go">#go</a><a href="/tagged/golang">#golang</a></section>
		<section class="featured-in"><h3>THIS ARTICLE WAS FEATURED IN</h3><a href="/terminal">Terminal</a><a href="/lite">Lite</a></section>
		<nav class="prev-next"><a href="/prev">Previous</a><a href="/next">Up Next</a></nav>
		<section class="about-author"><h3>About Author</h3><p>Author bio here</p><button>Subscribe</button></section>
		<section class="comments"><h3>Comments</h3><p>A user comment</p></section>
		<section class="related-stories"><h3>RELATED STORIES</h3>` + relatedCards + `</section>
	</main>
</body>
</html>`

	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	content := ExtractContent(doc)
	t.Logf("Extracted HackerNoon content (%d chars):\n%s", len(content), content)

	wantPresent := []string{
		"The runtime/trace package contains a powerful tool",
		"execution traces are just a bunch of relatively low-cost events",
		"Faster execution traces",
		"This article is available on The Go Blog under a CC BY 4.0 DEED license.",
	}
	for _, s := range wantPresent {
		if !strings.Contains(content, s) {
			t.Errorf("expected content to contain %q, not found", s)
		}
	}

	wantAbsent := []string{
		"Community Engagement",
		"TOPICS",
		"THIS ARTICLE WAS FEATURED IN",
		"Previous",
		"Up Next",
		"About Author",
		"Subscribe",
		"Comments",
		"RELATED STORIES",
		"Some Recommended Article Title",
	}
	for _, s := range wantAbsent {
		if strings.Contains(content, s) {
			t.Errorf("expected content NOT to contain %q, but it was present", s)
		}
	}
}

// TestConvertImageInsideLinkWithDivWrapper covers the Medium pattern
// <a><div><img/></div></a> — the div used to emit a trailing "\n\n" which
// then made the parent <a> render as "[![alt](src)\n\n](url)".
func TestConvertImageInsideLinkWithDivWrapper(t *testing.T) {
	html := `<html><body><section><a href="https://example.com/post"><div><img alt="hero" src="https://cdn.example.com/hero.jpg"/></div></a></section></body></html>`
	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out strings.Builder
	doc.Find("section").Contents().Each(func(i int, s *goquery.Selection) {
		out.WriteString(convertNodeToMarkdown(s))
	})
	got := out.String()
	if strings.Contains(got, "\n\n](") {
		t.Errorf("image-in-link with div wrapper still broken: %q", got)
	}
	if !strings.Contains(got, "[![hero](https://cdn.example.com/hero.jpg)](https://example.com/post)") {
		t.Errorf("got %q, want the link/image combo intact", got)
	}
}

// TestStripDuplicateBodyTitle covers the post-processing step that drops a
// "**Title**" or "# Title" line at the start of body content when it matches
// the article title (already stored in YAML frontmatter).
func TestStripDuplicateBodyTitle(t *testing.T) {
	title := "Democratizing ML at Netflix"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold title duplicated at start",
			in:   "**Democratizing ML at Netflix**\n\n## Intro\n\nBody.",
			want: "## Intro\n\nBody.",
		},
		{
			name: "h1 title duplicated at start",
			in:   "# Democratizing ML at Netflix\n\nBody.",
			want: "Body.",
		},
		{
			name: "no duplicate — left alone",
			in:   "## Intro\n\nBody.",
			want: "## Intro\n\nBody.",
		},
		{
			name: "title is prefix of a longer line — not stripped",
			in:   "**Democratizing ML at Netflix Scale**\n\nBody.",
			want: "**Democratizing ML at Netflix Scale**\n\nBody.",
		},
		{
			name: "leading whitespace before title",
			in:   "\n\n**Democratizing ML at Netflix**\n\nBody.",
			want: "Body.",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDuplicateBodyTitle(tt.in, title)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
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
		{
			// Issue #47: Medium og:title contains author + date + Medium suffix
			name:     "Medium: title with author and date suffix",
			input:    "Golang 1.26 Update: Stop Writing Pointer Helpers with new(expr) | by ElAmir Mansour | May, 2026 | Medium",
			expected: "Golang 1.26 Update: Stop Writing Pointer Helpers with new(expr)",
		},
		{
			name:     "Medium: title with author and publication suffix",
			input:    "Some Article Title | by Jane Doe | Towards Data Science | Medium",
			expected: "Some Article Title",
		},
		{
			name:     "Medium: title with only Medium suffix",
			input:    "Some Article Title | Medium",
			expected: "Some Article Title",
		},
		{
			name:     "Non-Medium pipe-separated title unchanged",
			input:    "Some Article | Towards Data Science",
			expected: "Some Article | Towards Data Science",
		},
		{
			// Issue #48: Medium custom-domain publication (e.g. netflixtechblog.com)
			// ends with the publication name, not " | Medium".
			name:     "Medium: custom-domain publication suffix (no | Medium)",
			input:    "Democratizing Machine Learning at Netflix: Building the Model Lifecycle Graph | by Netflix Technology Blog | May, 2026 | Netflix TechBlog",
			expected: "Democratizing Machine Learning at Netflix: Building the Model Lifecycle Graph",
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
		name     string
		input    string
		expected string
	}{
		{"plain text gets dashed", "Hello World", "Hello-World"},
		{"slash and colon become dash", "foo/bar:baz", "foo-bar-baz"},
		{"unsafe glob chars stripped", "a?b*c|d", "abcd"},
		{"double quote stripped", `quote"here`, "quotehere"},
		{"untouched normal", "normal", "normal"},

		// Regression for issue #68: title with `, % & .` etc. was producing
		// odd slugs like `Title.-75,700-...-tokens-75%.---Towards-AI`.
		{"comma stripped", "75,700 stars", "75700-stars"},
		{"percent replaced with pct", "cut tokens 75%", "cut-tokens-75pct"},
		{"ampersand becomes and", "Rock & Roll", "Rock--and--Roll"},
		{"hash stripped", "issue #68 fix", "issue-68-fix"},
		{"apostrophe stripped", "don't panic", "dont-panic"},
		{"trailing dot trimmed", "Hello.", "Hello"},
		{"leading dot trimmed - no hidden dir", ".bashrc-style", "bashrc-style"},
		{"trailing-and-leading dots and spaces trimmed", "  .title.  ", "title"},
		{"empty input returns untitled", "", "untitled"},
		{"only-stripped chars return untitled", "?*|", "untitled"},
		{"only dots return untitled after trim", "...", "untitled"},
		{
			"full failing title from issue #68",
			`Matt Pocock dumped 17 markdown files on GitHub. 75,700 stars later, one cut my tokens 75%`,
			`Matt-Pocock-dumped-17-markdown-files-on-GitHub.-75700-stars-later-one-cut-my-tokens-75pct`,
		},
		// Regression for issue #70: long CJK+English title exceeds 255-byte filename limit
		{
			"long mixed CJK+English title truncated to 200 bytes",
			"Matt-Pocock-在-GitHub-上发布了-17-个-Markdown-文件。获得-75700-个星标后，有人删减了我的-75pct-的令牌。-作者：Chew-Loong-Nian---人工智能工程师--2026-年-5-月--迈向人工智能-----Matt-Pocock-Dumped-17-Markdown-Files-on-GitHub.-75700-Stars-Later-One-Cut-My-Tokens-75pct",
			"Matt-Pocock-在-GitHub-上发布了-17-个-Markdown-文件。获得-75700-个星标后，有人删减了我的-75pct-的令牌。-作者：Chew-Loong-Nian---人工智能工程师--2026-年-5-月--迈",
		},
		{
			"very long ASCII title truncated",
			"A-Very-Long-Title-That-Exceeds-Two-Hundred-Bytes-And-Should-Be-Truncated-To-Prevent-File-Name-Too-Long-Errors-On-The-Linux-File-System-When-Saving-Web-Clipped-Documents-With-CJK-Mixed-Content-And-Multiple-Segments-Combined-Together-Into-One-Single-Long-Slug-Name",
			"A-Very-Long-Title-That-Exceeds-Two-Hundred-Bytes-And-Should-Be-Truncated-To-Prevent-File-Name-Too-Long-Errors-On-The-Linux-File-System-When-Saving-Web-Clipped-Documents-With-CJK-Mixed-Content-And-Mult",
		},
	}
	for _, tt := range tests {
		name := tt.name
		if name == "" {
			name = tt.input
		}
		t.Run(name, func(t *testing.T) {
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

	doc, err := ParseHTML(mockHTML)
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

	doc, err := ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse mock HTML: %v", err)
	}

	// Preprocess images
	preprocessWeChatImages(doc)

	// Extract content
	content := ExtractContent(doc)
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

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Preprocess images
	preprocessWeChatImages(doc)

	// Extract content
	content := ExtractContent(doc)
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

func TestNeedsBrowser(t *testing.T) {
	tests := []struct {
		url    string
		expect bool
		method string
	}{
		{"https://mp.weixin.qq.com/s/some-article", true, "wechat"},
		{"https://mp.weixin.qq.com/s?__biz=MzI2&mid=123", true, "wechat"},
		{"https://www.bestblogs.dev/article/abc123", false, ""},
		{"https://go.dev/blog/some-article", false, ""},
		{"https://x.com/user/status/123", false, ""},
		{"https://example.com", false, ""},
		{"not-a-url", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			cfg, ok := needsBrowser(tt.url)
			if ok != tt.expect {
				t.Errorf("needsBrowser(%q) = %v, want %v", tt.url, ok, tt.expect)
			}
			if ok && cfg.FetchMethod != tt.method {
				t.Errorf("needsBrowser(%q).FetchMethod = %q, want %q", tt.url, cfg.FetchMethod, tt.method)
			}
		})
	}
}

func TestClipWebParseAndExtract(t *testing.T) {
	html := `<html><head><title>Test Article</title>
	<meta property="article:published_time" content="2026-05-01"/>
	</head><body>
	<nav>Navigation</nav>
	<article><h1>Test Article</h1><p>Hello world.</p>
	<img src="https://example.com/img.png"/></article>
	<footer>Footer</footer></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatalf("ParseHTML failed: %v", err)
	}

	title := extractTitle(doc)
	if title != "Test Article" {
		t.Errorf("Expected title 'Test Article', got %q", title)
	}

	content := ExtractContent(doc)
	if !strings.Contains(content, "Hello world") {
		t.Errorf("Expected content to contain 'Hello world', got %q", content)
	}
	if strings.Contains(content, "Navigation") {
		t.Errorf("Expected nav to be stripped from content")
	}

	pubTime := extractPublishedTime(doc)
	if pubTime.IsZero() {
		t.Error("Expected published time to be extracted")
	}
}

func TestClipWebValidation(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		html  string
		valid bool
	}{
		{"empty html", "https://example.com", "", false},
		{"empty url", "", "<html></html>", false},
		{"valid", "https://example.com", "<html><body>Hello</body></html>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := WebClipRequest{URL: tt.url, HTML: tt.html}
			hasURL := req.URL != ""
			hasHTML := req.HTML != ""
			if tt.valid && (!hasURL || !hasHTML) {
				t.Errorf("Expected valid request to have both URL and HTML")
			}
			if !tt.valid && hasURL && hasHTML {
				t.Errorf("Expected invalid request to be missing URL or HTML")
			}
		})
	}
}

func TestClipWebWeChatPostprocess(t *testing.T) {
	html := `<html><body><div id="js_content">
	<img data-src="https://mmbiz.qpic.cn/test.png" src=""/>
	<p>WeChat article content</p>
	</div></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	src, _ := doc.Find("img").Attr("src")
	if src != "" {
		t.Errorf("Before postprocess, src should be empty, got %q", src)
	}

	preprocessWeChatImages(doc)

	src, _ = doc.Find("img").Attr("src")
	if src != "https://mmbiz.qpic.cn/test.png" {
		t.Errorf("After postprocess, src should be data-src value, got %q", src)
	}
}

func TestExtractContentRemovesWeChatLineNumbers(t *testing.T) {
	html := `<html><body><div id="js_content">
	<p>Some code explanation</p>
	<section class="code-snippet__line-index">
		<li></li><li></li><li></li><li></li><li></li>
	</section>
	<pre><code>func main() {
    fmt.Println("hello")
}</code></pre>
	</div></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	content := ExtractContent(doc)

	if strings.Contains(content, "code-snippet") {
		t.Error("Expected code line number elements to be removed")
	}
	if !strings.Contains(content, "Some code explanation") {
		t.Error("Expected article content to be preserved")
	}
	if !strings.Contains(content, "func main()") {
		t.Error("Expected code block content to be preserved")
	}
}

func TestExtractContentFromContentAreaOnly(t *testing.T) {
	html := `<html><body>
	<nav>Navigation bar</nav>
	<div id="js_content">
		<p>Article content here</p>
	</div>
	<footer>Footer stuff</footer>
	</body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	content := ExtractContent(doc)

	if !strings.Contains(content, "Article content here") {
		t.Error("Expected content area text to be present")
	}
	if strings.Contains(content, "Navigation bar") {
		t.Error("Expected nav to be stripped")
	}
	if strings.Contains(content, "Footer stuff") {
		t.Error("Expected footer to be stripped")
	}
}

func TestExtractContentWithExtensionHTML(t *testing.T) {
	// Simulates content.js sending only the content area HTML (not full DOM)
	html := `<div id="js_content">
		<h2>Article Title</h2>
		<p>Paragraph one.</p>
		<p>Paragraph two.</p>
	</div>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	content := ExtractContent(doc)

	if !strings.Contains(content, "Article Title") {
		t.Error("Expected title in content")
	}
	if !strings.Contains(content, "Paragraph one") {
		t.Error("Expected paragraph content")
	}
}

func TestWeChatDecorativeSectionsNotProduceEmptyListItems(t *testing.T) {
	// WeChat uses decorative sections for chapter separators (gold lines + dots)
	html := `<html><body><div id="js_content">
	<section>
		<section style="width: 25px; height: 2px; background: rgb(255, 200, 88);"><span><br/></span></section>
		<section style="width: 4px; height: 4px; background: rgb(255, 200, 88);"><span><br/></span></section>
		<section style="width: 25px; height: 2px; background: rgb(255, 200, 88);"><span><br/></span></section>
	</section>
	<p>Real content after separator</p>
	</div></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	content := ExtractContent(doc)

	if strings.Contains(content, "\n-\n") || strings.Contains(content, "\n- \n") {
		t.Errorf("Expected no empty list items, got:\n%s", content)
	}
	if !strings.Contains(content, "Real content after separator") {
		t.Error("Expected real content to be preserved")
	}
}

func TestMergeTableRows(t *testing.T) {
	input := "| 路径 | 耗时 | 场景 |\n\n|------|-------|-------|\n\n| Layer 1 | ~0.001ms | 热路径 |\n\n| Layer 2 | ~1ms | 冷启动 |"
	expected := "| 路径 | 耗时 | 场景 |\n|------|-------|-------|\n| Layer 1 | ~0.001ms | 热路径 |\n| Layer 2 | ~1ms | 冷启动 |"

	result := mergeTableRows(input)
	if result != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, result)
	}
}

func TestMergeTableRowsPreservesNonTableBlanks(t *testing.T) {
	input := "Some text\n\nAnother paragraph\n\n| H1 | H2 |\n|---|---|\n| A | B |\n\nMore text"
	result := mergeTableRows(input)

	if !strings.Contains(result, "text\n\nAnother") {
		t.Error("Expected blank line between non-table paragraphs to be preserved")
	}
	if !strings.Contains(result, "| B |\n\nMore") {
		t.Error("Expected blank line after table to be preserved")
	}
}

func TestExtractContentMergesTableRows(t *testing.T) {
	html := `<html><body><article>
	<p>性能对比：</p>
	<p>| 路径 | 耗时 | 场景 |</p>
	<p>|------|-------|-------|</p>
	<p>| Layer 1 | ~0.001ms | 热路径 |</p>
	<p>| Layer 2 | ~1ms | 冷启动 |</p>
	</article></body></html>`

	doc, err := ParseHTML(html)
	if err != nil {
		t.Fatal(err)
	}

	content := ExtractContent(doc)

	// Table rows should be contiguous (no blank line between them)
	if strings.Contains(content, "|\n\n|") {
		t.Errorf("Expected table rows to be merged without blank lines, got:\n%s", content)
	}
	if !strings.Contains(content, "| 路径 | 耗时 | 场景 |\n|") {
		t.Errorf("Expected header and separator to be adjacent, got:\n%s", content)
	}
}
