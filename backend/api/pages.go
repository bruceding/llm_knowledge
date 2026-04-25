package api

import (
	"fmt"
	"llm-knowledge/db"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// PagesHandler handles PDF page image generation
type PagesHandler struct {
	DataDir string
}

// GeneratePages converts PDF pages to images and stores them in pages/ directory
// POST /api/documents/:id/generate-pages
func (h *PagesHandler) GeneratePages(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can generate page images"})
	}

	pdfPath := filepath.Join(h.DataDir, doc.RawPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	// Get PDF info to determine total pages
	pdfInfoCmd := exec.Command("pdfinfo", pdfPath)
	pdfInfoOutput, err := pdfInfoCmd.Output()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get PDF info"})
	}

	// Parse total pages
	lines := strings.Split(string(pdfInfoOutput), "\n")
	var totalPages int
	for _, line := range lines {
		if strings.HasPrefix(line, "Pages:") {
			totalPages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			break
		}
	}

	// Create pages directory
	pagesDir := filepath.Join(h.DataDir, doc.RawPath, "pages")
	os.MkdirAll(pagesDir, 0755)

	// Generate page images using pdftoppm
	// -png: output PNG format
	// -r 100: 100 DPI for reasonable quality and size
	pdftoppmCmd := exec.Command("pdftoppm", "-png", "-r", "100", pdfPath, filepath.Join(pagesDir, "page"))
	if err := pdftoppmCmd.Run(); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to generate page images"})
	}

	// Rename files from page-1.png to page_1.png for consistent naming
	files, _ := os.ReadDir(pagesDir)
	for _, f := range files {
		oldName := f.Name()
		// pdftoppm generates page-1.png, page-2.png, etc.
		if strings.HasPrefix(oldName, "page-") && strings.HasSuffix(oldName, ".png") {
			// Convert to page_1.png format
			pageNum := strings.TrimPrefix(oldName, "page-")
			pageNum = strings.TrimSuffix(pageNum, ".png")
			newName := fmt.Sprintf("page_%s.png", pageNum)
			os.Rename(filepath.Join(pagesDir, oldName), filepath.Join(pagesDir, newName))
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":          doc.ID,
		"total_pages": totalPages,
		"pages_dir":   pagesDir,
		"message":     "Page images generated successfully",
	})
}

// CheckPages checks if page images already exist
// GET /api/documents/:id/pages-status
func (h *PagesHandler) CheckPages(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	pagesDir := filepath.Join(h.DataDir, doc.RawPath, "pages")
	if _, err := os.Stat(pagesDir); os.IsNotExist(err) {
		return c.JSON(http.StatusOK, echo.Map{
			"exists":    false,
			"page_count": 0,
		})
	}

	// Count page images
	files, _ := os.ReadDir(pagesDir)
	pageCount := 0
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "page_") && strings.HasSuffix(f.Name(), ".png") {
			pageCount++
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"exists":     true,
		"page_count": pageCount,
	})
}