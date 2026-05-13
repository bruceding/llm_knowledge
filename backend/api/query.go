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
	systemPrompt := h.buildSystemPrompt(req.DocID, userId)

	// Get user directory for session isolation
	userDir := GetUserDir(c)

	// Use context.Background() — request context gets cancelled when handler returns,
	// which would kill the Claude subprocess via exec.CommandContext.
	ctx := context.Background()

	// GetOrResume atomically: reuse existing > resume previous > create new.
	// It also guards against concurrent resume attempts for the same conversation.
	// The onRealSessionID callback updates the DB when the real session_id arrives
	// asynchronously (handles fallback local-xxx IDs).
	contextLost := false
	qs := h.Pool.Get(req.ConversationID)
	if qs == nil {
		var err error
		var source claude.SessionSource
		qs, source, err = h.Pool.GetOrResume(ctx, req.ConversationID, conv.SessionID, systemPrompt, userDir, func(convID uint, newSID string) {
			db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSID)
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
		}
		// If a new session was created (not resumed) and there was a previous real session,
		// the conversation context was lost — warn the user and inject history.
		if source == claude.SourceCreated && conv.SessionID != "" {
			contextLost = true
		}
	}

	// Only write session_id to DB if it's a real ID (not a local-xxx fallback).
	// If it's a fallback, the onSessionID callback will update the DB later.
	if qs.SessionID() != conv.SessionID && !strings.HasPrefix(qs.SessionID(), "local-") {
		db.DB.Model(&db.Conversation{}).Where("id = ?", req.ConversationID).Update("session_id", qs.SessionID())
	}

	// Load images if provided
	var imageData []claude.ImageData
	for _, imgPath := range req.Images {
		img, err := loadImageData(userDir, imgPath)
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
	_, err := qs.Ask(messageToSend, userMsg.ID, imageData)
	if err != nil {
		log.Printf("[query] Failed to ask question: %v", err)
		// Session might be dead, try to recreate
		h.Pool.Remove(req.ConversationID)
		qs, _, err = h.Pool.GetOrResume(ctx, req.ConversationID, "", systemPrompt, userDir, func(convID uint, newSID string) {
			db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSID)
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to recreate session"})
		}
		// Only write real session_id to DB
		if sid := qs.SessionID(); !strings.HasPrefix(sid, "local-") {
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
	systemPrompt := h.buildSystemPrompt(0, userId)

	// Get user directory for session isolation
	userDir := GetUserDir(c)

	// Use context.Background() for session creation
	ctx := context.Background()

	// Remove old session if exists, then get or resume/create atomically
	h.Pool.Remove(req.ConversationID)

	qs, _, err := h.Pool.GetOrResume(ctx, req.ConversationID, conv.SessionID, systemPrompt, userDir, func(convID uint, newSID string) {
		db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newSID)
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create session"})
	}

	// Only write real session_id to DB (not local-xxx fallback)
	newSessionID := qs.SessionID()
	if newSessionID != conv.SessionID && !strings.HasPrefix(newSessionID, "local-") {
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

	// Flush SSE headers immediately so the frontend sees the connection open quickly.
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

		// GetOrResume atomically: try resume first, fall back to new session.
		// This prevents concurrent resume attempts and registers a callback
		// to update the DB when the real session_id arrives.
		bgCtx := context.Background()
		systemPrompt := h.buildSystemPrompt(0, userId)
		userDir := GetUserDir(c)
		var err error
		qs, _, err = h.Pool.GetOrResume(bgCtx, convID, conv.SessionID, systemPrompt, userDir, func(cid uint, newSID string) {
			db.DB.Model(&db.Conversation{}).Where("id = ?", cid).Update("session_id", newSID)
		})
		if err != nil {
			writeSSE(echo.Map{
				"type":           "error",
				"conversationId": convID,
				"error":          "failed to create session",
			})
			return nil
		}

		// Only write real session_id to DB (not local-xxx fallback)
		if newSID := qs.SessionID(); newSID != conv.SessionID && !strings.HasPrefix(newSID, "local-") {
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
	// Initialize StreamProcessor with existing content for SSE reconnect.
	sp := claude.NewStreamProcessor()
	if content := qs.StreamingContent(); len(content) > 0 {
		writeSSE(echo.Map{
			"type":           "full",
			"conversationId": convID,
			"content":        content,
		})
		sp.MarkAsStreamedWithContent(content)
	}

	// Subscribe to session events AFTER capturing content
	eventCh := qs.Subscribe()
	defer qs.Unsubscribe(eventCh)

	// Stream events using select to detect client disconnect promptly.
	// StreamProcessor converts raw events into clean SSE events for frontend.
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				// Channel closed (session terminated)
				return nil
			}

			sseEvent := sp.Process(evt)

			// Flush pending tool events after each Process call
			for sp.HasPendingEvents() {
				pending := sp.FlushPending()
				if pending.Type != "" {
					sseData, _ := json.Marshal(echo.Map{
						"type":           pending.Type,
						"conversationId": convID,
						"text":           pending.Delta,
						"content":        pending.Content,
						"toolId":         pending.ToolID,
						"toolName":       pending.ToolName,
						"toolInput":      pending.ToolInput,
					})
					if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", sseData); err != nil {
						return nil
					}
					flusher.Flush()
				}
			}

			// Skip empty events (filtered system, de-duplicated assistant)
			if sseEvent.Type == "" {
				continue
			}

			switch sseEvent.Type {
			case "delta":
				writeSSE(echo.Map{
					"type":           "delta",
					"conversationId": convID,
					"text":           sseEvent.Delta,
				})
			case "full":
				writeSSE(echo.Map{
					"type":           "full",
					"conversationId": convID,
					"content":        sseEvent.Content,
				})
			case "tool_start":
				writeSSE(echo.Map{
					"type":           "tool_start",
					"conversationId": convID,
					"toolId":         sseEvent.ToolID,
					"toolName":       sseEvent.ToolName,
					"toolInput":      sseEvent.ToolInput,
				})
			case "tool_input":
				writeSSE(echo.Map{
					"type":           "tool_input",
					"conversationId": convID,
					"toolId":         sseEvent.ToolID,
					"toolName":       sseEvent.ToolName,
					"toolInput":      sseEvent.ToolInput,
				})
			case "tool_end":
				writeSSE(echo.Map{
					"type":           "tool_end",
					"conversationId": convID,
					"toolId":         sseEvent.ToolID,
				})
			case "done":
				writeSSE(echo.Map{
					"type":           "done",
					"conversationId": convID,
				})
				sp.Reset() // Prepare for next turn
			case "error":
				writeSSE(echo.Map{
					"type":           "error",
					"conversationId": convID,
					"error":          sseEvent.Content,
				})
				// Fatal errors (non interrupt) close SSE connection
				if evt.Subtype != "error_during_execution" {
					return nil
				}
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
func (h *QueryHandler) buildSystemPrompt(docID uint, userId uint) string {
	var prompt strings.Builder

	prompt.WriteString("你是一个知识库助手。知识库文件在 wiki/ 目录下，wiki/index.md 是索引。请使用 Read、Glob、Grep 等工具读取相关文件回答用户问题。如果文件内容不足以回答，可以使用你自己的知识补充。")

	if docID > 0 {
		var doc db.Document
		if err := db.DB.Where("id = ? AND user_id = ?", docID, userId).First(&doc).Error; err == nil && doc.WikiPath != "" {
			// Strip user prefix since Claude CWD is already in userDir
			wikiRelPath := StripUserPrefix(doc.WikiPath)
			prompt.WriteString(fmt.Sprintf(" 重点关注: %s", wikiRelPath))
		}
	}

	return prompt.String()
}

// loadImageData loads image file and converts to ImageData for Claude
func loadImageData(userDir string, imagePath string) (claude.ImageData, error) {
	// imagePath can be:
	// - "/data/users/1/cache/images/xxx.png" (new format with user prefix)
	// - "/data/cache/images/xxx.png" (old format without user prefix)
	// Convert to actual file path
	if !strings.HasPrefix(imagePath, "/data/") {
		return claude.ImageData{}, fmt.Errorf("invalid image path: %s", imagePath)
	}
	relPath := strings.TrimPrefix(imagePath, "/data/")

	// Strip users/{userId}/ prefix if present, then join with userDir
	// This handles both formats uniformly
	cleanPath := StripUserPrefix(relPath)
	fullPath := filepath.Join(userDir, cleanPath)

	// Path traversal containment check against userDir
	absUserDir, err := filepath.Abs(userDir)
	if err != nil {
		return claude.ImageData{}, fmt.Errorf("path error")
	}
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return claude.ImageData{}, fmt.Errorf("path error")
	}
	// Ensure path is within userDir (with separator to prevent prefix collision)
	if !strings.HasPrefix(absFull, absUserDir+string(filepath.Separator)) && absFull != absUserDir {
		return claude.ImageData{}, fmt.Errorf("access denied")
	}

	// Read file
	data, err := os.ReadFile(absFull)
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
