package api

import (
	"context"
	"llm-knowledge/claude"
	"llm-knowledge/db"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeClaude writes a stub claude executable that emits a system.init
// event echoing the provided session id, then idles so the session stays open
// long enough for the test to observe the callback. Returns the executable path.
func writeFakeClaude(t *testing.T, dir, emittedSessionID string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-claude.sh")
	script := `#!/bin/sh
printf '{"type":"system","subtype":"init","session_id":"` + emittedSessionID + `"}\n'
# Idle so readEvents has time to process before the pipe closes.
sleep 5
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return path
}

// TestDocChat_PersistsChatSessionIDOnInit verifies the wiring that lets doc-chat
// recover after the in-memory session is cleaned up: the onRealSessionID
// callback the Stream handler registers must fire on system.init and write
// the resulting session_id to Document.ChatSessionID so the next /stream call
// can pass it as prevSessionID for --resume.
func TestDocChat_PersistsChatSessionIDOnInit(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	fakeBin := writeFakeClaude(t, tmp, "real-session-abc")

	doc := db.Document{Title: "Test Doc", UserID: 1}
	if err := db.DB.Create(&doc).Error; err != nil {
		t.Fatalf("create doc: %v", err)
	}

	pool := claude.NewSessionPool(tmp, fakeBin)
	defer pool.Close()

	callbackFired := make(chan string, 1)
	docID := doc.ID
	session, err := pool.StartSession(
		context.Background(),
		"docInfo",
		1,    // userID
		docID,
		tmp,  // userDir
		"",   // prevSessionID — fresh start path
		func(oldID, newID string) {
			// Mirror the production callback installed in DocChatHandler.Stream.
			db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("chat_session_id", newID)
			callbackFired <- newID
		},
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	select {
	case newID := <-callbackFired:
		if newID != "real-session-abc" {
			t.Errorf("callback got %q, want real-session-abc", newID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("onRealSessionID callback never fired")
	}

	var refreshed db.Document
	if err := db.DB.First(&refreshed, docID).Error; err != nil {
		t.Fatalf("reload doc: %v", err)
	}
	if refreshed.ChatSessionID != "real-session-abc" {
		t.Errorf("Document.ChatSessionID = %q, want real-session-abc", refreshed.ChatSessionID)
	}
}

// TestDocChat_FreshStartDoesNotResume sanity-checks that an empty prevSessionID
// does NOT cause Claude to be invoked with --resume — guards against future
// refactors silently always-resuming.
func TestDocChat_FreshStartDoesNotResume(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	// Fake binary captures its own argv to a file so the test can inspect it.
	argFile := filepath.Join(tmp, "args.log")
	script := `#!/bin/sh
echo "$@" > ` + argFile + `
sleep 5
`
	binPath := filepath.Join(tmp, "fake-claude.sh")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	pool := claude.NewSessionPool(tmp, binPath)
	defer pool.Close()

	session, err := pool.StartSession(
		context.Background(),
		"docInfo", 1, 1, tmp,
		"", // no prevSessionID
		nil,
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// Give the script a moment to dump its args.
	// Poll for the script to flush its argv to disk; the goroutine fork+exec
	// can take >300ms on a loaded machine.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(argFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if strings.Contains(string(data), "--resume") {
		t.Errorf("args contain --resume on fresh start: %s", data)
	}
}

// TestDocChat_PrevSessionIDAddsResume verifies a real prev session id triggers
// --resume in the Claude argv, so backend session cleanup is recoverable.
func TestDocChat_PrevSessionIDAddsResume(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	argFile := filepath.Join(tmp, "args.log")
	script := `#!/bin/sh
echo "$@" > ` + argFile + `
sleep 5
`
	binPath := filepath.Join(tmp, "fake-claude.sh")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	pool := claude.NewSessionPool(tmp, binPath)
	defer pool.Close()

	session, err := pool.StartSession(
		context.Background(),
		"docInfo", 1, 1, tmp,
		"prev-real-id-xyz",
		nil,
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// Poll for the script to flush its argv to disk; the goroutine fork+exec
	// can take >300ms on a loaded machine.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(argFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if !strings.Contains(string(data), "--resume prev-real-id-xyz") {
		t.Errorf("args missing --resume prev-real-id-xyz: %s", data)
	}
}

// TestDocChat_LocalFallbackIDSkipsResume guards against passing a local-xxx
// fallback id (used before system.init arrives) to --resume — that id is not a
// real Claude session and would just produce an error.
func TestDocChat_LocalFallbackIDSkipsResume(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	argFile := filepath.Join(tmp, "args.log")
	script := `#!/bin/sh
echo "$@" > ` + argFile + `
sleep 5
`
	binPath := filepath.Join(tmp, "fake-claude.sh")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	pool := claude.NewSessionPool(tmp, binPath)
	defer pool.Close()

	session, err := pool.StartSession(
		context.Background(),
		"docInfo", 1, 1, tmp,
		"local-1234567890",
		nil,
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// Poll for the script to flush its argv to disk; the goroutine fork+exec
	// can take >300ms on a loaded machine.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(argFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	data, err := os.ReadFile(argFile)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if strings.Contains(string(data), "--resume") {
		t.Errorf("args contain --resume for local-xxx fallback id: %s", data)
	}
}
