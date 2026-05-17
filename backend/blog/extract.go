package blog

import (
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type ArticleLink struct {
	URL   string
	Title string
}

// ExtractArticleLinks extracts article links from the index page HTML
func ExtractArticleLinks(htmlContent, indexURL, linkSelector, linkExclude string) ([]ArticleLink, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(indexURL)
	if err != nil {
		return nil, err
	}

	var links []ArticleLink
	seen := make(map[string]bool)

	doc.Find(linkSelector).Each(func(i int, s *goquery.Selection) {
		// Exclude links matching linkExclude
		if linkExclude != "" && s.Find(linkExclude).Length() > 0 {
			return
		}
		if linkExclude != "" && s.Is(linkExclude) {
			return
		}

		href, exists := s.Attr("href")
		if !exists || href == "" {
			return
		}

		// Resolve relative URLs
		articleURL := href
		if !strings.HasPrefix(href, "http") {
			resolved := baseURL.ResolveReference(&url.URL{Path: href})
			articleURL = resolved.String()
		}

		// Deduplicate
		if seen[articleURL] {
			return
		}
		seen[articleURL] = true

		// Extract title from link text or nearby element
		title := strings.TrimSpace(s.Text())
		if title == "" {
			title = s.Find("h1, h2, h3, .title").Text()
			title = strings.TrimSpace(title)
		}
		if title == "" {
			title = href
		}

		links = append(links, ArticleLink{
			URL:   articleURL,
			Title: title,
		})
	})

	return links, nil
}

// extractPublishedTime extracts publication time from HTML meta tags
func extractPublishedTime(doc *goquery.Document) time.Time {
	metaNames := []string{
		"article:published_time",
		"datePublished",
		"publish-date",
		"published",
		"date",
	}

	for _, name := range metaNames {
		if val, exists := doc.Find("meta[property=\"" + name + "\"]").Attr("content"); exists && val != "" {
			if t := parseWebDate(val); !t.IsZero() {
				return t
			}
		}
		if val, exists := doc.Find("meta[name=\"" + name + "\"]").Attr("content"); exists && val != "" {
			if t := parseWebDate(val); !t.IsZero() {
				return t
			}
		}
	}

	// Try time element
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

	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05+00:00",
		"2006-01-02",
		"January 2, 2006",
		"Jan 2, 2006",
		"02 Jan 2006",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{}
}
