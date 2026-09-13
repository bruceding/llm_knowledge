package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// piParseCase 是一条 ParseLine 的表驱动用例。
//
// 所有 line 样本取自 pi 0.85.1 的 docs/rpc.md 线上示例(Events 一节)与本机实测
// 抓到的真实输出,**不是**照 @earendil-works/pi-ai 的 .d.ts 写的 —— 两者不一致
// (.d.ts 的 assistantMessageEvent 带 partial 累积快照,而 rpc.md:992-993 明写
// rpc 线格式已移除它)。
type piParseCase struct {
	name string
	line string

	wantOK bool

	// wantOK 为真时校验以下字段(zero value 表示「必须是空」)
	wantType      string
	wantSubtype   string
	wantSessionID string
	wantError     string
	wantContent   string
	wantDelta     *Delta
	wantMessage   *Message
}

func TestPiParseLine(t *testing.T) {
	// 真实抓到的 get_state 响应(本机 pi 0.85.1 + qwen3.8-max,已截断 model 对象)
	const realGetStateResponse = `{"id": "init-1", "type": "response", "command": "get_state", "success": true, "data": {"model": {"id": "qwen3.8-max"}, "thinkingLevel": "medium", "isStreaming": false, "sessionFile": "/tmp/x/.pi-sessions/01a09977.jsonl", "sessionId": "01a09977-e1f4-719f-8a77-1859426da76c", "messageCount": 0}}`

	cases := []piParseCase{
		// ---------- message_update → Delta ----------
		{
			name:     "text_delta 产出 DeltaText,并带上 contentIndex",
			line:     `{"type":"message_update","usage":{"input":100,"output":1},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello "}}`,
			wantOK:   true,
			wantType: "",
			wantDelta: &Delta{
				Kind:  DeltaText,
				Index: 0,
				Text:  "Hello ",
			},
		},
		{
			name:   "text_delta 的 contentIndex 被如实带进 Delta.Index(thinking 占 0、text 占 1 时不能错位)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"正文"}}`,
			wantOK: true,
			wantDelta: &Delta{
				Kind:  DeltaText,
				Index: 1,
				Text:  "正文",
			},
		},
		{
			name:   "空 text_delta 不产出(空增量守卫)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":""}}`,
			wantOK: false,
		},
		{
			// 规格实测约束 3:同一段文本一轮里出现三次,text_end.content 是第二份完整内容
			name:   "text_end 携带完整 content,必须忽略(否则与 delta 重复)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"Hello world"}}`,
			wantOK: false,
		},
		{
			name:   "text_start 忽略",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`,
			wantOK: false,
		},
		{
			name:   "thinking_start 忽略",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"thinking_start","contentIndex":0}}`,
			wantOK: false,
		},
		{
			name:   "thinking_delta 忽略(与既有 TestExtractTextDelta_ThinkingIgnored 行为一致)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"让我想想"}}`,
			wantOK: false,
		},
		{
			name:   "thinking_end 携带完整 content,同样忽略",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"thinking_end","contentIndex":0,"content":"让我想想"}}`,
			wantOK: false,
		},
		{
			name:   "toolcall_start 产出 DeltaToolStart,带 id 与 toolName",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_abc123","toolName":"write"}}`,
			wantOK: true,
			wantDelta: &Delta{
				Kind:     DeltaToolStart,
				Index:    1,
				ToolID:   "call_abc123",
				ToolName: "write",
			},
		},
		{
			// 与 ClaudeProtocol 的 content_block_start 对齐:ID/Name 为空即不产出
			name:   "toolcall_start 缺 id 不产出",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"toolName":"write"}}`,
			wantOK: false,
		},
		{
			name:   "toolcall_start 缺 toolName 不产出",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":1,"id":"call_abc123"}}`,
			wantOK: false,
		},
		{
			name:   "toolcall_delta 产出 DeltaToolInput(入参 JSON 片段)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"{\"path\":"}}`,
			wantOK: true,
			wantDelta: &Delta{
				Kind:      DeltaToolInput,
				Index:     1,
				ToolInput: `{"path":`,
			},
		},
		{
			// 承 main 的 89c3862:Process 对空 ToolInput 没有二次守卫,
			// 放行就会下发一个携带上一条累积输入的重复 tool_input 事件
			name:   "空 toolcall_delta 不产出(空增量守卫,pi 侧不可重犯 claude 侧曾有的疏漏)",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":""}}`,
			wantOK: false,
		},
		{
			name:   "toolcall_end 产出 DeltaToolEnd",
			line:   `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","contentIndex":1,"toolCall":{"type":"toolCall","id":"call_abc123","name":"write","arguments":{"path":"a.txt"}}}}`,
			wantOK: true,
			wantDelta: &Delta{
				Kind:  DeltaToolEnd,
				Index: 1,
			},
		},
		{
			name:   "message_update 缺 assistantMessageEvent 时跳过",
			line:   `{"type":"message_update","usage":{"input":1}}`,
			wantOK: false,
		},

		// ---------- message_end → assistant ----------
		{
			name:     "message_end(role=assistant)产出完整消息,并把 pi 的块类型翻译成归一化形状",
			line:     `{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"让我看看"},{"type":"text","text":"答案是这样"},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"/tmp/a.txt"}}]}}`,
			wantOK:   true,
			wantType: "assistant",
			// Content 取第一个非空 text 块,与 ClaudeProtocol 对齐
			wantContent: "答案是这样",
			wantMessage: &Message{
				Role: "assistant",
				Content: []ContentBlock{
					{Type: "thinking", Text: "让我看看"},
					{Type: "text", Text: "答案是这样"},
					{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"/tmp/a.txt"}`)},
				},
			},
		},
		{
			name:        "message_end 的 content 为空数组时仍产出 assistant(不报错、不丢弃)",
			line:        `{"type":"message_end","message":{"role":"assistant","content":[]}}`,
			wantOK:      true,
			wantType:    "assistant",
			wantMessage: &Message{Role: "assistant"},
		},
		{
			// 规格实测约束 2:pi 给 user 消息也发 message_start/message_end,
			// 且 content[0].text 就是我们发出的 prompt 原文
			name:   "message_end(role=user)必须跳过,否则用户自己的提问会被当作助手回复推回前端",
			line:   `{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"请读一下这份文档"}]}}`,
			wantOK: false,
		},
		{
			name:   "message_end 缺 message 字段时跳过",
			line:   `{"type":"message_end"}`,
			wantOK: false,
		},
		{
			name:   "message_start 忽略(即使 role=assistant)",
			line:   `{"type":"message_start","message":{"role":"assistant","content":[]}}`,
			wantOK: false,
		},

		// ---------- 轮次结束 ----------
		{
			// rpc.md:905-911:agent_settled 才是 "no automatic retry, compaction retry,
			// or queued continuation remains"
			name:     "agent_settled 映射为 result(驱动 SSE done)",
			line:     `{"type":"agent_settled"}`,
			wantOK:   true,
			wantType: "result",
		},
		{
			// rpc.md:893:agent_end 之后 "may still be followed by retry, compaction,
			// or queued continuations" —— 用它会在自动重试/压缩时提前发 done
			name:   "agent_end 不映射为 result",
			line:   `{"type":"agent_end","messages":[],"willRetry":true}`,
			wantOK: false,
		},

		// ---------- response ----------
		{
			name:          "get_state 成功响应归一化为 system/init(计划 D1)",
			line:          realGetStateResponse,
			wantOK:        true,
			wantType:      "system",
			wantSubtype:   "init",
			wantSessionID: "01a09977-e1f4-719f-8a77-1859426da76c",
		},
		{
			name:          "get_state 只有 sessionFile 时用它兜底(pi 的 --session 接受路径或 UUID 前缀)",
			line:          `{"id":"init-1","type":"response","command":"get_state","success":true,"data":{"sessionFile":"/tmp/x/.pi-sessions/abc.jsonl"}}`,
			wantOK:        true,
			wantType:      "system",
			wantSubtype:   "init",
			wantSessionID: "/tmp/x/.pi-sessions/abc.jsonl",
		},
		{
			// 拿不到 ID 时不能产出 system/init:上层 onSessionID 会用它注册别名并写 DB
			name:   "get_state 成功但拿不到任何 ID 时跳过(让 local-<UnixNano> fallback 兜住)",
			line:   `{"id":"init-1","type":"response","command":"get_state","success":true,"data":{"messageCount":0}}`,
			wantOK: false,
		},
		{
			name:   "get_state 成功但缺 data 时跳过",
			line:   `{"id":"init-1","type":"response","command":"get_state","success":true}`,
			wantOK: false,
		},
		{
			// 规格实测:prompt 的 response 在 +3.01s 到达,首个 message_update 在 +4.16s
			name:   "prompt 的成功响应不可当作完成信号",
			line:   `{"id":"msg-1","type":"response","command":"prompt","success":true}`,
			wantOK: false,
		},
		{
			name:   "abort 的成功响应跳过",
			line:   `{"type":"response","command":"abort","success":true}`,
			wantOK: false,
		},
		{
			name:      "任何命令失败(success=false)映射为 error",
			line:      `{"type":"response","command":"set_model","success":false,"error":"Model not found: invalid/model"}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "Model not found: invalid/model",
		},
		{
			name:      "get_state 失败也是 error,而不是 system/init",
			line:      `{"id":"init-1","type":"response","command":"get_state","success":false,"error":"no session"}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "no session",
		},
		{
			name:      "prompt 失败映射为 error",
			line:      `{"id":"msg-1","type":"response","command":"prompt","success":false,"error":"Cannot submit a prompt while compaction is in progress"}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "Cannot submit a prompt while compaction is in progress",
		},
		{
			name:      "失败但缺 error 文本时给出可定位的兜底消息",
			line:      `{"type":"response","command":"prompt","success":false}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "pi command failed: prompt",
		},
		{
			// success 缺席不算失败:pi 的解析错误响应带 success:false,而某些
			// 中间响应可能不带该字段,不应被误判成错误
			name:   "response 缺 success 字段时不当作失败",
			line:   `{"type":"response","command":"prompt"}`,
			wantOK: false,
		},
		{
			name:      "parse 错误(response.command=parse)也是 error",
			line:      `{"type":"response","command":"parse","success":false,"error":"Failed to parse command: Unexpected token..."}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "Failed to parse command: Unexpected token...",
		},

		// ---------- extension_error ----------
		{
			name:      "extension_error 映射为 error,并带上扩展路径与事件名以便定位",
			line:      `{"type":"extension_error","extensionPath":"/opt/scripts/pi-path-validator.ts","event":"tool_call","error":"Error message..."}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "extension error (/opt/scripts/pi-path-validator.ts, event=tool_call): Error message...",
		},
		{
			name:      "extension_error 缺 error 文本时仍有可读消息",
			line:      `{"type":"extension_error"}`,
			wantOK:    true,
			wantType:  "error",
			wantError: "extension error",
		},

		// ---------- 有意忽略的事件 ----------
		{name: "agent_start 忽略", line: `{"type":"agent_start"}`, wantOK: false},
		{name: "turn_start 忽略", line: `{"type":"turn_start"}`, wantOK: false},
		{
			name:   "turn_end 忽略(它带完整 message,放行会与 message_end 重复)",
			line:   `{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"toolResults":[]}`,
			wantOK: false,
		},
		{name: "queue_update 忽略", line: `{"type":"queue_update","queue":[]}`, wantOK: false},
		{name: "compaction_start 忽略", line: `{"type":"compaction_start"}`, wantOK: false},
		{name: "compaction_end 忽略", line: `{"type":"compaction_end"}`, wantOK: false},
		{name: "auto_retry_start 忽略", line: `{"type":"auto_retry_start"}`, wantOK: false},
		{name: "auto_retry_end 忽略", line: `{"type":"auto_retry_end"}`, wantOK: false},
		{name: "summarization_retry_scheduled 忽略", line: `{"type":"summarization_retry_scheduled"}`, wantOK: false},
		{name: "summarization_retry_attempt_start 忽略", line: `{"type":"summarization_retry_attempt_start"}`, wantOK: false},
		{name: "summarization_retry_finished 忽略", line: `{"type":"summarization_retry_finished"}`, wantOK: false},
		{
			name:   "bash_execution_update 忽略(我们不使用 rpc 的 bash 命令)",
			line:   `{"type":"bash_execution_update","id":"b1","output":"chunk"}`,
			wantOK: false,
		},
		{
			name:   "tool_execution_start 忽略(工具开始已由 toolcall_start 承载)",
			line:   `{"type":"tool_execution_start","toolCallId":"call_abc123","toolName":"bash","args":{"command":"ls -la"}}`,
			wantOK: false,
		},
		{
			// partialResult 是**累积快照**而非增量(rpc.md:1052-1054),放行会让前端重复显示
			name:   "tool_execution_update 忽略",
			line:   `{"type":"tool_execution_update","toolCallId":"call_abc123","toolName":"bash","partialResult":{"content":[{"type":"text","text":"partial output so far..."}]}}`,
			wantOK: false,
		},
		{
			// 见 D5:tool_execution_end 没有 contentIndex,映射成 DeltaToolEnd 会让
			// Index 落到 0,可能误关掉另一个正在进行的工具
			name:   "tool_execution_end 忽略(D5:工具结束由 toolcall_end 承载)",
			line:   `{"type":"tool_execution_end","toolCallId":"call_abc123","toolName":"bash","result":{"content":[{"type":"text","text":"total 48"}]},"isError":false}`,
			wantOK: false,
		},
		{
			// 本机实测抓到:pi-subagents 在没有任何请求的情况下主动推 UI 请求。
			// 它不在计划 Task 4 的忽略清单里,靠「未知类型静默跳过」落到这里。
			name:   "extension_ui_request 忽略(实测抓到 pi-subagents 主动推 setWidget)",
			line:   `{"type": "extension_ui_request", "id": "a83360bf-4d80-4e05-9d92-6467c3dba18e", "method": "setWidget", "widgetKey": "subagent-async"}`,
			wantOK: false,
		},
		{
			// 未知类型必须静默跳过而不是报错:pi 有破坏性变更历史(风险登记 R2),
			// 报错会让一次 pi 升级变成整条链路失败
			name:   "未知事件类型静默跳过",
			line:   `{"type":"some_brand_new_event","whatever":1}`,
			wantOK: false,
		},

		// ---------- framing 与畸形输入 ----------
		{name: "空行跳过", line: ``, wantOK: false},
		{name: "只有空白的行跳过", line: "   \t ", wantOK: false},
		{name: "畸形 JSON 跳过而不是报错", line: `{"type":"message_update",`, wantOK: false},
		{name: "根不是对象时跳过", line: `[1,2,3]`, wantOK: false},
		{name: "根是标量时跳过", line: `42`, wantOK: false},
		{
			name:     "行尾 \r 被剥除后正常解析",
			line:     "{\"type\":\"agent_settled\"}\r",
			wantOK:   true,
			wantType: "result",
		},
		{
			name:     "行尾 \\r\\n 被剥除后正常解析",
			line:     "{\"type\":\"agent_settled\"}\r\n",
			wantOK:   true,
			wantType: "result",
		},
	}

	p := &PiProtocol{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt, ok := p.ParseLine([]byte(tc.line))
			if ok != tc.wantOK {
				t.Fatalf("ParseLine() ok = %v, want %v (evt = %+v)", ok, tc.wantOK, evt)
			}
			if !ok {
				return
			}
			if evt.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", evt.Type, tc.wantType)
			}
			if evt.Subtype != tc.wantSubtype {
				t.Errorf("Subtype = %q, want %q", evt.Subtype, tc.wantSubtype)
			}
			if evt.SessionID != tc.wantSessionID {
				t.Errorf("SessionID = %q, want %q", evt.SessionID, tc.wantSessionID)
			}
			if evt.Error != tc.wantError {
				t.Errorf("Error = %q, want %q", evt.Error, tc.wantError)
			}
			if evt.Content != tc.wantContent {
				t.Errorf("Content = %q, want %q", evt.Content, tc.wantContent)
			}
			if !reflect.DeepEqual(evt.Delta, tc.wantDelta) {
				t.Errorf("Delta = %+v, want %+v", evt.Delta, tc.wantDelta)
			}
			if !reflect.DeepEqual(evt.Message, tc.wantMessage) {
				gotJSON, _ := json.Marshal(evt.Message)
				wantJSON, _ := json.Marshal(tc.wantMessage)
				t.Errorf("Message = %s, want %s", gotJSON, wantJSON)
			}
			// StreamEvent.Delta 是 json:"-",绝不能成为 wire 字段(全局约束)
			if b, err := json.Marshal(evt); err == nil && jsonHasKey(b, "Delta") {
				t.Errorf("归一化事件序列化后不得含 Delta 字段(json:\"-\"),got %s", b)
			}
		})
	}
}

func jsonHasKey(b []byte, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// TestPiParseLine_ToolCallTranslatedForUpperLayer 单独钉住那条**规格未覆盖**的坑:
// pi 的块类型是 "toolCall"(入参字段 arguments,是个对象),而 claude 包的
// ExtractToolUseFromAssistantMsg 硬编码判 "tool_use"。不翻译就会让 pi 的工具块
// 被静默丢弃 —— SSE 重连后前端永远收不到 tool_start。
//
// 这里在 agent 包内断言翻译结果;穿 StreamProcessor 的端到端验证在
// pi_constraints_test.go(外部测试包,可 import claude)。
func TestPiParseLine_ToolCallTranslatedForUpperLayer(t *testing.T) {
	p := &PiProtocol{}
	line := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_9","name":"read","arguments":{"path":"/data/a.md","offset":10}}]}}`

	evt, ok := p.ParseLine([]byte(line))
	if !ok {
		t.Fatal("expected the assistant message_end to be parsed")
	}
	if evt.Message == nil || len(evt.Message.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %+v", evt.Message)
	}
	block := evt.Message.Content[0]
	if block.Type != "tool_use" {
		t.Errorf("block type = %q, want the normalized %q (pi calls it toolCall)", block.Type, "tool_use")
	}
	if block.ID != "call_9" || block.Name != "read" {
		t.Errorf("block id/name = %q/%q, want call_9/read", block.ID, block.Name)
	}
	// arguments 是对象,归一化后必须是可直接 string() 的 JSON 文本
	if got := string(block.Input); got != `{"path":"/data/a.md","offset":10}` {
		t.Errorf("Input = %s, want the original arguments JSON", got)
	}
	var back map[string]any
	if err := json.Unmarshal(block.Input, &back); err != nil {
		t.Errorf("Input must stay valid JSON so the frontend can parse toolInput: %v", err)
	}
}

// TestPiParseLine_ProtocolAssertion 保证 PiProtocol 满足 Protocol(编译期断言在
// pi_parse.go 末尾;这里再跑一次运行时检查,让「Task 4 结束时接口已闭合」这件事
// 在测试输出里也可见)。
func TestPiParseLine_ProtocolAssertion(t *testing.T) {
	var proto Protocol = &PiProtocol{}
	if proto.Backend() != BackendPi {
		t.Errorf("Backend() = %q, want %q", proto.Backend(), BackendPi)
	}
	// Claude 侧的对应断言:两个后端走同一条 seam
	var claude Protocol = &ClaudeProtocol{}
	if claude.Backend() != BackendClaude {
		t.Errorf("Backend() = %q, want %q", claude.Backend(), BackendClaude)
	}
}
