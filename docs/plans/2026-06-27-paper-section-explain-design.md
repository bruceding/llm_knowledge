# Paper Section Explain — Design

Companion to [2026-06-27-paper-section-explain-implementation.md](2026-06-27-paper-section-explain-implementation.md). This doc fixes the **interaction and frontend design**; the implementation plan references it.

> **Revision 2026-06-27 (LLM-based sectionization).** The original v1 split
> paper.md by `##`/`###` headings. Manual verification exposed this never
> works: `UploadPDF` produces paper.md via `pdftotext -layout` (two-column
> plain text, **no markdown headings**, plus algorithm pseudocode lines like
> `4 Initialize unassigned set U ← V` that look like headings). Local
> regex parsing is both too brittle (two-column merge, false positives) and
> has nothing to parse. **Sectionization is now one Claude call** that reads
> paper.md and returns `[{title, body}]`, cached to
> `sections/index.json` + per-section `<slug>.src.md`. Per-section explain
> then points Claude at the cached body file (not "locate by title in
> paper.md") — fixing the duplicate-title known limitation too. Validated
> end-to-end on doc 72 (`2502.18965v1`): 25 sections identified correctly,
> pseudocode steps excluded, cache hit on re-open, explain produces clean
> 讲解. First-open cost: one Claude call (~3min for a 15-page paper), cached
> after.

## 1. 目标与范围

**目标**：把论文从"只有 doc-chat 能问"变成"每章自动有讲解"。用户经常在 doc-chat 里挨个问"这章什么意思、那个算法怎么回事"——预生成章节讲解后，读完就懂，再就细节深挖才进 chat。

**v1 做**：
- **一次 Claude 调用切章节**（`Sectionize`）：读 paper.md（pdftotext 双栏纯文本）→ 返回 `[{title, body}]`，识别章节标题、跳过算法伪代码步骤、清洗双栏杂乱换行，缓存到 `sections/index.json` + `<slug>.src.md`
- 每章懒加载生成"讲解式"摘要（一段讲解 + 末尾「关键要点」），阻塞 `-p` 调用读 `<slug>.src.md`，落盘缓存
- PDF 文档在 DocDetail 显示「章节讲解」入口，首次打开自动触发 Sectionize
- 顶部复用 `doc.summary` 作主旨 banner
- 底部「对这篇论文提问」→ 切到现有 doc-chat tab


**v1 不做**（明确砍掉，留 v2）：
- ❌ 回原文锚点（paper.md ↔ PDF 页码无映射，用户看的是原始 PDF，硬做脆弱）
- ❌ 流式生成（先阻塞 loading，最简）
- ❌ 预生成全部章节（懒加载，点开才生成）
- ❌ doc-chat 注入章节上下文（追问时 Claude 照常 Read paper.md 全文）
- ❌ 移动端适配（v1 桌面独占，见 §7）

## 2. 入口

DocDetail 头部的视图切换行，现有按钮：`PDF` / `Wiki` / `Translation` / `Dual PDF`（按文档状态显示）。新增 **「章节讲解」** 按钮，**仅 PDF 文档显示**（`isPDF` 分支内）。

- 点击 → `viewMode = 'sections'`，主内容区切换为 `PaperSectionsView`
- 默认视图不变（PDF 文档默认仍是 `pdf`），用户主动点入
- 后端 `ListSections` 对非 PDF 文档返回 400，作为第二道防线

## 3. 桌面布局（叠加式学习指南）

```
┌──────────────────────────────────────────────────────────┐
│ ←  PDF | 章节讲解 |                       [翻译PDF]      │  ← 头部视图切换行
├──────────┬───────────────────────────────────────────────┤
│          │  📄 主旨                                       │  ← summary banner（灰底）
│ 章节导航  │  doc.summary 复用，最多两行，溢出省略           │
│          ├───────────────────────────────────────────────┤
│ • Intro  │  ## Introduction                              │
│ • Method │  [生成讲解]                                    │  ← 未生成：按钮
│ • Exper  │                                               │
│ • Concl  │  ## Method                                    │
│          │  本章提出…（已生成讲解 markdown 渲染）          │  ← 已生成：内容
│          │  ## 关键要点                                   │
│          │  - 要点1                                      │
│          │  - 要点2                                      │
│          │                                               │
│          │  ## Experiments                               │
│          │  [生成讲解]                                    │
│          │                                               │
│          │               [💬 对这篇论文提问]              │  ← 底部追问入口
└──────────┴───────────────────────────────────────────────┘
   224px          flex-1 滚动区
```

- **左导航（224px）**：纯目录。点击 → 右区平滑滚动到对应章节。已生成讲解的章节文字变蓝，未生成的灰色——一眼看出进度。
- **右主区**：从上到下依次：summary banner → 各章节块（标题 + 内容/按钮）→ 底部追问按钮。
- **每章节块**：未生成时只放一个「生成讲解」按钮；生成后整段 markdown 渲染（讲解段 + `## 关键要点` 列表）。缓存命中则直接渲染，不重新生成。
- **右元数据面板**（现有）不动，仍可切 Metadata / Chat / Notes。读讲解时若嫌挤可隐藏面板。

## 4. 交互流程与状态

**主流程**：
1. 打开 PDF 文档 → 点「章节讲解」
2. 调 `GET /documents/:id/sections` → 渲染左导航 + 右区各章节块
3. 点某章「生成讲解」→ `POST /documents/:id/sections/:index/generate`（阻塞）→ 该章块原地填入讲解
4. 重复生成关心的章节，整页逐渐成为一篇学习指南
5. 想深挖 → 底部「对这篇论文提问」→ 切到 Chat tab（右面板），现有 doc-chat 继续工作

**状态机**（右主区整体 / 单章块）：

| 状态 | 表现 |
|---|---|
| 加载中 | 全屏 spinner |
| paper.md 不存在 | 空状态：`paper-sections-empty`，「尚未生成论文内容，请先提取」 |
| paper.md 存在但 0 章节 | 空状态：`paper-sections-empty`，「未识别到章节，可尝试 LLM 重新提取」 |
| 有章节、未生成 | 每章块显示「生成讲解」按钮 |
| 有章节、部分已生成 | 已生成渲染 markdown + 未生成按钮混合；左导航蓝/灰区分 |
| 生成中（某章） | 该章按钮变「生成中...」并 disabled；其它章按钮也 disabled（后端 `summarySem` 限流 1，同时只跑一个） |
| 生成失败 | 顶部红色错误条 + 该章按钮可重试 |
| 缓存命中 | 直接渲染，无 loading |

**并发说明**：后端 `GenerateSectionExplain` 复用 `summarySem`（cap 1），所以即使前端并发发起多个 generate，后端串行执行。前端把 `generatingIndex` 设为单值，期间所有生成按钮 disabled，与后端一致。

## 5. 与 PDF 视图 / doc-chat 的关系

- **PDF 视图**：保留。用户看的原始 PDF（`PDFViewer`）。「章节讲解」是并排的另一个视图模式，不替代 PDF。
- **doc-chat**：保留并复用，**全功能不回归**。`PaperSectionsView` 底部「对这篇论文提问」按钮调用 `onAskPaper` 回调，DocDetail 里执行 `setPanelHidden(false)` + `setMetadataTab('chat')`，把右面板切到 Chat tab。不新建 chat 通道、不注入章节上下文（v1）。
  - `DocumentChatPanel` 组件本身一行不改：流式问答、`--resume` 续连、Clear Chat、**Save（保存为 DocNote）**、Notes 列表、→ Wiki 推送 全部原样保留。
  - 「提问」按钮只是把焦点切到现有 Chat tab 并保证面板可见；save 流程仍走 `handleNoteSaved` → 重载 Notes，与现状一致。
  - 即：章节讲解是 PDF 视图旁的新视图，doc-chat 是独立的追问通道，两者并行不互斥。
- **wiki ingest**：不动。章节讲解与 wiki 的 sources/entities/topics 是正交两套产物，并存。

## 6. 空状态与边界

| 情况 | 处理 |
|---|---|
| 文档非 PDF | 后端 400；前端不显示「章节讲解」按钮 |
| `paper.md` 缺失（刚上传、未提取） | `paperMdExists:false` → 空状态文案引导先提取 |
| `paper.md` 来自 `UploadPDF`（pdftotext，无 `##` 标题） | 0 章节 → 空状态文案引导用「LLM 提取」重生成带标题层级的 paper.md |
| 章节标题重复 | slug 含 index 保证唯一；缓存不串。生成时靠 Claude 按"标题为 X"定位，重复标题可能选错——v1 接受此风险 |
| `paper.md` 被 LLM 重新提取后章节变了 | 旧缓存按 slug(含 title hash) 可能对不上新章节 → 用户重新生成即可；不做自动失效（v2） |
| Claude CLI 未配置 | 后端 503；前端错误条提示 |
| 章节正文过长 | 讲解靠 Claude Read paper.md 自行定位，不传全文进 prompt arg，无 arg 长度风险 |

## 7. 移动端

**v1 桌面独占**。移动端 DocDetail 头部没有视图切换行（移动用 FAB 进 chat），没有「章节讲解」入口。`effectiveViewMode` 对 `sections` 会回退到 `pdf`，不崩。移动适配（左导航改抽屉/折叠）留 v2。

## 8. 关键文案（i18n key → zh / en）

| key | zh | en |
|---|---|---|
| `paperSections.tab` | 章节讲解 | Sections |
| `paperSections.generate` | 生成讲解 | Generate |
| `paperSections.generating` | 生成中... | Generating... |
| `paperSections.noSections` | 未识别到章节。可尝试用「LLM 提取」重新生成带标题层级的论文内容后再来。 | No sections detected. Try re-extracting the paper with LLM extraction. |
| `paperSections.noPaperMd` | 尚未生成论文内容（paper.md）。请先在 PDF 视图旁的操作中提取论文。 | Paper content (paper.md) not generated yet. Please extract the paper first. |
| `paperSections.askPaper` | 对这篇论文提问 | Ask about this paper |

## 9. 组件接口

```
PaperSectionsView
  props:
    docId: number
    summary: string          // = document.summary，顶部 banner 用
    onAskPaper: () => void   // 切到 doc-chat tab
  内部状态: sections[], paperMdExists, loading, error, generatingIndex
  数据: fetchPaperSections(docId), generatePaperSection(docId, index)
```

`data-testid`（e2e 用）：`paper-sections-tab`（DocDetail 头部按钮）、`paper-sections-content`（右主区容器）、`paper-sections-empty`（空状态）。
