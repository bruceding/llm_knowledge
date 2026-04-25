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
	Type    string `json:"type"`             // assistant, tool_use, tool_result, result
	Content string `json:"content"`           // Text content of the event
	Tool    string `json:"tool,omitempty"`    // Tool name if type is tool_use or tool_result
	Error   string `json:"error,omitempty"`  // Error message if any
}

// Send executes the Claude CLI with streaming JSON output.
// Events are sent to the provided channel as they are received.
// The caller should close the channel after Send returns.
func (c *Client) Send(ctx context.Context, prompt string, eventCh chan<- StreamEvent) error {
	cmd := exec.CommandContext(ctx, c.BinPath, "--stream-json")
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var event StreamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil {
			// Only send valid events
			eventCh <- event
		}
		// Silently skip malformed JSON lines
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