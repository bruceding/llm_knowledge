# Chat Image Support Design

Date: 2026-04-29

## Overview

Add image support to Query Chat (知识库问答) system, allowing users to send images along with text questions to Claude.

## Requirements Summary

- Target: Query Chat only (not Doc Chat)
- Image sources: Paste from clipboard + Upload local file
- Storage: Server side, `~/.llm-knowledge/cache/images/`
- Size limit: 10MB per image
- Format: PNG, JPEG, GIF, WebP
- Display: Show images in both sending and history messages
- Feature: Click to enlarge image (modal view)

## Architecture

### Frontend UI (方案 B - 分离方案)

```
┌─────────────────────────────────────────┐
│ [缩略图1] [缩略图2] [× 删除按钮]          │  ← 图片区（独立）
├─────────────────────────────────────────┤
│ 输入框                                   │  ← 文本输入区
├─────────────────────────────────────────┤
│ [📎 上传按钮]               [发送按钮]   │  ← 操作按钮区
└─────────────────────────────────────────┘
```

- Image thumbnails: 64x64 px with delete button
- Click thumbnail → open enlargement modal
- Upload button: select local files
- Paste listener: Ctrl+V / right-click paste

### Backend API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/images/upload` | POST | Upload image (base64), return storage path |
| `/data/images/:filename` | GET | Static file service for images |

**Upload flow:**

1. Frontend sends base64 encoded image
2. Backend decodes, validates, generates unique filename
3. Stores to `dataDir/cache/images/`
4. Returns `{ path: "/data/images/abc123.png" }`

**Request structure changes:**

```go
type AskRequest struct {
    ConversationID uint     `json:"conversationId"`
    Question       string   `json:"question"`
    DocID          uint     `json:"docId,omitempty"`
    Images         []string `json:"images,omitempty"` // NEW: image paths
}

type ConversationMessage struct {
    ConversationID uint      `json:"conversationId"`
    Role           string    `json:"role"`
    Content        string    `json:"content"`
    Images         string    `json:"images"`        // NEW: JSON array of paths
    CreatedAt      time.Time `json:"createdAt"`
}
```

### Claude CLI Multimodal Message

**stdin format (stream-json with image):**

```json
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {
        "type": "image",
        "source": {
          "type": "base64",
          "media_type": "image/png",
          "data": "iVBORw0KGgoAAAANSUhEUg..."
        }
      },
      {
        "type": "text",
        "text": "这张图片说明了什么？"
      }
    ]
  }
}
```

**session.go changes:**

- Add `SendUserMessageWithImages(content string, images []ImageData) error`
- Build multimodal content array with image blocks + text block
- JSON encode and write to stdin

### Image Validation & Security

**Size validation:**
- Frontend: check `file.size <= 10MB` before upload
- Backend: verify decoded base64 length

**Format whitelist:**

| Format | media_type |
|--------|------------|
| PNG | `image/png` |
| JPEG | `image/jpeg` |
| GIF | `image/gif` |
| WebP | `image/webp` |

**Filename generation:**

```go
filename := fmt.Sprintf("%d_%s.%s", time.Now().UnixNano(), randomString(8), ext)
// Example: 1712345678901_a1b2c3d4.png
```

**Security measures:**

- Validate file header magic bytes (prevent fake extensions)
- No SVG allowed (XSS risk)
- Separate directory: `cache/images/` (not mixed with other data)

## Components to Modify

### Backend

1. `backend/api/images.go` - NEW: upload handler, static file handler
2. `backend/claude/session.go` - Add `SendUserMessageWithImages`
3. `backend/api/query.go` - Handle `Images` field in AskRequest
4. `backend/db/models.go` - Add `Images` field to `ConversationMessage`
5. `backend/main.go` - Register image routes, static file route

### Frontend

1. `frontend/src/components/ChatView.tsx` - Add image upload UI, paste listener, modal
2. `frontend/src/api.ts` - Add `uploadImage()` function
3. `frontend/src/types.ts` - Add image-related types

## Data Flow

```
User paste/upload → Frontend base64 encode
       ↓
POST /api/images/upload → Backend save to cache/images/
       ↓
Return { path: "/data/images/xxx.png" }
       ↓
User click send → POST /api/query/ask { question, images: ["/data/images/xxx.png"] }
       ↓
Backend load images → Convert to base64 → Send to Claude CLI stdin
       ↓
Claude response → SSE stream to frontend
       ↓
Save to DB with image paths
       ↓
History load → Display images from paths
```