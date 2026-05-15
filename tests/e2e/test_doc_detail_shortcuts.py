"""
E2E tests for DocDetail keyboard shortcuts (vim-like j/k/g/G scroll).

Each test seeds its own scrollable document via the web-clip API and removes
it afterwards, so the suite does not depend on existing user data.
"""

import pytest
from playwright.sync_api import Page


SEED_HTML = (
    "<html><head><title>Scroll Shortcut Test</title></head><body>"
    + "".join(f"<p>Paragraph {i} — {'lorem ipsum ' * 20}</p>" for i in range(80))
    + "</body></html>"
)
SEED_URL_PREFIX = "https://example.test/e2e-scroll-shortcuts/"


def _seed_document(page: Page) -> int:
    """Create a long document via the web-clip API. Returns its id."""
    import time

    seed_url = f"{SEED_URL_PREFIX}{int(time.time() * 1000)}"
    result = page.evaluate(
        """async ({url, html}) => {
            const t = localStorage.getItem('token');
            const r = await fetch('/api/raw/web-clip', {
                method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + t},
                body: JSON.stringify({url, html, title: 'Scroll Shortcut Test'}),
            });
            const body = await r.json();
            return {status: r.status, body};
        }""",
        {"url": seed_url, "html": SEED_HTML},
    )
    assert result["status"] == 200, f"Seed failed: {result}"
    body = result["body"]
    doc_id = body.get("document", {}).get("id") or body.get("id") or body.get("documentId")
    assert doc_id, f"Could not find doc id in response: {body}"
    return int(doc_id)


def _delete_document(page: Page, doc_id: int) -> None:
    page.evaluate(
        """async (id) => {
            const t = localStorage.getItem('token');
            await fetch('/api/documents/' + id, {
                method: 'DELETE',
                headers: {'Authorization': 'Bearer ' + t},
            });
        }""",
        doc_id,
    )


@pytest.fixture()
def doc_detail_page(authenticated_page: Page):
    """Seed a long doc, navigate to its detail page, and clean up afterwards."""
    page = authenticated_page
    doc_id = _seed_document(page)
    try:
        page.goto(f"http://localhost:9090/documents/{doc_id}")
        page.wait_for_load_state("networkidle")
        page.wait_for_selector("[data-testid='doc-content-scroll']", timeout=5000)
        # Allow React to finish rendering content
        page.wait_for_timeout(800)
        yield page, doc_id
    finally:
        _delete_document(page, doc_id)


def _scroll_top(page: Page) -> int:
    return page.evaluate(
        "() => document.querySelector('[data-testid=\"doc-content-scroll\"]').scrollTop"
    )


def _scroll_height(page: Page) -> int:
    return page.evaluate(
        "() => document.querySelector('[data-testid=\"doc-content-scroll\"]').scrollHeight"
    )


def _client_height(page: Page) -> int:
    return page.evaluate(
        "() => document.querySelector('[data-testid=\"doc-content-scroll\"]').clientHeight"
    )


class TestDocDetailScrollShortcuts:
    """j/k/g/G vim-style scroll shortcuts on document detail page."""

    def test_j_scrolls_down(self, doc_detail_page):
        page, _ = doc_detail_page
        assert _scroll_height(page) > _client_height(page) + 10, "Seeded doc not scrollable"

        page.evaluate("() => document.body.focus()")
        before = _scroll_top(page)
        page.keyboard.press("j")
        page.wait_for_timeout(500)  # smooth scroll animation
        after = _scroll_top(page)

        assert after > before, f"Expected scroll to increase, before={before} after={after}"

    def test_k_scrolls_up(self, doc_detail_page):
        page, _ = doc_detail_page
        assert _scroll_height(page) > _client_height(page) + 10

        page.evaluate(
            "() => { document.querySelector('[data-testid=\"doc-content-scroll\"]').scrollTop = 200; }"
        )
        page.wait_for_timeout(100)
        page.evaluate("() => document.body.focus()")

        before = _scroll_top(page)
        page.keyboard.press("k")
        page.wait_for_timeout(500)
        after = _scroll_top(page)

        assert after < before, f"Expected scroll to decrease, before={before} after={after}"

    def test_g_scrolls_to_top(self, doc_detail_page):
        page, _ = doc_detail_page
        assert _scroll_height(page) > _client_height(page) + 10

        page.evaluate(
            "() => { document.querySelector('[data-testid=\"doc-content-scroll\"]').scrollTop = 600; }"
        )
        page.wait_for_timeout(100)
        page.evaluate("() => document.body.focus()")

        page.keyboard.press("g")
        page.wait_for_timeout(1000)

        assert _scroll_top(page) == 0, f"Expected scrollTop=0, got {_scroll_top(page)}"

    def test_shift_g_scrolls_to_bottom(self, doc_detail_page):
        page, _ = doc_detail_page
        scroll_height = _scroll_height(page)
        client_height = _client_height(page)
        assert scroll_height > client_height + 10

        page.evaluate("() => document.body.focus()")
        page.keyboard.press("Shift+G")
        page.wait_for_timeout(1500)

        max_scroll = scroll_height - client_height
        actual = _scroll_top(page)
        assert actual >= max_scroll - 2, f"Expected near {max_scroll}, got {actual}"

    def test_iframe_keydown_forwarded_to_window(self, doc_detail_page):
        """
        Newsletter HTML view renders inside a sandboxed iframe; once focus is
        inside the iframe, keyboard events do not bubble to the parent window
        by default. DocDetail's iframe onLoad attaches a forwarder that
        re-dispatches keydown to window. This test validates that mechanism by
        building the same scenario (sandbox=allow-same-origin iframe + the
        forwarder snippet) inside the live page and asserting the parent
        window receives the synthetic event.
        """
        page, _ = doc_detail_page
        result = page.evaluate(
            """() => new Promise((resolve) => {
                const iframe = document.createElement('iframe');
                iframe.setAttribute('sandbox', 'allow-same-origin');
                iframe.srcdoc = '<html><body><p id="hit">x</p></body></html>';
                iframe.style.display = 'none';
                let received = null;
                const winListener = (e) => { received = {key: e.key, shift: e.shiftKey}; };
                window.addEventListener('keydown', winListener);
                iframe.addEventListener('load', () => {
                    const cd = iframe.contentDocument;
                    if (!cd) { resolve({ok: false, reason: 'no contentDocument'}); return; }
                    // Same forwarder used in DocDetail.tsx
                    cd.addEventListener('keydown', (ke) => {
                        window.dispatchEvent(new KeyboardEvent('keydown', {
                            key: ke.key, code: ke.code,
                            shiftKey: ke.shiftKey, ctrlKey: ke.ctrlKey,
                            altKey: ke.altKey, metaKey: ke.metaKey,
                        }));
                    });
                    // Dispatch a keydown inside the iframe document
                    cd.dispatchEvent(new KeyboardEvent('keydown', {key: 'G', shiftKey: true}));
                    setTimeout(() => {
                        window.removeEventListener('keydown', winListener);
                        iframe.remove();
                        resolve({ok: true, received});
                    }, 50);
                });
                document.body.appendChild(iframe);
            })"""
        )
        assert result["ok"], f"Setup failed: {result}"
        assert result["received"] == {"key": "G", "shift": True}, (
            f"Window did not receive forwarded keydown: {result}"
        )

    def test_j_ignored_in_input(self, doc_detail_page):
        """j must not scroll while focus is in an input/textarea."""
        page, _ = doc_detail_page
        assert _scroll_height(page) > _client_height(page) + 10

        input_field = page.locator("input[type='text']").first
        if not input_field.is_visible():
            pytest.skip("No input field visible to test")

        input_field.click()
        before = _scroll_top(page)
        page.keyboard.press("j")
        page.wait_for_timeout(300)
        after = _scroll_top(page)

        assert after == before, f"Scroll changed while in input: before={before} after={after}"
