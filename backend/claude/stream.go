package claude

import (
	"encoding/json"
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

// ToolUseStart represents the start of a tool use block.
type ToolUseStart struct {
	ID    string
	Name  string
	Index int
}

// ContentBlockStop represents a content_block_stop event.
type ContentBlockStop struct {
	Index int
}

// StreamProcessor converts raw StreamEvents (from Claude CLI NDJSON output)
// into clean SSEEvents for the frontend. It handles:
//   - stream_event → delta/tool_start/tool_input/tool_end (Claude/Qwen streaming)
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
func (sp *StreamProcessor) MarkAsStreamedWithContent(content string) {
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
	switch evt.Type {
	case "stream_event":
		if toolStart := ExtractToolUseStart(evt.Event); toolStart != nil {
			sp.activeTools[toolStart.Index] = &activeTool{
				id:   toolStart.ID,
				name: toolStart.Name,
			}
			sp.sentToolIDs[toolStart.ID] = true
			return SSEEvent{
				Type:     "tool_start",
				ToolID:   toolStart.ID,
				ToolName: toolStart.Name,
			}
		}

		if inputDelta := ExtractToolUseInputDelta(evt.Event); inputDelta != "" {
			var event struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal(evt.Event, &event); err == nil {
				if tool, ok := sp.activeTools[event.Index]; ok {
					tool.input += inputDelta
					return SSEEvent{
						Type:      "tool_input",
						ToolID:    tool.id,
						ToolInput: tool.input,
					}
				}
			}
			return SSEEvent{}
		}

		if stopEvent := ExtractContentBlockStop(evt.Event); stopEvent != nil {
			if tool, ok := sp.activeTools[stopEvent.Index]; ok {
				toolID := tool.id
				delete(sp.activeTools, stopEvent.Index)
				return SSEEvent{Type: "tool_end", ToolID: toolID}
			}
			return SSEEvent{}
		}

		delta := ExtractTextDelta(evt.Event)
		if delta != "" {
			sp.streamedDeltas = true
			return SSEEvent{Type: "delta", Delta: delta}
		}
		return SSEEvent{}

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
		toolBlocks := ExtractToolUseFromAssistant(evt.Event)
		if evt.Message != nil {
			toolBlocks = ExtractToolUseFromAssistantMsg(evt.Message)
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

		// assistant with raw Event field (no parsed Message)
		content := ExtractAssistantContent(evt.Event)
		if content != "" && !sp.streamedDeltas {
			return SSEEvent{Type: "full", Content: content}
		}
		if ev := sp.checkSSEReconnectExtension(content); ev.Type != "" {
			return ev
		}
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

// --- Extract functions for stream_event sub-events (from Event json.RawMessage) ---

// ExtractTextDelta extracts text delta from a stream_event payload.
// Only extracts text_delta; thinking_delta is explicitly ignored.
func ExtractTextDelta(eventRaw json.RawMessage) string {
	if eventRaw == nil {
		return ""
	}
	var event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		return ""
	}
	if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
		return event.Delta.Text
	}
	return ""
}

// ExtractToolUseStart extracts tool use start info from content_block_start event.
func ExtractToolUseStart(eventRaw json.RawMessage) *ToolUseStart {
	if eventRaw == nil {
		return nil
	}
	var event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		return nil
	}
	if event.Type == "content_block_start" && event.ContentBlock.Type == "tool_use" {
		if event.ContentBlock.ID == "" || event.ContentBlock.Name == "" {
			return nil
		}
		return &ToolUseStart{
			ID:    event.ContentBlock.ID,
			Name:  event.ContentBlock.Name,
			Index: event.Index,
		}
	}
	return nil
}

// ExtractToolUseInputDelta extracts partial JSON input from input_json_delta event.
func ExtractToolUseInputDelta(eventRaw json.RawMessage) string {
	if eventRaw == nil {
		return ""
	}
	var event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		return ""
	}
	if event.Type == "content_block_delta" && event.Delta.Type == "input_json_delta" {
		return event.Delta.PartialJSON
	}
	return ""
}

// ExtractContentBlockStop extracts block stop info from content_block_stop event.
func ExtractContentBlockStop(eventRaw json.RawMessage) *ContentBlockStop {
	if eventRaw == nil {
		return nil
	}
	var event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		return nil
	}
	if event.Type == "content_block_stop" {
		return &ContentBlockStop{Index: event.Index}
	}
	return nil
}

// --- Extract functions for assistant message (from Message or Event raw) ---

// ExtractAssistantContent extracts concatenated text blocks from a raw assistant message.
func ExtractAssistantContent(msgRaw json.RawMessage) string {
	if msgRaw == nil {
		return ""
	}
	var msg struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

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

// ExtractToolUseFromAssistant extracts tool_use blocks from raw assistant message.
func ExtractToolUseFromAssistant(msgRaw json.RawMessage) []ToolUseBlock {
	if msgRaw == nil {
		return nil
	}
	var msg struct {
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
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