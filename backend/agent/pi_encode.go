package agent

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// piPromptSeq 给每条 prompt 命令分配单调递增的 id。
//
// pi 用 id 回显关联 response(docs/rpc.md:73 的 {"id":"req-1","type":"response",
// "command":"prompt","success":true})。ParseLine 靠 response 里的 command 字段区分
// get_state 与 prompt,不依赖 id,所以这里不需要时钟唯一性 —— 用计数器而不是
// time.Now().UnixNano(),正是因为后者在紧循环下会撞(见 claude_encode.go 的
// interruptSeq 注释)。
var piPromptSeq atomic.Uint64

// EncodeUserMessage 构造 pi 的 prompt 命令(含结尾换行)。
//
// 形状对 pi 0.85.1 的 docs/rpc.md:43-58、:78 逐字核实:
//
//	{"id":"msg-1","type":"prompt","message":"..."}
//	带图:{"id":"msg-1","type":"prompt","message":"...","images":[{"type":"image","data":"<base64>","mimeType":"image/png"}]}
//
// 与 Claude 的两处结构差异(不能照搬 ClaudeProtocol.EncodeUserMessage):
//
//   - Claude 的 message.content 是 string **或** content-block 数组,图片塞进数组里
//     ({"type":"image","source":{"type":"base64","media_type":...,"data":...}});
//     pi 的 message 恒为字符串,图片走**兄弟字段** images
//   - 键名是 mimeType(驼峰)而不是 media_type(蛇形),且没有 source 那一层包装
//
// rpc 模式下 pi 不读管道 stdin 当 prompt(main.js:701-703 显式跳过,stdin 留作
// JSON-RPC),所以每一轮都必须走本函数编码成命令写入。
func (p *PiProtocol) EncodeUserMessage(content string, images []ImageData) ([]byte, error) {
	payload := map[string]any{
		"id":      fmt.Sprintf("msg-%d", piPromptSeq.Add(1)),
		"type":    "prompt",
		"message": content,
	}
	if len(images) > 0 {
		items := make([]map[string]any, 0, len(images))
		for _, img := range images {
			items = append(items, map[string]any{
				"type":     "image",
				"data":     img.Base64Data,
				"mimeType": img.MediaType,
			})
		}
		payload["images"] = items
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode pi prompt: %w", err)
	}
	return append(b, '\n'), nil
}

// EncodeInterrupt 构造 pi 的 abort 命令(含结尾换行)。
//
// docs/rpc.md:124-134:`{"type":"abort"}`,响应 {"type":"response","command":"abort",
// "success":true}。与 Claude 的 control_request/interrupt 完全不同形,也没有
// request_id —— 按文档原样,不加推测性字段。
//
// 注意 rpc.md:158:abort 之后队列里若还有消息,pi 会继续处理它们(要实现交互式
// Esc 的语义得先发 clear_queue)。本项目的中断语义就是「停下当前这一轮」,
// 且我们从不使用 steer/follow_up 排队,故不发 clear_queue。
func (p *PiProtocol) EncodeInterrupt() ([]byte, error) {
	b, err := json.Marshal(map[string]any{"type": "abort"})
	if err != nil {
		return nil, fmt.Errorf("encode pi abort: %w", err)
	}
	return append(b, '\n'), nil
}
