package blog

import (
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// DetectPlatform tries to identify the blog platform from URL and HTML content.
// Returns the matching PlatformRule, or nil if no match found.
func DetectPlatform(htmlContent, pageURL string) *PlatformRule {
	// Parse URL for matching
	u, err := url.Parse(pageURL)
	if err == nil {
		host := strings.ToLower(u.Host)
		// Remove common prefixes
		host = strings.TrimPrefix(host, "www.")
		host = strings.TrimPrefix(host, "blog.")

		// First: try URL patterns
		for i, rule := range PlatformRules {
			for _, pattern := range rule.URLPatterns {
				if strings.Contains(host, pattern) {
					return &PlatformRules[i]
				}
			}
		}
	}

	// Second: try HTML patterns
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	htmlStr := htmlContent
	for i, rule := range PlatformRules {
		// Skip rules that only have URL patterns (already checked)
		if len(rule.DetectPatterns) == 0 {
			continue
		}
		for _, pattern := range rule.DetectPatterns {
			if strings.Contains(htmlStr, pattern) {
				return &PlatformRules[i]
			}
			// Also check in DOM attributes
			if doc.Find("["+pattern+"]").Length() > 0 {
				return &PlatformRules[i]
			}
		}
	}

	return nil
}
