package api

import (
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// DocChatHandler handles document-specific chat operations
type DocChatHandler struct {
	Pool     *claude.SessionPool
	DataDir  string
}

// Stream handles SSE streaming for document chat
// GET /api/doc-chat/stream?docId=xxx
func (h *DocChatHandler) Stream(c echo.Context) error {
	userId := GetCurrentUserId(c)

	docIdStr := c.QueryParam("docId")
	if docIdStr == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "docId is required"})
	}

	docId, err := strconv.ParseUint(docIdStr, 10, 32)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid docId"})
	}

	// Get document info and verify ownership
	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", docId, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Build document context info
	docInfo := fmt.Sprintf("文档标题: %s。原始文件路径: %s。相关 wiki 文件在 wiki/ 目录下。", doc.Title, doc.RawPath)

	// Start new session with ownership
	session, err := h.Pool.StartSession(c.Request().Context(), docInfo, userId, uint(docId))
	if err != nil {
		log.Printf("[docchat] Failed to start session: %v", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to start session"})
	}

	// Mark SSE connection (reject if too many concurrent connections)
	if !session.SSEConnect() {
		session.Close()
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "too many concurrent connections"})
	}

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		session.SSEDisconnect()
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Subscribe to session events (fan-out mechanism)
	eventCh := session.Subscribe()
	defer session.Unsubscribe(eventCh)

	// Send session_id first
	sessionData, _ := json.Marshal(echo.Map{
		"type":      "session",
		"sessionId": session.SessionID,
	})
	if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", sessionData); err != nil {
		session.SSEDisconnect()
		return nil
	}
	flusher.Flush()

	ctx := c.Request().Context()
	skipFirstAssistant := true
	for {
		select {
		case <-ctx.Done():
			session.SSEDisconnect()
			return nil
		case evt, ok := <-eventCh:
			if !ok {
				session.SSEDisconnect()
				return nil
			}

			if evt.Type == "system" && (evt.Subtype == "hook_started" || evt.Subtype == "hook_response") {
				continue
			}

			if evt.Type == "assistant" && skipFirstAssistant {
				skipFirstAssistant = false
				continue
			}

			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", data); err != nil {
				session.SSEDisconnect()
				return nil
			}
			flusher.Flush()

			// Don't disconnect on error_during_execution (interrupt) - keep SSE for multi-turn
			if evt.Type == "error" && evt.Subtype != "error_during_execution" {
				session.SSEDisconnect()
				return nil
			}
		}
	}
}

// MessageRequest represents a user message request
type MessageRequest struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// Message sends a user message to the session
// POST /api/doc-chat/message
func (h *DocChatHandler) Message(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req MessageRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request"})
	}

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "message is required"})
	}

	// Get existing session
	session := h.Pool.GetSession(req.SessionID)
	if session == nil {
		// Session not found - need to start new one
		// Return isNewSession flag so frontend can reset
		return c.JSON(http.StatusOK, echo.Map{
			"status":        "session_expired",
			"isNewSession":  true,
			"message":       "对话已过期，请重新开始",
		})
	}

	// Authorization check: verify session belongs to this user
	if session.OwnerUserID != userId {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "session not found"})
	}

	// Send user message
	if err := session.SendUserMessage(req.Message); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to send message"})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"status":       "sent",
		"isNewSession": false,
	})
}

// Reconnect handles reconnecting to an existing session
// GET /api/doc-chat/reconnect?sessionId=xxx
func (h *DocChatHandler) Reconnect(c echo.Context) error {
	userId := GetCurrentUserId(c)

	sessionId := c.QueryParam("sessionId")
	if sessionId == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "sessionId is required"})
	}

	session := h.Pool.GetSession(sessionId)
	if session == nil {
		return c.JSON(http.StatusGone, echo.Map{"status": "expired"})
	}

	// Authorization check: verify session belongs to this user
	if session.OwnerUserID != userId {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "session not found"})
	}

	// Mark SSE connection (reject if too many concurrent connections)
	if !session.SSEConnect() {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "too many concurrent connections"})
	}

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		session.SSEDisconnect()
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Subscribe to session events (fan-out mechanism)
	eventCh := session.Subscribe()
	defer session.Unsubscribe(eventCh)

	// Confirm reconnection
	sessionData, _ := json.Marshal(echo.Map{
		"type":       "session",
		"sessionId":  session.SessionID,
		"reconnected": true,
	})
	if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", sessionData); err != nil {
		session.SSEDisconnect()
		return nil
	}
	flusher.Flush()

	// Keep SSE open for multi-turn (same as Stream)
	ctx := c.Request().Context()
	for {
		select {
		case <-ctx.Done():
			session.SSEDisconnect()
			return nil
		case evt, ok := <-eventCh:
			if !ok {
				session.SSEDisconnect()
				return nil
			}

			if evt.Type == "system" && evt.Subtype != "error" {
				continue
			}

			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", data); err != nil {
				session.SSEDisconnect()
				return nil
			}
			flusher.Flush()

			// Don't disconnect on error_during_execution (interrupt) - keep SSE for multi-turn
			if evt.Type == "error" && evt.Subtype != "error_during_execution" {
				session.SSEDisconnect()
				return nil
			}
		}
	}
}