"""
E2E tests for ChatView component (SSE streaming chat)

Tests the main chat functionality including:
- Creating conversations
- Sending messages with SSE streaming
- Conversation history sidebar
- Interruption (stop button)
- Image upload
"""

import pytest
from playwright.sync_api import Page, expect


@pytest.fixture(scope="function")
def setup_page(page: Page):
    """Setup page with base URL and wait for app to load"""
    # Assuming frontend runs on localhost:5173 (Vite dev server)
    page.goto("http://localhost:5173/chat")
    # Wait for the chat interface to be fully loaded
    page.wait_for_selector("h2", timeout=10000)
    return page


class TestChatViewBasic:
    """Basic ChatView functionality tests"""

    def test_chat_page_loads(self, setup_page: Page):
        """Test that chat page loads successfully"""
        page = setup_page

        # Verify header elements exist
        expect(page.locator("h2")).to_be_visible()

        # Verify input field exists
        expect(page.locator("input[type='text']")).to_be_visible()

        # Verify send button exists
        expect(page.locator("button:has-text('Send')")).to_be_visible()

    def test_empty_state_message(self, setup_page: Page):
        """Test that empty state message is shown when no messages"""
        page = setup_page

        # Should show "Start a conversation" message
        expect(page.locator("text=Start a conversation")).to_be_visible()

    def test_send_message_button_disabled_when_empty(self, setup_page: Page):
        """Test that send button is disabled when input is empty"""
        page = setup_page

        # Clear input
        page.locator("input[type='text']").fill("")
        page.locator("input[type='text']").press("Enter")

        # Send button should be disabled
        send_button = page.locator("button:has-svg")  # The send button has an SVG icon
        expect(send_button).to_be_disabled()


class TestChatViewSidebar:
    """Chat history sidebar tests"""

    def test_sidebar_toggle(self, setup_page: Page):
        """Test sidebar can be toggled open and closed"""
        page = setup_page

        # Click menu button to open sidebar
        menu_button = page.locator("button:has(svg)").first
        menu_button.click()

        # Sidebar should be visible
        expect(page.locator("text=New Conversation")).to_be_visible()

        # Click menu button again to close
        menu_button.click()

        # Sidebar should be hidden (check for absence of sidebar container)
        sidebar = page.locator(".w-64.border-r")
        expect(sidebar).not_to_be_visible()

    def test_new_conversation_button(self, setup_page: Page):
        """Test new conversation button in sidebar"""
        page = setup_page

        # Open sidebar
        page.locator("button:has(svg)").first.click()

        # Click new conversation button
        new_chat_button = page.locator("button:has-text('New Conversation')")
        new_chat_button.click()

        # Should create new conversation and close sidebar
        expect(page.locator("h2")).to_contain_text("New Conversation")


class TestChatViewInput:
    """Chat input functionality tests"""

    def test_type_and_clear_input(self, setup_page: Page):
        """Test typing in input field and clearing"""
        page = setup_page

        input_field = page.locator("input[type='text']")

        # Type some text
        input_field.fill("Hello, this is a test message")

        # Verify text is in input
        expect(input_field).to_have_value("Hello, this is a test message")

        # Clear the input
        input_field.clear()

        # Verify input is empty
        expect(input_field).to_have_value("")

    def test_enter_key_sends_message(self, setup_page: Page):
        """Test that Enter key sends message (when not empty)"""
        page = setup_page

        # Note: This test would require backend to be running
        # For now, we just verify the input is cleared after pressing Enter
        input_field = page.locator("input[type='text']")
        input_field.fill("Test message")
        input_field.press("Enter")

        # Input should be cleared after sending
        expect(input_field).to_have_value("", timeout=5000)


class TestChatViewImageUpload:
    """Image upload functionality tests"""

    def test_image_upload_button_exists(self, setup_page: Page):
        """Test that image upload button exists"""
        page = setup_page

        # Find the image upload button (has + icon)
        upload_button = page.locator("button:has-text('+')")
        expect(upload_button).to_be_visible()

    def test_image_upload_disabled_while_streaming(self, setup_page: Page):
        """Test that image upload is disabled during streaming"""
        page = setup_page

        # This would need a streaming state to test properly
        # For now, just verify button exists
        upload_button = page.locator("button:has-text('+')")
        expect(upload_button).to_be_visible()


class TestChatViewStreaming:
    """SSE streaming functionality tests"""

    @pytest.mark.skip(reason="Requires backend running and mock SSE server")
    def test_thinking_indicator_shown(self, setup_page: Page):
        """Test that thinking indicator is shown while waiting for response"""
        page = setup_page

        # Send a message (would need backend)
        input_field = page.locator("input[type='text']")
        input_field.fill("Hello")
        input_field.press("Enter")

        # Should show "Thinking..." indicator
        expect(page.locator("text=Thinking")).to_be_visible(timeout=5000)

    @pytest.mark.skip(reason="Requires backend running and mock SSE server")
    def test_stop_button_appears_during_streaming(self, setup_page: Page):
        """Test that stop button appears during streaming"""
        page = setup_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Tell me a long story")
        input_field.press("Enter")

        # Stop button (red button with square icon) should appear
        stop_button = page.locator("button.bg-red-500")
        expect(stop_button).to_be_visible(timeout=3000)

    @pytest.mark.skip(reason="Requires backend running and mock SSE server")
    def test_stop_button_interrupts_stream(self, setup_page: Page):
        """Test that stop button interrupts the stream"""
        page = setup_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Tell me a long story")
        input_field.press("Enter")

        # Click stop button
        stop_button = page.locator("button.bg-red-500")
        stop_button.click()

        # Should stop streaming and show "[Stopped]" or similar
        expect(page.locator("text=/[Stopped|已停止]/")).to_be_visible(timeout=3000)


class TestChatViewConversation:
    """Conversation management tests"""

    @pytest.mark.skip(reason="Requires backend running")
    def test_message_appears_after_send(self, setup_page: Page):
        """Test that user message appears in chat after sending"""
        page = setup_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Hello AI")
        input_field.press("Enter")

        # User message should appear in blue bubble
        user_message = page.locator(".bg-blue-500").locator("text=Hello AI")
        expect(user_message).to_be_visible(timeout=3000)

    @pytest.mark.skip(reason="Requires backend and conversation history")
    def test_conversation_appears_in_sidebar(self, setup_page: Page):
        """Test that new conversation appears in history sidebar"""
        page = setup_page

        # Send a message to create a conversation
        input_field = page.locator("input[type='text']")
        input_field.fill("Create a new conversation")
        input_field.press("Enter")

        # Wait for response
        page.wait_for_timeout(5000)

        # Open sidebar
        page.locator("button:has(svg)").first.click()

        # Conversation should appear in list
        expect(page.locator("ul.space-y-1 li")).to_be_visible()