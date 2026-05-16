package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ImageData represents an image to send to Claude
type ImageData struct {
	MediaType  string // e.g., "image/png"
	Base64Data string // base64 encoded image data (without prefix)
}

// InteractiveSession manages a bidirectional stream-json session with Claude CLI
type InteractiveSession struct {
	SessionID      string
	OwnerUserID    uint // user who created this session (for authorization)
	OwnerDocID     uint // document ID this session is for (for authorization)
	cmd            *exec.Cmd
	stdin          io.Writer
	stdoutScanner  *bufio.Scanner
	eventCh        chan StreamEvent   // main event channel (closed by readEvents)
	streamChs      []chan StreamEvent // subscriber channels for fan-out
	streamingContent  strings.Builder  // accumulated text for SSE reconnect recovery
	hasStreamDeltas   bool             // true if stream_event text deltas received this turn
	lastDisconnect time.Time
	sseCount       int // active SSE connections
	mu             sync.Mutex
	closeOnce      sync.Once // protects Close() from double channel close
	ctx            context.Context
	cancel         context.CancelFunc
	initDone       chan struct{}             // closed when system.init event is received
	onSessionID    func(oldID, newID string) // optional callback when real session_id arrives (for pool map + DB update)
	triedResume    bool                      // set if --resume was passed; pairs with onResumeFailed
	onResumeFailed func()                    // invoked if process exits without ever emitting system.init while triedResume is true
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
		p.mu.Lock()
		for sid, session := range p.sessions {
			session.mu.Lock()
			if session.sseCount == 0 && !session.lastDisconnect.IsZero() &&
				session.lastDisconnect.Add(120*time.Second).Before(time.Now()) {
				log.Printf("[session] Closing session %s after 120s timeout", sid)
				session.Close()
				delete(p.sessions, sid)
			}
			session.mu.Unlock()
		}
		p.mu.Unlock()
	}
}

// Helper functions for creating interactive sessions

func buildCmd(ctx context.Context, claudeBin string, args []string, dataDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = dataDir
	return cmd
}

// buildCmdWithEnv builds a command with a pre-filtered environment.
// extraEnv should come from BuildSecureEnv which already filters duplicates.
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

	secureArgs, err := BuildSecureArgs([]string{"Read"})
	if err != nil {
		return nil, fmt.Errorf("build secure args: %w", err)
	}
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}
	args = append(args, secureArgs...)

	resuming := prevSessionID != "" && !strings.HasPrefix(prevSessionID, "local-")
	if resuming {
		args = append(args, "--resume", prevSessionID)
	}

	// Add system prompt with document context
	systemPrompt := fmt.Sprintf("用户正在询问文档相关问题。%s 请使用 Read 工具读取相关文件回答。如果文件内容不足以回答，可以使用你自己的知识补充。", docInfo)
	args = append(args, "--system-prompt", systemPrompt)

	// Build environment with ALLOWED_DIR
	env := BuildSecureEnv(workDir)

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

// HasSession checks if a session exists
func (p *SessionPool) HasSession(sessionId string) bool {
	p.mu.RLock()
	_, exists := p.sessions[sessionId]
	p.mu.RUnlock()
	return exists
}

// SendUserMessage writes a message to stdin using json.Marshal for robust encoding.
func (s *InteractiveSession) SendUserMessage(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": content,
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[session] Failed to marshal message: %v", err)
		return err
	}
	jsonData = append(jsonData, '\n')

	_, err = s.stdin.Write(jsonData)
	if err != nil {
		log.Printf("[session] Failed to send message: %v", err)
		return err
	}

	log.Printf("[session] Sent user message to session %s", s.SessionID)
	return nil
}

// SendUserMessageWithImages sends a message with images to stdin
// Format: {"type":"user","message":{"role":"user","content":[...]}}
func (s *InteractiveSession) SendUserMessageWithImages(content string, images []ImageData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build content array
	msgContent := []map[string]interface{}{}

	// Add images first
	for _, img := range images {
		msgContent = append(msgContent, map[string]interface{}{
			"type": "image",
			"source": map[string]interface{}{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       img.Base64Data,
			},
		})
	}

	// Add text
	if content != "" {
		msgContent = append(msgContent, map[string]interface{}{
			"type": "text",
			"text": content,
		})
	}

	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": msgContent,
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[session] Failed to marshal message: %v", err)
		return err
	}

	_, err = s.stdin.Write(jsonData)
	if err != nil {
		log.Printf("[session] Failed to send message: %v", err)
		return err
	}
	s.stdin.Write([]byte("\n"))

	log.Printf("[session] Sent user message with %d images to session %s", len(images), s.SessionID)
	return nil
}

// SendInterrupt sends a control_request interrupt using json.Marshal.
func (s *InteractiveSession) SendInterrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := map[string]interface{}{
		"type":       "control_request",
		"request_id": fmt.Sprintf("%d", time.Now().UnixNano()),
		"request":    map[string]interface{}{
			"subtype": "interrupt",
		},
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	jsonData = append(jsonData, '\n')

	_, err = s.stdin.Write(jsonData)
	if err != nil {
		log.Printf("[session] Failed to send interrupt: %v", err)
		return err
	}

	log.Printf("[session] Sent interrupt to session %s", s.SessionID)
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
func (s *InteractiveSession) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		s.mu.Lock()
		sid := s.SessionID
		s.mu.Unlock()
		log.Printf("[session] Closed session %s", sid)
	})
}

// readEvents parses stdout JSON events and fans out to subscribers.
// All events (including system) are sent to streamChs; StreamProcessor in SSE
// handlers filters them. streamingContent is accumulated for SSE reconnect recovery.
func (s *InteractiveSession) readEvents() {
	for s.stdoutScanner.Scan() {
		line := s.stdoutScanner.Bytes()

		// Parse the raw event
		var rawEvent struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			SessionID string          `json:"session_id"`
			Message   json.RawMessage `json:"message"`
			Event     json.RawMessage `json:"event"`   // stream_event sub-event payload
			Content   string          `json:"content"`
			Result    string          `json:"result"`
			IsError   bool            `json:"is_error"`
			Error     string          `json:"error"`
		}

		if err := json.Unmarshal(line, &rawEvent); err != nil {
			continue
		}

		event := StreamEvent{
			Type:      rawEvent.Type,
			Subtype:   rawEvent.Subtype,
			SessionID: rawEvent.SessionID,
			Content:   rawEvent.Content,
			Result:    rawEvent.Result,
			Error:     rawEvent.Error,
			Event:     rawEvent.Event,
		}

		// Extract content from assistant message
		if rawEvent.Type == "assistant" && rawEvent.Message != nil {
			var msg Message
			if err := json.Unmarshal(rawEvent.Message, &msg); err == nil {
				event.Message = &msg
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" {
						event.Content = block.Text
						break
					}
				}
			}
			// Accumulate assistant text for SSE reconnect recovery (skip if deltas already accumulated)
			s.mu.Lock()
			if event.Content != "" && !s.hasStreamDeltas {
				s.streamingContent.WriteString(event.Content)
			}
			s.mu.Unlock()
		}

		// Accumulate stream_event text deltas for reconnect recovery
		if rawEvent.Type == "stream_event" && rawEvent.Event != nil {
			delta := ExtractTextDelta(rawEvent.Event)
			if delta != "" {
				s.mu.Lock()
				s.hasStreamDeltas = true
				s.streamingContent.WriteString(delta)
				s.mu.Unlock()
			}
		}

		// Handle result type
		if rawEvent.Type == "result" {
			event.Content = rawEvent.Result
			if rawEvent.IsError {
				event.Type = "error"
				event.Error = rawEvent.Result
			}
			// Reset streamingContent on turn end
			s.mu.Lock()
			s.streamingContent.Reset()
			s.hasStreamDeltas = false
			s.mu.Unlock()
		}

		// Auto-capture session_id from system.init event
		if rawEvent.Type == "system" && rawEvent.Subtype == "init" && rawEvent.SessionID != "" {
			s.mu.Lock()
			oldID := s.SessionID
			s.SessionID = rawEvent.SessionID
			callback := s.onSessionID
			s.mu.Unlock()
			log.Printf("[session] Got session_id from init event: %s (was: %s)", s.SessionID, oldID)
			if callback != nil && oldID != rawEvent.SessionID {
				callback(oldID, rawEvent.SessionID)
			}
			select {
			case <-s.initDone:
			default:
				close(s.initDone)
			}
			// Notify SSE subscribers so frontend can update its sessionId.
			// This synthetic event uses type "session_update" which the SSE
			// handler forwards directly (not filtered by StreamProcessor).
			if oldID != rawEvent.SessionID {
				updateEvt := StreamEvent{
					Type:      "session_update",
					SessionID: rawEvent.SessionID,
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
	log.Printf("[session] Claude process ended for session %s", s.SessionID)

	// If --resume was attempted but Claude exited without ever emitting
	// system.init, the prevSessionID was almost certainly stale (file gone,
	// machine moved, format upgrade). Notify the caller so the cached id
	// can be cleared — otherwise every reconnect loops on the same broken
	// resume forever.
	s.mu.Lock()
	resumeFailed := false
	if s.triedResume {
		select {
		case <-s.initDone:
			// init fired — resume succeeded
		default:
			resumeFailed = true
		}
	}
	cb := s.onResumeFailed
	sid := s.SessionID
	for _, ch := range s.streamChs {
		close(ch)
	}
	s.streamChs = nil
	s.mu.Unlock()
	// Close eventCh exactly once, here, so external Close() callers don't race
	// with the writes in the scan loop above.
	close(s.eventCh)
	if resumeFailed && cb != nil {
		log.Printf("[session] --resume failed for session %s (no system.init before exit)", sid)
		cb()
	}
}
