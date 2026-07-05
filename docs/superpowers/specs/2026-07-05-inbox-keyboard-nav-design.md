# Inbox 列表键盘导航 — 设计文档

**日期:** 2026-07-05
**范围:** `frontend/src/components/Inbox.tsx`(+ 一个 e2e 测试)

## 背景

Inbox 列表目前只有鼠标交互:`onMouseEnter/onMouseLeave` 维护 `hoveredDocId`,
并有一个 `d` 快捷键删除当前 hover 的文档(desktop only)。文档条目是 `<Link>`,
点击进入 `/documents/:id`。

DocDetail 已经用了 vim 键,但语义是**内容滚动**(`j`/`k` 上下滚、`g`/`G` 到顶/底、
`o` 打开来源、`d` 删除)。本需求给 Inbox **列表**加 vim 风格的**条目导航**,复用 `j`/`k` 的方向直觉。

## 目标

- 键盘在 Inbox 列表里上下移动"选中"光标(vim 风格)
- 选中项可用键盘直接操作:打开 / 删除 / 移到 Later
- 鼠标 hover 与键盘选中共用同一个高亮状态,互不打架
- 仅桌面端;移动端不启用

## 1. 状态模型

把现有的 `hoveredDocId` 升级为统一的 **`activeDocId`**(选中项),由鼠标和键盘共同驱动,
"谁最后操作谁生效":

- 鼠标 `onMouseEnter` → `setActiveDocId(doc.id)`(与现状一致)
- 按 `j`/`↓` / `k`/`↑` → 在 `documents` 数组里前后移动 `activeDocId`
- 首次按导航键且当前无选中:按下方向键选中**第一项**,按上方向键选中**最后一项**
- 边界不循环:第一项按上 / 最后一项按下 → 停在原地
- `onMouseLeave` **不再清空**选中(否则键盘光标会丢失)。选中项一直保留直到下一次
  鼠标 hover 或键盘移动。

**理由:** 现有 `d` 删除依赖 `hoveredDocId`,新键盘操作需作用于同一项;两套状态会冲突,
合并为单一 `activeDocId` 最干净。

## 2. 选中态可视化

现有高亮是纯 CSS `hover:` 伪类(`hover:shadow-md hover:border-blue-300`),
**只对鼠标生效**,键盘光标不会触发。改为 JS 状态驱动:

- 每个 `<Link>` 依据 `doc.id === activeDocId` 计算高亮 class(不再用 `hover:` 伪类)
- 高亮样式沿用现有视觉:阴影 + 蓝色边框 + 标题变蓝 + 箭头变蓝,保证鼠标观感不变
- 现有 `group-hover:` 的子元素联动(标题色、箭头色)改为基于选中态的条件 class

## 3. 按键处理

在现有 `d` 快捷键的 `useEffect` 基础上扩展为统一的 `keydown` handler,仍 **desktop only**
(`if (isMobile) return`):

| 键 | 行为 |
|---|---|
| `j` / `↓` | 选中下一项 |
| `k` / `↑` | 选中上一项 |
| `Enter` | `navigate()` 进入选中项详情页 |
| `d` | 删除选中项(复用现有确认弹窗与删除逻辑) |
| `l` | 选中项移到 Later(复用 `updateDocument(id, {status:'later'})`) |

细节:

- 沿用现有防护:焦点在 `input/textarea/select` 时不触发;带 `meta/ctrl/alt` 时不触发
- `j/k/↑/↓/Enter` 调 `e.preventDefault()`,避免方向键滚动页面、回车误触
- `l` 仅对 `status === 'inbox'` 的项生效(与列表里 Later 按钮的显示条件一致)
- 不做 DocDetail 那种"按住加速";列表导航一次一格更符合直觉

## 4. 滚动跟随

键盘把选中项移出可视区时需自动滚入。给每个条目挂 `ref`,`activeDocId` 变化后对选中项调用
`scrollIntoView({ block: 'nearest', behavior: 'smooth' })`。`nearest` 只在必要时滚动。

## 非目标 / YAGNI

- 不修改 DocDetail
- 不引入新依赖(不引第三方快捷键库)
- 不做移动端支持
- 不做循环选择(边界停止)
- 不做多选

## 测试

新增 e2e 测试(用项目 `.venv` 与 `start.sh`):

- 按 `j`/`k` 光标上下移动且高亮正确
- `Enter` 打开选中文档详情页
- 焦点在输入框时快捷键不触发

## 涉及文件

- `frontend/src/components/Inbox.tsx`(改造)
- `tests/e2e/`(新增键盘导航测试)
