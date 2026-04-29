package api

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

// QueryHandler handles query/QA operations with SSE streaming
type QueryHandler struct {
	DataDir   string
	ClaudeBin string
	Pool      *claude.QuerySessionPool
}

// AskRequest represents the request body for the Ask endpoint
type AskRequest struct {
	ConversationID uint     `json:"conversationId"`
	Question       string   `json:"question"`
	DocID          uint     `json:"docId,omitempty"` // Optional: focus on specific document
	Images         []string `json:"images,omitempty"`
}

// Ask handles SSE streaming query responses
func (h *QueryHandler) Ask(c echo.Context) error {
	var req AskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	if req.Question == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "question is required"})
	}

	// Create or get conversation
	convID := req.ConversationID
	var conv db.Conversation
	if convID == 0 {
		conv = db.Conversation{
			Title:     truncate(req.Question, 50),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := db.DB.Create(&conv).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create conversation"})
		}
		convID = conv.ID
	} else {
		if err := db.DB.First(&conv, convID).Error; err != nil {
			return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
		}
	}

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

	// Build system prompt (wiki paths only, Claude reads files itself)
	systemPrompt := h.buildSystemPrompt(req.DocID)

	// Get or create session, preferring resume for existing conversations
	ctx := c.Request().Context()
	var qs *claude.QuerySession
	var err error

	// 1. Check if there's an active session in the pool
	qs = h.Pool.Get(convID)
	if qs != nil {
		// Active session exists, reuse it (has full context)
	} else if conv.SessionID != "" {
		// 2. No active session but conversation has a previous SessionID — resume to keep context
		log.Printf("[query] No active session for conversation %d, resuming session %s", convID, conv.SessionID)
		qs, err = h.Pool.ResumeSession(ctx, convID, conv.SessionID, systemPrompt)
		if err != nil {
			// Resume failed, fall back to creating a fresh session
			log.Printf("[query] Resume failed (%v), creating fresh session", err)
			qs, err = h.Pool.GetOrCreate(ctx, convID, systemPrompt)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
			}
		}
	} else {
		// 3. No previous session at all, create a new one
		qs, err = h.Pool.GetOrCreate(ctx, convID, systemPrompt)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
		}
	}

	// Update session_id in conversation if changed
	newSessionID := qs.SessionID()
	if newSessionID != conv.SessionID {
		db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSessionID)
	}

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Send conversation ID to client first
	convData, _ := json.Marshal(echo.Map{"type": "conversation", "conversationId": convID})
	fmt.Fprintf(c.Response(), "data: %s\n\n", convData)
	flusher.Flush()

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
		log.Printf("[query] Failed to ask question: %v", err)
		// Session might be dead, try to recreate
		h.Pool.Remove(convID)
		qs, err = h.Pool.GetOrCreate(ctx, convID, systemPrompt)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to recreate session"})
		}
		// Update session_id
		if sid := qs.SessionID(); sid != newSessionID {
			db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", sid)
		}
		turnCh, err = qs.Ask(req.Question, imageData)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to ask question"})
		}
	}

	// Stream events to client
	var fullContent strings.Builder
	for evt := range turnCh {
		// Skip system events
		if evt.Type == "system" {
			continue
		}

		data, _ := json.Marshal(evt)
		fmt.Fprintf(c.Response(), "data: %s\n\n", data)
		flusher.Flush()

		if evt.Type == "assistant" {
			fullContent.WriteString(evt.Content)
		}
	}

	// Save assistant message
	assistantMsg := db.ConversationMessage{
		ConversationID: convID,
		Role:           "assistant",
		Content:        fullContent.String(),
		CreatedAt:      time.Now(),
	}
	if err := db.DB.Create(&assistantMsg).Error; err != nil {
		log.Printf("failed to save assistant message: %v", err)
	}

	// Update conversation timestamp
	db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())

	return nil
}

// buildSystemPrompt constructs the system prompt pointing to wiki file paths.
// Claude uses Read/Glob/Grep tools to find and read files itself.
func (h *QueryHandler) buildSystemPrompt(docID uint) string {
	var prompt strings.Builder

	prompt.WriteString("你是一个知识库助手。知识库文件在 wiki/ 目录下，wiki/index.md 是索引。请使用 Read、Glob、Grep 等工具读取相关文件回答用户问题。如果文件内容不足以回答，可以使用你自己的知识补充。")

	if docID > 0 {
		var doc db.Document
		if err := db.DB.First(&doc, docID).Error; err == nil && doc.WikiPath != "" {
			prompt.WriteString(fmt.Sprintf(" 重点关注: %s", doc.WikiPath))
		}
	}

	return prompt.String()
}

// ListConversations returns all conversations ordered by most recent first
// GET /api/conversations
func (h *QueryHandler) ListConversations(c echo.Context) error {
	var conversations []db.Conversation
	if err := db.DB.Order("updated_at DESC").Find(&conversations).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to list conversations"})
	}
	return c.JSON(http.StatusOK, conversations)
}

// GetConversationMessages returns all messages for a specific conversation
// GET /api/conversations/:id/messages
func (h *QueryHandler) GetConversationMessages(c echo.Context) error {
	id := c.Param("id")
	var messages []db.ConversationMessage
	if err := db.DB.Where("conversation_id = ?", id).Order("created_at ASC").Find(&messages).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get messages"})
	}
	return c.JSON(http.StatusOK, messages)
}

// loadImageData loads image file and converts to ImageData for Claude
func loadImageData(dataDir string, imagePath string) (claude.ImageData, error) {
	// imagePath is like "/data/cache/images/xxx.png"
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

// truncate shortens a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
