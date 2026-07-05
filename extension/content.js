if (!window._wikiClipperLoaded) {
window._wikiClipperLoaded = true;

// Selectors for non-content elements to remove before clipping
var REMOVE_SELECTORS = [
  'script', 'style', 'iframe', 'noscript',
  'nav', 'header', 'footer',
  '.ads', '.ad', '.advertisement', '.adsbygoogle',
  '.sidebar', '.navigation', '.menu',
  '.cookie-notice', '.cookie-banner', '.Cookie-notice',
  '[role="banner"]', '[role="navigation"]', '[role="complementary"]'
].join(', ');

// Content area selectors in priority order
var CONTENT_SELECTORS = [
  '#js_content',          // WeChat articles
  'article',
  'main',
  '[role="main"]',
  '.content',
  '.post-content',
  '#content'
];

// Extract cleaned HTML from the page
function extractContent() {
  // Pick the candidate with the most text, mirroring the backend's
  // "longest text wins" strategy (web.go ExtractContent). A naive
  // first-match grabs the wrong element on SPA sites where a sponsored /
  // related card is also an <article> ahead of the real body (issue #84:
  // HackerNoon returned the "SPONSORED" ad card instead of the article).
  var mainEl = null;
  var bestLen = 0;
  for (var i = 0; i < CONTENT_SELECTORS.length; i++) {
    var matches = document.querySelectorAll(CONTENT_SELECTORS[i]);
    for (var j = 0; j < matches.length; j++) {
      var len = (matches[j].innerText || '').trim().length;
      if (len > bestLen) {
        bestLen = len;
        mainEl = matches[j];
      }
    }
  }
  if (!mainEl) mainEl = document.body;

  var clone = mainEl.cloneNode(true);

  clone.querySelectorAll(REMOVE_SELECTORS).forEach(function(el) {
    el.remove();
  });

  return {
    url: location.href,
    title: document.title,
    html: clone.outerHTML
  };
}

// Show a toast notification at the bottom of the page
function showToast(success, message) {
  // Remove any existing toast
  var existing = document.getElementById('wiki-clipper-toast');
  if (existing) {
    existing.remove();
  }

  var toast = document.createElement('div');
  toast.id = 'wiki-clipper-toast';
  toast.setAttribute('style', [
    'all: initial',
    'position: fixed',
    'bottom: 24px',
    'left: 50%',
    'transform: translateX(-50%)',
    'background: #1a1a1a',
    'color: #ffffff',
    'font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
    'font-size: 14px',
    'padding: 12px 24px',
    'border-radius: 8px',
    'z-index: 2147483647',
    'display: flex',
    'align-items: center',
    'gap: 8px',
    'box-shadow: 0 4px 12px rgba(0,0,0,0.3)',
    'opacity: 1',
    'transition: opacity 0.3s ease'
  ].join('; '));

  var icon = success ? '✅' : '❌';
  toast.textContent = icon + ' ' + message;

  document.body.appendChild(toast);

  // Fade out and remove after 2.5 seconds
  setTimeout(function() {
    toast.style.opacity = '0';
    setTimeout(function() {
      toast.remove();
    }, 300);
  }, 2500);
}

// Listen for messages from background script
chrome.runtime.onMessage.addListener(function(msg, sender, sendResponse) {
  if (msg.action === 'extractContent') {
    sendResponse(extractContent());
  } else if (msg.action === 'showToast') {
    showToast(msg.success, msg.message);
  }
  return true;
});

} // end re-injection guard
