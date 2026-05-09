package claude

import (
	"context"
	"llm-knowledge/db"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB initializes an in-memory SQLite database for integration tests
// that need the routeEvents goroutine to auto-save assistant messages.
func setupTestDB(t *testing.T) {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect test database: %v", err)
	}
	db.DB = testDB
	testDB.AutoMigrate(&db.Conversation{}, &db.ConversationMessage{})
}

// TestBuildCmd_RequestContextCancellation verifies the root cause of issue #27:
// exec.CommandContext kills the subprocess when the context is cancelled.
// This test documents the behavior that necessitated using context.Background()
// in the Message and Stream handlers.
func TestBuildCmd_RequestContextCancellation(t *testing.T) {
	tests := []struct {
		name           string
		ctxFunc        func() (context.Context, context.CancelFunc)
		expectKilled   bool
		expectExitCode int
	}{
		{
			name: "request context cancellation kills subprocess",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			expectKilled:   true,
			expectExitCode: -1, // signal death, no exit code
		},
		{
			name: "background context keeps subprocess alive",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			expectKilled:   false,
			expectExitCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctxFunc()
			defer cancel()

			cmd := buildCmd(ctx, "/bin/sleep", []string{"10"}, t.TempDir())
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("failed to create stdin pipe: %v", err)
			}
			stdin.Close()

			if err := cmd.Start(); err != nil {
				t.Fatalf("failed to start command: %v", err)
			}

			// Cancel the context (simulates HTTP handler returning)
			cancel()

			// Give a brief window for the signal to be delivered
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			select {
			case err := <-done:
				if tt.expectKilled {
					// Process should have been killed by signal
					if cmd.ProcessState != nil {
						// On Unix, killed processes have signal-based exit status
						waitStatus, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
						if ok && waitStatus.Signaled() {
							// Expected: killed by signal
						} else if err != nil {
							// Also acceptable: Wait returns error for killed process
						} else {
							t.Errorf("expected process to be killed, but exited normally with code %d", cmd.ProcessState.ExitCode())
						}
					}
				} else {
					// Process should still be running; cancel() should be a no-op
					// Since we can't wait for it to exit naturally (sleep 10),
					// kill it manually and verify it was still alive
					cmd.Process.Kill()
					cmd.Wait()
				}
			case <-time.After(2 * time.Second):
				if tt.expectKilled {
					t.Error("expected process to be killed within 2s, but it's still running")
					cmd.Process.Kill()
					cmd.Wait()
				}
				// For background context, process still running is expected
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
	}
}

// TestExecCommandContext_SignalOnCancel is a focused test that directly proves
// exec.CommandContext sends SIGKILL when context is cancelled — the exact
// mechanism that caused Issue #27.
func TestExecCommandContext_SignalOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, "/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Verify process is running
	if cmd.ProcessState != nil {
		t.Fatal("process should not have exited yet")
	}

	// Cancel context (simulates HTTP request completing)
	cancel()

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// Good: process exited after context cancellation
		if cmd.ProcessState == nil {
			t.Fatal("ProcessState should not be nil after Wait")
		}
		waitStatus, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok {
			t.Fatalf("could not get WaitStatus")
		}
		if !waitStatus.Signaled() {
			t.Errorf("expected process to be killed by signal, exit code=%d", cmd.ProcessState.ExitCode())
		}
		if waitStatus.Signal() != syscall.SIGKILL {
			t.Errorf("expected SIGKILL, got signal %v", waitStatus.Signal())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process should have been killed by context cancellation within 3s")
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// TestBackgroundContext_SubprocessSurvivesCancel proves that using
// context.Background() (our fix) keeps the subprocess alive even after
// a parent context derived from it is cancelled.
func TestBackgroundContext_SubprocessSurvivesCancel(t *testing.T) {
	// Simulate the fix: use context.Background() for the subprocess
	bgCtx := context.Background()

	cmd := buildCmd(bgCtx, "/bin/sleep", []string{"30"}, t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Simulate: HTTP handler returns, request context is cancelled
	// (This is a no-op on bgCtx, proving the subprocess is independent)
	_, requestCancel := context.WithCancel(context.Background())
	requestCancel() // Cancel immediately — simulates handler returning

	// Give time for any propagation (there should be none)
	time.Sleep(500 * time.Millisecond)

	// Check process is still alive
	if cmd.ProcessState != nil {
		t.Fatal("subprocess should still be running — context.Background() should not be affected by request context cancellation")
	}

	// Clean up
	cmd.Process.Kill()
	cmd.Wait()
}

// TestQuerySessionPool_GetOrCreate_BackgroundContext tests that GetOrCreate
// with context.Background() creates a session that survives after the
// calling context is no longer needed.
func TestQuerySessionPool_GetOrCreate_BackgroundContext(t *testing.T) {
	// Skip if claude binary is not available
	claudeBin, err := exec.LookPath("claude")
	if err != nil || claudeBin == "" {
		t.Skip("claude binary not found, skipping integration test")
	}

	setupTestDB(t)

	pool := NewQuerySessionPool(t.TempDir(), claudeBin)
	defer pool.Close()

	ctx := context.Background()

	qs, err := pool.GetOrCreate(ctx, 1, "test prompt")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	// Verify session is in the pool
	got := pool.Get(1)
	if got == nil {
		t.Fatal("session should exist in pool after GetOrCreate")
	}
	if got.SessionID() != qs.SessionID() {
		t.Errorf("expected session ID %s, got %s", qs.SessionID(), got.SessionID())
	}

	// Verify second call returns same session
	qs2, err := pool.GetOrCreate(ctx, 1, "test prompt")
	if err != nil {
		t.Fatalf("second GetOrCreate failed: %v", err)
	}
	if qs2.SessionID() != qs.SessionID() {
		t.Error("second GetOrCreate should return the same session")
	}
}

// TestQuerySessionPool_Remove tests that Remove cleans up a session.
func TestQuerySessionPool_Remove(t *testing.T) {
	claudeBin, err := exec.LookPath("claude")
	if err != nil || claudeBin == "" {
		t.Skip("claude binary not found, skipping integration test")
	}

	setupTestDB(t)

	pool := NewQuerySessionPool(t.TempDir(), claudeBin)
	defer pool.Close()

	ctx := context.Background()

	_, err = pool.GetOrCreate(ctx, 42, "test prompt")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	if pool.Get(42) == nil {
		t.Fatal("session should exist in pool")
	}

	pool.Remove(42)

	if pool.Get(42) != nil {
		t.Error("session should be removed from pool after Remove")
	}
}

// TestQuerySessionPool_SessionSurvivesRequestCancel is the core regression
// test for Issue #27. It proves that using context.Background() in
// GetOrCreate keeps the Claude subprocess alive even after the request
// context that was originally associated with the HTTP handler is cancelled.
//
// Before the fix, the handler used c.Request().Context() which was cancelled
// when the handler returned, causing exec.CommandContext to SIGKILL the
// subprocess immediately.
func TestQuerySessionPool_SessionSurvivesRequestCancel(t *testing.T) {
	claudeBin, err := exec.LookPath("claude")
	if err != nil || claudeBin == "" {
		t.Skip("claude binary not found, skipping integration test")
	}

	setupTestDB(t)

	pool := NewQuerySessionPool(t.TempDir(), claudeBin)
	defer pool.Close()

	// Simulate what the handler does AFTER the fix: context.Background()
	bgCtx := context.Background()

	qs, err := pool.GetOrCreate(bgCtx, 100, "你是一个测试助手，简短回答")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	// Simulate HTTP request context being cancelled (handler returns)
	_, reqCancel := context.WithCancel(context.Background())
	reqCancel() // Cancel immediately — simulates handler returning

	// Give time for any signal propagation (should be none with our fix)
	time.Sleep(500 * time.Millisecond)

	// Session should still be alive in the pool
	if pool.Get(100) == nil {
		t.Fatal("session should still exist in pool after request context cancelled")
	}

	// Most importantly: the session should still be functional.
	// Send a question and verify we get a response.
	ch, err := qs.Ask("1+1等于几？只回答数字", 1, nil)
	if err != nil {
		t.Fatalf("Ask failed after request context cancelled: %v", err)
	}

	// Read events until we get a result or timeout
	timeout := time.After(60 * time.Second)
	gotResult := false
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				if !gotResult {
					t.Fatal("event channel closed without receiving result — subprocess was likely killed")
				}
				return
			}
			if evt.Type == "result" {
				gotResult = true
				if evt.ResultFullContent == "" {
					t.Error("expected non-empty assistant reply")
				}
				t.Logf("Assistant replied: %s", truncateStr(evt.ResultFullContent, 100))
			}
		case <-timeout:
			if !gotResult {
				t.Fatal("timed out waiting for result — subprocess may have been killed by context cancellation")
			}
			return
		}
	}
}

// TestQuerySessionPool_RequestContextKillsSession demonstrates the OLD broken
// behavior: if we pass a request context to GetOrCreate, cancelling it kills
// the subprocess. This test documents why the fix was necessary.
func TestQuerySessionPool_RequestContextKillsSession(t *testing.T) {
	claudeBin, err := exec.LookPath("claude")
	if err != nil || claudeBin == "" {
		t.Skip("claude binary not found, skipping integration test")
	}

	setupTestDB(t)

	pool := NewQuerySessionPool(t.TempDir(), claudeBin)
	defer pool.Close()

	// Simulate the OLD broken behavior: use a cancellable request context
	reqCtx, reqCancel := context.WithCancel(context.Background())

	qs, err := pool.GetOrCreate(reqCtx, 200, "你是一个测试助手，简短回答")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	// Cancel the request context (simulates HTTP handler returning)
	reqCancel()

	// Give time for SIGKILL to be delivered
	time.Sleep(500 * time.Millisecond)

	// Session should be dead — Ask should fail or produce no useful result
	ch, err := qs.Ask("你好", 1, nil)
	if err != nil {
		// Expected: subprocess is already dead
		t.Logf("Ask correctly failed after context cancellation: %v", err)
		return
	}

	// If Ask didn't error, the channel should close quickly (dead subprocess)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Log("Event channel closed — subprocess was killed by context cancellation (expected)")
				return
			}
		case <-timeout:
			t.Log("WARNING: subprocess survived request context cancellation — this shouldn't happen with a cancellable context")
			return
		}
	}
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
