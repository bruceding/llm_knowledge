package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRenderedHTML_StaticPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div id="content">Hello from test server</div></body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	html, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#content",
		Timeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("FetchRenderedHTML failed: %v", err)
	}
	if !strings.Contains(html, "Hello from test server") {
		t.Errorf("Expected HTML to contain test content, got: %s", html[:min(200, len(html))])
	}
}

func TestFetchRenderedHTML_JSRendered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<div id="app"></div>
			<script>
				document.getElementById('app').innerHTML = '<p id="dynamic">JS rendered content</p>';
			</script>
		</body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	html, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#dynamic",
		Timeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("FetchRenderedHTML failed: %v", err)
	}
	if !strings.Contains(html, "JS rendered content") {
		t.Errorf("Expected JS-rendered content in HTML, got: %s", html[:min(200, len(html))])
	}
}

func TestFetchRenderedHTML_TimeoutOnMissingSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div>No matching selector here</div></body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	_, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#nonexistent",
		Timeout:      3 * time.Second,
	})
	if err == nil {
		t.Fatal("Expected error for missing selector, got nil")
	}
}

func TestFetchRenderedHTML_ScrollToLoad(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Simulate WeChat-style lazy images: data-src with empty/placeholder src
		w.Write([]byte(`<html><body style="height:3000px">
			<div id="content">
				<img data-src="https://example.com/img1.png" src="" />
				<div style="height:2000px"></div>
				<img data-src="https://example.com/img2.png" src="data:image/gif;base64,R0lGODlhAQ==" />
			</div>
		</body></html>`))
	}))
	defer server.Close()

	pool := NewPool(2)
	defer pool.Close()

	html, err := pool.FetchRenderedHTML(server.URL, RenderOpts{
		WaitSelector: "#content",
		ScrollToLoad: true,
		Timeout:      15 * time.Second,
	})
	if err != nil {
		t.Fatalf("FetchRenderedHTML failed: %v", err)
	}

	// ScrollToLoad forces data-src → src for images with empty/placeholder src
	if !strings.Contains(html, `src="https://example.com/img1.png"`) {
		t.Error("Expected img1 src to be set from data-src")
	}
	if !strings.Contains(html, `src="https://example.com/img2.png"`) {
		t.Error("Expected img2 src to be set from data-src (replacing base64 placeholder)")
	}
}

func TestPool_Close(t *testing.T) {
	pool := NewPool(2)
	pool.Close()
}
