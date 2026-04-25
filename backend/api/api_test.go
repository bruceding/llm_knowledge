package api

import (
	"encoding/json"
	"fmt"
	"llm-knowledge/db"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) {
	testDB, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect test database: %v", err)
	}
	db.DB = testDB

	// Auto migrate
	testDB.AutoMigrate(&db.Document{}, &db.Tag{})
}

// cleanupTestDB removes the test database file
func cleanupTestDB(t *testing.T) {
	os.Remove("test.db")
}

// setupTestEcho creates a new Echo instance for testing
func setupTestEcho() *echo.Echo {
	e := echo.New()
	return e
}

func TestPagesHandler_CheckPages(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	// Create test data directory
	dataDir := t.TempDir()
	rawPath := "papers/test-paper"
	pagesDir := filepath.Join(dataDir, rawPath, "pages")
	os.MkdirAll(pagesDir, 0755)

	// Create test document
	doc := db.Document{
		Title:      "Test Paper",
		RawPath:    rawPath,
		SourceType: "pdf",
	}
	db.DB.Create(&doc)

	// Create some test page images
	for i := 1; i <= 3; i++ {
		filename := fmt.Sprintf("page_%d.png", i)
		os.WriteFile(filepath.Join(pagesDir, filename), []byte("test"), 0644)
	}

	e := setupTestEcho()
	handler := &PagesHandler{DataDir: dataDir}

	// Test CheckPages
	req := httptest.NewRequest(http.MethodGet, "/api/documents/1/pages-status", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err := handler.CheckPages(c)
	if err != nil {
		t.Fatalf("CheckPages failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)

	if result["exists"] != true {
		t.Errorf("expected exists=true, got %v", result["exists"])
	}

	pageCount := result["page_count"].(float64)
	if pageCount != 3 {
		t.Errorf("expected page_count=3, got %d", int(pageCount))
	}
}

func TestPagesHandler_CheckPages_NotFound(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	dataDir := t.TempDir()

	// Create test document without pages
	doc := db.Document{
		Title:      "Test Paper",
		RawPath:    "papers/no-pages",
		SourceType: "pdf",
	}
	db.DB.Create(&doc)

	e := setupTestEcho()
	handler := &PagesHandler{DataDir: dataDir}

	req := httptest.NewRequest(http.MethodGet, "/api/documents/1/pages-status", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err := handler.CheckPages(c)
	if err != nil {
		t.Fatalf("CheckPages failed: %v", err)
	}

	var result map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &result)

	if result["exists"] != false {
		t.Errorf("expected exists=false, got %v", result["exists"])
	}
}

func TestTranslateHandler_InvalidRequest(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	e := setupTestEcho()
	handler := &TranslateHandler{DataDir: t.TempDir(), ClaudeBin: "/nonexistent/claude"}

	// Test empty body - c.Bind returns nil for empty body, docId will be 0
	req := httptest.NewRequest(http.MethodPost, "/api/translate", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler.Translate(c)
	// For empty body, docId will be 0, handler returns 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rec.Code)
	}

	// Test invalid targetLang
	body := `{"docId": 1, "targetLang": "invalid"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/translate", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)

	handler.Translate(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid targetLang, got %d", rec2.Code)
	}
}

func TestTranslateHandler_DocumentNotFound(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	e := setupTestEcho()
	handler := &TranslateHandler{DataDir: t.TempDir(), ClaudeBin: "/nonexistent/claude"}

	body := `{"docId": 999, "targetLang": "zh"}`
	req := httptest.NewRequest(http.MethodPost, "/api/translate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler.Translate(c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}