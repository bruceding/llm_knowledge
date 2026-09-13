// Package agent_test 是 agent 的外部测试包。
//
// 之所以需要它:本文件要把 PiProtocol.ParseLine 的输出**穿过 claude 包的
// StreamProcessor**,验证前端实际收到的 SSE 事件序列。而 claude 已经 import agent,
// 所以包内测试无法反向 import claude;外部测试包(agent_test)与 agent 是两个不同的
// 包,agent_test → claude → agent 不成环。
//
// 只验证「一条约束」级的端到端行为;逐事件的映射断言在 pi_parse_test.go(包内)。
package agent_test

import (
	"strings"
	"testing"

	"llm-knowledge/agent"
	"llm-knowledge/claude"
)

// piProtocol 返回一个只用于解析的 PiProtocol。
// ParseLine 不碰 bin/scriptsDir/webTools,故零值即可 —— 这样测试也不必准备沙箱
// extension 文件,更不会因为读真实 ~/.pi/agent/web-search.json 而随环境漂移。
func piProtocol() agent.Protocol { return &agent.PiProtocol{} }

// runThroughProcessor 把若干行 pi JSONL 依次经 ParseLine → StreamProcessor.Process,
// 返回前端实际会收到的 SSE 事件(过滤掉 Process 返回的空事件)。
func runThroughProcessor(t *testing.T, lines ...string) []claude.SSEEvent {
	t.Helper()
	proto := piProtocol()
	sp := claude.NewStreamProcessor()

	var got []claude.SSEEvent
	for _, line := range lines {
		evt, ok := proto.ParseLine([]byte(line))
		if !ok {
			continue // ParseLine 判定该行应跳过,根本不该进 Process
		}
		if sse := sp.Process(evt); sse.Type != "" {
			got = append(got, sse)
		}
	}
	return got
}

// types 把 SSE 事件序列压成类型串,便于断言与失败时一眼看出差异。
func types(events []claude.SSEEvent) string {
	parts := make([]string, 0, len(events))
	for _, e := range events {
		parts = append(parts, e.Type)
	}
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------------
// 约束 1:rpc 模式启动后不主动输出任何行,也不发 --mode json 那个 session 头行,
// 所以 sessionId 只能靠主动发 get_state 取(计划 D1)。
//
// 本用例验证的是 D1 的**闭环**:get_state 的 response 被归一化成 system/init 之后,
// 上层不需要任何后端分支就能拿到会话 ID。注意它必须**不产生任何 SSE 事件** ——
// Process 对 system 事件是过滤掉的,所以这条握手对前端完全透明。
// ---------------------------------------------------------------------------
func TestPiConstraint1_SessionIDViaGetStateIsTransparentToFrontend(t *testing.T) {
	proto := piProtocol()
	line := `{"id":"init-1","type":"response","command":"get_state","success":true,"data":{"sessionId":"01a09977-e1f4-719f-8a77-1859426da76c","messageCount":0}}`

	evt, ok := proto.ParseLine([]byte(line))
	if !ok {
		t.Fatal("get_state 的成功响应必须被解析,否则上层拿不到真实 sessionId、只能用 local-<UnixNano> 兜底")
	}
	if evt.Type != "system" || evt.Subtype != "init" {
		t.Errorf("必须归一化成 system/init 才能复用既有的 waitForInit 与 onSessionID 链,got type=%q subtype=%q", evt.Type, evt.Subtype)
	}
	if evt.SessionID != "01a09977-e1f4-719f-8a77-1859426da76c" {
		t.Errorf("SessionID = %q, want the real pi session id", evt.SessionID)
	}

	// 对前端必须完全透明:不能因为多了这条握手就多出一个 SSE 事件
	events := runThroughProcessor(t, line)
	if len(events) != 0 {
		t.Errorf("get_state 握手不得产生任何 SSE 事件,前端零改动是全局约束。got %v", events)
	}
}

// ---------------------------------------------------------------------------
// 约束 2:pi 给 user 消息也发 message_start/message_end,且 content[0].text 就是
// 我们发出的 prompt 原文。不按 role 过滤的话,用户自己的提问会被当作助手回复
// 推回前端(聊天框里出现两条一样的消息)。
//
// 穿 StreamProcessor 验证:一个全新的处理器(未收到任何 delta,故 streamedDeltas
// 为 false)在遇到 assistant 消息时会下发 full —— 这正是 user 消息一旦漏过滤
// 就会走到的分支。
// ---------------------------------------------------------------------------
func TestPiConstraint2_UserMessageEndNeverReachesFrontend(t *testing.T) {
	userPrompt := "请读一下这份文档并总结要点"
	userLine := `{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"` + userPrompt + `"}]}}`

	events := runThroughProcessor(t, userLine)
	if len(events) != 0 {
		t.Fatalf("user 的 message_end 不得产生任何 SSE 事件,got %v", events)
	}

	// 对照组:同样的形状换成 role=assistant 就**必须**下发 full。
	// 没有对照组就无法区分「被 role 过滤掉了」与「本来就不会产生事件」。
	assistantLine := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"` + userPrompt + `"}]}}`
	control := runThroughProcessor(t, assistantLine)
	if len(control) != 1 || control[0].Type != "full" || control[0].Content != userPrompt {
		t.Fatalf("对照组失效:assistant 的 message_end 本应下发一个 full 事件,got %v —— 那么上面 user 用例的通过就证明不了过滤生效", control)
	}
}

// ---------------------------------------------------------------------------
// 约束 3:同一段文本在一轮里出现三次 —— text_delta(增量)、text_end.content
// (完整)、message_end.message.content[](完整且权威)。只对 text_delta 产出 Delta,
// 前端才会只收到一份文本。
//
// 这是计划明确要求「穿 StreamProcessor 验证前端只收到一份文本」的那一条。
// 关键点:message_end 到达时 streamedDeltas 已为 true,所以 Process 会抑制 full;
// 也就是说 **streamedDeltas 对 pi 是必需路径,而不是 Qwen 代理的 workaround**
// (rpc.md:994 原话 "Treat message_end.message as authoritative")。
// ---------------------------------------------------------------------------
func TestPiConstraint3_TextAppearsOnceOnTheFrontend(t *testing.T) {
	events := runThroughProcessor(t,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"assistant","content":[]}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_start","contentIndex":0}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"让我想想"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_end","contentIndex":0,"content":"让我想想"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":1}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"Hello"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":" world"}}`,
		// 第二份完整内容:必须被忽略
		`{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":1,"content":"Hello world"}}`,
		// 第三份完整内容(权威):到达时 streamedDeltas 已为 true,故抑制 full
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"让我想想"},{"type":"text","text":"Hello world"}]}}`,
		`{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}]},"toolResults":[]}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
	)

	if got := types(events); got != "delta,delta,done" {
		t.Fatalf("前端应只收到两段增量 + 一个 done,got %q (完整: %+v)", got, events)
	}
	// 文本只出现一份:把 delta 拼起来正好等于完整内容,且没有任何 full 事件重复它
	var joined strings.Builder
	for _, e := range events {
		if e.Type == "delta" {
			joined.WriteString(e.Delta)
		}
		if e.Type == "full" {
			t.Errorf("不得出现 full 事件(那会让前端在同一段文本上再渲染一份): %+v", e)
		}
	}
	if joined.String() != "Hello world" {
		t.Errorf("拼起来的文本 = %q, want %q", joined.String(), "Hello world")
	}
	// thinking 内容不得泄漏给前端
	for _, e := range events {
		if strings.Contains(e.Delta, "让我想想") || strings.Contains(e.Content, "让我想想") {
			t.Errorf("thinking 内容不得下发给前端: %+v", e)
		}
	}
}

// ---------------------------------------------------------------------------
// 约束 4:prompt 命令自己的 response{success:true} **先于所有事件到达**
// (规格实测:response 在 +3.01s,首个 message_update 在 +4.16s),它只表示
// 「已接受」,不可当作完成信号 —— 否则会在模型还没开口时就给前端发 done。
// ---------------------------------------------------------------------------
func TestPiConstraint4_PromptResponseIsNotACompletionSignal(t *testing.T) {
	events := runThroughProcessor(t,
		// 真实顺序:prompt 的 response 最先到
		`{"id":"msg-1","type":"response","command":"prompt","success":true}`,
		`{"type":"agent_start"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"答案"}}`,
		`{"type":"agent_settled"}`,
	)

	if got := types(events); got != "delta,done" {
		t.Fatalf("prompt 的成功响应不得触发 done,got %q (完整: %+v)", got, events)
	}
	for i, e := range events {
		if e.Type == "done" && i != len(events)-1 {
			t.Errorf("done 必须是最后一个事件(它出现在 index %d,而增量在其后),说明 prompt 的 response 被误当成完成信号", i)
		}
	}
}

// ---------------------------------------------------------------------------
// 约束 5:轮次结束必须用 agent_settled,不是 agent_end。
// rpc.md:893 说 agent_end 之后 "may still be followed by retry, compaction, or
// queued continuations",而 :905-911 说 agent_settled 才是完全落定。
// 用 agent_end 会在自动重试/压缩时提前发 done,前端会以为这一轮结束了。
// ---------------------------------------------------------------------------
func TestPiConstraint5_SettledNotAgentEndDrivesDone(t *testing.T) {
	events := runThroughProcessor(t,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"第一段"}}`,
		// willRetry=true:自动重试即将发生,此时绝不能 done
		`{"type":"agent_end","messages":[],"willRetry":true}`,
		`{"type":"auto_retry_start"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"第二段"}}`,
		`{"type":"auto_retry_end"}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
	)

	if got := types(events); got != "delta,delta,done" {
		t.Fatalf("只有 agent_settled 该驱动 done,got %q (完整: %+v)", got, events)
	}
	doneCount := 0
	for _, e := range events {
		if e.Type == "done" {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("done 应恰好一次,got %d 次(agent_end 被误用会导致重试期间提前 done)", doneCount)
	}
}

// ---------------------------------------------------------------------------
// 约束 6(承 main 的 89c3862):空 toolcall_delta 不得产出 DeltaToolInput。
//
// Process(claude/stream.go)对空 ToolInput **没有**二次守卫:它会执行
// tool.input += "" 然后下发一个携带上一条累积输入的 tool_input 事件。所以守卫
// 必须落在 ParseLine,而本用例穿 StreamProcessor 证明前端不会收到重复的 tool_input。
// ---------------------------------------------------------------------------
func TestPiConstraint6_EmptyToolDeltaDoesNotDuplicateToolInput(t *testing.T) {
	events := runThroughProcessor(t,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_abc123","toolName":"read"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"{\"path\":"}}`,
		// 空增量:放行就会多出一个内容完全相同的 tool_input
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":""}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"\"a.txt\"}"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","contentIndex":1,"toolCall":{"type":"toolCall","id":"call_abc123","name":"read","arguments":{"path":"a.txt"}}}}`,
	)

	if got := types(events); got != "tool_start,tool_input,tool_input,tool_end" {
		t.Fatalf("空 delta 不得产生额外的 tool_input,got %q (完整: %+v)", got, events)
	}

	var inputs []string
	for _, e := range events {
		if e.Type == "tool_input" {
			inputs = append(inputs, e.ToolInput)
			if e.ToolID != "call_abc123" || e.ToolName != "read" {
				t.Errorf("tool_input 必须带上 toolcall_start 的 id/name,got %+v", e)
			}
		}
	}
	// tool_input 携带的是**累积**输入,所以两次必须严格递进,不能出现相同值
	if len(inputs) != 2 || inputs[0] != `{"path":` || inputs[1] != `{"path":"a.txt"}` {
		t.Errorf("累积入参序列 = %v, want [{\"path\": {\"path\":\"a.txt\"}]", inputs)
	}
}

// ---------------------------------------------------------------------------
// 约束 7:contentIndex 必须原样带进 Delta.Index。
//
// Process 的 activeTools 是按 Index 键控的(map[int]*activeTool),所以
// toolcall_start 与后续 delta/end 的 contentIndex 不一致时,入参与结束事件会被
// 静默丢弃。pi 的 contentIndex 与 Claude 的 index 同构(规格实测:thinking 占 0、
// text 占 1),现有结构可直接复用 —— 但前提是 Index 不能错位到 0。
// ---------------------------------------------------------------------------
func TestPiConstraint7_ContentIndexIsCarriedIntoDeltaIndex(t *testing.T) {
	proto := piProtocol()

	// thinking 占 contentIndex 0,工具调用占 1(与规格实测一致)
	startEvt, ok := proto.ParseLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_1","toolName":"read"}}`))
	if !ok || startEvt.Delta == nil {
		t.Fatal("toolcall_start 应产出 Delta")
	}
	if startEvt.Delta.Index != 1 {
		t.Errorf("Delta.Index = %d, want 1(contentIndex 必须原样带过来,否则 Process 的 activeTools 会错位)", startEvt.Delta.Index)
	}

	deltaEvt, ok := proto.ParseLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"{}"}}`))
	if !ok || deltaEvt.Delta == nil || deltaEvt.Delta.Index != 1 {
		t.Fatalf("toolcall_delta 的 Index 必须与 start 一致,got %+v", deltaEvt.Delta)
	}

	endEvt, ok := proto.ParseLine([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","contentIndex":1}}`))
	if !ok || endEvt.Delta == nil || endEvt.Delta.Index != 1 {
		t.Fatalf("toolcall_end 的 Index 必须与 start 一致,got %+v", endEvt.Delta)
	}

	// 反证:index 错位时前端确实收不到入参 —— 证明 Index 是被真实使用的键,
	// 而不是一个填了也没人看的字段
	mismatched := runThroughProcessor(t,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_1","toolName":"read"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":0,"delta":"{}"}}`,
	)
	if got := types(mismatched); got != "tool_start" {
		t.Errorf("index 错位时 delta 应被丢弃(证明 activeTools 确实按 Index 键控),got %q", got)
	}
}

// ---------------------------------------------------------------------------
// 约束 8(规格未覆盖,实现时从 pi-ai 的 types.d.ts:256-264 核实):pi 的内容块叫
// "toolCall"、入参字段叫 arguments 且是个**对象**,而 claude 包的
// ExtractToolUseFromAssistantMsg 硬编码判 block.Type == "tool_use"。
//
// 不在 ParseLine 里翻译,pi 的工具块就会被静默丢弃。正常流式路径下前端已经从
// toolcall_start 拿到了 tool_start,所以这个洞在**SSE 重连**时才暴露:重连走的是
// assistant 完整消息 + sentToolIDs 去重那条路,拿不到工具块就永远补不回 tool_start。
// ---------------------------------------------------------------------------
func TestPiConstraint8_ToolCallBlocksSurviveIntoAssistantMessage(t *testing.T) {
	// 全新处理器 = 模拟重连后没有收到任何 delta 的情形
	events := runThroughProcessor(t,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"我来读一下"},{"type":"toolCall","id":"call_9","name":"read","arguments":{"path":"/data/a.md"}}]}}`,
	)

	got := types(events)
	if got != "tool_start" {
		t.Fatalf("pi 的 toolCall 块必须被翻译成 tool_use 才能被 ExtractToolUseFromAssistantMsg 认出,got %q (完整: %+v)", got, events)
	}
	if events[0].ToolID != "call_9" || events[0].ToolName != "read" {
		t.Errorf("tool_start 的 id/name = %q/%q, want call_9/read", events[0].ToolID, events[0].ToolName)
	}
	if events[0].ToolInput != `{"path":"/data/a.md"}` {
		t.Errorf("tool_start 的 toolInput = %q, want the arguments JSON", events[0].ToolInput)
	}

	// 剩下的文本应在 FlushPending 里作为 full 下发(处理器此前没见过任何 delta)。
	// 丢失文本意味着 pi 路径在重连后只显示工具、不显示回答。
	sp := claude.NewStreamProcessor()
	evt, ok := piProtocol().ParseLine([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"我来读一下"},{"type":"toolCall","id":"call_9","name":"read","arguments":{"path":"/data/a.md"}}]}}`))
	if !ok {
		t.Fatal("parse failed")
	}
	var collected []string
	if sse := sp.Process(evt); sse.Type != "" {
		collected = append(collected, sse.Type)
	}
	for {
		sse := sp.FlushPending()
		if sse.Type == "" {
			break
		}
		collected = append(collected, sse.Type+":"+sse.Content)
	}
	if len(collected) != 2 || collected[0] != "tool_start" || collected[1] != "full:我来读一下" {
		t.Errorf("工具块与文本都应下发,got %v", collected)
	}
}

// ---------------------------------------------------------------------------
// 约束 9:错误必须到达前端,而不是让 SSE 静默挂着。
// 两条来源:命令失败的 response、扩展抛错的 extension_error。
// 沙箱 extension 拦截工具调用时**不走** extension_error(它返回 {block:true,reason},
// 那是正常的工具结果),所以本用例不涉及越界拦截。
// ---------------------------------------------------------------------------
func TestPiConstraint9_ErrorsReachFrontend(t *testing.T) {
	events := runThroughProcessor(t,
		`{"id":"msg-1","type":"response","command":"prompt","success":false,"error":"Cannot submit a prompt while compaction is in progress"}`,
	)
	if len(events) != 1 || events[0].Type != "error" {
		t.Fatalf("命令失败必须下发 error 事件,got %v", events)
	}
	if !strings.Contains(events[0].Content, "compaction") {
		t.Errorf("error 内容必须带上 pi 的原因文本,got %q", events[0].Content)
	}

	extEvents := runThroughProcessor(t,
		`{"type":"extension_error","extensionPath":"/opt/scripts/pi-path-validator.ts","event":"tool_call","error":"boom"}`,
	)
	if len(extEvents) != 1 || extEvents[0].Type != "error" {
		t.Fatalf("extension_error 必须下发 error 事件,got %v", extEvents)
	}
	if !strings.Contains(extEvents[0].Content, "pi-path-validator.ts") {
		t.Errorf("error 内容必须指出是哪个扩展抛的,否则无法定位,got %q", extEvents[0].Content)
	}
}
