# LLM Knowledge

[English](#english) | [中文](#chinese)

A self-hosted knowledge management tool that ingests documents, extracts content via LLM, and provides intelligent Q&A — all in a single binary.

---

## English

LLM Knowledge is a personal knowledge base that helps you collect, understand, and query your documents. It ingests PDFs, web pages, and RSS feeds, uses Claude to extract and summarize content, and lets you chat with your documents through an SSE-powered conversational interface.

### Features

- **Multi-source ingestion** — Upload PDFs, clip web pages, or subscribe to RSS feeds
- **LLM-powered extraction** — Claude CLI extracts structured content, generates summaries, and translates between Chinese and English
- **Document chat** — Multi-turn SSE streaming Q&A with session management, powered by Claude
- **PDF translation** — Layout-preserving PDF translation via pdf2zh (OpenAI-compatible API)
- **Dual PDF viewer** — Scroll/scale-synced side-by-side comparison of original and translated PDFs
- **Bilingual UI** — Full i18n support for English and Chinese
- **Single binary** — Frontend embedded in Go binary, just download and run

### Prerequisites

- **Go** 1.25+
- **Node.js & npm** (for building frontend)
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — available in PATH
- **pdf2zh** (optional) — for PDF translation feature

### Quick Start

```bash
# Clone and start (default port 9999)
git clone https://github.com/bruceding/llm_knowledge.git
cd llm_knowledge
./start.sh

# Or build and run manually
make build
./llm-knowledge          # default port 3456

# Development mode with hot reload
make dev                 # backend :3456, frontend :5173
```

Data is stored in `~/.llm-knowledge/` (configurable via `DATA_DIR` env var).

### Configuration

| Env Variable | Default | Description |
|---|---|---|
| `PORT` | `3456` | Server port |
| `DATA_DIR` | `~/.llm-knowledge` | Data and database directory |
| `PDF2ZH_VENV_DIR` | `$DATA_DIR/.venv` | pdf2zh Python venv path |

### Tech Stack

- **Backend:** Go + Echo + GORM (SQLite) + Claude CLI
- **Frontend:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist (in-browser rendering) + pdf2zh (translation)

---

## 中文

LLM Knowledge 是一个自托管的个人知识管理工具，帮助你收集、理解和查询文档。支持导入 PDF、网页剪藏和 RSS，使用 Claude 提取和总结内容，并通过 SSE 流式对话与文档进行智能问答。

### 功能

- **多源导入** — 上传 PDF、剪藏网页、订阅 RSS
- **LLM 驱动提取** — Claude CLI 提取结构化内容、生成摘要、中英互译
- **文档对话** — 基于 SSE 的流式多轮问答，支持会话管理
- **PDF 翻译** — 通过 pdf2zh 实现排版保留的 PDF 翻译（兼容 OpenAI API）
- **双 PDF 对比视图** — 原文与译文左右分屏，滚动/缩放同步
- **双语界面** — 完整的中英文国际化支持
- **单文件部署** — 前端嵌入 Go 二进制文件，下载即用

### 环境要求

- **Go** 1.25+
- **Node.js & npm**（用于构建前端）
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — 需在 PATH 中可用
- **pdf2zh**（可选）— 用于 PDF 翻译功能

### 快速开始

```bash
# 克隆并启动（默认端口 9999）
git clone https://github.com/bruceding/llm_knowledge.git
cd llm_knowledge
./start.sh

# 或手动构建运行
make build
./llm-knowledge          # 默认端口 3456

# 开发模式（热重载）
make dev                 # 后端 :3456，前端 :5173
```

数据存储在 `~/.llm-knowledge/`（可通过 `DATA_DIR` 环境变量配置）。

### 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `3456` | 服务端口 |
| `DATA_DIR` | `~/.llm-knowledge` | 数据和数据库目录 |
| `PDF2ZH_VENV_DIR` | `$DATA_DIR/.venv` | pdf2zh Python 虚拟环境路径 |

### 技术栈

- **后端:** Go + Echo + GORM (SQLite) + Claude CLI
- **前端:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist（浏览器渲染）+ pdf2zh（翻译）
