"""
E2E tests for DocumentChatPanel — the document chat SSE path.

This file used to hold 16 tests that all pointed at the long-gone :5173 dev
server and were unconditionally skipped, leaving /api/doc-chat with zero
automation. It now runs against the single binary on :9090 and seeds its own
document via the web-clip API (same convention as test_doc_detail_shortcuts.py),
so it does not depend on existing user data.

Covered:
- opening the Chat tab establishes GET /api/doc-chat/stream?docId=… and the panel
  becomes ready (input enabled, no error banner)
- the SSE `session` event is captured — proved by the reconnect-by-id request
  issued when the chat tab is re-activated
- repeated tab switching keeps the panel healthy instead of wedging
- sending a message POSTs /api/doc-chat/message with that sessionId and the
  reply streams back over the same SSE connection
- Clear wipes the messages and restarts the session with ?fresh=1
- Save-as-note modal flow (the note POST is stubbed so runs don't litter the DB)

Requires ./start.sh (backend on :9090) and a working Claude CLI: the messaging
tests drive real Claude turns.
"""

import json
import re
import time
from collections import namedtuple
from urllib.parse import parse_qs, urlparse

import pytest
from playwright.sync_api import Page, expect

BASE_URL = "http://localhost:9090"

CHAT_TAB_SEL = "button:text-is('Chat')"
NOTES_TAB_SEL = "button:text-is('Notes')"
DOC_INPUT_SEL = "input[placeholder='Ask about this document...']"
CLEAR_BTN_SEL = "button:text-is('Clear')"
# 用户气泡: 行容器 justify-end(仅 user 行有),气泡 bg-blue-500 text-white。
# 与 test_chat_streaming.py 保持同一套选择器约定。
USER_BUBBLE_SEL = "div.justify-end > div.bg-blue-500.text-white"
ASSISTANT_BUBBLE_SEL = "div.justify-start > div.bg-gray-100"
ERROR_BANNER_SEL = "div.text-red-500.bg-red-50"
# 空状态文案(与输入框 placeholder 同文)。不能用 class 定位: Notes tab 的空状态
# 用的是同一组 class,虽然被 CSS hidden 但仍留在 DOM 里,会触发 strict mode 冲突。
EMPTY_STATE_TEXT = "Ask about this document..."
# Save 按钮只在 !msg.isStreaming 时渲染,因此它是「回合结束」的确切信号
# (思考中的气泡里也有文字 "Thinking...",不能用气泡非空判断结束)。
SAVE_NOTE_BTN_SEL = "button[title='Save as note']"
NOTE_MODAL_SEL = "div.fixed.inset-0"

# A real Claude turn: read files, then answer. Generous, but bounded.
REPLY_TIMEOUT_MS = 120_000

SEED_HTML = (
    "<html><head><title>Doc Chat E2E</title></head><body>"
    "<p>The capital of Florin is Guilder. The currency is the Florin dollar.</p>"
    + "".join(f"<p>Section {i} — {'background text ' * 12}</p>" for i in range(10))
    + "</body></html>"
)
SEED_URL_PREFIX = "https://example.test/e2e-doc-chat/"

DocChat = namedtuple("DocChat", "page doc_id stream_response")


def _seed_document(page: Page) -> int:
    """Create a document via the web-clip API and return its id."""
    seed_url = f"{SEED_URL_PREFIX}{int(time.time() * 1000)}"
    result = page.evaluate(
        """async ({url, html}) => {
            const t = localStorage.getItem('token');
            const r = await fetch('/api/raw/web-clip', {
                method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + t},
                body: JSON.stringify({url, html, title: 'Doc Chat E2E'}),
            });
            return {status: r.status, body: await r.json()};
        }""",
        {"url": seed_url, "html": SEED_HTML},
    )
    assert result["status"] == 200, f"Seed failed: {result}"
    body = result["body"]
    doc_id = body.get("document", {}).get("id") or body.get("id") or body.get("documentId")
    assert doc_id, f"Could not find doc id in response: {body}"
    return int(doc_id)


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


@pytest.fixture()
def doc_chat(authenticated_page: Page):
    """Seed a doc, open its Chat tab, wait for the SSE stream, clean up after.

    Yields DocChat(page, doc_id, stream_response) once the panel is connected:
    the /stream response arrived and the input is enabled (it stays disabled
    while the panel is `connecting`).
    """
    page = authenticated_page
    doc_id = _seed_document(page)
    try:
        page.goto(f"{BASE_URL}/documents/{doc_id}")
        page.wait_for_load_state("networkidle")
        page.wait_for_selector(CHAT_TAB_SEL, timeout=10_000)

        with page.expect_response(
            lambda r: f"/api/doc-chat/stream?docId={doc_id}" in r.url, timeout=30_000
        ) as stream_info:
            page.locator(CHAT_TAB_SEL).first.click()

        expect(page.locator(DOC_INPUT_SEL)).to_be_enabled(timeout=20_000)
        yield DocChat(page, doc_id, stream_info.value)
    finally:
        _delete_document(page, doc_id)


def send_doc_message(dc: DocChat, message: str):
    """Type into the doc chat input and press Enter."""
    ci = dc.page.locator(DOC_INPUT_SEL)
    expect(ci).to_be_enabled(timeout=10_000)
    ci.fill(message)
    # fill 可能被受控输入框的重渲染吞掉,不确认就 press 会变成静默失败
    expect(ci).to_have_value(message, timeout=5_000)
    ci.press("Enter")


def wait_reply(dc: DocChat, timeout: int = REPLY_TIMEOUT_MS) -> str:
    """Wait for the assistant turn to finish and return its text."""
    page = dc.page
    expect(page.locator(SAVE_NOTE_BTN_SEL).first).to_be_visible(timeout=timeout)
    expect(page.locator(DOC_INPUT_SEL)).to_be_enabled(timeout=10_000)
    reply = page.locator(ASSISTANT_BUBBLE_SEL).last.inner_text().strip()
    assert reply, "assistant bubble is empty after the turn completed"
    assert not re.fullmatch(r"Thinking\.{0,3}", reply), f"reply never replaced the thinking placeholder: {reply!r}"
    return reply


class TestDocChatConnection:
    """SSE handshake and session handling (no Claude turn needed)."""

    def test_chat_tab_opens_sse_stream(self, doc_chat: DocChat):
        """Chat tab opens GET /api/doc-chat/stream?docId=… and the panel is usable."""
        dc = doc_chat
        assert dc.stream_response.status == 200, f"stream failed: {dc.stream_response.status}"
        assert f"docId={dc.doc_id}" in dc.stream_response.url
        assert "fresh=" not in dc.stream_response.url, "first connect must not force a fresh session"

        expect(dc.page.locator(DOC_INPUT_SEL)).to_be_visible()
        expect(dc.page.get_by_text(EMPTY_STATE_TEXT, exact=True)).to_be_visible()
        expect(dc.page.locator(ERROR_BANNER_SEL)).to_have_count(0)

    def test_session_id_captured_and_reused_on_tab_switch(self, doc_chat: DocChat):
        """回到 Chat tab 必须用 SSE 里拿到的 sessionId 走 /reconnect。

        这是「session 事件确实被解析并保存」唯一可观测的证据: 前端只有在拿到
        sessionId 之后才会 reconnect-by-id,否则会退回重新 /stream(并重启一个
        Claude 进程)。
        """
        page = doc_chat.page
        page.locator(NOTES_TAB_SEL).first.click()
        # 给面板一点时间执行 active=false 的 cleanup(abort 掉旧 SSE),
        # 否则切回来时可能还挂在旧连接上,不会发出 reconnect。
        page.wait_for_timeout(300)

        with page.expect_request(
            lambda r: "/api/doc-chat/reconnect?sessionId=" in r.url, timeout=20_000
        ) as req_info:
            page.locator(CHAT_TAB_SEL).first.click()

        sid = parse_qs(urlparse(req_info.value.url).query).get("sessionId", [""])[0]
        assert sid, f"reconnect without sessionId: {req_info.value.url}"
        expect(page.locator(DOC_INPUT_SEL)).to_be_enabled(timeout=20_000)
        expect(page.locator(ERROR_BANNER_SEL)).to_have_count(0)

    def test_repeated_tab_switching_stays_healthy(self, doc_chat: DocChat):
        """反复切 tab 不能把面板卡死(重连循环 / 连接数打满)。"""
        page = doc_chat.page
        for _ in range(3):
            page.locator(NOTES_TAB_SEL).first.click()
            page.locator(CHAT_TAB_SEL).first.click()
            expect(page.locator(DOC_INPUT_SEL)).to_be_enabled(timeout=20_000)
            expect(page.locator(ERROR_BANNER_SEL)).to_have_count(0)


class TestDocChatMessaging:
    """Sending a message over the SSE session (real Claude turns)."""

    def test_send_message_streams_reply(self, doc_chat: DocChat):
        dc = doc_chat
        page = dc.page
        question = "Reply with exactly one word: pong"

        with page.expect_request(
            lambda r: "/api/doc-chat/message" in r.url, timeout=15_000
        ) as req_info:
            send_doc_message(dc, question)

        body = json.loads(req_info.value.post_data or "{}")
        assert body.get("message") == question, f"wrong message posted: {body}"
        assert body.get("sessionId"), f"posted without the SSE sessionId: {body}"

        expect(page.locator(USER_BUBBLE_SEL).last).to_contain_text(question, timeout=5_000)
        wait_reply(dc)
        expect(page.locator(ERROR_BANNER_SEL)).to_have_count(0)

    def test_clear_wipes_messages_and_restarts_fresh(self, doc_chat: DocChat):
        """Clear 必须清空消息并带 fresh=1 重开 session。

        fresh=1 是关键: 没有它后端会拿 DB 里的 chat_session_id 去 --resume,
        刚清空的对话又会被接回来。
        """
        page = doc_chat.page
        send_doc_message(doc_chat, "Say something short")
        expect(page.locator(USER_BUBBLE_SEL).last).to_be_visible(timeout=5_000)

        with page.expect_request(
            lambda r: "/api/doc-chat/stream" in r.url and "fresh=1" in r.url, timeout=20_000
        ):
            page.locator(CLEAR_BTN_SEL).first.click()

        expect(page.locator(USER_BUBBLE_SEL)).to_have_count(0, timeout=5_000)
        expect(page.locator(ASSISTANT_BUBBLE_SEL)).to_have_count(0, timeout=5_000)
        expect(page.locator(DOC_INPUT_SEL)).to_be_enabled(timeout=20_000)
        expect(page.locator(ERROR_BANNER_SEL)).to_have_count(0)


class TestDocChatNoteSaving:
    """Save-as-note flow on a completed reply."""

    def test_save_reply_as_note(self, doc_chat: DocChat):
        dc = doc_chat
        page = dc.page
        # 存笔记会真的写库(删文档不会级联删笔记),这里只验证前端接线,拦掉 POST。
        # 注意必须放行 GET: /notes 的读取走同一个 URL,返回非数组会让 DocDetail
        # 在 notes.map 上抛异常,整页白屏。
        def stub_note_post(route):
            if route.request.method == "POST":
                route.fulfill(status=200, content_type="application/json",
                              body='{"id":1,"content":"stubbed"}')
            else:
                route.continue_()

        page.route("**/api/documents/*/notes", stub_note_post)

        send_doc_message(dc, "Reply with exactly one word: pong")
        wait_reply(dc)

        # 一次回合可能产生多个 assistant 气泡(tool_use 和正文是分开的消息)。
        # 保存后那个气泡的 Save 按钮会被换成 Saved 标记,所以不能拿「含有 Save
        # 按钮」当气泡的稳定身份,改成数按钮数量 + 数 Saved 标记。
        save_buttons = page.locator(SAVE_NOTE_BTN_SEL)
        before = save_buttons.count()
        assert before > 0, "completed reply has no Save button"
        save_buttons.last.click()
        modal = page.locator(NOTE_MODAL_SEL)
        expect(modal.locator("h3", has_text="Save as Note")).to_be_visible(timeout=5_000)
        textarea = modal.locator("textarea")
        expect(textarea).to_be_visible()
        assert textarea.input_value().strip(), "note modal was not prefilled with the reply"

        with page.expect_request(
            lambda r: r.method == "POST" and r.url.endswith("/notes"), timeout=10_000
        ) as note_req:
            modal.locator("button:text-is('Save')").click()
        assert json.loads(note_req.value.post_data or "{}").get("content", "").strip(), \
            "note posted with empty content"

        expect(modal).to_have_count(0, timeout=5_000)
        expect(page.locator("span.text-green-600", has_text="Saved")).to_have_count(1, timeout=5_000)
        expect(save_buttons).to_have_count(before - 1, timeout=5_000)
