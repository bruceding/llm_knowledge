package api

import (
	"strings"
	"testing"
)

func TestRSSHandlerExists(t *testing.T) {
	h := RSSHandler{DataDir: "/tmp"}
	if h.DataDir != "/tmp" {
		t.Error("Expected DataDir")
	}
}

func TestProcessHTMLToMarkdown_PreTagIndentation(t *testing.T) {
	// Test that syntax-highlighted HTML with indentation is cleaned up
	html := `<div class="codehilite"><pre><span></span><code><span class="p">...</span>
<span class="k">union</span><span class="w"> </span><span class="k">all</span>
<span class="k">select</span>
<span class="w">  </span><span class="n">id</span><span class="p">,</span>
<span class="w">  </span><span class="s1">&#39;beat&#39;</span><span class="w"> </span><span class="k">as</span><span class="w"> </span><span class="k">type</span>
</code></pre></div>`

	result, _, _ := processHTMLToMarkdown(html, "/tmp", "http://example.com")

	// Check that there's no excessive indentation in the code block
	if strings.Contains(result, "    union") || strings.Contains(result, "  id") {
		t.Errorf("Code block should not have leading indentation, got: %s", result)
	}

	// Check that it's properly formatted as a code block
	if !strings.Contains(result, "```") {
		t.Error("Expected code block syntax")
	}

	// The content should be clean without leading spaces
	if strings.Contains(result, "\n  ") || strings.Contains(result, "\n    ") {
		t.Errorf("Content lines should not have leading whitespace, got: %s", result)
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