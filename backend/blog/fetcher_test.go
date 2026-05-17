package blog

import "testing"

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
