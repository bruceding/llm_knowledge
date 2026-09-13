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

// writeFakeBackendBinary 写一个假 CLI:吃掉 stdin(prompt 走 stdin,D2),再把 lines 逐行
// 打到 stdout。用文件而不是在 shell 里 printf 拼接,免得 JSON 的引号与 % 需要二次转义。
func writeFakeBackendBinary(t *testing.T, name string, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	linesFile := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(linesFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\ncat > /dev/null\ncat " + linesFile + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return bin
}

// collectSend 跑一次 Send 并收下全部事件。channel 给足缓冲:Send 是同步写入的,
// 缓冲不够就会与调用方互相等待。
func collectSend(t *testing.T, prompt string) []StreamEvent {
	t.Helper()
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	eventCh := make(chan StreamEvent, 64)
	if err := client.Send(ctx, prompt, eventCh, ""); err != nil {
		t.Fatalf("Send: %v", err)
	}
	close(eventCh)

	var out []StreamEvent
	for evt := range eventCh {
		out = append(out, evt)
	}
	return out
}

// TestSend_ParsesEachBackendWireFormat 钉住「Send 的解析必须走 Protocol 接缝」这条性质。
//
// 改造前 Send 的 spawn 已经跟随 resolver(proto.Bin() / proto.OnceArgs),解析却仍用
// claude 的 RawEvent(assistant / result / system)。pi 的 --mode json 事件词表与它
// **没有交集**(message_update / message_end / agent_settled / response),于是切到 pi 之后
// 每个事件都落到 switch 之外:Content 与 Result 恒空、Message 恒 nil,而 err 也恒为 nil。
// 后果不是「少一段文本」:api/translate.go 会拿这个空串**覆盖已有的 paper_<lang>.md 并回
// complete**,ingest 的进度与错误日志一起消失 —— 一次静默的数据破坏。
//
// 两个子用例分别钉住两侧:pi 必须解析出内容(P0 的回归护栏),claude 必须与改造前逐条等价
// (完成定义「LLMBackend=claude 时行为与改造前逐条等价」)。
func TestSend_ParsesEachBackendWireFormat(t *testing.T) {
	t.Run("pi 的 --mode json 必须解析出内容", func(t *testing.T) {
		fakePi := writeFakeBackendBinary(t, "pi", []string{
			`{"type":"session","sessionId":"s-1"}`, // json 模式的头行,应被跳过
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","text":"你好"}}`,
			`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"你好,世界"}]}}`,
			`{"type":"agent_settled"}`,
		})
		scripts := t.TempDir()
		if err := os.WriteFile(filepath.Join(scripts, "pi-path-validator.ts"), []byte("// stub\n"), 0o644); err != nil {
			t.Fatalf("write sandbox stub: %v", err)
		}
		agent.Init(agent.ResolverOptions{
			ClaudeBin:  "/some/where/claude",
			PiBin:      fakePi,
			ScriptsDir: scripts,
		})
		t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })
		setPiBackendInDB(t)

		events := collectSend(t, "把这段翻译成英文")

		var assistant, result int
		for _, evt := range events {
			switch evt.Type {
			case "assistant":
				assistant++
				if evt.Content != "你好,世界" {
					t.Errorf("assistant 的 Content = %q, want %q —— pi 路径又变回空内容了(P0 复发)",
						evt.Content, "你好,世界")
				}
				if evt.Message == nil || len(evt.Message.Content) != 1 {
					t.Errorf("assistant 的 Message = %+v, want 恰一个 content 块", evt.Message)
				}
			case "result":
				result++
			case "session":
				t.Errorf("json 模式的头行被下发了(Type=%q),它应当被 ParseLine 跳过", evt.Type)
			}
		}
		if assistant != 1 {
			t.Errorf("assistant 事件数 = %d, want 1。全部事件:%+v", assistant, events)
		}
		if result != 1 {
			t.Errorf("result 事件数 = %d, want 1 —— agent_settled 必须归一化成既有的 result 语义,"+
				"否则上层等不到回合结束。全部事件:%+v", result, events)
		}
	})

	t.Run("claude 的 stream-json 与改造前逐条等价", func(t *testing.T) {
		fakeClaude := writeFakeBackendBinary(t, "claude", []string{
			`{"type":"system","subtype":"init","session_id":"abc"}`, // 必须跳过(改造前也跳过)
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"第一段"},{"type":"text","text":"第二段"}]}}`,
			`{"type":"result","result":"最终文本","is_error":false}`,
			`{"type":"system","subtype":"error"}`,             // → error
			`{"type":"result","result":"炸了","is_error":true}`, // → error + Error 文本
		})
		initTestBackend(t, fakeClaude)

		events := collectSend(t, "总结一下这篇论文")

		if len(events) != 4 {
			t.Fatalf("下发事件数 = %d, want 4(system/init 必须被跳过,否则 SSE 会多出改造前没有的帧)。事件:%+v",
				len(events), events)
		}

		asst := events[0]
		if asst.Type != "assistant" {
			t.Errorf("events[0].Type = %q, want assistant", asst.Type)
		}
		// Content 取**第一个**非空 text 块:改造前的内联循环取最后一个(它不 break)。
		// 统一到 Protocol 接缝后以第一个为准,与 ClaudeProtocol.ParseLine / PiProtocol.ParseLine
		// 的既有约定一致。差异只在「一条 assistant 消息含多个 text 块」时可观测;
		// 而 api/translate.go 无论如何都会把 Content 与 Message 的全部 text 块**都**追加一遍
		// (既有的重复写入缺陷,与本修复无关,已单独记录)。
		if asst.Content != "第一段" {
			t.Errorf("events[0].Content = %q, want 第一段(ParseLine 取第一个非空 text 块)", asst.Content)
		}
		if asst.Message == nil || len(asst.Message.Content) != 2 {
			t.Errorf("events[0].Message = %+v, want 2 个 content 块", asst.Message)
		}

		res := events[1]
		if res.Type != "result" || res.Result != "最终文本" || res.Content != "最终文本" {
			t.Errorf("events[1] = %+v, want Type=result 且 Result/Content 都是 最终文本", res)
		}
		if res.ResultIsError {
			t.Errorf("events[1].ResultIsError = true, want false(is_error=false)")
		}

		if events[2].Type != "error" {
			t.Errorf("events[2].Type = %q, want error(system 且 subtype=error 必须转成 error)", events[2].Type)
		}
		last := events[3]
		if last.Type != "error" || last.Error != "炸了" {
			t.Errorf("events[3] = %+v, want Type=error 且 Error=炸了(is_error=true 的 result)", last)
		}
	})
}

// writeFakeBackendBinaryWithStderr 与 writeFakeBackendBinary 同,但额外往 stderr 写一段文本。
// 用于复现「退出码 0 + stdout 没有可解析事件 + 真正的原因只在 stderr」这条实测行为。
func writeFakeBackendBinaryWithStderr(t *testing.T, name string, lines []string, stderrText string) string {
	t.Helper()
	dir := t.TempDir()
	linesFile := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(linesFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}
	errFile := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(errFile, []byte(stderrText), 0o644); err != nil {
		t.Fatalf("write stderr file: %v", err)
	}
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\ncat > /dev/null\ncat " + linesFile + "\ncat " + errFile + " >&2\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return bin
}

// initPiTestBackend 把 resolver 指向 pi 后端:假 pi 二进制 + 沙箱 stub(OnceArgs 的
// fail-closed 前置校验要求该文件存在)+ DB 里的 LLMBackend="pi"。
func initPiTestBackend(t *testing.T, fakePi string) {
	t.Helper()
	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "pi-path-validator.ts"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write sandbox stub: %v", err)
	}
	agent.Init(agent.ResolverOptions{
		ClaudeBin:  "/some/where/claude",
		PiBin:      fakePi,
		ScriptsDir: scripts,
	})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })
	setPiBackendInDB(t)
}

// sendAndCollect 跑一次 Send,同时返回事件与错误(不代替调用方判定成败)。
func sendAndCollect(t *testing.T, prompt string) ([]StreamEvent, error) {
	t.Helper()
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	eventCh := make(chan StreamEvent, 64)
	err := client.Send(ctx, prompt, eventCh, "")
	close(eventCh)

	var out []StreamEvent
	for evt := range eventCh {
		out = append(out, evt)
	}
	return out, err
}

// TestSend_ZeroEventsWithStderrIsAnError 钉住实测到的 pi 0.85.1 行为:模型/凭据不可用时,
// `--mode json` 只在 stdout 写一行 session 头(被 ParseLine 跳过)、把原因写在 stderr,
// 而**退出码是 0**。
//
// 于是「解析改走 Protocol 接缝」这一修复对它**无效** —— stdout 上压根没有可解析的事件。
// 若不把「零事件 + 有 stderr」判成失败,调用方看到的就是「成功但内容为空」:
// api/translate.go 会拿空串覆盖已有的 paper_<lang>.md 并回 complete,ingest 静默"完成"。
// 切换探测也拦不住:ProbePi 只跑 `pi --version` + SessionArgs + web-search.json 静态预检,
// **不验凭据**。
func TestSend_ZeroEventsWithStderrIsAnError(t *testing.T) {
	t.Run("零事件加stderr必须报错", func(t *testing.T) {
		fakePi := writeFakeBackendBinaryWithStderr(t, "pi",
			[]string{`{"type":"session","version":3,"id":"s-1"}`},
			"No API key found for the selected model.\n\nUse /login to log into a provider.\n")
		initPiTestBackend(t, fakePi)

		events, err := sendAndCollect(t, "把这段翻译成英文")
		if err == nil {
			t.Fatalf("Send 返回 nil,但一个事件都没产出且 stderr 有内容 —— " +
				"调用方会把它当成「成功但内容为空」,translate 因此会用空串覆盖已有译文")
		}
		assertNotResolverError(t, err)
		if !strings.Contains(err.Error(), "No API key") {
			t.Errorf("错误里没有带上 stderr 的原因,运维将无从判断: %v", err)
		}
		for _, evt := range events {
			t.Errorf("零事件场景下不应下发任何事件,收到 %+v", evt)
		}
	})

	t.Run("有事件时不因stderr噪音报错", func(t *testing.T) {
		// 反向对照:没有这一腿,「只要 stderr 非空就报错」也能让上面那条变绿,
		// 而那会把所有带告警输出的正常回合一起打成失败。
		fakePi := writeFakeBackendBinaryWithStderr(t, "pi",
			[]string{
				`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"译文在此"}]}}`,
				`{"type":"agent_settled"}`,
			},
			"Warning: some noisy deprecation notice\n")
		initPiTestBackend(t, fakePi)

		events, err := sendAndCollect(t, "把这段翻译成英文")
		if err != nil {
			t.Fatalf("有正常事件却报错(过度收紧): %v", err)
		}
		var got string
		for _, evt := range events {
			if evt.Type == "assistant" {
				got = evt.Content
			}
		}
		if got != "译文在此" {
			t.Errorf("assistant 的 Content = %q, want 译文在此", got)
		}
	})
}
