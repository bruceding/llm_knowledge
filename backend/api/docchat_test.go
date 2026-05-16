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
		uint(1),
		docID,
		tmp,
		"",
		func(oldID, newID string) {
			db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("chat_session_id", newID)
			callbackFired <- newID
		},
		nil,
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
		"docInfo", uint(1), uint(1), tmp,
		"", // no prevSessionID
		nil, nil,
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
		"docInfo", uint(1), uint(1), tmp,
		"prev-real-id-xyz",
		nil, nil,
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

// TestDocChat_ResumeFailureClearsCachedID covers the regression flagged in
// review: when --resume is attempted but Claude exits without ever emitting
// system.init (stale prevSessionID, gone session file, etc.), the
// onResumeFailed callback must fire so the caller can drop the broken id and
// the next reconnect starts fresh instead of looping on the same failure.
func TestDocChat_ResumeFailureClearsCachedID(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	// Fake Claude that exits immediately with no stdout — emulates a stale
	// resume id that the real CLI would reject.
	binPath := filepath.Join(tmp, "fake-claude.sh")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	doc := db.Document{Title: "Test", UserID: 1, ChatSessionID: "stale-prev-id"}
	if err := db.DB.Create(&doc).Error; err != nil {
		t.Fatalf("create doc: %v", err)
	}

	pool := claude.NewSessionPool(tmp, binPath)
	defer pool.Close()

	failed := make(chan struct{}, 1)
	docID := doc.ID
	session, err := pool.StartSession(
		context.Background(),
		"docInfo", uint(1), docID, tmp,
		"stale-prev-id",
		nil,
		func() {
			db.DB.Model(&db.Document{}).Where("id = ?", docID).Update("chat_session_id", "")
			failed <- struct{}{}
		},
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	select {
	case <-failed:
	case <-time.After(3 * time.Second):
		t.Fatal("onResumeFailed never fired after Claude exit without init")
	}

	var refreshed db.Document
	if err := db.DB.First(&refreshed, docID).Error; err != nil {
		t.Fatalf("reload doc: %v", err)
	}
	if refreshed.ChatSessionID != "" {
		t.Errorf("Document.ChatSessionID = %q, want empty after resume failure", refreshed.ChatSessionID)
	}
}

// TestDocChat_ExplicitCloseBeforeInitDoesNotInvokeFailureCallback covers a
// regression flagged in PR review: a resumed session that is Close()'d before
// the user sends the first message (so system.init never fires) must NOT
// trigger onResumeFailed — the prevSessionID is still valid, the user just
// hasn't typed anything yet. Without this guard, every 120s idle cleanup or
// "Clear Chat" click would silently wipe a working chat_session_id.
func TestDocChat_ExplicitCloseBeforeInitDoesNotInvokeFailureCallback(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	// Fake claude that idles indefinitely with no output, mimicking a real
	// resumed Claude waiting for the first user message.
	binPath := filepath.Join(tmp, "fake-claude.sh")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	pool := claude.NewSessionPool(tmp, binPath)
	defer pool.Close()

	failed := make(chan struct{}, 1)
	session, err := pool.StartSession(
		context.Background(),
		"docInfo", uint(1), uint(1), tmp,
		"prev-real-id",
		nil,
		func() { failed <- struct{}{} },
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Simulate idle-cleanup or handleClear: explicit Close before any
	// stdout (and therefore before system.init).
	session.Close()

	select {
	case <-failed:
		t.Fatal("onResumeFailed fired on explicit Close — this would wipe a valid chat_session_id")
	case <-time.After(500 * time.Millisecond):
		// expected: callback did not fire
	}
}

// TestDocChat_ResumeSuccessDoesNotInvokeFailureCallback ensures the resume
// failure path doesn't false-positive when --resume actually succeeds.
func TestDocChat_ResumeSuccessDoesNotInvokeFailureCallback(t *testing.T) {
	setupTestDB(t)
	defer cleanupTestDB(t)

	tmp := t.TempDir()
	fakeBin := writeFakeClaude(t, tmp, "resumed-ok")

	pool := claude.NewSessionPool(tmp, fakeBin)
	defer pool.Close()

	failed := make(chan struct{}, 1)
	session, err := pool.StartSession(
		context.Background(),
		"docInfo", uint(1), uint(1), tmp,
		"prev-real-id",
		nil,
		func() { failed <- struct{}{} },
	)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// Wait for init event (fake claude emits it, then idles), then close
	// the session to force readEvents to wind down. Failure callback must
	// NOT fire because init was observed.
	time.Sleep(500 * time.Millisecond)
	session.Close()

	select {
	case <-failed:
		t.Fatal("onResumeFailed fired even though system.init was emitted")
	case <-time.After(500 * time.Millisecond):
		// expected: callback did not fire
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
		"docInfo", uint(1), uint(1), tmp,
		"local-1234567890",
		nil, nil,
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
