# LLM Knowledge

[中文文档](README_ZH.md)

A self-hosted knowledge management tool that ingests documents, extracts content via LLM, and provides intelligent Q&A — all in a single binary.

LLM Knowledge is a personal knowledge base that helps you collect, understand, and query your documents. It ingests PDFs, web pages, and RSS feeds, uses Claude to extract and summarize content, and lets you chat with your documents through an SSE-powered conversational interface.

## Features

- **Multi-source ingestion** — Upload PDFs (drag & drop or URL), clip web pages via Chrome extension, subscribe to RSS feeds or blogs with auto-sync, or import newsletters via IMAP
- **LLM-powered extraction** — Claude CLI extracts structured content, generates summaries, and translates between Chinese and English
- **Document chat** — Multi-turn SSE streaming Q&A with session management, powered by Claude CLI
- **Query chat** — Free-form conversational AI with stop/interrupt support, conversation history, and multimodal image input
- **PDF translation** — Layout-preserving PDF translation via pdf2zh (OpenAI-compatible API)
- **Dual PDF viewer** — Scroll/scale-synced side-by-side comparison of original and translated PDFs
- **Tags & Wiki** — Tag documents and browse curated entries in the Wiki view
- **Multi-user accounts** — Register, login, and per-user data isolation with token-based auth
- **Mobile responsive** — Dedicated mobile shell with bottom-sheet chat for phones and tablets
- **Bilingual UI** — Full i18n support for English and Chinese
- **Single binary** — Frontend embedded in Go binary, just download and run

## Prerequisites

- **Go** 1.25+
- **Node.js & npm** (for building frontend)
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — available in PATH
- **Python 3.12** (optional) — for PDF translation via pdf2zh (PEP 695 syntax required)
- **qpdf** (optional) — pdf2zh dependency for pikepdf

## Quick Start

```bash
# Clone and start (default port 9999)
git clone https://github.com/bruceding/llm_knowledge.git
cd llm_knowledge
./start.sh
```

The `start.sh` script automatically:
- Checks and installs **pdftotext** (poppler) for PDF text extraction
- Checks **Python 3.12** availability (prints warning if missing, PDF translation disabled)
- Checks and installs **qpdf** for pdf2zh dependency
- Builds backend and frontend
- Starts the server on port 9999

```bash
# Custom port
PORT=8080 ./start.sh

# Or build and run manually
make build
./llm-knowledge -port 8080

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

## Keyboard Shortcuts

Available on desktop browsers. Shortcuts are disabled while typing in inputs.

### Document Detail (vim-style scrolling)

- `j` / `k` — Scroll content down / up (hold to accelerate)
- `g` / `G` — Jump to top / bottom of the document

### Inbox & Wiki Document Lists

- `d` — Delete the document the mouse is currently hovering over (with confirmation)

### Chat (Document Chat & Query Chat)

- `Enter` — Send message
- `Shift` + `Enter` — Insert a new line

### Dialogs & Search

- `Escape` — Close the current dialog (e.g., Blog config, confirm dialog)
- `Enter` — Submit search in PDF viewer / add a tag in document detail

## Tech Stack

- **Backend:** Go + Echo + GORM (SQLite) + Claude CLI
- **Frontend:** React 19 + TypeScript + Vite + Tailwind CSS v4
- **PDF:** pdfjs-dist (in-browser rendering) + pdf2zh (translation)

## Chrome Extension — Wiki Web Clipper

One-click web page clipping to your knowledge base. Works with any modern Chromium-based browser (Chrome, Edge, Brave, etc.).

### Installation

1. Open Chrome and navigate to `chrome://extensions/`
2. Enable **Developer mode** (toggle in top-right corner)
3. Click **Load unpacked** and select the `extension/` folder from this project

### Setup

After installation, click the extension icon to open the settings page:

1. Enter your **Wiki URL** (e.g., `http://localhost:9999` or your deployed address)
2. Enter your **username** and **password**
3. Click **Save and Connect** to authenticate

The extension will store your credentials locally and auto-refresh tokens when needed.

### Usage

- Navigate to any web page you want to save
- Click the extension icon in the toolbar
- The page will be clipped and sent to your wiki's "Raw Documents" section
- Success: green ✓ badge | Failure: red ✗ badge | Progress: gray "..." badge
- Toast notifications appear on the page to confirm the result

### Features

- **Full page capture** — Extracts complete HTML content with preserved structure
- **Auto title detection** — Uses page title as document name
- **WeChat article support** — Special handling for WeChat public account articles
- **Authentication** — Secure token-based auth with auto-expiry handling
- **Visual feedback** — Badge and toast notifications for operation status

### Supported Sites

Works on most public websites. Some sites with heavy JavaScript rendering may require the page to fully load before clipping.

## Blog Subscription — Index Page Sync

Subscribe to a blog by its index/archive page URL. The server fetches the page (rendering JavaScript via a headless browser when needed), auto-detects known platforms, extracts article links, and pulls the latest posts into your inbox.

### Setup

1. Go to **Import → Blog** tab in the UI
2. Enter the blog's **index URL** (e.g., `https://example.com/blog`) and an optional **name**
3. Optionally enable **Auto Sync** for periodic background pulls
4. Click **Add Feed**

If the blog is on a known platform (WordPress, Medium-style, etc.), it works immediately. For unknown sites, a config dialog will appear asking you to provide CSS selectors:

- **Link selector** — CSS selector for article links on the index page (e.g., `article h2 a`)
- **Content selector** — CSS selector for the article body on each post page (e.g., `article .post-content`)
- **Link exclude** (optional) — CSS selector for links to ignore (e.g., pagination, tags)

### Usage

- **First sync** scans up to 20 candidate articles, sorts by publication date, and imports the 5 most recent
- **Subsequent syncs** only fetch new articles since the last run
- Click **Sync Now** on any feed to pull updates manually
- JavaScript-rendered SPAs are supported via a shared headless browser pool

### Features

- **Platform auto-detection** — Common blog platforms work out of the box, no selector config needed
- **SPA support** — Headless browser renders JavaScript before extraction
- **Smart deduplication** — Tracks seen URLs per feed so re-syncs don't create duplicates
- **Background auto-sync** — Per-feed scheduling, runs alongside RSS sync

## Newsletter Import — IMAP Email Sync

Automatically import newsletters from your email inbox via IMAP. Perfect for subscribing to tech newsletters, industry updates, and curated content.

### Setup

1. Go to **Import → Newsletter** tab in the UI
2. Configure your IMAP settings:
   - **Host**: IMAP server address (e.g., `imap.gmail.com` for Gmail)
   - **Port**: `993` (IMAPS, recommended) or `143` (IMAP)
   - **Username**: Your email address
   - **Password**: Email password or app-specific password
   - **Folder**: Mailbox folder name (default: `Newsletter`)
3. Enable **Auto Sync** if you want hourly automatic syncing
4. Click **Save and Connect**

### Gmail Setup

For Gmail, you need an **App Password** instead of your regular password:

1. Go to [Google Account Security](https://myaccount.google.com/security)
2. Enable **2-Step Verification** (required for app passwords)
3. Go to **App passwords** → Generate new password
4. Select "Mail" and "Other (Custom name)" → name it "LLM Knowledge"
5. Use the generated 16-character password in the setup

### Usage

- Click **Sync Now** to manually fetch new newsletters
- First sync imports up to 10 newsletters (to avoid overwhelming)
- Subsequent syncs fetch only emails since the last sync
- Auto-sync runs hourly if enabled

### Features

- **HTML extraction** — Extracts clean HTML content from multipart emails
- **Image handling** — Downloads embedded images, filters tracking pixels
- **Smart cleanup** — Removes duplicate titles, footer noise, unsubscribe links
- **Sender tagging** — Auto-creates tags based on newsletter sender
- **Claude summary** — Background summary generation for each newsletter
- **View-in-browser links** — Extracts and preserves original newsletter links

### Folder Organization

Newsletters are stored in `~/.llm-knowledge/raw/newsletter/<sender>/`:
- `<slug>.md` — Markdown version with metadata header
- `<slug>.html` — Original HTML for rich rendering
- `assets/` — Downloaded images
