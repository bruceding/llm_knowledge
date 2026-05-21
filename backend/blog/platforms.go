package blog

type PlatformRule struct {
	Name            string   // Platform name: claude, webflow, wordpress, etc.
	URLPatterns     []string // URL patterns to match first
	DetectPatterns  []string // HTML patterns to detect platform
	LinkSelector    string   // CSS selector for article links on index page
	ContentSelector string   // CSS selector for article content
	LinkExclude     string   // CSS selector to exclude (e.g. category links)
}

var PlatformRules = []PlatformRule{
	{
		Name:            "claude",
		URLPatterns:     []string{"claude.com"},
		LinkSelector:    ".card_blog_wrap a[href^='/blog/']",
		LinkExclude:     "a[aria-hidden='true']",
		// claude.com renders article body as many sibling .u-rich-text-blog
		// blocks; selecting <main> captures all of them with a single match.
		ContentSelector: "main",
	},
	{
		Name:            "webflow",
		DetectPatterns:  []string{"data-wf-domain", "data-wf-page"},
		LinkSelector:    "a[href^='/blog/']",
		ContentSelector: ".w-richtext, .rich-text",
	},
	{
		Name:            "wordpress",
		DetectPatterns:  []string{"class=\"wp-", "WordPress"},
		LinkSelector:    "article a, .post-title a, .entry-title a",
		ContentSelector: ".entry-content, .post-content, .article-content",
	},
	{
		Name:            "ghost",
		DetectPatterns:  []string{"class=\"gh-", "Ghost"},
		LinkSelector:    "a[href^='/post/']",
		ContentSelector: ".gh-content, .post-content",
	},
	{
		Name:            "medium",
		URLPatterns:     []string{"medium.com"},
		LinkSelector:    "article a[href^='/p/']",
		ContentSelector: "article section",
	},
	{
		Name:            "substack",
		URLPatterns:     []string{"substack.com"},
		LinkSelector:    "a[href^='/p/']",
		ContentSelector: ".post-content, .available-content",
	},
}
