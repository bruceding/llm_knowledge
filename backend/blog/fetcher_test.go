package blog

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
