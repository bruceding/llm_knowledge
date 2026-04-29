package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Client wraps the Claude CLI binary for programmatic invocation.
type Client struct {
	BinPath string // Path to the claude binary (e.g., "claude" or "/usr/local/bin/claude")
}

// StreamEvent represents a single event in the streaming response from Claude CLI.
type StreamEvent struct {
	Type             string   `json:"type"`                      // system, assistant, tool_use, thinking, result, error
	Content          string   `json:"content"`                   // Text content of the event (extracted)
	Subtype          string   `json:"subtype"`                   // subtype for system messages
	SessionID        string   `json:"session_id,omitempty"`      // Session ID from system events
	Result           string   `json:"result"`                    // Result text for type "result"
	Error            string   `json:"error,omitempty"`           // Error message if any
	ToolName         string   `json:"toolName,omitempty"`        // Tool name for tool_use events
	ToolInput        string   `json:"toolInput,omitempty"`       // Tool input for tool_use events
	Message          *Message `json:"message,omitempty"`         // Message for type "assistant"
	ResultMessageID  uint     `json:"resultMessageId,omitempty"` // User message ID for saving assistant reply (set in result)
	ResultFullContent string  `json:"resultFullContent,omitempty"` // Accumulated assistant content for saving (set in result)
}

// Message represents the message field in assistant events
type Message struct {
	Role    string        `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock represents a content block in a message
type ContentBlock struct {
	Type  string          `json:"type"`  // text, thinking, tool_use
	Text  string          `json:"text"`  // text content (for text/thinking blocks)
	ID    string          `json:"id,omitempty"`   // tool use ID
	Name  string          `json:"name,omitempty"` // tool name (for tool_use blocks)
	Input json.RawMessage `json:"input,omitempty"` // tool input (for tool_use blocks)
}

// RawEvent represents the raw JSON event from Claude CLI (used for parsing)
type RawEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Result  string          `json:"result"`
	IsError bool            `json:"is_error"`
	Message json.RawMessage `json:"message"`
}

// Send executes the Claude CLI with streaming JSON output.
// Events are sent to the provided channel as they are received.
// The caller should close the channel after Send returns.
func (c *Client) Send(ctx context.Context, prompt string, eventCh chan<- StreamEvent) error {
	// Use --bare to skip hooks for faster execution in automated scenarios
	// Use --allowedTools to pre-approve file operations
	// Use --dangerously-skip-permissions for non-interactive ingest scenarios
	cmd := exec.CommandContext(ctx, c.BinPath,
		"--bare",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", "Read", "Write", "Edit", "Bash",
		"--dangerously-skip-permissions",
	)
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// Increase buffer size for large messages
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		// Parse raw event first
		var raw RawEvent
		if err := json.Unmarshal(line, &raw); err != nil {
			// Skip malformed JSON lines
			continue
		}

		// Convert to StreamEvent based on type
		event := StreamEvent{
			Type:    raw.Type,
			Subtype: raw.Subtype,
		}

		switch raw.Type {
		case "assistant":
			// Parse message content
			if raw.Message != nil {
				var msg Message
				if err := json.Unmarshal(raw.Message, &msg); err == nil {
					event.Message = &msg
					// Extract text from content blocks
					for _, block := range msg.Content {
						if block.Type == "text" && block.Text != "" {
							event.Content = block.Text
						}
					}
				}
			}
		case "result":
			event.Content = raw.Result
			event.Result = raw.Result
			if raw.IsError {
				event.Type = "error"
				event.Error = raw.Result
			}
		case "system":
			// Skip system messages (hooks, init, etc.) unless it's an error
			if raw.Subtype == "error" {
				event.Type = "error"
			} else {
				continue
			}
		}

		// Send the event
		eventCh <- event
	}

	if err := scanner.Err(); err != nil {
		// Try to wait for the command even if scanner failed
		_ = cmd.Wait()
		return fmt.Errorf("error reading stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("claude command failed: %w", err)
	}

	return nil
}

// SendSimple executes the Claude CLI with a simple prompt and returns the response as a string.
// This is a convenience method for non-streaming use cases.
func (c *Client) SendSimple(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, c.BinPath, "-p", prompt)
	out, err := cmd.Output()
	if err != nil {
		// Include stderr in the error message if available
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("claude command failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude command failed: %w", err)
	}
	return string(out), nil
}

// SendSimpleWithRead executes the Claude CLI with -p mode and Read tool enabled.
// This is faster than stream-json mode for simple tasks like generating summaries.
func (c *Client) SendSimpleWithRead(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, c.BinPath,
		"--bare",
		"-p",
		"--allowedTools", "Read",
		"--dangerously-skip-permissions",
		prompt,
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("claude command failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SendWithTools executes the Claude CLI with tools enabled (like Read for PDFs).
// Returns the final response as a string.
func (c *Client) SendWithTools(ctx context.Context, prompt string) (string, error) {
	// Create a channel to collect events
	eventCh := make(chan StreamEvent, 100)

	// Run Send in a goroutine
	go func() {
		defer close(eventCh)
		if err := c.Send(ctx, prompt, eventCh); err != nil {
			eventCh <- StreamEvent{Type: "error", Error: err.Error()}
		}
	}()

	// Collect all content from events
	// Only use "result" event content - "assistant" text blocks are duplicated in result
	var result strings.Builder
	for evt := range eventCh {
		if evt.Type == "error" && evt.Error != "" {
			return "", fmt.Errorf("claude error: %s", evt.Error)
		}
		if evt.Type == "result" && evt.Result != "" {
			result.WriteString(evt.Result)
		}
	}

	return result.String(), nil
}

// SendWithOutput executes the Claude CLI and writes output to the provided writer.
// This is useful for capturing output directly to a file or buffer.
func (c *Client) SendWithOutput(ctx context.Context, prompt string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, c.BinPath, "-p", prompt)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude command failed: %w", err)
	}

	return nil
}

// NewClient creates a new Claude client with the default binary path ("claude").
func NewClient() *Client {
	return &Client{BinPath: "claude"}
}

// NewClientWithPath creates a new Claude client with a specific binary path.
func NewClientWithPath(binPath string) *Client {
	return &Client{BinPath: binPath}
}