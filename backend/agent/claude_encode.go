package agent

import (
	"encoding/json"
	"fmt"
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

// EncodeInterrupt 构造 control_request/interrupt 行(含结尾换行)。
func (p *ClaudeProtocol) EncodeInterrupt() ([]byte, error) {
	b, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("%d", time.Now().UnixNano()),
		"request": map[string]any{
			"subtype": "interrupt",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode interrupt: %w", err)
	}
	return append(b, '\n'), nil
}
