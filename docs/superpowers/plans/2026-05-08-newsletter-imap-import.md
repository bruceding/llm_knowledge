# Newsletter IMAP Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add newsletter import via IMAP — users configure email credentials, system fetches unread emails from a "Newsletter" folder, parses HTML to markdown, stores as Documents.

**Architecture:** New `crypto` package for AES-256-GCM password encryption. New `IMAPConfig` model in existing DB layer. New `NewsletterHandler` following the established RSS handler pattern (CRUD + sync + auto-scheduler). Frontend additions to Settings (IMAP config form) and Import (sync trigger).

**Tech Stack:** Go stdlib crypto, `github.com/emersion/go-imap/v2`, existing goquery HTML→markdown pipeline, React/TypeScript frontend with i18n.

**Spec:** `docs/superpowers/specs/2026-05-08-newsletter-imap-import-design.md`

---

## File Structure

### New files
| File | Responsibility |
|------|---------------|
| `backend/crypto/crypto.go` | AES-256-GCM encrypt/decrypt with auto-generated key |
| `backend/crypto/crypto_test.go` | Round-trip and error tests |
| `backend/api/newsletter.go` | NewsletterHandler: config CRUD, test connection, sync, auto-sync scheduler |

### Modified files
| File | Change |
|------|--------|
| `backend/db/models.go` | Add `IMAPConfig` struct |
| `backend/db/db.go` | Add `IMAPConfig` to AutoMigrate list |
| `backend/main.go` | Register newsletter API routes, start auto-sync scheduler |
| `backend/go.mod` / `backend/go.sum` | Add `go-imap/v2` dependency |
| `frontend/src/api.ts` | Add IMAP config/test/sync API functions |
| `frontend/src/types.ts` | Add `IMAPConfig`, `IMAPTestResult` types |
| `frontend/src/components/SettingsPage.tsx` | Add IMAP configuration section |
| `frontend/src/components/ImportView.tsx` | Add Newsletter sync section |
| `frontend/src/i18n/locales/en.json` | Add newsletter/IMAP i18n keys |
| `frontend/src/i18n/locales/zh.json` | Add newsletter/IMAP i18n keys (Chinese) |

---

## Task 1: Crypto package — AES-256-GCM encryption

**Files:**
- Create: `backend/crypto/crypto.go`
- Create: `backend/crypto/crypto_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/crypto/crypto_test.go`:

```go
package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// Use a temp dir for key file
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")

	plaintext := "my-secret-password"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if ciphertext == plaintext {
		t.Fatal("ciphertext should not equal plaintext")
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptDecryptWithEnvKey(t *testing.T) {
	// Set env key (32 bytes hex = 64 hex chars)
	os.Setenv("ENCRYPT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	defer os.Unsetenv("ENCRYPT_KEY")
	cachedKey = nil // reset cached key

	plaintext := "another-secret"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}

	cachedKey = nil // cleanup
}

func TestDecryptWithWrongKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")
	cachedKey = nil

	plaintext := "secret"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Change key by regenerating
	os.Remove(keyPath)
	cachedKey = nil

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Fatal("Decrypt should fail with wrong key")
	}
}

func TestEncryptEmptyString(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")
	cachedKey = nil

	ciphertext, err := Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt empty string failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != "" {
		t.Fatalf("expected empty string, got %q", decrypted)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go test ./crypto/ -v`
Expected: compilation error — package `crypto` does not exist yet.

- [ ] **Step 3: Write the implementation**

Create `backend/crypto/crypto.go`:

```go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	cachedKey []byte
	keyMu     sync.Mutex
	keyPath   string
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	keyPath = filepath.Join(home, ".llm-knowledge", "encrypt.key")
}

func getKey() ([]byte, error) {
	keyMu.Lock()
	defer keyMu.Unlock()

	if cachedKey != nil {
		return cachedKey, nil
	}

	// Priority 1: environment variable
	if envKey := os.Getenv("ENCRYPT_KEY"); envKey != "" {
		key, err := hex.DecodeString(envKey)
		if err != nil {
			return nil, fmt.Errorf("ENCRYPT_KEY must be hex-encoded: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("ENCRYPT_KEY must be 32 bytes (64 hex chars), got %d", len(key))
		}
		cachedKey = key
		return cachedKey, nil
	}

	// Priority 2: key file
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := hex.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			cachedKey = key
			return cachedKey, nil
		}
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create key directory: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("failed to write key file: %w", err)
	}

	fmt.Printf("[crypto] Generated new encryption key at %s\n", keyPath)
	cachedKey = key
	return cachedKey, nil
}

func Encrypt(plaintext string) (string, error) {
	key, err := getKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(ciphertext string) (string, error) {
	key, err := getKey()
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go test ./crypto/ -v`
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/crypto/crypto.go backend/crypto/crypto_test.go
git commit -m "feat: add AES-256-GCM crypto package for password encryption"
```

---

## Task 2: IMAPConfig data model and migration

**Files:**
- Modify: `backend/db/models.go` (append new struct)
- Modify: `backend/db/db.go:27-32` (add to AutoMigrate)

- [ ] **Step 1: Add IMAPConfig model**

Add to the end of `backend/db/models.go` (before the closing of the file):

```go
type IMAPConfig struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"uniqueIndex;not null" json:"userId"`
	Host          string    `json:"host"`
	Port          int       `gorm:"default:993" json:"port"`
	Username      string    `json:"username"`
	EncryptedPass string    `json:"-"`
	FolderName    string    `gorm:"default:Newsletter" json:"folderName"`
	AutoSync      bool      `gorm:"default:false" json:"autoSync"`
	LastSyncAt    time.Time `json:"lastSyncAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
```

Note: `EncryptedPass` uses `json:"-"` to never leak to frontend.

- [ ] **Step 2: Add to AutoMigrate**

In `backend/db/db.go`, find the AutoMigrate call (line 27-32) and add `&IMAPConfig{}`:

```go
err = DB.AutoMigrate(
	&User{}, &Session{}, &Captcha{},
	&Document{}, &Tag{}, &DocumentTag{},
	&Conversation{}, &ConversationMessage{},
	&UserSettings{}, &RSSFeed{}, &DocNote{},
	&IMAPConfig{},
)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go build ./...`
Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/db/models.go backend/db/db.go
git commit -m "feat: add IMAPConfig model for newsletter IMAP import"
```

---

## Task 3: Add go-imap dependency

**Files:**
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /Users/dingjing/learn/llm_knowledge/backend && go get github.com/emersion/go-imap/v2@latest
```

- [ ] **Step 2: Verify**

```bash
cd /Users/dingjing/learn/llm_knowledge/backend && grep "go-imap" go.mod
```

Expected: line containing `github.com/emersion/go-imap/v2`.

- [ ] **Step 3: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/go.mod backend/go.sum
git commit -m "chore: add go-imap/v2 dependency for newsletter IMAP import"
```

---

## Task 4: Newsletter handler — config CRUD + test connection

**Files:**
- Create: `backend/api/newsletter.go`

This task implements the config management and connection test endpoints. The sync logic is in Task 5.

- [ ] **Step 1: Create the handler with config CRUD and test connection**

Create `backend/api/newsletter.go`:

```go
package api

import (
	"fmt"
	"llm-knowledge/crypto"
	"llm-knowledge/db"
	"net/http"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/labstack/echo/v4"
)

type NewsletterHandler struct {
	DataDir   string
	ClaudeBin string
}

type IMAPConfigRequest struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	FolderName string `json:"folderName"`
	AutoSync   bool   `json:"autoSync"`
}

type IMAPConfigResponse struct {
	ID         uint      `json:"id"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Username   string    `json:"username"`
	FolderName string    `json:"folderName"`
	AutoSync   bool      `json:"autoSync"`
	LastSyncAt time.Time `json:"lastSyncAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

func toIMAPConfigResponse(cfg *db.IMAPConfig) IMAPConfigResponse {
	return IMAPConfigResponse{
		ID:         cfg.ID,
		Host:       cfg.Host,
		Port:       cfg.Port,
		Username:   cfg.Username,
		FolderName: cfg.FolderName,
		AutoSync:   cfg.AutoSync,
		LastSyncAt: cfg.LastSyncAt,
		CreatedAt:  cfg.CreatedAt,
	}
}

func (h *NewsletterHandler) GetConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusOK, echo.Map{"configured": false})
	}

	resp := toIMAPConfigResponse(&cfg)
	return c.JSON(http.StatusOK, echo.Map{"configured": true, "config": resp})
}

func (h *NewsletterHandler) UpdateConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var req IMAPConfigRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid request"})
	}

	if req.Host == "" || req.Username == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "host and username are required"})
	}
	if req.Port == 0 {
		req.Port = 993
	}
	if req.FolderName == "" {
		req.FolderName = "Newsletter"
	}

	var cfg db.IMAPConfig
	isNew := db.DB.Where("user_id = ?", userId).First(&cfg).Error != nil

	cfg.UserID = userId
	cfg.Host = req.Host
	cfg.Port = req.Port
	cfg.Username = req.Username
	cfg.FolderName = req.FolderName
	cfg.AutoSync = req.AutoSync

	// Only update password if provided (allows updating other fields without re-entering password)
	if req.Password != "" {
		encrypted, err := crypto.Encrypt(req.Password)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to encrypt password"})
		}
		cfg.EncryptedPass = encrypted
	} else if isNew {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "password is required"})
	}

	if err := db.DB.Save(&cfg).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save config"})
	}

	return c.JSON(http.StatusOK, echo.Map{"configured": true, "config": toIMAPConfigResponse(&cfg)})
}

func (h *NewsletterHandler) DeleteConfig(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "config not found"})
	}

	// Delete inbox-status newsletter documents (preserve published/archived)
	var inboxDocs []db.Document
	db.DB.Where("source_type = ? AND user_id = ? AND status = ?", "newsletter", userId, "inbox").Find(&inboxDocs)
	for _, doc := range inboxDocs {
		if doc.RawPath != "" {
			fullPath := fmt.Sprintf("%s/%s", h.DataDir, doc.RawPath)
			// Remove the entire document directory
			removeDir(fullPath)
		}
	}
	db.DB.Where("source_type = ? AND user_id = ? AND status = ?", "newsletter", userId, "inbox").Delete(&db.Document{})

	db.DB.Delete(&cfg)

	return c.JSON(http.StatusOK, echo.Map{"message": "config deleted"})
}

func (h *NewsletterHandler) TestConnection(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"success": false, "message": "IMAP not configured"})
	}

	password, err := crypto.Decrypt(cfg.EncryptedPass)
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": "failed to decrypt password"})
	}

	client, err := imapclient.DialTLS(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), nil)
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": fmt.Sprintf("connection failed: %v", err)})
	}
	defer client.Close()

	if err := client.Login(cfg.Username, password).Wait(); err != nil {
		return c.JSON(http.StatusOK, echo.Map{"success": false, "message": fmt.Sprintf("login failed: %v", err)})
	}

	selectData, err := client.Select(cfg.FolderName, nil).Wait()
	if err != nil {
		return c.JSON(http.StatusOK, echo.Map{
			"success":     false,
			"folderExists": false,
			"message":     fmt.Sprintf("folder '%s' not found: %v", cfg.FolderName, err),
		})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"success":      true,
		"folderExists": true,
		"unseenCount":  selectData.NumMessages,
		"message":      fmt.Sprintf("Connection successful, folder '%s' accessible", cfg.FolderName),
	})
}

// removeDir removes a directory and all its contents (helper for cleanup)
func removeDir(path string) {
	if path != "" {
		os.RemoveAll(path)
	}
}
```

Note: Add `"os"` to the imports (alongside the existing ones).

- [ ] **Step 2: Add missing os import**

Make sure the import block in newsletter.go includes `"os"`:

```go
import (
	"fmt"
	"llm-knowledge/crypto"
	"llm-knowledge/db"
	"net/http"
	"os"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/labstack/echo/v4"
)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go build ./...`
Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/api/newsletter.go
git commit -m "feat: add newsletter handler with IMAP config CRUD and test connection"
```

---

## Task 5: Newsletter handler — sync logic

**Files:**
- Modify: `backend/api/newsletter.go` (add sync methods)

This is the core IMAP sync logic: connect, fetch unread emails, parse HTML, create Documents.

- [ ] **Step 1: Add sync endpoint and internal sync logic**

Append to `backend/api/newsletter.go`, adding required imports and the sync methods:

First, update the imports to include all needed packages:

```go
import (
	"encoding/json"
	"fmt"
	"llm-knowledge/crypto"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/labstack/echo/v4"
)
```

Then add these methods:

```go
func (h *NewsletterHandler) Sync(c echo.Context) error {
	userId := GetCurrentUserId(c)

	var cfg db.IMAPConfig
	if err := db.DB.Where("user_id = ?", userId).First(&cfg).Error; err != nil {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "IMAP not configured"})
	}

	result := h.syncInternal(&cfg)
	if result.Error != "" {
		return c.JSON(http.StatusOK, result)
	}
	return c.JSON(http.StatusOK, result)
}

type NewsletterSyncResult struct {
	NewArticles    int    `json:"newArticles"`
	Total          int    `json:"total"`
	DownloadErrors int    `json:"downloadErrors"`
	Message        string `json:"message"`
	Error          string `json:"error,omitempty"`
}

func (h *NewsletterHandler) syncInternal(cfg *db.IMAPConfig) NewsletterSyncResult {
	password, err := crypto.Decrypt(cfg.EncryptedPass)
	if err != nil {
		return NewsletterSyncResult{Error: "failed to decrypt password: " + err.Error()}
	}

	client, err := imapclient.DialTLS(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), nil)
	if err != nil {
		return NewsletterSyncResult{Error: "connection failed: " + err.Error()}
	}
	defer client.Close()

	if err := client.Login(cfg.Username, password).Wait(); err != nil {
		return NewsletterSyncResult{Error: "login failed: " + err.Error()}
	}

	if _, err := client.Select(cfg.FolderName, nil).Wait(); err != nil {
		return NewsletterSyncResult{Error: fmt.Sprintf("folder '%s' not found: %v", cfg.FolderName, err)}
	}

	// Build search criteria: UNSEEN, and SINCE LastSyncAt for subsequent syncs
	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}
	if !cfg.LastSyncAt.IsZero() {
		criteria.Since = cfg.LastSyncAt
	}

	searchData, err := client.Search(criteria, nil).Wait()
	if err != nil {
		return NewsletterSyncResult{Error: "search failed: " + err.Error()}
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		cfg.LastSyncAt = time.Now()
		db.DB.Save(cfg)
		return NewsletterSyncResult{Message: "No new newsletters"}
	}

	// First sync: limit to 10 most recent
	isFirstSync := cfg.LastSyncAt.IsZero()

	// We need to fetch envelopes first to sort by date for first sync limiting
	fetchOptions := &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}

	uidSet := imap.UIDSetNum(uids...)
	fetchCmd := client.Fetch(uidSet, fetchOptions)

	type fetchedMsg struct {
		uid      imap.UID
		envelope *imap.Envelope
		body     []byte
	}
	var messages []fetchedMsg

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		var envelope *imap.Envelope
		var body []byte

		for {
			item := msg.Next()
			if item == nil {
				break
			}
			switch data := item.(type) {
			case imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			case imapclient.FetchItemDataBodySection:
				body, _ = io.ReadAll(data.Literal)
			}
		}

		if envelope != nil {
			messages = append(messages, fetchedMsg{
				uid:      msg.UID,
				envelope: envelope,
				body:     body,
			})
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return NewsletterSyncResult{Error: "fetch failed: " + err.Error()}
	}

	// Sort by date descending (most recent first)
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].envelope.Date.After(messages[j].envelope.Date)
	})

	// Limit to 10 on first sync
	if isFirstSync && len(messages) > 10 {
		messages = messages[:10]
	}

	total := len(messages)
	newArticles := 0
	downloadErrors := 0
	var processedUIDs []imap.UID

	for _, m := range messages {
		messageID := m.envelope.MessageID
		if messageID == "" {
			messageID = fmt.Sprintf("%s-%s-%d", m.envelope.Subject, m.envelope.Date.Format(time.RFC3339), m.uid)
		}

		// Dedup check (including soft-deleted)
		var existing db.Document
		if db.DB.Unscoped().Where("source_guid = ? AND user_id = ?", messageID, cfg.UserID).First(&existing).Error == nil {
			processedUIDs = append(processedUIDs, m.uid)
			continue
		}

		// Extract sender info
		fromName := ""
		fromAddr := ""
		if len(m.envelope.From) > 0 {
			fromName = m.envelope.From[0].Name
			fromAddr = fmt.Sprintf("%s@%s", m.envelope.From[0].Mailbox, m.envelope.From[0].Host)
			if fromName == "" {
				fromName = fromAddr
			}
		}

		subject := m.envelope.Subject
		if subject == "" {
			subject = "untitled"
		}

		// Parse email body to get HTML
		htmlContent := extractHTMLFromEmail(m.body)
		if htmlContent == "" {
			processedUIDs = append(processedUIDs, m.uid)
			continue
		}

		// Extract "view in browser" link
		viewURL := extractViewInBrowserLink(htmlContent)

		// Setup directories
		senderDir := sanitizeFilename(fromName)
		feedDir := filepath.Join(h.DataDir, "raw", "newsletter", senderDir)
		assetsDir := filepath.Join(feedDir, "assets")
		os.MkdirAll(assetsDir, 0755)

		// Filter decorative images and process HTML to markdown
		filteredHTML := filterDecorativeImages(htmlContent)
		markdown, imgCount, imgErrors := processHTMLToMarkdown(filteredHTML, assetsDir, viewURL)
		downloadErrors += imgErrors

		// Build markdown content with metadata header
		var content strings.Builder
		content.WriteString(fmt.Sprintf("# %s\n\n", subject))
		content.WriteString(fmt.Sprintf("**From:** %s\n", fromName))
		content.WriteString(fmt.Sprintf("**Date:** %s\n", m.envelope.Date.Format("2006-01-02 15:04")))
		if viewURL != "" {
			content.WriteString(fmt.Sprintf("**Link:** %s\n", viewURL))
		}
		content.WriteString("\n## Content\n\n")
		content.WriteString(markdown)

		// Save markdown file
		slug := sanitizeFilename(subject)
		articlePath := filepath.Join(feedDir, slug+".md")
		if err := os.WriteFile(articlePath, []byte(content.String()), 0644); err != nil {
			continue
		}

		// Build metadata
		metadata := map[string]string{
			"from":      fromName,
			"fromAddr":  fromAddr,
			"subject":   subject,
			"date":      m.envelope.Date.Format(time.RFC3339),
			"messageId": messageID,
			"images":    strconv.Itoa(imgCount),
		}
		metadataJSON, _ := json.Marshal(metadata)

		doc := db.Document{
			UserID:     cfg.UserID,
			Title:      subject,
			Slug:       slug,
			SourceType: "newsletter",
			RawPath:    filepath.Join("raw", "newsletter", senderDir, slug+".md"),
			SourceURL:  viewURL,
			SourceGUID: messageID,
			Language:   "en",
			Status:     "inbox",
			Metadata:   string(metadataJSON),
			CreatedAt:  m.envelope.Date,
			UpdatedAt:  time.Now(),
		}

		if err := db.DB.Create(&doc).Error; err != nil {
			continue
		}

		// Auto-create tag from sender name
		if fromName != "" {
			var tag db.Tag
			result := db.DB.Where("name = ? AND user_id = ?", fromName, cfg.UserID).First(&tag)
			if result.Error != nil {
				tag = db.Tag{Name: fromName, Color: "#808080", UserID: cfg.UserID}
				db.DB.Create(&tag)
			}
			db.DB.Create(&db.DocumentTag{DocumentID: doc.ID, TagID: tag.ID})
		}

		// Async summary generation
		if h.ClaudeBin != "" {
			docID := doc.ID
			rawPath := doc.RawPath
			go func() {
				summary, err := ingest.GenerateSummary(h.DataDir, rawPath, h.ClaudeBin)
				if err != nil {
					fmt.Printf("[newsletter] summary generation failed for %d: %v\n", docID, err)
				} else {
					db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("summary", summary)
					fmt.Printf("[newsletter] summary generated for %d\n", docID)
				}
			}()
		}

		processedUIDs = append(processedUIDs, m.uid)
		newArticles++
	}

	// Mark processed messages as \Seen
	if len(processedUIDs) > 0 {
		storeFlags := imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagSeen},
		}
		uidSet := imap.UIDSetNum(processedUIDs...)
		if err := client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
			fmt.Printf("[newsletter] failed to mark messages as seen: %v\n", err)
		}
	}

	cfg.LastSyncAt = time.Now()
	db.DB.Save(cfg)

	msg := fmt.Sprintf("Synced %d new newsletters", newArticles)
	if downloadErrors > 0 {
		msg += fmt.Sprintf(" (%d image download errors)", downloadErrors)
	}

	return NewsletterSyncResult{
		NewArticles:    newArticles,
		Total:          total,
		DownloadErrors: downloadErrors,
		Message:        msg,
	}
}

// extractHTMLFromEmail extracts HTML body from raw email bytes.
// Simple approach: look for HTML content between boundaries or as the full body.
func extractHTMLFromEmail(body []byte) string {
	bodyStr := string(body)

	// Try to find HTML part in multipart message
	// Look for Content-Type: text/html and extract until next boundary
	htmlIdx := strings.Index(strings.ToLower(bodyStr), "content-type: text/html")
	if htmlIdx >= 0 {
		// Find the empty line after headers (separates headers from body)
		headerEnd := strings.Index(bodyStr[htmlIdx:], "\r\n\r\n")
		if headerEnd < 0 {
			headerEnd = strings.Index(bodyStr[htmlIdx:], "\n\n")
		}
		if headerEnd >= 0 {
			contentStart := htmlIdx + headerEnd + 4
			if strings.Contains(bodyStr[htmlIdx:htmlIdx+headerEnd], "\r\n\r\n") {
				contentStart = htmlIdx + headerEnd + 4
			} else {
				contentStart = htmlIdx + headerEnd + 2
			}

			// Find boundary end
			remaining := bodyStr[contentStart:]
			boundaryIdx := strings.Index(remaining, "\r\n--")
			if boundaryIdx < 0 {
				boundaryIdx = strings.Index(remaining, "\n--")
			}
			if boundaryIdx >= 0 {
				return remaining[:boundaryIdx]
			}
			return remaining
		}
	}

	// If the whole body looks like HTML
	if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<HTML") {
		return bodyStr
	}

	return ""
}

// extractViewInBrowserLink finds "view in browser" links in newsletter HTML
func extractViewInBrowserLink(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return ""
	}

	patterns := []string{
		"view in browser",
		"view online",
		"view this email",
		"view in your browser",
		"在浏览器中查看",
		"view it in your browser",
		"read online",
		"open in browser",
	}

	var viewURL string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		if viewURL != "" {
			return
		}
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		for _, pattern := range patterns {
			if strings.Contains(text, pattern) {
				if href, exists := s.Attr("href"); exists && href != "" {
					viewURL = href
					return
				}
			}
		}
	})

	return viewURL
}

// filterDecorativeImages removes tracking pixels and decorative images from HTML
func filterDecorativeImages(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}

	sizePattern := regexp.MustCompile(`(\d+)`)

	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		shouldRemove := false

		// Check explicit width/height attributes
		width, hasWidth := s.Attr("width")
		height, hasHeight := s.Attr("height")

		if hasWidth {
			if w := sizePattern.FindString(width); w != "" {
				if val, err := strconv.Atoi(w); err == nil && val < 50 {
					shouldRemove = true
				}
			}
		}
		if hasHeight {
			if h := sizePattern.FindString(height); h != "" {
				if val, err := strconv.Atoi(h); err == nil && val < 50 {
					shouldRemove = true
				}
			}
		}

		// Check for 1x1 tracking pixels
		if hasWidth && hasHeight {
			w := sizePattern.FindString(width)
			h := sizePattern.FindString(height)
			if w == "1" && h == "1" {
				shouldRemove = true
			}
		}

		// Check inline style for small dimensions
		if style, exists := s.Attr("style"); exists {
			styleLower := strings.ToLower(style)
			if strings.Contains(styleLower, "width:1px") || strings.Contains(styleLower, "height:1px") ||
				strings.Contains(styleLower, "width: 1px") || strings.Contains(styleLower, "height: 1px") {
				shouldRemove = true
			}
			if strings.Contains(styleLower, "display:none") || strings.Contains(styleLower, "display: none") {
				shouldRemove = true
			}
		}

		if shouldRemove {
			s.Remove()
		}
	})

	result, err := doc.Html()
	if err != nil {
		return htmlContent
	}
	return result
}

// StartAutoSyncScheduler starts background newsletter sync on 1-hour interval
func (h *NewsletterHandler) StartAutoSyncScheduler() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		fmt.Println("[newsletter] Auto-sync scheduler started, checking every hour")

		for range ticker.C {
			h.syncAutoSyncConfigs()
		}
	}()
}

func (h *NewsletterHandler) syncAutoSyncConfigs() {
	var configs []db.IMAPConfig
	if err := db.DB.Where("auto_sync = ?", true).Find(&configs).Error; err != nil {
		fmt.Printf("[newsletter] Failed to query auto-sync configs: %v\n", err)
		return
	}

	if len(configs) == 0 {
		return
	}

	fmt.Printf("[newsletter] Checking %d auto-sync configs...\n", len(configs))

	minInterval := 1 * time.Hour
	for _, cfg := range configs {
		if !cfg.LastSyncAt.IsZero() && time.Since(cfg.LastSyncAt) < minInterval {
			continue
		}

		fmt.Printf("[newsletter] Auto-syncing for user %d (%s)\n", cfg.UserID, cfg.Username)
		result := h.syncInternal(&cfg)
		if result.Error != "" {
			fmt.Printf("[newsletter] Auto-sync failed for user %d: %s\n", cfg.UserID, result.Error)
		} else {
			fmt.Printf("[newsletter] Auto-sync completed for user %d: %s\n", cfg.UserID, result.Message)
		}
	}
}
```

- [ ] **Step 2: Add `io` to imports**

The sync method uses `io.ReadAll`, so ensure `"io"` is in the import block:

```go
import (
	"encoding/json"
	"fmt"
	"io"
	"llm-knowledge/crypto"
	"llm-knowledge/db"
	"llm-knowledge/ingest"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/labstack/echo/v4"
)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go build ./...`
Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/api/newsletter.go
git commit -m "feat: add newsletter IMAP sync logic with email parsing and dedup"
```

---

## Task 6: Register routes and scheduler in main.go

**Files:**
- Modify: `backend/main.go:236-246` (after RSS routes)

- [ ] **Step 1: Add newsletter routes and scheduler**

In `backend/main.go`, after the RSS routes block (around line 244, after `rssH.StartAutoSyncScheduler()`), add:

```go
// Newsletter IMAP API (protected)
newsletterH := &api.NewsletterHandler{
	DataDir:   cfg.DataDir,
	ClaudeBin: cfg.ClaudeBin,
}
apiGroup.GET("/imap/config", newsletterH.GetConfig)
apiGroup.PUT("/imap/config", newsletterH.UpdateConfig)
apiGroup.DELETE("/imap/config", newsletterH.DeleteConfig)
apiGroup.POST("/imap/test", newsletterH.TestConnection)
apiGroup.POST("/imap/sync", newsletterH.Sync)

// Start newsletter auto-sync scheduler
newsletterH.StartAutoSyncScheduler()
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/backend && go build ./...`
Expected: compiles without errors.

- [ ] **Step 3: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add backend/main.go
git commit -m "feat: register newsletter IMAP routes and auto-sync scheduler"
```

---

## Task 7: Frontend — types and API client

**Files:**
- Modify: `frontend/src/types.ts` (append new types)
- Modify: `frontend/src/api.ts` (append new API functions)

- [ ] **Step 1: Add TypeScript types**

Append to `frontend/src/types.ts`:

```typescript
// Newsletter IMAP types
export interface IMAPConfig {
  id: number
  host: string
  port: number
  username: string
  folderName: string
  autoSync: boolean
  lastSyncAt: string
  createdAt: string
}

export interface IMAPConfigInput {
  host: string
  port: number
  username: string
  password: string
  folderName: string
  autoSync: boolean
}

export interface IMAPConfigResponse {
  configured: boolean
  config?: IMAPConfig
}

export interface IMAPTestResult {
  success: boolean
  folderExists?: boolean
  unseenCount?: number
  message: string
}

export interface NewsletterSyncResult {
  newArticles: number
  total: number
  downloadErrors: number
  message: string
  error?: string
}
```

- [ ] **Step 2: Add API functions**

Append to `frontend/src/api.ts`, also adding the new types to the import at line 1:

Update the import line at the top of api.ts to include the new types:

```typescript
import type { Document, UpdateDocRequest, SSEEvent, UserSettings, Conversation, Message, LoginResponse, RegisterResponse, CaptchaResponse, IMAPConfigInput, IMAPConfigResponse, IMAPTestResult, NewsletterSyncResult } from './types'
```

Then append these functions at the end of the file (before the last closing brace or at the very end):

```typescript
// Newsletter IMAP API
export async function getIMAPConfig(): Promise<IMAPConfigResponse> {
  const res = await fetch(`${API_BASE}/imap/config`, { headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to fetch IMAP config')
  return res.json()
}

export async function updateIMAPConfig(config: IMAPConfigInput): Promise<IMAPConfigResponse> {
  const res = await fetch(`${API_BASE}/imap/config`, {
    method: 'PUT',
    headers: getHeaders(),
    body: JSON.stringify(config),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Failed to update IMAP config')
  return data
}

export async function deleteIMAPConfig(): Promise<void> {
  const res = await fetch(`${API_BASE}/imap/config`, { method: 'DELETE', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to delete IMAP config')
}

export async function testIMAPConnection(): Promise<IMAPTestResult> {
  const res = await fetch(`${API_BASE}/imap/test`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to test IMAP connection')
  return res.json()
}

export async function syncNewsletter(): Promise<NewsletterSyncResult> {
  const res = await fetch(`${API_BASE}/imap/sync`, { method: 'POST', headers: getHeaders() })
  if (!res.ok) throw new Error('Failed to sync newsletters')
  return res.json()
}
```

- [ ] **Step 3: Verify TypeScript compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add frontend/src/types.ts frontend/src/api.ts
git commit -m "feat: add newsletter IMAP API client and types"
```

---

## Task 8: Frontend — i18n strings

**Files:**
- Modify: `frontend/src/i18n/locales/en.json`
- Modify: `frontend/src/i18n/locales/zh.json`

- [ ] **Step 1: Add English i18n keys**

In `frontend/src/i18n/locales/en.json`, add to the `"import"` section (after the `"deleteFeedConfirm"` line):

```json
"newsletter": "Newsletter",
"newsletterHint": "Import newsletters from your email via IMAP.",
"newsletterNotConfigured": "Configure IMAP in Settings to import newsletters.",
"goToSettings": "Go to Settings",
"syncNewsletter": "Sync Now",
"syncing": "Syncing...",
"lastSync": "Last sync",
"never": "Never",
"deleteImapConfirm": "Are you sure you want to delete the IMAP configuration? Inbox newsletters will be removed."
```

Add to the `"settings"` section (after the `"modelName"` line):

```json
"newsletter": "Newsletter (IMAP)",
"newsletterHint": "Configure IMAP to import newsletters from your email inbox.",
"imapHost": "IMAP Server",
"imapPort": "Port",
"imapUsername": "Username",
"imapPassword": "Password",
"imapPasswordHint": "Gmail: use app-specific password. QQ/163: use authorization code.",
"imapFolder": "Folder Name",
"imapFolderHint": "Create this folder in your email and set up rules to route newsletters there.",
"autoSync": "Auto Sync",
"autoSyncHint": "Automatically sync every hour",
"testConnection": "Test Connection",
"testing": "Testing...",
"testSuccess": "Connection successful",
"testFailed": "Connection failed",
"deleteConfig": "Delete Configuration",
"configSaved": "IMAP configuration saved"
```

- [ ] **Step 2: Add Chinese i18n keys**

In `frontend/src/i18n/locales/zh.json`, add to the `"import"` section (after the `"deleteFeedConfirm"` line):

```json
"newsletter": "Newsletter 邮件",
"newsletterHint": "通过 IMAP 从邮箱导入 Newsletter。",
"newsletterNotConfigured": "请先在设置中配置 IMAP 以导入 Newsletter。",
"goToSettings": "前往设置",
"syncNewsletter": "立即同步",
"syncing": "同步中...",
"lastSync": "上次同步",
"never": "从未",
"deleteImapConfirm": "确定要删除 IMAP 配置吗？收件箱中的 Newsletter 将被移除。"
```

Add to the `"settings"` section (after the `"modelName"` line):

```json
"newsletter": "Newsletter (IMAP)",
"newsletterHint": "配置 IMAP 以从邮箱导入 Newsletter。",
"imapHost": "IMAP 服务器",
"imapPort": "端口",
"imapUsername": "用户名",
"imapPassword": "密码",
"imapPasswordHint": "Gmail 请使用应用专用密码，QQ/163 请使用授权码。",
"imapFolder": "文件夹名",
"imapFolderHint": "在邮箱中创建此文件夹，并设置规则将 Newsletter 自动归类到该文件夹。",
"autoSync": "自动同步",
"autoSyncHint": "每小时自动同步一次",
"testConnection": "测试连接",
"testing": "测试中...",
"testSuccess": "连接成功",
"testFailed": "连接失败",
"deleteConfig": "删除配置",
"configSaved": "IMAP 配置已保存"
```

- [ ] **Step 3: Verify JSON is valid**

Run: `cd /Users/dingjing/learn/llm_knowledge/frontend && node -e "JSON.parse(require('fs').readFileSync('src/i18n/locales/en.json','utf8')); console.log('en.json OK')" && node -e "JSON.parse(require('fs').readFileSync('src/i18n/locales/zh.json','utf8')); console.log('zh.json OK')"`
Expected: "en.json OK" and "zh.json OK".

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add frontend/src/i18n/locales/en.json frontend/src/i18n/locales/zh.json
git commit -m "feat: add newsletter IMAP i18n strings (en + zh)"
```

---

## Task 9: Frontend — Settings page IMAP section

**Files:**
- Modify: `frontend/src/components/SettingsPage.tsx`

- [ ] **Step 1: Add IMAP config state and handlers**

In `SettingsPage.tsx`, add new imports at the top:

```typescript
import { fetchSettings, updateSettings, getIMAPConfig, updateIMAPConfig, deleteIMAPConfig, testIMAPConnection } from '../api'
```

(Replace the existing import line that only imports `fetchSettings, updateSettings`.)

Add state variables after the existing state declarations (around line 16):

```typescript
// IMAP state
const [imapHost, setImapHost] = useState('')
const [imapPort, setImapPort] = useState(993)
const [imapUsername, setImapUsername] = useState('')
const [imapPassword, setImapPassword] = useState('')
const [imapFolder, setImapFolder] = useState('Newsletter')
const [imapAutoSync, setImapAutoSync] = useState(false)
const [imapConfigured, setImapConfigured] = useState(false)
const [imapSaving, setImapSaving] = useState(false)
const [imapTesting, setImapTesting] = useState(false)
const [imapTestResult, setImapTestResult] = useState<string | null>(null)
const [imapTestSuccess, setImapTestSuccess] = useState(false)
const [imapSuccess, setImapSuccess] = useState(false)
const [imapError, setImapError] = useState<string | null>(null)
```

Add IMAP load logic inside the existing `loadSettings` function, after the current settings load:

```typescript
// Load IMAP config
try {
  const imapRes = await getIMAPConfig()
  if (imapRes.configured && imapRes.config) {
    setImapConfigured(true)
    setImapHost(imapRes.config.host)
    setImapPort(imapRes.config.port)
    setImapUsername(imapRes.config.username)
    setImapFolder(imapRes.config.folderName)
    setImapAutoSync(imapRes.config.autoSync)
  }
} catch (err) {
  console.error('Failed to load IMAP config:', err)
}
```

Add handler functions after `handleSave`:

```typescript
const handleImapSave = async () => {
  setImapSaving(true)
  setImapSuccess(false)
  setImapError(null)
  try {
    await updateIMAPConfig({
      host: imapHost,
      port: imapPort,
      username: imapUsername,
      password: imapPassword,
      folderName: imapFolder,
      autoSync: imapAutoSync,
    })
    setImapConfigured(true)
    setImapPassword('')
    setImapSuccess(true)
    setTimeout(() => setImapSuccess(false), 3000)
  } catch (err) {
    setImapError(err instanceof Error ? err.message : 'Failed to save IMAP config')
  } finally {
    setImapSaving(false)
  }
}

const handleImapTest = async () => {
  setImapTesting(true)
  setImapTestResult(null)
  try {
    const result = await testIMAPConnection()
    setImapTestResult(result.message)
    setImapTestSuccess(result.success)
  } catch (err) {
    setImapTestResult(err instanceof Error ? err.message : 'Test failed')
    setImapTestSuccess(false)
  } finally {
    setImapTesting(false)
  }
}

const handleImapDelete = async () => {
  if (!window.confirm(t('import.deleteImapConfirm'))) return
  try {
    await deleteIMAPConfig()
    setImapConfigured(false)
    setImapHost('')
    setImapPort(993)
    setImapUsername('')
    setImapPassword('')
    setImapFolder('Newsletter')
    setImapAutoSync(false)
  } catch (err) {
    setImapError(err instanceof Error ? err.message : 'Failed to delete config')
  }
}
```

- [ ] **Step 2: Add IMAP config UI section**

In the JSX return, after the PDF Translation section's closing `</div>` (around line 175) and before the Messages section, add:

```tsx
{/* Newsletter IMAP Section */}
<div className="bg-white border border-gray-200 rounded-lg p-6 mb-6">
  <h3 className="text-lg font-medium text-gray-800 mb-2">{t('settings.newsletter')}</h3>
  <p className="text-sm text-gray-600 mb-4">{t('settings.newsletterHint')}</p>

  <div className="space-y-4">
    <div className="grid grid-cols-2 gap-4">
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapHost')}</label>
        <input
          type="text"
          value={imapHost}
          onChange={(e) => setImapHost(e.target.value)}
          placeholder="imap.gmail.com"
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapPort')}</label>
        <input
          type="number"
          value={imapPort}
          onChange={(e) => setImapPort(parseInt(e.target.value) || 993)}
          className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
    </div>

    <div>
      <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapUsername')}</label>
      <input
        type="text"
        value={imapUsername}
        onChange={(e) => setImapUsername(e.target.value)}
        placeholder="user@gmail.com"
        className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
    </div>

    <div>
      <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapPassword')}</label>
      <input
        type="password"
        value={imapPassword}
        onChange={(e) => setImapPassword(e.target.value)}
        placeholder={imapConfigured ? '••••••••' : t('settings.imapPassword')}
        className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
      <p className="text-xs text-gray-500 mt-1">{t('settings.imapPasswordHint')}</p>
    </div>

    <div>
      <label className="block text-xs font-medium text-gray-500 mb-1">{t('settings.imapFolder')}</label>
      <input
        type="text"
        value={imapFolder}
        onChange={(e) => setImapFolder(e.target.value)}
        placeholder="Newsletter"
        className="w-full px-3 py-2 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
      <p className="text-xs text-gray-500 mt-1">{t('settings.imapFolderHint')}</p>
    </div>

    <div className="flex items-center gap-3">
      <label className="relative inline-flex items-center cursor-pointer">
        <input
          type="checkbox"
          checked={imapAutoSync}
          onChange={(e) => setImapAutoSync(e.target.checked)}
          className="sr-only peer"
        />
        <div className="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-blue-300 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-blue-500"></div>
      </label>
      <div>
        <span className="text-sm text-gray-700">{t('settings.autoSync')}</span>
        <p className="text-xs text-gray-500">{t('settings.autoSyncHint')}</p>
      </div>
    </div>

    {imapTestResult && (
      <div className={`p-3 rounded-lg text-sm ${imapTestSuccess ? 'bg-green-50 border border-green-200 text-green-700' : 'bg-red-50 border border-red-200 text-red-700'}`}>
        {imapTestResult}
      </div>
    )}

    {imapSuccess && (
      <div className="p-3 bg-green-50 border border-green-200 rounded-lg text-green-700 text-sm">
        {t('settings.configSaved')}
      </div>
    )}

    {imapError && (
      <div className="p-3 bg-red-50 border border-red-200 rounded-lg text-red-700 text-sm">
        {imapError}
      </div>
    )}

    <div className="flex gap-3">
      <button
        onClick={handleImapSave}
        disabled={imapSaving || !imapHost || !imapUsername}
        className="px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 text-sm"
      >
        {imapSaving ? t('common.loading') : t('common.save')}
      </button>
      {imapConfigured && (
        <>
          <button
            onClick={handleImapTest}
            disabled={imapTesting}
            className="px-4 py-2 border border-gray-300 text-gray-700 rounded-lg hover:bg-gray-50 transition-colors disabled:text-gray-400 text-sm"
          >
            {imapTesting ? t('settings.testing') : t('settings.testConnection')}
          </button>
          <button
            onClick={handleImapDelete}
            className="px-4 py-2 border border-red-300 text-red-600 rounded-lg hover:bg-red-50 transition-colors text-sm"
          >
            {t('settings.deleteConfig')}
          </button>
        </>
      )}
    </div>
  </div>
</div>
```

- [ ] **Step 3: Verify TypeScript compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add frontend/src/components/SettingsPage.tsx
git commit -m "feat: add IMAP configuration section to Settings page"
```

---

## Task 10: Frontend — Import page Newsletter section

**Files:**
- Modify: `frontend/src/components/ImportView.tsx`

- [ ] **Step 1: Add newsletter state and handlers**

In `ImportView.tsx`, update the import at line 3 to include newsletter API functions:

```typescript
import { uploadPDF, uploadPDFUrl, clipWeb, addRSSFeed, listRSSFeeds, deleteRSSFeed, syncRSSFeed, getIMAPConfig, syncNewsletter } from '../api'
```

Add state variables after the existing RSS state (around line 29):

```typescript
// Newsletter state
const [newsletterConfigured, setNewsletterConfigured] = useState(false)
const [newsletterLastSync, setNewsletterLastSync] = useState<string | null>(null)
const [syncingNewsletter, setSyncingNewsletter] = useState(false)
```

Add newsletter loading inside the existing `useEffect` (after `loadRSSFeeds()`):

```typescript
loadNewsletterConfig()
```

Add the load function after `loadRSSFeeds`:

```typescript
const loadNewsletterConfig = async () => {
  try {
    const res = await getIMAPConfig()
    setNewsletterConfigured(res.configured)
    if (res.configured && res.config) {
      setNewsletterLastSync(res.config.lastSyncAt)
    }
  } catch (err) {
    console.error('Failed to load newsletter config:', err)
  }
}
```

Add sync handler after `handleDeleteFeed`:

```typescript
const handleSyncNewsletter = async () => {
  setSyncingNewsletter(true)
  setError(null)

  try {
    const result = await syncNewsletter()
    if (result.error) {
      setError(result.error)
    } else if (result.newArticles > 0) {
      setUploadResult({
        id: 0,
        path: '',
        message: result.message,
        pages: result.newArticles,
      })
    } else {
      setUploadResult({
        id: 0,
        path: '',
        message: result.message,
        pages: 0,
      })
    }
    await loadNewsletterConfig()
  } catch (err) {
    setError(err instanceof Error ? err.message : 'Failed to sync newsletters')
  } finally {
    setSyncingNewsletter(false)
  }
}
```

- [ ] **Step 2: Add Newsletter UI section**

In the JSX, after the RSS Feeds `</div>` block (the outer RSS container closing tag, around line 434), add:

```tsx
{/* Newsletter */}
<h3 className="text-lg font-semibold text-gray-700 mt-6">{t('import.newsletter')}</h3>
<div className="border border-gray-200 rounded-lg p-6">
  <p className="text-gray-600 mb-4 text-sm">{t('import.newsletterHint')}</p>

  {!newsletterConfigured ? (
    <div className="text-center py-4">
      <p className="text-gray-500 mb-3 text-sm">{t('import.newsletterNotConfigured')}</p>
      <a
        href="/settings"
        className="inline-block px-4 py-2 bg-blue-500 text-white rounded-lg hover:bg-blue-600 transition-colors text-sm"
      >
        {t('import.goToSettings')}
      </a>
    </div>
  ) : (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-sm text-gray-600">
          {t('import.lastSync')}: {newsletterLastSync && newsletterLastSync !== '0001-01-01T00:00:00Z'
            ? new Date(newsletterLastSync).toLocaleString()
            : t('import.never')}
        </div>
        <button
          onClick={handleSyncNewsletter}
          disabled={syncingNewsletter}
          className="px-4 py-2 bg-purple-500 text-white rounded-lg hover:bg-purple-600 transition-colors disabled:bg-gray-300 disabled:text-gray-500 text-sm"
        >
          {syncingNewsletter ? t('import.syncing') : t('import.syncNewsletter')}
        </button>
      </div>
    </div>
  )}
</div>
```

- [ ] **Step 3: Verify TypeScript compilation**

Run: `cd /Users/dingjing/learn/llm_knowledge/frontend && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add frontend/src/components/ImportView.tsx
git commit -m "feat: add Newsletter sync section to Import page"
```

---

## Task 11: Manual end-to-end test

- [ ] **Step 1: Build and start the application**

```bash
cd /Users/dingjing/learn/llm_knowledge/frontend && npm run build
cd /Users/dingjing/learn/llm_knowledge/backend && go run main.go
```

- [ ] **Step 2: Test IMAP configuration flow**

1. Open browser to `http://localhost:3456/settings`
2. Scroll to "Newsletter (IMAP)" section
3. Enter IMAP credentials (e.g. Gmail with app password)
4. Click "Save"
5. Verify "IMAP configuration saved" message appears
6. Click "Test Connection"
7. Verify connection success message appears

- [ ] **Step 3: Test sync flow**

1. Navigate to `http://localhost:3456/import`
2. Scroll to "Newsletter" section
3. Verify it shows "Last sync: Never" and "Sync Now" button
4. Click "Sync Now"
5. Verify sync result shows (e.g. "Synced N new newsletters")
6. Navigate to Inbox and verify newsletter documents appear
7. Click on a newsletter document
8. Verify content is readable markdown with images
9. Press "o" to verify "view in browser" link opens (if available)

- [ ] **Step 4: Final commit**

```bash
cd /Users/dingjing/learn/llm_knowledge
git add -A
git commit -m "feat: newsletter IMAP import — complete implementation"
```
