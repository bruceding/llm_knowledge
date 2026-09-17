package agent

import (
	"bytes"
	"encoding/json"
)

// piRawEvent 是 pi 单行 JSONL 输出的解析中间类型。
//
// 字段形状取自 pi 0.85.1 的 docs/rpc.md(Events 一节),而不是 @earendil-works/pi-ai
// 的 .d.ts —— 两者**不一致**,以 rpc.md 为准,因为 ParseLine 吃的是 rpc 线格式:
//
//   - pi-ai 的 types.d.ts:411-455 里每个 assistantMessageEvent 都带
//     `partial: AssistantMessage`(累积快照),而 rpc.md:992-993 明写
//     "message_update intentionally omits the former cumulative message field and
//     assistantMessageEvent.partial"。照 .d.ts 写会去读一个线上根本不存在的字段
//   - pi-ai 的 types.d.ts:442 的 toolcall_start 只有 contentIndex,而 rpc.md:990 的
//     线上示例带 id 与 toolName(rpc 层补的)。规格「解析器后端差异」表的实测结论
//     与 rpc.md 一致,故按 rpc.md
type piRawEvent struct {
	Type string `json:"type"`

	// message_update 的载荷
	AssistantMessageEvent *piAssistantMessageEvent `json:"assistantMessageEvent"`

	// message_start / message_end / turn_end 的载荷
	Message *piMessage `json:"message"`

	// type=response 的字段
	Command string       `json:"command"`
	Success *bool        `json:"success"`
	Error   string       `json:"error"`
	Data    *piStateData `json:"data"`

	// type=extension_error 的字段
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
}

// piAssistantMessageEvent 是 message_update 里的增量事件。
type piAssistantMessageEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`    // text_delta / thinking_delta / toolcall_delta
	Content      string `json:"content"`  // text_end / thinking_end 携带完整内容,**必须忽略**
	ID           string `json:"id"`       // toolcall_start
	ToolName     string `json:"toolName"` // toolcall_start
}

// piMessage 是 pi 的 AgentMessage(rpc.md:929-936 的 message 字段)。
type piMessage struct {
	Role    string           `json:"role"`
	Content []piContentBlock `json:"content"`
}

// piContentBlock 是 pi 的内容块。形状取自 @earendil-works/pi-ai 的
// types.d.ts:237-264(TextContent / ThinkingContent / ToolCall),
// AssistantMessage.content 是这三者的联合(types.d.ts:307-312)。
//
// **与 Claude 的块类型名不同,必须翻译**(见 convertPiMessage):
// pi 用 "toolCall" 且入参字段叫 arguments(是**对象**),Claude 用 "tool_use"
// 且字段叫 input;pi 的 thinking 内容在 thinking 字段,Claude 在 thinking 字段
// 但归一化后统一放 Text。
type piContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`      // type=text
	Thinking  string          `json:"thinking"`  // type=thinking
	ID        string          `json:"id"`        // type=toolCall
	Name      string          `json:"name"`      // type=toolCall
	Arguments json.RawMessage `json:"arguments"` // type=toolCall,Record<string, any>
}

// piStateData 是 get_state 响应里我们用得到的那两个字段(rpc.md:196-215)。
type piStateData struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
}

// ParseLine 把一行 pi JSONL 解析为归一化 StreamEvent。ok=false 表示该行应被跳过。
//
// 映射规则见各 case 的注释。**未知事件类型一律静默跳过而不报错** —— pi 的协议有
// 破坏性变更历史(rpc.md 记载 message_update 曾移除累积 message 与
// assistantMessageEvent.partial,即风险登记 R2),报错会让一次 pi 升级变成整条链路
// 失败。同理,下列事件全部有意忽略:
//
//	agent_start、turn_start、turn_end、message_start、
//	thinking_start/thinking_delta/thinking_end、queue_update、
//	compaction_start/compaction_end、auto_retry_start/auto_retry_end、
//	summarization_retry_*、bash_execution_update、
//	tool_execution_start/tool_execution_update/tool_execution_end(见 D5)、
//	extension_ui_request(实测抓到:pi-subagents 会主动推 setWidget)
//
// 以及 prompt / abort 等命令自己的成功 response(见 parseResponse)。
func (p *PiProtocol) ParseLine(line []byte) (StreamEvent, bool) {
	// JSONL framing:上层按 \n 切分;这里只容忍并剥除行尾 \r。
	// 不得改用会按 U+2028/U+2029 切分的通用行读取器(rpc.md 明确警告),
	// Go 的 bufio.ScanLines 已合规。
	line = bytes.TrimRight(line, "\r")
	if len(bytes.TrimSpace(line)) == 0 {
		return StreamEvent{}, false
	}

	var raw piRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return StreamEvent{}, false
	}

	switch raw.Type {
	case "message_update":
		return parsePiMessageUpdate(raw.AssistantMessageEvent)

	case "message_end":
		// **必须**过滤 role:pi 给 user 消息也发 message_start/message_end,
		// 且 content[0].text 就是我们发出的 prompt 原文(规格实测约束 2)。
		// 不过滤的话,用户自己的提问会被当作助手回复推回前端。
		if raw.Message == nil || raw.Message.Role != "assistant" {
			return StreamEvent{}, false
		}
		msg := convertPiMessage(raw.Message)
		evt := StreamEvent{Type: "assistant", Message: msg}
		// 与 ClaudeProtocol 对齐:Content 取第一个非空 text 块。
		// StreamProcessor 走的是 Message,但 query_pool 等上层会读 Content。
		for _, b := range msg.Content {
			if b.Type == "text" && b.Text != "" {
				evt.Content = b.Text
				break
			}
		}
		return evt, true

	case "agent_settled":
		// 轮次结束 → 既有 result 语义 → StreamProcessor 发 done。
		// **不是 agent_end**:rpc.md:893 说 agent_end 之后 "may still be followed by
		// retry, compaction, or queued continuations",而 :905-911 说 agent_settled
		// 才是 "no automatic retry, compaction retry, or queued continuation remains"。
		// 用 agent_end 会在自动重试/压缩时提前发 done。
		return StreamEvent{Type: "result"}, true

	case "response":
		return parsePiResponse(raw)

	case "extension_error":
		// 沙箱 extension 抛错即 block(fail-safe),但那条 block 不会以本事件形式
		// 到达;本事件是扩展**自己**抛错。转成 error 让前端看到,而不是静默卡住。
		msg := raw.Error
		if msg == "" {
			msg = "extension error"
		}
		if raw.ExtensionPath != "" || raw.Event != "" {
			msg = "extension error (" + raw.ExtensionPath + ", event=" + raw.Event + "): " + raw.Error
		}
		return StreamEvent{Type: "error", Error: msg}, true
	}

	return StreamEvent{}, false
}

// parsePiMessageUpdate 把 message_update 里的 assistantMessageEvent 归一化为 Delta。
//
// 只对 text_delta 与 toolcall_* 产出 Delta。text_end 携带完整 content,**必须忽略**
// (规格实测约束 3:同一段文本在一轮里出现三次 —— text_delta 增量、text_end.content
// 完整、message_end.message.content[] 完整且权威;放行任何一份完整内容都会与增量
// 重复,前端会看到双份文本)。thinking_* 一律忽略,与既有
// TestExtractTextDelta_ThinkingIgnored 的行为一致。
func parsePiMessageUpdate(ame *piAssistantMessageEvent) (StreamEvent, bool) {
	if ame == nil {
		return StreamEvent{}, false
	}

	switch ame.Type {
	case "text_delta":
		// 空增量守卫:Process 对 DeltaText 的空串有二次守卫,但保持与
		// toolcall_delta 一致地在此拦住,避免依赖下游行为。
		if ame.Delta == "" {
			return StreamEvent{}, false
		}
		return StreamEvent{Delta: &Delta{
			Kind:  DeltaText,
			Index: ame.ContentIndex,
			Text:  ame.Delta,
		}}, true

	case "toolcall_start":
		// 与 ClaudeProtocol 的 content_block_start 对齐:ID 或 Name 为空时不产出,
		// 否则会下发一个字段为空的 tool_start。
		if ame.ID == "" || ame.ToolName == "" {
			return StreamEvent{}, false
		}
		return StreamEvent{Delta: &Delta{
			Kind:     DeltaToolStart,
			Index:    ame.ContentIndex,
			ToolID:   ame.ID,
			ToolName: ame.ToolName,
		}}, true

	case "toolcall_delta":
		// **空增量守卫(承 main 的 89c3862,pi 侧不可重犯 claude 侧曾有的疏漏)**:
		// Process(claude/stream.go)对空 ToolInput **没有**二次守卫,放行就会
		// tool.input += "" 然后下发一个携带上一条累积输入的重复 tool_input 事件。
		if ame.Delta == "" {
			return StreamEvent{}, false
		}
		return StreamEvent{Delta: &Delta{
			Kind:      DeltaToolInput,
			Index:     ame.ContentIndex,
			ToolInput: ame.Delta,
		}}, true

	case "toolcall_end":
		// 见 D5:tool_execution_end **不**映射到这里。
		return StreamEvent{Delta: &Delta{
			Kind:  DeltaToolEnd,
			Index: ame.ContentIndex,
		}}, true
	}

	// text_start / text_end / thinking_start / thinking_delta / thinking_end /
	// 以及未来 pi 新增的任何 assistantMessageEvent 类型:静默跳过
	return StreamEvent{}, false
}

// parsePiResponse 处理 type=response。
//
// 三种去向:
//  1. get_state 且成功 → 归一化成 system/init(计划 D1),使上层既有的 waitForInit、
//     onSessionID 别名注册、local-<UnixNano> fallback、onResumeFailed 降级链
//     一行都不用改,claude 包也就无需知道后端差异
//  2. 任何命令失败(success=false)→ error
//  3. 其他成功响应(prompt / abort / ...)→ **跳过**
//
// 第 3 条是硬约束:prompt 自己的 response{success:true} 只表示「已接受」,
// 而且它**先于所有事件到达**(规格实测:response 在 +3.01s,首个 message_update
// 在 +4.16s)。把它当完成信号会在模型还没开口时就给前端发 done。
func parsePiResponse(raw piRawEvent) (StreamEvent, bool) {
	failed := raw.Success != nil && !*raw.Success
	if failed {
		msg := raw.Error
		if msg == "" {
			msg = "pi command failed: " + raw.Command
		}
		return StreamEvent{Type: "error", Error: msg}, true
	}

	if raw.Command != "get_state" {
		return StreamEvent{}, false
	}

	// get_state 成功。优先 sessionId,退回 sessionFile —— pi 的 --session 接受
	// 「会话文件路径或 UUID 前缀」(rpc.md:278-279),两者都能用于 resume。
	id := ""
	if raw.Data != nil {
		id = raw.Data.SessionID
		if id == "" {
			id = raw.Data.SessionFile
		}
	}
	// 拿不到任何 ID 时**不要**产出 system/init:上层 onSessionID 会用它注册别名并
	// 写进 DB,空值会污染那条链。让既有的 local-<UnixNano> fallback 兜住。
	if id == "" {
		return StreamEvent{}, false
	}
	return StreamEvent{Type: "system", Subtype: "init", SessionID: id}, true
}

// convertPiMessage 把 pi 的 AgentMessage 翻译成归一化的 Message。
//
// 这一步是**必需的**,不是可选的规整:pi 的块类型名与字段名和 Claude 不同,而
// claude 包的 ExtractToolUseFromAssistantMsg 硬编码判 block.Type == "tool_use"、
// ExtractAssistantContentFromMsg 判 "text"。直接把 pi 的 message 塞进去会让
// 工具块被静默丢弃(SSE 重连后前端永远收不到 tool_start)。
//
// 翻译表:
//
//	{text, text}                 → {Type:"text",     Text: text}
//	{thinking, thinking}         → {Type:"thinking", Text: thinking}
//	{toolCall, id, name, args}   → {Type:"tool_use", ID, Name, Input: args}
//
// arguments 在 pi 侧是 Record<string, any>(对象),归一化的 ContentBlock.Input 是
// json.RawMessage,所以直接透传原始 JSON 即可 —— Process 用 string(tool.Input)
// 填 SSE 的 toolInput 字段,前端拿到的仍是一段 JSON 文本。
func convertPiMessage(msg *piMessage) *Message {
	out := &Message{Role: msg.Role}
	if len(msg.Content) == 0 {
		return out
	}
	out.Content = make([]ContentBlock, 0, len(msg.Content))
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			out.Content = append(out.Content, ContentBlock{Type: "text", Text: b.Text})
		case "thinking":
			out.Content = append(out.Content, ContentBlock{Type: "thinking", Text: b.Thinking})
		case "toolCall":
			out.Content = append(out.Content, ContentBlock{
				Type:  "tool_use",
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Arguments,
			})
		default:
			// 未知块类型:保留类型名与文本(若有),不猜测语义
			out.Content = append(out.Content, ContentBlock{Type: b.Type, Text: b.Text})
		}
	}
	return out
}

// 编译期断言:PiProtocol 至此满足 Protocol(旗标/env/编码/解析/探测五组齐全)。
var _ Protocol = (*PiProtocol)(nil)
