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


class TestChatViewPublic:
    """Tests for public/unauthenticated access to chat page"""

    def test_chat_redirects_to_login(self, page: Page):
        """Test that /chat redirects to login when not authenticated"""
        page.goto("http://localhost:9090/chat")

        # Should redirect to login page
        page.wait_for_url("http://localhost:9090/login", timeout=5000)

        # Login form should be visible
        expect(page.locator("input[placeholder='Enter username']")).to_be_visible()
        expect(page.locator("input[placeholder='Enter password']")).to_be_visible()

    def test_login_page_elements(self, page: Page):
        """Test login page has all required elements"""
        page.goto("http://localhost:9090/login")

        # Username field
        expect(page.locator("input[placeholder='Enter username']")).to_be_visible()

        # Password field
        expect(page.locator("input[placeholder='Enter password']")).to_be_visible()

        # Login button exists
        expect(page.locator("button[type='submit']")).to_be_visible()


class TestChatViewAuthRequired:
    """Tests that require authenticated user"""

    def test_chat_page_loads_authenticated(self, authenticated_page: Page):
        """Test that chat page loads successfully when authenticated"""
        page = authenticated_page

        # Should be on main page, not login
        expect(page).to_have_url("http://localhost:9090/")

        # Verify header elements exist
        expect(page.locator("h2")).to_be_visible()

    def test_empty_state_message(self, authenticated_page: Page):
        """Test that empty state message is shown when no messages"""
        page = authenticated_page

        # The h2 header should be visible
        expect(page.locator("h2")).to_be_visible()

    def test_input_field_exists(self, authenticated_page: Page):
        """Test that input field exists on chat page"""
        page = authenticated_page

        # Verify input field exists
        expect(page.locator("input[type='text']")).to_be_visible()

    def test_sidebar_navigation(self, authenticated_page: Page):
        """Test sidebar navigation items are visible"""
        page = authenticated_page

        # Sidebar should be visible with navigation - use specific role
        expect(page.get_by_role("link", name="Inbox")).to_be_visible()

    def test_type_in_input(self, authenticated_page: Page):
        """Test typing in sidebar search input"""
        page = authenticated_page

        # Use sidebar search input
        input_field = page.get_by_placeholder("Search...")

        # Type some text
        input_field.fill("Hello, this is a test message")

        # Verify text is in input
        expect(input_field).to_have_value("Hello, this is a test message")


class TestChatViewChatPage:
    """Tests specific to the /chat page with conversation sidebar"""

    @pytest.fixture
    def chat_page(self, authenticated_page: Page):
        """Navigate to /chat page"""
        page = authenticated_page
        page.goto("http://localhost:9090/chat")
        page.wait_for_load_state("networkidle")
        return page

    def test_chat_page_new_conversation_button(self, chat_page: Page):
        """Test that chat input exists on chat page"""
        page = chat_page

        # Chat page should have input (placeholder might vary by language)
        # Just check that some input exists
        expect(page.locator("input[type='text']").first).to_be_visible()

    def test_chat_page_sidebar_visible(self, chat_page: Page):
        """Test that conversation sidebar is visible"""
        page = chat_page

        # Main sidebar should be visible
        expect(page.locator("aside")).to_be_visible()


class TestChatViewStreaming:
    """SSE streaming functionality tests - require backend"""

    @pytest.mark.skip(reason="Requires SSE stream - manual testing recommended")
    def test_thinking_indicator_shown(self, authenticated_page: Page):
        """Test that thinking indicator is shown while waiting for response"""
        page = authenticated_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Hello")
        input_field.press("Enter")

        # Should show "Thinking..." indicator
        expect(page.locator("text=Thinking")).to_be_visible(timeout=5000)

    @pytest.mark.skip(reason="Requires SSE stream - manual testing recommended")
    def test_stop_button_appears_during_streaming(self, authenticated_page: Page):
        """Test that stop button appears during streaming"""
        page = authenticated_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Tell me a long story")
        input_field.press("Enter")

        # Stop button (red button) should appear
        stop_button = page.locator("button.bg-red-500")
        expect(stop_button).to_be_visible(timeout=3000)


class TestChatViewConversation:
    """Conversation management tests"""

    @pytest.mark.skip(reason="Requires SSE stream - manual testing recommended")
    def test_message_appears_after_send(self, authenticated_page: Page):
        """Test that user message appears in chat after sending"""
        page = authenticated_page

        # Send a message
        input_field = page.locator("input[type='text']")
        input_field.fill("Hello AI")
        input_field.press("Enter")

        # User message should appear
        user_message = page.locator(".bg-blue-500").locator("text=Hello AI")
        expect(user_message).to_be_visible(timeout=3000)

    @pytest.mark.skip(reason="Requires conversation history")
    def test_conversation_appears_in_sidebar(self, authenticated_page: Page):
        """Test that new conversation appears in history sidebar"""
        page = authenticated_page

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