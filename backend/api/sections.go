package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"llm-knowledge/db"
	"llm-knowledge/ingest"

	"github.com/labstack/echo/v4"
)

type sectionDTO struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	HasBody     bool   `json:"hasBody"`
	Explanation string `json:"explanation,omitempty"`
	Generating  bool   `json:"generating,omitempty"`
}

// loadOwnedPDF returns the paper dir (relative to userDir) of the owned PDF
// document, or emits an error response and returns ok=false.
func loadOwnedPDF(c echo.Context) (string, bool) {
	userId := GetCurrentUserId(c)
	id := c.Param("id")
	var doc db.Document
	if err := db.DB.Where("id = ? AND user_id = ?", id, userId).First(&doc).Error; err != nil {
		_ = c.JSON(http.StatusNotFound, echo.Map{"error": "document not found"})
		return "", false
	}
	if doc.SourceType != "pdf" {
		_ = c.JSON(http.StatusBadRequest, echo.Map{"error": "only PDF documents have sections"})
		return "", false
	}
	return StripUserPrefix(doc.RawPath), true
}

// generatingTimeout is how long a .generating marker is considered valid.
const generatingTimeout = 3 * time.Minute

// buildSectionDTOs maps sections to DTOs, attaching HasBody and any cached
// explanation. LoadSectionExplain is only attempted when a body file exists
// (no body → no cached explanation possible).
func buildSectionDTOs(sections []ingest.Section, sectionsDir string) []sectionDTO {
	out := make([]sectionDTO, 0, len(sections))
	for _, s := range sections {
		dto := sectionDTO{Index: s.Index, Title: s.Title, Slug: s.Slug, HasBody: ingest.SectionBodyExists(sectionsDir, s.Slug)}
		if dto.HasBody {
			if exp, ok := ingest.LoadSectionExplain(sectionsDir, s.Slug); ok {
				dto.Explanation = exp
			} else if ingest.IsGenerating(sectionsDir, s.Slug, generatingTimeout) {
				dto.Generating = true
			}
		}
		out = append(out, dto)
	}
	return out
}

// ListSections returns the cached section list (after Sectionize has run) with
// any cached explanations. GET /api/documents/:id/sections
// Response: { "sections": [...], "paperMdExists": bool, "sectionized": bool }
func (h *DocHandler) ListSections(c echo.Context) error {
	rawRelPath, ok := loadOwnedPDF(c)
	if !ok {
		return nil
	}
	userDir := GetUserDir(c)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")
	if _, err := os.Stat(paperMdPath); err != nil {
		return c.JSON(http.StatusOK, echo.Map{"sections": []sectionDTO{}, "paperMdExists": false, "sectionized": false})
	}
	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	sections, ok := ingest.LoadSectionIndex(sectionsDir)
	if !ok {
		// Not sectionized yet — frontend triggers POST /sectionize.
		return c.JSON(http.StatusOK, echo.Map{"sections": []sectionDTO{}, "paperMdExists": true, "sectionized": false})
	}
	return c.JSON(http.StatusOK, echo.Map{"sections": buildSectionDTOs(sections, sectionsDir), "paperMdExists": true, "sectionized": true})
}

// Sectionize runs one Claude call to identify the paper's chapters and caches
// the result. POST /api/documents/:id/sections/sectionize
func (h *DocHandler) Sectionize(c echo.Context) error {
	rawRelPath, ok := loadOwnedPDF(c)
	if !ok {
		return nil
	}
	if h.ClaudeBin == "" {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "claude binary not configured"})
	}
	userDir := GetUserDir(c)
	paperMdPath := filepath.Join(userDir, rawRelPath, "paper.md")
	if _, err := os.Stat(paperMdPath); err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "paper content not generated yet"})
	}
	sections, err := ingest.Sectionize(userDir, rawRelPath, h.ClaudeBin)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to sectionize: " + err.Error()})
	}
	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	return c.JSON(http.StatusOK, echo.Map{"sections": buildSectionDTOs(sections, sectionsDir), "paperMdExists": true, "sectionized": true})
}

// GenerateSection kicks off async explanation generation for one section.
// It writes a .generating marker, starts a goroutine, and returns 202
// immediately. The frontend polls GET /sections to detect completion.
// POST /api/documents/:id/sections/:index/generate
func (h *DocHandler) GenerateSection(c echo.Context) error {
	rawRelPath, ok := loadOwnedPDF(c)
	if !ok {
		return nil
	}
	idx, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid section index"})
	}
	if h.ClaudeBin == "" {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "claude binary not configured"})
	}
	userDir := GetUserDir(c)
	sectionsDir := filepath.Join(userDir, rawRelPath, "sections")
	sections, ok := ingest.LoadSectionIndex(sectionsDir)
	if !ok {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "paper not sectionized yet"})
	}
	if idx < 0 || idx >= len(sections) {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "section index out of range"})
	}
	section := sections[idx]

	srcRelPath := filepath.ToSlash(filepath.Join(rawRelPath, "sections", section.Slug+".src.md"))
	srcAbs := filepath.Join(userDir, srcRelPath)
	if _, err := os.Stat(srcAbs); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "section has no standalone body"})
	}

	if err := ingest.MarkGenerating(sectionsDir, section.Slug); err != nil {
		if ingest.IsGenerating(sectionsDir, section.Slug, generatingTimeout) {
			return c.JSON(http.StatusAccepted, echo.Map{"status": "already generating"})
		}
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to mark generating"})
	}

	claudeBin := h.ClaudeBin
	go func() {
		defer ingest.ClearGenerating(sectionsDir, section.Slug)
		explanation, err := ingest.GenerateSectionExplain(userDir, srcRelPath, section.Title, claudeBin)
		if err != nil || explanation == "" {
			return
		}
		_ = ingest.SaveSectionExplain(sectionsDir, section.Slug, explanation)
	}()

	return c.JSON(http.StatusAccepted, echo.Map{"status": "generating"})
}
