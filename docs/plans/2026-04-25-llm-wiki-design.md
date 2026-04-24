# LLM Wiki 个人知识库 — 设计文档

日期: 2026-04-25

## 核心理念

采用 Karpathy LLM Wiki 模式：LLM 增量构建和维护一个持久化的结构化 wiki（markdown 文件集合），而非每次查询时从原始文档碎片化检索（RAG）。

**关键区别：** Wiki 是持久化、持续生长的知识产物。交叉引用已存在，矛盾已被标记，综合已反映所有已读内容。每添加一个源、每问一个问题，Wiki 都变得更丰富。

## 架构总览

```
┌─────────────────────────────────────────────┐
│                 Go Binary (单文件部署)         │
│                                               │
│  ┌──────────┐  ┌──────────┐  ┌─────────────┐ │
│  │ PDF 解析  │  │ RSS 抓取 │  │ Web 裁剪     │ │
│  └────┬─────┘  └────┬─────┘  └──────┬──────┘ │
│       │              │               │        │
│       ▼              ▼               ▼        │
│  ┌──────────────────────────────────────┐    │
│  │        raw/  (原始文件，只读)          │    │
│  └──────────────┬───────────────────────┘    │
│                 │ LLM 读取，不修改             │
│                 ▼                             │
│  ┌──────────────────────────────────────┐    │
│  │        wiki/ (LLM 维护的 markdown)    │    │
│  └──────────────┬───────────────────────┘    │
│                 │                             │
│  ┌──────────────┴───────────────────────┐    │
│  │  Claude CLI (os/exec)                │    │
│  │  读取 raw/ → 写 wiki/ → Lint → 回答  │    │
│  └──────────────────────────────────────┘    │
│                                               │
│  SQLite: 元数据、对话记录、标签、状态           │
└─────────────────────────────────────────────┘
```

### 与用户交互方式

- **React UI** (浏览器) — 管理内容：导入/编辑/审核/浏览
- **Claude CLI** (os/exec) — LLM 引擎：Go 通过 `claude -p` 或 `claude --stream-json` 调用
- **不调用 LLM API**，直接通过 claude 进程完成任务

## 技术栈

| 层 | 技术 |
|----|------|
| 前端 | React + TypeScript |
| 后端 | Go, Echo 框架, GORM, SQLite |
| LLM | Claude CLI (os/exec) |
| 部署 | 单 Go 二进制文件（内嵌前端静态资源） |

## 目录结构

```
~/.llm-knowledge/
├── raw/                    # 原始文件，只读
│   ├── papers/             # PDF 论文
│   │   └── {name}/
│   │       ├── paper.pdf
│   │       ├── paper.md    # 提取全文
│   │       ├── assets/     # 导出的图表
│   │       └── meta.json
│   ├── articles/           # 网页裁剪 (markdown + assets/)
│   │   └── {name}/
│   │       ├── article.md
│   │       ├── assets/
│   │       └── meta.json
│   └── feeds/              # RSS 抓取 (markdown)
│       └── {name}/
│           ├── feed.md
│           └── meta.json
├── wiki/                   # LLM 维护的 markdown
│   ├── index.md            # 全库目录
│   ├── entities/           # 实体页（人物、方法、模型）
│   ├── topics/             # 主题页（概念、综述）
│   ├── sources/            # 源文档页（每篇论文/文章一页）
│   └── log.md              # 操作日志（按时间线追加）
├── data/
│   └── knowledge.db        # SQLite
└── schema.md               # Wiki 约定（LLM 行为规范）
```

## 数据模型 (SQLite)

```
documents
├─ id, title, source_type(pdf/rss/web/manual)
├─ raw_path (raw/ 下路径), wiki_path (wiki/sources/ 下路径)
├─ language, status(inbox/published/archived)
├─ metadata JSON (作者/期刊/年份/摘要/关键词)
├─ created_at, updated_at

tags
├─ id, name, color, created_at

document_tags (多对多)
├─ document_id, tag_id

conversations
├─ id, title, created_at, updated_at

conversation_messages
├─ id, conversation_id, role(user/assistant/system)
├─ content, context_doc_ids (JSON), created_at
```

`wiki/` 和 `raw/` 下的 markdown 文件是知识主体，SQLite 仅存元数据和对话记录。

## 图片处理

- **PDF 图片**：解析时导出内嵌图到 `assets/`，markdown 用相对路径引用
- **网页图片**：裁剪时下载到 `assets/`，替换为相对路径
- **Claude 读取**：markdown 内联图片可一并读取；量大时分批处理

## 核心流程

### Ingest（入库）

```
PDF上传
  → 提取全文 + 导出图片 (ledongthuc/pdf)
  → 存入 raw/papers/{name}/
  → claude 读取 paper.md
  → 写 wiki/sources/{name}.md (摘要/方法/关键发现)
  → 更新 wiki/index.md
  → 更新 wiki/entities/ wiki/topics/ 相关页面
  → 追加 wiki/log.md
  → 提取关键词 → SQLite tags
  → 入收件箱 (status: inbox)
  → React UI 审核 → published
```

### Query（问答）

```
用户提问 (POST /api/conversations/{id}/messages)
  → 构建上下文 (system prompt + 历史对话)
  → exec: claude --stream-json
     stdin: 引导读取 wiki/index.md → 定位页面 → 读具体页面
  → stdout: JSON stream events 逐行解析
  → SSE 推送前端 (text_delta, tool_use, tool_result)
  → 保存消息到 SQLite
  → 用户可选: 回答沉淀为 wiki 新页面
```

### Lint（健康检查）

```
定期 claude 扫描 wiki/
  → 检查页面矛盾
  → 检查过时论断
  → 检查孤儿页面
  → 建议新搜索方向
```

### 翻译

```
PDF(en) → claude 流式翻译 → 中文全文存入 paper_zh.md
  → 实体/主题页同步更新中文版本
```

## React UI 页面

| 页面 | 功能 |
|------|------|
| 收件箱 | 待审核列表 → 详情 → 确认入库/编辑/删除 |
| 文档详情 | 左侧 md 渲染，右侧 meta/标签/状态 |
| Wiki 浏览 | 渲染 markdown，支持 [[双向链接]] 跳转 |
| 对话界面 | SSE 流式输出，tool_use 状态条，引用链接 |
| 导入 | 拖拽 PDF / 粘贴 URL / RSS 源管理 |

设计风格：极简白色 + Inter 字体 + 暗色模式

## MVP 范围

1. PDF 论文解析 + 文本提取
2. Ingest 流程（提取 → Wiki 页面 → 收件箱 → 审核）
3. 基础 Wiki 浏览（markdown 渲染 + [[链接]]）
4. 对话问答（SSE 流式，claude --stream-json）
5. 翻译功能
6. 单二进制编译部署
