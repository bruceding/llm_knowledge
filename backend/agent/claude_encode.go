package agent

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// EncodeUserMessage 构造写入 stdin 的用户消息行(含结尾换行)。
// images 为 nil 或空时,content 是纯字符串;否则是 [图片..., 文本?] 数组。
// 图片在前、文本在后,与既有行为一致。
func (p *ClaudeProtocol) EncodeUserMessage(content string, images []ImageData) ([]byte, error) {
	var payload any

	if len(images) == 0 {
		payload = map[string]any{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": content,
			},
		}
	} else {
		blocks := make([]map[string]any, 0, len(images)+1)
		for _, img := range images {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": img.MediaType,
					"data":       img.Base64Data,
				},
			})
		}
		if content != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": content,
			})
		}
		payload = map[string]any{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": blocks,
			},
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode user message: %w", err)
	}
	return append(b, '\n'), nil
}

// interruptSeq 让连续调用 EncodeInterrupt 得到的 request_id 严格递增。
// 只用 time.Now().UnixNano() 不够:UnixNano 取的是墙钟(单调读数被剥掉),
// 且实测本机粒度约 1µs(返回值尾数恒为 000),紧循环内相邻两次会落到同一时刻。
// 墙钟非递减 + 序号严格递增 ⇒ 二者之和严格递增,故 request_id 不重复。
// 残余风险仅限「墙钟被 NTP 往回拨正好 1ns」,且 request_id 在本仓是只写字段
// (无任何解析/配对逻辑),重复也不产生错误行为。
var interruptSeq atomic.Uint64

// EncodeInterrupt 构造 control_request/interrupt 行(含结尾换行)。
func (p *ClaudeProtocol) EncodeInterrupt() ([]byte, error) {
	b, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("%d", time.Now().UnixNano()+int64(interruptSeq.Add(1))),
		"request": map[string]any{
			"subtype": "interrupt",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode interrupt: %w", err)
	}
	return append(b, '\n'), nil
}
