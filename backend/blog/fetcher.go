package blog

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"llm-knowledge/browser"
	"llm-knowledge/ssrf"
)

// IsSPAShell returns true when the HTML looks like an unhydrated SPA shell:
// the user-provided selector matches zero elements AND the page contains a
// known SPA root marker (#__next, #root, #app). The root-marker check alone
// is not sufficient — every SSR Next.js / Vue / React page ships those roots
// even when fully rendered — so a non-empty selector is required to make the
// "no content visible" judgement reliable.
//
// When selector is empty (e.g. AddFeed before platform detection runs), the
// function returns false so the http path is preferred. True empty SPAs will
// fail platform auto-detection and surface needConfig to the user; once a
// selector is configured the browser fallback kicks in on the next sync.
func IsSPAShell(html, selector string) bool {
	if selector == "" {
		return false
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return false
	}

	// If selector matches anything, content is already in DOM — not a shell.
	if doc.Find(selector).Length() > 0 {
		return false
	}

	shellSelectors := []string{"#__next", "#root", "#app"}
	for _, sel := range shellSelectors {
		if doc.Find(sel).Length() > 0 {
			return true
		}
	}
	return false
}

// Fetcher fetches blog index/post HTML, falling back to browser rendering
// when the HTTP response is detected as an SPA shell.
type Fetcher struct {
	Pool        *browser.Pool
	validateURL func(string) error // defaults to ssrf.ValidateURLHost; override in tests
}

// fetchWithFallback fetches targetURL via http.Client. If the response is
// detected as an SPA shell (and a browser pool is available), it re-fetches
// using browser rendering with WaitStable 2s. The selector is the link or
// content selector used to decide whether the static HTML is sufficient.
//
// Returns (html, usedBrowser, error). usedBrowser is true when the rendered
// HTML came from the browser pool.
func (f *Fetcher) fetchWithFallback(targetURL, selector string) (string, bool, error) {
	validate := f.validateURL
	if validate == nil {
		validate = ssrf.ValidateURLHost
	}
	if err := validate(targetURL); err != nil {
		return "", false, err
	}

	httpHTML, httpErr := fetchHTTP(targetURL)
	httpOK := httpErr == nil

	if httpOK && !IsSPAShell(httpHTML, selector) {
		return httpHTML, false, nil
	}

	// Need browser. If pool unavailable, return whatever http gave us
	// (may be empty or a shell — caller will fail to extract).
	if f.Pool == nil {
		if httpOK {
			return httpHTML, false, nil
		}
		return "", false, httpErr
	}

	rendered, rerr := f.Pool.FetchRenderedHTML(targetURL, browser.RenderOpts{
		WaitStable: 2 * time.Second,
		Timeout:    30 * time.Second,
	})
	if rerr != nil {
		if httpOK {
			// Browser failed but we have http content — return it as a degraded fallback.
			log.Printf("[blog] browser render failed for %s, falling back to HTTP shell HTML: %v", targetURL, rerr)
			return httpHTML, false, nil
		}
		return "", false, fmt.Errorf("browser render: %w", rerr)
	}
	return rendered, true, nil
}

// FetchIndex fetches the index page HTML (with browser fallback for SPA).
// linkSelector is the user-configured link selector; passing it allows the
// SPA detector to recognise pages that already contain the links in static HTML.
func (f *Fetcher) FetchIndex(indexURL, linkSelector string) (string, error) {
	html, _, err := f.fetchWithFallback(indexURL, linkSelector)
	return html, err
}

// FetchArticle fetches an article and returns the inner HTML of the matched
// content node, the published time (from meta tags or <time>), and the page
// title (from <h1>). Browser fallback is triggered when the static HTML is
// an SPA shell. Falls back to common selectors (article, main, .content,
// .post-content) if contentSelector matches nothing.
func (f *Fetcher) FetchArticle(articleURL, contentSelector string) (string, time.Time, string, error) {
	html, _, err := f.fetchWithFallback(articleURL, contentSelector)
	if err != nil {
		return "", time.Time{}, "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", time.Time{}, "", err
	}

	contentNode := doc.Find(contentSelector)
	if contentNode.Length() == 0 {
		fallbacks := []string{"article", "main", ".content", ".post-content"}
		for _, sel := range fallbacks {
			if doc.Find(sel).Length() > 0 {
				contentNode = doc.Find(sel).First()
				break
			}
		}
	}

	contentHTML := ""
	if contentNode.Length() > 0 {
		// Remove style and script tags before extracting content
		contentNode.First().Find("style, script").Remove()
		inner, herr := contentNode.First().Html()
		if herr == nil {
			contentHTML = inner
		}
	}

	// Prefer the article-body <h1> over the page-global one (the global doc
	// often picks up a brand/wordmark <h1> in the site header).
	title := ""
	if contentNode.Length() > 0 {
		title = strings.TrimSpace(contentNode.First().Find("h1").First().Text())
	}
	if title == "" {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	publishedTime := extractPublishedTime(doc)

	return contentHTML, publishedTime, title, nil
}

// browserUserAgent is sent on the http path so origins fronted by CDNs (e.g.
// Cloudflare) don't reject the default Go User-Agent and force every fetch
// onto the slow browser fallback.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func fetchHTTP(targetURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUserAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
