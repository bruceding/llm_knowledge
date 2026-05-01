package api

import (
	"fmt"
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

type DocNoteHandler struct {
	DataDir string
}

type CreateNoteRequest struct {
	Content     string `json:"content"`
	SourceMsgID string `json:"sourceMsgId"`
}

type UpdateNoteRequest struct {
	Content string `json:"content"`
}

func (h *DocNoteHandler) ListNotes(c echo.Context) error {
	userId := GetCurrentUserId(c)
	docID := c.Param("id")

	// Verify document ownership
	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", docID, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	var notes []db.DocNote
	db.DB.Where("document_id = ?", docID).Order("created_at desc").Find(&notes)
	return c.JSON(http.StatusOK, notes)
}

func (h *DocNoteHandler) CreateNote(c echo.Context) error {
	userId := GetCurrentUserId(c)
	docID := c.Param("id")
	docIDUint, err := strconv.ParseUint(docID, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid document id"})
	}

	// Verify document ownership
	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", docIDUint, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	var req CreateNoteRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.Content) == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "content cannot be empty"})
	}

	// Dedup: check if same sourceMsgID already saved
	if req.SourceMsgID != "" {
		var existing db.DocNote
		if err := db.DB.Where("document_id = ? AND source_msg_id = ?", docIDUint, req.SourceMsgID).First(&existing).Error; err == nil {
			return c.JSON(http.StatusOK, existing)
		}
	}

	note := db.DocNote{
		UserID:      userId,
		DocumentID:  uint(docIDUint),
		Content:     req.Content,
		SourceMsgID: req.SourceMsgID,
	}
	if err := db.DB.Create(&note).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to create note"})
	}
	return c.JSON(http.StatusCreated, note)
}

func (h *DocNoteHandler) UpdateNote(c echo.Context) error {
	userId := GetCurrentUserId(c)
	noteID := c.Param("noteId")

	var note db.DocNote
	if err := db.DB.Where("id = ? AND user_id = ?", noteID, userId).First(&note).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "note not found"})
	}

	var req UpdateNoteRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request body"})
	}
	if strings.TrimSpace(req.Content) == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "content cannot be empty"})
	}

	note.Content = req.Content
	note.UpdatedAt = time.Now()
	if err := db.DB.Save(&note).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to update note"})
	}
	return c.JSON(http.StatusOK, note)
}

func (h *DocNoteHandler) DeleteNote(c echo.Context) error {
	userId := GetCurrentUserId(c)
	noteID := c.Param("noteId")

	result := db.DB.Where("id = ? AND user_id = ?", noteID, userId).Delete(&db.DocNote{})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to delete note"})
	}
	if result.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "note not found"})
	}
	return c.JSON(http.StatusOK, echo.Map{"message": "note deleted"})
}

func (h *DocNoteHandler) PushToWiki(c echo.Context) error {
	userId := GetCurrentUserId(c)
	noteID := c.Param("noteId")

	var note db.DocNote
	if err := db.DB.Where("id = ? AND user_id = ?", noteID, userId).First(&note).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "note not found"})
	}

	var doc db.Document
	if err := db.DB.First(&doc, note.DocumentID).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	if doc.Title == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "document has no title"})
	}

	wikiPath := filepath.Join(h.DataDir, "wiki", "sources", doc.Title+".md")

	// Create sources directory if needed
	os.MkdirAll(filepath.Dir(wikiPath), 0755)

	// Build the note block - use clean markdown quote format
	now := time.Now()
	noteBlock := fmt.Sprintf(
		"\n> **[%s]** %s\n",
		now.Format("2006-01-02"),
		note.Content,
	)

	// Read existing wiki content
	existing, err := os.ReadFile(wikiPath)
	if err != nil {
		// File doesn't exist - create with header
		content := "## Chat Notes\n\n" + noteBlock
		if err := os.WriteFile(wikiPath, []byte(content), 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to write wiki file"})
		}
	} else {
		content := string(existing)
		// Check if Chat Notes section exists
		if strings.Contains(content, "## Chat Notes") {
			// Append to the Chat Notes section
			content += noteBlock
		} else {
			// Create Chat Notes section
			content += "\n## Chat Notes\n\n" + noteBlock
		}
		if err := os.WriteFile(wikiPath, []byte(content), 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to write wiki file"})
		}
	}

	// Mark note as wiki-pushed
	note.WikiPushed = true
	note.WikiPushedAt = time.Now()
	db.DB.Save(&note)

	return c.JSON(http.StatusOK, echo.Map{"message": "pushed to wiki", "wikiPath": "wiki/sources/" + doc.Title + ".md"})
}
