package api

import (
	"fmt"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupQueryTestDB creates an in-memory test database with Conversation tables.
func setupQueryTestDB(t *testing.T) {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect test database: %v", err)
	}
	db.DB = testDB
	testDB.AutoMigrate(&db.Conversation{}, &db.ConversationMessage{})
}

func cleanupQueryTestDB(t *testing.T) {
	t.Helper()
}

// setupQueryHandler creates a QueryHandler with a session pool for testing.
// Uses a non-existent claude binary to avoid accidentally starting real sessions.
func setupQueryHandler(t *testing.T) *QueryHandler {
	t.Helper()
	setupQueryTestDB(t)
	t.Cleanup(func() { cleanupQueryTestDB(t) })

	dataDir := t.TempDir()
	pool := claude.NewQuerySessionPool(dataDir, "/nonexistent/claude")
	t.Cleanup(func() { pool.Close() })

	return &QueryHandler{
		DataDir:   dataDir,
		ClaudeBin: "/nonexistent/claude",
		Pool:      pool,
	}
}

// TestMessageHandler_Validation tests that the Message handler properly
// validates request fields.
func TestMessageHandler_Validation(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	tests := []struct {
		name       string
		body       string
		expectCode int
		expectErr  string
	}{
		{
			name:       "empty message",
			body:       `{"conversationId": 1, "message": ""}`,
			expectCode: http.StatusBadRequest,
			expectErr:  "message is required",
		},
		{
			name:       "missing conversationId",
			body:       `{"message": "hello"}`,
			expectCode: http.StatusBadRequest,
			expectErr:  "conversationId is required",
		},
		{
			name:       "invalid JSON",
			body:       `{invalid}`,
			expectCode: http.StatusBadRequest,
			expectErr:  "invalid request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/query/message", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.Set("userId", uint(1))

			err := handler.Message(c)
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if rec.Code != tt.expectCode {
				t.Errorf("expected status %d, got %d; body: %s", tt.expectCode, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.expectErr) {
				t.Errorf("expected error message containing %q, got %q", tt.expectErr, rec.Body.String())
			}
		})
	}
}

// TestMessageHandler_ConversationNotFound tests that accessing a conversation
// owned by another user returns 404.
func TestMessageHandler_ConversationNotFound(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	// Create a conversation owned by user 2
	conv := db.Conversation{
		Title:     "Other User Chat",
		UserID:    2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	db.DB.Create(&conv)

	body := `{"conversationId": 1, "message": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/query/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("userId", uint(1)) // Different user

	err := handler.Message(c)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateConversation tests the CreateConversation endpoint.
func TestCreateConversation(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	tests := []struct {
		name        string
		body        string
		expectCode  int
		expectTitle string
	}{
		{
			name:        "with custom title",
			body:        `{"title": "My Chat"}`,
			expectCode:  http.StatusOK,
			expectTitle: "My Chat",
		},
		{
			name:        "empty title defaults to New Chat",
			body:        `{"title": ""}`,
			expectCode:  http.StatusOK,
			expectTitle: "New Chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/query/conversation", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.Set("userId", uint(1))

			err := handler.CreateConversation(c)
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if rec.Code != tt.expectCode {
				t.Errorf("expected status %d, got %d; body: %s", tt.expectCode, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDeleteConversation tests that deleting a conversation removes it from DB.
func TestDeleteConversation(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	// Create a conversation
	conv := db.Conversation{
		Title:     "To Delete",
		UserID:    1,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	db.DB.Create(&conv)

	req := httptest.NewRequest(http.MethodDelete, "/api/conversations/"+fmt.Sprint(conv.ID), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("userId", uint(1))
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprint(conv.ID))

	err := handler.DeleteConversation(c)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify deleted from DB
	var count int64
	db.DB.Model(&db.Conversation{}).Where("id = ?", conv.ID).Count(&count)
	if count != 0 {
		t.Errorf("conversation should be deleted from DB, but count=%d", count)
	}
}

// TestListConversations tests listing conversations for a user.
func TestListConversations(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	// Create conversations for user 1
	for i := 0; i < 3; i++ {
		db.DB.Create(&db.Conversation{
			Title:     "Chat",
			UserID:    1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	}
	// Create conversation for user 2 (should not appear)
	db.DB.Create(&db.Conversation{
		Title:     "Other Chat",
		UserID:    2,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("userId", uint(1))

	err := handler.ListConversations(c)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	// Should only return user 1's conversations
	if !strings.Contains(rec.Body.String(), `"userId":1`) {
		t.Errorf("expected user 1 conversations in response, got: %s", rec.Body.String())
	}
}

// TestStreamHandler_MissingConversationId tests that Stream returns 400
// when conversationId query param is missing.
func TestStreamHandler_MissingConversationId(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/api/query/stream", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("userId", uint(1))

	err := handler.Stream(c)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestInterruptHandler_NoActiveSession tests that interrupt returns success
// when there's no active session.
func TestInterruptHandler_NoActiveSession(t *testing.T) {
	handler := setupQueryHandler(t)
	e := echo.New()

	// Create a conversation
	conv := db.Conversation{
		Title:     "Test Chat",
		UserID:    1,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	db.DB.Create(&conv)

	body := fmt.Sprintf(`{"conversationId": %d}`, conv.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/query/interrupt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set("userId", uint(1))

	err := handler.Interrupt(c)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
