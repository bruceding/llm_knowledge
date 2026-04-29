# LLM Knowledge

[中文文档](README_ZH.md)

A self-hosted knowledge management tool that ingests documents, extracts content via LLM, and provides intelligent Q&A — all in a single binary.

LLM Knowledge is a personal knowledge base that helps you collect, understand, and query your documents. It ingests PDFs, web pages, and RSS feeds, uses Claude to extract and summarize content, and lets you chat with your documents through an SSE-powered conversational interface.

## Features

- **Multi-source ingestion** — Upload PDFs (drag & drop or URL), clip web pages, or subscribe to RSS feeds with auto-sync
- **LLM-powered extraction** — Claude CLI extracts structured content, generates summaries, and translates between Chinese and English
- **Document chat** — Multi-turn SSE streaming Q&A with session management, powered by Claude CLI
- **Query chat** — Free-form conversational AI with stop/interrupt support, conversation history, and multimodal image input
- **PDF translation** — Layout-preserving PDF translation via pdf2zh (OpenAI-compatible API)
- **Dual PDF viewer** — Scroll/scale-synced side-by-side comparison of original and translated PDFs
- **Bilingual UI** — Full i18n support for English and Chinese
- **Single binary** — Frontend embedded in Go binary, just download and run

## Prerequisites

- **Go** 1.25+
- **Node.js & npm** (for building frontend)
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — available in PATH
- **pdf2zh** (optional) — for PDF translation feature

## Quick Start

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

## Configuration

| Env Variable | Default | Description |
|---|---|---|
| `PORT` | `3456` | Server port |
| `DATA_DIR` | `~/.llm-knowledge` | Data and database directory |
| `PDF2ZH_VENV_DIR` | `$DATA_DIR/.venv` | pdf2zh Python venv path |

## Tech Stack

- **Backend:** Go + Echo + GORM (SQLite) + Claude CLI
- **Frontend:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist (in-browser rendering) + pdf2zh (translation)
