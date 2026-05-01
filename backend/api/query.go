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

// CreateConversationRequest represents the request for creating a new conversation
type CreateConversationRequest struct {
	Title string `json:"title"`
	DocID uint   `json:"docId,omitempty"`
}

// CreateConversation creates a new conversation and returns its ID
// POST /api/query/conversation
func (h *QueryHandler) CreateConversation(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req CreateConversationRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	title := req.Title
	if title == "" {
		title = "New Chat"
	}

	conv := db.Conversation{
		Title:     truncate(title, 100),
		UserID:    userId,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.DB.Create(&conv).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create conversation"})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"conversationId": conv.ID,
		"title":          conv.Title,
	})
}

// QueryMessageRequest represents a user message request for query chat
type QueryMessageRequest struct {
	ConversationID uint     `json:"conversationId"`
	Message        string   `json:"message"`
	DocID          uint     `json:"docId,omitempty"`
	Images         []string `json:"images,omitempty"`
}

// Message sends a user message to the session and saves it to DB
// POST /api/query/message
func (h *QueryHandler) Message(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req QueryMessageRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "message is required"})
	}

	if req.ConversationID == 0 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "conversationId is required"})
	}

	// Get conversation and verify ownership
	var conv db.Conversation
	if err := db.DB.Where("id = ? AND user_id = ?", req.ConversationID, userId).First(&conv).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
	}

	// Save user message with images
	imagesJSON := "[]"
	if len(req.Images) > 0 {
		imagesBytes, _ := json.Marshal(req.Images)
		imagesJSON = string(imagesBytes)
	}
	userMsg := db.ConversationMessage{
		ConversationID: req.ConversationID,
		Role:           "user",
		Content:        req.Message,
		Images:         imagesJSON,
		CreatedAt:      time.Now(),
	}
	if err := db.DB.Create(&userMsg).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save user message"})
	}

	// Update conversation timestamp and title if first message
	db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("updated_at", time.Now())
	if conv.Title == "New Chat" {
		newTitle := truncate(req.Message, 50)
		db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("title", newTitle)
	}

	// Build system prompt
	systemPrompt := h.buildSystemPrompt(req.DocID)

	// Get or create session
	ctx := c.Request().Context()
	var qs *claude.QuerySession
	var err error

	qs = h.Pool.Get(req.ConversationID)
	if qs != nil {
		// Active session exists, reuse it
	} else if conv.SessionID != "" {
		// Resume previous session
		log.Printf("[query] No active session for conversation %d, resuming session %s", req.ConversationID, conv.SessionID)
		qs, err = h.Pool.ResumeSession(ctx, req.ConversationID, conv.SessionID, systemPrompt)
		if err != nil {
			log.Printf("[query] Resume failed (%v), creating fresh session", err)
			qs, err = h.Pool.GetOrCreate(ctx, req.ConversationID, systemPrompt)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
			}
		}
	} else {
		// Create new session
		qs, err = h.Pool.GetOrCreate(ctx, req.ConversationID, systemPrompt)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
		}
	}

	// Update session_id in conversation if changed
	newSessionID := qs.SessionID()
	if newSessionID != conv.SessionID {
		db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("session_id", newSessionID)
	}

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

	// Send question to session with message ID for saving assistant reply
	_, err = qs.Ask(req.Message, userMsg.ID, imageData)
	if err != nil {
		log.Printf("[query] Failed to ask question: %v", err)
		// Session might be dead, try to recreate
		h.Pool.Remove(req.ConversationID)
		qs, err = h.Pool.GetOrCreate(ctx, req.ConversationID, systemPrompt)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to recreate session"})
		}
		// Update session_id
		if sid := qs.SessionID(); sid != newSessionID {
			db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("session_id", sid)
		}
		_, err = qs.Ask(req.Message, userMsg.ID, imageData)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to ask question"})
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"status":       "sent",
		"messageId":    userMsg.ID,
		"sessionId":    qs.SessionID(),
	})
}

// Stream handles SSE streaming for query chat - continuous connection
// GET /api/query/stream?conversationId=xxx
func (h *QueryHandler) Stream(c echo.Context) error {
	userId := GetCurrentUserId(c)

	convIDStr := c.QueryParam("conversationId")
	if convIDStr == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "conversationId is required"})
	}

	convID := parseUint(convIDStr)
	if convID == 0 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid conversationId"})
	}

	// Verify conversation ownership
	var conv db.Conversation
	if err := db.DB.Where("id = ? AND user_id = ?", convID, userId).First(&conv).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
	}

	// Set SSE headers first
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}
	flusher.Flush() // Flush headers immediately so EventSource onopen fires

	// Get or create session (creates Claude process if first connection)
	ctx := c.Request().Context()
	systemPrompt := h.buildSystemPrompt(0)
	qs, err := h.Pool.GetOrCreate(ctx, convID, systemPrompt)
	if err != nil {
		data, _ := json.Marshal(echo.Map{
			"type":           "error",
			"conversationId": convID,
			"error":          "failed to create session",
		})
		fmt.Fprintf(c.Response(), "data: %s\n\n", data)
		flusher.Flush()
		return nil
	}

	// Subscribe to session events
	eventCh := qs.Subscribe()
	defer qs.Unsubscribe(eventCh)

	// Mark SSE connection
	qs.SSEConnect()

	// Stream events
	for evt := range eventCh {
		// Skip system hook events
		if evt.Type == "system" && (evt.Subtype == "hook_started" || evt.Subtype == "hook_response") {
			continue
		}

		// Send event to SSE
		data, _ := json.Marshal(evt)
		fmt.Fprintf(c.Response(), "data: %s\n\n", data)
		flusher.Flush()

		// On result, save assistant message to DB (only if has content)
		if evt.Type == "result" && evt.Subtype != "error_during_execution" && evt.ResultMessageID > 0 && evt.ResultFullContent != "" {
			assistantMsg := db.ConversationMessage{
				ConversationID: convID,
				Role:           "assistant",
				Content:        evt.ResultFullContent,
				CreatedAt:      time.Now(),
			}
			if err := db.DB.Create(&assistantMsg).Error; err != nil {
				log.Printf("[query] Failed to save assistant message: %v", err)
			}
			// Update conversation timestamp
			db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())
		}

		// On interrupt (error_during_execution), don't save empty content
		// Frontend will handle showing "[Stopped]"

		// Stop on error (but not error_during_execution which is from interrupt)
		if evt.Type == "error" && evt.Subtype != "error_during_execution" {
			break
		}

		// Check if client disconnected
		select {
		case <-ctx.Done():
			qs.SSEDisconnect()
			return nil
		default:
		}
	}

	qs.SSEDisconnect()
	return nil
}

// InterruptRequest represents an interrupt request
type InterruptRequest struct {
	ConversationID uint `json:"conversationId"`
}

// Interrupt sends an interrupt signal to stop the current turn
// POST /api/query/interrupt
func (h *QueryHandler) Interrupt(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req InterruptRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	if req.ConversationID == 0 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "conversationId is required"})
	}

	// Verify conversation ownership
	var conv db.Conversation
	if err := db.DB.Where("id = ? AND user_id = ?", req.ConversationID, userId).First(&conv).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
	}

	// Get session
	qs := h.Pool.Get(req.ConversationID)
	if qs == nil {
		return c.JSON(http.StatusOK, echo.Map{"status": "no_active_session"})
	}

	// Send interrupt
	if err := qs.Interrupt(); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to send interrupt"})
	}

	return c.JSON(http.StatusOK, echo.Map{"status": "interrupted"})
}

// ListConversations returns all conversations ordered by most recent first
// GET /api/conversations
func (h *QueryHandler) ListConversations(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var conversations []db.Conversation
	if err := db.DB.Where("user_id = ?", userId).Order("updated_at DESC").Find(&conversations).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to list conversations"})
	}
	return c.JSON(http.StatusOK, conversations)
}

// GetConversationMessages returns all messages for a specific conversation
// GET /api/conversations/:id/messages
func (h *QueryHandler) GetConversationMessages(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	// Verify conversation ownership
	var conv db.Conversation
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&conv).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
	}

	var messages []db.ConversationMessage
	if err := db.DB.Where("conversation_id = ?", id).Order("created_at ASC").Find(&messages).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get messages"})
	}
	return c.JSON(http.StatusOK, messages)
}

// DeleteConversation deletes a conversation and its messages
// DELETE /api/conversations/:id
func (h *QueryHandler) DeleteConversation(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	convID := parseUint(id)
	if convID == 0 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid conversation id"})
	}

	// Verify conversation ownership
	var conv db.Conversation
	if err := db.DB.Where("id = ? AND user_id = ?", convID, userId).First(&conv).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "conversation not found"})
	}

	// Remove session from pool if exists
	h.Pool.Remove(convID)

	// Delete messages
	if err := db.DB.Where("conversation_id = ?", convID).Delete(&db.ConversationMessage{}).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to delete messages"})
	}

	// Delete conversation
	if err := db.DB.Delete(&db.Conversation{}, convID).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to delete conversation"})
	}

	return c.JSON(http.StatusOK, echo.Map{"status": "deleted", "conversationId": convID})
}

// buildSystemPrompt constructs the system prompt pointing to wiki file paths.
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

// parseUint parses a string to uint
func parseUint(s string) uint {
	var result uint
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + uint(c-'0')
		} else {
			return 0
		}
	}
	return result
}
