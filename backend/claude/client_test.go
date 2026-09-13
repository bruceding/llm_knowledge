package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm-knowledge/agent"
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

// TestNewClient_DoesNotPinABackend 钉住 NewClient() 返回的 Client **不携带任何后端
// 信息**(Proto 为 nil),于是 protocol() 必须向 resolver 惰性取值。这正是「Settings
// 里切后端」能对摘要/分节/翻译/PDF 这些 once 调用链路生效的前提。
//
// 原先这里断言的是 client.BinPath == "claude",也就是「默认钉死 claude」—— 那个断言
// 与后端开关直接矛盾,所以随 BinPath 字段一并删除。NewClientWithPath 也已移除:
// 让调用点自己钉住二进制,正是 Plan 1 遗留的接缝缺口(切了后端而这些链路照旧
// spawn claude,且不报错)。
func TestNewClient_DoesNotPinABackend(t *testing.T) {
	client := NewClient()
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.Proto != nil {
		t.Errorf("NewClient() 的 Proto = %v, want nil —— 非 nil 意味着后端被钉死在构造点,"+
			"Settings 里切换后端对这条链路将无效", client.Proto)
	}
}

// TestClientProtocol_FollowsResolver 断言 Client 的后端**跟着 resolver 走**。
// 这是 Task 8 的核心性质:删掉 BinPath 之后,once 调用链路必须随开关一起换后端,
// 而不是继续 spawn claude。
//
// 上面的 DoesNotPinABackend 只证明了「构造时没钉死」,证明不了「真的会跟随」——
// 一个恒返回 claude 的 protocol() 同样能通过它。所以这里两个后端各测一次。
func TestClientProtocol_FollowsResolver(t *testing.T) {
	client := NewClient()

	// claude 后端
	initTestBackend(t, "/some/where/claude")
	proto, err := client.protocol()
	if err != nil {
		t.Fatalf("claude 后端下 protocol() 报错: %v", err)
	}
	if got := proto.Backend(); got != agent.BackendClaude {
		t.Errorf("resolver 指向 claude 时 protocol().Backend() = %q, want %q", got, agent.BackendClaude)
	}
	if got := proto.Bin(); got != "/some/where/claude" {
		t.Errorf("protocol().Bin() = %q, want resolver 注入的 /some/where/claude", got)
	}

	// pi 后端:同一个 client 实例,只换 resolver 指向
	piBin := writeFakePiBinary(t)
	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "pi-path-validator.ts"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write sandbox stub: %v", err)
	}
	agent.Init(agent.ResolverOptions{
		ClaudeBin:  "/some/where/claude",
		PiBin:      piBin,
		ScriptsDir: scripts,
	})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })
	setPiBackendInDB(t)

	proto, err = client.protocol()
	if err != nil {
		t.Fatalf("pi 后端下 protocol() 报错: %v", err)
	}
	if got := proto.Backend(); got != agent.BackendPi {
		t.Errorf("resolver 指向 pi 时 protocol().Backend() = %q, want %q —— "+
			"说明这条链路仍在钉死 claude", got, agent.BackendPi)
	}
	if got := proto.Bin(); got != piBin {
		t.Errorf("protocol().Bin() = %q, want %q", got, piBin)
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
