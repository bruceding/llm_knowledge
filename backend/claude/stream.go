package claude

import (
	"encoding/json"
	"llm-knowledge/agent"
	"strings"
)

// SSEEvent is the clean event type sent to frontend via SSE.
// StreamProcessor converts raw Claude CLI events into this format.
// JSON tags match the wire format that SSE handlers send via echo.Map.
type SSEEvent struct {
	Type      string `json:"type"`       // delta, full, done, tool_start, tool_input, tool_end, error
	Delta     string `json:"text"`       // text delta (for "delta") — wire name is "text", not "delta"
	Content   string `json:"content"`    // full content (for "full" or "error")
	ToolID    string `json:"toolId"`     // tool use block ID (for "tool_start", "tool_end")
	ToolName  string `json:"toolName"`   // tool name (for "tool_start")
	ToolInput string `json:"toolInput"`  // accumulated tool input JSON (for "tool_input")
}

// activeTool tracks an in-progress tool use block
type activeTool struct {
	id    string
	name  string
	input string // accumulated JSON input
}

// ToolUseBlock represents a tool_use block in an assistant message.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// StreamProcessor converts raw StreamEvents (from Claude CLI NDJSON output)
// into clean SSEEvents for the frontend. It handles:
//   - Delta → delta/tool_start/tool_input/tool_end(与 Type 无关,优先分派)
//   - assistant → full (GLM non-streaming) or de-duplicated skip (Qwen mixed)
//   - result → done (explicit turn-end signal)
//   - system → filtered out
//
// Three de-duplication mechanisms:
//   - streamedDeltas: prevents Qwen from sending duplicate assistant after deltas
//   - sseReconnectContent: handles GLM SSE reconnect content extension
//   - sentToolIDs: prevents duplicate tool_start on SSE reconnect
type StreamProcessor struct {
	streamedDeltas      bool
	sseReconnectContent string
	activeTools         map[int]*activeTool
	sentToolIDs         map[string]bool
	pendingToolEvents   []ToolUseBlock
	pendingToolIndex    int
	pendingAssistantContent string
}

// NewStreamProcessor creates a ready-to-use StreamProcessor.
func NewStreamProcessor() *StreamProcessor {
	return &StreamProcessor{
		activeTools:    make(map[int]*activeTool),
		sentToolIDs:    make(map[string]bool),
	}
}

// Reset clears state for a new streaming turn.
func (sp *StreamProcessor) Reset() {
	sp.streamedDeltas = false
	sp.sseReconnectContent = ""
	sp.activeTools = make(map[int]*activeTool)
	sp.sentToolIDs = make(map[string]bool)
	sp.pendingToolEvents = nil
	sp.pendingToolIndex = 0
	sp.pendingAssistantContent = ""
}

// MarkAsStreamedWithContent sets streamedDeltas=true and records content already
// sent to subscriber (e.g. via SSE reconnect). For non-streaming models (GLM),
// the full assistant message may extend this content; the processor emits a full
// event to replace partial reconnect content.
//
// No-op when content is empty: a reconnect after the prior turn already finished
// has nothing to dedupe against, and falsely setting streamedDeltas would cause
// the next turn's assistant event (short replies that skip stream_event deltas)
// to be silently dropped. See issue #58.
func (sp *StreamProcessor) MarkAsStreamedWithContent(content string) {
	if content == "" {
		return
	}
	sp.streamedDeltas = true
	sp.sseReconnectContent = content
}

// checkSSEReconnectExtension checks if new content extends sseReconnectContent.
// If reconnect content is a prefix of new content, emits a full event for replacement.
func (sp *StreamProcessor) checkSSEReconnectExtension(content string) SSEEvent {
	if content != "" && sp.sseReconnectContent != "" && strings.HasPrefix(content, sp.sseReconnectContent) {
		sp.sseReconnectContent = ""
		return SSEEvent{Type: "full", Content: content}
	}
	return SSEEvent{}
}

// HasPendingEvents returns true if there are pending tool or content events to emit.
func (sp *StreamProcessor) HasPendingEvents() bool {
	return len(sp.pendingToolEvents) > 0 && sp.pendingToolIndex < len(sp.pendingToolEvents) ||
		sp.pendingAssistantContent != ""
}

// FlushPending processes pending tool events and content from an assistant message.
// Call this after Process() returns a tool_start from an assistant message,
// until it returns an empty event.
func (sp *StreamProcessor) FlushPending() SSEEvent {
	if len(sp.pendingToolEvents) > 0 && sp.pendingToolIndex < len(sp.pendingToolEvents) {
		tool := sp.pendingToolEvents[sp.pendingToolIndex]
		sp.pendingToolIndex++
		sp.sentToolIDs[tool.ID] = true
		return SSEEvent{
			Type:      "tool_start",
			ToolID:    tool.ID,
			ToolName:  tool.Name,
			ToolInput: string(tool.Input),
		}
	}

	if sp.pendingAssistantContent != "" && sp.pendingToolIndex >= len(sp.pendingToolEvents) {
		content := sp.pendingAssistantContent
		sp.pendingAssistantContent = ""
		sp.pendingToolEvents = nil
		sp.pendingToolIndex = 0
		if content != "" && !sp.streamedDeltas {
			return SSEEvent{Type: "full", Content: content}
		}
		if ev := sp.checkSSEReconnectExtension(content); ev.Type != "" {
			return ev
		}
	}

	return SSEEvent{}
}

// Process converts a raw StreamEvent into a clean SSEEvent.
// Returns empty SSEEvent for filtered/skipped events (system, duplicate assistant).
func (sp *StreamProcessor) Process(evt StreamEvent) SSEEvent {
	// Delta 与 Type 是正交的两个维度(pi 后端的 Type 取值与 Claude 不同),
	// 所以归一化增量的处理放在 switch evt.Type 之前。
	if evt.Delta != nil {
		switch evt.Delta.Kind {
		case agent.DeltaToolStart:
			sp.activeTools[evt.Delta.Index] = &activeTool{
				id:   evt.Delta.ToolID,
				name: evt.Delta.ToolName,
			}
			sp.sentToolIDs[evt.Delta.ToolID] = true
			return SSEEvent{
				Type:     "tool_start",
				ToolID:   evt.Delta.ToolID,
				ToolName: evt.Delta.ToolName,
			}

		case agent.DeltaToolInput:
			if tool, ok := sp.activeTools[evt.Delta.Index]; ok {
				tool.input += evt.Delta.ToolInput
				return SSEEvent{
					Type:      "tool_input",
					ToolID:    tool.id,
					ToolName:  tool.name,
					ToolInput: tool.input,
				}
			}
			return SSEEvent{}

		case agent.DeltaToolEnd:
			if tool, ok := sp.activeTools[evt.Delta.Index]; ok {
				toolID := tool.id
				delete(sp.activeTools, evt.Delta.Index)
				return SSEEvent{Type: "tool_end", ToolID: toolID}
			}
			return SSEEvent{}

		case agent.DeltaText:
			if evt.Delta.Text == "" {
				return SSEEvent{}
			}
			sp.streamedDeltas = true
			return SSEEvent{Type: "delta", Delta: evt.Delta.Text}
		}
	}

	switch evt.Type {
	case "assistant":
		// Pending tool events from previous call
		if len(sp.pendingToolEvents) > 0 && sp.pendingToolIndex < len(sp.pendingToolEvents) {
			tool := sp.pendingToolEvents[sp.pendingToolIndex]
			sp.pendingToolIndex++
			sp.sentToolIDs[tool.ID] = true
			return SSEEvent{
				Type:      "tool_start",
				ToolID:    tool.ID,
				ToolName:  tool.Name,
				ToolInput: string(tool.Input),
			}
		}

		// Pending content from previous call
		if sp.pendingAssistantContent != "" && sp.pendingToolIndex >= len(sp.pendingToolEvents) {
			content := sp.pendingAssistantContent
			sp.pendingAssistantContent = ""
			sp.pendingToolEvents = nil
			sp.pendingToolIndex = 0
			if content != "" && !sp.streamedDeltas {
				return SSEEvent{Type: "full", Content: content}
			}
			if ev := sp.checkSSEReconnectExtension(content); ev.Type != "" {
				return ev
			}
			return SSEEvent{}
		}

		// First time processing this assistant message
		if evt.Message != nil {
			toolBlocks := ExtractToolUseFromAssistantMsg(evt.Message)
			content := ExtractAssistantContentFromMsg(evt.Message)
			if len(toolBlocks) > 0 {
				var newBlocks []ToolUseBlock
				for _, block := range toolBlocks {
					if !sp.sentToolIDs[block.ID] {
						newBlocks = append(newBlocks, block)
					}
				}
				sp.pendingToolEvents = newBlocks
				sp.pendingAssistantContent = content
				sp.pendingToolIndex = 0

				if len(newBlocks) > 0 {
					tool := newBlocks[0]
					sp.pendingToolIndex = 1
					sp.sentToolIDs[tool.ID] = true
					return SSEEvent{
						Type:      "tool_start",
						ToolID:    tool.ID,
						ToolName:  tool.Name,
						ToolInput: string(tool.Input),
					}
				}
			}

			if content != "" && !sp.streamedDeltas {
				return SSEEvent{Type: "full", Content: content}
			}
			if ev := sp.checkSSEReconnectExtension(content); ev.Type != "" {
				return ev
			}
			return SSEEvent{}
		}

		// evt.Message 为 nil 时无内容可下发。
		// (原先此处会回退去挖 raw Event,但生产代码只在 stream_event 行填充 Event,
		// assistant 行必然已解析出 Message;测试也只覆盖 *FromMsg 变体。属死路径,
		// 随 StreamEvent.Event 字段一并移除。)
		return SSEEvent{}

	case "result":
		sp.streamedDeltas = false
		sp.sseReconnectContent = ""
		sp.activeTools = make(map[int]*activeTool)
		return SSEEvent{Type: "done"}

	case "error":
		return SSEEvent{Type: "error", Content: evt.Error}

	case "system":
		// Filter out system events (init, hooks, etc.)
		return SSEEvent{}

	default:
		return SSEEvent{}
	}
}

// --- Extract functions for assistant message (from parsed *Message) ---

// ExtractAssistantContentFromMsg extracts text from a parsed Message struct.
func ExtractAssistantContentFromMsg(msg *Message) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != "" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// ExtractToolUseFromAssistantMsg extracts tool_use blocks from a parsed Message struct.
func ExtractToolUseFromAssistantMsg(msg *Message) []ToolUseBlock {
	if msg == nil {
		return nil
	}
	var blocks []ToolUseBlock
	for _, block := range msg.Content {
		if block.Type == "tool_use" && block.ID != "" && block.Name != "" {
			blocks = append(blocks, ToolUseBlock{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}
