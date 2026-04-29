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

// InteractiveSession manages a bidirectional stream-json session with Claude CLI
type InteractiveSession struct {
	SessionID      string
	cmd            *exec.Cmd
	stdin          io.Writer
	stdoutScanner  *bufio.Scanner
	eventCh        chan StreamEvent
	lastDisconnect time.Time
	sseCount       int // active SSE connections
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
}

// SessionPool manages all active sessions
type SessionPool struct {
	sessions  map[string]*InteractiveSession
	mu        sync.RWMutex
	dataDir   string
	claudeBin string
}

// NewSessionPool creates a new session pool
func NewSessionPool(dataDir, claudeBin string) *SessionPool {
	p := &SessionPool{
		sessions:  make(map[string]*InteractiveSession),
		dataDir:   dataDir,
		claudeBin: claudeBin,
	}
	go p.cleanupLoop()
	return p
}

// cleanupLoop closes sessions after 30 seconds of no active SSE connections
func (p *SessionPool) cleanupLoop() {
	for {
		time.Sleep(10 * time.Second)
		p.mu.Lock()
		for sid, session := range p.sessions {
			session.mu.Lock()
			if session.sseCount == 0 && !session.lastDisconnect.IsZero() &&
				session.lastDisconnect.Add(30*time.Second).Before(time.Now()) {
				log.Printf("[session] Closing session %s after 30s timeout", sid)
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

func createPipes(cmd *exec.Cmd) (io.Writer, io.Reader, error) {
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	return stdinPipe, stdoutPipe, nil
}

func newScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return scanner
}

func waitForInit(session *InteractiveSession, timeout time.Duration) error {
	for {
		select {
		case evt := <-session.eventCh:
			if evt.Type == "system" && evt.Subtype == "init" {
				session.SessionID = evt.SessionID
				return nil
			}
		case <-time.After(timeout):
			return fmt.Errorf("timed out waiting for init event")
		}
	}
}

// StartSession creates a new Claude session
func (p *SessionPool) StartSession(ctx context.Context, docInfo string) (*InteractiveSession, error) {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--allowedTools", "Read",
		"--dangerously-skip-permissions",
	}

	// Add system prompt with document context
	systemPrompt := fmt.Sprintf("用户正在询问文档相关问题。%s 请使用 Read 工具读取相关文件回答。如果文件内容不足以回答，可以使用你自己的知识补充。", docInfo)
	args = append(args, "--system-prompt", systemPrompt)

	ctx, cancel := context.WithCancel(ctx)
	cmd := buildCmd(ctx, p.claudeBin, args, p.dataDir)

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

	// IMPORTANT: Send initial stdin message BEFORE readEvents to trigger init
	initMsg := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"用户准备提问，请等待。\"}}\n"
	session.stdin.Write([]byte(initMsg))

	// Start reading events
	go session.readEvents()

	// Wait for session_id from system init event
	if err := waitForInit(session, 5*time.Second); err != nil {
		log.Printf("[session] Warning: %v, using fallback ID", err)
		session.SessionID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}

	p.mu.Lock()
	p.sessions[session.SessionID] = session
	p.mu.Unlock()

	log.Printf("[session] Started new session %s", session.SessionID)
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

// SendUserMessage writes a message to stdin
// Format: {"type":"user","message":{"role":"user","content":"..."}}
func (s *InteractiveSession) SendUserMessage(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Escape quotes and newlines in content
	escapedContent := strings.ReplaceAll(content, "\"", "\\\"")
	escapedContent = strings.ReplaceAll(escapedContent, "\n", "\\n")
	msg := fmt.Sprintf("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"%s\"}}\n", escapedContent)

	_, err := s.stdin.Write([]byte(msg))
	if err != nil {
		log.Printf("[session] Failed to send message: %v", err)
		return err
	}

	log.Printf("[session] Sent user message to session %s", s.SessionID)
	return nil
}

// SendInterrupt sends a control_request interrupt to stdin to stop the current turn.
// The session remains alive and can continue accepting new messages.
func (s *InteractiveSession) SendInterrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := fmt.Sprintf("{\"type\":\"control_request\",\"request_id\":\"%d\",\"request\":{\"subtype\":\"interrupt\"}}\n", time.Now().UnixNano())

	_, err := s.stdin.Write([]byte(msg))
	if err != nil {
		log.Printf("[session] Failed to send interrupt: %v", err)
		return err
	}

	log.Printf("[session] Sent interrupt to session %s", s.SessionID)
	return nil
}

// SSEConnect increments SSE connection count
func (s *InteractiveSession) SSEConnect() {
	s.mu.Lock()
	s.sseCount++
	s.lastDisconnect = time.Time{} // Clear disconnect time
	s.mu.Unlock()
	log.Printf("[session] SSE connected, count=%d", s.sseCount)
}

// SSEDisconnect decrements SSE count and records disconnect time
func (s *InteractiveSession) SSEDisconnect() {
	s.mu.Lock()
	s.sseCount--
	if s.sseCount == 0 {
		s.lastDisconnect = time.Now()
	}
	s.mu.Unlock()
	log.Printf("[session] SSE disconnected, count=%d", s.sseCount)
}

// Events returns the event channel
func (s *InteractiveSession) Events() <-chan StreamEvent {
	return s.eventCh
}

// Close terminates the session
func (s *InteractiveSession) Close() {
	s.cancel()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	close(s.eventCh)
	log.Printf("[session] Closed session %s", s.SessionID)
}

// readEvents parses stdout JSON events
func (s *InteractiveSession) readEvents() {
	for s.stdoutScanner.Scan() {
		line := s.stdoutScanner.Bytes()

		// Parse the raw event
		var rawEvent struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			SessionID string          `json:"session_id"`
			Message   json.RawMessage `json:"message"`
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
		}

		// Extract content from assistant message
		if rawEvent.Type == "assistant" && rawEvent.Message != nil {
			var msg Message
			if err := json.Unmarshal(rawEvent.Message, &msg); err == nil {
				event.Message = &msg
				// Extract text content for backwards compatibility
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" {
						event.Content = block.Text
						break
					}
				}
			}
		}

		// Handle result type
		if rawEvent.Type == "result" {
			event.Content = rawEvent.Result
			if rawEvent.IsError {
				event.Type = "error"
				event.Error = rawEvent.Result
			}
		}

		s.eventCh <- event
	}

	if err := s.stdoutScanner.Err(); err != nil {
		log.Printf("[session] Scanner error: %v", err)
	}

	// Wait for command to finish
	s.cmd.Wait()
	log.Printf("[session] Claude process ended for session %s", s.SessionID)
}