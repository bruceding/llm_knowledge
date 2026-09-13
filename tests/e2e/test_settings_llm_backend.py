"""
E2E tests for the admin LLM backend switch (Plan 2 Task 9).

Task 9's gate asked for this to be checked by hand ("手工点开 Settings 确认开关可见/
普通用户不可见"). Both halves are automated here instead.

Auth
----
These tests need two *specific* roles at once (admin and normal user), which the
shared conftest `authenticated_page` cannot provide — it holds whoever logged in
last, and refreshing it needs a human to solve a captcha. So they load dedicated
storage-state files produced by:

    python3 tests/e2e/make_auth_state.py

That script reuses sessions the app already issued (read-only DB access, no
passwords). If a state file is missing, or the admin still has
must_change_password=1 (which makes PrivateRoute redirect everything to
/change-password), the tests skip with an actionable message rather than timing
out on a selector that can never appear.
"""

import json
import pathlib

import pytest
from playwright.sync_api import Browser, Page, expect

AUTH_DIR = pathlib.Path(__file__).parent / ".auth"
SETTINGS_URL = "http://localhost:9090/settings"

SECTION = "[data-testid='advanced-section-llm-backend']"
SELECT = "[data-testid='llm-backend-select']"
SAVE = "[data-testid='llm-backend-save']"
SUCCESS = "[data-testid='llm-backend-success']"
ERROR = "[data-testid='llm-backend-error']"

# 保存会触发服务端探测(spawn `pi --version` + 校验沙箱/配置),不是纯内存操作。
PROBE_BUDGET_MS = 25000
# 区块在 {isAdmin && ...} 里,而 isAdmin 来自一次异步 /api/settings。
RENDER_BUDGET_MS = 10000

BANNER_APPEARED = """() => document.querySelector('[data-testid="llm-backend-success"]')
                     || document.querySelector('[data-testid="llm-backend-error"]')"""


def _state_file(name: str) -> pathlib.Path:
    return AUTH_DIR / name


def _skip_unless_usable(name: str, require_admin: bool) -> pathlib.Path:
    path = _state_file(name)
    if not path.exists():
        pytest.skip(
            f"缺少 {path.name} —— 先跑 `python3 tests/e2e/make_auth_state.py`"
            f"(需要浏览器里已有一个 {'管理员' if require_admin else '普通用户'} 的有效登录态)"
        )
    if require_admin:
        stored = json.loads(path.read_text())
        auth = next(
            (i["value"] for o in stored["origins"] for i in o["localStorage"]
             if i["name"] == "auth-storage"),
            "{}",
        )
        if json.loads(auth).get("state", {}).get("mustChangePassword"):
            pytest.skip(
                "admin 的 must_change_password 仍为 1,前端 PrivateRoute 会把所有页面"
                "重定向到 /change-password,到不了 Settings。请先在登录页修改 admin 密码,"
                "然后重跑 `python3 tests/e2e/make_auth_state.py`。"
            )
    return path


def _open(browser: Browser, context_args: dict, state: pathlib.Path) -> Page:
    context = browser.new_context(**{**context_args, "storage_state": str(state)})
    page = context.new_page()
    page.set_default_timeout(RENDER_BUDGET_MS)
    return page


def read_backend(page: Page) -> str:
    """用浏览器自己的会话直接问服务端,拿到 llmBackend 的**真实**值。"""
    return page.evaluate(
        """async () => {
            const token = localStorage.getItem('token');
            const r = await fetch('/api/admin/settings', {
                headers: token ? { 'Authorization': 'Bearer ' + token } : {},
            });
            if (!r.ok) throw new Error('GET /api/admin/settings -> ' + r.status);
            return (await r.json()).llmBackend;
        }"""
    )


@pytest.fixture
def admin_page(browser: Browser, browser_context_args):
    state = _skip_unless_usable("state-admin.json", require_admin=True)
    page = _open(browser, browser_context_args, state)
    page.goto(SETTINGS_URL)
    page.wait_for_load_state("networkidle")
    yield page
    page.context.close()


@pytest.fixture
def user_page(browser: Browser, browser_context_args):
    state = _skip_unless_usable("state-nonadmin.json", require_admin=False)
    page = _open(browser, browser_context_args, state)
    page.goto(SETTINGS_URL)
    page.wait_for_load_state("networkidle")
    yield page
    page.context.close()


class TestLLMBackendVisibility:
    def test_admin_sees_switch(self, admin_page: Page):
        """管理员能看到开关,且下拉框恰好是 claude/pi 两个取值。

        只断言"可见"不够:一个空的 select 同样可见。取值集合就是前后端的契约
        (后端对其它取值一律 400),所以在这里钉住。
        """
        page = admin_page
        expect(page.locator(SECTION)).to_be_visible(timeout=RENDER_BUDGET_MS)
        expect(page.locator(SELECT)).to_be_visible()

        values = page.locator(f"{SELECT} option").evaluate_all("els => els.map(e => e.value)")
        assert values == ["claude", "pi"], f"下拉框取值 = {values}, want ['claude', 'pi']"

    def test_normal_user_does_not_see_switch(self, user_page: Page):
        """普通用户看不到这个区块 —— 计划 Task 9 闸门的另一半。

        断言方式要能区分「没渲染」与「渲染了但被 CSS 藏起来」:前者 count 为 0,
        后者 count 为 1 而不可见。两者都算通过的话,一个把 isAdmin 判断写错的实现
        (区块照常渲染、只靠样式隐藏)就会蒙混过关 —— 那种实现下,区块里的数据仍会
        进 DOM,而且任何人改一行 CSS 就能看到管理员开关。所以这里要求 count == 0。

        同时用管理员区块做对照:同一个页面、同一时刻,普通用户既看不到后端开关,
        也看不到翻译设置(两者共用 isAdmin 判断)。若哪天 isAdmin 恒为真,这个
        用例会立刻红,而不是等到有人越权改了后端才发现。
        """
        page = user_page
        # 先确认页面确实渲染出来了(否则"看不到"可能只是白屏)
        page.wait_for_load_state("networkidle")
        assert page.url.rstrip("/").endswith("/settings"), (
            f"普通用户没能停在 /settings(实际 {page.url}),用例前提不成立"
        )

        expect(page.locator(SECTION)).to_have_count(0)
        expect(page.locator("[data-testid='advanced-section-translation']")).to_have_count(0)

        # 顺带确认服务端也拒绝:光靠前端不渲染不算访问控制
        status = page.evaluate(
            """async () => {
                const t = localStorage.getItem('token');
                const r = await fetch('/api/admin/settings', {
                    headers: t ? { 'Authorization': 'Bearer ' + t } : {},
                });
                return r.status;
            }"""
        )
        assert status == 403, f"普通用户 GET /api/admin/settings 得到 {status}, want 403"


class TestLLMBackendSwitch:
    def test_select_reflects_server_state(self, admin_page: Page):
        """下拉框显示的是服务端真实值,而不是写死的默认 claude。

        没有这条,一个 value 恒为 'claude' 的 select 也能通过所有可见性断言 ——
        管理员会看到"当前是 claude",而实际生效的可能是 pi。
        """
        page = admin_page
        expect(page.locator(SELECT)).to_be_visible(timeout=RENDER_BUDGET_MS)
        assert page.locator(SELECT).input_value() == read_backend(page)

    def test_switch_to_pi_roundtrip(self, admin_page: Page):
        """切到 pi 并保存:UI 的结论必须与服务端真实状态一致。

        刻意**不**预设 pi 在本机可用:探测通过就该出现绿条且服务端变成 pi,探测不通过
        就该出现红条且服务端保持原值。两种都合法;不合法的是「UI 说成功了但服务端没变」
        或「UI 什么也没说」。所以这条对"这台机器装没装 pi"是确定性的,同时真的把
        Task 7 的探测链路端到端跑了一遍。
        """
        page = admin_page
        expect(page.locator(SELECT)).to_be_visible(timeout=RENDER_BUDGET_MS)
        original = read_backend(page)
        try:
            page.locator(SELECT).select_option("pi")
            page.locator(SAVE).click()
            page.wait_for_function(BANNER_APPEARED, timeout=PROBE_BUDGET_MS)
            now = read_backend(page)

            if now == "pi":
                expect(page.locator(SUCCESS)).to_be_visible()
                expect(page.locator(ERROR)).to_have_count(0)
            else:
                # 探测拦下了切换。错误文案必须给出**可操作的具体原因**,而不是 api.ts
                # 原先那句笼统的 "Failed to update global settings" —— 那条路径已改成
                # 透出服务端的 body.error,这里守住它不退化。
                expect(page.locator(ERROR)).to_be_visible()
                expect(page.locator(SUCCESS)).to_have_count(0)
                text = page.locator(ERROR).inner_text()
                assert "Failed to update global settings" not in text, (
                    "错误文案退回了笼统消息,服务端的探测诊断被吞掉了: " + text
                )
                assert "pi" in text, f"错误文案没有说明是哪个后端/什么原因: {text}"
                print(f"\n[探测拦下切换,服务端原因] {text}")

            # 保存失败时下拉框**保留**用户刚选的值(实测行为)。这是常规表单语义:
            # 让人看到自己选了什么、为什么失败,修好配置后直接重试而不必重选。
            # 所以这里不断言「回弹到服务端值」—— 真正防误导的保证是下面这条:
            # **重新加载后必须显示服务端真实值**,否则管理员会看着一个写着 pi 的
            # 下拉框,以为 pi 已经生效了。
            page.reload()
            page.wait_for_load_state("networkidle")
            page.wait_for_selector(SELECT, timeout=RENDER_BUDGET_MS)
            assert page.locator(SELECT).input_value() == now, (
                f"重新加载后下拉框显示 {page.locator(SELECT).input_value()!r},"
                f"而服务端真实值是 {now!r} —— 管理员会误以为切换已生效"
            )
        finally:
            # 必须还原:否则服务会留在 pi 上,后续聊天 e2e 就变成在测另一个后端,
            # "claude 路径行为不变"的回归基线也就没了。
            if read_backend(page) != original:
                page.locator(SELECT).select_option(original)
                page.locator(SAVE).click()
                page.wait_for_function(BANNER_APPEARED, timeout=PROBE_BUDGET_MS)
            assert read_backend(page) == original, f"未能把后端还原成 {original}"

    def test_switch_back_to_claude_never_probes(self, admin_page: Page):
        """切回 claude 一定成功 —— 它是 pi 坏掉时的逃生门。

        Task 7 的 Go 测试已用 marker 文件证明"切 claude 不触发探测";这里从 UI 侧
        再钉一次结论:即便本机 pi 完全不可用,切回 claude 也必须能保存成功。
        否则管理员会被锁死在一个坏掉的后端上。
        """
        page = admin_page
        expect(page.locator(SELECT)).to_be_visible(timeout=RENDER_BUDGET_MS)
        page.locator(SELECT).select_option("claude")
        page.locator(SAVE).click()
        page.wait_for_selector(SUCCESS, timeout=PROBE_BUDGET_MS)
        expect(page.locator(SUCCESS)).to_be_visible()
        assert read_backend(page) == "claude"
