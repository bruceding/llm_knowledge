# E2E Tests

End-to-end tests for the frontend components using Playwright.

## Setup

### Prerequisites

- Python 3.12+
- Node.js 18+ (for running frontend dev server)

### Installation

1. Create and activate virtual environment:

```bash
python3 -m venv .venv
source .venv/bin/activate  # On macOS/Linux
# or
.venv\Scripts\activate  # On Windows
```

2. Install dependencies:

```bash
pip install playwright pytest-playwright
playwright install chromium
```

## Running Tests

### Run all E2E tests

```bash
source .venv/bin/activate
pytest tests/e2e
```

### Run specific test file

```bash
source .venv/bin/activate
pytest tests/e2e/test_chat_view.py
```

### Run with headed mode (see browser)

```bash
source .venv/bin/activate
pytest tests/e2e --headed
```

### Run specific test

```bash
source .venv/bin/activate
pytest tests/e2e/test_chat_view.py::TestChatViewBasic::test_chat_page_loads
```

## Test Categories

### ChatView Tests (`test_chat_view.py`)

Tests for the main chat interface:

- **Basic Tests**: Page loading, empty state, button states
- **Sidebar Tests**: Toggle sidebar, new conversation button
- **Input Tests**: Typing, clearing, Enter key behavior
- **Image Upload Tests**: Upload button visibility, disabled state
- **Streaming Tests**: Thinking indicator, stop button, interruption
- **Conversation Tests**: Message display, conversation history

### DocumentChatPanel Tests (`test_document_chat_panel.py`)

Tests for the document chat panel:

- **Basic Tests**: Panel loading, input field, empty state
- **Session Tests**: Connection indicator, reconnection
- **Messaging Tests**: Send message, thinking indicator, streaming
- **Note Saving Tests**: Save button, modal, save/cancel actions
- **Clear Tests**: Clear button, conversation clearing
- **Error Handling Tests**: Error display, session expiration

## Notes

- Tests marked with `@pytest.mark.skip` require backend server running
- For full test coverage, start both frontend and backend servers before running tests
- The tests use fixtures to set up the page state before each test

## Development

To add new tests:

1. Create test file in `tests/e2e/` directory
2. Use Playwright API to interact with page elements
3. Use `expect()` for assertions
4. Use fixtures for setup/teardown