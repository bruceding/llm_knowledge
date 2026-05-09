package api

import (
	"os"
	"strings"
	"testing"
)

func TestFetchFullArticleContent(t *testing.T) {
	// Create temp assets dir
	tmpDir, err := os.MkdirTemp("", "rss-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	assetsDir := tmpDir + "/assets"
	os.MkdirAll(assetsDir, 0755)

	// Test fetching a real article
	content, imgs, errs, err := fetchFullArticleContent(
		"https://internals-for-interns.com/posts/zfs-filesystem/",
		assetsDir,
	)

	if err != nil {
		t.Logf("Fetch error: %v", err)
		// Don't fail on network errors, just log
		return
	}

	// Check content is substantial (not just excerpt)
	if len(content) < 500 {
		t.Errorf("Expected substantial content (>500 chars), got %d chars: %s", len(content), content[:200])
	}

	// Should contain key sections from the article
	if !strings.Contains(content, "block pointer") {
		t.Errorf("Expected content about 'block pointer', got: %s", content[:300])
	}

	t.Logf("Success: got %d chars, %d images downloaded, %d image errors", len(content), imgs, errs)
}

func TestRSSHandlerExists(t *testing.T) {
	h := RSSHandler{DataDir: "/tmp"}
	if h.DataDir != "/tmp" {
		t.Error("Expected DataDir")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct{ input, want string }{
		{"simple", "simple"},
		{"hello world", "hello-world"},
		{"a/b\\c:d", "a-b-c-d"},
		{"it's", "its"},
		{"  trim  ", "trim"},
		{strings.Repeat("a", 150), strings.Repeat("a", 100)},
		{"file<name>test", "file-name-test"},
		{"pipe|star*quest?", "pipe-star-quest-"},
		{"\"quoted\"", "-quoted-"},
	}
	for _, tt := range tests {
		if got := sanitizeFilename(tt.input); got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProcessHTMLToMarkdown_PreTagIndentation(t *testing.T) {
	// Test that syntax-highlighted HTML with indentation is cleaned up.
	// Note: no Go-source indentation inside the raw string — whitespace in <pre> is meaningful.
	html := `<div class="codehilite"><pre><span></span><code><span class="p">...</span>
<span class="k">union</span><span class="w"> </span><span class="k">all</span>
<span class="k">select</span>
<span class="w">  </span><span class="n">id</span><span class="p">,</span>
<span class="w">  </span><span class="s1">&#39;beat&#39;</span><span class="w"> </span><span class="k">as</span><span class="w"> </span><span class="k">type</span>
</code></pre></div>`

	result, _, _ := processHTMLToMarkdown(html, "/tmp", "http://example.com")

	// Check that it's properly formatted as a code block
	if !strings.Contains(result, "```") {
		t.Error("Expected code block syntax")
	}

	// Top-level lines (union, select) should have no leading indentation
	if strings.Contains(result, "    union") {
		t.Errorf("Top-level code lines should not have excessive indentation, got: %s", result)
	}

	// Relative indentation (e.g. 2 spaces before "id,") should be preserved
	if !strings.Contains(result, "  id,") {
		t.Errorf("Relative indentation should be preserved, got: %s", result)
	}
}

func TestProcessHTMLToMarkdown_BasicElements(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		want     string
		notWant  string
	}{
		{
			name: "paragraph",
			html: "<p>Hello World</p>",
			want: "Hello World",
		},
		{
			name: "heading",
			html: "<h2>Title</h2>",
			want: "## Title",
		},
		{
			name: "link",
			html: `<a href="https://example.com">Link</a>`,
			want: "[Link](https://example.com)",
		},
		{
			name: "blockquote",
			html: "<blockquote><p>Quote text</p></blockquote>",
			want: "> Quote text",
		},
		{
			name: "inline code",
			html: "<code>inline code</code>",
			want: "`inline code`",
		},
		{
			name: "bold",
			html: "<strong>bold text</strong>",
			want: "**bold text**",
		},
		{
			name: "italic",
			html: "<em>italic text</em>",
			want: "*italic text*",
		},
		{
			name: "inline code with surrounding text",
			html: "<p>Use the <code>fmt</code> package.</p>",
			want: "Use the `fmt` package.",
			notWant: "Use the`fmt`",
		},
		{
			name: "italic with surrounding text",
			html: "<p>This is <em>important</em> news.</p>",
			want: "This is *important* news.",
			notWant: "This is*important*",
		},
		{
			name: "bold with surrounding text",
			html: "<p>This is <strong>critical</strong> information.</p>",
			want: "This is **critical** information.",
			notWant: "This is**critical**",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, _ := processHTMLToMarkdown(tt.html, "/tmp", "http://example.com")
			if !strings.Contains(result, tt.want) {
				t.Errorf("Expected '%s' in result, got: %s", tt.want, result)
			}
			if tt.notWant != "" && strings.Contains(result, tt.notWant) {
				t.Errorf("Did not expect '%s' in result, got: %s", tt.notWant, result)
			}
		})
	}
}