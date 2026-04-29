# LLM Knowledge

[English](README.md)

一个自托管的个人知识管理工具，帮助你收集、理解和查询文档 — 单文件部署，开箱即用。

LLM Knowledge 支持导入 PDF、网页剪藏和 RSS，使用 Claude 提取和总结内容，并通过 SSE 流式对话与文档进行智能问答。

## 功能

- **多源导入** — 上传 PDF（拖拽或 URL）、剪藏网页、订阅 RSS 并自动同步
- **LLM 驱动提取** — Claude CLI 提取结构化内容、生成摘要、中英互译
- **文档对话** — 基于 SSE 的流式多轮问答，支持会话管理
- **知识库问答** — 自由对话式 AI，支持中断/停止、会话历史和图片输入
- **PDF 翻译** — 通过 pdf2zh 实现排版保留的 PDF 翻译（兼容 OpenAI API）
- **双 PDF 对比视图** — 原文与译文左右分屏，滚动/缩放同步
- **双语界面** — 完整的中英文国际化支持
- **单文件部署** — 前端嵌入 Go 二进制文件，下载即用

## 环境要求

- **Go** 1.25+
- **Node.js & npm**（用于构建前端）
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — 需在 PATH 中可用
- **pdf2zh**（可选）— 用于 PDF 翻译功能

## 快速开始

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

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `3456` | 服务端口 |
| `DATA_DIR` | `~/.llm-knowledge` | 数据和数据库目录 |
| `PDF2ZH_VENV_DIR` | `$DATA_DIR/.venv` | pdf2zh Python 虚拟环境路径 |

## 技术栈

- **后端:** Go + Echo + GORM (SQLite) + Claude CLI
- **前端:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist（浏览器渲染）+ pdf2zh（翻译）
