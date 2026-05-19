# 移动端响应式适配设计

**日期**: 2026-05-19
**作者**: 丁静 + Claude
**状态**: 待审阅

## 背景与目标

当前前端（React 19 + Vite + Tailwind v4 + react-router v7）是 desktop-first，整体使用 `flex h-screen` + 固定 `w-64` Sidebar 布局，未做任何响应式断点处理。希望让项目能在手机屏幕上使用，**面向只读消费场景**：浏览 Inbox / 看文档 / 与文档对话 / 看 Wiki / 看对话历史。

写入侧（导入文档、批量管理、Tag 编辑、笔记从 0 写、各种高级配置）仍然只在桌面进行。

## 设计原则

1. **桌面零改动**：现有桌面布局完全不变，桌面 e2e 必须全绿。
2. **响应式 Web，同一份代码**：用 Tailwind 断点切换；不做独立移动工程、不做独立路由前缀（如 `/m/...`）、不做 user-agent 判断。
3. **复用 > 重写**：桌面已有的业务组件（[DocDetail](../../../frontend/src/components/DocDetail.tsx)、[DocumentChatPanel](../../../frontend/src/components/DocumentChatPanel.tsx)、[ChatView](../../../frontend/src/components/ChatView.tsx) 等）只在外层套响应式分支或调用方式，业务逻辑不重写。
4. **后端零改动**：所有移动能力基于现有 API。
5. **YAGNI**：不引入新依赖（不用 `react-responsive`、不用专门的 mobile UI 库）；抽屉、Bottom Sheet、Tab Bar 用 Tailwind + 少量自写 CSS 实现，与现有 [ConfirmDialog](../../../frontend/src/components/ConfirmDialog.tsx) 风格一致。

## 范围边界

### 移动端保留的能力

- 看文档（Markdown / Web / PDF）、看 Wiki（Index / Entities / Topics / Sources）、看对话历史
- 搜索、按 tag 筛选
- 与文档对话（新建会话、发消息）
- Inbox → Later / Archived 状态变更（属于阅读流的一部分）
- 翻译入口：触发 PDF / Markdown 翻译，查看翻译结果
- 在文档对话里把 AI 回复 **save 为笔记**（固化对话产物，textarea 预填，零打字门槛）
- 修改密码 / 登出 / 切换语言

### 移动端隐藏的能力

- 删除文档 / 修改标题/内容 / 重新生成摘要 / 发布
- DocDetail 内嵌笔记区的「新增笔记」「删除笔记」「Push to Wiki」
- Tag 增 / 删 / 改 / 颜色编辑（仅保留只读列表）
- Import（订阅 RSS / 上传 PDF / 抓取 URL）
- Settings 高级配置（API key、模型配置、翻译开关、订阅源管理等）

### 不在移动端出现的页面

- `/import` 路由保留，但手机访问时渲染提示页：「导入功能仅桌面端可用，请回到 Inbox」+ 一个返回按钮。

## §1 总体策略 & 断点

- **断点**：Tailwind `md` (768px)。`< md` 走移动端布局，`≥ md` 走现有桌面布局。
- **实现手段优先级**：
  1. 优先 Tailwind 响应式 class（`hidden md:flex` / `flex md:hidden` / `md:w-64`）
  2. 仅在「桌面/手机两边渲染的 DOM 树差异较大、用 hidden class 会导致两边都挂载浪费」时，用 `useIsMobile()` hook 兜底（典型场景：App Layout 主框架）。
- **后端**：不改。
- **不引入新依赖**。

### 新增/改动文件

**新增**:
- `frontend/src/hooks/useIsMobile.tsx`
- `frontend/src/components/Layout/MobileHeader.tsx`
- `frontend/src/components/Layout/BottomTabBar.tsx`
- `frontend/src/components/Layout/MobileDrawer.tsx`
- `frontend/src/components/Layout/SidebarContent.tsx`（从 [Sidebar.tsx](../../../frontend/src/components/Sidebar.tsx) 抽取的共用部分）
- `frontend/src/components/DocChatBottomSheet.tsx`

**改动**:
- [App.tsx](../../../frontend/src/App.tsx)：Layout 加移动端壳分支
- [Sidebar.tsx](../../../frontend/src/components/Sidebar.tsx)：拆出 `SidebarContent`，自身保留桌面壳
- [DocDetail.tsx](../../../frontend/src/components/DocDetail.tsx)：手机分支 = 单栏正文 + FAB + 翻译按钮在 MobileHeader；隐藏写操作按钮；精简 viewMode
- [DocumentChatPanel.tsx](../../../frontend/src/components/DocumentChatPanel.tsx)：高度/宽度改为 `h-full w-full`；保存笔记的 modal 在手机分支变为「内容区切换为编辑态」（不叠 sheet）
- [ChatView.tsx](../../../frontend/src/components/ChatView.tsx)：手机分支用单栏路由化（`/chat` 列表，`/chat/:id` 详情）
- [WikiView.tsx](../../../frontend/src/components/WikiView.tsx)：手机端列表 + 顶部 segmented tabs
- [SettingsPage.tsx](../../../frontend/src/components/SettingsPage.tsx)：高级 section 用 `hidden md:block` 包起来
- [ImportView.tsx](../../../frontend/src/components/ImportView.tsx)：顶层手机分支 = 提示页
- [Inbox.tsx](../../../frontend/src/components/Inbox.tsx)、[DocumentsList.tsx](../../../frontend/src/components/DocumentsList.tsx)、[TagsView.tsx](../../../frontend/src/components/TagsView.tsx)：响应式 class 微调（单列、紧凑 padding）

## §2 移动端壳结构（MobileShell）

```
┌─────────────────────────────────────┐
│  MobileHeader (sticky top, h-12)    │ ← ☰ + 当前页标题 + 右侧 slot
├─────────────────────────────────────┤
│                                     │
│         <Outlet />  路由内容         │  flex-1 overflow-auto, pb-14
│                                     │
├─────────────────────────────────────┤
│  BottomTabBar (fixed bottom, h-14)  │ ← 5 tab：Inbox / 文档 / Wiki / 对话 / 更多
└─────────────────────────────────────┘

   ☰ 点开 ↓
   ┌──────────────┐
   │ Drawer (左侧) │ 复用 SidebarContent
   │   w-72        │
   └──────────────┘
```

### 2.1 `MobileHeader`

- 左：☰ 按钮（开关 Drawer）
- 中：动态标题（每个页面挂载时通过全局 store 或 context 设置自己的标题）
- 右：可选 slot，给页面塞操作按钮（如 DocDetail 的「翻译」、「💬」FAB 替代或并存）

### 2.2 `BottomTabBar`

- 5 个 tab 入口：
  1. **📥 Inbox** → `/`
  2. **📚 文档** → `/documents`（含 `?status=...` 子筛选）
  3. **📖 Wiki** → `/wiki`（含 `/wiki/entities` `/wiki/topics` `/wiki/sources` 子页）
  4. **💬 对话** → `/chat`
  5. **☰ 更多** → 打开 Drawer（不是单独路由）
- 当前 tab 高亮：基于 `useLocation().pathname` 前缀匹配
- **隐藏 BottomTabBar 的页面**：
  - `/documents/:id` 详情页（沉浸阅读）
  - `/login`、`/register`、`/change-password`

### 2.3 `MobileDrawer`

- 左侧滑入，宽度 `w-72`，背后半透明遮罩点击关闭
- 内容：复用从 Sidebar 抽出的 `SidebarContent`（搜索 + 全部入口 + 用户名 + 登出）
- Drawer 内 **隐藏 Import 入口**（写操作）
- 点击任意 Link 后自动关闭 Drawer
- 打开时给 `<body>` 加 `overflow-hidden` 防 iOS Safari 滚动穿透

### 2.4 `App.tsx` Layout 改造

```tsx
function Layout() {
  const isMobile = useIsMobile()
  const location = useLocation()
  const hideShellOnDocDetail = location.pathname.match(/^\/documents\/\d+$/)

  if (!isMobile) {
    return (
      <div className="flex h-screen bg-white">
        {!hideShellOnDocDetail && <Sidebar />}
        <main className="flex-1 overflow-auto"><Outlet /></main>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-[100dvh] bg-white">
      <MobileHeader />
      <main className="flex-1 overflow-auto pb-14"><Outlet /></main>
      {!hideShellOnDocDetail && <BottomTabBar />}
      <MobileDrawer />
    </div>
  )
}
```

> 这里用 `useIsMobile()` 而非 Tailwind class 切换，是因为桌面/手机两边的壳 DOM 结构差异较大（Sidebar vs Header+TabBar+Drawer），class hide 两边都挂载会浪费且让 useEffect 双跑。

### 2.5 兼容细节

- **Safe-area**：BottomTabBar 用 `pb-[env(safe-area-inset-bottom)]` 兼容刘海/底栏
- **键盘弹起**：高度统一用 `100dvh` 替代 `100vh`，避免输入框被键盘遮住或 BottomTabBar 被顶飞
- **滚动穿透**：抽屉/sheet 打开时给 `<body>` 加 `overflow-hidden`

## §3 核心页面的移动端形态

### 3.1 DocDetail（最复杂）

**桌面**：左右两栏（正文 + DocumentChatPanel），保持原样。

**手机**：

```
┌────────────────────────────────────┐
│ ← 文档标题       [中文/原文] [翻译] │  MobileHeader 自定义右侧 slot
├────────────────────────────────────┤
│                                    │
│   正文 / PDF / 翻译视图 (单栏)      │
│   viewMode 精简：                   │
│     - markdown 类：raw / translation │
│     - PDF 类：pdf / translation     │
│   隐藏：dual-pdf, bilingual, html, wiki │
│                                    │
│                          ┌────┐    │
│                          │ 💬 │    │ FAB → ChatBottomSheet
│                          └────┘    │
└────────────────────────────────────┘
```

**ChatBottomSheet (`DocChatBottomSheet.tsx`)**：

- 默认收起；点 FAB 弹出，初始高度 `70dvh`
- 顶部一根「拖动条」，可上滑全屏 / 下滑收起
- 内容直接渲染 [DocumentChatPanel](../../../frontend/src/components/DocumentChatPanel.tsx)；DocumentChatPanel 业务逻辑不动，只把硬编码的高度/宽度改为 `h-full w-full`
- 关闭：下滑超过 30%、点击遮罩、按浏览器返回键
- 输入框 sticky bottom + dvh

**翻译入口**（MobileHeader 右侧）：

- 已翻译 → 一键切换显示原文 / 中文（复用现有 viewMode 切换逻辑）
- 未翻译 → 触发对应翻译 API（PDF 用 `translatePDF`，Web/RSS/Blog 用 `translateMarkdown`），显示流式进度

**写操作隐藏**（仅手机）：

- 编辑标题、重新生成摘要、删除文档、发布按钮 → 不渲染
- 文档内嵌笔记区 → 改为只读列表（隐藏「添加笔记」「删除笔记」「Push to Wiki」按钮）

### 3.2 ChatBottomSheet 内的 Save Notes

PC 上每条 AI 回复旁有 `Save` 按钮，点击弹出居中 modal（textarea 预填消息内容，可微调）→ 调 `createDocNote(docId, content, msgId)`。

**手机端调整**：

- `Save` 按钮**保留**（这是固化对话产物，零打字门槛，不属于「从 0 写笔记」）
- 居中 modal 在手机分支改为「内容区切换为编辑态」：点 Save 后，ChatBottomSheet 的内容区从对话流切换为编辑界面（顶部「保存笔记」标题，下面 textarea + 取消/保存按钮）。完成或取消后切回对话流。
- 这样**不引入嵌套 sheet**，避免层级和键盘问题
- 桌面行为不变（仍是居中 modal）

### 3.3 ChatView

**桌面**：左侧会话列表 + 右侧对话流，保持原样。

**手机**：

- `/chat`（无 id）→ 满屏会话列表
- `/chat/:id` → 满屏对话流，左上角 ← 返回到 `/chat`
- 输入框 sticky bottom + dvh
- 隐藏写操作：删除会话、重命名会话

### 3.4 WikiView

**桌面**：master-detail 不变。

**手机**：

- `/wiki` → 索引列表
- `/wiki/entities` / `/wiki/topics` / `/wiki/sources` → 各子列表
- 顶部一行 segmented tabs：`索引 / 实体 / 主题 / 来源`（页面内 tab，与底部 TabBar 不冲突）
- 点击列表项进入详情（保持现有路由方式，不引入新路由）
- 全只读，无写操作

### 3.5 SettingsPage

手机分支只渲染 3 个 section：

1. 语言切换
2. 修改密码（路由跳到 `/change-password`）
3. 登出

其他 section 用 `<div className="hidden md:block">` 包起来，桌面照常显示。

### 3.6 列表页（Inbox / DocumentsList / TagsView）

最小改动：

- 卡片 `w-full`，padding 紧凑（`p-3` 替代 `p-6`）
- 多列网格 → 单列（`grid-cols-1 md:grid-cols-2 lg:grid-cols-3`）
- 卡片菜单：保留「移到 Later/Archived」（状态变更属阅读流），隐藏删除/编辑

### 3.7 ImportView

手机访问时顶层渲染：

```
┌────────────────────────────┐
│  📵                        │
│  导入功能仅桌面端可用       │
│  请使用电脑访问以导入文档    │
│  [返回 Inbox]              │
└────────────────────────────┘
```

桌面行为不变。

## §4 测试策略 + 实施拆分

### 4.1 测试策略

- **e2e 必加**（项目 CLAUDE.md 要求：页面改动必须加 e2e，用 `.venv` 和 `start.sh`）
- **viewport 模拟**：Playwright `iPhone 12` (390×844) 和 `iPad Mini` (768×1024)；768 边界恰好走桌面分支需断言
- **必测路径（最少 6 条）**：
  1. 手机访问 `/` → BottomTabBar 显示、桌面 Sidebar 不显示、点 ☰ 抽屉打开
  2. BottomTabBar 切换：4 个 tab 高亮和路由正确
  3. 进入 `/documents/:id` → BottomTabBar 自动隐藏、看到 FAB
  4. 点 FAB → ChatBottomSheet 弹出，发消息收回复
  5. ChatBottomSheet 内点 AI 回复 Save → 切到编辑态 → Save 成功
  6. 桌面 viewport 同样路由 → 桌面布局完整、无 mobile-only DOM
- **手动回归清单**（PR 前逐项过）：
  - iOS Safari 键盘弹起不顶飞 BottomTabBar
  - 抽屉打开时 body 不滚动
  - 刘海屏 safe-area 正确
- **桌面零回归**：现有桌面 e2e 全绿是必要条件

### 4.2 实施步骤（在一个 worktree 内分步完成，最后整体合主分支）

分支建议：`feat/mobile-responsive`

| # | 步骤 | 改动 | 验证 |
|---|---|---|---|
| 1 | 基础设施 | `useIsMobile`、`Layout/{MobileHeader,BottomTabBar,MobileDrawer,SidebarContent}`、`App.tsx` Layout 改造 | 手机 viewport 看到空壳 + tab 切换路由正确；桌面无变化 |
| 2 | 列表页响应式 | `Inbox` `DocumentsList` `TagsView` `WikiView` 加 `md:` class | 手机单列卡片；桌面无变化 |
| 3 | DocDetail 手机分支 | DocDetail 加手机分支、`DocChatBottomSheet`、DocumentChatPanel 高度/宽度调整、MobileHeader 翻译按钮、viewMode 精简 | 手机看文档+召唤聊天+触发翻译流畅；桌面无变化 |
| 4 | ChatBottomSheet 内 Save Note | DocumentChatPanel 保存 modal 在手机分支变为「内容区切换为编辑态」 | 手机 Save 流程不卡键盘；桌面行为不变 |
| 5 | ChatView 单栏路由化 | ChatView 手机分支：`/chat` 列表，`/chat/:id` 详情 | 手机两路由切换正确；桌面双栏不变 |
| 6 | SettingsPage 精简 + 写操作隐藏 + Import 提示页 | SettingsPage 高级 section 用 `hidden md:block`、各处卡片菜单隐藏写按钮、ImportView 顶层手机分支 | 手机端写操作不可见；桌面无变化 |
| 7 | e2e 测试 | 按 4.1 加测试用例 | 全绿 |

### 4.3 完成定义（DoD）

- [ ] 7 个步骤全部完成，PR review 通过
- [ ] 桌面 e2e 全绿（零回归）
- [ ] 移动 e2e 6 条全绿
- [ ] 真机自测 iPhone Safari + Android Chrome 通过
- [ ] 手动回归清单全过

## 不做的事（非目标）

- 不做独立的 mobile 工程或 H5 套壳 / 原生 App
- 不做 PWA（manifest / service worker / 离线）—— 留作后续可能的扩展
- 不做暗黑模式 / 主题切换
- 不在手机端开放任何写入入口（除已确认的：状态变更、对话、Save AI 回复为笔记、改密码、切语言）
- 不重写大组件的业务逻辑，只在外层套响应式分支
- 后端不改
