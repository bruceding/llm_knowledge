package claude

import (
	"encoding/json"
	"fmt"
	"testing"
)

// --- SSEEvent JSON tag tests ---

func TestSSEEvent_JSONTags(t *testing.T) {
	evt := SSEEvent{
		Type:      "delta",
		Delta:     "hello",
		Content:   "full text",
		ToolID:    "tool-123",
		ToolName:  "Read",
		ToolInput: `{"file_path":"/test"}`,
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)

	// Verify JSON field names match wire format (what frontend expects)
	if v := m["type"]; v != "delta" {
		t.Errorf("expected type=delta, got %v", v)
	}
	if v := m["text"]; v != "hello" {
		t.Errorf("expected text=hello (Delta field with json:'text' tag), got %v", v)
	}
	if v := m["content"]; v != "full text" {
		t.Errorf("expected content='full text', got %v", v)
	}
	if v := m["toolId"]; v != "tool-123" {
		t.Errorf("expected toolId='tool-123', got %v", v)
	}
	if v := m["toolName"]; v != "Read" {
		t.Errorf("expected toolName=Read, got %v", v)
	}
	if v := m["toolInput"]; v != `{"file_path":"/test"}` {
		t.Errorf("expected toolInput json, got %v", v)
	}
}

// --- Claude streaming model tests (stream_event deltas) ---

func TestStreamProcessor_ClaudeStreaming_Delta(t *testing.T) {
	sp := NewStreamProcessor()
	evt := makeStreamEventDelta("Hello")
	result := sp.Process(evt)

	if result.Type != "delta" {
		t.Fatalf("expected delta, got %s", result.Type)
	}
	if result.Delta != "Hello" {
		t.Errorf("expected Delta='Hello', got %q", result.Delta)
	}
	if !sp.streamedDeltas {
		t.Error("streamedDeltas should be true after delta event")
	}
}

func TestStreamProcessor_ClaudeStreaming_AssistantSkipped(t *testing.T) {
	sp := NewStreamProcessor()

	// First: receive stream_event deltas
	sp.Process(makeStreamEventDelta("Hello"))
	sp.Process(makeStreamEventDelta(" world"))

	// Then: receive assistant event (duplicate of streamed content)
	result := sp.Process(makeAssistantEvent("Hello world"))

	if result.Type != "" {
		t.Errorf("assistant should be skipped when streamedDeltas=true, got type=%s", result.Type)
	}
}

func TestStreamProcessor_ClaudeStreaming_ResultToDone(t *testing.T) {
	sp := NewStreamProcessor()

	sp.Process(makeStreamEventDelta("Hi"))
	sp.Process(makeAssistantEvent("Hi"))

	result := sp.Process(makeResultEvent("Hi"))
	if result.Type != "done" {
		t.Fatalf("expected done, got %s", result.Type)
	}

	// After done, sp.Reset() should be called externally — verify state is still dirty
	if sp.streamedDeltas != false {
		// Reset hasn't been called yet (done handler should call Reset)
		// This is expected — the SSE handler calls sp.Reset() after emitting done
	}
}

func TestStreamProcessor_ClaudeStreaming_SystemFiltered(t *testing.T) {
	sp := NewStreamProcessor()
	result := sp.Process(StreamEvent{Type: "system", Subtype: "init", SessionID: "abc"})
	if result.Type != "" {
		t.Errorf("system events should be filtered, got type=%s", result.Type)
	}
}

func TestStreamProcessor_ClaudeStreaming_FullTurn(t *testing.T) {
	sp := NewStreamProcessor()

	// system.init — filtered
	result := sp.Process(StreamEvent{Type: "system", Subtype: "init"})
	if result.Type != "" {
		t.Errorf("system should be filtered")
	}

	// stream_event deltas
	result = sp.Process(makeStreamEventDelta("Hello"))
	if result.Type != "delta" || result.Delta != "Hello" {
		t.Errorf("expected delta='Hello', got type=%s delta=%q", result.Type, result.Delta)
	}

	result = sp.Process(makeStreamEventDelta(" world"))
	if result.Type != "delta" || result.Delta != " world" {
		t.Errorf("expected delta=' world'")
	}

	// assistant — skipped (streamedDeltas=true)
	result = sp.Process(makeAssistantEvent("Hello world"))
	if result.Type != "" {
		t.Errorf("assistant should be skipped, got %s", result.Type)
	}

	// result → done
	result = sp.Process(makeResultEvent("Hello world"))
	if result.Type != "done" {
		t.Fatalf("expected done, got %s", result.Type)
	}
}

// --- GLM non-streaming model tests (no stream_event, only assistant) ---

func TestStreamProcessor_GLM_AssistantFull(t *testing.T) {
	sp := NewStreamProcessor()

	// GLM: no stream_event deltas, only assistant event with full content
	result := sp.Process(makeAssistantEvent("Complete response"))
	if result.Type != "full" {
		t.Fatalf("expected full for non-streaming model, got %s", result.Type)
	}
	if result.Content != "Complete response" {
		t.Errorf("expected Content='Complete response', got %q", result.Content)
	}
}

func TestStreamProcessor_GLM_ResultToDone(t *testing.T) {
	sp := NewStreamProcessor()

	sp.Process(makeAssistantEvent("Reply"))
	result := sp.Process(makeResultEvent("Reply"))
	if result.Type != "done" {
		t.Fatalf("expected done, got %s", result.Type)
	}
}

// --- Qwen mixed model tests (deltas + duplicate assistant) ---

func TestStreamProcessor_Qwen_DeltasThenDuplicateAssistant(t *testing.T) {
	sp := NewStreamProcessor()

	// Deltas arrive first
	sp.Process(makeStreamEventDelta("Partial"))
	sp.Process(makeStreamEventDelta(" response"))

	// Duplicate assistant event (same content) — should be skipped
	result := sp.Process(makeAssistantEvent("Partial response"))
	if result.Type != "" {
		t.Errorf("duplicate assistant should be skipped when streamedDeltas=true, got %s", result.Type)
	}

	// result → done
	result = sp.Process(makeResultEvent("Partial response"))
	if result.Type != "done" {
		t.Fatalf("expected done, got %s", result.Type)
	}
}

// --- SSE reconnect tests ---

func TestStreamProcessor_SSEReconnect_Extension(t *testing.T) {
	sp := NewStreamProcessor()
	sp.MarkAsStreamedWithContent("Partial") // simulate reconnect with partial content

	// GLM sends full assistant that extends the partial content
	result := sp.Process(makeAssistantEvent("Partial response"))
	if result.Type != "full" {
		t.Fatalf("expected full (reconnect extension), got %s", result.Type)
	}
	if result.Content != "Partial response" {
		t.Errorf("expected extended content 'Partial response', got %q", result.Content)
	}
}

func TestStreamProcessor_SSEReconnect_NoPrefixMatch(t *testing.T) {
	// Realistic case after a completed prior turn + Reset: the next assistant
	// event (no deltas, GLM-style) should emit a full event.
	sp := NewStreamProcessor()

	// Previous turn completed — Reset was called
	sp.Reset()

	// New turn: fresh assistant (no deltas, GLM-style)
	result := sp.Process(makeAssistantEvent("New response"))
	if result.Type != "full" {
		t.Fatalf("expected full for fresh turn after Reset, got %s", result.Type)
	}
	if result.Content != "New response" {
		t.Errorf("expected 'New response', got %q", result.Content)
	}
}

// Regression: docchat Reconnect handler unconditionally calls
// MarkAsStreamedWithContent(session.StreamingContent()). When the prior turn
// already completed cleanly, StreamingContent() is "". With the old logic,
// streamedDeltas was set to true anyway, causing the NEXT turn's assistant
// event (the common short-reply path that skips stream_event deltas) to be
// silently dropped — frontend renders an empty bubble. See issue #58.
func TestStreamProcessor_MarkAsStreamedWithEmptyContent_DoesNotSuppressNextTurn(t *testing.T) {
	sp := NewStreamProcessor()
	sp.MarkAsStreamedWithContent("") // reconnect with no in-flight content

	result := sp.Process(makeAssistantEvent("Hi there!"))
	if result.Type != "full" {
		t.Fatalf("expected full for fresh assistant after empty-content reconnect, got %q", result.Type)
	}
	if result.Content != "Hi there!" {
		t.Errorf("expected 'Hi there!', got %q", result.Content)
	}
}

func TestStreamProcessor_SSEReconnect_StreamingAfterReconnect(t *testing.T) {
	sp := NewStreamProcessor()
	sp.MarkAsStreamedWithContent("Already seen")

	// After reconnect, new deltas arrive — they should be emitted normally
	result := sp.Process(makeStreamEventDelta(" new"))
	if result.Type != "delta" {
		t.Fatalf("expected delta after reconnect, got %s", result.Type)
	}
	if result.Delta != " new" {
		t.Errorf("expected delta=' new', got %q", result.Delta)
	}

	// assistant with content extending reconnect — should emit full replacement
	result = sp.Process(makeAssistantEvent("Already seen new"))
	if result.Type != "full" {
		t.Fatalf("expected full (reconnect extension after new deltas), got %s", result.Type)
	}
}

// --- Tool use tests ---

func TestStreamProcessor_ToolStartFromStreamEvent(t *testing.T) {
	sp := NewStreamProcessor()
	evt := makeToolStartStreamEvent(0, "tool-1", "Read")
	result := sp.Process(evt)

	if result.Type != "tool_start" {
		t.Fatalf("expected tool_start, got %s", result.Type)
	}
	if result.ToolID != "tool-1" {
		t.Errorf("expected ToolID='tool-1', got %q", result.ToolID)
	}
	if result.ToolName != "Read" {
		t.Errorf("expected ToolName=Read, got %q", result.ToolName)
	}
}

func TestStreamProcessor_ToolInputDelta(t *testing.T) {
	sp := NewStreamProcessor()

	// Start tool
	sp.Process(makeToolStartStreamEvent(0, "tool-1", "Read"))

	// Input delta
	result := sp.Process(makeToolInputDeltaStreamEvent(0, `{"file_pa`))
	if result.Type != "tool_input" {
		t.Fatalf("expected tool_input, got %s", result.Type)
	}
	if result.ToolID != "tool-1" {
		t.Errorf("expected ToolID='tool-1', got %q", result.ToolID)
	}

	// More input
	result = sp.Process(makeToolInputDeltaStreamEvent(0, `th":"/test"}`))
	if result.Type != "tool_input" {
		t.Fatalf("expected tool_input, got %s", result.Type)
	}
}

func TestStreamProcessor_ToolEndFromStreamEvent(t *testing.T) {
	sp := NewStreamProcessor()
	sp.Process(makeToolStartStreamEvent(0, "tool-1", "Read"))

	result := sp.Process(makeContentBlockStopStreamEvent(0))
	if result.Type != "tool_end" {
		t.Fatalf("expected tool_end, got %s", result.Type)
	}
	if result.ToolID != "tool-1" {
		t.Errorf("expected ToolID='tool-1', got %q", result.ToolID)
	}
}

func TestStreamProcessor_ToolFromAssistantMessage(t *testing.T) {
	sp := NewStreamProcessor()

	// Assistant message with tool_use blocks (non-streaming model)
	msg := &Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "tool_use", ID: "tool-1", Name: "Read", Input: json.RawMessage(`{"file_path":"/test.md"}`)},
		},
	}
	evt := StreamEvent{Type: "assistant", Message: msg}

	result := sp.Process(evt)
	if result.Type != "tool_start" {
		t.Fatalf("expected tool_start from assistant, got %s", result.Type)
	}
	if result.ToolID != "tool-1" || result.ToolName != "Read" {
		t.Errorf("expected tool-1/Read, got %s/%s", result.ToolID, result.ToolName)
	}

	// FlushPending should return empty (only one tool, already emitted)
	pending := sp.FlushPending()
	if pending.Type != "" {
		t.Errorf("expected no pending events after single tool, got type=%s", pending.Type)
	}
}

func TestStreamProcessor_MultipleToolFromAssistant(t *testing.T) {
	sp := NewStreamProcessor()

	msg := &Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "tool_use", ID: "tool-1", Name: "Read", Input: json.RawMessage(`{"file_path":"/a.md"}`)},
			{Type: "tool_use", ID: "tool-2", Name: "Grep", Input: json.RawMessage(`{"pattern":"test"}`)},
		},
	}
	evt := StreamEvent{Type: "assistant", Message: msg}

	// First call emits tool-1
	result := sp.Process(evt)
	if result.Type != "tool_start" || result.ToolID != "tool-1" {
		t.Fatalf("expected tool_start/tool-1, got %s/%s", result.Type, result.ToolID)
	}

	// FlushPending emits tool-2
	pending := sp.FlushPending()
	if pending.Type != "tool_start" || pending.ToolID != "tool-2" {
		t.Fatalf("expected tool_start/tool-2 from FlushPending, got %s/%s", pending.Type, pending.ToolID)
	}
}

// --- Extract function tests ---

func TestExtractTextDelta(t *testing.T) {
	eventRaw := json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	delta := ExtractTextDelta(eventRaw)
	if delta != "Hello" {
		t.Errorf("expected 'Hello', got %q", delta)
	}
}

func TestExtractTextDelta_ThinkingIgnored(t *testing.T) {
	eventRaw := json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","text":"inner thought"}}`)
	delta := ExtractTextDelta(eventRaw)
	if delta != "" {
		t.Errorf("thinking_delta should be ignored, got %q", delta)
	}
}

func TestExtractToolUseStart(t *testing.T) {
	eventRaw := json.RawMessage(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-1","name":"Read"}}`)
	result := ExtractToolUseStart(eventRaw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ID != "tool-1" || result.Name != "Read" || result.Index != 2 {
		t.Errorf("expected tool-1/Read/2, got %s/%s/%d", result.ID, result.Name, result.Index)
	}
}

func TestExtractToolUseInputDelta(t *testing.T) {
	raw := `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"key\":\"val\""}}`
	eventRaw := json.RawMessage(raw)
	delta := ExtractToolUseInputDelta(eventRaw)
	if delta != `{"key":"val"` {
		t.Errorf("expected partial JSON, got %q", delta)
	}
}

func TestExtractContentBlockStop(t *testing.T) {
	eventRaw := json.RawMessage(`{"type":"content_block_stop","index":2}`)
	result := ExtractContentBlockStop(eventRaw)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Index != 2 {
		t.Errorf("expected index=2, got %d", result.Index)
	}
}

func TestExtractAssistantContentFromMsg(t *testing.T) {
	msg := &Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "Hello"},
			{Type: "thinking", Text: "inner"},
			{Type: "text", Text: " world"},
		},
	}
	content := ExtractAssistantContentFromMsg(msg)
	if content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", content)
	}
}

func TestExtractToolUseFromAssistantMsg(t *testing.T) {
	msg := &Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "Let me read"},
			{Type: "tool_use", ID: "tool-1", Name: "Read", Input: json.RawMessage(`{"file_path":"/a"}`)},
			{Type: "tool_use", ID: "tool-2", Name: "Grep", Input: json.RawMessage(`{"pattern":"x"}`)},
		},
	}
	blocks := ExtractToolUseFromAssistantMsg(msg)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 tool blocks, got %d", len(blocks))
	}
	if blocks[0].ID != "tool-1" || blocks[0].Name != "Read" {
		t.Errorf("expected tool-1/Read, got %s/%s", blocks[0].ID, blocks[0].Name)
	}
	if blocks[1].ID != "tool-2" || blocks[1].Name != "Grep" {
		t.Errorf("expected tool-2/Grep, got %s/%s", blocks[1].ID, blocks[1].Name)
	}
}

// --- Reset tests ---

func TestStreamProcessor_Reset(t *testing.T) {
	sp := NewStreamProcessor()
	sp.Process(makeStreamEventDelta("Hi"))
	sp.MarkAsStreamedWithContent("partial")
	sp.Process(makeToolStartStreamEvent(0, "t-1", "Read"))

	sp.Reset()

	if sp.streamedDeltas != false {
		t.Error("streamedDeltas should be false after Reset")
	}
	if sp.sseReconnectContent != "" {
		t.Error("sseReconnectContent should be empty after Reset")
	}
	if len(sp.activeTools) != 0 {
		t.Error("activeTools should be empty after Reset")
	}
	if len(sp.sentToolIDs) != 0 {
		t.Error("sentToolIDs should be empty after Reset")
	}
}

// --- Error event test ---

func TestStreamProcessor_Error(t *testing.T) {
	sp := NewStreamProcessor()
	result := sp.Process(StreamEvent{Type: "error", Error: "something broke"})
	if result.Type != "error" {
		t.Fatalf("expected error, got %s", result.Type)
	}
	if result.Content != "something broke" {
		t.Errorf("expected Content='something broke', got %q", result.Content)
	}
}

// --- Helper functions to create test events ---

func makeStreamEventDelta(text string) StreamEvent {
	raw := json.RawMessage(fmt.Sprintf(
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%s"}}`,
		jsonEscape(text),
	))
	return StreamEvent{Type: "stream_event", Event: raw}
}

func makeAssistantEvent(content string) StreamEvent {
	msg := &Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: content},
		},
	}
	return StreamEvent{Type: "assistant", Content: content, Message: msg}
}

func makeResultEvent(content string) StreamEvent {
	return StreamEvent{Type: "result", Result: content, Content: content}
}

func makeToolStartStreamEvent(index int, id string, name string) StreamEvent {
	raw := json.RawMessage(fmt.Sprintf(
		`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":"%s","name":"%s"}}`,
		index, id, name,
	))
	return StreamEvent{Type: "stream_event", Event: raw}
}

func makeToolInputDeltaStreamEvent(index int, partialJSON string) StreamEvent {
	// partial_json field must be a properly escaped JSON string within the outer JSON
	escaped, _ := json.Marshal(partialJSON)
	raw := json.RawMessage(fmt.Sprintf(
		`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`,
		index, string(escaped),
	))
	return StreamEvent{Type: "stream_event", Event: raw}
}

func makeContentBlockStopStreamEvent(index int) StreamEvent {
	raw := json.RawMessage(fmt.Sprintf(
		`{"type":"content_block_stop","index":%d}`,
		index,
	))
	return StreamEvent{Type: "stream_event", Event: raw}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal wraps in quotes, strip them
	return string(b[1:len(b)-1])
}
