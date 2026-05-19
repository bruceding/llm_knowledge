"""
E2E tests for mobile shell: header, bottom tab bar, drawer.
Viewport: iPhone 12 (390x844).
"""

from playwright.sync_api import Page, expect


class TestMobileShell:
    def test_bottom_tab_bar_visible(self, mobile_page: Page):
        """Bottom tab bar with 5 tabs is visible on home."""
        page = mobile_page
        nav = page.get_by_role("navigation", name="bottom navigation")
        expect(nav).to_be_visible()
        expect(nav.get_by_label("inbox")).to_be_visible()
        expect(nav.get_by_label("documents")).to_be_visible()
        expect(nav.get_by_label("wiki")).to_be_visible()
        expect(nav.get_by_label("chat")).to_be_visible()
        expect(nav.get_by_label("more")).to_be_visible()

    def test_desktop_sidebar_hidden(self, mobile_page: Page):
        """Desktop fixed-width Sidebar should NOT be in DOM on mobile."""
        page = mobile_page
        sidebars = page.locator("aside.w-64")
        expect(sidebars).to_have_count(0)

    def test_drawer_opens_on_more_tap(self, mobile_page: Page):
        """Tapping 'more' opens the drawer."""
        page = mobile_page
        page.get_by_label("more").click()
        drawer = page.get_by_label("navigation drawer")
        expect(drawer).to_be_visible()
        expect(drawer.get_by_role("link", name="Inbox")).to_be_visible()

    def test_drawer_backdrop_closes(self, mobile_page: Page):
        """Clicking backdrop closes the drawer."""
        page = mobile_page
        page.get_by_label("more").click()
        page.locator('[aria-hidden="true"]').first.click()
        drawer = page.get_by_label("navigation drawer")
        page.wait_for_timeout(300)
        cls = drawer.get_attribute("class")
        assert "-translate-x-full" in (cls or "")

    def test_tab_navigation(self, mobile_page: Page):
        """Tapping tabs navigates and updates highlight."""
        page = mobile_page
        page.get_by_label("documents").click()
        expect(page).to_have_url("http://localhost:9090/documents")
        page.get_by_label("wiki").click()
        expect(page).to_have_url("http://localhost:9090/wiki")
        page.get_by_label("chat").click()
        expect(page).to_have_url("http://localhost:9090/chat")


class TestMobileListPages:
    def test_inbox_renders(self, mobile_page: Page):
        page = mobile_page
        # We're already on / from fixture
        expect(page.locator("h2")).to_be_visible()

    def test_documents_renders(self, mobile_page: Page):
        page = mobile_page
        page.get_by_label("documents").click()
        expect(page).to_have_url("http://localhost:9090/documents")

    def test_tags_via_drawer(self, mobile_page: Page):
        page = mobile_page
        page.get_by_label("more").click()
        page.get_by_role("link", name="Tags").click()
        expect(page).to_have_url("http://localhost:9090/tags")
