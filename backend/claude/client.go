package claude

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"llm-knowledge/agent"
	"os/exec"
	"strings"
)

// Client wraps the Claude CLI binary for programmatic invocation.
type Client struct {
	// Proto 是协议实现。nil 时由 protocol() 向 resolver 惰性取当前生效后端 ——
	// 这正是「Settings 里切后端」能对 once 调用链路生效的原因,所以**不要**给它
	// 加一个二进制路径字段:那会让某个调用点重新钉死后端(Plan 1 遗留的接缝缺口)。
	Proto agent.Protocol
}

// protocol 返回生效的 Protocol。
//
// c.Proto 非空时用它(显式注入,供测试与需要钉死后端的场合)。否则向 resolver 取
// 当前后端 —— **不得**在此直接 agent.NewClaudeProtocol(...):那是 Plan 1 遗留的
// 硬编码构造点,留着就等于后端开关对 Client 这条路径无效(Settings 切到 pi,而
// 摘要/分节/翻译/PDF 转换依旧 spawn claude,且不报错)。
// agent/invariant_test.go 会把这种泄漏判红。
func (c *Client) protocol() (agent.Protocol, error) {
	if c.Proto != nil {
		return c.Proto, nil
	}
	proto, err := agent.Current()
	if err != nil {
		return nil, fmt.Errorf("resolve agent backend: %w", err)
	}
	return proto, nil
}

// 以下类型已上移到 agent 包(见 docs/superpowers/specs/2026-09-12-pi-backend-switch-design.md)。
// 保留别名使既有 import 与测试零改动。
type (
	StreamEvent  = agent.StreamEvent
	Message      = agent.Message
	ContentBlock = agent.ContentBlock
)

// Send executes the Claude CLI with streaming JSON output.
// Events are sent to the provided channel as they are received.
// The caller should close the channel after Send returns.
// If workDir is non-empty, the command runs in that directory.
func (c *Client) Send(ctx context.Context, prompt string, eventCh chan<- StreamEvent, workDir string) error {
	proto, err := c.protocol()
	if err != nil {
		return err
	}
	args, err := proto.OnceArgs("", []string{"Read", "Write", "Edit"}, true, "")
	if err != nil {
		return fmt.Errorf("build once args: %w", err)
	}
	cmd := exec.CommandContext(ctx, proto.Bin(), args...)
	if workDir != "" {
		cmd.Dir = workDir
		if env := proto.Env(workDir); len(env) > 0 {
			cmd.Env = env
		}
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// stderr 必须接管:不设时 os/exec 会把它接到 /dev/null,子进程的失败原因就彻底丢了。
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	// Increase buffer size for large messages
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	emitted := 0

	for scanner.Scan() {
		line := scanner.Bytes()

		// 解析一律走 Protocol 接缝,不在这里认任何后端的线格式。
		//
		// 两个后端的事件词表**没有交集**:claude 是 assistant/result/system,pi 的
		// --mode json 是 message_update/message_end/agent_settled/response。改造前这里
		// 用 claude 的 RawEvent 解析,而 spawn 已经跟随 resolver —— 于是切到 pi 之后
		// 每个事件都落到 switch 之外,Content/Result 恒为空。后果不是「少一段文本」:
		// api/translate.go 会拿这个空串**覆盖已有的 paper_<lang>.md 并报成功**,
		// ingest 的进度与错误日志一起消失,而且 err 始终为 nil(静默失败)。
		event, ok := proto.ParseLine(line)
		if !ok {
			// 畸形 JSON:与改造前一致,跳过
			continue
		}

		switch event.Type {
		case "system":
			// 与改造前逐条等价:init/hook 之类的 system 帧不下发,只有 error 例外。
			// pi 侧的 get_state 响应会被归一化成 system/init,正是这里要挡掉的 ——
			// once 调用链路不需要 session id,下发它只会给 SSE 多出无意义的帧。
			if event.Subtype != "error" {
				continue
			}
			event.Type = "error"
		case "result":
			// claude 的 result 文本既作 Result 也作 Content(调用方两者都读);
			// ResultIsError 时转成 error,与改造前的 is_error 分支一致。
			// pi 的 agent_settled 归一化成**空** result(正文已由 message_end 下发),
			// 于是这里只起「回合结束」的作用,不会覆盖已有内容。
			event.Content = event.Result
			if event.ResultIsError {
				event.Type = "error"
				event.Error = event.Result
			}
		}

		// Send the event
		eventCh <- event
		emitted++
	}

	if err := scanner.Err(); err != nil {
		// Try to wait for the command even if scanner failed
		_ = cmd.Wait()
		return fmt.Errorf("error reading stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("claude command failed: %w", err)
	}

	// 零事件 + 有 stderr = 失败,即使退出码是 0。
	//
	// 实测 pi 0.85.1:模型/凭据不可用时,`--mode json` 只在 stdout 写一行 session 头
	// (被 ParseLine 跳过)、把真正的原因写在 stderr,而**退出码是 0**。于是一个事件
	// 也不产出、Wait() 返回 nil —— 调用方看到的是「成功但内容为空」,而
	// api/translate.go 会拿这个空串覆盖已有的 paper_<lang>.md 并回 complete。
	// 切换探测(ProbePi)也拦不住它:它只跑 `pi --version` + SessionArgs +
	// web-search.json 静态预检,**不验凭据**。
	//
	// claude 侧同理成立:成功的 --print --output-format stream-json 至少会有一个
	// result 事件,「零事件 + stderr 有内容」在那里同样是失败。
	if emitted == 0 && stderr.Len() > 0 {
		return fmt.Errorf("agent backend produced no events (stderr: %s)",
			strings.SplitN(stderr.String(), "\n", 2)[0])
	}

	return nil
}

// SendSimple executes the agent CLI with a simple prompt and returns the response as a string.
// This is a convenience method for non-streaming use cases.
//
// D2:prompt 走 stdin 而不是 argv。旗标集因此发生变化 —— 原本只有裸的 `-p`,
// 现在走 OnceArgs 会带上 secure 旗标(--disallowedTools / --dangerously-skip-permissions /
// --settings)。这是必需的:不走 OnceArgs 就拿不到 pi 的硬化旗标,pi 会以**全部
// 内置工具(含 bash)**启动。代价是 claude 侧从「无权限绕过、工具需授权」变成
// 「绕过权限但 Bash/Task 等被硬阻断、文件工具过 path-validator hook」。
//
// 本函数在**生产代码里零调用点**(只有两个错误路径的单测),所以上述变化不影响
// 现有行为;若将来要启用它,应先重新评估是否需要收紧工具面。
func (c *Client) SendSimple(ctx context.Context, prompt string) (string, error) {
	proto, err := c.protocol()
	if err != nil {
		return "", err
	}
	args, err := proto.OnceArgs("", nil, false, "")
	if err != nil {
		return "", fmt.Errorf("build once args: %w", err)
	}
	cmd := exec.CommandContext(ctx, proto.Bin(), args...)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		// Include stderr in the error message if available
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("claude command failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude command failed: %w", err)
	}
	return string(out), nil
}

// SendSimpleWithRead executes the Claude CLI with -p mode and Read tool enabled.
// This is faster than stream-json mode for simple tasks like generating summaries.
// If workDir is non-empty, the command runs in that directory.
func (c *Client) SendSimpleWithRead(ctx context.Context, prompt string, workDir string) (string, error) {
	proto, err := c.protocol()
	if err != nil {
		return "", err
	}
	args, err := proto.OnceArgs("", []string{"Read"}, false, "")
	if err != nil {
		return "", fmt.Errorf("build once args: %w", err)
	}

	cmd := exec.CommandContext(ctx, proto.Bin(), args...)
	if workDir != "" {
		cmd.Dir = workDir
		// Set ALLOWED_DIR environment for security hooks
		if env := proto.Env(workDir); len(env) > 0 {
			cmd.Env = env
		}
	}
	// D2:prompt 从 argv 改为 stdin。原先这里的 `args = append(args, prompt)` 是
	// Plan 1 遗留的最后一处 argv prompt:对 ps 可见(而 ingest 的 prompt 里含用户
	// 文档内容),且受 ARG_MAX 限制。pi 侧更不能放 argv —— 它的 `-p` 是「读管道
	// stdin 并合并进初始 prompt」,两边都给会被拼接成一段。
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("claude command failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return "", fmt.Errorf("claude command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SendWithTools executes the Claude CLI with tools enabled (like Read for PDFs).
// Returns the final response as a string.
func (c *Client) SendWithTools(ctx context.Context, prompt string, workDir string) (string, error) {
	// Create a channel to collect events
	eventCh := make(chan StreamEvent, 100)

	// Run Send in a goroutine
	go func() {
		defer close(eventCh)
		if err := c.Send(ctx, prompt, eventCh, workDir); err != nil {
			eventCh <- StreamEvent{Type: "error", Error: err.Error()}
		}
	}()

	// Collect all content from events
	// Only use "result" event content - "assistant" text blocks are duplicated in result
	var result strings.Builder
	for evt := range eventCh {
		if evt.Type == "error" && evt.Error != "" {
			return "", fmt.Errorf("claude error: %s", evt.Error)
		}
		if evt.Type == "result" && evt.Result != "" {
			result.WriteString(evt.Result)
		}
	}

	return result.String(), nil
}

// SendWithOutput executes the agent CLI and writes output to the provided writer.
// This is useful for capturing output directly to a file or buffer.
//
// 同 SendSimple:生产代码里**零调用点**,且 D2 改造同时修正了一个既有怪癖 ——
// 原实现把 prompt 同时放进了 argv(`-p prompt`)**和** stdin,两遗都送。
func (c *Client) SendWithOutput(ctx context.Context, prompt string, output io.Writer) error {
	proto, err := c.protocol()
	if err != nil {
		return err
	}
	args, err := proto.OnceArgs("", nil, false, "")
	if err != nil {
		return fmt.Errorf("build once args: %w", err)
	}
	cmd := exec.CommandContext(ctx, proto.Bin(), args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude command failed: %w", err)
	}

	return nil
}

// NewClient creates a new Claude client with the default binary path ("claude").
// NewClient returns a Client that resolves its backend lazily through the
// resolver (see protocol()). There is deliberately no "WithPath" variant:
// letting a call site pin the binary is exactly the seam that made Plan 1's
// backend switch ineffective for the once-call paths (summary/sectionize/
// translate/PDF), so the option was removed rather than deprecated.
func NewClient() *Client {
	return &Client{}
}
