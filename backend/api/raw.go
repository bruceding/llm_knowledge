package api

import (
	"context"
	"io"
	"llm-knowledge/ingest"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	// Trigger async ingest pipeline
	if h.ClaudeBin != "" {
		go func() {
			wikiDir := filepath.Join(h.DataDir, "wiki")
			p := ingest.NewPipeline(wikiDir, h.ClaudeBin)
			if err := p.Ingest(context.Background(), mdPath, name); err != nil {
				log.Printf("[api] ingest failed for %s: %v", name, err)
			}
		}()
	}

	return c.JSON(http.StatusOK, echo.Map{
		"path":    dir,
		"message": "PDF uploaded and text extracted",
		"pages":   len(extracted.Pages),
	})
}
