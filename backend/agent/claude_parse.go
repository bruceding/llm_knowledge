package agent

import "encoding/json"

// claudeRawEvent 是 Claude CLI 单行输出的解析中间类型(原 claude.RawEvent)。
type claudeRawEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	Error     string          `json:"error"`
	Content   string          `json:"content"`
	Message   json.RawMessage `json:"message"`
	Event     json.RawMessage `json:"event"`
}

// ParseLine 把一行 Claude CLI JSONL 解析为归一化 StreamEvent。
// ok=false 表示该行应被跳过(畸形 JSON)。
func (p *ClaudeProtocol) ParseLine(line []byte) (StreamEvent, bool) {
	var raw claudeRawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return StreamEvent{}, false
	}

	evt := StreamEvent{
		Type:          raw.Type,
		Subtype:       raw.Subtype,
		SessionID:     raw.SessionID,
		Content:       raw.Content,
		Result:        raw.Result,
		ResultIsError: raw.IsError,
		Error:         raw.Error,
	}

	switch raw.Type {
	case "assistant":
		if raw.Message != nil {
			var msg Message
			if err := json.Unmarshal(raw.Message, &msg); err == nil {
				evt.Message = &msg
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" {
						evt.Content = block.Text
						break
					}
				}
			}
		}

	case "stream_event":
		evt.Delta = parseClaudeStreamEvent(raw.Event)
	}

	return evt, true
}

// parseClaudeStreamEvent 把 Anthropic SSE 透传的 content_block_* 子事件归一化为 Delta。
// 返回 nil 表示该子事件不携带可用增量(例如 thinking_delta)。
func parseClaudeStreamEvent(eventRaw json.RawMessage) *Delta {
	if eventRaw == nil {
		return nil
	}
	var sub struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if err := json.Unmarshal(eventRaw, &sub); err != nil {
		return nil
	}

	switch sub.Type {
	case "content_block_start":
		if sub.ContentBlock.Type != "tool_use" {
			return nil
		}
		// 原 ExtractToolUseStart 在 ID 或 Name 为空时返回 nil,行为保留
		if sub.ContentBlock.ID == "" || sub.ContentBlock.Name == "" {
			return nil
		}
		return &Delta{
			Kind:     DeltaToolStart,
			Index:    sub.Index,
			ToolID:   sub.ContentBlock.ID,
			ToolName: sub.ContentBlock.Name,
		}

	case "content_block_delta":
		switch sub.Delta.Type {
		case "text_delta":
			if sub.Delta.Text == "" {
				return nil
			}
			return &Delta{Kind: DeltaText, Index: sub.Index, Text: sub.Delta.Text}
		case "input_json_delta":
			if sub.Delta.PartialJSON == "" {
				return nil
			}
			return &Delta{Kind: DeltaToolInput, Index: sub.Index, ToolInput: sub.Delta.PartialJSON}
		}
		// thinking_delta 等其他类型一律忽略
		return nil

	case "content_block_stop":
		return &Delta{Kind: DeltaToolEnd, Index: sub.Index}
	}

	return nil
}
