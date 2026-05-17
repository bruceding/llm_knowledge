package blog

import (
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	// Test claude.com URL detection
	rule := DetectPlatform("", "https://claude.com/blog")
	if rule == nil {
		t.Error("Expected claude platform to be detected from URL")
	} else if rule.Name != "claude" {
		t.Errorf("Expected 'claude', got '%s'", rule.Name)
	}

	// Test webflow HTML detection
	html := `<html data-wf-domain="test.webflow.io"><body></body></html>`
	rule = DetectPlatform(html, "https://example.com/blog")
	if rule == nil {
		t.Error("Expected webflow platform to be detected from HTML")
	} else if rule.Name != "webflow" {
		t.Errorf("Expected 'webflow', got '%s'", rule.Name)
	}

	// Test unknown platform
	rule = DetectPlatform("<html><body>unknown</body></html>", "https://unknown-site.com")
	if rule != nil {
		t.Errorf("Expected nil for unknown platform, got '%s'", rule.Name)
	}
}

func TestExtractArticleLinks(t *testing.T) {
	html := `
	<html>
	<body>
		<a href="/blog/article-1">Article 1</a>
		<a href="/blog/article-2">Article 2</a>
		<a href="/blog/category/news">Category</a>
		<a href="/blog/article-1">Duplicate</a>
	</body>
	</html>
	`

	links, err := ExtractArticleLinks(html, "https://claude.com/blog", "a[href^='/blog/']", "a[href*='/category/']")
	if err != nil {
		t.Fatalf("ExtractArticleLinks failed: %v", err)
	}

	if len(links) != 2 {
		t.Errorf("Expected 2 links (deduped, excluded category), got %d", len(links))
	}

	if links[0].URL != "https://claude.com/blog/article-1" {
		t.Errorf("Expected 'https://claude.com/blog/article-1', got '%s'", links[0].URL)
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}