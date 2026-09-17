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
- **[Claude CLI](https://docs.anthropic.com/en/docs/claude-code/overview)** — available in PATH (the default LLM backend)
- **[pi](https://github.com/earendil-works/pi-coding-agent)** (optional) — alternative LLM backend, `npm install -g @earendil-works/pi-coding-agent`; also needs the `pi-web-access` extension (pin `0.29.0`). Only one backend is active at a time, chosen by an admin in Settings — see [LLM Backend Switch](#llm-backend-switch-claude--pi)
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
- Checks whether **pi** and **pi-web-access** are present (optional backend; warns only, never blocks)
- Builds backend and frontend
- Deploys `path-validator.py` and `pi-path-validator.ts` into the runtime `scripts/` dir (i.e. `LLM_SCRIPTS_DIR`)
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

## LLM Backend Switch (claude / pi)

Two LLM backends are supported and **exactly one is active at a time**, selected by an admin under Settings → "LLM Backend" (the `llmBackend` field of `GET/PUT /api/admin/settings`). Document chat, free-form Q&A, ingest summarisation/sectionisation and per-page PDF extraction all follow the same switch — no code change or restart needed.

- Defaults to `claude`; the only accepted values are `claude` and `pi`, anything else is a 400
- Switching to `pi` **probes before saving**; if the probe fails the API returns 400 and the stored value is left alone
- A successful save takes effect immediately (the resolver cache is 5s and is invalidated on save)
- Switching back to `claude` is **never probed** — it is the escape hatch for when pi is broken, and probing would block it

### Deploying the pi backend

1. `npm install -g @earendil-works/pi-coding-agent` (verified on 0.85.1; its `engines` field requires **Node.js >= 22.19.0**)
2. Install the `pi-web-access` extension and **pin its version** (verified on 0.29.0)
3. Pin down `web-search.json`: copy the repo's `backend/scripts/web-search.json.sample` to
   `$PI_CODING_AGENT_DIR/web-search.json`, or to
   `<service account HOME>/.pi/agent/web-search.json` when that env var is unset.
   It is **not** `$XDG_CONFIG_HOME/pi/` — pi does not read that location.
4. `pi-path-validator.ts` is copied into the runtime `scripts/` dir (i.e. `LLM_SCRIPTS_DIR`)
   automatically by `make build` and `start.sh`; there is no manual step. Without it the pi
   backend is **fail-closed**: the switch returns 400 and spawning is refused, rather than
   silently degrading to "no sandbox".

`start.sh` reports whether pi and pi-web-access are present. Missing ones only warn and never
block startup (the claude backend does not need pi).

### Operational warnings

**① Switching backends invalidates context resumption for in-flight conversations — in both directions.**
`chat_session_id` / `session_id` hold **that backend's own** session ID and the two formats are not interchangeable. After a switch, old conversations cannot be resumed: the history is still in the database and still readable, it just cannot be continued. **"Switch back and the conversation resumes" does NOT hold** — messages produced while the other backend was active forked the two sessions. Do not switch while conversations are in flight.

**② Only the `admin` account can flip the switch. Do not rename or delete it.**
The control renders inside `{isAdmin && ...}` and `isAdmin` comes from `users.role`. `db/db.go:38` contains an **unguarded** migration:

```sql
UPDATE users SET role='admin'
 WHERE username='admin' AND (role IS NULL OR role='' OR role='user')
```

It only re-promotes a row whose username is **literally `admin`**. So if that account is renamed, this migration will not restore it and nobody will be able to see the backend switch in the UI again (only a direct database edit would).

**③ `--tools` does not stop code that runs at extension load time.**
pi's `--tools` constrains **tool calls** only; an extension executes its top-level code as soon as it is loaded. So pin the `pi-web-access` version and control the `packages` list in `settings.json` — every loaded package is code running inside the service process.

**④ `web-search.json` must explicitly disable the extension commands, or any user can run extension code.**
In rpc mode pi **dispatches user messages that start with `/` as extension commands**. So an ordinary document-chat message (e.g. `/search foo`) can trigger one directly. The deployment template disables all four:

```json
"commands": {
  "websearch":      { "enabled": false },
  "curator":        { "enabled": false },
  "search":         { "enabled": false },
  "google-account": { "enabled": false }
}
```

**The default is all-enabled**, so "no config file at all" is not a safe state — it is the most dangerous one.

Why none of the four existing defences stop this:

| Defence | Why it does not help |
|---|---|
| `--tools` | Governs **tool calls**; command dispatch never goes through it |
| `--no-skills` / `--no-prompt-templates` | Only disable skills and prompt templates, unrelated to extension commands |
| the sandbox extension's `input` hook | Fires **after** the command has been dispatched, i.e. once it is already running |
| filtering message content server-side | Not viable — it would break legitimate questions that start with `/` |

Commands registered by *other* loaded packages can only be covered by ③ (pinned versions + a controlled `packages` list). The Go side validates this config at startup and warns; the Settings switch rejects a fatally broken config with a 400.

**⑤ A malformed `toolNames` makes the entire pi backend `exit 1` — not just the web features.**
pi-web-access throws inside `resolveToolNames` → the extension fails to load → pi exits with code 1. That kills the **ingest paths that never touch web tools** (summarisation, sectionisation, PDF extraction) as well.

On whether the probe can detect this: `pi --version` **cannot** — it exits 0 early at pi's `main.js:483-486` and never loads extensions. So besides `pi --version`, the switch probe runs a **static pre-check** using the same parsing logic as the Go side (both read the very same file, see step 3 above), rejecting bad `toolNames` shapes, values and cross-key duplicates with a 400 at switch time.

The static pre-check does **not** cover: `pi-web-access` not being installed at all, or **another** package throwing at load time. `start.sh` warns about the former; only ③ covers the latter.

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
