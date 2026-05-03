package api

import (
	"io"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type RawHandler struct {
	DataDir  string
	ClaudeBin string // Path to Claude CLI binary
}

// UploadPDF handles PDF file upload, saves the original file,
// and extracts text content into a markdown file
func (h *RawHandler) UploadPDF(c echo.Context) error {
	// Get uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "no file provided"})
	}

	// Validate file extension
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "file must be a PDF"})
	}

	// Open the uploaded file
	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to open uploaded file"})
	}
	defer src.Close()

	// Create paper directory name from filename (without .pdf extension)
	// Use filepath.Base to prevent path traversal attacks
	name := strings.TrimSuffix(filepath.Base(file.Filename), ".pdf")
	dir := filepath.Join(h.DataDir, "raw", "papers", name)

	// Create directory structure: raw/papers/{name}/ and raw/papers/{name}/assets/
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create directory structure"})
	}

	// Save the original PDF file
	pdfPath := filepath.Join(dir, "paper.pdf")
	dst, err := os.Create(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create PDF file"})
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save PDF file"})
	}

	// Extract text from the PDF
	extracted, err := ingest.ExtractPDFText(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to extract text from PDF: " + err.Error()})
	}

	// Write extracted text to markdown file
	mdPath := filepath.Join(dir, "paper.md")
	if err := os.WriteFile(mdPath, []byte(extracted.FullText), 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to write markdown file"})
	}

	// Create Document record in database
	rawRelPath := filepath.Join("raw", "papers", name)
	mdRelPath := filepath.Join("raw", "papers", name, "paper.md")

	userId := GetCurrentUserId(c)

	doc := db.Document{
		UserID:     userId,
		Title:      name,
		SourceType: "pdf",
		RawPath:    rawRelPath,
		WikiPath:   "",
		Language:   detectLanguage(extracted.FullText),
		Status:     "inbox",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.DB.Create(&doc).Error; err != nil {
		log.Printf("[api] failed to create document record: %v", err)
		// Continue anyway - the file was saved successfully
	}

	// Capture docID before goroutine to avoid race condition
	docID := doc.ID

	// Trigger async ingest pipeline
	if h.ClaudeBin != "" {
		// Generate summary asynchronously
		go func() {
			summary, err := ingest.GenerateSummary(h.DataDir, rawRelPath, h.ClaudeBin)
			if err != nil {
				log.Printf("[api] summary generation failed for %s: %v", name, err)
			} else {
				db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
				log.Printf("[api] summary generated for %s", name)
			}
		}()

	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":       doc.ID,
		"path":     dir,
		"message":  "PDF uploaded and text extracted",
		"pages":    len(extracted.Pages),
		"rawPath":  mdRelPath,
	})
}

// UploadPDFFromURL handles PDF upload from HTTP URL
// downloads the PDF and processes it same as UploadPDF
func (h *RawHandler) UploadPDFFromURL(c echo.Context) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}
	if req.URL == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "url is required"})
	}

	// Parse URL to extract filename
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid URL"})
	}

	// Download PDF with timeout
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(req.URL)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to download PDF: " + err.Error()})
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "download failed with status: " + resp.Status})
	}

	// Validate PDF by Content-Type or URL extension
	contentType := resp.Header.Get("Content-Type")
	isPDF := strings.Contains(contentType, "application/pdf") ||
		strings.Contains(contentType, "application/x-pdf") ||
		strings.HasSuffix(strings.ToLower(parsedURL.Path), ".pdf")
	if !isPDF {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "URL does not appear to be a PDF"})
	}

	// Extract filename from URL path or Content-Disposition header
	name := extractPDFName(parsedURL, resp.Header)

	// Create directory structure
	dir := filepath.Join(h.DataDir, "raw", "papers", name)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create directory structure"})
	}

	// Save the downloaded PDF
	pdfPath := filepath.Join(dir, "paper.pdf")
	dst, err := os.Create(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create PDF file"})
	}
	defer dst.Close()

	if _, err := io.Copy(dst, resp.Body); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save PDF file"})
	}

	// Extract text from the PDF
	extracted, err := ingest.ExtractPDFText(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to extract text from PDF: " + err.Error()})
	}

	// Write extracted text to markdown file
	mdPath := filepath.Join(dir, "paper.md")
	if err := os.WriteFile(mdPath, []byte(extracted.FullText), 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to write markdown file"})
	}

	// Create Document record in database
	rawRelPath := filepath.Join("raw", "papers", name)
	mdRelPath := filepath.Join("raw", "papers", name, "paper.md")

	userId := GetCurrentUserId(c)

	doc := db.Document{
		UserID:     userId,
		Title:      name,
		SourceType: "pdf",
		RawPath:    rawRelPath,
		WikiPath:   "",
		Language:   detectLanguage(extracted.FullText),
		Status:     "inbox",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.DB.Create(&doc).Error; err != nil {
		log.Printf("[api] failed to create document record: %v", err)
	}

	docID := doc.ID

	// Trigger async summary generation
	if h.ClaudeBin != "" {
		go func() {
			summary, err := ingest.GenerateSummary(h.DataDir, rawRelPath, h.ClaudeBin)
			if err != nil {
				log.Printf("[api] summary generation failed for %s: %v", name, err)
			} else {
				db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
				log.Printf("[api] summary generated for %s", name)
			}
		}()
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":      doc.ID,
		"path":    dir,
		"message": "PDF downloaded and text extracted",
		"pages":   len(extracted.Pages),
		"rawPath": mdRelPath,
	})
}

// extractPDFName extracts PDF name from URL path or Content-Disposition header
func extractPDFName(parsedURL *url.URL, header http.Header) string {
	// Try Content-Disposition header first
	disposition := header.Get("Content-Disposition")
	if disposition != "" {
		// Parse filename from Content-Disposition: attachment; filename="xxx.pdf"
		if strings.Contains(disposition, "filename=") {
			parts := strings.Split(disposition, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], `" `)
				return strings.TrimSuffix(filepath.Base(filename), ".pdf")
			}
		}
	}

	// Fall back to URL path
	path := parsedURL.Path
	if path != "" {
		name := filepath.Base(path)
		return strings.TrimSuffix(name, ".pdf")
	}

	// Last resort: use hostname + timestamp
	return parsedURL.Hostname() + "-" + time.Now().Format("20060102-150405")
}

// detectLanguage performs simple language detection based on character frequency
func detectLanguage(text string) string {
	// Simple heuristic: count CJK characters
	cjkCount := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjkCount++
		}
	}
	if cjkCount > len(text)/10 {
		return "zh"
	}
	return "en"
}
