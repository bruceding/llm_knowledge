package blog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsSPAShell(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		selector string
		wantSPA  bool
	}{
		{
			name:     "next.js empty shell, no selector matches",
			html:     `<html><body><div id="__next"></div><script src="/_next/x.js"></script></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  true,
		},
		{
			name:     "react root empty shell",
			html:     `<html><body><div id="root"></div></body></html>`,
			selector: "article",
			wantSPA:  true,
		},
		{
			name:     "static HTML with content matches selector",
			html:     `<html><body><a href="/blog/foo">foo</a></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
		{
			name:     "next.js shell but selector matches inside",
			html:     `<html><body><div id="__next"><a href="/blog/foo">foo</a></div></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
		{
			name:     "no shell marker, no selector match (treat as non-SPA)",
			html:     `<html><body><p>nothing here</p></body></html>`,
			selector: "a[href^='/blog/']",
			wantSPA:  false,
		},
		{
			name:     "empty selector with shell marker is best-effort: returns false",
			html:     `<html><body><div id="__next"></div></body></html>`,
			selector: "",
			wantSPA:  false,
		},
		{
			name:     "empty selector on plain page returns false",
			html:     `<html><body><p>hi</p></body></html>`,
			selector: "",
			wantSPA:  false,
		},
		{
			name:     "SSR Next.js page with root marker but no selector match (probably misconfigured selector, not a shell)",
			html:     `<html><body><div id="__next"><main><article>real content</article></main></div></body></html>`,
			selector: "a.does-not-exist",
			wantSPA:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSPAShell(tt.html, tt.selector)
			if got != tt.wantSPA {
				t.Errorf("IsSPAShell() = %v, want %v", got, tt.wantSPA)
			}
		})
	}
}

func TestFetcher_FetchHTTPOnly_NonSPA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><a href="/blog/foo">foo</a></body></html>`))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil, validateURL: func(string) error { return nil }} // nil pool: must not be used for non-SPA
	html, usedBrowser, err := f.fetchWithFallback(srv.URL, "a[href^='/blog/']")
	if err != nil {
		t.Fatalf("fetchWithFallback: %v", err)
	}
	if usedBrowser {
		t.Error("expected http path, got browser")
	}
	if !contains(html, "/blog/foo") {
		t.Errorf("expected http body, got %q", html)
	}
}

func TestFetcher_NoPool_SPAShellPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="__next"></div></body></html>`))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil, validateURL: func(string) error { return nil }}
	html, usedBrowser, err := f.fetchWithFallback(srv.URL, "a[href^='/blog/']")
	if err != nil {
		t.Fatalf("fetchWithFallback: %v", err)
	}
	if usedBrowser {
		t.Error("expected http path (pool nil), got browser")
	}
	if !contains(html, "__next") {
		t.Errorf("expected shell HTML, got %q", html)
	}
}

func TestFetcher_FetchArticle_ReturnsInnerHTML(t *testing.T) {
	body := `<html><head>
<meta property="article:published_time" content="2025-01-15T10:00:00Z">
</head><body>
<h1>Hello World</h1>
<article><p>Para <strong>one</strong>.</p><p>Para two.</p></article>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil, validateURL: func(string) error { return nil }}
	html, pub, title, err := f.FetchArticle(srv.URL, "article")
	if err != nil {
		t.Fatalf("FetchArticle: %v", err)
	}
	if title != "Hello World" {
		t.Errorf("title = %q, want %q", title, "Hello World")
	}
	if !contains(html, "<strong>one</strong>") {
		t.Errorf("expected inner HTML preserved, got %q", html)
	}
	want := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	if !pub.Equal(want) {
		t.Errorf("pub = %v, want %v", pub, want)
	}
}

// TestFetcher_FetchArticle_ConcatenatesMultipleNodes mirrors claude.com's
// article layout where the body is split across many sibling
// .u-rich-text-blog blocks. The fix must concatenate all matched nodes,
// not just the first one.
func TestFetcher_FetchArticle_ConcatenatesMultipleNodes(t *testing.T) {
	const paragraphCount = 31
	const paragraphText = "This paragraph carries enough text to make the assertion meaningful when only the first node is captured."

	var b strings.Builder
	b.WriteString("<html><body><main>")
	for i := 0; i < paragraphCount; i++ {
		fmt.Fprintf(&b, `<div class="u-rich-text-blog"><p>%s block %d</p></div>`, paragraphText, i)
	}
	b.WriteString("</main></body></html>")
	body := b.String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil, validateURL: func(string) error { return nil }}
	html, _, _, err := f.FetchArticle(srv.URL, ".u-rich-text-blog")
	if err != nil {
		t.Fatalf("FetchArticle: %v", err)
	}

	// Inner HTML of one block is "<p>...</p>"; concatenated output must
	// include every block. Asserting count > 10x guards against any naive
	// .First()-only regression.
	gotCount := strings.Count(html, "block ")
	if gotCount < paragraphCount {
		t.Fatalf("expected %d blocks in concatenated output, got %d. html=%q", paragraphCount, gotCount, html)
	}

	singleInnerLen := len(fmt.Sprintf("<p>%s block 0</p>", paragraphText))
	if len(html) < singleInnerLen*10 {
		t.Fatalf("expected concatenated HTML > 10x single block (%d), got %d", singleInnerLen*10, len(html))
	}
}

// TestFetcher_FetchArticle_SecondaryFallback covers the case where the
// configured selector matches an empty wrapper. The fetcher must detect the
// thin output and switch to a fallback selector that produces more text.
func TestFetcher_FetchArticle_SecondaryFallback(t *testing.T) {
	realBody := strings.Repeat("Real article body sentence. ", 80) // > 500 chars
	body := `<html><body>
<div class="u-rich-text-blog"></div>
<main><article><p>` + realBody + `</p></article></main>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &Fetcher{Pool: nil, validateURL: func(string) error { return nil }}
	html, _, _, err := f.FetchArticle(srv.URL, ".u-rich-text-blog")
	if err != nil {
		t.Fatalf("FetchArticle: %v", err)
	}
	if !strings.Contains(html, "Real article body sentence.") {
		t.Fatalf("expected secondary fallback to capture <main> content, got %q", html)
	}
}
