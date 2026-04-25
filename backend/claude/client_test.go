package claude

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient()
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.BinPath != "claude" {
		t.Errorf("expected BinPath 'claude', got %q", client.BinPath)
	}
}

func TestNewClientWithPath(t *testing.T) {
	path := "/usr/local/bin/claude"
	client := NewClientWithPath(path)
	if client == nil {
		t.Fatal("NewClientWithPath returned nil")
	}
	if client.BinPath != path {
		t.Errorf("expected BinPath %q, got %q", path, client.BinPath)
	}
}

func TestSendSimple_NonExistentBinary(t *testing.T) {
	client := NewClientWithPath("/non/existent/path/to/claude")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.SendSimple(ctx, "test prompt")
	if err == nil {
		t.Error("expected error for non-existent binary, got nil")
	}
}

func TestSend_NonExistentBinary(t *testing.T) {
	client := NewClientWithPath("/non/existent/path/to/claude")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan StreamEvent, 1)
	err := client.Send(ctx, "test prompt", eventCh)
	close(eventCh)

	if err == nil {
		t.Error("expected error for non-existent binary, got nil")
	}
}

func TestSend_ContextCancellation(t *testing.T) {
	client := NewClientWithPath("/bin/sleep") // Use a command that will block
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	eventCh := make(chan StreamEvent, 1)
	err := client.Send(ctx, "10", eventCh)
	close(eventCh)

	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestSendSimple_ContextCancellation(t *testing.T) {
	client := NewClientWithPath("/bin/sleep") // Use a command that will block
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := client.SendSimple(ctx, "10")
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}