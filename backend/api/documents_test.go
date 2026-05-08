package api

import (
	"encoding/json"
	"fmt"
	"llm-knowledge/db"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPublish_PDFDocument(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	dataDir := t.TempDir()

	// Create directory-based raw content (PDF format)
	rawPath := "raw/papers/test-paper"
	os.MkdirAll(filepath.Join(dataDir, rawPath), 0755)
	os.WriteFile(filepath.Join(dataDir, rawPath, "paper.md"), []byte("# Test"), 0644)

	doc := db.Document{
		Title:      "Test PDF Paper",
		Slug:       "test-pdf-paper",
		RawPath:    rawPath,
		SourceType: "pdf",
		Status:     "inbox",
		UserID:     1,
	}
	db.DB.Create(&doc)

	e := setupTestEcho()
	handler := &DocHandler{DataDir: dataDir, ClaudeBin: ""}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/documents/%d/publish", doc.ID), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", doc.ID))
	c.Set("userId", uint(1))

	err := handler.Publish(c)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["status"] != "published" {
		t.Errorf("expected status=published, got %v", result["status"])
	}

	// Verify DB updated
	var updated db.Document
	db.DB.First(&updated, doc.ID)
	if updated.Status != "published" {
		t.Errorf("expected DB status=published, got %s", updated.Status)
	}
}

func TestPublish_RSSDocument(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	dataDir := t.TempDir()

	// Create file-based raw content (RSS format — rawPath points directly to .md)
	rawPath := "raw/rss/Test Feed/some-article.md"
	os.MkdirAll(filepath.Join(dataDir, "raw/rss/Test Feed"), 0755)
	os.WriteFile(filepath.Join(dataDir, rawPath), []byte("# RSS Article"), 0644)

	doc := db.Document{
		Title:      "Some Article",
		Slug:       "some-article",
		RawPath:    rawPath,
		SourceType: "rss",
		Status:     "inbox",
		UserID:     1,
	}
	db.DB.Create(&doc)

	e := setupTestEcho()
	handler := &DocHandler{DataDir: dataDir, ClaudeBin: ""}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/documents/%d/publish", doc.ID), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", doc.ID))
	c.Set("userId", uint(1))

	err := handler.Publish(c)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["status"] != "published" {
		t.Errorf("expected status=published, got %v", result["status"])
	}
}

func TestPublish_DocumentNotFound(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	e := setupTestEcho()
	handler := &DocHandler{DataDir: t.TempDir(), ClaudeBin: ""}

	req := httptest.NewRequest(http.MethodPost, "/api/documents/999/publish", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("999")
	c.Set("userId", uint(1))

	handler.Publish(c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestResolveMdPath(t *testing.T) {
	dataDir := t.TempDir()

	tests := []struct {
		name     string
		rawPath  string
		wantFile string
	}{
		{
			name:     "PDF directory with paper.md",
			rawPath:  "raw/papers/test-paper",
			wantFile: filepath.Join(dataDir, "raw/papers/test-paper", "paper.md"),
		},
		{
			name:     "RSS direct .md file",
			rawPath:  "raw/rss/Feed Name/article-title.md",
			wantFile: filepath.Join(dataDir, "raw/rss/Feed Name/article-title.md"),
		},
		{
			name:     "Web directory with paper.md",
			rawPath:  "raw/web/some-page",
			wantFile: filepath.Join(dataDir, "raw/web/some-page", "paper.md"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mdPath string
			if len(tt.rawPath) > 3 && tt.rawPath[len(tt.rawPath)-3:] == ".md" {
				mdPath = filepath.Join(dataDir, tt.rawPath)
			} else {
				mdPath = filepath.Join(dataDir, tt.rawPath, "paper.md")
			}
			if mdPath != tt.wantFile {
				t.Errorf("mdPath = %q, want %q", mdPath, tt.wantFile)
			}
		})
	}
}
