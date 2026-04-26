package api

import (
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"

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
	docId := c.QueryParam("docId")
	if docId == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "docId is required"})
	}

	// Get document info
	var doc db.Document
	if err := db.DB.First(&doc, docId).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Build document context info
	docInfo := fmt.Sprintf("文档标题: %s。原始文件路径: %s。相关 wiki 文件在 wiki/ 目录下。", doc.Title, doc.RawPath)

	// Start new session
	session, err := h.Pool.StartSession(c.Request().Context(), docInfo)
	if err != nil {
		log.Printf("[docchat] Failed to start session: %v", err)
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to start session"})
	}

	// Mark SSE connection
	session.SSEConnect()

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		session.SSEDisconnect()
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Send session_id first
	sessionData, _ := json.Marshal(echo.Map{
		"type":      "session",
		"sessionId": session.SessionID,
	})
	fmt.Fprintf(c.Response(), "data: %s\n\n", sessionData)
	flusher.Flush()

	// Stream events - keep connection open for multi-turn conversations
	// SSE will close when client disconnects or Claude process ends
	skipFirstAssistant := true // Skip initial assistant response from init message
	for evt := range session.Events() {
		// Skip hook system events (keep init for session_id)
		if evt.Type == "system" && (evt.Subtype == "hook_started" || evt.Subtype == "hook_response") {
			continue
		}

		// Skip first assistant response (triggered by init stdin message)
		if evt.Type == "assistant" && skipFirstAssistant {
			skipFirstAssistant = false
			continue
		}

		data, _ := json.Marshal(evt)
		fmt.Fprintf(c.Response(), "data: %s\n\n", data)
		flusher.Flush()

		// Stop on error or process exit signal
		if evt.Type == "error" {
			break
		}
	}

	// Mark SSE disconnect
	session.SSEDisconnect()

	return nil
}

// MessageRequest represents a user message request
type MessageRequest struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// Message sends a user message to the session
// POST /api/doc-chat/message
func (h *DocChatHandler) Message(c echo.Context) error {
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
	sessionId := c.QueryParam("sessionId")
	if sessionId == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "sessionId is required"})
	}

	session := h.Pool.GetSession(sessionId)
	if session == nil {
		// Session expired
		return c.JSON(http.StatusOK, echo.Map{
			"status":       "expired",
			"isNewSession": true,
		})
	}

	// Mark SSE connection
	session.SSEConnect()

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		session.SSEDisconnect()
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Confirm session exists
	sessionData, _ := json.Marshal(echo.Map{
		"type":       "session",
		"sessionId":  session.SessionID,
		"isNewSession": false,
	})
	fmt.Fprintf(c.Response(), "data: %s\n\n", sessionData)
	flusher.Flush()

	// Stream events
	for evt := range session.Events() {
		if evt.Type == "system" && evt.Subtype != "error" {
			continue
		}

		data, _ := json.Marshal(evt)
		fmt.Fprintf(c.Response(), "data: %s\n\n", data)
		flusher.Flush()

		if evt.Type == "result" || evt.Type == "error" {
			break
		}
	}

	session.SSEDisconnect()
	return nil
}