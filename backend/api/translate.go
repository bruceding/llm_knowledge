package api

import (
	"context"
	"encoding/json"
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// TranslateHandler handles translation operations with SSE streaming
type TranslateHandler struct {
	DataDir  string
	ClaudeBin string
}

// TranslateRequest represents the request body for the Translate endpoint
type TranslateRequest struct {
	DocID      uint   `json:"docId"`
	TargetLang string `json:"targetLang"` // zh, en
}

// validTargetLangs contains supported target languages
var validTargetLangs = map[string]string{
	"zh": "中文",
	"en": "English",
}

// Translate handles SSE streaming translation of a document
func (h *TranslateHandler) Translate(c echo.Context) error {
	var req TranslateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	if req.DocID == 0 {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "docId is required"})
	}

	// Validate target language
	targetLangName, valid := validTargetLangs[req.TargetLang]
	if !valid {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid targetLang, must be 'zh' or 'en'"})
	}

	// Get document
	var doc db.Document
	if err := db.DB.First(&doc, req.DocID).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Check if raw path exists
	if doc.RawPath == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document has no raw content"})
	}

	// Read source content
	rawPath := filepath.Join(h.DataDir, doc.RawPath, "paper.md")
	content, err := os.ReadFile(rawPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": fmt.Sprintf("failed to read source content: %v", err)})
	}

	// Build translation prompt
	prompt := fmt.Sprintf("请将以下内容翻译为%s，保持学术性和专业性，不要添加任何额外的解释或评论：\n\n%s",
		targetLangName, string(content))

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	// Send document info to client first
	docData, _ := json.Marshal(echo.Map{
		"type":       "document",
		"docId":      doc.ID,
		"title":      doc.Title,
		"targetLang": req.TargetLang,
	})
	fmt.Fprintf(c.Response(), "data: %s\n\n", docData)
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

	// Save translated content to file
	translatedFilename := fmt.Sprintf("paper_%s.md", req.TargetLang)
	translatedPath := filepath.Join(h.DataDir, doc.RawPath, translatedFilename)
	if err := os.WriteFile(translatedPath, []byte(fullContent.String()), 0644); err != nil {
		log.Printf("failed to save translated content: %v", err)
		// Send error event to client
		errorData, _ := json.Marshal(echo.Map{
			"type":  "error",
			"error": fmt.Sprintf("failed to save translated content: %v", err),
		})
		fmt.Fprintf(c.Response(), "data: %s\n\n", errorData)
		flusher.Flush()
	} else {
		// Send completion event with file path
		completeData, _ := json.Marshal(echo.Map{
			"type":     "complete",
			"filePath": translatedPath,
		})
		fmt.Fprintf(c.Response(), "data: %s\n\n", completeData)
		flusher.Flush()
	}

	return nil
}