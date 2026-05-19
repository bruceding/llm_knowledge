"""
Common configuration and fixtures for E2E tests
"""

import pytest
from playwright.sync_api import Page, Browser
from pathlib import Path


# Path to store authentication state
AUTH_STATE_FILE = Path(__file__).parent / ".auth" / "state.json"


@pytest.fixture(scope="session")
def browser_context_args(browser_context_args):
    """
    Configure browser context for all tests
    """
    return {
        **browser_context_args,
        "viewport": {"width": 1280, "height": 720},
    }


@pytest.fixture(scope="function")
def page(page: Page):
    """
    Configure page for each test
    """
    page.set_default_timeout(10000)
    return page


@pytest.fixture(scope="session")
def saved_auth_state(browser: Browser, browser_context_args):
    """
    Perform login once per session and save the auth state.
    Returns the path to the saved state file.
    """
    AUTH_STATE_FILE.parent.mkdir(parents=True, exist_ok=True)

    # If already logged in this session, use existing state
    if AUTH_STATE_FILE.exists():
        # Verify the state is still valid
        context = browser.new_context(storage_state=str(AUTH_STATE_FILE))
        page = context.new_page()
        page.goto("http://localhost:9090/")

        # Check if we're still logged in
        if not page.url.endswith("/login"):
            print("\n✅ Using saved authentication state")
            page.close()
            context.close()
            return str(AUTH_STATE_FILE)

        # State expired, delete it
        page.close()
        context.close()
        AUTH_STATE_FILE.unlink()
        print("\n⚠️ Saved auth state expired, need to re-login")

    # Perform manual login
    print("\n" + "=" * 60)
    print("🔐 Opening browser for manual login...")
    print("=" * 60 + "\n")

    context = browser.new_context(**browser_context_args)
    page = context.new_page()

    page.goto("http://localhost:9090/login")
    page.wait_for_selector("input[placeholder='Enter username']", timeout=5000)

    # Auto-fill credentials
    page.locator("input[placeholder='Enter username']").fill("")
    page.locator("input[placeholder='Enter password']").fill("")

    print("\n⚠️  Username/password filled. Please:")
    print("   1. Enter the captcha from the image")
    print("   2. Click Login button")
    print("   Waiting up to 90 seconds for successful login...")
    print("=" * 60 + "\n")

    # Wait for successful login redirect
    try:
        page.wait_for_url("http://localhost:9090/", timeout=90000)

        # Wait for localStorage to sync
        page.wait_for_timeout(2000)

        # Verify token is saved
        token = page.evaluate("localStorage.getItem('token')")
        if not token:
            print("\n❌ Token not found in localStorage after login")
            page.close()
            context.close()
            pytest.exit("Login succeeded but token not saved")

        print(f"\n✅ Login successful! Token saved: {token[:20]}...")

        # Save storage state
        context.storage_state(path=str(AUTH_STATE_FILE))
        print(f"   Auth state saved to: {AUTH_STATE_FILE}")

        page.close()
        context.close()

        return str(AUTH_STATE_FILE)

    except Exception as e:
        print(f"\n❌ Login failed or timed out: {e}")
        page.close()
        context.close()
        pytest.exit("Login required - run tests again to retry")


@pytest.fixture(scope="function")
def authenticated_page(saved_auth_state: str, browser: Browser):
    """
    Return a page with authenticated state loaded
    """
    context = browser.new_context(storage_state=saved_auth_state)
    page = context.new_page()

    page.goto("http://localhost:9090/")

    # Verify we're logged in
    if page.url.endswith("/login"):
        # Auth state expired - delete and skip
        Path(saved_auth_state).unlink(missing_ok=True)
        context.close()
        pytest.skip("Auth state expired - restart tests to re-login")

    yield page

    context.close()


@pytest.fixture(scope="function")
def mobile_page(saved_auth_state: str, browser: Browser):
    """
    Authenticated page with mobile viewport (iPhone 12: 390x844).
    """
    context = browser.new_context(
        storage_state=saved_auth_state,
        viewport={"width": 390, "height": 844},
        device_scale_factor=3,
        is_mobile=True,
        has_touch=True,
        user_agent="Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) "
                   "AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
    )
    page = context.new_page()
    page.goto("http://localhost:9090/")

    if page.url.endswith("/login"):
        Path(saved_auth_state).unlink(missing_ok=True)
        context.close()
        pytest.skip("Auth state expired - restart tests to re-login")

    yield page

    context.close()


def pytest_configure(config):
    """
    Configure pytest with custom markers
    """
    config.addinivalue_line(
        "markers", "skip(reason): mark test to be skipped"
    )
    config.addinivalue_line(
        "markers", "requires_backend: mark test that requires backend server"
    )
    config.addinivalue_line(
        "markers", "requires_auth: mark test that requires authenticated user"
    )