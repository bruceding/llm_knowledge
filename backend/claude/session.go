package claude

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"llm-knowledge/agent"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ImageData 已上移到 agent 包,保留别名。
type ImageData = agent.ImageData

// InteractiveSession manages a bidirectional stream-json session with Claude CLI
type InteractiveSession struct {
	SessionID        string
	OwnerUserID      uint // user who created this session (for authorization)
	OwnerDocID       uint // document ID this session is for (for authorization)
	cmd              *exec.Cmd
	stdin            io.Writer
	stdoutScanner    *bufio.Scanner
	proto            agent.Protocol     // 本会话的 CLI 协议实现
	eventCh          chan StreamEvent   // main event channel (closed by readEvents)
	streamChs        []chan StreamEvent // subscriber channels for fan-out
	streamingContent strings.Builder    // accumulated text for SSE reconnect recovery
	hasStreamDeltas  bool               // true if stream_event text deltas received this turn
	lastDisconnect   time.Time
	sseCount         int // active SSE connections
	mu               sync.Mutex
	closeOnce        sync.Once // protects Close() from double channel close
	ctx              context.Context
	cancel           context.CancelFunc
	initDone         chan struct{}             // closed when system.init event is received
	onSessionID      func(oldID, newID string) // optional callback when real session_id arrives (for pool map + DB update)
	triedResume      bool                      // set if --resume was passed; pairs with onResumeFailed
	onResumeFailed   func()                    // invoked if process exits without ever emitting system.init while triedResume is true
	closedExplicitly bool                      // set by Close(); suppresses onResumeFailed (the id may still be valid)
}

// SessionPool manages all active sessions
type SessionPool struct {
	sessions  map[string]*InteractiveSession
	mu        sync.RWMutex
	dataDir   string
	claudeBin string
	done      chan struct{}
}

// NewSessionPool creates a new session pool
func NewSessionPool(dataDir, claudeBin string) *SessionPool {
	p := &SessionPool{
		sessions:  make(map[string]*InteractiveSession),
		dataDir:   dataDir,
		claudeBin: claudeBin,
		done:      make(chan struct{}),
	}
	go p.cleanupLoop()
	return p
}

// Close terminates all sessions and stops the cleanup loop.
func (p *SessionPool) Close() {
	close(p.done)
	p.mu.Lock()
	for sid, session := range p.sessions {
		session.Close()
		delete(p.sessions, sid)
	}
	p.mu.Unlock()
	log.Printf("[session] SessionPool closed, all sessions terminated")
}

// cleanupLoop closes sessions after 120 seconds of no active SSE connections
func (p *SessionPool) cleanupLoop() {
	for {
		select {
		case <-p.done:
			return
		case <-time.After(10 * time.Second):
		}
		var toClose []*InteractiveSession
		p.mu.Lock()
		for sid, session := range p.sessions {
			sseCount, lastDisconnect := session.SSEState()
			if sseCount == 0 && !lastDisconnect.IsZero() &&
				lastDisconnect.Add(120*time.Second).Before(time.Now()) {
				log.Printf("[session] Closing session %s after 120s timeout", sid)
				toClose = append(toClose, session)
				delete(p.sessions, sid)
			}
		}
		p.mu.Unlock()
		for _, session := range toClose {
			session.Close()
		}
	}
}

// Helper functions for creating interactive sessions

func buildCmd(ctx context.Context, claudeBin string, args []string, dataDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = dataDir
	return cmd
}

// buildCmdWithEnv builds a command with a pre-filtered environment.
// extraEnv 应来自 Protocol.Env,它已过滤重复的 ALLOWED_DIR。
func buildCmdWithEnv(ctx context.Context, claudeBin string, args []string, dataDir string, extraEnv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = dataDir
	if len(extraEnv) > 0 {
		cmd.Env = extraEnv
	}
	return cmd
}

func createPipes(cmd *exec.Cmd) (io.Writer, io.Reader, io.Reader, error) {
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	return stdinPipe, stdoutPipe, stderrPipe, nil
}

func newScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return scanner
}

func waitForInit(session *InteractiveSession, timeout time.Duration) error {
	select {
	case <-session.initDone:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for init event")
	}
}

// StartSession creates a new Claude session with user/document ownership.
// If prevSessionID is a real (non-fallback) Claude session ID, --resume is added
// so Claude restores the conversation history.
// onRealSessionID, if non-nil, is invoked when the real session_id arrives via
// system.init (in addition to the pool's own alias-registration callback).
// onResumeFailed, if non-nil, is invoked when --resume was attempted but the
// Claude process exited without ever emitting system.init — typically because
// the prevSessionID was stale (session file gone, machine moved, etc.).
// Callers should use this to clear the cached prevSessionID so the next attempt
// starts fresh instead of looping on the same broken resume.
// userDir is required for file isolation - returns an error if empty.
func (p *SessionPool) StartSession(ctx context.Context, docInfo string, userID uint, docID uint, userDir string, prevSessionID string, onRealSessionID func(oldID, newID string), onResumeFailed func()) (*InteractiveSession, error) {
	if userDir == "" {
		return nil, fmt.Errorf("userDir is required for session isolation")
	}
	workDir := userDir

	proto := agent.NewClaudeProtocol(p.claudeBin, GetSettingsPath())

	// Add system prompt with document context
	systemPrompt := fmt.Sprintf("用户正在询问文档相关问题。%s 请使用 Read 工具读取相关文件回答。如果文件内容不足以回答，可以使用你自己的知识补充。", docInfo)

	resuming := prevSessionID != "" && !strings.HasPrefix(prevSessionID, "local-")
	var args []string
	var err error
	if resuming {
		args, err = proto.ResumeArgs(prevSessionID, systemPrompt, []string{"Read"})
	} else {
		args, err = proto.SessionArgs(systemPrompt, []string{"Read"})
	}
	if err != nil {
		return nil, fmt.Errorf("build session args: %w", err)
	}

	// Build environment with ALLOWED_DIR
	env := proto.Env(workDir)

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmdWithEnv(ctx, p.claudeBin, args, workDir, env)

	stdinPipe, stdoutPipe, stderrPipe, err := createPipes(cmd)
	if err != nil {
		cancel()
		return nil, err
	}

	session := &InteractiveSession{
		OwnerUserID:   userID,
		OwnerDocID:    docID,
		cmd:           cmd,
		stdin:         stdinPipe,
		stdoutScanner: newScanner(stdoutPipe),
		proto:         proto,
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
			log.Printf("[session] Claude stderr: %s", scanner.Text())
		}
		if scanner.Err() != nil {
			log.Printf("[session] stderr scanner error: %v", scanner.Err())
		}
	}()

	// Set fallback session ID and wire callbacks BEFORE starting readEvents so
	// system.init can never arrive on a still-nil onSessionID (data race).
	session.mu.Lock()
	if session.SessionID == "" {
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	session.onSessionID = func(oldID, newID string) {
		p.mu.Lock()
		p.sessions[newID] = session
		p.mu.Unlock()
		log.Printf("[session] SessionPool added alias: %s (keeping old key %s)", newID, oldID)
		if onRealSessionID != nil {
			onRealSessionID(oldID, newID)
		}
	}
	if resuming {
		// Track the fact that this session expects --resume to succeed; if
		// readEvents finishes without ever firing system.init, onResumeFailed
		// is invoked so the caller can drop the stale prevSessionID.
		session.triedResume = true
		session.onResumeFailed = onResumeFailed
	}
	sessionID := session.SessionID
	session.mu.Unlock()

	// In interactive mode (no --print), system.init only fires after the first
	// user message, so don't block waiting for it. Use a fallback ID immediately;
	// the real session_id will be captured by readEvents via onSessionID callback.
	go session.readEvents()

	p.mu.Lock()
	p.sessions[sessionID] = session
	p.mu.Unlock()

	if resuming {
		log.Printf("[session] Started resumed session from prev=%s (fallback id=%s)", prevSessionID, session.SessionID)
	} else {
		log.Printf("[session] Started new session %s", session.SessionID)
	}
	return session, nil
}

// GetSession retrieves an existing session
func (p *SessionPool) GetSession(sessionId string) *InteractiveSession {
	p.mu.RLock()
	session := p.sessions[sessionId]
	p.mu.RUnlock()
	return session
}

// CloseByDocID closes and removes all sessions owned by the given docID.
// Used by fresh=1 (Clear Chat) to ensure old callbacks don't race with a
// new session's onRealSessionID / onResumeFailed DB writes.
func (p *SessionPool) CloseByDocID(docID uint) {
	p.mu.Lock()
	closed := map[*InteractiveSession]bool{}
	for sid, session := range p.sessions {
		if session.OwnerDocID == docID && !closed[session] {
			session.Close()
			closed[session] = true
		}
		delete(p.sessions, sid)
	}
	p.mu.Unlock()
}

// HasSession checks if a session exists
func (p *SessionPool) HasSession(sessionId string) bool {
	p.mu.RLock()
	_, exists := p.sessions[sessionId]
	p.mu.RUnlock()
	return exists
}

// SendUserMessage writes a message to stdin, encoded by the session's Protocol.
func (s *InteractiveSession) SendUserMessage(content string) error {
	return s.sendEncoded(func() ([]byte, error) {
		return s.proto.EncodeUserMessage(content, nil)
	}, "message")
}

// SendUserMessageWithImages sends a message with images to stdin
// Format: {"type":"user","message":{"role":"user","content":[...]}}
func (s *InteractiveSession) SendUserMessageWithImages(content string, images []ImageData) error {
	return s.sendEncoded(func() ([]byte, error) {
		return s.proto.EncodeUserMessage(content, images)
	}, fmt.Sprintf("%d image(s)", len(images)))
}

// SendInterrupt sends a control_request interrupt, encoded by the session's Protocol.
func (s *InteractiveSession) SendInterrupt() error {
	return s.sendEncoded(s.proto.EncodeInterrupt, "interrupt")
}

// sendEncoded 在持有 s.mu 的情况下把已编码的行写入 stdin。
// what 仅用于日志。
func (s *InteractiveSession) sendEncoded(encode func() ([]byte, error), what string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := encode()
	if err != nil {
		log.Printf("[session] Failed to encode %s: %v", what, err)
		return err
	}
	if _, err := s.stdin.Write(data); err != nil {
		log.Printf("[session] Failed to send %s: %v", what, err)
		return err
	}
	log.Printf("[session] Sent %s to session %s", what, s.SessionID)
	return nil
}

// SSEConnect increments SSE connection count; returns false if limit reached
const maxSSEConnsPerSession = 3

// GetSessionID returns the current session ID in a thread-safe manner.
// The ID may be a fallback if system.init hasn't been received yet.
func (s *InteractiveSession) GetSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SessionID
}

func (s *InteractiveSession) SSEConnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sseCount >= maxSSEConnsPerSession {
		log.Printf("[session] SSE connect rejected: count=%d, limit=%d", s.sseCount, maxSSEConnsPerSession)
		return false
	}
	s.sseCount++
	s.lastDisconnect = time.Time{} // Clear disconnect time
	log.Printf("[session] SSE connected, count=%d", s.sseCount)
	return true
}

// SSEDisconnect decrements SSE count and records disconnect time
func (s *InteractiveSession) SSEDisconnect() {
	s.mu.Lock()
	if s.sseCount <= 0 {
		s.sseCount = 0
		log.Printf("[session] WARNING: SSEDisconnect called with sseCount already 0 on session %s", s.SessionID)
		s.mu.Unlock()
		return
	}
	s.sseCount--
	if s.sseCount == 0 {
		s.lastDisconnect = time.Now()
	}
	count := s.sseCount
	s.mu.Unlock()
	log.Printf("[session] SSE disconnected, count=%d", count)
}

// SSEState returns the current SSE connection count and last disconnect time.
func (s *InteractiveSession) SSEState() (sseCount int, lastDisconnect time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sseCount, s.lastDisconnect
}

// Events returns the event channel (for direct access, prefer Subscribe for fan-out)
func (s *InteractiveSession) Events() <-chan StreamEvent {
	return s.eventCh
}

// Subscribe returns a channel that receives a copy of all session events.
// The channel has a buffer of 100 events. Call Unsubscribe when done.
func (s *InteractiveSession) Subscribe() chan StreamEvent {
	ch := make(chan StreamEvent, 100)
	s.mu.Lock()
	s.streamChs = append(s.streamChs, ch)
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *InteractiveSession) Unsubscribe(ch chan StreamEvent) {
	s.mu.Lock()
	for i, c := range s.streamChs {
		if c == ch {
			s.streamChs = append(s.streamChs[:i], s.streamChs[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

// StreamingContent returns accumulated text for SSE reconnect recovery.
func (s *InteractiveSession) StreamingContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamingContent.String()
}

// Close terminates the session (safe to call multiple times).
// Killing the subprocess closes its stdout, which lets readEvents drain its
// scan loop and close eventCh / streamChs as it exits — that's the only safe
// place to close those channels, since closing them here would race with
// readEvents trying to write.
// Also flips closedExplicitly so readEvents knows the process exit was
// requested (e.g. 120s cleanup, handleClear, server shutdown) rather than a
// genuine crash, and suppresses the onResumeFailed callback. Without this
// flag, killing a resumed session before its first user message triggers
// system.init would wipe a valid chat_session_id from the DB.
func (s *InteractiveSession) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closedExplicitly = true
		s.onSessionID = nil    // prevent stale DB writes after Close
		s.onResumeFailed = nil // prevent stale DB clears after Close
		sid := s.SessionID
		s.mu.Unlock()
		s.cancel()
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		log.Printf("[session] Closed session %s", sid)
	})
}

// readEvents parses stdout JSON events and fans out to subscribers.
// All events (including system) are sent to streamChs; StreamProcessor in SSE
// handlers filters them. streamingContent is accumulated for SSE reconnect recovery.
func (s *InteractiveSession) readEvents() {
	for s.stdoutScanner.Scan() {
		line := s.stdoutScanner.Bytes()

		event, ok := s.proto.ParseLine(line)
		if !ok {
			continue
		}

		// 累积 assistant 文本用于 SSE 重连恢复(若已收到 delta 则跳过)
		if event.Type == "assistant" && event.Content != "" {
			s.mu.Lock()
			if !s.hasStreamDeltas {
				s.streamingContent.WriteString(event.Content)
			}
			s.mu.Unlock()
		}

		// 累积文本 delta 用于重连恢复
		if event.Delta != nil && event.Delta.Kind == agent.DeltaText && event.Delta.Text != "" {
			s.mu.Lock()
			s.hasStreamDeltas = true
			s.streamingContent.WriteString(event.Delta.Text)
			s.mu.Unlock()
		}

		// Handle result type
		if event.Type == "result" {
			event.Content = event.Result
			if event.ResultIsError {
				event.Type = "error"
				event.Error = event.Result
			}
			// Reset streamingContent on turn end
			s.mu.Lock()
			s.streamingContent.Reset()
			s.hasStreamDeltas = false
			s.mu.Unlock()
		}

		// Auto-capture session_id from system.init event
		if event.Type == "system" && event.Subtype == "init" && event.SessionID != "" {
			s.mu.Lock()
			oldID := s.SessionID
			s.SessionID = event.SessionID
			callback := s.onSessionID
			s.mu.Unlock()
			log.Printf("[session] Got session_id from init event: %s (was: %s)", s.SessionID, oldID)
			if callback != nil && oldID != event.SessionID {
				callback(oldID, event.SessionID)
			}
			select {
			case <-s.initDone:
			default:
				close(s.initDone)
			}
			// Notify SSE subscribers so frontend can update its sessionId.
			// This synthetic event uses type "session_update" which the SSE
			// handler forwards directly (not filtered by StreamProcessor).
			if oldID != event.SessionID {
				updateEvt := StreamEvent{
					Type:      "session_update",
					SessionID: event.SessionID,
				}
				s.mu.Lock()
				for _, ch := range s.streamChs {
					select {
					case ch <- updateEvt:
					default:
					}
				}
				s.mu.Unlock()
			}
		}

		// Send to main channel (non-blocking to avoid deadlock when no consumer)
		select {
		case s.eventCh <- event:
		default:
		}

		// Fan-out to all subscribers — system/stream_event/result/assistant all pass through.
		// StreamProcessor in SSE handlers filters and converts events for frontend.
		s.mu.Lock()
		for _, ch := range s.streamChs {
			select {
			case ch <- event:
			default:
				if event.Type == "error" || event.Type == "result" {
					log.Printf("[session] WARNING: critical event %s dropped for slow subscriber on session %s", event.Type, s.SessionID)
				}
			}
		}
		s.mu.Unlock()
	}

	if err := s.stdoutScanner.Err(); err != nil {
		log.Printf("[session] Scanner error: %v", err)
	}

	s.cmd.Wait()

	// If --resume was attempted but Claude exited without ever emitting
	// system.init, the prevSessionID was almost certainly stale (file gone,
	// machine moved, format upgrade). Notify the caller so the cached id
	// can be cleared — otherwise every reconnect loops on the same broken
	// resume forever.
	s.mu.Lock()
	resumeFailed := false
	// Only treat a missing init as a resume failure when the process was NOT
	// killed by Close(). An explicit Close (idle timeout, navigate-away,
	// handleClear-after-no-message) doesn't mean the prevSessionID is bad —
	// the user just hasn't sent the first message yet, so init never fired.
	if s.triedResume && !s.closedExplicitly {
		select {
		case <-s.initDone:
			// init fired — resume succeeded
		default:
			resumeFailed = true
		}
	}
	cb := s.onResumeFailed
	sid := s.SessionID
	log.Printf("[session] Claude process ended for session %s", sid)
	for _, ch := range s.streamChs {
		close(ch)
	}
	s.streamChs = nil
	s.mu.Unlock()
	// Invoke onResumeFailed BEFORE closing eventCh so the stale session_id
	// is cleared from DB before the SSE stream signals completion to the
	// client.  Without this ordering, the frontend can reconnect and read
	// the same stale chat_session_id from DB before the callback runs,
	// creating an infinite --resume failure loop.
	if resumeFailed && cb != nil {
		log.Printf("[session] --resume failed for session %s (no system.init before exit)", sid)
		cb()
	}
	close(s.eventCh)
}
