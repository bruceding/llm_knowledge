"""
E2E tests for Inbox list keyboard navigation (vim-like j/k + arrows).

Seeds three documents via the web-clip API (they land in the Inbox with
status=inbox), navigates to the Inbox at "/", and cleans up afterwards.
"""

import time

import pytest
from playwright.sync_api import Page


SEED_URL_PREFIX = "https://example.test/e2e-inbox-nav/"


def _seed_document(page: Page, title: str) -> int:
    seed_url = f"{SEED_URL_PREFIX}{title}-{int(time.time() * 1000)}"
    html = f"<html><head><title>{title}</title></head><body><p>{title} body</p></body></html>"
    result = page.evaluate(
        """async ({url, html, title}) => {
            const t = localStorage.getItem('token');
            const r = await fetch('/api/raw/web-clip', {
                method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + t},
                body: JSON.stringify({url, html, title}),
            });
            const body = await r.json();
            return {status: r.status, body};
        }""",
        {"url": seed_url, "html": html, "title": title},
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
def inbox_with_docs(authenticated_page: Page):
    """Seed 3 docs, open the Inbox, clean up afterwards. Yields (page, [ids])."""
    page = authenticated_page
    ids = [_seed_document(page, f"NavDoc{i}") for i in range(3)]
    try:
        page.goto("http://localhost:9090/")
        page.wait_for_load_state("networkidle")
        for doc_id in ids:
            page.wait_for_selector(f"[data-testid='inbox-item-{doc_id}']", timeout=5000)
        page.wait_for_timeout(300)
        yield page, ids
    finally:
        for doc_id in ids:
            _delete_document(page, doc_id)


def _active_id(page: Page):
    el = page.query_selector("[data-testid^='inbox-item-'][data-active='true']")
    if el is None:
        return None
    testid = el.get_attribute("data-testid")
    return int(testid.rsplit("-", 1)[1])


class TestInboxKeyboardNav:
    def test_j_selects_first_then_moves_down(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        assert _active_id(page) is None

        page.keyboard.press("j")
        page.wait_for_timeout(150)
        first = _active_id(page)
        assert first in ids, f"j did not select an inbox item, got {first}"

        page.keyboard.press("j")
        page.wait_for_timeout(150)
        second = _active_id(page)
        assert second in ids and second != first, f"second j did not move, first={first} second={second}"

    def test_k_moves_up(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("j")
        page.wait_for_timeout(100)
        page.keyboard.press("j")
        page.wait_for_timeout(100)
        down_id = _active_id(page)
        page.keyboard.press("k")
        page.wait_for_timeout(150)
        up_id = _active_id(page)
        assert up_id != down_id, f"k did not move selection up, down={down_id} up={up_id}"

    def test_arrow_keys_work(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("ArrowDown")
        page.wait_for_timeout(150)
        assert _active_id(page) in ids, "ArrowDown did not select an item"

    def test_enter_opens_selected_doc(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("j")
        page.wait_for_timeout(150)
        selected = _active_id(page)
        page.keyboard.press("Enter")
        page.wait_for_url(f"http://localhost:9090/documents/{selected}", timeout=5000)
        assert page.url.endswith(f"/documents/{selected}")

    def test_keys_ignored_in_input(self, inbox_with_docs):
        page, ids = inbox_with_docs
        search = page.locator("input, textarea").first
        if not search.is_visible():
            pytest.skip("No input visible to test")
        search.click()
        page.keyboard.press("j")
        page.wait_for_timeout(150)
        assert _active_id(page) is None, "j selected an item while focus was in an input"
