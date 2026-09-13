package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeEncodeUserMessage_PlainText(t *testing.T) {
	p := &ClaudeProtocol{}
	b, err := p.EncodeUserMessage("你好", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("expected trailing newline, got: %q", string(b))
	}

	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v (%q)", err, string(b))
	}
	if got.Type != "user" || got.Message.Role != "user" || got.Message.Content != "你好" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
}

func TestClaudeEncodeUserMessage_ImagesFirstThenText(t *testing.T) {
	p := &ClaudeProtocol{}
	images := []ImageData{
		{MediaType: "image/png", Base64Data: "AAAA"},
		{MediaType: "image/jpeg", Base64Data: "BBBB"},
	}
	b, err := p.EncodeUserMessage("描述这两张图", images)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("expected trailing newline, got: %q", string(b))
	}

	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Message.Content) != 3 {
		t.Fatalf("expected 3 content blocks (2 images + 1 text), got %d", len(got.Message.Content))
	}
	// 图片在前、文本在后 —— 与既有 SendUserMessageWithImages 行为一致
	for i, want := range []string{"image", "image", "text"} {
		if got.Message.Content[i]["type"] != want {
			t.Errorf("block %d: expected type %q, got %v", i, want, got.Message.Content[i]["type"])
		}
	}
	src0, ok := got.Message.Content[0]["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected image source object, got %v", got.Message.Content[0]["source"])
	}
	if src0["type"] != "base64" || src0["media_type"] != "image/png" || src0["data"] != "AAAA" {
		t.Errorf("unexpected first image source: %v", src0)
	}
	// 第二张图必须带自己的 source(实现若误用 images[0] 填所有块,这里会红)
	src1, ok := got.Message.Content[1]["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected image source object, got %v", got.Message.Content[1]["source"])
	}
	if src1["type"] != "base64" || src1["media_type"] != "image/jpeg" || src1["data"] != "BBBB" {
		t.Errorf("unexpected second image source: %v", src1)
	}
	if got.Message.Content[2]["text"] != "描述这两张图" {
		t.Errorf("unexpected text block: %v", got.Message.Content[2])
	}
}

func TestClaudeEncodeUserMessage_EmptyTextWithImagesOmitsTextBlock(t *testing.T) {
	p := &ClaudeProtocol{}
	b, err := p.EncodeUserMessage("", []ImageData{{MediaType: "image/png", Base64Data: "AAAA"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Message struct {
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Message.Content) != 1 {
		t.Fatalf("expected only the image block, got %d", len(got.Message.Content))
	}
}

func TestClaudeEncodeInterrupt_IsControlRequest(t *testing.T) {
	p := &ClaudeProtocol{}
	b, err := p.EncodeInterrupt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("expected trailing newline, got: %q", string(b))
	}
	var got struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Type != "control_request" || got.Request.Subtype != "interrupt" {
		t.Fatalf("unexpected interrupt envelope: %+v", got)
	}
	if got.RequestID == "" {
		t.Fatal("expected non-empty request_id")
	}
}

func TestClaudeEncodeInterrupt_RequestIDsAreUnique(t *testing.T) {
	p := &ClaudeProtocol{}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		b, err := p.EncodeInterrupt()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if seen[got.RequestID] {
			t.Fatalf("duplicate request_id %q at iteration %d", got.RequestID, i)
		}
		seen[got.RequestID] = true
	}
}
