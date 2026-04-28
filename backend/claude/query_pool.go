package claude

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// QuerySession wraps an InteractiveSession with turn-based event routing.
// It continuously consumes events from the underlying session and routes
// them to per-question channels, allowing per-request SSE streaming.
type QuerySession struct {
	session *InteractiveSession
	convID  uint
	turnCh  chan StreamEvent // active turn's event channel (nil when idle)
	mu      sync.Mutex       // protects turnCh
	lastAsk time.Time        // last time a question was asked
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

// routeEvents continuously reads from the underlying session's event channel
// and routes events to the active turn channel. When no turn is active,
// events are discarded.
func (qs *QuerySession) routeEvents() {
	for evt := range qs.session.Events() {
		qs.mu.Lock()
		if qs.turnCh != nil {
			qs.turnCh <- evt
			if evt.Type == "result" || evt.Type == "error" {
				close(qs.turnCh)
				qs.turnCh = nil
			}
		}
		qs.mu.Unlock()
	}
}

// Ask sends a question to the session and returns a channel that receives
// events for this specific question. Only one question can be active at a time.
func (qs *QuerySession) Ask(content string) (<-chan StreamEvent, error) {
	qs.mu.Lock()
	if qs.turnCh != nil {
		qs.mu.Unlock()
		return nil, fmt.Errorf("another question is already in progress")
	}
	ch := make(chan StreamEvent, 100)
	qs.turnCh = ch
	qs.lastAsk = time.Now()
	qs.mu.Unlock()

	if err := qs.session.SendUserMessage(content); err != nil {
		qs.mu.Lock()
		qs.turnCh = nil
		qs.mu.Unlock()
		return nil, err
	}

	return ch, nil
}

// Close terminates the underlying session.
func (qs *QuerySession) Close() {
	qs.session.Close()
}

// LastAsk returns the time of the last question.
func (qs *QuerySession) LastAsk() time.Time {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return qs.lastAsk
}

// SessionID returns the underlying session's ID.
func (qs *QuerySession) SessionID() string {
	return qs.session.SessionID
}

// SSEConnect delegates to the underlying session.
func (qs *QuerySession) SSEConnect() {
	qs.session.SSEConnect()
}

// SSEDisconnect delegates to the underlying session.
func (qs *QuerySession) SSEDisconnect() {
	qs.session.SSEDisconnect()
}

// QuerySessionPool manages interactive sessions for the Query system,
// keyed by conversation ID. Sessions expire after 90 seconds of inactivity.
type QuerySessionPool struct {
	sessions  map[uint]*QuerySession
	mu        sync.RWMutex
	dataDir   string
	claudeBin string
}

// NewQuerySessionPool creates a new pool with 90s cleanup timeout.
func NewQuerySessionPool(dataDir, claudeBin string) *QuerySessionPool {
	p := &QuerySessionPool{
		sessions:  make(map[uint]*QuerySession),
		dataDir:   dataDir,
		claudeBin: claudeBin,
	}
	go p.cleanupLoop()
	return p
}

// cleanupLoop closes sessions after 90 seconds of inactivity.
func (p *QuerySessionPool) cleanupLoop() {
	for {
		time.Sleep(10 * time.Second)
		p.mu.Lock()
		for convID, qs := range p.sessions {
			lastAsk := qs.LastAsk()
			if !lastAsk.IsZero() && lastAsk.Add(90*time.Second).Before(time.Now()) {
				log.Printf("[query-pool] Closing session for conversation %d after 90s inactivity", convID)
				qs.Close()
				delete(p.sessions, convID)
			}
		}
		p.mu.Unlock()
	}
}

// GetOrCreate retrieves an existing session or creates a new one.
func (p *QuerySessionPool) GetOrCreate(ctx context.Context, convID uint, systemPrompt string) (*QuerySession, error) {
	p.mu.RLock()
	qs, exists := p.sessions[convID]
	p.mu.RUnlock()

	if exists {
		return qs, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if qs, exists = p.sessions[convID]; exists {
		return qs, nil
	}

	session, err := StartSession(ctx, p.claudeBin, p.dataDir, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to start session: %w", err)
	}

	qs = newQuerySession(session, convID)
	p.sessions[convID] = qs
	log.Printf("[query-pool] Created new session %s for conversation %d", session.SessionID, convID)
	return qs, nil
}

// ResumeSession creates a new session by resuming a previous one via --resume.
func (p *QuerySessionPool) ResumeSession(ctx context.Context, convID uint, prevSessionID string, systemPrompt string) (*QuerySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	session, err := StartResumedSession(ctx, p.claudeBin, p.dataDir, prevSessionID, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	qs := newQuerySession(session, convID)
	p.sessions[convID] = qs
	log.Printf("[query-pool] Resumed session %s (from %s) for conversation %d", session.SessionID, prevSessionID, convID)
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
func StartSession(ctx context.Context, claudeBin string, dataDir string, systemPrompt string) (*InteractiveSession, error) {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--allowedTools", "Read", "Glob", "Grep", "LS",
		"--dangerously-skip-permissions",
	}

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmd(ctx, claudeBin, args, dataDir)

	stdinPipe, stdoutPipe, err := createPipes(cmd)
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
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Send init message to trigger init event
	initMsg := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"用户准备提问，请等待。\"}}\n"
	session.stdin.Write([]byte(initMsg))

	go session.readEvents()

	// Wait for session_id from system init event
	if err := waitForInit(session, 5*time.Second); err != nil {
		log.Printf("[session] Warning: %v, using fallback ID", err)
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}

	// Drain the init message's response so it doesn't leak into user conversations
	drainInitResponse(session)

	return session, nil
}

// StartResumedSession creates a new InteractiveSession that resumes a previous conversation.
func StartResumedSession(ctx context.Context, claudeBin string, dataDir string, prevSessionID string, systemPrompt string) (*InteractiveSession, error) {
	args := []string{
		"--resume", prevSessionID,
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--allowedTools", "Read", "Glob", "Grep", "LS",
		"--dangerously-skip-permissions",
	}

	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmd(ctx, claudeBin, args, dataDir)

	stdinPipe, stdoutPipe, err := createPipes(cmd)
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
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Send init message
	initMsg := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"继续对话。\"}}\n"
	session.stdin.Write([]byte(initMsg))

	go session.readEvents()

	// Wait for session_id
	if err := waitForInit(session, 5*time.Second); err != nil {
		log.Printf("[session] Warning: %v, using fallback ID", err)
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}

	// Drain the init message's response
	drainInitResponse(session)

	return session, nil
}

// drainInitResponse discards all events from the init message until a result event,
// preventing the init response from leaking into user conversations.
func drainInitResponse(session *InteractiveSession) {
	for {
		select {
		case evt := <-session.eventCh:
			if evt.Type == "result" || evt.Type == "error" {
				return
			}
		case <-time.After(10 * time.Second):
			log.Printf("[session] Warning: timed out draining init response")
			return
		}
	}
}
