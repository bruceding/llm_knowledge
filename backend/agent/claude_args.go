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
//
// WebFetch/WebSearch 故意不列入:它们有用且无法直接读本地文件;WebFetch 的 SSRF
// 风险另行跟踪(见 backend/claude/security_test.go 的 TestPathValidator_WebFetchSSRF)。
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
	secure, err := p.SecureArgs(tools)
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

// SecureArgs 返回强制本项目安全模型的那组旗标:工具白名单、危险工具黑名单、
// 权限绕过,以及(已配置时的)安全 settings 文件。
//
// 调用方在返回切片前后自行拼接 --output-format / --input-format / --print /
// --resume / --system-prompt。
//
// allowedTools 含 ClaudeDangerousDisallowedTools 中任一项时返回错误:那是编程错误
// (两个冲突旗标会把行为交给 CLI 内部决定),但我们以 error 而非 panic 暴露,
// 好让 goroutine 里的错误调用不会拖垮整个服务进程。
func (p *ClaudeProtocol) SecureArgs(allowedTools []string) ([]string, error) {
	for _, t := range allowedTools {
		if slices.Contains(ClaudeDangerousDisallowedTools, t) {
			return nil, fmt.Errorf("SecureArgs: allowedTools contains dangerous tool %q; "+
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
// print=false 时只用 -p(纯文本输出,对应 SendSimpleWithRead,prompt 由调用方追加)。
// sysPrompt 非空时把 --system-prompt 追加到末尾(secure 旗标之后)。
func (p *ClaudeProtocol) OnceArgs(sysPrompt string, tools []string, print bool) ([]string, error) {
	secure, err := p.SecureArgs(tools)
	if err != nil {
		return nil, err
	}
	args := []string{"-p"}
	if print {
		args = []string{"--print", "--output-format", "stream-json", "--verbose"}
	}
	args = append(args, secure...)
	if sysPrompt != "" {
		args = append(args, "--system-prompt", sysPrompt)
	}
	return args, nil
}

// Probe 探测 CLI 是否可用。Claude 后端保持既有行为(不做探测),
// 因此返回 nil。Plan 2 的 PiProtocol 会实现为 LookPath + `pi --version`。
func (p *ClaudeProtocol) Probe(ctx context.Context) error { return nil }

var _ Protocol = (*ClaudeProtocol)(nil)
