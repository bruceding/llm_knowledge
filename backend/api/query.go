package api

import (
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"
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
	ConversationID uint   `json:"conversationId"`
	Question       string `json:"question"`
	DocID          uint   `json:"docId,omitempty"` // Optional: focus on specific document
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

	// Save user message
	userMsg := db.ConversationMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        req.Question,
		CreatedAt:      time.Now(),
	}
	if err := db.DB.Create(&userMsg).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save user message"})
	}

	// Build system prompt (wiki paths only, Claude reads files itself)
	systemPrompt := h.buildSystemPrompt(req.DocID)

	// Get or create session
	ctx := c.Request().Context()
	qs, err := h.Pool.GetOrCreate(ctx, convID, systemPrompt)
	if err != nil {
		// If session creation failed and we have a previous session_id, try resume
		if conv.SessionID != "" {
			log.Printf("[query] Session creation failed, trying resume with session %s: %v", conv.SessionID, err)
			qs, err = h.Pool.ResumeSession(ctx, convID, conv.SessionID, systemPrompt)
			if err != nil {
				log.Printf("[query] Resume also failed: %v", err)
				return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create or resume session"})
			}
		} else {
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

	// Send question to session and get turn channel
	turnCh, err := qs.Ask(req.Question)
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
		turnCh, err = qs.Ask(req.Question)
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

// truncate shortens a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
