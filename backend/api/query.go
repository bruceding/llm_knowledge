package api

import (
	"context"
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

	// Use context.Background() — request context gets cancelled when handler returns,
	// which would kill the Claude subprocess via exec.CommandContext.
	ctx := context.Background()
	var qs *claude.QuerySession
	var err error

	qs = h.Pool.Get(req.ConversationID)
	contextLost := false
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
			contextLost = true
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

	// Build the actual message to send; if context was lost, prepend history
	messageToSend := req.Message
	if contextLost {
		var historyMsgs []db.ConversationMessage
		if err := db.DB.Where("conversation_id = ? AND role IN ? AND id != ?", req.ConversationID, []string{"user", "assistant"}, userMsg.ID).
			Order("created_at DESC").Limit(20).Find(&historyMsgs).Error; err == nil && len(historyMsgs) > 0 {
			// Reverse to chronological order (queried DESC for limit)
			for i, j := 0, len(historyMsgs)-1; i < j; i, j = i+1, j-1 {
				historyMsgs[i], historyMsgs[j] = historyMsgs[j], historyMsgs[i]
			}
			var sb strings.Builder
			sb.WriteString("[以下是本对话的历史消息]\n")
			for _, hm := range historyMsgs {
				if hm.Role == "user" {
					sb.WriteString(fmt.Sprintf("用户: %s\n", hm.Content))
				} else {
					sb.WriteString(fmt.Sprintf("助手: %s\n", hm.Content))
				}
			}
			sb.WriteString("\n[用户新消息]\n")
			sb.WriteString(req.Message)
			messageToSend = sb.String()
		}
	}

	// Send question to session with message ID for saving assistant reply
	_, err = qs.Ask(messageToSend, userMsg.ID, imageData)
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
		_, err = qs.Ask(messageToSend, userMsg.ID, imageData)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to ask question"})
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"status":      "sent",
		"messageId":   userMsg.ID,
		"sessionId":   qs.SessionID(),
		"contextLost": contextLost,
	})
}

// ResumeConversationRequest represents a request to resume/activate a conversation
type ResumeConversationRequest struct {
	ConversationID uint `json:"conversationId"`
}

// ResumeConversation activates a conversation session for SSE streaming.
// This must be called before establishing SSE connection or sending messages.
// POST /api/query/resume
func (h *QueryHandler) ResumeConversation(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req ResumeConversationRequest
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

	// Build system prompt
	systemPrompt := h.buildSystemPrompt(0)

	// Use context.Background() for session creation
	ctx := context.Background()

	// Atomically: remove old session if exists, then get or create new one
	// This ensures clean session state when resuming
	h.Pool.Remove(req.ConversationID)

	qs, err := h.Pool.GetOrCreate(ctx, req.ConversationID, systemPrompt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
	}

	// Update session_id in database
	newSessionID := qs.SessionID()
	if newSessionID != conv.SessionID {
		db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("session_id", newSessionID)
	}

	return c.JSON(http.StatusOK, echo.Map{
		"status":    "resumed",
		"sessionId": qs.SessionID(),
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

	// Check if an active session already exists in the pool
	qs := h.Pool.Get(convID)
	if qs == nil && conv.SessionID != "" {
		// No active session but conversation has a previous session_id — try resume.
		// Resume is faster than creating a new session because Claude CLI caches context.
		bgCtx := context.Background()
		systemPrompt := h.buildSystemPrompt(0)
		resumedQs, err := h.Pool.ResumeSession(bgCtx, convID, conv.SessionID, systemPrompt)
		if err != nil {
			log.Printf("[query] Resume failed for conversation %d (%v), will create new session", convID, err)
		} else {
			qs = resumedQs
			// Update session_id in database if changed
			if newSID := qs.SessionID(); newSID != conv.SessionID {
				db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSID)
			}
		}
	}

	// Flush SSE headers immediately so the frontend sees the connection open quickly.
	// If no session was found/resumed, we create one below.
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	writeSSE := func(data map[string]interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData)
		flusher.Flush()
	}

	if qs == nil {
		// No session yet — notify frontend that we're initializing
		writeSSE(echo.Map{
			"type":           "session_initializing",
			"conversationId": convID,
		})

		// Create session (starts Claude CLI process, but no longer blocks on init msg + drain)
		bgCtx := context.Background()
		systemPrompt := h.buildSystemPrompt(0)
		var err error
		qs, err = h.Pool.GetOrCreate(bgCtx, convID, systemPrompt)
		if err != nil {
			writeSSE(echo.Map{
				"type":           "error",
				"conversationId": convID,
				"error":          "failed to create session",
			})
			return nil
		}

		// Update session_id in database
		if newSID := qs.SessionID(); newSID != conv.SessionID {
			db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSID)
		}
	}

	// Always notify frontend that session is ready (either existing or just created)
	writeSSE(echo.Map{
		"type":           "session_ready",
		"conversationId": convID,
	})

	// Mark SSE connection (reject if too many concurrent connections)
	if !qs.SSEConnect() {
		writeSSE(echo.Map{
			"type":           "error",
			"conversationId": convID,
			"error":          "too many concurrent SSE connections",
		})
		return nil
	}
	defer qs.SSEDisconnect()

	// Capture streaming content BEFORE subscribing to avoid duplication.
	if content := qs.StreamingContent(); len(content) > 0 {
		writeSSE(echo.Map{
			"type":           "full",
			"conversationId": convID,
			"content":        content,
		})
	}

	// Subscribe to session events AFTER capturing content
	eventCh := qs.Subscribe()
	defer qs.Unsubscribe(eventCh)

	// Stream events using select to detect client disconnect promptly
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				// Channel closed (session terminated)
				return nil
			}
			// Skip system hook events
			if evt.Type == "system" && (evt.Subtype == "hook_started" || evt.Subtype == "hook_response") {
				continue
			}

			// Send event to SSE; check write error to detect client disconnect
			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", data); err != nil {
				return nil
			}
			flusher.Flush()

			// Stop on error (but not error_during_execution which is from interrupt)
			if evt.Type == "error" && evt.Subtype != "error_during_execution" {
				return nil
			}

		case <-c.Request().Context().Done():
			// Client disconnected
			return nil
		}
	}
}

// Status returns whether a conversation's session is active and currently thinking.
// GET /api/query/status?conversationId=xxx
func (h *QueryHandler) Status(c echo.Context) error {
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

	qs := h.Pool.Get(convID)
	status := "idle"
	streamingContent := ""
	if qs != nil {
		status = qs.Status()
		if status == "streaming" {
			streamingContent = qs.StreamingContent()
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"status":           status,
		"streamingContent": streamingContent,
	})
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
