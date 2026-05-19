"""ChatView mobile single-column routing."""

from playwright.sync_api import Page, expect


class TestMobileChatView:
    def test_chat_root_shows_session_list_only(self, mobile_page: Page):
        page = mobile_page
        page.get_by_label("chat").click()
        expect(page).to_have_url("http://localhost:9090/chat")
        # On mobile, conversation stream area should NOT be rendered when no id
        msg_input = page.get_by_role("textbox", name="message input")
        expect(msg_input).to_have_count(0)

    def test_chat_with_id_shows_stream_only(self, mobile_page: Page):
        page = mobile_page
        page.goto("http://localhost:9090/chat")
        sessions = page.locator('[data-testid="chat-session-item"]')
        if sessions.count() == 0:
            return  # no sessions, skip
        sessions.first.click()
        expect(page.get_by_role("textbox", name="message input")).to_be_visible()
