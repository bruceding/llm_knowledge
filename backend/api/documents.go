package api

import (
	"context"
	"fmt"
	"llm-knowledge/agent"
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
	DataDir string
}

// ListInbox returns all documents with status "inbox"
func (h *DocHandler) ListInbox(c echo.Context) error {
	userId := GetCurrentUserId(c)
	var docs []db.Document
	result := db.DB.Preload("Tags").Where("status = ? AND user_id = ?", "inbox", userId).Order("created_at desc").Find(&docs)
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": result.Error.Error()})
	}
	return c.JSON(http.StatusOK, docs)
}

// GetDoc returns a single document by ID with its tags
func (h *DocHandler) GetDoc(c echo.Context) error {
	id := c.Param("id")
	userId := GetCurrentUserId(c)
	var doc db.Document
	result := db.DB.Preload("Tags").Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}
	return c.JSON(http.StatusOK, doc)
}

// UpdateDocRequest represents the request body for updating a document
type UpdateDocRequest struct {
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	TagNames []string `json:"tagNames"`
}

// UpdateDoc updates a document's title, status, and tags
func (h *DocHandler) UpdateDoc(c echo.Context) error {
	id := c.Param("id")
	userId := GetCurrentUserId(c)

	// Check if document exists and belongs to user
	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	// Parse request body
	var req UpdateDocRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}

	// Validate status if provided
	if req.Status != "" {
		validStatuses := map[string]bool{"inbox": true, "later": true, "published": true, "archived": true}
		if !validStatuses[req.Status] {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid status, must be one of: inbox, later, published, archived"})
		}
		doc.Status = req.Status
	}

	// Update title if provided
	if req.Title != "" {
		doc.Title = req.Title
		if doc.Slug == "" {
			doc.Slug = req.Title // fallback for legacy records
		}
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
				// Find or create tag scoped to this user
				var tag db.Tag
				result := tx.Where("name = ? AND user_id = ?", tagName, userId).First(&tag)
				if result.Error != nil {
					// Create new tag
					tag = db.Tag{
						Name:   tagName,
						Color:  "#808080", // Default color
						UserID: userId,
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
	userId := GetCurrentUserId(c)

	// Check if document exists and belongs to user
	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	// Update status to published
	doc.Status = "published"
	doc.UpdatedAt = time.Now()
	if err := db.DB.Save(&doc).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to publish document"})
	}

	// Trigger wiki ingest if raw content exists.
	// 这里原先还有 `&& h.ClaudeBin != ""`,但 config.go:46-48 把 ClaudeBin 兜底成
	// "claude"、永不为空,所以那个条件恒真。字段随后端开关一起删掉,条件随之消失。
	// 「LLM 到底可不可用」现在由 resolver 在实际调用时判定并返回错误,比在入口处
	// 靠一个字符串是否为空来猜更准确。
	if doc.RawPath != "" {
		userDir := GetUserDir(c)
		userIdStr := GetUserIdStr(c)

		// Extract relative path from doc.RawPath (which is "users/{userId}/raw/...")
		// Claude CLI runs with cmd.Dir = userDir, so paths should be relative to userDir
		rawRelPath := StripUserPrefix(doc.RawPath)

		// Build absolute path for existence check
		var mdPath string
		if strings.HasSuffix(rawRelPath, ".md") {
			mdPath = filepath.Join(userDir, rawRelPath)
		} else {
			mdPath = filepath.Join(userDir, rawRelPath, "paper.md")
		}

		// Build relative path for Claude (relative to userDir)
		var claudeRelPath string
		if strings.HasSuffix(rawRelPath, ".md") {
			claudeRelPath = rawRelPath
		} else {
			claudeRelPath = rawRelPath + "/paper.md"
		}

		if _, err := os.Stat(mdPath); err == nil {
			// Run wiki ingest asynchronously to avoid blocking the HTTP response
			docID := doc.ID
			docSlug := doc.Slug
			if docSlug == "" {
				docSlug = doc.Title // fallback for legacy records
			}
			go func() {
				p := ingest.NewPipeline(userDir)
				ctx := context.Background()
				if err := p.Ingest(ctx, claudeRelPath, docSlug, docID); err != nil {
					log.Printf("[api] wiki ingest failed for %d: %v", docID, err)
				} else {
					wikiRelPath := filepath.Join("users", userIdStr, "wiki", "sources", docSlug+".md")
					db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("wiki_path", wikiRelPath)
					log.Printf("[api] wiki ingest completed for %d: %s", docID, wikiRelPath)
				}
			}()
		}
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":      doc.ID,
		"status":  "published",
		"message": "Publishing, wiki import started in background",
	})
}

// DeleteDoc deletes a document and its associated files
func (h *DocHandler) DeleteDoc(c echo.Context) error {
	id := c.Param("id")
	idUint, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid document id"})
	}
	userId := GetCurrentUserId(c)

	// Check if document exists and belongs to user
	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	log.Printf("[delete] Deleting document id=%d title=%q sourceType=%s rawPath=%q wikiPath=%q userId=%d remoteAddr=%s",
		doc.ID, doc.Title, doc.SourceType, doc.RawPath, doc.WikiPath, userId, c.RealIP())

	userDir := GetUserDir(c)

	// Delete associated raw files if RawPath is set
	if doc.RawPath != "" {
		rawRelPath := StripUserPrefix(doc.RawPath)
		rawPath := filepath.Join(userDir, rawRelPath)
		if _, err := os.Stat(rawPath); err == nil {
			// RSS articles are single .md files in a shared feed directory
			if strings.HasPrefix(rawRelPath, "raw/rss/") {
				os.Remove(rawPath)
			} else {
				// For papers/web clips, RawPath is the document's own directory
				// (e.g. raw/web/{title}/ or raw/papers/{name}/).
				// Check if other documents reference the same directory before removing.
				var refCount int64
				db.DB.Model(&db.Document{}).Where("raw_path = ? AND id != ? AND user_id = ?", doc.RawPath, doc.ID, userId).Count(&refCount)
				if refCount == 0 {
					os.RemoveAll(rawPath)
				} else {
					log.Printf("[delete] Skipping raw dir deletion: %d other document(s) reference %q", refCount, doc.RawPath)
				}
			}
		}
	}

	// Delete associated wiki files if WikiPath is set
	if doc.WikiPath != "" {
		wikiRelPath := StripUserPrefix(doc.WikiPath)
		wikiPath := filepath.Join(userDir, wikiRelPath)
		if _, err := os.Stat(wikiPath); err == nil {
			os.Remove(wikiPath)
		}
	}

	// Clean wiki content (entities, topics, index files) related to this document
	wikiDir := filepath.Join(userDir, "wiki")
	docSlug := doc.Slug
	if docSlug == "" {
		docSlug = doc.Title // fallback for legacy records
	}
	if err := ingest.CleanWikiForDocument(wikiDir, docSlug); err != nil {
		log.Printf("[api] wiki cleanup error for %s: %v", docSlug, err)
		// Continue anyway - document deletion is primary operation
	}

	// Delete document-tag associations
	db.DB.Where("document_id = ?", idUint).Delete(&db.DocumentTag{})

	// Hard-delete any soft-deleted records that share the same raw_path
	// This prevents "ghost" records from lingering after a delete + re-import cycle
	if doc.RawPath != "" {
		var ghostDocs []db.Document
		if err := db.DB.Unscoped().Where("raw_path = ? AND id != ? AND user_id = ? AND deleted_at IS NOT NULL", doc.RawPath, doc.ID, userId).Find(&ghostDocs).Error; err == nil {
			for _, ghost := range ghostDocs {
				db.DB.Unscoped().Delete(&ghost)
				log.Printf("[delete] Hard-deleted ghost record id=%d with same raw_path=%q", ghost.ID, doc.RawPath)
			}
		}
	}

	// Delete the document record from database (soft delete)
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
	userId := GetCurrentUserId(c)

	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be re-extracted"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	pdfPath := filepath.Join(userDir, rawRelPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	extracted, err := ingest.ExtractPDFText(pdfPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to extract text: " + err.Error()})
	}

	mdPath := filepath.Join(userDir, rawRelPath, "paper.md")
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
	userId := GetCurrentUserId(c)

	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be LLM-extracted"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	pdfPath := filepath.Join(userDir, rawRelPath, "paper.pdf")
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

	// Create user-scoped temp directory for images (avoid cross-user race)
	tempDir := filepath.Join("/tmp", fmt.Sprintf("pdf_pages_%d_%d", userId, doc.ID))
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	// Convert PDF to images
	pdftoppmCmd := exec.Command("pdftoppm", "-png", "-r", "150", "-f", startPage, "-l", endPage, pdfPath, filepath.Join(tempDir, "page"))
	if err := pdftoppmCmd.Run(); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to convert PDF to images"})
	}

	// Process each page with Claude CLI
	outputDir := filepath.Join(userDir, rawRelPath)
	os.MkdirAll(outputDir, 0755)
	assetsDir := filepath.Join(outputDir, "assets")
	os.MkdirAll(assetsDir, 0755)

	mdPath := filepath.Join(outputDir, "paper.md")
	mdFile, err := os.Create(mdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create markdown file"})
	}
	defer mdFile.Close()

	// Hoisted out of the per-page loop — these don't vary by page and the
	// symlink resolution inside Env shouldn't run N times. If OnceArgs ever
	// fails it fails identically for every page, so it's a request-level error
	// not a per-page one.
	//
	// Task 8:后端改由 resolver 决定。原先这里是 exec.Command("claude", ...) +
	// claude.BuildSecureArgs/BuildSecureEnv,是 Plan 1 遗留接缝里最严重的一处 ——
	// 它绕过 Protocol 直接 spawn,于是 Settings 切到 pi 之后 PDF 转换仍会 spawn
	// claude,而且**不报错**(只有装了 claude 才恰好能用,没装则静默失败成
	// "[Error processing page N]")。agent/invariant_test.go 现在守这条。
	proto, err := agent.Current()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to resolve LLM backend: " + err.Error()})
	}
	// D3:model 交给 OnceArgs,不再手工拼在 args 前面。claude 侧会加 --model sonnet
	// (与原行为一致),pi 侧忽略它 —— pi 的模型由 resolver 决定,不接受每请求覆盖。
	cmdArgs, err := proto.OnceArgs("", []string{"Read"}, false, "sonnet")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to build secure args: " + err.Error()})
	}
	secureEnv := proto.Env(tempDir)

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

		// Call the LLM CLI. Uses the hoisted args/env so every page goes through
		// --disallowedTools + the path-validator hook with ALLOWED_DIR scoped
		// narrowly to the per-call temp dir.
		prompt := "读取图片 " + pageImg + "，将其转换为 Markdown 格式。保留标题层级、段落结构、表格。如果有图片，用 ![描述](assets/img_" + strconv.Itoa(i) + ".png) 标记。如果有公式，用 LaTeX 格式。直接输出内容，不要解释。"
		claudeCmd := exec.Command(proto.Bin(), cmdArgs...)
		claudeCmd.Env = secureEnv
		// D2:prompt 从 argv 改走 stdin。原先是 `-p <prompt>`:对 ps 可见,且受
		// ARG_MAX 限制;pi 侧更不能放 argv —— 它的 `-p` 是「读管道 stdin 并合并进
		// 初始 prompt」,两边都给会被拼接成一段。
		claudeCmd.Stdin = strings.NewReader(prompt)
		output, err := claudeCmd.Output()
		if err != nil {
			mdFile.WriteString("[Error processing page " + strconv.Itoa(i) + ": " + err.Error() + "]\n")
			continue
		}

		mdFile.WriteString(string(output))
		mdFile.WriteString("\n")
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":          doc.ID,
		"total_pages": totalPages,
		"pages":       end - start + 1,
		"message":     "PDF extracted with LLM successfully",
		"output_path": mdPath,
	})
}

// HTMLExtract converts PDF to HTML using pdftohtml, preserving original layout
func (h *DocHandler) HTMLExtract(c echo.Context) error {
	id := c.Param("id")
	userId := GetCurrentUserId(c)

	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents can be converted to HTML"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	pdfPath := filepath.Join(userDir, rawRelPath, "paper.pdf")
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "PDF file not found"})
	}

	// Create HTML output directory
	htmlDir := filepath.Join(userDir, rawRelPath, "html")
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
		"id":         doc.ID,
		"html_dir":   htmlDir,
		"html_pages": htmlFiles,
		"first_page": filepath.Join("/data", rawRelPath, "html/page-1.html"),
		"message":    "PDF converted to HTML successfully",
	})
}

// ListAll returns all documents (optionally filtered by status)
func (h *DocHandler) ListAll(c echo.Context) error {
	userId := GetCurrentUserId(c)
	status := c.QueryParam("status")
	var docs []db.Document

	query := db.DB.Preload("Tags").Where("user_id = ?", userId)
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
	userId := GetCurrentUserId(c)

	// Check if document exists and belongs to user
	var doc db.Document
	result := db.DB.Where("id = ? AND user_id = ?", idUint, userId).First(&doc)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "文档不存在或无权访问"})
	}

	if doc.RawPath == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document has no raw content"})
	}

	// Get user directory for summary generation
	userDir := GetUserDir(c)

	// Extract relative path from doc.RawPath (e.g., "users/1/raw/papers/foo" -> "raw/papers/foo")
	rawRelPath := StripUserPrefix(doc.RawPath)

	// Generate summary
	summary, err := ingest.GenerateSummary(userDir, rawRelPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to generate summary: " + err.Error()})
	}

	// Update document
	if err := db.DB.Model(&doc).Update("summary", summary).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to update summary"})
	}

	return c.JSON(http.StatusOK, echo.Map{"summary": summary})
}
