package api

import (
	"context"
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// QueryHandler handles query/QA operations with SSE streaming
type QueryHandler struct {
	DataDir  string
	ClaudeBin string
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
	if convID == 0 {
		conv := db.Conversation{
			Title:     truncate(req.Question, 50),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := db.DB.Create(&conv).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create conversation"})
		}
		convID = conv.ID
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

	// Get history context (last 10 messages)
	var history []db.ConversationMessage
	db.DB.Where("conversation_id = ?", convID).
		Order("created_at desc").
		Limit(10).
		Find(&history)

	// Reverse to get chronological order
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	// Build prompt with wiki context
	prompt, err := h.buildQueryPrompt(history, req.Question, req.DocID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to build prompt"})
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

	// Create Claude client and channel for streaming
	claudeClient := claude.NewClientWithPath(h.ClaudeBin)
	eventCh := make(chan claude.StreamEvent)

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

	// Start Claude in goroutine
	go func() {
		defer close(eventCh)
		if err := claudeClient.Send(ctx, prompt, eventCh); err != nil {
			// Send error event
			eventCh <- claude.StreamEvent{
				Type:  "error",
				Error: err.Error(),
			}
		}
	}()

	var fullContent strings.Builder

	// Stream events to client
	for evt := range eventCh {
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
	db.DB.Create(&assistantMsg)

	// Update conversation timestamp
	db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())

	return nil
}

// buildQueryPrompt constructs the prompt for Claude with wiki context and history
func (h *QueryHandler) buildQueryPrompt(history []db.ConversationMessage, question string, docID uint) (string, error) {
	var prompt strings.Builder

	// System prompt
	prompt.WriteString("你是一个知识库助手。请根据提供的上下文和对话历史回答用户问题。\n\n")

	// Add wiki context
	wikiContext, err := h.getWikiContext(docID)
	if err == nil && wikiContext != "" {
		prompt.WriteString("## 知识库上下文\n\n")
		prompt.WriteString(wikiContext)
		prompt.WriteString("\n\n")
	}

	// Add conversation history
	if len(history) > 0 {
		prompt.WriteString("## 对话历史\n\n")
		for _, msg := range history {
			switch msg.Role {
			case "user":
				prompt.WriteString(fmt.Sprintf("用户: %s\n", msg.Content))
			case "assistant":
				prompt.WriteString(fmt.Sprintf("助手: %s\n", msg.Content))
			}
		}
		prompt.WriteString("\n")
	}

	// Add current question
	prompt.WriteString("## 当前问题\n\n")
	prompt.WriteString(question)
	prompt.WriteString("\n\n请根据上述上下文回答问题。如果上下文中没有相关信息，请说明这一点。")

	return prompt.String(), nil
}

// getWikiContext retrieves relevant wiki content for context
func (h *QueryHandler) getWikiContext(docID uint) (string, error) {
	var context strings.Builder

	// If docID is provided, focus on that document's wiki page
	if docID > 0 {
		var doc db.Document
		if err := db.DB.First(&doc, docID).Error; err == nil && doc.WikiPath != "" {
			wikiPath := filepath.Join(h.DataDir, doc.WikiPath)
			if content, err := os.ReadFile(wikiPath); err == nil {
				context.WriteString(fmt.Sprintf("### 文档: %s\n\n", doc.Title))
				context.WriteString(string(content))
				context.WriteString("\n\n")
			}
		}
	}

	// Read wiki index for general context
	indexPath := filepath.Join(h.DataDir, "wiki", "index.md")
	if content, err := os.ReadFile(indexPath); err == nil {
		context.WriteString("### 知识库索引\n\n")
		context.WriteString(string(content))
		context.WriteString("\n")
	}

	return context.String(), nil
}

// truncate shortens a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}