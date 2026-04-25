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
	"os/exec"
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
// For PDF documents, it also generates page images for bilingual view
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

	// For PDF documents, generate page images if not exist
	if doc.SourceType == "pdf" {
		pagesDir := filepath.Join(h.DataDir, doc.RawPath, "pages")
		if _, err := os.Stat(pagesDir); os.IsNotExist(err) {
			// Generate page images
			pdfPath := filepath.Join(h.DataDir, doc.RawPath, "paper.pdf")
			os.MkdirAll(pagesDir, 0755)
			pdftoppmCmd := exec.Command("pdftoppm", "-png", "-r", "100", pdfPath, filepath.Join(pagesDir, "page"))
			if err := pdftoppmCmd.Run(); err != nil {
				log.Printf("[translate] failed to generate page images: %v", err)
			} else {
				// Rename files from page-1.png to page_1.png
				files, _ := os.ReadDir(pagesDir)
				for _, f := range files {
					oldName := f.Name()
					if strings.HasPrefix(oldName, "page-") && strings.HasSuffix(oldName, ".png") {
						pageNum := strings.TrimPrefix(oldName, "page-")
						pageNum = strings.TrimSuffix(pageNum, ".png")
						newName := fmt.Sprintf("page_%s.png", pageNum)
						os.Rename(filepath.Join(pagesDir, oldName), filepath.Join(pagesDir, newName))
					}
				}
			}
		}
	}

	// Read source content
	rawPath := filepath.Join(h.DataDir, doc.RawPath, "paper.md")
	content, err := os.ReadFile(rawPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": fmt.Sprintf("failed to read source content: %v", err)})
	}

	// Build translation prompt with page segmentation for PDF
	var prompt string
	if doc.SourceType == "pdf" {
		// PDF: preserve page structure with --- Page N --- markers
		// First, convert --- Page Break --- to numbered pages
		pageContent := string(content)
		pageNum := 1
		pageContent = strings.Replace(pageContent, "--- Page Break ---", fmt.Sprintf("\n\n--- Page %d ---\n", pageNum), 1)
		for strings.Contains(pageContent, "--- Page Break ---") {
			pageNum++
			pageContent = strings.Replace(pageContent, "--- Page Break ---", fmt.Sprintf("\n\n--- Page %d ---\n", pageNum), 1)
		}

		prompt = fmt.Sprintf(`请将以下PDF内容翻译为%s。

内容已按页面分段，请严格保持页面结构，按以下格式输出：

--- Page 1 ---
[第1页的翻译内容]

--- Page 2 ---
[第2页的翻译内容]

...

翻译要求：
1. 保持学术性和专业性
2. 保持原文的标题层级、段落结构
3. 公式用LaTeX格式保留
4. 不要添加额外解释或评论
5. 每页翻译后必须有 --- Page N --- 标记分隔

原文内容：
%s`, targetLangName, pageContent)
	} else {
		// Non-PDF: simple translation
		prompt = fmt.Sprintf("请将以下内容翻译为%s，保持学术性和专业性，不要添加任何额外的解释或评论：\n\n%s",
			targetLangName, string(content))
	}

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