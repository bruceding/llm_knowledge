package blog

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
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
