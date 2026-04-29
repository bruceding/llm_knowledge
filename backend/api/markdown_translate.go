package api

import (
	"errors"
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

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

// CheckMarkdownTranslationStatus checks if translated Markdown file exists
// GET /api/documents/:id/markdown-translation-status
func (h *MarkdownTranslateHandler) CheckMarkdownTranslationStatus(c echo.Context) error {
	docID := c.Param("id")
	if docID == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document id required"})
	}

	var doc db.Document
	if err := db.DB.First(&doc, docID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
		}
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get document"})
	}

	// Only for Web/RSS documents
	if doc.SourceType != "web" && doc.SourceType != "rss" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only web/rss documents supported"})
	}

	// Determine target language based on source
	targetLang := "zh"
	if doc.Language == "zh" {
		targetLang = "en"
	}

	// Determine translation file path
	translatedPath := h.getTranslatedPath(doc.RawPath, targetLang)
	fullPath := filepath.Join(h.DataDir, translatedPath)

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