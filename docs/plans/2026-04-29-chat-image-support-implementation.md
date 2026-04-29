# Chat Image Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add image support to Query Chat, allowing users to send images with text questions.

**Architecture:** Frontend uploads images as base64 → backend stores in cache/images/ → sends multimodal message to Claude CLI via stream-json → saves image paths in DB → displays in chat history.

**Tech Stack:** Go (Echo framework), React (TypeScript), SQLite (GORM)

---

## Task 1: Add Images field to ConversationMessage model

**Files:**
- Modify: `backend/db/models.go:48-55`
- Modify: `backend/db/db.go:16`

**Step 1: Add Images field to ConversationMessage struct**

```go
type ConversationMessage struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `json:"conversationId"`
	Role           string    `json:"role"` // user, assistant, system
	Content        string    `json:"content"`
	ContextDocIDs  string    `json:"contextDocIds"` // JSON array
	Images         string    `gorm:"default:''" json:"images"` // NEW: JSON array of image paths
	CreatedAt      time.Time `json:"createdAt"`
}
```

**Step 2: Verify AutoMigrate includes the new field**

The existing `db.go:16` already has `AutoMigrate` with `ConversationMessage`. The new field will be auto-added on next start.

**Step 3: Commit**

```bash
git add backend/db/models.go
git commit -m "feat(db): add Images field to ConversationMessage"
```

---

## Task 2: Create image upload API handler

**Files:**
- Create: `backend/api/images.go`

**Step 1: Create images.go with upload handler**

```go
package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"llm-knowledge/db"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// ImageUploadRequest represents the request body for image upload
type ImageUploadRequest struct {
	Data     string `json:"data"`     // base64 encoded image data
	Type     string `json:"type"`     // image type: png, jpeg, gif, webp
	Filename string `json:"filename"` // optional original filename
}

// ImageUploadResponse represents the response for image upload
type ImageUploadResponse struct {
	Path     string `json:"path"`     // e.g., /data/images/1712345678901_abc123.png
	Filename string `json:"filename"` // stored filename
}

// ImagesHandler handles image upload and serving
type ImagesHandler struct {
	DataDir string
}

// allowedImageTypes maps type strings to media types and extensions
var allowedImageTypes = map[string]struct {
	mediaType string
	ext       string
}{
	"png":  {"image/png", ".png"},
	"jpeg": {"image/jpeg", ".jpg"},
	"jpg":  {"image/jpeg", ".jpg"},
	"gif":  {"image/gif", ".gif"},
	"webp": {"image/webp", ".webp"},
}

// magicBytes maps file signatures to media types
var magicBytes = map[string]string{
	"\x89PNG\r\n\x1a\n": "image/png",
	"\xff\xd8\xff":      "image/jpeg",
	"GIF87a":            "image/gif",
	"GIF89a":            "image/gif",
	"RIFF":              "image/webp", // WebP starts with RIFF....WEBP
}

// Upload handles image upload from base64 data
// POST /api/images/upload
func (h *ImagesHandler) Upload(c echo.Context) error {
	var req ImageUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	// Validate image type
	typeInfo, ok := allowedImageTypes[strings.ToLower(req.Type)]
	if !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "unsupported image type. Allowed: png, jpeg, gif, webp"})
	}

	// Decode base64
	data := req.Data
	// Remove data URL prefix if present (e.g., "data:image/png;base64,")
	if strings.Contains(data, ",") {
		data = strings.Split(data, ",")[1]
	}

	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid base64 data"})
	}

	// Check size limit (10MB)
	if len(decoded) > 10*1024*1024 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "image too large. Maximum 10MB"})
	}

	// Validate magic bytes
	detectedType := detectImageType(decoded)
	if detectedType != typeInfo.mediaType && detectedType != "" {
		log.Printf("[images] Warning: declared type %s but detected %s", typeInfo.mediaType, detectedType)
	}

	// Create images directory
	imagesDir := filepath.Join(h.DataDir, "cache", "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create images directory"})
	}

	// Generate unique filename
	timestamp := time.Now().UnixNano()
	randomStr := randomString(8)
	filename := fmt.Sprintf("%d_%s%s", timestamp, randomStr, typeInfo.ext)
	fullPath := filepath.Join(imagesDir, filename)

	// Write file
	if err := os.WriteFile(fullPath, decoded, 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save image"})
	}

	log.Printf("[images] Saved image %s (%d bytes)", filename, len(decoded))

	return c.JSON(http.StatusOK, ImageUploadResponse{
		Path:     "/data/images/" + filename,
		Filename: filename,
	})
}

// detectImageType checks magic bytes to verify image type
func detectImageType(data []byte) string {
	if len(data) < 8 {
		return ""
	}

	// Check PNG
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 {
		return "image/png"
	}

	// Check JPEG
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}

	// Check GIF
	if len(data) >= 6 {
		header := string(data[0:6])
		if header == "GIF87a" || header == "GIF89a" {
			return "image/gif"
		}
	}

	// Check WebP
	if len(data) >= 12 {
		if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
	}

	return ""
}

// randomString generates a random alphanumeric string of given length
func randomString(length int) string {
	rand.Seed(time.Now().UnixNano())
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
```

**Step 2: Commit**

```bash
git add backend/api/images.go
git commit -m "feat(api): add image upload API handler"
```

---

## Task 3: Register image routes in main.go

**Files:**
- Modify: `backend/main.go:175-181`

**Step 1: Add ImagesHandler and routes**

Find the location after Markdown Translation API routes (around line 181), add:

```go
	// Image Upload API
	imagesH := &api.ImagesHandler{
		DataDir: cfg.DataDir,
	}
	e.POST("/api/images/upload", imagesH.Upload, middleware.BodyLimit("15M"))
```

**Step 2: Verify /data/* route already serves images**

The existing `/data/*` route at line 72-99 already serves files from `cfg.DataDir`. Images stored in `cache/images/` will be accessible via `/data/images/filename`.

**Step 3: Commit**

```bash
git add backend/main.go
git commit -m "feat: register image upload API route"
```

---

## Task 4: Add SendUserMessageWithImages to session.go

**Files:**
- Modify: `backend/claude/session.go:186-205`

**Step 1: Add ImageData struct after StreamEvent struct (around line 30)**

```go
// ImageData represents an image to send to Claude
type ImageData struct {
	MediaType  string // e.g., "image/png"
	Base64Data string // base64 encoded image data (without prefix)
}
```

**Step 2: Add SendUserMessageWithImages method after SendUserMessage**

```go
// SendUserMessageWithImages sends a message with images to stdin
// Format: {"type":"user","message":{"role":"user","content":[...]}}
func (s *InteractiveSession) SendUserMessageWithImages(content string, images []ImageData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build content array
	msgContent := []map[string]interface{}{}

	// Add images first
	for _, img := range images {
		msgContent = append(msgContent, map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       img.Base64Data,
			},
		})
	}

	// Add text
	if content != "" {
		msgContent = append(msgContent, map[string]interface{}{
			"type": "text",
			"text": content,
		})
	}

	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": msgContent,
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[session] Failed to marshal message: %v", err)
		return err
	}

	_, err = s.stdin.Write(jsonData)
	if err != nil {
		log.Printf("[session] Failed to send message: %v", err)
		return err
	}
	s.stdin.Write([]byte("\n"))

	log.Printf("[session] Sent user message with %d images to session %s", len(images), s.SessionID)
	return nil
}
```

**Step 3: Commit**

```bash
git add backend/claude/session.go
git commit -m "feat(claude): add SendUserMessageWithImages for multimodal messages"
```

---

## Task 5: Update QuerySession to support images

**Files:**
- Modify: `backend/claude/query_pool.go:52-70`

**Step 1: Modify Ask method to accept images**

Change the `Ask` function signature and implementation:

```go
// Ask sends a question with optional images to the session and returns a channel
// that receives events for this specific question.
func (qs *QuerySession) Ask(content string, images []ImageData) (<-chan StreamEvent, error) {
	qs.mu.Lock()
	if qs.turnCh != nil {
		qs.mu.Unlock()
		return nil, fmt.Errorf("another question is already in progress")
	}
	ch := make(chan StreamEvent, 100)
	qs.turnCh = ch
	qs.lastAsk = time.Now()
	qs.mu.Unlock()

	if len(images) > 0 {
		if err := qs.session.SendUserMessageWithImages(content, images); err != nil {
			qs.mu.Lock()
			qs.turnCh = nil
			qs.mu.Unlock()
			return nil, err
		}
	} else {
		if err := qs.session.SendUserMessage(content); err != nil {
			qs.mu.Lock()
			qs.turnCh = nil
			qs.mu.Unlock()
			return nil, err
		}
	}

	return ch, nil
}
```

**Step 2: Commit**

```bash
git add backend/claude/query_pool.go
git commit -m "feat(claude): update QuerySession.Ask to support images"
```

---

## Task 6: Update query.go to handle images

**Files:**
- Modify: `backend/api/query.go:24-28`
- Modify: `backend/api/query.go:60-70`
- Modify: `backend/api/query.go:125-142`

**Step 1: Add Images to AskRequest struct**

```go
type AskRequest struct {
	ConversationID uint     `json:"conversationId"`
	Question       string   `json:"question"`
	DocID          uint     `json:"docId,omitempty"`
	Images         []string `json:"images,omitempty"` // NEW: image paths like "/data/images/xxx.png"
}
```

**Step 2: Import needed packages (add to imports at line 3-13)**

```go
import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)
```

**Step 3: Update user message save to include images**

Around line 61-69, change:

```go
	// Save user message with images
	imagesJSON := "[]"
	if len(req.Images) > 0 {
		imagesBytes, _ := json.Marshal(req.Images)
		imagesJSON = string(imagesBytes)
	}
	userMsg := db.ConversationMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        req.Question,
		Images:         imagesJSON,
		CreatedAt:      time.Now(),
	}
	if err := db.DB.Create(&userMsg).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save user message"})
	}
```

**Step 4: Update Ask call to load and send images**

Around line 125-142, add helper function and update the Ask call:

Add helper function before `truncate` function:

```go
// loadImageData loads image file and converts to ImageData for Claude
func loadImageData(dataDir string, imagePath string) (claude.ImageData, error) {
	// imagePath is like "/data/images/xxx.png"
	// Convert to actual file path
	if !strings.HasPrefix(imagePath, "/data/") {
		return claude.ImageData{}, fmt.Errorf("invalid image path: %s", imagePath)
	}
	relPath := strings.TrimPrefix(imagePath, "/data/")
	fullPath := filepath.Join(dataDir, relPath)

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return claude.ImageData{}, fmt.Errorf("failed to read image: %w", err)
	}

	// Detect media type from extension
	ext := strings.ToLower(filepath.Ext(fullPath))
	mediaType := "image/png" // default
	switch ext {
	case ".jpg", ".jpeg":
		mediaType = "image/jpeg"
	case ".gif":
		mediaType = "image/gif"
	case ".webp":
		mediaType = "image/webp"
	}

	return claude.ImageData{
		MediaType:  mediaType,
		Base64Data: base64.StdEncoding.EncodeToString(data),
	}, nil
}
```

Update the Ask call (around line 125):

```go
	// Load images if provided
	var imageData []claude.ImageData
	for _, imgPath := range req.Images {
		img, err := loadImageData(h.DataDir, imgPath)
		if err != nil {
			log.Printf("[query] Failed to load image %s: %v", imgPath, err)
			continue
		}
		imageData = append(imageData, img)
	}

	// Send question to session and get turn channel
	turnCh, err := qs.Ask(req.Question, imageData)
	if err != nil {
		// ... existing error handling ...
	}
```

Also update the retry Ask call (around line 138):

```go
		turnCh, err = qs.Ask(req.Question, imageData)
```

**Step 5: Commit**

```bash
git add backend/api/query.go
git commit -m "feat(api): handle images in query ask endpoint"
```

---

## Task 7: Add frontend image upload API

**Files:**
- Modify: `frontend/src/api.ts`

**Step 1: Add uploadImage function at the end of api.ts**

```typescript
// Image Upload API
export async function uploadImage(data: string, type: string): Promise<{ path: string; filename: string }> {
  const res = await fetch(`${API_BASE}/images/upload`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ data, type }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to upload image')
  }
  return res.json()
}
```

**Step 2: Update AskRequest in types.ts**

Add to `frontend/src/types.ts`:

```typescript
export interface AskRequest {
  conversationId?: number
  question: string
  docId?: number
  images?: string[]  // NEW: image paths
}

export interface ImageUploadResponse {
  path: string
  filename: string
}
```

**Step 3: Commit**

```bash
git add frontend/src/api.ts frontend/src/types.ts
git commit -m "feat(frontend): add image upload API and update AskRequest type"
```

---

## Task 8: Update ChatView UI - add image upload area

**Files:**
- Modify: `frontend/src/components/ChatView.tsx`

**Step 1: Add state for pending images**

Add after other useState declarations (around line 50):

```typescript
  const [pendingImages, setPendingImages] = useState<string[]>([])  // image paths waiting to be sent
```

**Step 2: Add image upload handler functions**

Add after handleSend function (around line 230):

```typescript
  // Handle image upload from file input or paste
  const handleImageUpload = useCallback(async (file: File) => {
    // Check file type
    const allowedTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']
    if (!allowedTypes.includes(file.type)) {
      alert(t('chatView.imageTypeError'))
      return
    }

    // Check file size (10MB)
    if (file.size > 10 * 1024 * 1024) {
      alert(t('chatView.imageSizeError'))
      return
    }

    // Read as base64
    const reader = new FileReader()
    reader.onload = async (e) => {
      const base64 = e.target?.result as string
      try {
        const type = file.type.split('/')[1] // png, jpeg, gif, webp
        const result = await uploadImage(base64, type)
        setPendingImages(prev => [...prev, result.path])
      } catch (err) {
        console.error('Failed to upload image:', err)
        alert(t('chatView.imageUploadError'))
      }
    }
    reader.readAsDataURL(file)
  }, [t])

  // Handle paste event
  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const items = e.clipboardData.items
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (file) {
          handleImageUpload(file)
        }
      }
    }
  }, [handleImageUpload])

  // Handle file input change
  const handleFileInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files) {
      for (const file of files) {
        handleImageUpload(file)
      }
    }
    e.target.value = ''  // Reset input
  }, [handleImageUpload])

  // Remove pending image
  const handleRemoveImage = useCallback((index: number) => {
    setPendingImages(prev => prev.filter((_, i) => i !== index))
  }, [])
```

**Step 3: Update handleSend to include images**

Modify the askQuestion call to include images:

```typescript
      await askQuestion(
        {
          conversationId: currentConversationId || 0,
          question: input,
          images: pendingImages,  // NEW
        },
        // ... rest unchanged
      )
```

And clear pending images after sending:

```typescript
    setPendingImages([])  // Clear images after sending
```

**Step 4: Commit**

```bash
git add frontend/src/components/ChatView.tsx
git commit -m "feat(frontend): add image upload handlers to ChatView"
```

---

## Task 9: Update ChatView UI - add image preview area

**Files:**
- Modify: `frontend/src/components/ChatView.tsx`

**Step 1: Add image preview section in the render**

Find the input area (around line 430), add image preview section before the input:

```tsx
          {/* Pending images preview */}
          {pendingImages.length > 0 && (
            <div className="flex gap-2 p-2 bg-gray-50 rounded-lg mb-2">
              {pendingImages.map((path, index) => (
                <div key={path} className="relative">
                  <img
                    src={path}
                    alt={`pending-${index}`}
                    className="w-16 h-16 object-cover rounded cursor-pointer hover:opacity-80"
                    onClick={() => setEnlargedImage(path)}
                  />
                  <button
                    onClick={() => handleRemoveImage(index)}
                    className="absolute -top-1 -right-1 w-5 h-5 bg-red-500 text-white rounded-full text-xs hover:bg-red-600"
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
          )}
```

**Step 2: Add image enlargement modal state**

Add state:

```typescript
  const [enlargedImage, setEnlargedImage] = useState<string | null>(null)
```

**Step 3: Add enlargement modal component**

Add before the closing `</div>` of the main container:

```tsx
      {/* Image enlargement modal */}
      {enlargedImage && (
        <div
          className="fixed inset-0 bg-black bg-opacity-75 flex items-center justify-center z-50"
          onClick={() => setEnlargedImage(null)}
        >
          <button
            className="absolute top-4 right-4 w-8 h-8 bg-white text-black rounded-full text-lg hover:bg-gray-200"
            onClick={() => setEnlargedImage(null)}
          >
            ×
          </button>
          <img
            src={enlargedImage}
            alt="enlarged"
            className="max-w-[90%] max-h-[90%] object-contain"
          />
        </div>
      )}
```

**Step 4: Commit**

```bash
git add frontend/src/components/ChatView.tsx
git commit -m "feat(frontend): add image preview and enlargement modal"
```

---

## Task 10: Add upload button and paste listener to input area

**Files:**
- Modify: `frontend/src/components/ChatView.tsx`

**Step 1: Add upload button and paste listener to input**

Find the input element (around line 440), add `onPaste` handler:

```tsx
            <input
              ref={inputRef}
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              onPaste={handlePaste}  // NEW
              placeholder={t('chatView.inputPlaceholder')}
              disabled={streaming}
              className="flex-1 px-4 py-3 border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-500"
            />
```

**Step 2: Add upload button before send/stop button**

```tsx
            {/* Upload button */}
            <input
              type="file"
              accept="image/png,image/jpeg,image/gif,image/webp"
              onChange={handleFileInputChange}
              className="hidden"
              id="image-upload-input"
            />
            <button
              onClick={() => document.getElementById('image-upload-input')?.click()}
              disabled={streaming}
              className="px-4 py-3 bg-gray-100 text-gray-600 rounded-lg hover:bg-gray-200 transition-colors disabled:bg-gray-300 disabled:text-gray-500"
              title={t('chatView.uploadImage')}
            >
              📎
            </button>
```

**Step 3: Commit**

```bash
git add frontend/src/components/ChatView.tsx
git commit -m "feat(frontend): add upload button and paste listener"
```

---

## Task 11: Display images in message history

**Files:**
- Modify: `frontend/src/components/ChatView.tsx`

**Step 1: Parse images from message**

The Message type needs an images field. First update `frontend/src/types.ts`:

```typescript
export interface Message {
  id: number
  conversationId: number
  role: 'user' | 'assistant' | 'system'
  content: string
  images?: string[]  // NEW: JSON array parsed
  contextDocIds: string
  createdAt: string
}
```

**Step 2: Update message rendering to show images**

Find the message rendering section. For user messages, show images:

```tsx
            {msg.role === 'user' && msg.images && msg.images.length > 0 && (
              <div className="flex gap-2 mb-2">
                {msg.images.map((imgPath, idx) => (
                  <img
                    key={idx}
                    src={imgPath}
                    alt={`image-${idx}`}
                    className="w-20 h-20 object-cover rounded cursor-pointer hover:opacity-80"
                    onClick={() => setEnlargedImage(imgPath)}
                  />
                ))}
              </div>
            )}
```

**Step 3: Parse images JSON when loading messages**

In the effect that loads messages, parse the images field:

```typescript
      const parsedMessages = messagesData.map((m: any) => ({
        ...m,
        images: m.images ? JSON.parse(m.images) : []
      }))
```

**Step 4: Commit**

```bash
git add frontend/src/types.ts frontend/src/components/ChatView.tsx
git commit -m "feat(frontend): display images in message history"
```

---

## Task 12: Add i18n translations for image errors

**Files:**
- Modify: `frontend/src/locales/en.json`
- Modify: `frontend/src/locales/zh.json`

**Step 1: Add English translations**

```json
  "chatView": {
    ...
    "imageTypeError": "Unsupported image type. Allowed: PNG, JPEG, GIF, WebP",
    "imageSizeError": "Image too large. Maximum size: 10MB",
    "imageUploadError": "Failed to upload image",
    "uploadImage": "Upload image"
  }
```

**Step 2: Add Chinese translations**

```json
  "chatView": {
    ...
    "imageTypeError": "不支持的图片格式。允许: PNG, JPEG, GIF, WebP",
    "imageSizeError": "图片过大。最大限制: 10MB",
    "imageUploadError": "图片上传失败",
    "uploadImage": "上传图片"
  }
```

**Step 3: Commit**

```bash
git add frontend/src/locales/en.json frontend/src/locales/zh.json
git commit -m "feat(i18n): add image-related translations"
```

---

## Task 13: Build and test

**Step 1: Build backend**

```bash
cd backend && go build -o llm-knowledge .
```

**Step 2: Build frontend**

```bash
cd frontend && npm run build
```

**Step 3: Run server and test**

```bash
./backend/llm-knowledge -port 9999
```

Test flow:
1. Open http://localhost:9999/chat
2. Paste an image (Ctrl+V) or click upload button
3. Verify thumbnail appears
4. Send message with image
5. Verify image is displayed in history
6. Click image to enlarge

**Step 4: Final commit**

```bash
git add backend/llm-knowledge frontend/dist
git commit -m "feat: chat image support complete"
```

---

## Summary

| Task | Description |
|------|-------------|
| 1 | Add Images field to DB model |
| 2 | Create image upload API handler |
| 3 | Register image routes in main.go |
| 4 | Add SendUserMessageWithImages to session.go |
| 5 | Update QuerySession to support images |
| 6 | Update query.go to handle images |
| 7 | Add frontend image upload API |
| 8 | Add image upload handlers to ChatView |
| 9 | Add image preview and enlargement modal |
| 10 | Add upload button and paste listener |
| 11 | Display images in message history |
| 12 | Add i18n translations |
| 13 | Build and test |