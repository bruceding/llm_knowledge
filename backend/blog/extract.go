package blog

import (
	"fmt"
	"io"
	"llm-knowledge/ssrf"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type ArticleLink struct {
	URL   string
	Title string
}

// FetchIndexPage fetches the HTML content of the index page
func FetchIndexPage(indexURL string) (string, error) {
	if err := ssrf.ValidateURLHost(indexURL); err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(indexURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
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

// FetchArticleContent fetches article content and extracts it using the content selector
func FetchArticleContent(articleURL, contentSelector string) (string, time.Time, error) {
	if err := ssrf.ValidateURLHost(articleURL); err != nil {
		return "", time.Time{}, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(articleURL)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", time.Time{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", time.Time{}, err
	}

	// Extract content using selector
	var content strings.Builder
	contentNode := doc.Find(contentSelector)
	if contentNode.Length() == 0 {
		// Fallback to common selectors
		fallbacks := []string{"article", "main", ".content", ".post-content"}
		for _, sel := range fallbacks {
			if doc.Find(sel).Length() > 0 {
				contentNode = doc.Find(sel).First()
				break
			}
		}
	}

	if contentNode.Length() > 0 {
		content.WriteString(contentNode.Text())
	}

	// Extract title from h1
	title := strings.TrimSpace(doc.Find("h1").First().Text())

	// Extract published time (reuse pattern from web.go)
	publishedTime := extractPublishedTime(doc)

	return title + "\n\n" + content.String(), publishedTime, nil
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
