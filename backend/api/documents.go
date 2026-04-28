package api

import (
	"context"
	"fmt"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// DocHandler handles document CRUD operations
type DocHandler struct {
	DataDir   string
	ClaudeBin string
}

// ListInbox returns all documents with status "inbox"
func (h *DocHandler) ListInbox(c echo.Context) error {
	var docs []db.Document
	result := db.DB.Where("status = ?", "inbox").Order("created_at desc").Find(&docs)
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": result.Error.Error()})
	}
	return c.JSON(http.StatusOK, docs)
}

// GetDoc returns a single document by ID with its tags
func (h *DocHandler) GetDoc(c echo.Context) error {
	id := c.Param("id")
	var doc db.Document
	result := db.DB.Preload("Tags").First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}
	return c.JSON(http.StatusOK, doc)
}

// UpdateDocRequest represents the request body for updating a document
type UpdateDocRequest struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	TagNames []string `json:"tagNames"`
}

// UpdateDoc updates a document's title, status, and tags
func (h *DocHandler) UpdateDoc(c echo.Context) error {
	id := c.Param("id")

	// Check if document exists
	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Parse request body
	var req UpdateDocRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	// Validate status if provided
	if req.Status != "" {
		validStatuses := map[string]bool{"inbox": true, "published": true, "archived": true}
		if !validStatuses[req.Status] {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid status, must be one of: inbox, published, archived"})
		}
		doc.Status = req.Status
	}

	// Update title if provided
	if req.Title != "" {
		doc.Title = req.Title
	}

	// Wrap all database operations in a transaction
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		// Save document changes
		if err := tx.Save(&doc).Error; err != nil {
			return err
		}

		// Update tags if provided
		if req.TagNames != nil {
			// Remove existing tag associations
			if err := tx.Where("document_id = ?", doc.ID).Delete(&db.DocumentTag{}).Error; err != nil {
				return err
			}

			// Add new tags
			for _, tagName := range req.TagNames {
				if tagName == "" {
					continue
				}
				// Find or create tag
				var tag db.Tag
				result := tx.Where("name = ?", tagName).First(&tag)
				if result.Error != nil {
					// Create new tag
					tag = db.Tag{
						Name:  tagName,
						Color: "#808080", // Default color
					}
					if err := tx.Create(&tag).Error; err != nil {
						continue // Skip if tag creation fails
					}
				}
				// Create document-tag association
				docTag := db.DocumentTag{
					DocumentID: doc.ID,
					TagID:      tag.ID,
				}
				if err := tx.Create(&docTag).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to update document"})
	}

	// Reload document with tags
	db.DB.Preload("Tags").First(&doc, doc.ID)
	return c.JSON(http.StatusOK, doc)
}

// Publish sets a document's status to "published" and triggers wiki ingest
func (h *DocHandler) Publish(c echo.Context) error {
	id := c.Param("id")

	// Check if document exists
	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Update status to published
	doc.Status = "published"
	doc.UpdatedAt = time.Now()
	if err := db.DB.Save(&doc).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to publish document"})
	}

	// Trigger wiki ingest if raw content exists and ClaudeBin is configured
	if doc.RawPath != "" && h.ClaudeBin != "" {
		wikiDir := filepath.Join(h.DataDir, "wiki")
		mdPath := filepath.Join(h.DataDir, doc.RawPath, "paper.md")

		// Find markdown file
		if _, err := os.Stat(mdPath); err == nil {
			p := ingest.NewPipeline(wikiDir, h.ClaudeBin)
			ctx := context.Background()
			name := doc.Title
			if err := p.Ingest(ctx, mdPath, name, doc.ID); err != nil {
				log.Printf("[api] wiki ingest failed for %d: %v", doc.ID, err)
				// Still return success - status was updated
			} else {
				wikiRelPath := filepath.Join("wiki", name+".md")
				db.DB.Model(&doc).Update("wiki_path", wikiRelPath)
				doc.WikiPath = wikiRelPath
			}
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":       doc.ID,
		"status":   "published",
		"wikiPath": doc.WikiPath,
		"message":  "Published and imported to wiki",
	})
}

// DeleteDoc deletes a document and its associated files
func (h *DocHandler) DeleteDoc(c echo.Context) error {
	id := c.Param("id")
	idUint, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid document id"})
	}

	// Check if document exists
	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Delete associated raw files if RawPath is set
	if doc.RawPath != "" {
		rawPath := filepath.Join(h.DataDir, doc.RawPath)
		if _, err := os.Stat(rawPath); err == nil {
			// RSS articles share a feed directory - only delete the single file
			if strings.HasPrefix(doc.RawPath, "raw/rss/") {
				os.Remove(rawPath)
			} else {
				// For papers/web clips, each document has its own directory
				parentDir := filepath.Dir(rawPath)
				if filepath.Base(parentDir) != "raw" {
					os.RemoveAll(parentDir)
				} else {
					os.Remove(rawPath)
				}
			}
		}
	}

	// Delete associated wiki files if WikiPath is set
	if doc.WikiPath != "" {
		wikiPath := filepath.Join(h.DataDir, doc.WikiPath)
		if _, err := os.Stat(wikiPath); err == nil {
			os.Remove(wikiPath)
		}
	}

	// Clean wiki content (entities, topics, index files) related to this document
	wikiDir := filepath.Join(h.DataDir, "wiki")
	docName := doc.Title
	if err := ingest.CleanWikiForDocument(wikiDir, docName); err != nil {
		log.Printf("[api] wiki cleanup error for %s: %v", docName, err)
		// Continue anyway - document deletion is primary operation
	}

	// Delete document-tag associations
	db.DB.Where("document_id = ?", idUint).Delete(&db.DocumentTag{})

	// Delete the document record from database
	if err := db.DB.Delete(&doc).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to delete document"})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":      doc.ID,
		"message": "document deleted",
	})
}

// ReExtract re-extracts text from a PDF document and overwrites the raw markdown file
func (h *DocHandler) ReExtract(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be re-extracted"})
	}

	pdfPath := filepath.Join(h.DataDir, doc.RawPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	extracted, err := ingest.ExtractPDFText(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to extract text: " + err.Error()})
	}

	mdPath := filepath.Join(h.DataDir, doc.RawPath, "paper.md")
	if err := os.WriteFile(mdPath, []byte(extracted.FullText), 0644); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to write markdown file"})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":      doc.ID,
		"pages":   len(extracted.Pages),
		"message": "PDF text re-extracted successfully",
	})
}

// LLMExtract extracts PDF to markdown using Claude CLI with vision capabilities
func (h *DocHandler) LLMExtract(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be LLM-extracted"})
	}

	pdfPath := filepath.Join(h.DataDir, doc.RawPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	// Get page range from query params
	startPage := c.QueryParam("start_page")
	endPage := c.QueryParam("end_page")
	if startPage == "" {
		startPage = "1"
	}
	if endPage == "" {
		endPage = "all"
	}

	// Get PDF info
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

	if endPage == "all" {
		endPage = strconv.Itoa(totalPages)
	}

	start, _ := strconv.Atoi(startPage)
	end, _ := strconv.Atoi(endPage)
	if end > totalPages {
		end = totalPages
	}

	// Create temp directory for images
	tempDir := filepath.Join("/tmp", "pdf_pages")
	os.MkdirAll(tempDir, 0755)

	// Convert PDF to images
	pdftoppmCmd := exec.Command("pdftoppm", "-png", "-r", "150", "-f", startPage, "-l", endPage, pdfPath, filepath.Join(tempDir, "page"))
	if err := pdftoppmCmd.Run(); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to convert PDF to images"})
	}

	// Process each page with Claude CLI
	outputDir := filepath.Join(h.DataDir, doc.RawPath)
	os.MkdirAll(outputDir, 0755)
	assetsDir := filepath.Join(outputDir, "assets")
	os.MkdirAll(assetsDir, 0755)

	mdPath := filepath.Join(outputDir, "paper.md")
	mdFile, err := os.Create(mdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create markdown file"})
	}
	defer mdFile.Close()

	for i := start; i <= end; i++ {
		// Image filename format: page-01.png, page-02.png
		pageImg := filepath.Join(tempDir, fmt.Sprintf("page-%02d.png", i))

		if _, err := os.Stat(pageImg); os.IsNotExist(err) {
			continue
		}

		// Copy page image to assets with proper naming
		assetImgPath := filepath.Join(assetsDir, fmt.Sprintf("img_%d.png", i))
		input, err := os.ReadFile(pageImg)
		if err != nil {
			continue
		}
		os.WriteFile(assetImgPath, input, 0644)

		// Write page header
		mdFile.WriteString("\n---\n\n## Page " + strconv.Itoa(i) + "\n\n")

		// Call Claude CLI
		claudeCmd := exec.Command("claude", "--model", "sonnet", "--allowed-tools", "Read", "-p",
			"读取图片 "+pageImg+"，将其转换为 Markdown 格式。保留标题层级、段落结构、表格。如果有图片，用 ![描述](assets/img_"+strconv.Itoa(i)+".png) 标记。如果有公式，用 LaTeX 格式。直接输出内容，不要解释。")

		claudeCmd.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
		output, err := claudeCmd.Output()
		if err != nil {
			mdFile.WriteString("[Error processing page " + strconv.Itoa(i) + ": " + err.Error() + "]\n")
			continue
		}

		mdFile.WriteString(string(output))
		mdFile.WriteString("\n")
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":           doc.ID,
		"total_pages":  totalPages,
		"pages":        end - start + 1,
		"message":      "PDF extracted with LLM successfully",
		"output_path":  mdPath,
	})
}

// HTMLExtract converts PDF to HTML using pdftohtml, preserving original layout
func (h *DocHandler) HTMLExtract(c echo.Context) error {
	id := c.Param("id")

	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be converted to HTML"})
	}

	pdfPath := filepath.Join(h.DataDir, doc.RawPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	// Create HTML output directory
	htmlDir := filepath.Join(h.DataDir, doc.RawPath, "html")
	os.RemoveAll(htmlDir)
	os.MkdirAll(htmlDir, 0755)

	// Execute pdftohtml
	cmd := exec.Command("pdftohtml", "-c", pdfPath, filepath.Join(htmlDir, "page"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error":  "failed to convert PDF to HTML",
			"output": string(output),
		})
	}

	// Count generated files
	files, _ := os.ReadDir(htmlDir)
	htmlFiles := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".html") {
			htmlFiles++
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":          doc.ID,
		"html_dir":    htmlDir,
		"html_pages":  htmlFiles,
		"first_page":  "/data/" + doc.RawPath + "/html/page-1.html",
		"message":     "PDF converted to HTML successfully",
	})
}

// ListAll returns all documents (optionally filtered by status)
func (h *DocHandler) ListAll(c echo.Context) error {
	status := c.QueryParam("status")
	var docs []db.Document

	query := db.DB.Preload("Tags")
	if status != "" {
		query = query.Where("status = ?", status)
	}
	query = query.Order("created_at desc")

	result := query.Find(&docs)
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": result.Error.Error()})
	}
	return c.JSON(http.StatusOK, docs)
}

// RegenerateSummary regenerates summary for an existing document
func (h *DocHandler) RegenerateSummary(c echo.Context) error {
	id := c.Param("id")
	idUint, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid document id"})
	}

	// Get Claude bin path from environment or default
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	// Check if document exists
	var doc db.Document
	result := db.DB.First(&doc, idUint)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.RawPath == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document has no raw content"})
	}

	// Generate summary
	summary, err := ingest.GenerateSummary(h.DataDir, doc.RawPath, claudeBin)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to generate summary: " + err.Error()})
	}

	// Update document
	if err := db.DB.Model(&doc).Update("summary", summary).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to update summary"})
	}

	return c.JSON(http.StatusOK, echo.Map{"summary": summary})
}
