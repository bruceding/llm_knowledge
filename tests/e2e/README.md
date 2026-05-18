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

### Backend setup

The full app (frontend + backend) is served by a single Go process on **`http://localhost:9090`**. Start it with:

```bash
./start.sh
```

`start.sh` rebuilds the frontend (`frontend/dist`) into `backend/fs/dist`, recompiles the Go binary, kills any old process listening on `:9090`, and writes logs to `logs/llm-knowledge.log`. There is **no separate frontend dev server** — Playwright should always target `http://localhost:9090`.

### Programmatic login (preferred for repros / scripted tests)

The login UI requires a CAPTCHA, but the API exposes an **extension-client login flow** that skips it. Use this to obtain a token non-interactively:

```bash
curl -s -X POST http://localhost:9090/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"<user>","password":"<pass>","clientType":"extension"}'
# → {"token":"<UUID>","userId":N,"username":"<user>",...}
```

Then inject the token into `localStorage` BEFORE navigating, so Zustand's `auth-storage` rehydrates as logged-in:

```python
TOKEN = "<uuid from login>"
auth_storage = json.dumps({
    "state": {"isLoggedIn": True, "userId": 2, "username": "dingjing",
              "mustChangePassword": False, "token": TOKEN},
    "version": 0,
})
page.add_init_script(
    f"localStorage.setItem('token', '{TOKEN}');"
    f"localStorage.setItem('auth-storage', `{auth_storage}`);"
)
page.goto("http://localhost:9090/documents/<docId>")
```

This is what the `conftest.py` `saved_auth_state` fixture does as a one-time manual flow; for ad-hoc bug repros, prefer the programmatic path above.

### Run all E2E tests

```bash
source .venv/bin/activate   # required — top-level CLAUDE.md note: "页面改动必须加 e2e 测试，用 .venv 和 start.sh"
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

## Non-interactive auth against `localhost:9090`

`start.sh` builds and serves frontend + backend on `http://localhost:9090`.
The standard `conftest.py::saved_auth_state` flow opens a browser and waits for
a human to type the captcha. For headless reproductions / one-off bug repros,
use **API login + localStorage injection** to skip the captcha entirely:

```bash
# 1. Bring the service up (start.sh keeps it running on 9090):
./start.sh

# 2. Get a token via the extension login (skips captcha; see auth.go:191):
curl -s -X POST http://localhost:9090/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"<user>","password":"<password>","clientType":"extension"}'
# → {"token":"<UUID>","userId":<id>,"username":"<user>",...}
```

Inject the token via Playwright's `add_init_script` so the React app boots
already authenticated:

```python
import json
from playwright.sync_api import sync_playwright

TOKEN  = "<UUID-from-curl>"
USERID = 2
USER   = "<user>"

# Zustand persist key (frontend/src/store/authStore.ts)
auth_storage = json.dumps({
    "state": {"isLoggedIn": True, "userId": USERID, "username": USER,
              "mustChangePassword": False, "token": TOKEN},
    "version": 0,
})

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_context(viewport={"width": 1400, "height": 900}).new_page()
    page.add_init_script(
        f"localStorage.setItem('token', '{TOKEN}');"
        f"localStorage.setItem('auth-storage', `{auth_storage}`);"
    )
    page.goto("http://localhost:9090/documents/<docId>")
    page.wait_for_load_state("networkidle")
    # ... assertions ...
    browser.close()
```

Why both keys?

- `token` — read by `frontend/src/api.ts::getHeaders()` for fetch auth.
- `auth-storage` — Zustand-persisted state read by `PrivateRoute` to decide
  whether to redirect to `/login`. Setting only `token` still lands on `/login`.

Pick a `docId` to target with:

```bash
sqlite3 ~/.llm-knowledge/data/knowledge.db \
  "SELECT id, title, user_id FROM documents WHERE user_id=<id> ORDER BY id DESC LIMIT 5;"
```

Backend logs land in `logs/llm-knowledge.log` — `[session]` / `[docchat]`
lines are the most useful for chat reproductions.

## Development

To add new tests:

1. Create test file in `tests/e2e/` directory
2. Use Playwright API to interact with page elements
3. Use `expect()` for assertions
4. Use fixtures for setup/teardown