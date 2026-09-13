package claude

import (
	"context"
	"strings"
	"testing"
	"time"
)

// assertNotResolverError 断言错误来自被测行为本身,而不是「resolver 没初始化」。
//
// 这条断言不是洁癖:Client.protocol() 改为走 agent.Current() 之后,BinPath 不再
// 决定 spawn 哪个二进制。若忘了 initTestBackend,下面这些用例仍然会「通过」——
// 因为 Current() 返回的错误同样满足 err != nil,而它们声称要测的
// 「二进制不存在」「上下文已取消」其实一次都没被执行到(实测过:错误文本是
// "agent.Init was never called")。有了这条断言,那种假绿会立刻变红。
func assertNotResolverError(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "agent.Init") {
		t.Errorf("用例测到的是 resolver 未初始化,而不是它声称要测的行为;"+
			"请先调用 initTestBackend 注入二进制。实际错误: %v", err)
	}
}

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
	// 二进制路径经 resolver 注入(Client.protocol() 已不再读 BinPath)
	initTestBackend(t, "/non/existent/path/to/claude")
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.SendSimple(ctx, "test prompt")
	if err == nil {
		t.Error("expected error for non-existent binary, got nil")
	}
	assertNotResolverError(t, err)
}

func TestSend_NonExistentBinary(t *testing.T) {
	initTestBackend(t, "/non/existent/path/to/claude")
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan StreamEvent, 1)
	err := client.Send(ctx, "test prompt", eventCh, "")
	close(eventCh)

	if err == nil {
		t.Error("expected error for non-existent binary, got nil")
	}
	assertNotResolverError(t, err)
}

func TestSend_ContextCancellation(t *testing.T) {
	initTestBackend(t, "/bin/sleep") // 用一个会阻塞的命令
	client := NewClient()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	eventCh := make(chan StreamEvent, 1)
	err := client.Send(ctx, "10", eventCh, "")
	close(eventCh)

	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
	assertNotResolverError(t, err)
}

func TestSendSimple_ContextCancellation(t *testing.T) {
	initTestBackend(t, "/bin/sleep") // 用一个会阻塞的命令
	client := NewClient()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := client.SendSimple(ctx, "10")
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
	assertNotResolverError(t, err)
}
