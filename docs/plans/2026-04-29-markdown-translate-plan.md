# Markdown Translation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Markdown translation feature for Web/RSS documents using OpenAI-compatible API.

**Architecture:** Backend adds new SSE endpoint that calls OpenAI API to translate Markdown content. Frontend adds translation button and bilingual view mode for Web/RSS documents.

**Tech Stack:** Go (echo), OpenAI Go SDK (`github.com/sashabaranov/go-openai`), React (SSE streaming)

---

## Task 1: Add OpenAI Go SDK dependency

**Files:**
- Modify: `backend/go.mod`

**Step 1: Add dependency**

Run:
```bash
cd backend && go get github.com/sashabaranov/go-openai
```

**Step 2: Verify dependency added**

Run:
```bash
cd backend && cat go.mod | grep go-openai
```
Expected: Line containing `github.com/sashabaranov/go-openai`

**Step 3: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "feat: add OpenAI Go SDK dependency for markdown translation"
```

---

## Task 2: Create markdown_translate.go handler

**Files:**
- Create: `backend/api/markdown_translate.go`

**Step 1: Create handler file with struct and imports**

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"llm-knowledge/db"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sashabaranov/go-openai"
	"gorm.io/gorm"
)

type MarkdownTranslateHandler struct {
	DataDir string
}

type MarkdownTranslateRequest struct {
	DocID      uint   `json:"docId"`
	TargetLang string `json:"targetLang"` // zh, en
}

var validMarkdownTargetLangs = map[string]string{
	"zh": "中文",
	"en": "English",
}
```

**Step 2: Add CheckMarkdownTranslationStatus endpoint**

```go
// CheckMarkdownTranslationStatus checks if translated Markdown file exists
// GET /api/documents/:id/markdown-translation-status
func (h *MarkdownTranslateHandler) CheckMarkdownTranslationStatus(c echo.Context) error {
	docID := c.Param("id")
	if docID == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document id required"})
	}

	var doc db.Document
	if err := db.DB.First(&doc, docID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
		}
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get document"})
	}

	// Only for Web/RSS documents
	if doc.SourceType != "web" && doc.SourceType != "rss" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only web/rss documents supported"})
	}

	// Determine target language based on source
	targetLang := "zh"
	if doc.Language == "zh" {
		targetLang = "en"
	}

	// Determine translation file path
	translatedPath := h.getTranslatedPath(doc.RawPath, targetLang)
	fullPath := filepath.Join(h.DataDir, translatedPath)

	exists := false
	if _, err := os.Stat(fullPath); err == nil {
		exists = true
	}

	return c.JSON(http.StatusOK, echo.Map{
		"exists":    exists,
		"path":      filepath.Join("/data", translatedPath),
		"targetLang": targetLang,
	})
}

// getTranslatedPath returns the path for translated file
func (h *MarkdownTranslateHandler) getTranslatedPath(rawPath string, targetLang string) string {
	if strings.HasSuffix(rawPath, ".md") {
		// RSS: raw/rss/feed/title.md -> raw/rss/feed/title_zh.md
		return strings.TrimSuffix(rawPath, ".md") + "_" + targetLang + ".md"
	}
	// Web: raw/web/title -> raw/web/title/paper_zh.md
	return rawPath + "/paper_" + targetLang + ".md"
}
```

**Step 3: Commit**

```bash
git add backend/api/markdown_translate.go
git commit -m "feat: add markdown translation status check endpoint"
```

---

## Task 3: Add TranslateMarkdown SSE endpoint

**Files:**
- Modify: `backend/api/markdown_translate.go`

**Step 1: Add translation prompt constant**

```go
const markdownTranslatePrompt = `请将以下 Markdown 内容翻译为%s。

要求：
1. 保持原文的 Markdown 格式（标题、链接、引用、代码块等）
2. 每个段落原文后，添加翻译内容，格式为：
   > 翻译：译文内容
3. 代码块和 URL 不需要翻译
4. 保持学术性和专业性

原文内容：
%s`
```

**Step 2: Add TranslateMarkdown endpoint with SSE streaming**

```go
// TranslateMarkdown translates Markdown document using OpenAI-compatible API
// POST /api/markdown-translate (SSE streaming)
func (h *MarkdownTranslateHandler) TranslateMarkdown(c echo.Context) error {
	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	var req MarkdownTranslateRequest
	if err := c.Bind(&req); err != nil {
		sendMarkdownSSEError(c, flusher, "invalid request body")
		return nil
	}

	if req.DocID == 0 {
		sendMarkdownSSEError(c, flusher, "docId is required")
		return nil
	}

	// Validate target language
	targetLangName, valid := validMarkdownTargetLangs[req.TargetLang]
	if !valid {
		sendMarkdownSSEError(c, flusher, "invalid targetLang, must be 'zh' or 'en'")
		return nil
	}

	// Get document
	var doc db.Document
	if err := db.DB.First(&doc, req.DocID).Error; err != nil {
		sendMarkdownSSEError(c, flusher, "document not found")
		return nil
	}

	// Validate source type
	if doc.SourceType != "web" && doc.SourceType != "rss" {
		sendMarkdownSSEError(c, flusher, "only web/rss documents supported")
		return nil
	}

	if doc.RawPath == "" {
		sendMarkdownSSEError(c, flusher, "document has no raw content")
		return nil
	}

	// Get settings
	var settings db.UserSettings
	if err := db.DB.First(&settings).Error; err != nil {
		sendMarkdownSSEError(c, flusher, "failed to get settings")
		return nil
	}

	if !settings.TranslationEnabled {
		sendMarkdownSSEError(c, flusher, "translation not enabled in settings")
		return nil
	}

	if settings.TranslationApiKey == "" {
		sendMarkdownSSEError(c, flusher, "API key not configured")
		return nil
	}

	// Read source content
	sourcePath := h.getSourcePath(doc.RawPath)
	fullSourcePath := filepath.Join(h.DataDir, sourcePath)
	content, err := os.ReadFile(fullSourcePath)
	if err != nil {
		sendMarkdownSSEError(c, flusher, "source file not found")
		return nil
	}

	// Send progress event
	sendMarkdownSSEEvent(c, flusher, "progress", echo.Map{"message": "正在翻译..."})

	// Create OpenAI client
	config := openai.DefaultConfig(settings.TranslationApiKey)
	config.BaseURL = settings.TranslationApiBase
	client := openai.NewClientWithConfig(config)

	// Build prompt
	prompt := fmt.Sprintf(markdownTranslatePrompt, targetLangName, string(content))

	// Create chat completion request with streaming
	ctx := c.Request().Context()
	stream, err := client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: settings.TranslationModel,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Stream: true,
	})
	if err != nil {
		sendMarkdownSSEError(c, flusher, "API call failed: "+err.Error())
		return nil
	}
	defer stream.Close()

	// Stream response
	var translatedContent strings.Builder
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			sendMarkdownSSEError(c, flusher, "stream error: "+err.Error())
			return nil
		}

		if len(response.Choices) > 0 {
			chunk := response.Choices[0].Delta.Content
			if chunk != "" {
				translatedContent.WriteString(chunk)
				// Send chunk to client
				sendMarkdownSSEEvent(c, flusher, "chunk", echo.Map{"content": chunk})
			}
		}
	}

	// Save translated content
	translatedPath := h.getTranslatedPath(doc.RawPath, req.TargetLang)
	fullTranslatedPath := filepath.Join(h.DataDir, translatedPath)

	// Ensure directory exists for Web documents
	if !strings.HasSuffix(doc.RawPath, ".md") {
		dir := filepath.Dir(fullTranslatedPath)
		os.MkdirAll(dir, 0755)
	}

	if err := os.WriteFile(fullTranslatedPath, []byte(translatedContent.String()), 0644); err != nil {
		sendMarkdownSSEError(c, flusher, "failed to save translation: "+err.Error())
		return nil
	}

	// Send completion event
	sendMarkdownSSEEvent(c, flusher, "complete", echo.Map{
		"path":       filepath.Join("/data", translatedPath),
		"targetLang": req.TargetLang,
	})

	return nil
}

// getSourcePath returns the path for source Markdown file
func (h *MarkdownTranslateHandler) getSourcePath(rawPath string) string {
	if strings.HasSuffix(rawPath, ".md") {
		// RSS: direct .md file
		return rawPath
	}
	// Web: directory with paper.md
	return rawPath + "/paper.md"
}

func sendMarkdownSSEEvent(c echo.Context, flusher http.Flusher, eventType string, data echo.Map) {
	data["type"] = eventType
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData)
	flusher.Flush()
}

func sendMarkdownSSEError(c echo.Context, flusher http.Flusher, errorMsg string) {
	sendMarkdownSSEEvent(c, flusher, "error", echo.Map{"error": errorMsg})
}
```

**Step 3: Add missing imports**

Add `"encoding/json"` to imports.

**Step 4: Verify compilation**

Run:
```bash
cd backend && go build -o ../llm-knowledge . 2>&1
```
Expected: No errors

**Step 5: Commit**

```bash
git add backend/api/markdown_translate.go
git commit -m "feat: add markdown translation SSE endpoint with OpenAI streaming"
```

---

## Task 4: Register routes in main.go

**Files:**
- Modify: `backend/main.go`

**Step 1: Add route registration**

Find the location where other handlers are registered (around line 140-150). Add:

```go
// Markdown Translation API (SSE streaming)
markdownTranslateH := &api.MarkdownTranslateHandler{
	DataDir: cfg.DataDir,
}
e.POST("/api/markdown-translate", markdownTranslateH.TranslateMarkdown)
e.GET("/api/documents/:id/markdown-translation-status", markdownTranslateH.CheckMarkdownTranslationStatus)
```

**Step 2: Verify compilation**

Run:
```bash
cd backend && go build -o ../llm-knowledge . 2>&1
```
Expected: No errors

**Step 3: Commit**

```bash
git add backend/main.go
git commit -m "feat: register markdown translation API routes"
```

---

## Task 5: Add frontend API functions

**Files:**
- Modify: `frontend/src/api.ts`

**Step 1: Add checkMarkdownTranslationStatus function**

```typescript
// Markdown Translation API
export async function checkMarkdownTranslationStatus(docId: number): Promise<{
  exists: boolean
  path: string
  targetLang: string
}> {
  const res = await fetch(`${API_BASE}/documents/${docId}/markdown-translation-status`)
  if (!res.ok) throw new Error('Failed to check markdown translation status')
  return res.json()
}
```

**Step 2: Add translateMarkdown SSE function**

```typescript
export async function translateMarkdown(
  docId: number,
  targetLang: string,
  onEvent: (event: SSEEvent) => void
): Promise<void> {
  const res = await fetch(`${API_BASE}/markdown-translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ docId, targetLang }),
  })

  if (!res.ok) throw new Error('Failed to start markdown translation')

  const reader = res.body?.getReader()
  if (!reader) throw new Error('No response body')

  const decoder = new TextDecoder()
  let buffer = ''

  while (true) {
    const { done, value } = await reader.read()
    if (done) break

    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''

    for (const line of lines) {
      if (line.startsWith('data: ')) {
        try {
          const data = JSON.parse(line.slice(6))
          onEvent(data)
        } catch {
          // Ignore parse errors
        }
      }
    }
  }
}
```

**Step 3: Commit**

```bash
git add frontend/src/api.ts
git commit -m "feat: add markdown translation API functions in frontend"
```

---

## Task 6: Update DocDetail.tsx for translation button

**Files:**
- Modify: `frontend/src/components/DocDetail.tsx`

**Step 1: Add imports for new API**

Add to imports:
```typescript
import { checkMarkdownTranslationStatus, translateMarkdown } from '../api'
```

**Step 2: Add state for markdown translation**

Add new state variables after existing translation state (around line 40-45):
```typescript
// Markdown translation state (for Web/RSS documents)
const [markdownTranslationStatus, setMarkdownTranslationStatus] = useState<{ exists: boolean; path?: string; targetLang?: string } | null>(null)
const [markdownTranslating, setMarkdownTranslating] = useState(false)
const [bilingualContent, setBilingualContent] = useState('')
```

**Step 3: Add markdown translation check in loadDocument**

Inside `loadDocument` function, after the RSS/Web raw content loading section (around line 155), add:
```typescript
// Check markdown translation status for Web/RSS documents
if ((doc.sourceType === 'rss' || doc.sourceType === 'web') && settings?.translationEnabled) {
  try {
    const mdStatus = await checkMarkdownTranslationStatus(doc.id)
    setMarkdownTranslationStatus(mdStatus)
    if (mdStatus.exists && mdStatus.path) {
      // Load bilingual content
      const bilingualRes = await fetch(mdStatus.path)
      if (bilingualRes.ok) {
        setBilingualContent(await bilingualRes.text())
      }
    }
  } catch (err) {
    console.error('Failed to check markdown translation status:', err)
  }
}
```

**Step 4: Add handleMarkdownTranslate callback function**

Add after `handlePDFTranslate` function (around line 320):
```typescript
// Markdown Translation handler (for Web/RSS)
const handleMarkdownTranslate = useCallback(async (targetLang?: string) => {
  if (!document || !settings?.translationEnabled) return

  const lang = targetLang || (document.language === 'en' ? 'zh' : 'en')
  setMarkdownTranslating(true)

  try {
    await translateMarkdown(document.id, lang, (event: SSEEvent) => {
      if (event.type === 'error') {
        setError(event.error || 'Translation failed')
        setMarkdownTranslating(false)
      } else if (event.type === 'complete') {
        setMarkdownTranslating(false)
        if (event.path) {
          setMarkdownTranslationStatus({ exists: true, path: event.path, targetLang: lang })
          setViewMode('bilingual')
          // Load bilingual content
          fetch(event.path).then(res => res.text()).then(text => setBilingualContent(text))
        }
      }
    })
  } catch (err) {
    setError(err instanceof Error ? err.message : 'Failed to translate markdown')
    setMarkdownTranslating(false)
  }
}, [document, settings])
```

**Step 5: Update viewMode type to include 'bilingual' for Web/RSS**

The viewMode state already includes 'bilingual'. Update `getDisplayContent` function to return bilingual content:
```typescript
case 'bilingual':
  return bilingualContent || rawContent
```

**Step 6: Add translation button and view mode button in header**

In the header section (around line 462), add for Web/RSS documents:
```tsx
{/* Markdown translate buttons - only for Web/RSS */}
{!isPDF && (doc.sourceType === 'rss' || doc.sourceType === 'web') && settings?.translationEnabled && (
  <>
    {!markdownTranslationStatus?.exists && !markdownTranslating && (
      <button
        onClick={() => handleMarkdownTranslate()}
        className="px-3 py-1.5 text-sm bg-purple-100 text-purple-700 rounded-lg hover:bg-purple-200"
      >
        {t('docDetail.translate')}
      </button>
    )}
    {markdownTranslating && (
      <div className="flex items-center gap-2 px-3 py-1.5 bg-purple-50 rounded-lg">
        <div className="animate-spin h-4 w-4 border-2 border-purple-500 rounded-full border-t-transparent"></div>
        <span className="text-sm text-purple-700">{t('docDetail.translating')}</span>
      </div>
    )}
    {markdownTranslationStatus?.exists && (
      <button
        onClick={() => setViewMode('bilingual')}
        className={`px-3 py-1.5 rounded-lg text-sm ${
          viewMode === 'bilingual' ? 'bg-purple-100 text-purple-700' : 'text-gray-600 hover:bg-gray-100'
        }`}
      >
        {t('docDetail.bilingual')} ({markdownTranslationStatus.targetLang?.toUpperCase()})
      </button>
    )}
  </>
)}
```

**Step 7: Verify frontend build**

Run:
```bash
cd frontend && npm run build 2>&1 | tail -20
```
Expected: Build successful

**Step 8: Commit**

```bash
git add frontend/src/components/DocDetail.tsx
git commit -m "feat: add markdown translation UI for Web/RSS documents"
```

---

## Task 7: Add i18n translations

**Files:**
- Modify: `frontend/src/i18n/en.json`
- Modify: `frontend/src/i18n/zh.json`

**Step 1: Add English translations**

Add to `docDetail` section:
```json
"translate": "Translate",
"translating": "Translating...",
"bilingual": "Bilingual"
```

**Step 2: Add Chinese translations**

Add to `docDetail` section:
```json
"translate": "翻译",
"translating": "正在翻译...",
"bilingual": "双语对照"
```

**Step 3: Commit**

```bash
git add frontend/src/i18n/en.json frontend/src/i18n/zh.json
git commit -m "feat: add i18n translations for markdown translation"
```

---

## Task 8: Build and test

**Step 1: Build backend**

Run:
```bash
cd backend && go build -o ../llm-knowledge .
```

**Step 2: Build frontend**

Run:
```bash
cd frontend && npm run build
```

**Step 3: Start server and test**

Run:
```bash
cd .. && ./llm-knowledge --port 9999
```

Open browser to `http://localhost:9999/documents/<id>` for a Web/RSS document and verify:
1. Translation button appears
2. Clicking starts translation with progress indicator
3. Completion shows bilingual view button
4. Bilingual view displays translated content

**Step 4: Final commit**

```bash
git add -A
git commit -m "feat: complete markdown translation feature for Web/RSS documents"
```