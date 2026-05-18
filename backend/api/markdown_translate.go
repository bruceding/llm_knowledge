package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sashabaranov/go-openai"
	"gorm.io/gorm"
)

const markdownTranslatePrompt = `请将以下 Markdown 内容翻译为%s，输出双语对照版本。

要求：
1. 保持原文的 Markdown 格式（标题、链接、引用、代码块等）
2. 每个段落先保留原文，紧接着输出翻译段落，格式如下：
   原文段落内容

   翻译段落内容

3. 代码块内的代码和 URL 不需要翻译，但代码块外的说明文字需要翻译
4. 保持学术性和专业性
5. 翻译段落不要添加任何前缀（如"翻译："），直接输出译文
6. 标题、链接等元素也需要双语对照

原文内容：
%s`

type MarkdownTranslateHandler struct {
	DataDir string
}

type MarkdownTranslateRequest struct {
	DocID      uint   `json:"docId"`
	TargetLang string `json:"targetLang"` // zh, en
}

var validMarkdownTargetLangs = map[string]string{
	"zh": "中文",
	"en": "English",
}

// isMarkdownTranslatable reports whether the given document source type
// stores its raw content as Markdown that can be fed to the markdown
// translator. Web stores at <rawPath>/paper.md; rss/blog/newsletter store
// at <rawPath>.md. PDFs and other types use a different translation path.
func isMarkdownTranslatable(sourceType string) bool {
	switch sourceType {
	case "web", "rss", "blog", "newsletter":
		return true
	}
	return false
}

// CheckMarkdownTranslationStatus checks if translated Markdown file exists
// GET /api/documents/:id/markdown-translation-status
func (h *MarkdownTranslateHandler) CheckMarkdownTranslationStatus(c echo.Context) error {
	docID := c.Param("id")
	if docID == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document id required"})
	}
	userId := GetCurrentUserId(c)

	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", docID, userId).First(&doc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
		}
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get document"})
	}

	// Only for Web/RSS/Blog/Newsletter documents
	if !isMarkdownTranslatable(doc.SourceType) {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only web/rss/blog/newsletter documents supported"})
	}

	// Determine target language based on source
	targetLang := "zh"
	if doc.Language == "zh" {
		targetLang = "en"
	}

	// Determine translation file path
	rawRelPath := StripUserPrefix(doc.RawPath)
	translatedPath := h.getTranslatedPath(rawRelPath, targetLang)
	userDir := GetUserDir(c)
	fullPath := filepath.Join(userDir, translatedPath)

	exists := false
	if _, err := os.Stat(fullPath); err == nil {
		exists = true
	}

	return c.JSON(http.StatusOK, echo.Map{
		"exists":     exists,
		"path":       filepath.Join("/data", translatedPath),
		"targetLang": targetLang,
	})
}

// getTranslatedPath returns the path for translated file
func (h *MarkdownTranslateHandler) getTranslatedPath(rawPath string, targetLang string) string {
	if strings.HasSuffix(rawPath, ".md") {
		// RSS: raw/rss/feed/title.md -> raw/rss/feed/title_zh.md
		return strings.TrimSuffix(rawPath, ".md") + "_" + targetLang + ".md"
	}
	// Web: raw/web/title -> raw/web/title/paper_zh.md
	return rawPath + "/paper_" + targetLang + ".md"
}

// getSourcePath returns the path for source Markdown file
func (h *MarkdownTranslateHandler) getSourcePath(rawPath string) string {
	if strings.HasSuffix(rawPath, ".md") {
		// RSS: direct .md file
		return rawPath
	}
	// Web: directory with paper.md
	return rawPath + "/paper.md"
}

// TranslateMarkdown translates Markdown document using OpenAI-compatible API
// POST /api/markdown-translate (SSE streaming)
func (h *MarkdownTranslateHandler) TranslateMarkdown(c echo.Context) error {
	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "streaming not supported"})
	}

	var req MarkdownTranslateRequest
	if err := c.Bind(&req); err != nil {
		sendMarkdownSSEError(c, flusher, "invalid request body")
		return nil
	}

	if req.DocID == 0 {
		sendMarkdownSSEError(c, flusher, "docId is required")
		return nil
	}

	// Validate target language
	targetLangName, valid := validMarkdownTargetLangs[req.TargetLang]
	if !valid {
		sendMarkdownSSEError(c, flusher, "invalid targetLang, must be 'zh' or 'en'")
		return nil
	}

	// Get document
	userId := GetCurrentUserId(c)
	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", req.DocID, userId).First(&doc).Error; err != nil {
		sendMarkdownSSEError(c, flusher, "document not found")
		return nil
	}

	// Validate source type
	if !isMarkdownTranslatable(doc.SourceType) {
		sendMarkdownSSEError(c, flusher, "only web/rss/blog/newsletter documents supported")
		return nil
	}

	if doc.RawPath == "" {
		sendMarkdownSSEError(c, flusher, "document has no raw content")
		return nil
	}

	// Get global settings
	var settings db.GlobalSettings
	if err := db.DB.First(&settings).Error; err != nil {
		sendMarkdownSSEError(c, flusher, "failed to get global settings")
		return nil
	}

	if !settings.TranslationEnabled {
		sendMarkdownSSEError(c, flusher, "translation not enabled")
		return nil
	}

	if settings.TranslationApiKey == "" {
		sendMarkdownSSEError(c, flusher, "API key not configured")
		return nil
	}

	// Read source content
	rawRelPath := StripUserPrefix(doc.RawPath)
	sourcePath := h.getSourcePath(rawRelPath)
	userDir := GetUserDir(c)
	fullSourcePath := filepath.Join(userDir, sourcePath)
	content, err := os.ReadFile(fullSourcePath)
	if err != nil {
		sendMarkdownSSEError(c, flusher, "source file not found")
		return nil
	}

	// Send progress event
	sendMarkdownSSEEvent(c, flusher, "progress", echo.Map{"message": "正在翻译..."})

	// Create OpenAI client
	config := openai.DefaultConfig(settings.TranslationApiKey)
	config.BaseURL = settings.TranslationApiBase
	client := openai.NewClientWithConfig(config)

	// Build prompt
	prompt := fmt.Sprintf(markdownTranslatePrompt, targetLangName, string(content))

	// Create chat completion request with streaming
	ctx := c.Request().Context()
	stream, err := client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: settings.TranslationModel,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Stream: true,
	})
	if err != nil {
		sendMarkdownSSEError(c, flusher, "API call failed: "+err.Error())
		return nil
	}
	defer stream.Close()

	// Stream response
	var translatedContent strings.Builder
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			sendMarkdownSSEError(c, flusher, "stream error: "+err.Error())
			return nil
		}

		if len(response.Choices) > 0 {
			chunk := response.Choices[0].Delta.Content
			if chunk != "" {
				translatedContent.WriteString(chunk)
				// Send chunk to client
				if err := sendMarkdownSSEEvent(c, flusher, "chunk", echo.Map{"content": chunk}); err != nil {
					return nil
				}
			}
		}
	}

	// Save translated content
	translatedPath := h.getTranslatedPath(rawRelPath, req.TargetLang)
	fullTranslatedPath := filepath.Join(userDir, translatedPath)

	// Ensure directory exists for Web documents
	if !strings.HasSuffix(doc.RawPath, ".md") {
		dir := filepath.Dir(fullTranslatedPath)
		os.MkdirAll(dir, 0755)
	}

	if err := os.WriteFile(fullTranslatedPath, []byte(translatedContent.String()), 0644); err != nil {
		sendMarkdownSSEError(c, flusher, "failed to save translation: "+err.Error())
		return nil
	}

	// Send completion event
	sendMarkdownSSEEvent(c, flusher, "complete", echo.Map{
		"path":       filepath.Join("/data", translatedPath),
		"targetLang": req.TargetLang,
	})

	return nil
}

func sendMarkdownSSEEvent(c echo.Context, flusher http.Flusher, eventType string, data echo.Map) error {
	data["type"] = eventType
	jsonData, _ := json.Marshal(data)
	if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func sendMarkdownSSEError(c echo.Context, flusher http.Flusher, errorMsg string) error {
	return sendMarkdownSSEEvent(c, flusher, "error", echo.Map{"error": errorMsg})
}