package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type RenderOpts struct {
	WaitSelector string
	WaitStable   time.Duration
	Timeout      time.Duration
	ScrollToLoad bool
}

type Pool struct {
	mu          sync.Mutex
	browser     *rod.Browser
	lastUsed    time.Time
	sem         chan struct{}
	idleTimeout time.Duration
	stopIdle    chan struct{}
	reaperDone  sync.WaitGroup
}

func NewPool(maxConcurrency int) *Pool {
	p := &Pool{
		sem:         make(chan struct{}, maxConcurrency),
		idleTimeout: 5 * time.Minute,
		stopIdle:    make(chan struct{}),
	}
	p.reaperDone.Add(1)
	go p.idleReaper()
	return p
}

func (p *Pool) ensureBrowser() (*rod.Browser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.browser != nil {
		return p.browser, nil
	}

	u, err := launcher.New().
		Set("headless", "new").
		Set("disable-blink-features", "AutomationControlled").
		Launch()
	if err != nil {
		return nil, fmt.Errorf("launch chromium: %w", err)
	}

	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("connect to chromium: %w", err)
	}

	p.browser = b
	p.lastUsed = time.Now()
	return b, nil
}

func (p *Pool) FetchRenderedHTML(url string, opts RenderOpts) (string, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return "", fmt.Errorf("timeout waiting for browser slot")
	}

	b, err := p.ensureBrowser()
	if err != nil {
		return "", err
	}

	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", fmt.Errorf("create page: %w", err)
	}
	closePage := page
	defer closePage.Close()

	page = page.Context(ctx)

	page.MustEvalOnNewDocument(`
Object.defineProperty(navigator, 'webdriver', {get: () => false});
window.chrome = {runtime: {}, loadTimes: function(){}, csi: function(){}};
Object.defineProperty(navigator, 'languages', {get: () => ['zh-CN', 'zh', 'en']});
Object.defineProperty(navigator, 'platform', {get: () => 'MacIntel'});
`)

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("navigate to %s: %w", url, err)
	}

	// WaitLoad may fail on SPAs that destroy execution context during hydration
	_ = page.WaitLoad()

	if opts.WaitSelector != "" {
		_, err := page.Element(opts.WaitSelector)
		if err != nil {
			return "", fmt.Errorf("wait for selector %q: %w", opts.WaitSelector, err)
		}
	}

	if opts.WaitStable > 0 {
		if err := page.WaitStable(opts.WaitStable); err != nil {
			return "", fmt.Errorf("wait stable: %w", err)
		}
	}

	if opts.ScrollToLoad {
		// Scroll through the page to trigger lazy-loaded images, then force-copy
		// data-src to src for any images still using lazy placeholders.
		page.Eval(`(async () => {
			const delay = ms => new Promise(r => setTimeout(r, ms));
			const step = Math.max(window.innerHeight || 600, 600);
			const maxH = document.documentElement.scrollHeight || 5000;
			for (let y = 0; y <= maxH; y += step) {
				window.scrollTo(0, y);
				await delay(200);
			}
			window.scrollTo(0, 0);
			await delay(200);
			document.querySelectorAll('img[data-src]').forEach(img => {
				if (!img.src || img.src === '' || img.src.startsWith('data:')) {
					img.src = img.dataset.src;
				}
			});
		})()`)
	}

	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("get HTML: %w", err)
	}

	p.mu.Lock()
	p.lastUsed = time.Now()
	p.mu.Unlock()

	return html, nil
}

func (p *Pool) idleReaper() {
	defer p.reaperDone.Done()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if p.mu.TryLock() {
				if p.browser != nil && time.Since(p.lastUsed) > p.idleTimeout {
					p.browser.Close()
					p.browser = nil
				}
				p.mu.Unlock()
			}
		case <-p.stopIdle:
			return
		}
	}
}

func (p *Pool) Close() {
	close(p.stopIdle)
	p.reaperDone.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.browser != nil {
		p.browser.Close()
		p.browser = nil
	}
}
