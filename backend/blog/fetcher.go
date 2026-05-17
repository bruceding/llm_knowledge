package blog

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"llm-knowledge/browser"
	"llm-knowledge/ssrf"
)

// IsSPAShell returns true when the HTML looks like an unhydrated SPA shell:
// it contains a known SPA root (#__next, #root, #app) and the user-provided
// selector matches zero elements. When the selector already matches, the page
// is treated as already-rendered (CSR or SSR) regardless of root markers.
func IsSPAShell(html, selector string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return false
	}

	// If selector matches anything, content is already in DOM — not a shell.
	if selector != "" && doc.Find(selector).Length() > 0 {
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
			return httpHTML, false, nil
		}
		return "", false, fmt.Errorf("browser render: %w", rerr)
	}
	return rendered, true, nil
}

func fetchHTTP(targetURL string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(targetURL)
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
