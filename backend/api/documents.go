package api

import (
	"llm-knowledge/db"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// DocHandler handles document CRUD operations
type DocHandler struct {
	DataDir string
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

// Publish sets a document's status to "published"
func (h *DocHandler) Publish(c echo.Context) error {
	id := c.Param("id")

	// Check if document exists
	var doc db.Document
	result := db.DB.First(&doc, id)
	if result.Error != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}

	// Update status to published
	if err := db.DB.Model(&doc).Update("status", "published").Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to publish document"})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"id":     doc.ID,
		"status": "published",
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
			// Get the parent directory (e.g., raw/papers/{name}/)
			parentDir := filepath.Dir(rawPath)
			if filepath.Base(parentDir) != "raw" {
				// Remove the entire paper directory
				os.RemoveAll(parentDir)
			} else {
				// Just remove the file if it's directly in raw/
				os.Remove(rawPath)
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
