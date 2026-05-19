"""
E2E for DocDetail on mobile: single column + FAB + ChatBottomSheet.
"""

import pytest
from playwright.sync_api import Page, expect


@pytest.fixture(scope="function")
def first_doc_id(mobile_page: Page) -> int:
    """Pick any document id from DocumentsList, fall back to skip."""
    page = mobile_page
    page.goto("http://localhost:9090/documents")
    page.wait_for_load_state("domcontentloaded")
    first = page.locator('a[href^="/documents/"]').first
    if first.count() == 0:
        pytest.skip("No documents available")
    href = first.get_attribute("href")
    assert href is not None
    return int(href.rsplit("/", 1)[-1])


class TestMobileDocDetail:
    def test_bottom_tab_bar_hidden_on_doc_detail(self, mobile_page: Page, first_doc_id: int):
        page = mobile_page
        page.goto(f"http://localhost:9090/documents/{first_doc_id}")
        page.wait_for_load_state("domcontentloaded")
        nav = page.get_by_role("navigation", name="bottom navigation")
        expect(nav).to_have_count(0)

    def test_fab_visible(self, mobile_page: Page, first_doc_id: int):
        page = mobile_page
        page.goto(f"http://localhost:9090/documents/{first_doc_id}")
        page.wait_for_load_state("domcontentloaded")
        fab = page.get_by_label("open chat")
        expect(fab).to_be_visible()

    def test_fab_opens_chat_sheet(self, mobile_page: Page, first_doc_id: int):
        page = mobile_page
        page.goto(f"http://localhost:9090/documents/{first_doc_id}")
        page.wait_for_load_state("domcontentloaded")
        page.get_by_label("open chat").click()
        expect(page.get_by_role("dialog", name="document chat")).to_be_visible()

    def test_translate_button_visible(self, mobile_page: Page, first_doc_id: int):
        page = mobile_page
        page.goto(f"http://localhost:9090/documents/{first_doc_id}")
        page.wait_for_load_state("domcontentloaded")
        expect(page.get_by_label("translate")).to_be_visible()
