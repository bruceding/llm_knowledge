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

    def test_save_note_uses_inline_edit_on_mobile(self, mobile_page: Page, first_doc_id: int):
        """On mobile, clicking Save next to AI message switches to inline edit view (not modal)."""
        page = mobile_page
        page.goto(f"http://localhost:9090/documents/{first_doc_id}")
        page.get_by_label("open chat").click()

        inp = page.get_by_role("dialog", name="document chat").get_by_role("textbox")
        inp.fill("Hello")
        page.keyboard.press("Enter")

        # Wait for AI response with Save button
        save_btn = page.get_by_role("button", name="Save").first
        save_btn.wait_for(state="visible", timeout=20000)
        save_btn.click()

        # Inline edit view: dialog content swaps to a heading "Save as Note" and a textarea
        edit_view = page.get_by_role("region", name="save note edit")
        expect(edit_view).to_be_visible()
        # No centered modal layered on top
        centered_modal = page.locator('div.fixed.inset-0.bg-opacity-40')
        expect(centered_modal).to_have_count(0)
