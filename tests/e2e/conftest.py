"""
Common configuration and fixtures for E2E tests
"""

import pytest
from playwright.sync_api import Page


@pytest.fixture(scope="session")
def browser_context_args(browser_context_args):
    """
    Configure browser context for all tests
    """
    return {
        **browser_context_args,
        # Add common configuration here
        "viewport": {"width": 1280, "height": 720},
    }


@pytest.fixture(scope="function")
def page(page: Page):
    """
    Configure page for each test
    """
    # Set default timeout
    page.set_default_timeout(10000)

    # Return configured page
    return page


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