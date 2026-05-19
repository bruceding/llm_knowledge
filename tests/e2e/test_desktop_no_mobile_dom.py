"""Verify desktop viewport does not render any mobile-only shell DOM."""

from playwright.sync_api import Page, expect


class TestDesktopNoMobileDOM:
    def test_no_bottom_tab_bar_on_desktop(self, authenticated_page: Page):
        page = authenticated_page
        nav = page.get_by_role("navigation", name="bottom navigation")
        expect(nav).to_have_count(0)

    def test_no_mobile_drawer_on_desktop(self, authenticated_page: Page):
        page = authenticated_page
        drawer = page.get_by_label("navigation drawer")
        expect(drawer).to_have_count(0)

    def test_desktop_sidebar_visible(self, authenticated_page: Page):
        page = authenticated_page
        sidebar = page.locator("aside.w-64")
        expect(sidebar).to_be_visible()
