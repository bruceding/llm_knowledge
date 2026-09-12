# Agent 抽象层(等价重构) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Claude CLI 的协议细节(旗标、env、stdin 编码、stdout 解析)从 `backend/claude` 抽到新的 `backend/agent` 包背后,引入归一化的 `Delta` 类型取代 `StreamEvent.Event json.RawMessage`。**本计划不引入 pi,不改变任何外部行为。**

**Architecture:** 新增 `agent.Protocol` 接口作为唯一 seam。`StreamEvent`/`Message`/`ContentBlock`/`ImageData` 四个共享类型上移到 `agent` 包,`claude` 包内保留类型别名,使所有既有 import 与测试零改动。`ClaudeProtocol` 是接口的第一个实现,承接现有全部 Claude 专有逻辑。`StreamProcessor` 改为消费 `Delta`,不再深挖 Claude 的 `content_block_*` raw JSON。

**Tech Stack:** Go 1.25+、`encoding/json`、标准库 `os/exec`。无新增第三方依赖。

**规格:** `docs/superpowers/specs/2026-09-12-pi-backend-switch-design.md`(见「架构 → seam 位置」「`agent` 包」「解析器后端差异」三节)。**本计划只实现规格的抽象层部分;pi 后端、Settings 开关、沙箱 extension 属 Plan 2。** 执行者应同时读规格与本计划。

## 全局约束

- **行为逐条不变**:本计划结束时,`LLMBackend` 概念尚未引入,claude 是唯一后端,所有 API/SSE/CLI 旗标与改造前完全一致
- **前端 diff 必须为空**:`SSEEvent` 的 JSON tag(`type`/`text`/`content`/`toolId`/`toolName`/`toolInput`)一字不改
- **每个任务的收尾闸门**:`cd backend && go build ./... && go vet ./... && go test ./...` 必须全绿,才允许 commit
- **不新增第三方依赖**,不改 `go.mod`
- **不动** `CLAUDE.md`、`scripts/path-validator.py`、`backend/dependencies/`
- 提交信息用中文,前缀 `refactor(agent):`
- 本计划在 `main` 上编写;**实现开始前**应先 `git worktree prune`(现有两个 worktree 条目已失效,指向旧 home `/Users/bruceding 1/...`)再建 `feature/agent-abstraction-layer`

## 文件结构

**创建**

| 文件 | 职责 |
|---|---|
| `backend/agent/agent.go` | 共享类型:`Backend`、`DeltaKind`、`Delta`、`ImageData`、`StreamEvent`、`Message`、`ContentBlock`、`Protocol` 接口 |
| `backend/agent/claude_args.go` | `ClaudeProtocol` 的旗标与 env 构造(承接 `security.go` 的 `BuildSecureArgs`/`BuildSecureEnv`) |
| `backend/agent/claude_encode.go` | `ClaudeProtocol` 的 stdin 编码(user message / images / interrupt) |
| `backend/agent/claude_parse.go` | `ClaudeProtocol.ParseLine` + 四个 `content_block_*` 解析helper(从 `stream.go` 迁入) |
| `backend/agent/claude_args_test.go` | 旗标/env 单测(从 `security_test.go` 平移) |
| `backend/agent/claude_parse_test.go` | 解析单测(从 `stream_test.go` 的 `TestExtract*` 平移) |

**修改**

| 文件 | 改动 |
|---|---|
| `backend/claude/client.go` | 删除 `StreamEvent`/`Message`/`ContentBlock` 定义,改为 `agent` 别名;`Client` 增 `Proto` 字段 |
| `backend/claude/session.go` | `ImageData` 改别名;`readEvents` 委托 `ParseLine`;`buildCmdWithEnv`/`SendUserMessage*`/`SendInterrupt` 委托 Protocol |
| `backend/claude/stream.go` | `Process` 改消费 `Delta`;删除四个 stream_event `Extract*`;删除 `assistant` + raw `Event` 死路径 |
| `backend/claude/query_pool.go` | 两个 Start 函数改用 `proto.SessionArgs`/`ResumeArgs` |
| `backend/claude/security.go` | `BuildSecureArgs`/`BuildSecureEnv` 保留为转发壳(避免 `api/`、`ingest/` 调用点大改),实体迁至 `agent` |
| `backend/claude/stream_test.go` | 四个 fixture helper 改构造 `Delta`;`TestExtract*` 迁出 |
| `backend/claude/security_test.go` | `BuildSecure*` 相关用例迁至 `agent` 包;跨语言同步测试留在原处 |

**拆分理由:** `claude_args.go` / `claude_encode.go` / `claude_parse.go` 三个文件各对应 `Protocol` 接口的一组方法,彼此无耦合,单文件都能一次放进上下文。不合并成一个大 `claude.go`。

---

## Task 1: `agent` 包骨架 + 类型上移

纯搬迁。结束时 `claude` 包通过类型别名对外暴露完全相同的类型,所有既有调用点与测试不需要任何修改。

**Files:**
- Create: `backend/agent/agent.go`
- Modify: `backend/claude/client.go:21-49`(删除三个类型定义)
- Modify: `backend/claude/session.go:17-21`(删除 `ImageData` 定义)

- [ ] **Step 1: 写 `agent.go`**

创建 `backend/agent/agent.go`:

```go
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
	DeltaNone DeltaKind = iota
	DeltaText       // 文本增量
	DeltaToolStart  // 工具调用开始(带 ToolID + ToolName)
	DeltaToolInput  // 工具入参增量
	DeltaToolEnd    // 工具调用结束
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
	MediaType  string
	Base64Data string
}

// StreamEvent 是从 CLI stdout 解析出的单条归一化事件。
//
// 与原 claude.StreamEvent 的唯一差异:Event json.RawMessage 被 Delta *Delta 取代。
// ResultMessageID / ResultFullContent 不是 wire 字段,而是 query_pool 的 routeEvents
// 写入的路由元数据(用于把 assistant 回复存回 DB),Protocol 实现不应触碰。
type StreamEvent struct {
	Type      string // 后端语义的事件类型,见各 Protocol 的 ParseLine
	Content   string
	Subtype   string
	SessionID string
	Result    string
	Error     string
	ToolName  string // 既有字段,当前生产代码未赋值,保留以免波及调用点
	ToolInput string  // 同上
	Message   *Message
	Delta     *Delta

	// Event 是 Claude 专有的 stream_event 原始载荷。它在本重构中是
	// **过渡字段**:Task 1-4 期间与 Delta 并存以保证每个任务收尾全绿,
	// Task 5 将其连同消费方一并删除。新增代码不得读取它。
	Event json.RawMessage

	ResultIsError bool // Claude 的 result.is_error;上层据此把事件转成 error

	ResultMessageID   uint
	ResultFullContent string
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
	OnceArgs(tools []string, print bool) ([]string, error)
	Env(allowedDir string) []string

	// stdin 编码
	EncodeUserMessage(content string, images []ImageData) ([]byte, error)
	EncodeInterrupt() ([]byte, error)

	// stdout 解析:一行 JSONL → 归一化事件。ok=false 表示该行应跳过。
	ParseLine(line []byte) (evt StreamEvent, ok bool)

	// Probe 探测 CLI 是否可用。
	Probe(ctx context.Context) error
}
```

- [ ] **Step 2: 在 `claude` 包建立别名,删除原定义**

`backend/claude/client.go` 的 import 块加入 `"llm-knowledge/agent"`,然后把 `client.go:21-49` 的 `StreamEvent`、`Message`、`ContentBlock` 三个 `type` 定义整体替换为:

```go
// 以下类型已上移到 agent 包(见 docs/superpowers/specs/2026-09-12-pi-backend-switch-design.md)。
// 保留别名使既有 import 与测试零改动。
type (
	StreamEvent  = agent.StreamEvent
	Message      = agent.Message
	ContentBlock = agent.ContentBlock
)
```

`RawEvent`(原 `client.go:52-63`)**保留在 claude 包不动** —— 它是 Claude 专有的解析中间类型,Plan 2 会把它迁进 `agent/claude_parse.go`。

`backend/claude/session.go:17-21` 的 `ImageData` 定义替换为:

```go
// ImageData 已上移到 agent 包,保留别名。
type ImageData = agent.ImageData
```

并在 `session.go` 的 import 块加入 `"llm-knowledge/agent"`。

- [ ] **Step 3: 运行闸门**

运行:`cd backend && go build ./... && go vet ./... && go test ./...`
预期:**全部 PASS,且无需修改任何测试文件**。

原因:`StreamEvent` 在本步骤仍保留 `Event json.RawMessage` 过渡字段,所以:
- `session.go:564` 的 `Event: rawEvent.Event,` 照旧编译通过
- `stream_test.go:494/516/526/534` 的四个 fixture helper 照旧构造 `StreamEvent{Type:"stream_event", Event: raw}`
- `stream.go` 的 `Process` 照旧挖 `evt.Event`

本任务是**纯类型搬迁**,不触碰任何运行时逻辑。`Delta` 与 `ResultIsError` 字段此时已定义但尚无生产者与消费者 —— 这是有意的中间态,Task 3 才接线。

若此处出现编译失败,只可能是别名写错或 import 缺失,**不要**通过删测试或改 `Process` 来绕过。

- [ ] **Step 4: 验证别名确实生效(无重复定义)**

运行:`cd backend && grep -rn "^type StreamEvent\|^type Message \|^type ContentBlock\|^type ImageData" claude/ agent/`
预期:
- `agent/agent.go` 各一处 `type StreamEvent struct` / `type Message struct` / `type ContentBlock struct` / `type ImageData struct`
- `claude/client.go` 一处 `type (` 别名块、`claude/session.go` 一处 `type ImageData = agent.ImageData`
- **不得**出现 `claude` 包内的 `struct` 定义残留

- [ ] **Step 5: Commit**

```bash
git add backend/agent/agent.go backend/claude/client.go backend/claude/session.go backend/claude/stream_test.go
git commit -m "refactor(agent): 新增 agent 包并上移共享类型" -m "StreamEvent/Message/ContentBlock/ImageData 迁至 backend/agent,claude 包保留类型别名,既有 import 与测试零改动。新增 Delta/ResultIsError 字段与 Protocol 接口(尚无实现接线)。Event 字段作为过渡保留,由 Task 5 删除。"
```

---

## Task 2: `ClaudeProtocol` 旗标与 env

把 `security.go` 的 `BuildSecureArgs`/`BuildSecureEnv` 实体迁进 `agent` 包,`security.go` 保留转发壳以免波及 `api/`、`ingest/` 的调用点。

**Files:**
- Create: `backend/agent/claude_args.go`
- Create: `backend/agent/claude_args_test.go`
- Modify: `backend/claude/security.go`

- [ ] **Step 1: 写失败的测试**

创建 `backend/agent/claude_args_test.go`。用例从 `backend/claude/security_test.go` 的 `TestBuildSecureArgs_*`、`TestBuildSecureEnv_ResolvesSymlinks` 平移(断言逐条照抄,只把被测函数换成方法):

```go
package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClaudeSessionArgs_AlwaysContainsDisallowedTools(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("sys prompt", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--disallowedTools") {
		t.Fatalf("expected --disallowedTools in args: %v", args)
	}
	for _, dangerous := range ClaudeDangerousDisallowedTools {
		if !strings.Contains(joined, dangerous) {
			t.Errorf("expected dangerous tool %q in --disallowedTools, got: %v", dangerous, args)
		}
	}
}

func TestClaudeSessionArgs_AllowedToolsRespected(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("", []string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "Read,Glob,Grep,LS") {
		t.Fatalf("expected joined allowlist in args: %v", args)
	}
}

func TestClaudeSessionArgs_RejectsAllowedDangerousOverlap(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	if _, err := p.SessionArgs("", []string{"Read", "Bash"}); err == nil {
		t.Fatal("expected error when allowedTools contains a dangerous tool")
	}
}

func TestClaudeSessionArgs_ContainsStreamJSONFlags(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"--output-format", "stream-json", "--input-format", "--verbose",
		"--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
}

func TestClaudeResumeArgs_ContainsResumeID(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.ResumeArgs("prev-id-123", "", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || args[i+1] != "prev-id-123" {
		t.Fatalf("expected --resume prev-id-123 in args: %v", args)
	}
}

func TestClaudeEnv_ResolvesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(filepath.Dir(tmp), "agent-env-link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	defer os.Remove(link)

	p := &ClaudeProtocol{bin: "claude"}
	env := p.Env(link)
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := "ALLOWED_DIR=" + resolved
	if !slices.Contains(env, want) {
		t.Fatalf("expected %q in env, got: %v", want, env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "ALLOWED_DIR=") && e != want {
			t.Fatalf("duplicate/shadowing ALLOWED_DIR entry: %q", e)
		}
	}
}

func TestClaudeEnv_EmptyDirReturnsNil(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	if env := p.Env(""); env != nil {
		t.Fatalf("expected nil env for empty allowedDir, got: %v", env)
	}
}
```

- [ ] **Step 2: 运行测试验证失败**

运行:`cd backend && go test ./agent/ -run TestClaude -v`
预期:编译失败,`undefined: ClaudeProtocol`、`undefined: ClaudeDangerousDisallowedTools`

- [ ] **Step 3: 写 `claude_args.go`**

创建 `backend/agent/claude_args.go`,实体从 `backend/claude/security.go` 的 `BuildSecureArgs`(security.go 内)与 `BuildSecureEnv` 逐行搬移,仅改receiver与导出名:

```go
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ClaudeProtocol 是 Claude Code CLI 的 Protocol 实现。
type ClaudeProtocol struct {
	bin          string
	settingsPath string // 可为空;非空时追加 --settings
}

// NewClaudeProtocol 构造 Claude 后端。settingsPath 来自 claude.GetSettingsPath()。
func NewClaudeProtocol(bin, settingsPath string) *ClaudeProtocol {
	return &ClaudeProtocol{bin: bin, settingsPath: settingsPath}
}

func (p *ClaudeProtocol) Backend() Backend { return BackendClaude }
func (p *ClaudeProtocol) Bin() string      { return p.bin }

// ClaudeDangerousDisallowedTools 是生产会话中绝不允许的工具。
// --disallowedTools 优先级高于 --dangerously-skip-permissions,故列在此处即硬阻断。
//
// 该列表必须与 scripts/path-validator.py 的 ALWAYS_DENIED_TOOLS 保持一致,
// 由 backend/claude/security_test.go 的 TestDangerousToolsCrossLanguageSync 守护。
var ClaudeDangerousDisallowedTools = []string{
	"Bash",
	"Task",
	"NotebookEdit",
	"KillShell",
	"BashOutput",
	"SlashCommand",
}

// SessionArgs 构造多轮交互会话的旗标(stream-json 双向)。
func (p *ClaudeProtocol) SessionArgs(sysPrompt string, tools []string) ([]string, error) {
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}
	secure, err := p.secureArgs(tools)
	if err != nil {
		return nil, err
	}
	args = append(args, secure...)
	if sysPrompt != "" {
		args = append(args, "--system-prompt", sysPrompt)
	}
	return args, nil
}

// ResumeArgs 在 SessionArgs 基础上前置 --resume <id>。
func (p *ClaudeProtocol) ResumeArgs(prevSessionID, sysPrompt string, tools []string) ([]string, error) {
	args, err := p.SessionArgs(sysPrompt, tools)
	if err != nil {
		return nil, err
	}
	return append([]string{"--resume", prevSessionID}, args...), nil
}

func (p *ClaudeProtocol) secureArgs(allowedTools []string) ([]string, error) {
	for _, t := range allowedTools {
		if slices.Contains(ClaudeDangerousDisallowedTools, t) {
			return nil, fmt.Errorf("secureArgs: allowedTools contains dangerous tool %q; "+
				"this conflicts with --disallowedTools and must be a programming error", t)
		}
	}

	args := make([]string, 0, 8)
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowedTools, ","))
	}
	args = append(args,
		"--disallowedTools", strings.Join(ClaudeDangerousDisallowedTools, ","),
		"--dangerously-skip-permissions",
	)
	if p.settingsPath != "" {
		args = append(args, "--settings", p.settingsPath)
	}
	return args, nil
}

// Env 构造子进程环境:继承当前环境,剔除既有 ALLOWED_DIR 后注入解析过的绝对路径。
//
// allowedDir 经 filepath.EvalSymlinks 解析,以匹配 path-validator.py 自身 realpath()
// 的结果。否则调用方传 /tmp/foo(macOS 上是 /private/tmp/foo 的符号链接)会与 hook
// 解析出的 /private/tmp/foo 不一致,导致每一次合法读取都被拒。
func (p *ClaudeProtocol) Env(allowedDir string) []string {
	if allowedDir == "" {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(allowedDir); err == nil {
		allowedDir = resolved
	}

	baseEnv := os.Environ()
	filtered := make([]string, 0, len(baseEnv))
	for _, e := range baseEnv {
		if !strings.HasPrefix(e, "ALLOWED_DIR=") {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, fmt.Sprintf("ALLOWED_DIR=%s", allowedDir))
}

// OnceArgs 构造一次性调用的旗标。
// print=true 时用 --print + stream-json(Claude 的 Send 路径);
// print=false 时只用 -p(纯文本输出,对应 SendSimpleWithRead)。
func (p *ClaudeProtocol) OnceArgs(tools []string, print bool) ([]string, error) {
	secure, err := p.SecureArgs(tools)
	if err != nil {
		return nil, err
	}
	if print {
		return append([]string{"--print", "--output-format", "stream-json", "--verbose"}, secure...), nil
	}
	return append([]string{"-p"}, secure...), nil
}

// Probe 探测 CLI 是否可用。Claude 后端保持既有行为(不做探测),
// 因此返回 nil。Plan 2 的 PiProtocol 会实现为 LookPath + `pi --version`。
func (p *ClaudeProtocol) Probe(ctx context.Context) error { return nil }
```

- [ ] **Step 4: 运行测试验证通过**

运行:`cd backend && go test ./agent/ -run TestClaude -v`
预期:7 个用例全部 PASS

本步同时使 `ClaudeProtocol` 满足 `Protocol` 接口的旗标部分。编译期验证接口实现:在 `claude_args.go` 末尾加一行

```go
var _ Protocol = (*ClaudeProtocol)(nil)
```

此时会**编译失败**,因为 `EncodeUserMessage`/`EncodeInterrupt`/`ParseLine` 尚未实现(Task 3、4 补上)。因此本行**先不要加**,留到 Task 4 Step 9 的闸门之后再加。

- [ ] **Step 5: `security.go` 改为转发壳**

`backend/claude/security.go` 里:

删除 `DangerousDisallowedTools` 变量定义,替换为:

```go
// DangerousDisallowedTools 已迁至 agent.ClaudeDangerousDisallowedTools,保留别名。
var DangerousDisallowedTools = agent.ClaudeDangerousDisallowedTools
```

删除 `BuildSecureArgs` 与 `BuildSecureEnv` 的函数体,替换为转发:

```go
// BuildSecureArgs 转发到 agent.ClaudeProtocol,保留以兼容既有调用点。
func BuildSecureArgs(allowedTools []string) ([]string, error) {
	return NewProtocol().secureArgsForTest(allowedTools)
}
```

**注意:** 上面这行需要 `claude` 包能拿到一个 `ClaudeProtocol`。在 `backend/claude/client.go` 增加包级构造函数(Task 4 会让它可注入):

```go
// NewProtocol 返回当前唯一的后端实现(Claude)。Plan 2 会改为从 agent.Current() 解析。
func NewProtocol() agent.Protocol {
	return agent.NewClaudeProtocol("claude", GetSettingsPath())
}
```

由于 `secureArgs` 是小写不可跨包调用,`BuildSecureArgs` 应改为直接调用导出方法:

```go
// BuildSecureArgs 转发到 agent.ClaudeProtocol.secureArgs 的导出等价物。
func BuildSecureArgs(allowedTools []string) ([]string, error) {
	return agent.NewClaudeProtocol("claude", GetSettingsPath()).SecureArgs(allowedTools)
}

// BuildSecureEnv 转发到 agent.ClaudeProtocol.Env。
func BuildSecureEnv(allowedDir string) []string {
	return agent.NewClaudeProtocol("claude", GetSettingsPath()).Env(allowedDir)
}
```

并把 `agent/claude_args.go` 里的 `secureArgs` 改名为导出的 `SecureArgs`(同步改 `SessionArgs`/`ResumeArgs` 内的调用点与测试)。

`security.go` 的 import 块加入 `"llm-knowledge/agent"`。`SecurityConfig`、`InitSecurityConfig`、`generateSettingsFile`、`cleanupStaleSettings`、`GetSettingsPath`、`CleanupSecuritySettings`、`HookMatcher`、`Hook` **全部保留不动**。

- [ ] **Step 6: 运行闸门**

运行:`cd backend && go build ./... && go vet ./... && go test ./...`
预期:全部 PASS。`security_test.go` 的 `TestBuildSecureArgs_*` / `TestBuildSecureEnv_*` 经转发壳仍然通过;`TestDangerousToolsCrossLanguageSync` 仍然通过(它比对 `DangerousDisallowedTools` 与 Python 的 `ALWAYS_DENIED_TOOLS`,别名指向同一底层值)。

**若 `TestBuildSecureArgs_BypassFlagPresent` 失败**:原 `BuildSecureArgs` 返回的是**纯安全旗标**(不含 `--output-format` 等),而 `SecureArgs` 语义相同,应仍通过。若断言的是完整会话旗标,说明该用例其实测的是 `SessionArgs`,改为调用 `SessionArgs` 并同步调整断言。

- [ ] **Step 7: Commit**

```bash
git add backend/agent/claude_args.go backend/agent/claude_args_test.go backend/claude/security.go backend/claude/client.go
git commit -m "refactor(agent): ClaudeProtocol 承接旗标与 env 构造" -m "BuildSecureArgs/BuildSecureEnv 实体迁至 agent.ClaudeProtocol,claude 包保留转发壳,api/ 与 ingest/ 调用点零改动。"
```

---

## Task 3: `ClaudeProtocol.ParseLine` + `Delta` + `StreamProcessor` 重构

本任务是整个计划的原子核心:解析、归一化、消费三处必须同时切换,否则测试无法保持绿色。

**Files:**
- Create: `backend/agent/claude_parse.go`
- Create: `backend/agent/claude_parse_test.go`
- Modify: `backend/claude/stream.go:153-300`(`Process`)、`stream.go:302-395`(删除四个 `Extract*`)
- Modify: `backend/claude/session.go:536-620`(`readEvents`)
- Modify: `backend/claude/stream_test.go`(四个 fixture helper 定型)

- [ ] **Step 1: 写失败的解析测试**

创建 `backend/agent/claude_parse_test.go`。用例从 `backend/claude/stream_test.go` 的 `TestExtractTextDelta`、`TestExtractTextDelta_ThinkingIgnored`、`TestExtractToolUseStart`、`TestExtractToolUseInputDelta`、`TestExtractContentBlockStop` 平移,改为断言 `ParseLine` 产出的 `Delta`:

```go
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
	evt, ok := p.ParseLine(wrap(t, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`))
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
```

- [ ] **Step 2: 运行测试验证失败**

运行:`cd backend && go test ./agent/ -run TestParseLine -v`
预期:编译失败,`p.ParseLine undefined`、`evt.ResultIsError undefined`

- [ ] **Step 3: 写 `claude_parse.go`**

(`ResultIsError` 字段已在 Task 1 Step 1 定义,本任务直接使用。它存在的理由:原 `session.go` 的 `readEvents` 在函数内直接读 `rawEvent.IsError` 并把 `event.Type` 改写成 `"error"`;该逻辑属 Claude 专有解析,应下沉到 `ParseLine`,故需一个归一化字段承载。)

创建 `backend/agent/claude_parse.go`。四个 `content_block_*` 解析函数从 `backend/claude/stream.go:302-395` 逐行搬移,仅改为包内私有并返回 `*Delta`:

```go
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
		Type         string `json:"type"`
		Index        int    `json:"index"`
		Delta        struct {
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
```

- [ ] **Step 4: 运行解析测试验证通过**

运行:`cd backend && go test ./agent/ -run TestParseLine -v`
预期:10 个用例全部 PASS

- [ ] **Step 5: 重构 `StreamProcessor.Process` 消费 `Delta`**

`backend/claude/stream.go` 的 `Process`,把 `case "stream_event":` 整个分支(原 155-201 行)替换为:

```go
	if evt.Delta != nil {
		switch evt.Delta.Kind {
		case DeltaToolStart:
			sp.activeTools[evt.Delta.Index] = &activeTool{
				id:   evt.Delta.ToolID,
				name: evt.Delta.ToolName,
			}
			sp.sentToolIDs[evt.Delta.ToolID] = true
			return SSEEvent{
				Type:     "tool_start",
				ToolID:   evt.Delta.ToolID,
				ToolName: evt.Delta.ToolName,
			}

		case DeltaToolInput:
			if tool, ok := sp.activeTools[evt.Delta.Index]; ok {
				tool.input += evt.Delta.ToolInput
				return SSEEvent{
					Type:      "tool_input",
					ToolID:    tool.id,
					ToolName:  tool.name,
					ToolInput: tool.input,
				}
			}
			return SSEEvent{}

		case DeltaToolEnd:
			if tool, ok := sp.activeTools[evt.Delta.Index]; ok {
				toolID := tool.id
				delete(sp.activeTools, evt.Delta.Index)
				return SSEEvent{Type: "tool_end", ToolID: toolID}
			}
			return SSEEvent{}

		case DeltaText:
			sp.streamedDeltas = true
			return SSEEvent{Type: "delta", Delta: evt.Delta.Text}
		}
		return SSEEvent{}
	}
```

**位置:** 放在 `switch evt.Type {` **之前**(作为独立 `if`),因为 `Delta` 与 `Type` 是正交的两个维度,pi 后端的 `Type` 取值与 Claude 不同。

同一函数内,`case "assistant":` 分支里删除这段死代码(原 240-249 行):

```go
		// assistant with raw Event field (no parsed Message)
		content := ExtractAssistantContent(evt.Event)
		if content != "" && !sp.streamedDeltas {
			return SSEEvent{Type: "full", Content: content}
		}
		if ev := sp.checkSSEReconnectExtension(content); ev.Type != "" {
			return ev
		}
		return SSEEvent{}
```

替换为:

```go
		// evt.Message 为 nil 时无内容可下发。
		// (原先此处会回退去挖 raw Event,但生产代码只在 stream_event 行填充 Event,
		// assistant 行必然已解析出 Message;测试也只覆盖 *FromMsg 变体。属死路径,
		// 随 StreamEvent.Event 字段一并移除。)
		return SSEEvent{}
```

`case "result":` 分支保持不变(它读 `evt.Type`,不依赖 `Event`)。

- [ ] **Step 6: 删除四个 stream_event `Extract*` 函数**

`backend/claude/stream.go` 删除:`ExtractTextDelta`、`ExtractToolUseStart`、`ExtractToolUseInputDelta`、`ExtractContentBlockStop`(原 302-395 行),以及随之无用的 `ToolUseStart`、`ContentBlockStop` 两个类型(原 35-45 行)。

**保留**:`ExtractAssistantContent`、`ExtractAssistantContentFromMsg`、`ExtractToolUseFromAssistant`、`ExtractToolUseFromAssistantMsg`、`ToolUseBlock` —— 它们操作的是已归一化的 `*Message` 或 assistant 原始消息 JSON。

**但** `ExtractAssistantContent(msgRaw)` 与 `ExtractToolUseFromAssistant(msgRaw)` 的唯一调用点刚在 Step 6 被删除。检查是否还有其他调用点:

运行:`cd backend && grep -rn "ExtractAssistantContent(\|ExtractToolUseFromAssistant(" --include=*.go .`

若只剩 `_test.go` 的调用,则一并删除这两个函数与其测试(`TestExtractAssistantContent`、`TestExtractToolUseFromAssistant` 若存在),并在 commit message 中说明。若仍有生产调用点,保留。

- [ ] **Step 7: `readEvents` 委托 `ParseLine`**

`backend/claude/session.go` 的 `readEvents`,把整个循环体开头(原 537-608 行,从 `var rawEvent struct {...}` 到 `// Handle result type` 之前)替换为:

```go
	for s.stdoutScanner.Scan() {
		line := s.stdoutScanner.Bytes()

		event, ok := s.proto.ParseLine(line)
		if !ok {
			continue
		}

		// 累积 assistant 文本用于 SSE 重连恢复(若已收到 delta 则跳过)
		if event.Type == "assistant" && event.Content != "" {
			s.mu.Lock()
			if !s.hasStreamDeltas {
				s.streamingContent.WriteString(event.Content)
			}
			s.mu.Unlock()
		}

		// 累积文本 delta 用于重连恢复
		if event.Delta != nil && event.Delta.Kind == agent.DeltaText && event.Delta.Text != "" {
			s.mu.Lock()
			s.hasStreamDeltas = true
			s.streamingContent.WriteString(event.Delta.Text)
			s.mu.Unlock()
		}
```

紧接其后的 `// Handle result type` 块改为读归一化字段:

```go
		if event.Type == "result" {
			event.Content = event.Result
			if event.ResultIsError {
				event.Type = "error"
				event.Error = event.Result
			}
			s.mu.Lock()
			s.streamingContent.Reset()
			s.hasStreamDeltas = false
			s.mu.Unlock()
		}
```

`// Auto-capture session_id from system.init event` 及其后的逻辑**保持不变**(它读 `event.Type`/`event.Subtype`/`event.SessionID`,均已归一化)。

同时删除本任务 Step 3(上一任务)遗留的 `eventRaw := rawEvent.Event` 局部变量与 `rawEvent` 结构体定义。`session.go` 的 import 需含 `"llm-knowledge/agent"`;若 `encoding/json` 在本文件已无其他用途则移除该 import。

- [ ] **Step 8: 给 `InteractiveSession` 加 `proto` 字段**

`backend/claude/session.go` 的 `InteractiveSession` 结构体增加:

```go
	proto agent.Protocol
```

在 `SessionPool.StartSession` 与 `query_pool.go` 的 `StartSession`/`StartResumedSession` 构造 `&InteractiveSession{...}` 处补 `proto: proto`(见 Task 4 Step 3 的接线方式;本任务可先临时写 `proto: agent.NewClaudeProtocol(claudeBin, GetSettingsPath())` 以保持编译通过,Task 4 会统一)。

- [ ] **Step 9: 定型 `stream_test.go` 的四个 fixture helper**

`backend/claude/stream_test.go` 的 494、516、526、534 行四个 helper,改为直接构造 `Delta`。以 text_delta helper 为例:

```go
func textDeltaEvent(index int, text string) StreamEvent {
	return StreamEvent{
		Type:  "stream_event",
		Delta: &agent.Delta{Kind: agent.DeltaText, Index: index, Text: text},
	}
}

func toolStartEvent(index int, id, name string) StreamEvent {
	return StreamEvent{
		Type:  "stream_event",
		Delta: &agent.Delta{Kind: agent.DeltaToolStart, Index: index, ToolID: id, ToolName: name},
	}
}

func toolInputEvent(index int, partial string) StreamEvent {
	return StreamEvent{
		Type:  "stream_event",
		Delta: &agent.Delta{Kind: agent.DeltaToolInput, Index: index, ToolInput: partial},
	}
}

func toolEndEvent(index int) StreamEvent {
	return StreamEvent{
		Type:  "stream_event",
		Delta: &agent.Delta{Kind: agent.DeltaToolEnd, Index: index},
	}
}
```

把原先调用这四个 helper 的测试改为调用新签名(**保持每个测试的断言不变** —— 断言的是 `SSEEvent` 输出,与输入构造方式无关)。

删除 `stream_test.go` 中的 `TestExtractTextDelta`、`TestExtractTextDelta_ThinkingIgnored`、`TestExtractToolUseStart`、`TestExtractToolUseInputDelta`、`TestExtractContentBlockStop`(已平移至 `agent/claude_parse_test.go`)。

- [ ] **Step 10: 运行闸门**

运行:`cd backend && go build ./... && go vet ./... && go test ./...`
预期:全部 PASS。特别确认 `stream_test.go` 剩余的用例全绿:
`TestSSEEvent_JSONTags`、`TestStreamProcessor_ClaudeStreaming_*`(5 个)、`TestStreamProcessor_GLM_*`(2 个)、`TestStreamProcessor_Qwen_DeltasThenDuplicateAssistant`、`TestStreamProcessor_SSEReconnect_*`(3 个)、`TestStreamProcessor_ToolStartFromStreamEvent`、`TestStreamProcessor_ToolInputDelta`、`TestStreamProcessor_ToolEndFromStreamEvent`、`TestStreamProcessor_ToolFromAssistantMessage`、`TestStreamProcessor_MultipleToolFromAssistant`、`TestExtractAssistantContentFromMsg`、`TestExtractToolUseFromAssistantMsg`、`TestStreamProcessor_Reset`、`TestStreamProcessor_Error`、`TestStreamProcessor_MarkAsStreamedWithEmptyContent_DoesNotSuppressNextTurn`

- [ ] **Step 11: Commit**

```bash
git add backend/agent/ backend/claude/stream.go backend/claude/session.go backend/claude/stream_test.go
git commit -m "refactor(agent): ParseLine 产出归一化 Delta,StreamProcessor 改消费 Delta" -m "ClaudeProtocol.ParseLine 承接原 readEvents 的行解析与四个 content_block_* Extract 函数。
StreamEvent.Event(json.RawMessage)由 Delta 取代,新增 ResultIsError 承载 result.is_error。
移除 Process 中 assistant+raw Event 的死回退分支(生产只在 stream_event 行填 Event)。
SSEEvent 线格式与前端契约不变。"
```

---

## Task 4: stdin 编码与旗标的委托接线

让 `InteractiveSession` 与 `Client` 通过注入的 `Protocol` 工作,而不是直接构造 Claude 旗标。结束时 `query_pool.go` 与 `session.go` 内不再出现任何 Claude 专有旗标字面量。

**Files:**
- Create: `backend/agent/claude_encode.go`
- Create: `backend/agent/claude_encode_test.go`
- Modify: `backend/claude/session.go`(`SendUserMessage`、`SendUserMessageWithImages`、`SendInterrupt`、`buildCmdWithEnv`、两个 Start 函数)
- Modify: `backend/claude/query_pool.go:478-600`(`StartSession`、`StartResumedSession`)
- Modify: `backend/claude/client.go`(`Client` 增 `Proto` 字段)

- [ ] **Step 1: 写失败的编码测试**

创建 `backend/agent/claude_encode_test.go`:

```go
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
	src, ok := got.Message.Content[0]["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected image source object, got %v", got.Message.Content[0]["source"])
	}
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AAAA" {
		t.Errorf("unexpected image source: %v", src)
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
```

- [ ] **Step 2: 运行测试验证失败**

运行:`cd backend && go test ./agent/ -run TestClaudeEncode -v`
预期:编译失败,`p.EncodeUserMessage undefined`

- [ ] **Step 3: 写 `claude_encode.go`**

创建 `backend/agent/claude_encode.go`,逻辑从 `backend/claude/session.go` 的 `SendUserMessage`(312-341)、`SendUserMessageWithImages`(343-394)、`SendInterrupt`(396-427)搬移,只把"写 stdin"改为"返回字节":

```go
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

```

`claude_encode.go` 的 import 只需 `encoding/json`、`fmt`、`time`。

- [ ] **Step 4: 运行编码测试验证通过**

运行:`cd backend && go test ./agent/ -run TestClaudeEncode -v`
预期:5 个用例全部 PASS

- [ ] **Step 5: `SendUserMessage*` / `SendInterrupt` 改为委托**

`backend/claude/session.go`,把 `SendUserMessage` 整个函数体替换为:

```go
func (s *InteractiveSession) SendUserMessage(content string) error {
	return s.sendEncoded(func() ([]byte, error) {
		return s.proto.EncodeUserMessage(content, nil)
	}, "message")
}
```

`SendUserMessageWithImages` 替换为:

```go
func (s *InteractiveSession) SendUserMessageWithImages(content string, images []ImageData) error {
	return s.sendEncoded(func() ([]byte, error) {
		return s.proto.EncodeUserMessage(content, images)
	}, fmt.Sprintf("%d image(s)", len(images)))
}
```

`SendInterrupt` 替换为:

```go
func (s *InteractiveSession) SendInterrupt() error {
	return s.sendEncoded(s.proto.EncodeInterrupt, "interrupt")
}
```

新增私有 helper(放在这三个方法之后):

```go
// sendEncoded 在持有 s.mu 的情况下把已编码的行写入 stdin。
// what 仅用于日志。
func (s *InteractiveSession) sendEncoded(encode func() ([]byte, error), what string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := encode()
	if err != nil {
		log.Printf("[session] Failed to encode %s: %v", what, err)
		return err
	}
	if _, err := s.stdin.Write(data); err != nil {
		log.Printf("[session] Failed to send %s: %v", what, err)
		return err
	}
	log.Printf("[session] Sent %s to session %s", what, s.SessionID)
	return nil
}
```

`session.go` 的 import 需含 `"fmt"` 与 `"log"`(既有)。若 `encoding/json` 在本文件已无其他用途则移除。

- [ ] **Step 6: `buildCmdWithEnv` 改为委托 `Env`**

`backend/claude/session.go` 的 `buildCmdWithEnv` 保持不变(它只接收已构造好的 `extraEnv`)。改动在**调用点**:`SessionPool.StartSession`、`query_pool.go` 的 `StartSession`/`StartResumedSession` 里,把

```go
	env := BuildSecureEnv(userDir)
```

改为

```go
	env := proto.Env(userDir)
```

- [ ] **Step 7: 两个 Start 函数改用 `SessionArgs`/`ResumeArgs`**

`backend/claude/query_pool.go` 的 `StartSession`(478-552),把从 `secureArgs, err := BuildSecureArgs(...)` 到 `args = append(args, "--system-prompt", systemPrompt)` 的整段替换为:

```go
	proto := agent.NewClaudeProtocol(claudeBin, GetSettingsPath())
	args, err := proto.SessionArgs(systemPrompt, []string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		return nil, fmt.Errorf("build session args: %w", err)
	}

	env := proto.Env(userDir)
```

同文件 `StartResumedSession`(554-600)同样替换,但用 `ResumeArgs`:

```go
	proto := agent.NewClaudeProtocol(claudeBin, GetSettingsPath())
	args, err := proto.ResumeArgs(prevSessionID, systemPrompt, []string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		return nil, fmt.Errorf("build resume args: %w", err)
	}

	env := proto.Env(userDir)
```

`backend/claude/session.go` 的 `SessionPool.StartSession`(171-278)同样处理,但工具白名单是 `[]string{"Read"}`,且 resume 是条件的:

```go
	proto := agent.NewClaudeProtocol(claudeBin, GetSettingsPath())

	resuming := prevSessionID != "" && !strings.HasPrefix(prevSessionID, "local-")
	var args []string
	if resuming {
		args, err = proto.ResumeArgs(prevSessionID, systemPrompt, []string{"Read"})
	} else {
		args, err = proto.SessionArgs(systemPrompt, []string{"Read"})
	}
	if err != nil {
		return nil, fmt.Errorf("build session args: %w", err)
	}

	env := proto.Env(workDir)
```

注意此处 `systemPrompt` 的构造(`fmt.Sprintf("用户正在询问文档相关问题。%s ...", docInfo)`)必须**在**上述代码之前完成,保持原有顺序。

三处构造 `&InteractiveSession{...}` 时补上 `proto: proto`。

- [ ] **Step 8: `Client` 增 `Proto` 字段**

`backend/claude/client.go`:

```go
type Client struct {
	BinPath string        // Path to the claude binary (e.g., "claude" or "/usr/local/bin/claude")
	Proto   agent.Protocol // 协议实现;nil 时按 BinPath 惰性构造 ClaudeProtocol
}
```

`NewClient` 与 `NewClientWithPath` 保持不变(它们只设 `BinPath`),另增:

```go
// protocol 返回生效的 Protocol,nil 时按 BinPath 惰性构造。
func (c *Client) protocol() agent.Protocol {
	if c.Proto != nil {
		return c.Proto
	}
	return agent.NewClaudeProtocol(c.BinPath, GetSettingsPath())
}
```

`Send`(65-160)里把 `BuildSecureArgs([]string{"Read","Write","Edit"})` 与手写的 `--print`/`--output-format`/`--verbose` 旗标替换为:

```go
	args, err := c.protocol().OnceArgs([]string{"Read", "Write", "Edit"}, true)
	if err != nil {
		return fmt.Errorf("build once args: %w", err)
	}
```

`OnceArgs` 已在 Task 2 Step 3 写入 `agent/claude_args.go` 并列入 `Protocol` 接口(Task 1 Step 1),本步只需调用。

`SendSimpleWithRead`(181-208)改用 `c.protocol().OnceArgs([]string{"Read"}, false)`。
`SendSimple`(165-179)与 `SendWithOutput`(239-250)保持 `-p` 字面量不变 —— 它们不带任何安全旗标,这是既有行为,不在本次重构范围内改动。

`Send` 里 `cmd.Env` 的设置改为 `if env := c.protocol().Env(workDir); len(env) > 0 { cmd.Env = env }`。

- [ ] **Step 9: 加接口实现断言 + 运行闸门**

`ClaudeProtocol` 至此已实现 `Protocol` 的全部 10 个方法:`Backend`、`Bin`、`SessionArgs`、`ResumeArgs`、`OnceArgs`、`Env`、`EncodeUserMessage`、`EncodeInterrupt`、`ParseLine`、`Probe`。在 `backend/agent/claude_args.go` 末尾加编译期断言:

```go
var _ Protocol = (*ClaudeProtocol)(nil)
```

运行:`cd backend && go build ./... && go vet ./... && go test ./...`
预期:全部 PASS。若断言报 `missing method`,说明某个方法签名与接口不一致 —— 以 `agent/agent.go` 的接口定义为准修正实现,**不要**改接口迁就实现。

- [ ] **Step 10: 验证旗标字面量已彻底移出**

运行:`cd backend && grep -rn '"--output-format"\|"--input-format"\|"--dangerously-skip-permissions"\|"--allowedTools"\|"--disallowedTools"' claude/ api/ ingest/`
预期:**只**在 `claude/client.go` 的 `SendSimple`/`SendWithOutput` 里出现 `"-p"`(不含上述任何旗标),其余命中应全部落在 `agent/` 包内。

运行:`cd backend && grep -rn '"stream-json"' claude/ api/ ingest/`
预期:0 命中。

- [ ] **Step 11: Commit**

```bash
git add backend/agent/ backend/claude/session.go backend/claude/query_pool.go backend/claude/client.go
git commit -m "refactor(agent): 会话与一次性调用全面委托 Protocol" -m "SendUserMessage/SendUserMessageWithImages/SendInterrupt 改走 EncodeUserMessage/EncodeInterrupt;
三处 Start 函数改走 SessionArgs/ResumeArgs/Env;Client 增 Proto 字段与 OnceArgs。
claude/api/ingest 包内不再出现 Claude 专有旗标字面量(SendSimple/SendWithOutput 的裸 -p 除外,属既有行为)。"
```

---

## Task 5: 删除 `Event` 过渡字段与全量回归

前四个任务结束后,`StreamEvent.Event` 已无生产者(`ParseLine` 不写)也无消费者(`Process` 只读 `Delta`),`session.go` 与 `stream_test.go` 均已改为 `Delta` 路径。本任务把这个过渡字段及其残留一并删除,并做全量回归。

**Files:**
- Modify: `backend/agent/agent.go`(删 `Event` 字段)
- Modify: `backend/claude/stream.go`(视 Step 3 的 grep 结果)
- Modify: `backend/claude/security_test.go`(确认覆盖无遗漏)

- [ ] **Step 1: 确认 `Event` 已无生产者也无消费者**

运行:`cd backend && grep -rn "\.Event\b\|Event:" claude/ agent/ api/ ingest/ --include=*.go`

预期命中且**仅**命中以下三类,其余均需先查清再继续:
- `agent/agent.go` 里 `Event json.RawMessage` 的字段定义本身
- `agent/claude_parse.go` 里 `claudeRawEvent.Event` 与其 `json.Unmarshal` 目标(这是 Claude 专有中间类型,**保留**)
- `SSEEvent`、`ToolUseBlock` 等同后缀标识符(无关,保留)

若发现 `claude/` 包内仍有读写 `event.Event` / `evt.Event` 的代码,说明 Task 3 未清理干净 —— **先回去补,不要在本任务里顺手改**。

- [ ] **Step 2: 删除 `StreamEvent.Event` 字段**

`backend/agent/agent.go` 的 `StreamEvent` 里删除整段:

```go
	// Event 是 Claude 专有的 stream_event 原始载荷。它在本重构中是
	// **过渡字段**:Task 1-4 期间与 Delta 并存以保证每个任务收尾全绿,
	// Task 5 将其连同消费方一并删除。新增代码不得读取它。
	Event json.RawMessage
```

若删除后 `agent.go` 不再使用 `encoding/json`,**不要**移除该 import —— `ContentBlock.Input json.RawMessage` 仍在用。

- [ ] **Step 3: 清理随之孤立的 `Extract*` 函数**

Task 3 Step 5 删掉了 `Process` 中 `assistant` + raw `Event` 的回退分支,那是 `ExtractAssistantContent(msgRaw)` 与 `ExtractToolUseFromAssistant(msgRaw)` 的唯一生产调用点。确认是否已成孤儿:

运行:`cd backend && grep -rn "ExtractAssistantContent(\|ExtractToolUseFromAssistant(" --include=*.go .`

- **若只剩 `_test.go` 命中** → 删除这两个函数(`stream.go`)及其对应测试,并删除 `stream_test.go` 里仅为它们服务的 fixture
- **若仍有生产调用点** → 保留,并在 commit message 里说明调用方是谁

注意区分同名的 `*FromMsg` 变体:`ExtractAssistantContentFromMsg(*Message)` 与 `ExtractToolUseFromAssistantMsg(*Message)` 操作的是已归一化的 `*Message`,**它们有生产调用点(`Process` 的 `assistant` 分支),必须保留**。

- [ ] **Step 4: 确认 `security_test.go` 覆盖无遗漏**

运行:`cd backend && grep -n "^func Test" claude/security_test.go agent/claude_args_test.go`

`security_test.go` 里与 `agent/claude_args_test.go` 语义重复的用例(`TestBuildSecureArgs_AlwaysContainsDisallowedTools`、`TestBuildSecureArgs_AllowedToolsRespected`、`TestBuildSecureArgs_RejectsAllowedDangerousOverlap`、`TestBuildSecureEnv_ResolvesSymlinks`)**保留**,因为它们测的是 `claude` 包的转发壳,与 `agent` 包直测互补。

`TestCleanupStaleSettings_AgeGated`、`TestPathValidator_WebFetchSSRF`、`TestDangerousToolsCrossLanguageSync`、`TestBuildSecureArgs_BypassFlagPresent`、`TestBuildSecureArgs_EmptyAllowedToolsOmitsFlag` **必须保留不动**(前三个测的是 settings 文件生成、Python 校验器、跨语言同步,与本次重构无关)。

- [ ] **Step 5: 前端零改动确认**

运行:`cd /Users/dingjing/Projects/llm_knowledge && git status --short frontend/`
预期:无输出。

运行:`cd /Users/dingjing/Projects/llm_knowledge && git diff --stat HEAD~5 -- frontend/`
预期:无输出(本计划五个 commit 均未触及前端)。

- [ ] **Step 6: 手工冒烟(claude 后端,行为等价验证)**

启动服务并验证文档问答与自由问答各一轮:

```bash
cd /Users/dingjing/Projects/llm_knowledge && ./start.sh
```

- 打开任一文档 → 文档问答面板提问 → 确认流式文本逐字出现、无重复、`done` 正常收尾
- 若该文档触发了工具调用(Read),确认 `tool_start`/`tool_input`/`tool_end` 三个 SSE 事件仍按序出现
- 自由问答发一张图片 → 确认图片被接受且回复正常
- 自由问答流式过程中点停止 → 确认 interrupt 生效

- [ ] **Step 7: 既有 e2e 回归**

运行:
```bash
cd /Users/dingjing/Projects/llm_knowledge && pytest tests/e2e/test_chat_streaming.py tests/e2e/test_document_chat_panel.py -v
```
预期:全绿。若因环境未启动服务而失败,先 `./start.sh` 再跑。

- [ ] **Step 8: 全量回归**

运行:
```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1
```
预期:全部 PASS,无 skip(除非环境缺 `python3`)。

运行:`cd backend && go test ./claude/ ./agent/ -count=1 -v 2>&1 | grep -c "^--- PASS"`
预期:数量 ≥ 改造前的 `claude` 包 PASS 数(迁出的 5 个 `TestExtract*` 变为 `agent` 包的 10 个 `TestParseLine_*` 与 7 个 `TestClaude*Args/Env`、5 个 `TestClaudeEncode*`,总数应明显增加)。

- [ ] **Step 9: Commit**

```bash
git add backend/agent/agent.go backend/claude/
git commit -m "refactor(agent): 删除 StreamEvent.Event 过渡字段与孤儿 Extract 函数" -m "ParseLine 已成为唯一的行解析入口,Process 只消费 Delta,Event 字段无生产者与消费者。"
```

若 Step 1-3 未产生任何改动(理论上不可能,`Event` 字段至少要从 `agent.go` 删掉),跳过本步骤。

---

## 验证清单(全部完成后)

- [ ] `cd backend && go build ./... && go vet ./... && go test ./... -count=1` 全绿
- [ ] `cd backend && grep -rn '"stream-json"' claude/ api/ ingest/` → 0 命中
- [ ] `cd backend && grep -rn "dangerously-skip-permissions" claude/ api/ ingest/` → 0 命中(只在 `agent/` 内)
- [ ] `cd backend && grep -rn "\.Event\b" claude/ api/ ingest/ --include=*.go` → 0 命中(`agent/claude_parse.go` 内的 `claudeRawEvent.Event` 除外)
- [ ] `cd backend && grep -n "Event json.RawMessage" agent/agent.go` → 0 命中(过渡字段已删)
- [ ] `git diff --stat <base>..HEAD -- frontend/` → 无输出
- [ ] `scripts/path-validator.py`、`backend/dependencies/`、`CLAUDE.md`、`go.mod` 均未被修改
- [ ] 手工:文档问答流式无重复文本、工具事件三段齐全、`done` 正常
- [ ] 手工:自由问答图片输入与 interrupt 均正常
- [ ] `pytest tests/e2e/test_chat_streaming.py tests/e2e/test_document_chat_panel.py -v` 全绿
- [ ] `agent` 包内 `Protocol` 接口的 10 个方法均已被 `ClaudeProtocol` 实现(`var _ Protocol = (*ClaudeProtocol)(nil)` 编译通过),且无 Claude 专有旗标字面量残留在 `claude` 包

## 交接给 Plan 2

本计划完成后,`agent.Protocol` 已就位且 claude 行为逐条不变。Plan 2 在此基础上:

1. 实现 `PiProtocol`(旗标/编码/解析,依据规格里实测确认的三条 pi 专有约束)
2. 新增 `scripts/pi-path-validator.ts` 沙箱 extension
3. `agent.Current()` resolver + `GlobalSettings.LLMBackend` + Admin API 探测
4. `main.go` 去除 11 处 `ClaudeBin` 注入、`ingest` 形参改造
5. 前端 SettingsPage 开关 + i18n
6. `start.sh` / README

Plan 2 的每个任务都会替换本计划中 `agent.NewClaudeProtocol(claudeBin, GetSettingsPath())` 的硬编码构造点为 `agent.Current()`。
