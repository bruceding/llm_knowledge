// Package agent 收拢各 LLM CLI 后端的协议差异(旗标、环境、stdin 编码、
// stdout 解析),向上层暴露归一化的 StreamEvent / Delta。
//
// 上层(claude 包的 SessionPool / QuerySessionPool / StreamProcessor / Client)
// 只依赖本包的类型与 Protocol 接口,不感知具体是哪个 CLI。
package agent

import (
	"context"
	"encoding/json"
)

// Backend 标识 agent 后端种类。
type Backend string

const (
	BackendClaude Backend = "claude"
	BackendPi     Backend = "pi"
)

// DeltaKind 是流式增量的种类。
type DeltaKind int

const (
	DeltaNone      DeltaKind = iota
	DeltaText                // 文本增量
	DeltaToolStart           // 工具调用开始(带 ToolID + ToolName)
	DeltaToolInput           // 工具入参增量
	DeltaToolEnd             // 工具调用结束
)

// Delta 是流式增量的归一化表示,取代原先直挖 CLI 专有 raw JSON 的做法。
// Claude 的 content_block_* 与 pi 的 assistantMessageEvent 都映射到它。
type Delta struct {
	Kind      DeltaKind
	Text      string // DeltaText:增量文本
	Index     int    // 内容块序号:Claude 的 index / pi 的 contentIndex
	ToolID    string // DeltaToolStart / DeltaToolEnd
	ToolName  string // DeltaToolStart
	ToolInput string // DeltaToolInput:入参 JSON 片段
}

// ImageData 是一张待发送的图片。字段与原 claude.ImageData 一致。
type ImageData struct {
	MediaType  string // e.g., "image/png"
	Base64Data string // base64 encoded image data (without prefix)
}

// StreamEvent 是从 CLI stdout 解析出的单条归一化事件。
//
// 与原 claude.StreamEvent 的唯一差异:Event json.RawMessage 被 Delta *Delta 取代。
// ResultMessageID / ResultFullContent 不是 wire 字段,而是 query_pool 的 routeEvents
// 写入的路由元数据(用于把 assistant 回复存回 DB),Protocol 实现不应触碰。
type StreamEvent struct {
	Type      string   `json:"type"`                 // 后端语义的事件类型,见各 Protocol 的 ParseLine
	Content   string   `json:"content"`              // Text content of the event (extracted)
	Subtype   string   `json:"subtype"`              // subtype for system messages
	SessionID string   `json:"session_id,omitempty"` // Session ID from system events
	Result    string   `json:"result"`               // Result text for type "result"
	Error     string   `json:"error,omitempty"`      // Error message if any
	ToolName  string   `json:"toolName,omitempty"`   // 既有字段,当前生产代码未赋值,保留以免波及调用点
	ToolInput string   `json:"toolInput,omitempty"`  // 同上
	Message   *Message `json:"message,omitempty"`    // Message for type "assistant"
	Delta     *Delta   `json:"delta,omitempty"`

	// Event 是 Claude 专有的 stream_event 原始载荷。它在本重构中是
	// **过渡字段**:Task 1-4 期间与 Delta 并存以保证每个任务收尾全绿,
	// Task 5 将其连同消费方一并删除。新增代码不得读取它。
	Event json.RawMessage `json:"event,omitempty"`

	ResultIsError bool `json:"resultIsError,omitempty"` // Claude 的 result.is_error;上层据此把事件转成 error

	ResultMessageID   uint   `json:"resultMessageId,omitempty"`   // User message ID for saving assistant reply (set in result)
	ResultFullContent string `json:"resultFullContent,omitempty"` // Accumulated assistant content for saving (set in result)
}

// Message 是一条 assistant 消息的归一化形态。
// Claude 的 assistant 事件与 pi 的 message_end 都映射到它。
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock 字段与 JSON tag 与原 claude.ContentBlock 完全一致(注意 Text 无 omitempty)。
type ContentBlock struct {
	Type  string          `json:"type"` // text, thinking, tool_use
	Text  string          `json:"text"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// Protocol 收拢一个 CLI 后端的全部差异。
type Protocol interface {
	Backend() Backend
	Bin() string

	// 旗标与环境
	SessionArgs(sysPrompt string, tools []string) ([]string, error)
	ResumeArgs(prevSessionID, sysPrompt string, tools []string) ([]string, error)
	OnceArgs(sysPrompt string, tools []string, print bool) ([]string, error)
	Env(allowedDir string) []string

	// stdin 编码
	EncodeUserMessage(content string, images []ImageData) ([]byte, error)
	EncodeInterrupt() ([]byte, error)

	// stdout 解析:一行 JSONL → 归一化事件。ok=false 表示该行应跳过。
	ParseLine(line []byte) (evt StreamEvent, ok bool)

	// Probe 探测 CLI 是否可用。
	Probe(ctx context.Context) error
}
