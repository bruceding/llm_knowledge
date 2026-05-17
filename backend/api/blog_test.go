package api

import "testing"

func TestExtractURLSlug(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"simple slug", "https://example.com/blog/my-post", "my-post"},
		{"trailing slash", "https://example.com/blog/my-post/", "my-post"},
		{"html suffix", "https://example.com/blog/my-post.html", "my-post"},
		{"php suffix", "https://example.com/blog/my-post.php", "my-post"},
		{"aspx suffix", "https://example.com/blog/my-post.aspx", "my-post"},
		{"deep nested", "https://example.com/2025/05/17/article", "article"},
		{"query string ignored", "https://example.com/blog/foo?utm_source=x", "foo"},
		{"fragment ignored", "https://example.com/blog/foo#section", "foo"},
		{"empty path", "https://example.com", ""},
		{"root only", "https://example.com/", ""},
		{"percent-decoded path with spaces", "https://example.com/blog/hello%20world", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractURLSlug(tt.url)
			if got != tt.want {
				t.Errorf("extractURLSlug(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
