# Inbox 列表键盘导航 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 Inbox 列表加 vim 风格键盘导航(j/k/↑/↓ 移动选中,Enter 打开,d 删除,l 移到 Later),桌面端专用。

**Architecture:** 把现有 `hoveredDocId` 升级为统一的 `activeDocId`,由鼠标 hover 和键盘共同驱动。选中态从 CSS `:hover` 伪类改为 JS 状态驱动的条件 class。单一 `keydown` handler 处理导航与操作。选中项变化时 `scrollIntoView` 跟随。

**Tech Stack:** React + TypeScript(react-router-dom `Link`/`useNavigate`)、Tailwind、Playwright e2e(pytest)。

**改动文件:**
- Modify: `frontend/src/components/Inbox.tsx` — 全部前端逻辑
- Create: `tests/e2e/test_inbox_keyboard_nav.py` — e2e 测试

---

## Task 1: 状态改造 + 选中态可视化

把 `hoveredDocId` 换成 `activeDocId`,加 `useNavigate` 和条目 ref,选中态改 JS 驱动。此任务先不接键盘,只保证鼠标 hover 行为与现状一致、并暴露 `data-active` 供后续测试。

**Files:**
- Modify: `frontend/src/components/Inbox.tsx`

- [ ] **Step 1: 改 imports**

`frontend/src/components/Inbox.tsx:1-2` 改为:

```tsx
import { useState, useEffect, useRef } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
```

- [ ] **Step 2: 组件内加 navigate、重命名 state、加 refs**

`frontend/src/components/Inbox.tsx` 组件顶部,把 `const location = useLocation()` 之后补一行,并把 `hoveredDocId` 那行(约第 20 行)替换:

```tsx
  const location = useLocation()
  const navigate = useNavigate()
```

把:

```tsx
  const [hoveredDocId, setHoveredDocId] = useState<number | null>(null)
```

替换为:

```tsx
  const [activeDocId, setActiveDocId] = useState<number | null>(null)
  const itemRefs = useRef<Map<number, HTMLElement>>(new Map())
```

- [ ] **Step 3: 删除旧的 `d`-only 键盘 effect**

删除现有的这段(约 `frontend/src/components/Inbox.tsx:32-48`,以 `// Keyboard shortcut: 'd' to delete hovered document` 开头到其 `}, [hoveredDocId, t, confirm, isMobile])` 结束)。Task 2 会用新的统一 handler 取代它。

- [ ] **Step 4: 加选中项滚动跟随 effect**

在 `loadDocuments` 定义之前插入:

```tsx
  // Scroll the active item into view when selection changes (keyboard nav)
  useEffect(() => {
    if (activeDocId === null) return
    itemRefs.current.get(activeDocId)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [activeDocId])
```

- [ ] **Step 5: 改 `<Link>` — ref、data 属性、选中态 class、去掉 onMouseLeave**

把 `documents.map` 里的 `<Link ...>` 开标签(约 `frontend/src/components/Inbox.tsx:195-201`)替换为:

```tsx
            <Link
              key={doc.id}
              ref={(el) => {
                if (el) itemRefs.current.set(doc.id, el)
                else itemRefs.current.delete(doc.id)
              }}
              data-testid={`inbox-item-${doc.id}`}
              data-active={doc.id === activeDocId ? 'true' : undefined}
              to={`/documents/${doc.id}`}
              onMouseEnter={() => setActiveDocId(doc.id)}
              className={`block bg-white border rounded-lg p-4 transition-all cursor-pointer group ${
                doc.id === activeDocId ? 'shadow-md border-blue-300' : 'border-gray-200 hover:shadow-md hover:border-blue-300'
              }`}
            >
```

- [ ] **Step 6: 标题颜色改选中态驱动**

把 `frontend/src/components/Inbox.tsx` 里标题的 `<h3>`(约第 205 行,含 `group-hover:text-blue-600`)替换为:

```tsx
                  <h3 className={`font-medium transition-colors line-clamp-1 ${
                    doc.id === activeDocId ? 'text-blue-600' : 'text-gray-800 group-hover:text-blue-600'
                  }`}>
                    {doc.title}
                  </h3>
```

- [ ] **Step 7: 箭头颜色改选中态驱动**

把底部箭头 `<svg>`(约第 275-281 行,含 `group-hover:text-blue-500`)的 className 替换为:

```tsx
                <svg
                  className={`w-4 h-4 transition-colors ${doc.id === activeDocId ? 'text-blue-500' : 'group-hover:text-blue-500'}`}
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                >
```

- [ ] **Step 8: 编译检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无报错(尤其无 unused `setHoveredDocId` / `hoveredDocId` 残留、无 unused import)。

- [ ] **Step 9: Commit**

```bash
git add frontend/src/components/Inbox.tsx
git commit -m "refactor(inbox): 统一 hovered/selected 为 activeDocId,选中态改 JS 驱动"
```

---

## Task 2: 键盘导航与操作 handler

加统一 `keydown` handler:j/↓ 下移、k/↑ 上移、Enter 打开、d 删除、l 移到 Later。

**Files:**
- Modify: `frontend/src/components/Inbox.tsx`

- [ ] **Step 1: 在滚动跟随 effect 之后插入键盘 handler**

在 Task 1 Step 4 加的 scroll effect 之后插入:

```tsx
  // Keyboard navigation & actions (desktop only)
  useEffect(() => {
    if (isMobile) return
    const handleKeyDown = async (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLSelectElement) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (documents.length === 0) return

      const currentIndex = documents.findIndex((d) => d.id === activeDocId)

      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault()
        const next = currentIndex < 0 ? 0 : Math.min(currentIndex + 1, documents.length - 1)
        setActiveDocId(documents[next].id)
        return
      }
      if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault()
        const prev = currentIndex < 0 ? documents.length - 1 : Math.max(currentIndex - 1, 0)
        setActiveDocId(documents[prev].id)
        return
      }

      if (activeDocId === null) return
      const activeDoc = documents.find((d) => d.id === activeDocId)
      if (!activeDoc) return

      if (e.key === 'Enter') {
        e.preventDefault()
        navigate(`/documents/${activeDocId}`)
        return
      }
      if (e.key === 'd') {
        e.preventDefault()
        const confirmed = await confirm({
          title: t('common.delete'),
          message: t('docDetail.deleteConfirm'),
        })
        if (!confirmed) return
        deleteDocument(activeDocId).then(() => loadDocuments())
        return
      }
      if (e.key === 'l') {
        if (activeDoc.status !== 'inbox') return
        e.preventDefault()
        updateDocument(activeDocId, { status: 'later' })
          .then(() => loadDocuments())
          .catch((err) => setError(err instanceof Error ? err.message : 'Failed to move to later'))
        return
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [documents, activeDocId, isMobile, t, confirm, navigate])
```

- [ ] **Step 2: 编译检查**

Run: `cd frontend && npx tsc --noEmit`
Expected: 无报错。

- [ ] **Step 3: 手动冒烟(可选,快速)**

Run: `cd frontend && npm run build`
Expected: build 成功。

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/Inbox.tsx
git commit -m "feat(inbox): j/k/↑/↓ 导航,Enter 打开,d 删除,l 移到 Later"
```

---

## Task 3: E2E 测试

用 Playwright 验证键盘导航、Enter 打开、输入框内不触发。参照 `tests/e2e/test_doc_detail_shortcuts.py` 的 seed/cleanup 模式(web-clip 生成的文档进入 Inbox,status=inbox)。

**Files:**
- Create: `tests/e2e/test_inbox_keyboard_nav.py`

- [ ] **Step 1: 写测试文件**

Create `tests/e2e/test_inbox_keyboard_nav.py`:

```python
"""
E2E tests for Inbox list keyboard navigation (vim-like j/k + arrows).

Seeds three documents via the web-clip API (they land in the Inbox with
status=inbox), navigates to the Inbox at "/", and cleans up afterwards.
"""

import time

import pytest
from playwright.sync_api import Page


SEED_URL_PREFIX = "https://example.test/e2e-inbox-nav/"


def _seed_document(page: Page, title: str) -> int:
    seed_url = f"{SEED_URL_PREFIX}{title}-{int(time.time() * 1000)}"
    html = f"<html><head><title>{title}</title></head><body><p>{title} body</p></body></html>"
    result = page.evaluate(
        """async ({url, html, title}) => {
            const t = localStorage.getItem('token');
            const r = await fetch('/api/raw/web-clip', {
                method: 'POST',
                headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + t},
                body: JSON.stringify({url, html, title}),
            });
            const body = await r.json();
            return {status: r.status, body};
        }""",
        {"url": seed_url, "html": html, "title": title},
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
def inbox_with_docs(authenticated_page: Page):
    """Seed 3 docs, open the Inbox, clean up afterwards. Yields (page, [ids])."""
    page = authenticated_page
    ids = [_seed_document(page, f"NavDoc{i}") for i in range(3)]
    try:
        page.goto("http://localhost:9090/")
        page.wait_for_load_state("networkidle")
        for doc_id in ids:
            page.wait_for_selector(f"[data-testid='inbox-item-{doc_id}']", timeout=5000)
        page.wait_for_timeout(300)
        yield page, ids
    finally:
        for doc_id in ids:
            _delete_document(page, doc_id)


def _active_id(page: Page):
    el = page.query_selector("[data-testid^='inbox-item-'][data-active='true']")
    if el is None:
        return None
    testid = el.get_attribute("data-testid")
    return int(testid.rsplit("-", 1)[1])


class TestInboxKeyboardNav:
    def test_j_selects_first_then_moves_down(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        assert _active_id(page) is None

        page.keyboard.press("j")
        page.wait_for_timeout(150)
        first = _active_id(page)
        assert first in ids, f"j did not select an inbox item, got {first}"

        page.keyboard.press("j")
        page.wait_for_timeout(150)
        second = _active_id(page)
        assert second in ids and second != first, f"second j did not move, first={first} second={second}"

    def test_k_moves_up(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("j")
        page.wait_for_timeout(100)
        page.keyboard.press("j")
        page.wait_for_timeout(100)
        down_id = _active_id(page)
        page.keyboard.press("k")
        page.wait_for_timeout(150)
        up_id = _active_id(page)
        assert up_id != down_id, f"k did not move selection up, down={down_id} up={up_id}"

    def test_arrow_keys_work(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("ArrowDown")
        page.wait_for_timeout(150)
        assert _active_id(page) in ids, "ArrowDown did not select an item"

    def test_enter_opens_selected_doc(self, inbox_with_docs):
        page, ids = inbox_with_docs
        page.evaluate("() => document.body.focus()")
        page.keyboard.press("j")
        page.wait_for_timeout(150)
        selected = _active_id(page)
        page.keyboard.press("Enter")
        page.wait_for_url(f"http://localhost:9090/documents/{selected}", timeout=5000)
        assert page.url.endswith(f"/documents/{selected}")

    def test_keys_ignored_in_input(self, inbox_with_docs):
        page, ids = inbox_with_docs
        search = page.locator("input, textarea").first
        if not search.is_visible():
            pytest.skip("No input visible to test")
        search.click()
        page.keyboard.press("j")
        page.wait_for_timeout(150)
        assert _active_id(page) is None, "j selected an item while focus was in an input"
```

- [ ] **Step 2: 启动服务并跑测试**

Run(项目约定用 `.venv` 与 `start.sh`;若服务未起,先 `./start.sh` 起服务):

```bash
source .venv/bin/activate && pytest tests/e2e/test_inbox_keyboard_nav.py -v
```

Expected: 5 个测试全部 PASS。

- [ ] **Step 3: Commit**

```bash
git add tests/e2e/test_inbox_keyboard_nav.py
git commit -m "test(inbox): e2e 覆盖键盘导航/Enter 打开/输入框内不触发"
```

---

## 验证清单(全部完成后)

- [ ] `cd frontend && npx tsc --noEmit` 无报错
- [ ] `pytest tests/e2e/test_inbox_keyboard_nav.py -v` 全绿
- [ ] 手动:鼠标 hover 高亮与改动前观感一致
- [ ] 手动:j/k/↑/↓ 移动光标且选中项滚动跟随;Enter 打开;d 删除;l 移到 Later
