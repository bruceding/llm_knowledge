package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"llm-knowledge/db"
	"llm-knowledge/ingest"

	"github.com/labstack/echo/v4"
)

type sectionDTO struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Explanation string `json:"explanation,omitempty"`
}

// ListSections returns the paper's chapter list with any cached explanations.
// GET /api/documents/:id/sections
// Response: { "sections": [...], "paperMdExists": bool }
func (h *DocHandler) ListSections(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")

	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}
	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents have sections"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")

	// Missing paper.md → empty list + flag so the UI can guide the user to
	// extract first, rather than showing a hard error.
	if _, err := os.Stat(paperMdPath); err != nil {
		return c.JSON(http.StatusOK, echo.Map{"sections": []sectionDTO{}, "paperMdExists": false})
	}

	sections, err := ingest.SplitSections(paperMdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to split sections"})
	}

	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	out := make([]sectionDTO, 0, len(sections))
	for _, s := range sections {
		dto := sectionDTO{Index: s.Index, Title: s.Title, Slug: s.Slug}
		if exp, ok := ingest.LoadSectionExplain(sectionsDir, s.Slug); ok {
			dto.Explanation = exp
		}
		out = append(out, dto)
	}
	return c.JSON(http.StatusOK, echo.Map{"sections": out, "paperMdExists": true})
}

// GenerateSection generates (or regenerates) the explanation for one section
// by index, caches it, and returns it. Blocking -p call, no streaming.
// POST /api/documents/:id/sections/:index/generate
func (h *DocHandler) GenerateSection(c echo.Context) error {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid section index"})
	}

	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
	}
	if doc.SourceType != "pdf" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents have sections"})
	}
	if h.ClaudeBin == "" {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "claude binary not configured"})
	}

	userDir := GetUserDir(c)
	rawRelPath := StripUserPrefix(doc.RawPath)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")
	// Missing paper.md is a client/404 condition, not a 500 — and we must not
	// leak the absolute path via SplitSections' os.ReadFile error. Mirror
	// ListSections' handling.
	if _, err := os.Stat(paperMdPath); err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "paper content not generated yet"})
	}
	sections, err := ingest.SplitSections(paperMdPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to split sections"})
	}
	if idx < 0 || idx >= len(sections) {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "section index out of range"})
	}

	section := sections[idx]
	paperRelPath := filepath.ToSlash(filepath.Join(rawRelPath, "paper.md"))
	explanation, err := ingest.GenerateSectionExplain(userDir, paperRelPath, section.Title, h.ClaudeBin)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to generate explanation: " + err.Error()})
	}
	// Don't cache or return an empty explanation — it would be omitted by the
	// DTO's omitempty and the UI would silently re-show the Generate button.
	if explanation == "" {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "claude returned empty explanation"})
	}

	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	if err := ingest.SaveSectionExplain(sectionsDir, section.Slug, explanation); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to cache explanation: " + err.Error()})
	}

	return c.JSON(http.StatusOK, sectionDTO{
		Index:       section.Index,
		Title:       section.Title,
		Slug:        section.Slug,
		Explanation: explanation,
	})
}
