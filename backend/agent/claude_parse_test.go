package agent

import (
	"encoding/json"
	"testing"
)

// wrap 把 Claude 的 stream_event 子事件包成一行完整的 CLI 输出。
func wrap(t *testing.T, subEvent string) []byte {
	t.Helper()
	line := `{"type":"stream_event","event":` + subEvent + `}`
	if !json.Valid([]byte(line)) {
		t.Fatalf("invalid fixture JSON: %s", line)
	}
	return []byte(line)
}

func TestParseLine_TextDelta(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, ok := p.ParseLine(wrap(t, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`))
	if !ok {
		t.Fatal("expected ok")
	}
	if evt.Delta == nil || evt.Delta.Kind != DeltaText || evt.Delta.Text != "Hello" || evt.Delta.Index != 0 {
		t.Fatalf("unexpected delta: %+v", evt.Delta)
	}
}

func TestParseLine_ThinkingDeltaIgnored(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, ok := p.ParseLine(wrap(t, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm","text":"inner thought"}}`))
	if !ok {
		t.Fatal("expected ok (line is parseable, just carries no delta)")
	}
	if evt.Delta != nil {
		t.Fatalf("thinking_delta must not produce a Delta, got: %+v", evt.Delta)
	}
}

func TestParseLine_ToolUseStart(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, _ := p.ParseLine(wrap(t, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read"}}`))
	if evt.Delta == nil || evt.Delta.Kind != DeltaToolStart {
		t.Fatalf("expected DeltaToolStart, got: %+v", evt.Delta)
	}
	if evt.Delta.ToolID != "toolu_1" || evt.Delta.ToolName != "Read" || evt.Delta.Index != 1 {
		t.Fatalf("unexpected tool start delta: %+v", evt.Delta)
	}
}

func TestParseLine_ToolUseStartIncompleteIsIgnored(t *testing.T) {
	p := &ClaudeProtocol{}
	// 原 ExtractToolUseStart 在 ID 或 Name 为空时返回 nil,行为必须保留
	evt, _ := p.ParseLine(wrap(t, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"","name":"Read"}}`))
	if evt.Delta != nil {
		t.Fatalf("expected no Delta for incomplete tool_use start, got: %+v", evt.Delta)
	}
}

func TestParseLine_ToolInputDelta(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, _ := p.ParseLine(wrap(t, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`))
	if evt.Delta == nil || evt.Delta.Kind != DeltaToolInput || evt.Delta.ToolInput != `{"path":` || evt.Delta.Index != 1 {
		t.Fatalf("unexpected tool input delta: %+v", evt.Delta)
	}
}

func TestParseLine_ContentBlockStop(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, _ := p.ParseLine(wrap(t, `{"type":"content_block_stop","index":1}`))
	if evt.Delta == nil || evt.Delta.Kind != DeltaToolEnd || evt.Delta.Index != 1 {
		t.Fatalf("unexpected stop delta: %+v", evt.Delta)
	}
}

func TestParseLine_SystemInitCarriesSessionID(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, ok := p.ParseLine([]byte(`{"type":"system","subtype":"init","session_id":"abc-123"}`))
	if !ok {
		t.Fatal("expected ok")
	}
	if evt.Type != "system" || evt.Subtype != "init" || evt.SessionID != "abc-123" {
		t.Fatalf("unexpected system event: %+v", evt)
	}
}

func TestParseLine_ResultCarriesContentAndError(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, _ := p.ParseLine([]byte(`{"type":"result","result":"boom","is_error":true}`))
	if evt.Type != "result" || evt.Result != "boom" {
		t.Fatalf("unexpected result event: %+v", evt)
	}
	if !evt.ResultIsError {
		t.Fatal("expected ResultIsError to be true")
	}
}

func TestParseLine_AssistantMessageParsed(t *testing.T) {
	p := &ClaudeProtocol{}
	evt, _ := p.ParseLine([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`))
	if evt.Message == nil || len(evt.Message.Content) != 1 || evt.Message.Content[0].Text != "hi" {
		t.Fatalf("unexpected assistant message: %+v", evt.Message)
	}
	if evt.Content != "hi" {
		t.Fatalf("expected Content extracted from first text block, got %q", evt.Content)
	}
}

func TestParseLine_MalformedReturnsNotOK(t *testing.T) {
	p := &ClaudeProtocol{}
	if _, ok := p.ParseLine([]byte(`{not json`)); ok {
		t.Fatal("expected ok=false for malformed line")
	}
}
