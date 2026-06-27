"""
E2E test for the paper section-explain feature.

Seeds a PDF via /api/raw/pdf (UploadPDF uses pdftotext, whose paper.md has no
## markdown headings), opens the doc detail page, clicks the "章节讲解" tab,
and asserts the empty-state guidance renders. This validates the full wiring
(tab button -> view -> GET /api/documents/:id/sections -> empty state)
without depending on the Claude CLI.
"""

from pathlib import Path

import pytest
from playwright.sync_api import Page

SAMPLE_PDF = Path(__file__).parent.parent.parent / "backend" / "ingest" / "testdata" / "sample.pdf"


def _upload_pdf(page: Page) -> int:
    token = page.evaluate("localStorage.getItem('token')")
    assert token, "not authenticated"
    data = SAMPLE_PDF.read_bytes()
    resp = page.request.post(
        "http://localhost:9090/api/raw/pdf",
        headers={"Authorization": f"Bearer {token}"},
        multipart={
            "file": {"filename": "sample.pdf", "mimeType": "application/pdf", "buffer": data},
        },
    )
    assert resp.ok, f"PDF upload failed: {resp.status} {resp.text()}"
    body = resp.json()
    return int(body["id"])


def _delete_document(page: Page, doc_id: int) -> None:
    page.evaluate(
        """async (id) => {
            const t = localStorage.getItem('token');
            await fetch('/api/documents/' + id, {
                method: 'DELETE',
                headers: {'Authorization': 'Bearer ' + t},
            });
        }""",
        doc_id,
    )


@pytest.mark.requires_auth
def test_pdf_sections_tab_shows_empty_state(authenticated_page: Page):
    page = authenticated_page
    doc_id = _upload_pdf(page)
    try:
        page.goto(f"http://localhost:9090/documents/{doc_id}")
        page.wait_for_load_state("networkidle")
        page.wait_for_selector("[data-testid='paper-sections-tab']", timeout=5000)
        page.click("[data-testid='paper-sections-tab']")
        # pdftotext output has no ## section headings -> empty-state guidance.
        page.wait_for_selector("[data-testid='paper-sections-empty']", timeout=5000)
        assert page.is_visible("[data-testid='paper-sections-empty']")
    finally:
        _delete_document(page, doc_id)
