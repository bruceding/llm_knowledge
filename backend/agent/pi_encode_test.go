package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodePiLine 断言一行是合法 JSON、以 \n 结尾,并解出 map 供后续断言。
func decodePiLine(t *testing.T, b []byte) map[string]any {
	t.Helper()
	if len(b) == 0 {
		t.Fatal("encoded line is empty")
	}
	if b[len(b)-1] != '\n' {
		t.Errorf("encoded line must end with \\n (JSONL framing), got %q", string(b))
	}
	trimmed := strings.TrimSuffix(string(b), "\n")
	if strings.Contains(trimmed, "\n") {
		t.Errorf("encoded line must not contain an embedded newline: %q", trimmed)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(trimmed), &got); err != nil {
		t.Fatalf("encoded line is not valid JSON: %v (%q)", err, trimmed)
	}
	return got
}

// TestPiEncodeUserMessage_TextOnly 钉住纯文本 prompt 的 wire 形状:
// message 恒为**字符串**(不是 Claude 那样的 content-block 数组),且不带 images 键。
func TestPiEncodeUserMessage_TextOnly(t *testing.T) {
	p := &PiProtocol{}
	dec := mustDecode(t)
	got := dec(p.EncodeUserMessage("你好,请读一下这份文档", nil))

	if got["type"] != "prompt" {
		t.Errorf("type = %v, want prompt", got["type"])
	}
	msg, ok := got["message"].(string)
	if !ok {
		t.Fatalf("message must be a string (pi's shape), got %T: %v", got["message"], got["message"])
	}
	if msg != "你好,请读一下这份文档" {
		t.Errorf("message = %q, want the original content", msg)
	}
	if _, present := got["images"]; present {
		t.Errorf("images key must be absent when there are no images: %v", got)
	}
	if id, _ := got["id"].(string); id == "" {
		t.Error("expected a non-empty id (pi echoes it back in the response)")
	}
}

// TestPiEncodeUserMessage_WithImages 钉住带图形状,重点是它与 Claude 的三处不同:
// images 是 message 的**兄弟字段**、每项只有 type/data/mimeType 三键、
// 键名是驼峰 mimeType 且**没有** source 那一层包装。
func TestPiEncodeUserMessage_WithImages(t *testing.T) {
	p := &PiProtocol{}
	dec := mustDecode(t)
	images := []ImageData{
		{MediaType: "image/png", Base64Data: "cG5nLWRhdGE="},
		{MediaType: "image/jpeg", Base64Data: "anBlZy1kYXRh"},
	}
	got := dec(p.EncodeUserMessage("这两张图里是什么?", images))

	if msg, ok := got["message"].(string); !ok || msg != "这两张图里是什么?" {
		t.Errorf("message must stay a plain string alongside images, got %T %v", got["message"], got["message"])
	}

	rawImages, ok := got["images"].([]any)
	if !ok {
		t.Fatalf("images must be an array sibling of message, got %T: %v", got["images"], got)
	}
	if len(rawImages) != 2 {
		t.Fatalf("expected 2 images, got %d: %v", len(rawImages), rawImages)
	}
	for i, raw := range rawImages {
		img, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("images[%d] is not an object: %v", i, raw)
		}
		if img["type"] != "image" {
			t.Errorf("images[%d].type = %v, want image", i, img["type"])
		}
		if img["mimeType"] != images[i].MediaType {
			t.Errorf("images[%d].mimeType = %v, want %q (camelCase key, not media_type)", i, img["mimeType"], images[i].MediaType)
		}
		if img["data"] != images[i].Base64Data {
			t.Errorf("images[%d].data = %v, want %q", i, img["data"], images[i].Base64Data)
		}
		// Claude 的形状不得泄漏进来:没有 source 包装、没有 media_type 蛇形键
		if _, present := img["source"]; present {
			t.Errorf("images[%d] must not use Claude's source wrapper: %v", i, img)
		}
		if _, present := img["media_type"]; present {
			t.Errorf("images[%d] must not use Claude's media_type key: %v", i, img)
		}
		if len(img) != 3 {
			t.Errorf("images[%d] must have exactly type/data/mimeType, got %d keys: %v", i, len(img), img)
		}
	}
}

// TestPiEncodeUserMessage_ImagesOnlyKeepsEmptyMessage 断言只有图片、没有文字时
// 仍然产出合法的 prompt 命令(message 为空串),与 Claude 侧「content 为空则不加
// text block」的行为对应。
func TestPiEncodeUserMessage_ImagesOnlyKeepsEmptyMessage(t *testing.T) {
	p := &PiProtocol{}
	dec := mustDecode(t)
	got := dec(p.EncodeUserMessage("", []ImageData{{MediaType: "image/png", Base64Data: "eA=="}}))

	msg, ok := got["message"].(string)
	if !ok {
		t.Fatalf("message must still be a string, got %T", got["message"])
	}
	if msg != "" {
		t.Errorf("message = %q, want empty string", msg)
	}
	if imgs, ok := got["images"].([]any); !ok || len(imgs) != 1 {
		t.Errorf("expected exactly 1 image, got %v", got["images"])
	}
}

// TestPiEncodeInterrupt 钉住 abort 命令的形状:按 docs/rpc.md:129 原样,
// 只有 type 一个键(不加推测性字段,也没有 Claude 那样的 request_id)。
func TestPiEncodeInterrupt(t *testing.T) {
	p := &PiProtocol{}
	dec := mustDecode(t)
	got := dec(p.EncodeInterrupt())

	if got["type"] != "abort" {
		t.Errorf("type = %v, want abort", got["type"])
	}
	if len(got) != 1 {
		t.Errorf("abort must carry exactly one key per docs/rpc.md:129, got %d: %v", len(got), got)
	}
}

// TestPiEncode_PromptIDsAreUniqueAndDistinctFromInit 有两条理由:
//  1. id 必须唯一,否则多轮对话里 response 无法关联(这正是 claude_encode.go 的
//     interruptSeq 修的那个坑,pi 侧从一开始就用计数器而不是 UnixNano);
//  2. prompt 的 id 绝不能与 InitCommands 的 get_state id 撞上 —— ParseLine 要把
//     get_state 的 response 归一化成 system/init,而 prompt 的 response 明确
//     **不可**当作完成信号(它比首个 message_update 早到),混淆两者会直接坏掉
//     会话 ID 捕获或提前发 done。
func TestPiEncode_PromptIDsAreUniqueAndDistinctFromInit(t *testing.T) {
	p := &PiProtocol{}
	dec := mustDecode(t)

	initIDs := map[string]bool{}
	for _, cmd := range p.InitCommands() {
		var got struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(cmd, &got); err != nil {
			t.Fatalf("init command is not valid JSON: %v", err)
		}
		initIDs[got.ID] = true
	}
	if len(initIDs) == 0 {
		t.Fatal("expected at least one init command id")
	}

	seen := map[string]bool{}
	// 本用例钉住的是「id 非空、彼此不同、且永不与 get_state 的 id 撞上」。
	//
	// 它**拦不住**「未来有人把计数器改回 time.Now().UnixNano()」这类回归,因为那
	// 是时序相关的:实测把本循环提到 2000 次、改回 UnixNano 后连跑 5 次全绿 ——
	// EncodeUserMessage 的循环体(含多字节文本的 json.Marshal)每次 >1µs,在本机
	// 约 1µs 的时钟粒度下反而不会撞;而 claude 侧那个轻得多的 EncodeInterrupt
	// 在 50 次循环里就有约四成概率撞。故唯一性由构造(计数器)保证,不依赖本用例。
	const iterations = 200
	for i := 0; i < iterations; i++ {
		got := dec(p.EncodeUserMessage("hello", nil))
		id, _ := got["id"].(string)
		if id == "" {
			t.Fatal("expected a non-empty prompt id")
		}
		if seen[id] {
			t.Fatalf("duplicate prompt id %q at iteration %d", id, i)
		}
		seen[id] = true
		if initIDs[id] {
			t.Fatalf("prompt id %q collides with a get_state id; ParseLine would confuse them", id)
		}
	}
}

// mustDecode 返回一个助手:接受 Encode* 的 ([]byte, error),断言无错后解码成 map。
// 做成闭包是因为 Go 只允许 f(g()) 形式的多值展开,带额外的 t 实参就不行
// (与 pi_args_test.go 的 mustArgs 同一手法)。
func mustDecode(t *testing.T) func([]byte, error) map[string]any {
	t.Helper()
	return func(b []byte, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected encode error: %v", err)
		}
		return decodePiLine(t, b)
	}
}
