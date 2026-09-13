package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests drive the pools with a fake Claude CLI (a shell script speaking
// just enough stream-json) so session lifecycle can be asserted deterministically
// and in milliseconds instead of through the real binary.

// idleClaudeScript answers every stdin line and stays alive until stdin closes,
// like the real CLI in interactive mode: system.init only arrives with the first
// user message, never at startup.
const idleClaudeScript = `#!/bin/sh
n=0
while IFS= read -r line; do
  n=$((n+1))
  if [ "$n" = 1 ]; then
    printf '{"type":"system","subtype":"init","session_id":"fake-init"}\n'
  fi
  printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"pong"}]}}\n'
  printf '{"type":"result","subtype":"success","result":"pong","is_error":false}\n'
done
`

// exitAfterOneClaudeScript answers one turn and then exits, simulating a Claude
// process that disappears mid-conversation.
const exitAfterOneClaudeScript = `#!/bin/sh
IFS= read -r line
printf '{"type":"system","subtype":"init","session_id":"fake-exit"}\n'
printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"bye"}]}}\n'
printf '{"type":"result","subtype":"success","result":"bye","is_error":false}\n'
exit 0
`

// hangClaudeScript reports init and then never answers: a turn that stays open
// until something kills the process.
const hangClaudeScript = `#!/bin/sh
IFS= read -r line
printf '{"type":"system","subtype":"init","session_id":"fake-hang"}\n'
while IFS= read -r ignored; do :; done
`

func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake claude: %v", err)
	}
	return path
}

// waitFor polls cond until it holds, failing the test when it never does.
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, desc)
}

// drainTurn reads a turn channel until it is closed and returns what it saw.
func drainTurn(t *testing.T, ch <-chan StreamEvent, timeout time.Duration) []StreamEvent {
	t.Helper()
	var events []StreamEvent
	deadline := time.After(timeout)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-deadline:
			t.Fatalf("timed out after %s draining turn (%d events received)", timeout, len(events))
		}
	}
}

// TestStartSession_DoesNotBlockOnInit covers the "timed out waiting for init
// event" symptom. Interactive mode cannot emit system.init before the first user
// message, yet session creation waited for it anyway: 5s lost per session, spent
// holding the pool's write lock so every other Get/Status/Message call queued up
// behind it (observed as 5s latencies on unrelated endpoints during e2e runs).
func TestStartSession_DoesNotBlockOnInit(t *testing.T) {
	setupTestDB(t)
	pool := NewQuerySessionPool(t.TempDir(), writeFakeClaude(t, idleClaudeScript))
	defer pool.Close()

	start := time.Now()
	qs, err := pool.GetOrCreate(context.Background(), 1, "prompt", t.TempDir())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("session creation took %s, want <2s: it must not block on system.init", elapsed)
	}
	if sid := qs.SessionID(); !strings.HasPrefix(sid, "local-") {
		t.Errorf("expected a local-* fallback session id, got %q", sid)
	}
}

// TestQuerySessionPool_RecyclesIdleSessionWithoutSSE covers the leak behind the
// orphaned processes: a session that never got an SSE subscriber has a zero
// lastDisconnect, and the old sweep required a non-zero one, so its Claude
// process survived until server shutdown.
func TestQuerySessionPool_RecyclesIdleSessionWithoutSSE(t *testing.T) {
	setupTestDB(t)
	pool := newQuerySessionPool(t.TempDir(), writeFakeClaude(t, idleClaudeScript), 150*time.Millisecond)
	defer pool.Close()

	qs, err := pool.GetOrCreate(context.Background(), 7, "prompt", t.TempDir())
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if pool.Get(7) == nil {
		t.Fatal("session should be pooled right after creation")
	}

	waitFor(t, 5*time.Second, "idle session to be recycled", func() bool { return pool.Get(7) == nil })
	waitFor(t, 5*time.Second, "recycled session's Claude process to exit", func() bool { return qs.session.Exited() })
}

// TestSessionPool_RecyclesIdleSessionWithoutSSE is the doc-chat pool equivalent:
// same zero-lastDisconnect hole, same orphaned Claude process.
func TestSessionPool_RecyclesIdleSessionWithoutSSE(t *testing.T) {
	pool := newSessionPool(t.TempDir(), writeFakeClaude(t, idleClaudeScript), 150*time.Millisecond)
	defer pool.Close()

	session, err := pool.StartSession(context.Background(), "doc info", 1, 2, t.TempDir(), "", nil, nil)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	sid := session.GetSessionID()

	waitFor(t, 5*time.Second, "idle doc-chat session to be recycled", func() bool {
		return pool.GetSession(sid) == nil
	})
	waitFor(t, 5*time.Second, "recycled doc-chat process to exit", func() bool { return session.Exited() })
}

// TestQuerySessionPool_EvictsExitedSession asserts a dead session is never handed
// back. Reusing a corpse made /message write to a dead stdin and /stream subscribe
// to a channel that would never fire again.
func TestQuerySessionPool_EvictsExitedSession(t *testing.T) {
	setupTestDB(t)
	// Idle timeout far longer than the test, so eviction can only come from the
	// process having exited.
	pool := newQuerySessionPool(t.TempDir(), writeFakeClaude(t, exitAfterOneClaudeScript), time.Minute)
	defer pool.Close()

	userDir := t.TempDir()
	qs, err := pool.GetOrCreate(context.Background(), 11, "prompt", userDir)
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	ch, err := qs.Ask("hello", 1, nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	events := drainTurn(t, ch, 10*time.Second)

	var sawResult bool
	for _, evt := range events {
		if evt.Type == "result" {
			sawResult = true
		}
	}
	if !sawResult {
		t.Errorf("expected a result event, got %d events without one", len(events))
	}
	if !qs.WaitIdle(time.Second) {
		t.Error("session should be idle once its turn completed")
	}

	waitFor(t, 5*time.Second, "Claude process to exit", func() bool { return qs.session.Exited() })
	waitFor(t, 5*time.Second, "exited session to be evicted from the pool", func() bool { return pool.Get(11) == nil })

	fresh, err := pool.GetOrCreate(context.Background(), 11, "prompt", userDir)
	if err != nil {
		t.Fatalf("GetOrCreate after eviction failed: %v", err)
	}
	if fresh == qs {
		t.Error("pool handed back the exited session instead of creating a fresh one")
	}
}

// TestQuerySession_TurnEndsWhenProcessDies covers the stuck
// "another question is already in progress". The turn used to stay open forever
// when Claude vanished mid-turn, so every later message on that pooled
// conversation was rejected until the whole session was recycled.
func TestQuerySession_TurnEndsWhenProcessDies(t *testing.T) {
	setupTestDB(t)
	pool := newQuerySessionPool(t.TempDir(), writeFakeClaude(t, hangClaudeScript), time.Minute)
	defer pool.Close()

	qs, err := pool.GetOrCreate(context.Background(), 13, "prompt", t.TempDir())
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if _, err := qs.Ask("hello", 1, nil); err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	if _, err := qs.Ask("hello again", 2, nil); !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("Ask during an in-flight turn returned %v, want ErrTurnInProgress", err)
	}
	if qs.WaitIdle(200 * time.Millisecond) {
		t.Fatal("WaitIdle reported idle while a turn was still in flight")
	}

	qs.Close() // what the idle sweep does to a session Claude abandoned
	if !qs.WaitIdle(5 * time.Second) {
		t.Fatal("turn never ended after the Claude process died; the session would reject every later message")
	}
	if _, err := qs.Ask("after death", 3, nil); errors.Is(err, ErrTurnInProgress) {
		t.Fatal("Ask still reports a turn in progress after the process died")
	}
}
