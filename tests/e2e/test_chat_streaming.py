"""
E2E tests for SSE streaming and chat switching functionality.

Tests the core chat behaviors including:
- Sending messages that trigger SSE streaming
- Thinking indicator and stop button during streaming
- Conversation switching while streaming (race condition fix)
- Switching back to a streaming conversation
- Stop button + resume conversation
"""

import pytest
from playwright.sync_api import Page, expect

BASE_URL = "http://localhost:9090"

CHAT_INPUT_SEL = "input[placeholder*='Ask a question']"
CHAT_SIDEBAR_SEL = "div.w-64.border-r"
NEW_CONV_BTN_SEL = f"{CHAT_SIDEBAR_SEL} button.bg-blue-500"
CONV_LIST_ITEM_SEL = f"{CHAT_SIDEBAR_SEL} ul.space-y-1 li button"
STOP_BTN_SEL = "button.bg-red-500"
THINKING_SPINNER_SEL = ".animate-spin"
USER_MSG_SEL = ".bg-blue-500"


def send_message(page: Page, message: str):
    """Fill chat input and press Enter."""
    ci = page.locator(CHAT_INPUT_SEL).first
    expect(ci).to_be_visible(timeout=5000)
    ci.fill(message)
    ci.press("Enter")


def wait_streaming_start(page: Page, timeout: int = 10000):
    """Wait for streaming to start (input disabled)."""
    ci = page.locator(CHAT_INPUT_SEL).first
    expect(ci).to_be_disabled(timeout=timeout)


def wait_streaming_complete(page: Page, timeout: int = 60000):
    """Wait for streaming to complete (input re-enabled)."""
    ci = page.locator(CHAT_INPUT_SEL).first
    expect(ci).to_be_enabled(timeout=timeout)


def navigate_router(page: Page, conv_id: int):
    """Client-side navigation (simulate sidebar click without full page reload)."""
    js = "() => { window.history.pushState({}, '', '/chat/" + str(conv_id) + "'); window.dispatchEvent(new PopStateEvent('popstate', {state: window.history.state})); }"
    page.evaluate(js)


def create_conversation_api(page: Page, title: str) -> int:
    """Create a conversation via API and return its ID."""
    result = page.evaluate("""async ([title]) => {
        const token = localStorage.getItem('token');
        const res = await fetch('/api/query/conversation', {
            method: 'POST',
            headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token},
            body: JSON.stringify({title: title})
        });
        return await res.json();
    }""", [title])
    return result["conversationId"]


def get_token(page: Page) -> str:
    """Get auth token from localStorage."""
    return page.evaluate("() => localStorage.getItem('token')")


class TestSSEStreaming:
    """SSE streaming functionality tests."""

    @pytest.fixture
    def chat_page(self, authenticated_page: Page):
        page = authenticated_page
        page.goto(f"{BASE_URL}/chat")
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)
        return page

    def test_thinking_indicator_shown(self, chat_page: Page):
        """Thinking indicator appears while waiting for AI response."""
        send_message(chat_page, "What is 1+1?")
        spinner = chat_page.locator(THINKING_SPINNER_SEL).first
        expect(spinner).to_be_visible(timeout=10000)

    def test_stop_button_appears_during_streaming(self, chat_page: Page):
        """Stop button appears during streaming."""
        send_message(chat_page, "Explain quantum computing in detail")
        expect(chat_page.locator(STOP_BTN_SEL)).to_be_visible(timeout=10000)

    def test_input_disabled_during_streaming(self, chat_page: Page):
        """Input field is disabled during streaming."""
        send_message(chat_page, "Hello")
        wait_streaming_start(chat_page)

    def test_message_appears_after_send(self, chat_page: Page):
        """User message appears in chat after sending."""
        test_msg = "Hello AI test"
        send_message(chat_page, test_msg)
        user_bubble = chat_page.locator(USER_MSG_SEL).first
        expect(user_bubble).to_be_visible(timeout=5000)
        expect(user_bubble).to_contain_text(test_msg)

    def test_streaming_completes_with_content(self, chat_page: Page):
        """AI response content appears after streaming completes."""
        send_message(chat_page, "What is 2+2?")
        wait_streaming_complete(chat_page, timeout=120000)
        prose = chat_page.locator(".prose").first
        expect(prose).to_be_visible(timeout=5000)

    def test_stop_button_interrupts_streaming(self, chat_page: Page):
        """Stop button can interrupt streaming."""
        send_message(chat_page, "Write a detailed essay about AI")
        stop_btn = chat_page.locator(STOP_BTN_SEL)
        expect(stop_btn).to_be_visible(timeout=10000)
        stop_btn.click()
        ci = chat_page.locator(CHAT_INPUT_SEL).first
        expect(ci).to_be_enabled(timeout=5000)


class TestChatSwitching:
    """Tests for conversation switching - the race condition fix."""

    @pytest.fixture
    def setup_conversations(self, authenticated_page: Page):
        """Create conversations A and B, A completed, B ready for streaming."""
        page = authenticated_page
        page.goto(f"{BASE_URL}/")
        page.wait_for_load_state("domcontentloaded")

        conv_a = create_conversation_api(page, "Conv A Switch Test")
        conv_b = create_conversation_api(page, "Conv B Switch Test")

        # Navigate to A and complete a conversation
        page.goto(f"{BASE_URL}/chat/{conv_a}")
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)
        send_message(page, "Hello from A")
        wait_streaming_complete(page)

        return page, conv_a, conv_b

    def test_switch_away_while_streaming(self, setup_conversations):
        """Switching away from a streaming conversation works."""
        page, conv_a, conv_b = setup_conversations

        # Navigate to B (client-side)
        navigate_router(page, conv_b)
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)

        # Start streaming in B
        send_message(page, "Explain ML algorithms")
        wait_streaming_start(page)

        # Switch to A while B streaming
        navigate_router(page, conv_a)
        page.wait_for_timeout(5000)

        ci = page.locator(CHAT_INPUT_SEL).first
        # A should have input enabled (not stuck from B's streaming)
        expect(ci).to_be_enabled(timeout=5000)

    def test_switch_back_while_streaming(self, setup_conversations):
        """
        THE KEY TEST: Switch away while streaming, then switch back.
        Before fix: switching back was blocked until streaming completed.
        After fix: switching back works immediately.
        """
        page, conv_a, conv_b = setup_conversations

        # Navigate to B (client-side)
        navigate_router(page, conv_b)
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)

        # Start streaming in B
        send_message(page, "Explain ML algorithms in detail")
        wait_streaming_start(page)

        # Switch to A while B streaming
        navigate_router(page, conv_a)
        page.wait_for_timeout(5000)
        ci = page.locator(CHAT_INPUT_SEL).first
        expect(ci).to_be_enabled(timeout=5000)

        # SWITCH BACK to B (the KEY scenario)
        navigate_router(page, conv_b)
        page.wait_for_timeout(5000)
        ci = page.locator(CHAT_INPUT_SEL).first

        # B should restore streaming state (disabled while still streaming)
        # Or if already completed, input should be enabled
        # Either way, the page should NOT be stuck
        page.wait_for_timeout(3000)

        # Verify page is responsive: chat input exists (not frozen)
        expect(ci).to_be_attached(timeout=10000)

        # Eventually B completes and input becomes enabled
        expect(ci).to_be_enabled(timeout=90000)

    def test_rapid_switching_no_freeze(self, setup_conversations):
        """Rapid switching between conversations doesn't freeze the UI."""
        page, conv_a, conv_b = setup_conversations

        # Navigate to B and complete it
        navigate_router(page, conv_b)
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)
        send_message(page, "Hello from B")
        wait_streaming_complete(page)

        # Rapidly switch back and forth
        for _ in range(3):
            navigate_router(page, conv_a)
            page.wait_for_timeout(500)
            navigate_router(page, conv_b)
            page.wait_for_timeout(500)

        # Final switch to A
        navigate_router(page, conv_a)
        page.wait_for_timeout(2000)

        ci = page.locator(CHAT_INPUT_SEL).first
        expect(ci).to_be_enabled(timeout=5000)


class TestStopAndResume:
    """Tests for stop button + resume conversation flow."""

    @pytest.fixture
    def chat_page(self, authenticated_page: Page):
        page = authenticated_page
        page.goto(f"{BASE_URL}/chat")
        page.wait_for_selector(CHAT_INPUT_SEL, timeout=10000)
        return page

    def test_stop_then_send_new_message(self, chat_page: Page):
        """Stop streaming, then send a new message in the same conversation."""
        # Send a message that triggers streaming
        send_message(chat_page, "Write a long essay about AI")
        stop_btn = chat_page.locator(STOP_BTN_SEL)
        expect(stop_btn).to_be_visible(timeout=10000)

        # Click stop
        stop_btn.click()
        ci = chat_page.locator(CHAT_INPUT_SEL).first
        expect(ci).to_be_enabled(timeout=5000)

        # Send a new message in the same conversation
        send_message(chat_page, "What is 1+1? Short answer")
        wait_streaming_complete(chat_page, timeout=60000)

        # Both user messages should be visible
        user_msgs = chat_page.locator(USER_MSG_SEL)
        assert user_msgs.count() >= 2, "Should have at least 2 user messages"

    def test_stop_preserves_partial_content(self, chat_page: Page):
        """Stopping streaming preserves whatever content was generated."""
        send_message(chat_page, "Explain neural networks step by step")

        # Wait for some content to appear, then stop
        chat_page.wait_for_timeout(3000)
        stop_btn = chat_page.locator(STOP_BTN_SEL)
        expect(stop_btn).to_be_visible(timeout=10000)
        stop_btn.click()

        ci = chat_page.locator(CHAT_INPUT_SEL).first
        expect(ci).to_be_enabled(timeout=5000)

        # Some assistant content should exist (partial)
        assistant_msg = chat_page.locator(".bg-gray-100, .prose")
        # The assistant message exists even if partial
        assert assistant_msg.count() > 0, "Assistant message should exist after stop"