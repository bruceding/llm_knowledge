package api

import (
	"bufio"
	"errors"
	"fmt"
	"llm-knowledge/db"
	"llm-knowledge/pdf2zh"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type PDFTranslateHandler struct {
	DataDir       string
	PDF2ZhVenvDir string // pdf2zh venv directory (~/.llm-knowledge/.venv)
}

// CheckTranslationStatus checks if translated PDF exists
// GET /api/documents/:id/translation-status
func (h *PDFTranslateHandler) CheckTranslationStatus(c echo.Context) error {
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

	// Determine target language based on source
	targetLang := "zh"
	if doc.Language == "zh" {
		targetLang = "en"
	}

	// Check if translated PDF exists
	pdfDir := filepath.Join(h.DataDir, doc.RawPath)
	translatedPdfName := fmt.Sprintf("paper_%s.pdf", targetLang)
	translatedPdfPath := filepath.Join(pdfDir, translatedPdfName)

	exists := false
	if _, err := os.Stat(translatedPdfPath); err == nil {
		exists = true
	}

	return c.JSON(http.StatusOK, echo.Map{
		"exists":    exists,
		"path":      filepath.Join("/data", doc.RawPath, translatedPdfName),
		"targetLang": targetLang,
	})
}

// TranslatePDF translates PDF using pdf2zh CLI
// POST /api/pdf-translate (SSE streaming)
func (h *PDFTranslateHandler) TranslatePDF(c echo.Context) error {
	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	var input struct {
		DocID      uint   `json:"docId"`
		TargetLang string `json:"targetLang"` // optional, auto-detect if empty
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	// Get document
	var doc db.Document
	if err := db.DB.First(&doc, input.DocID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendSSEError(c, "document not found")
			return nil
		}
		sendSSEError(c, "failed to get document")
		return nil
	}

	// Get settings
	var settings db.UserSettings
	if err := db.DB.First(&settings).Error; err != nil {
		sendSSEError(c, "failed to get settings")
		return nil
	}

	if !settings.TranslationEnabled {
		sendSSEError(c, "translation not enabled in settings")
		return nil
	}

	if settings.TranslationApiKey == "" {
		sendSSEError(c, "API key not configured")
		return nil
	}

	// Check if pdf2zh is ready
	if !pdf2zh.IsReady(h.PDF2ZhVenvDir) {
		status := pdf2zh.GetInstallStatus()
		if status == "installing" {
			sendSSEError(c, "pdf2zh is still installing, please wait and try again later")
		} else if status == "failed" {
			sendSSEError(c, "pdf2zh installation failed, check logs for details")
		} else {
			sendSSEError(c, "pdf2zh is not installed")
		}
		return nil
	}

	// Determine target language
	targetLang := input.TargetLang
	if targetLang == "" {
		if doc.Language == "zh" {
			targetLang = "en"
		} else {
			targetLang = "zh"
		}
	}

	// PDF paths
	pdfDir := filepath.Join(h.DataDir, doc.RawPath)
	sourcePdf := filepath.Join(pdfDir, "paper.pdf")
	translatedPdf := filepath.Join(pdfDir, fmt.Sprintf("paper_%s.pdf", targetLang))

	// Check if source PDF exists
	if _, err := os.Stat(sourcePdf); err != nil {
		sendSSEError(c, "source PDF not found")
		return nil
	}

	// Check if translated PDF already exists
	if _, err := os.Stat(translatedPdf); err == nil {
		sendSSEEvent(c, "complete", echo.Map{
			"translatedPdf": filepath.Join("/data", doc.RawPath, fmt.Sprintf("paper_%s.pdf", targetLang)),
			"targetLang":    targetLang,
		})
		return nil
	}

	// Build pdf2zh command
	// Use the venv installed at ~/.llm-knowledge/.venv
	pdf2zhVenv := h.PDF2ZhVenvDir

	// Use shell to activate venv and run pdf2zh
	cmdStr := fmt.Sprintf(
		"source '%s/bin/activate' && pdf2zh --service openai '%s'",
		pdf2zhVenv, sourcePdf,
	)

	cmd := exec.Command("bash", "-c", cmdStr)
	cmd.Dir = pdfDir

	// Set environment variables
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("OPENAI_BASE_URL=%s", settings.TranslationApiBase),
		fmt.Sprintf("OPENAI_API_KEY=%s", settings.TranslationApiKey),
		fmt.Sprintf("OPENAI_MODEL=%s", settings.TranslationModel),
	)

	// Get stdout and stderr pipes
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendSSEError(c, "failed to create stdout pipe")
		return nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		sendSSEError(c, "failed to create stderr pipe")
		return nil
	}

	// Start command
	if err := cmd.Start(); err != nil {
		sendSSEError(c, fmt.Sprintf("failed to start pdf2zh: %v", err))
		return nil
	}

	sendSSEEvent(c, "progress", echo.Map{"message": "Translation started..."})

	// Read stdout for progress
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[pdf2zh stdout] %s", line)
			// Parse progress from output
			sendSSEEvent(c, "progress", echo.Map{"message": line})
		}
	}()

	// Read stderr for errors
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[pdf2zh stderr] %s", line)
		}
	}()

	// Wait for completion
	if err := cmd.Wait(); err != nil {
		sendSSEError(c, fmt.Sprintf("pdf2zh failed: %v", err))
		return nil
	}

	// Verify output exists
	// pdf2zh generates files with -mono and -dual suffix
	// We need to find the generated file and rename it
	files, err := os.ReadDir(pdfDir)
	if err != nil {
		sendSSEError(c, "failed to read output directory")
		return nil
	}

	// Find generated translated PDF (pdf2zh creates paper-mono.pdf and paper-dual.pdf)
	// We'll use the mono version as the main translated file
	generatedMono := filepath.Join(pdfDir, "paper-mono.pdf")
	generatedDual := filepath.Join(pdfDir, "paper-dual.pdf")

	// Check if files were generated
	monoExists := false
	dualExists := false
	for _, f := range files {
		if f.Name() == "paper-mono.pdf" {
			monoExists = true
		}
		if f.Name() == "paper-dual.pdf" {
			dualExists = true
		}
	}

	// Rename generated files to target language suffix
	if monoExists {
		newMono := filepath.Join(pdfDir, fmt.Sprintf("paper_%s.pdf", targetLang))
		if err := os.Rename(generatedMono, newMono); err != nil {
			log.Printf("Failed to rename mono PDF: %v", err)
		}
	}

	if dualExists {
		// Delete dual PDF, we don't need it
		if err := os.Remove(generatedDual); err != nil {
			log.Printf("Failed to delete dual PDF: %v", err)
		}
	}

	// Send completion event
	sendSSEEvent(c, "complete", echo.Map{
		"translatedPdf": filepath.Join("/data", doc.RawPath, fmt.Sprintf("paper_%s.pdf", targetLang)),
		"targetLang":    targetLang,
	})

	return nil
}

func sendSSEEvent(c echo.Context, eventType string, data echo.Map) {
	data["type"] = eventType
	event := fmt.Sprintf("data: %s\n\n", mustMarshal(data))
	c.Response().Write([]byte(event))
	c.Response().Flush()
}

func sendSSEError(c echo.Context, errMsg string) {
	sendSSEEvent(c, "error", echo.Map{"error": errMsg})
}

func mustMarshal(data echo.Map) string {
	// Simple JSON marshaling for echo.Map
	result := "{"
	i := 0
	for k, v := range data {
		if i > 0 {
			result += ","
		}
		switch val := v.(type) {
		case string:
			result += fmt.Sprintf("\"%s\":\"%s\"", k, strings.ReplaceAll(val, "\"", "\\\""))
		case bool:
			result += fmt.Sprintf("\"%s\":%v", k, val)
		case int:
			result += fmt.Sprintf("\"%s\":%d", k, val)
		case float64:
			result += fmt.Sprintf("\"%s\":%f", k, val)
		default:
			result += fmt.Sprintf("\"%s\":\"%v\"", k, val)
		}
		i++
	}
	result += "}"
	return result
}