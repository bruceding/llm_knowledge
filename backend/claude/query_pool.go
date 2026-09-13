package claude

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"llm-knowledge/db"
	"log"
	"strings"
	"sync"
	"time"
)

// ErrTurnInProgress is returned by Ask when the session already has a turn in
// flight. It is not a dead-session error: the Claude process is healthy, so
// callers must wait or reject instead of replacing the session.
var ErrTurnInProgress = errors.New("another question is already in progress")

// QuerySession wraps an InteractiveSession with turn-based event routing.
// It continuously consumes events from the underlying session and routes
// them to per-question channels and any stream subscribers.
type QuerySession struct {
	session          *InteractiveSession
	convID           uint
	turnCh           chan StreamEvent // active turn's event channel (nil when idle)
	turnDone         chan struct{}    // closed when the active turn ends (nil when idle)
	currentMessageID uint             // user message ID for current turn (for saving assistant reply)
	currentContent   strings.Builder  // accumulated assistant content for current turn
	hasStreamDeltas  bool             // true if stream_event text deltas received this turn (prevents double accumulation)
	mu               sync.Mutex       // protects turnCh, turnDone, currentMessageID, currentContent, streamChs
	streamChs        []chan StreamEvent
	lastAsk          time.Time // last time a question was asked
}

// newQuerySession creates a QuerySession that routes events from the
// underlying InteractiveSession in a turn-based manner.
func newQuerySession(session *InteractiveSession, convID uint) *QuerySession {
	qs := &QuerySession{
		session: session,
		convID:  convID,
	}
	go qs.routeEvents()
	return qs
}

// endTurnLocked closes the active turn's channels. Caller must hold qs.mu.
func (qs *QuerySession) endTurnLocked() {
	if qs.turnCh != nil {
		close(qs.turnCh)
		qs.turnCh = nil
	}
	if qs.turnDone != nil {
		close(qs.turnDone)
		qs.turnDone = nil
	}
}

// routeEvents continuously reads from the underlying session's event channel
// and routes events to the active turn channel and stream subscribers. When no
// turn is active and no stream subscribers exist, events are discarded.
func (qs *QuerySession) routeEvents() {
	for evt := range qs.session.Events() {
		qs.mu.Lock()
		// Accumulate assistant content for auto-save (skip if deltas already accumulated)
		if evt.Type == "assistant" && !qs.hasStreamDeltas {
			qs.currentContent.WriteString(evt.Content)
		}

		// Accumulate stream_event text deltas for auto-save (covers streaming models)
		if evt.Type == "stream_event" && evt.Event != nil {
			delta := ExtractTextDelta(evt.Event)
			if delta != "" {
				qs.hasStreamDeltas = true
				qs.currentContent.WriteString(delta)
			}
		}

		// On result/error, add message save info and prepare auto-save data
		var autoSaveMsg *db.ConversationMessage
		var autoSaveConvID uint
		if evt.Type == "result" || evt.Type == "error" {
			evt.ResultMessageID = qs.currentMessageID
			evt.ResultFullContent = qs.currentContent.String()

			// Prepare auto-save data (DB write happens outside lock)
			if evt.Type == "result" && evt.Subtype != "error_during_execution" &&
				qs.currentMessageID > 0 && qs.currentContent.Len() > 0 {
				autoSaveMsg = &db.ConversationMessage{
					ConversationID: qs.convID,
					Role:           "assistant",
					Content:        qs.currentContent.String(),
					Images:         "[]",
					CreatedAt:      time.Now(),
				}
				autoSaveConvID = qs.convID
			}
		}

		// Route to active turn (non-blocking)
		if qs.turnCh != nil {
			select {
			case qs.turnCh <- evt:
			default:
				if evt.Type == "result" || evt.Type == "error" {
					log.Printf("[query-pool] WARNING: critical %s event dropped for full turnCh on conversation %d", evt.Type, qs.convID)
				}
			}
			if evt.Type == "result" || evt.Type == "error" {
				qs.endTurnLocked()
			}
		}

		// Always reset on result/error to prevent content accumulation across turns
		if evt.Type == "result" || evt.Type == "error" {
			qs.currentMessageID = 0
			qs.currentContent.Reset()
			qs.hasStreamDeltas = false
		}

		// Fan-out to stream subscribers — all event types pass through.
		// StreamProcessor in SSE handlers filters and converts events for frontend.
		for _, ch := range qs.streamChs {
			select {
			case ch <- evt:
			default:
				if evt.Type == "result" || evt.Type == "error" {
					log.Printf("[query-pool] WARNING: critical %s event dropped for slow subscriber on conversation %d", evt.Type, qs.convID)
				}
			}
		}
		qs.mu.Unlock()

		// Auto-save to DB outside the lock to avoid blocking event processing
		if autoSaveMsg != nil {
			if err := db.DB.Create(autoSaveMsg).Error; err != nil {
				log.Printf("[query-pool] Failed to auto-save assistant message: %v", err)
			} else {
				log.Printf("[query-pool] Auto-saved assistant message for conversation %d", autoSaveConvID)
				db.DB.Model(&db.Conversation{}).Where("id = ?", autoSaveConvID).Update("updated_at", time.Now())
			}
		}
	}

	// Session event channel closed — clean up subscriber channels
	qs.mu.Lock()
	for _, ch := range qs.streamChs {
		close(ch)
	}
	qs.streamChs = nil
	// The Claude process is gone. End any in-flight turn: without this the
	// session kept reporting "another question is already in progress" for the
	// rest of its pooled life, and /message kept failing on a corpse.
	if qs.turnCh != nil {
		select {
		case qs.turnCh <- StreamEvent{Type: "error", Error: "claude process exited", ResultMessageID: qs.currentMessageID}:
		default:
		}
	}
	qs.endTurnLocked()
	qs.currentMessageID = 0
	qs.currentContent.Reset()
	qs.hasStreamDeltas = false
	qs.mu.Unlock()
}

// Ask sends a question to the session and returns a channel that receives
// events for this specific question. Only one question can be active at a time.
// messageID is the user message ID for saving the assistant reply later.
// images is optional image data to send with the message.
func (qs *QuerySession) Ask(content string, messageID uint, images []ImageData) (<-chan StreamEvent, error) {
	qs.mu.Lock()
	if qs.turnCh != nil {
		qs.mu.Unlock()
		return nil, ErrTurnInProgress
	}
	ch := make(chan StreamEvent, 100)
	qs.turnCh = ch
	qs.turnDone = make(chan struct{})
	qs.currentMessageID = messageID
	qs.currentContent.Reset()
	qs.lastAsk = time.Now()
	qs.mu.Unlock()

	var sendErr error
	if len(images) > 0 {
		sendErr = qs.session.SendUserMessageWithImages(content, images)
	} else {
		sendErr = qs.session.SendUserMessage(content)
	}
	if sendErr != nil {
		qs.mu.Lock()
		qs.endTurnLocked()
		qs.currentMessageID = 0
		qs.mu.Unlock()
		return nil, sendErr
	}

	return ch, nil
}

// WaitIdle blocks until the in-flight turn ends (result/error, or the Claude
// process exiting) and reports whether that happened within the timeout.
// It lets callers absorb the interrupt→send race — the frontend re-enables the
// input as soon as Stop is clicked, before Claude's result event arrives —
// without discarding a healthy session.
func (qs *QuerySession) WaitIdle(timeout time.Duration) bool {
	qs.mu.Lock()
	done := qs.turnDone
	qs.mu.Unlock()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Close terminates the underlying session.
func (qs *QuerySession) Close() {
	qs.session.Close()
}

// Interrupt sends an interrupt signal to stop the current turn.
// The session remains alive for future messages.
func (qs *QuerySession) Interrupt() error {
	return qs.session.SendInterrupt()
}

// Subscribe returns a channel that receives a copy of all session events.
// The channel has a buffer of 100 events. Call Unsubscribe when done.
func (qs *QuerySession) Subscribe() chan StreamEvent {
	ch := make(chan StreamEvent, 100)
	qs.mu.Lock()
	qs.streamChs = append(qs.streamChs, ch)
	qs.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (qs *QuerySession) Unsubscribe(ch chan StreamEvent) {
	qs.mu.Lock()
	for i, c := range qs.streamChs {
		if c == ch {
			qs.streamChs = append(qs.streamChs[:i], qs.streamChs[i+1:]...)
			break
		}
	}
	qs.mu.Unlock()
}

// Events returns the underlying session's event channel for continuous SSE streaming.
func (qs *QuerySession) Events() <-chan StreamEvent {
	return qs.session.Events()
}

// LastAsk returns the time of the last question.
func (qs *QuerySession) LastAsk() time.Time {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.lastAsk
}

// Status returns the current processing state: "idle", "thinking", or "streaming".
// - idle: no active turn
// - thinking: processing but no text output yet
// - streaming: processing and text has been emitted
func (qs *QuerySession) Status() string {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if qs.turnCh == nil {
		return "idle"
	}
	if qs.currentContent.Len() > 0 {
		return "streaming"
	}
	return "thinking"
}

// StreamingContent returns the accumulated text from the current streaming response.
func (qs *QuerySession) StreamingContent() string {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.currentContent.String()
}

// SessionID returns the underlying session's ID.
func (qs *QuerySession) SessionID() string {
	return qs.session.GetSessionID()
}

// SSEConnect delegates to the underlying session; returns false if limit reached.
func (qs *QuerySession) SSEConnect() bool {
	return qs.session.SSEConnect()
}

// SSEDisconnect delegates to the underlying session.
func (qs *QuerySession) SSEDisconnect() {
	qs.session.SSEDisconnect()
}

// SSEState returns the current SSE connection count and last disconnect time.
func (qs *QuerySession) SSEState() (int, time.Time) {
	return qs.session.SSEState()
}

// IdleSince returns when the underlying session last did something.
func (qs *QuerySession) IdleSince() time.Time {
	return qs.session.IdleSince()
}

// QuerySessionPool manages interactive sessions for the Query system,
// keyed by conversation ID. Sessions are recycled once they have been idle
// (no SSE subscriber, no activity) for the pool's idle timeout.
type QuerySessionPool struct {
	sessions    map[uint]*QuerySession
	mu          sync.RWMutex
	dataDir     string
	claudeBin   string
	idleTimeout time.Duration
	done        chan struct{}
}

// NewQuerySessionPool creates a new pool that recycles sessions idle for
// defaultSessionIdleTimeout.
func NewQuerySessionPool(dataDir, claudeBin string) *QuerySessionPool {
	return newQuerySessionPool(dataDir, claudeBin, defaultSessionIdleTimeout)
}

// newQuerySessionPool is NewQuerySessionPool with an explicit idle timeout, so
// tests can observe a sweep without waiting out the production default.
func newQuerySessionPool(dataDir, claudeBin string, idleTimeout time.Duration) *QuerySessionPool {
	p := &QuerySessionPool{
		sessions:    make(map[uint]*QuerySession),
		dataDir:     dataDir,
		claudeBin:   claudeBin,
		idleTimeout: idleTimeout,
		done:        make(chan struct{}),
	}
	go p.cleanupLoop()
	return p
}

// Close terminates all sessions and stops the cleanup loop.
func (p *QuerySessionPool) Close() {
	close(p.done)
	p.mu.Lock()
	var toClose []*QuerySession
	for convID, qs := range p.sessions {
		toClose = append(toClose, qs)
		delete(p.sessions, convID)
	}
	p.mu.Unlock()
	// Kill outside the lock: a dying process wakes routeEvents, which may still
	// need qs.mu.
	for _, qs := range toClose {
		qs.Close()
	}
	log.Printf("[query-pool] QuerySessionPool closed, all sessions terminated")
}

// cleanupLoop recycles sessions idle past the pool's idle timeout: no SSE
// subscriber and no activity since the deadline.
// The previous rule required a recorded SSE disconnect, so any session created
// without one (POST /query/message before the stream connects, or a stream that
// died before SSEConnect) was never recycled — its Claude process lived until
// server shutdown and e2e rounds piled up orphans.
func (p *QuerySessionPool) cleanupLoop() {
	tick := cleanupTick(p.idleTimeout)
	for {
		select {
		case <-p.done:
			return
		case <-time.After(tick):
		}
		var toClose []*QuerySession
		p.mu.Lock()
		for convID, qs := range p.sessions {
			if sseCount, _ := qs.SSEState(); sseCount > 0 {
				continue
			}
			idleFor := time.Since(qs.IdleSince())
			if idleFor < p.idleTimeout {
				continue
			}
			log.Printf("[query-pool] Closing session for conversation %d after %s idle", convID, idleFor.Round(time.Second))
			toClose = append(toClose, qs)
			delete(p.sessions, convID)
		}
		p.mu.Unlock()
		for _, qs := range toClose {
			qs.Close()
		}
	}
}

// Get returns a live session from the pool without creating a new one.
// Returns nil if the conversation has no session, or its Claude process already
// exited (the stale entry is evicted here).
func (p *QuerySessionPool) Get(convID uint) *QuerySession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.liveLocked(convID)
}

// liveLocked returns the pooled session for convID only if its Claude process is
// still running. Caller must hold p.mu for writing.
func (p *QuerySessionPool) liveLocked(convID uint) *QuerySession {
	qs := p.sessions[convID]
	if qs == nil {
		return nil
	}
	if !qs.session.Exited() {
		return qs
	}
	delete(p.sessions, convID)
	log.Printf("[query-pool] Evicted exited session for conversation %d", convID)
	return nil
}

// SessionSource indicates how a session was obtained by the pool.
type SessionSource string

const (
	SourceExisting SessionSource = "existing" // reused from pool
	SourceResumed  SessionSource = "resumed"  // resumed via --resume
	SourceCreated  SessionSource = "created"  // created fresh
)

// GetOrResume retrieves an existing session, resumes a previous one, or creates
// a new one. This is an atomic operation under the pool's write lock, preventing
// concurrent resume attempts for the same conversation.
// The onSessionID callback is registered to update the database when the real
// session_id arrives asynchronously.
// userDir is the user's directory for Claude session isolation.
func (p *QuerySessionPool) GetOrResume(ctx context.Context, convID uint, prevSessionID string, systemPrompt string, userDir string, onRealSessionID func(convID uint, newSID string)) (*QuerySession, SessionSource, error) {
	if qs := p.Get(convID); qs != nil {
		return qs, SourceExisting, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if existing := p.liveLocked(convID); existing != nil {
		return existing, SourceExisting, nil
	}

	var qs *QuerySession
	var session *InteractiveSession
	var err error
	source := SourceCreated

	// Try resume first if a previous session_id is available and looks real
	if prevSessionID != "" && !strings.HasPrefix(prevSessionID, "local-") {
		session, err = StartResumedSession(ctx, p.claudeBin, userDir, prevSessionID, systemPrompt)
		if err != nil {
			log.Printf("[query-pool] Resume failed for conversation %d (%v), creating fresh session", convID, err)
			session = nil // fall through to create new
		} else {
			source = SourceResumed
		}
	}

	if session == nil {
		session, err = StartSession(ctx, p.claudeBin, userDir, systemPrompt)
		if err != nil {
			return nil, "", fmt.Errorf("failed to start session: %w", err)
		}
	}

	// Register callback to update the database when real session_id arrives.
	// Sessions always start with a local-xxx fallback ID; the real one shows up
	// with system.init after the first user message.
	session.onSessionID = func(oldID, newID string) {
		log.Printf("[query-pool] session_id updated for conversation %d: %s -> %s", convID, oldID, newID)
		if onRealSessionID != nil {
			onRealSessionID(convID, newID)
		}
	}

	qs = newQuerySession(session, convID)
	p.sessions[convID] = qs
	log.Printf("[query-pool] Created session %s for conversation %d (source=%s)", session.GetSessionID(), convID, source)
	return qs, source, nil
}

// GetOrCreate retrieves an existing session or creates a new one.
// Prefer GetOrResume which also tries --resume when a previous session exists.
// userDir is the user's directory for Claude session isolation.
func (p *QuerySessionPool) GetOrCreate(ctx context.Context, convID uint, systemPrompt string, userDir string) (*QuerySession, error) {
	if qs := p.Get(convID); qs != nil {
		return qs, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if existing := p.liveLocked(convID); existing != nil {
		return existing, nil
	}

	var qs *QuerySession
	session, err := StartSession(ctx, p.claudeBin, userDir, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to start session: %w", err)
	}

	// Register callback to update the database when real session_id arrives
	session.onSessionID = func(oldID, newID string) {
		log.Printf("[query-pool] session_id updated for conversation %d: %s -> %s", convID, oldID, newID)
		// Use only convID in WHERE: local-xxx was never written to DB,
		// so WHERE session_id = oldID would match zero rows.
		db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newID)
	}

	qs = newQuerySession(session, convID)
	p.sessions[convID] = qs
	log.Printf("[query-pool] Created new session %s for conversation %d", session.GetSessionID(), convID)
	return qs, nil
}

// ResumeSession creates a new session by resuming a previous one via --resume.
// userDir is the user's directory for Claude session isolation.
func (p *QuerySessionPool) ResumeSession(ctx context.Context, convID uint, prevSessionID string, systemPrompt string, userDir string) (*QuerySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	session, err := StartResumedSession(ctx, p.claudeBin, userDir, prevSessionID, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	// Register callback to update the database when real session_id arrives
	session.onSessionID = func(oldID, newID string) {
		log.Printf("[query-pool] session_id updated for conversation %d: %s -> %s", convID, oldID, newID)
		// Use only convID in WHERE: local-xxx was never written to DB,
		// so WHERE session_id = oldID would match zero rows.
		db.DB.Model(&db.Conversation{}).Where("id = ?", convID).Update("session_id", newID)
	}

	qs := newQuerySession(session, convID)
	p.sessions[convID] = qs
	log.Printf("[query-pool] Resumed session %s (from %s) for conversation %d", session.GetSessionID(), prevSessionID, convID)
	return qs, nil
}

// Remove removes and closes a session.
func (p *QuerySessionPool) Remove(convID uint) {
	p.mu.Lock()
	qs, exists := p.sessions[convID]
	if exists {
		delete(p.sessions, convID)
	}
	p.mu.Unlock()

	if exists {
		qs.Close()
	}
}

// StartSession creates a new InteractiveSession with system prompt.
// Extracted from SessionPool.StartSession for reuse.
// No init message is sent — the first real user message triggers system.init,
// so session creation returns immediately without waiting for Claude CLI to boot.
// userDir is the user's directory (cmd.Dir) for Claude session isolation and security restriction.
func StartSession(ctx context.Context, claudeBin string, userDir string, systemPrompt string) (*InteractiveSession, error) {
	if userDir == "" {
		return nil, fmt.Errorf("userDir is required for session isolation")
	}
	secureArgs, err := BuildSecureArgs([]string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		return nil, fmt.Errorf("build secure args: %w", err)
	}
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}
	args = append(args, secureArgs...)

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Build environment with ALLOWED_DIR
	env := BuildSecureEnv(userDir)

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmdWithEnv(ctx, claudeBin, args, userDir, env)

	stdinPipe, stdoutPipe, stderrPipe, err := createPipes(cmd)
	if err != nil {
		cancel()
		return nil, err
	}

	session := &InteractiveSession{
		cmd:           cmd,
		stdin:         stdinPipe,
		stdoutScanner: newScanner(stdoutPipe),
		eventCh:       make(chan StreamEvent, 100),
		ctx:           ctx,
		cancel:        cancel,
		initDone:      make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Start goroutine to log stderr output (helps debug Claude CLI crashes)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			log.Printf("[query-session] Claude stderr: %s", scanner.Text())
		}
	}()

	// Start reading events — session_id will be auto-captured from system.init
	// when the first real user message is sent via Ask(). No init message needed.
	go session.readEvents()

	// Interactive mode (no --print) emits system.init only after the first user
	// message, so waiting for it here can never succeed. It burned 5s per session
	// while GetOrResume held the pool's write lock — stalling every other
	// Get/Status/Message call behind it — and logged "timed out waiting for init
	// event" on every single creation. Use a fallback ID; readEvents swaps in the
	// real one via onSessionID once the first message is sent.
	session.mu.Lock()
	if session.SessionID == "" {
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	session.lastActivity = time.Now() // idle sweeps count from creation, not from first use
	session.mu.Unlock()

	return session, nil
}

// StartResumedSession creates a new InteractiveSession that resumes a previous conversation.
// No init message is sent — the first real user message triggers system.init.
// userDir is the user's directory (cmd.Dir) for Claude session isolation and security restriction.
func StartResumedSession(ctx context.Context, claudeBin string, userDir string, prevSessionID string, systemPrompt string) (*InteractiveSession, error) {
	secureArgs, err := BuildSecureArgs([]string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		return nil, fmt.Errorf("build secure args: %w", err)
	}
	args := []string{
		"--resume", prevSessionID,
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}
	args = append(args, secureArgs...)

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	// Build environment with ALLOWED_DIR
	env := BuildSecureEnv(userDir)

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmdWithEnv(ctx, claudeBin, args, userDir, env)

	stdinPipe, stdoutPipe, stderrPipe, err := createPipes(cmd)
	if err != nil {
		cancel()
		return nil, err
	}

	session := &InteractiveSession{
		cmd:           cmd,
		stdin:         stdinPipe,
		stdoutScanner: newScanner(stdoutPipe),
		eventCh:       make(chan StreamEvent, 100),
		ctx:           ctx,
		cancel:        cancel,
		initDone:      make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Start goroutine to log stderr output (helps debug Claude CLI crashes)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			log.Printf("[query-session] Claude stderr: %s", scanner.Text())
		}
	}()

	// Start reading events — session_id will be auto-captured from system.init
	go session.readEvents()

	// Same as StartSession: system.init only arrives with the first user message,
	// so don't block on it. The local-xxx fallback ID is replaced later through
	// the onSessionID callback.
	session.mu.Lock()
	if session.SessionID == "" {
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	session.lastActivity = time.Now()
	session.mu.Unlock()

	return session, nil
}
