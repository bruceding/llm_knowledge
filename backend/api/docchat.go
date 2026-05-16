package api

import (
	"context"
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

// DocChatHandler handles document-specific chat operations
type DocChatHandler struct {
	Pool    *claude.SessionPool
	DataDir string
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
	docInfo := fmt.Sprintf("文档标题: %s。原始文件路径: %s。相关 wiki 文件在 wiki/ 目录下。", doc.Title, StripUserPrefix(doc.RawPath))

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
	writePing := func() {
		fmt.Fprint(c.Response(), ": ping\n\n")
		flusher.Flush()
	}

	writeSSE(echo.Map{
		"type":  "session_initializing",
		"docId": docId,
	})

	// Start session with ownership. Pass the document's stored ChatSessionID so
	// Claude can --resume the prior conversation when the in-memory session has
	// been cleaned up (120s idle timeout, server restart, SSE drop, etc.).
	// The callback persists the new session_id back to the document row so the
	// next resume uses the freshest id.
	// Use context.Background() — request context gets cancelled when handler returns,
	// which would kill the Claude subprocess via exec.CommandContext.
	userDir := GetUserDir(c)
	docIDForCallback := uint(docId)
	session, err := h.Pool.StartSession(
		context.Background(),
		docInfo,
		userId,
		uint(docId),
		userDir,
		doc.ChatSessionID,
		func(oldID, newID string) {
			if err := db.DB.Model(&db.Document{}).Where("id = ?", docIDForCallback).Update("chat_session_id", newID).Error; err != nil {
				log.Printf("[docchat] Failed to persist chat_session_id for doc %d: %v", docIDForCallback, err)
			}
		},
	)
	if err != nil {
		log.Printf("[docchat] Failed to start session: %v", err)
		writeSSE(echo.Map{"type": "error", "error": "failed to start session"})
		return nil
	}

	// Mark SSE connection (reject if too many concurrent connections)
	if !session.SSEConnect() {
		session.Close()
		writeSSE(echo.Map{"type": "error", "error": "too many concurrent connections"})
		return nil
	}
	defer session.SSEDisconnect()

	// Subscribe to session events (fan-out mechanism)
	eventCh := session.Subscribe()
	defer session.Unsubscribe(eventCh)

	// Send session_id
	writeSSE(echo.Map{
		"type":      "session",
		"sessionId": session.SessionID,
	})

	return streamSSEEvents(c.Request().Context(), claude.NewStreamProcessor(), eventCh, writeSSE, writePing)
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
			"status":       "session_expired",
			"isNewSession": true,
			"message":      "对话已过期，请重新开始",
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
	defer session.SSEDisconnect()

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	writeSSE := func(data map[string]interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData)
		flusher.Flush()
	}
	writePing := func() {
		fmt.Fprint(c.Response(), ": ping\n\n")
		flusher.Flush()
	}

	// Subscribe to session events (fan-out mechanism)
	eventCh := session.Subscribe()
	defer session.Unsubscribe(eventCh)

	// Confirm reconnection
	sessionData, _ := json.Marshal(echo.Map{
		"type":        "session",
		"sessionId":   session.SessionID,
		"reconnected": true,
	})
	if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", sessionData); err != nil {
		return nil
	}
	flusher.Flush()

	sp := claude.NewStreamProcessor()
	sp.MarkAsStreamedWithContent(session.StreamingContent())
	return streamSSEEvents(c.Request().Context(), sp, eventCh, writeSSE, writePing)
}

// streamSSEEvents is the shared SSE event loop used by both Stream and Reconnect.
// It reads raw events from eventCh, converts them via StreamProcessor, and writes
// clean SSE events to the client via writeSSE.
// writePing, if non-nil, is invoked on a 20s ticker to emit an SSE comment frame
// that keeps reverse-proxy idle timeouts (nginx default 60s) from closing the
// connection during long pauses between assistant messages.
func streamSSEEvents(ctx context.Context, sp *claude.StreamProcessor, eventCh chan claude.StreamEvent, writeSSE func(map[string]interface{}), writePing func()) error {
	var pingC <-chan time.Time
	if writePing != nil {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		pingC = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pingC:
			writePing()
			continue
		case evt, ok := <-eventCh:
			if !ok {
				return nil
			}

			// Forward session_update directly (bypasses StreamProcessor)
			if evt.Type == "session_update" && evt.SessionID != "" {
				writeSSE(echo.Map{
					"type":      "session",
					"sessionId": evt.SessionID,
				})
				continue
			}

			sseEvent := sp.Process(evt)

			for sp.HasPendingEvents() {
				pending := sp.FlushPending()
				if pending.Type != "" {
					writeSSE(echo.Map{
						"type":      pending.Type,
						"text":      pending.Delta,
						"content":   pending.Content,
						"toolId":    pending.ToolID,
						"toolName":  pending.ToolName,
						"toolInput": pending.ToolInput,
					})
				}
			}

			if sseEvent.Type == "" {
				continue
			}

			switch sseEvent.Type {
			case "delta":
				writeSSE(echo.Map{
					"type": "delta",
					"text": sseEvent.Delta,
				})
			case "full":
				writeSSE(echo.Map{
					"type":    "full",
					"content": sseEvent.Content,
				})
			case "tool_start":
				writeSSE(echo.Map{
					"type":      "tool_start",
					"toolId":    sseEvent.ToolID,
					"toolName":  sseEvent.ToolName,
					"toolInput": sseEvent.ToolInput,
				})
			case "tool_input":
				writeSSE(echo.Map{
					"type":      "tool_input",
					"toolId":    sseEvent.ToolID,
					"toolName":  sseEvent.ToolName,
					"toolInput": sseEvent.ToolInput,
				})
			case "tool_end":
				writeSSE(echo.Map{
					"type":   "tool_end",
					"toolId": sseEvent.ToolID,
				})
			case "done":
				writeSSE(echo.Map{
					"type": "done",
				})
				sp.Reset()
			case "error":
				writeSSE(echo.Map{
					"type":  "error",
					"error": sseEvent.Content,
				})
				if evt.Subtype != "error_during_execution" {
					return nil
				}
				sp.Reset()
			}
		}
	}
}
