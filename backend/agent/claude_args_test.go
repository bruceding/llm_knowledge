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
	idx := slices.Index(args, "--disallowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("missing --disallowedTools value: %v", args)
	}
	value := args[idx+1]

	// Every dangerous tool must appear in the value (csv).
	toolSet := strings.Split(value, ",")
	for _, dangerous := range ClaudeDangerousDisallowedTools {
		if !slices.Contains(toolSet, dangerous) {
			t.Errorf("--disallowedTools missing %q (got %q)", dangerous, value)
		}
	}
}

func TestClaudeSessionArgs_AllowedToolsRespected(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("", []string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx := slices.Index(args, "--allowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("--allowedTools not present: %v", args)
	}
	if args[idx+1] != "Read,Glob,Grep,LS" {
		t.Errorf("--allowedTools value = %q, want %q", args[idx+1], "Read,Glob,Grep,LS")
	}
}

func TestClaudeSessionArgs_RejectsAllowedDangerousOverlap(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	for _, dangerous := range ClaudeDangerousDisallowedTools {
		t.Run(dangerous, func(t *testing.T) {
			args, err := p.SessionArgs("", []string{"Read", dangerous})
			if err == nil {
				t.Errorf("expected error when allowedTools contains %q, got args=%v", dangerous, args)
			}
			if args != nil {
				t.Errorf("expected nil args on error, got %v", args)
			}
		})
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

// TestClaudeOnceArgs_PrintModeMatchesClientSend pins the flag sequence to be
// byte-identical to claude.Client.Send (--print + stream-json + secure flags).
func TestClaudeOnceArgs_PrintModeMatchesClientSend(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("", []string{"Read", "Write", "Edit"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantPrefix := []string{
		"--print", "--output-format", "stream-json", "--verbose",
		"--allowedTools", "Read,Write,Edit",
	}
	if len(args) < len(wantPrefix) {
		t.Fatalf("args too short: %v", args)
	}
	for i, want := range wantPrefix {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], want, args)
		}
	}
	for _, want := range []string{"--disallowedTools", "--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
	for _, unwanted := range []string{"-p", "--system-prompt"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("did not expect %q in args: %v", unwanted, args)
		}
	}
}

// TestClaudeOnceArgs_TextModeMatchesSendSimpleWithRead pins the flag sequence to
// match claude.Client.SendSimpleWithRead (-p + secure flags, prompt appended by
// the caller).
func TestClaudeOnceArgs_TextModeMatchesSendSimpleWithRead(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("", []string{"Read"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) == 0 || args[0] != "-p" {
		t.Fatalf("expected args[0] == \"-p\", got: %v", args)
	}
	i := slices.Index(args, "--allowedTools")
	if i < 0 || i == len(args)-1 || args[i+1] != "Read" {
		t.Errorf("expected --allowedTools Read in args: %v", args)
	}
	for _, want := range []string{"--disallowedTools", "--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
	for _, unwanted := range []string{"--print", "--output-format", "--verbose", "--system-prompt"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("did not expect %q in args: %v", unwanted, args)
		}
	}
}

// TestClaudeOnceArgs_SystemPromptAppended verifies --system-prompt is appended
// after the secure flags, so it can never displace the security flag block.
func TestClaudeOnceArgs_SystemPromptAppended(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("sys prompt", []string{"Read"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--system-prompt")
	if i < 0 || i == len(args)-1 || args[i+1] != "sys prompt" {
		t.Fatalf("expected adjacent --system-prompt \"sys prompt\" in args: %v", args)
	}
	secure := slices.Index(args, "--dangerously-skip-permissions")
	if secure < 0 || i < secure {
		t.Errorf("expected --system-prompt (idx %d) after secure flags (idx %d): %v", i, secure, args)
	}
}

// TestClaudeOnceArgs_ModelHint pins 计划 D3:model 非空时追加 --model <model>,
// 且位于 secure 旗标之后(原 api/documents.go 把 --model 放在 secureArgs 之前,
// 旗标集合不变、只有顺序变化);空串时绝不出现 --model,以保证摘要/分节/翻译
// 继续走 claude 默认模型而不被一并改成 sonnet。
func TestClaudeOnceArgs_ModelHint(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}

	args, err := p.OnceArgs("", []string{"Read"}, true, "sonnet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--model")
	if i < 0 || i == len(args)-1 || args[i+1] != "sonnet" {
		t.Fatalf("expected adjacent --model sonnet in args: %v", args)
	}
	if secure := slices.Index(args, "--dangerously-skip-permissions"); secure < 0 || i < secure {
		t.Errorf("expected --model (idx %d) after secure flags (idx %d): %v", i, secure, args)
	}

	noModel, err := p.OnceArgs("", []string{"Read"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(noModel, "--model") {
		t.Errorf("empty model hint must not add --model: %v", noModel)
	}
}

func TestClaudeEnv_ResolvesSymlinks(t *testing.T) {
	// 符号链接建在本次运行唯一的目录里(t.TempDir 每次不同),避开固定文件名在
	// 共享临时目录下残留导致 os.Symlink 返回 EEXIST、进而永久误跳过的路径。
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	p := &ClaudeProtocol{bin: "claude"}
	// 注入一个必须被替换掉的 ALLOWED_DIR,让下方的去重断言真的有判别力。
	t.Setenv("ALLOWED_DIR", "/bogus/must/be/replaced")
	env := p.Env(link)
	resolved, err := filepath.EvalSymlinks(target)
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

// 以下三个用例原先住在 claude/security_test.go,测的是兼容垫片 BuildSecureArgs 与
// DangerousDisallowedTools 别名。Task 8 删掉垫片后把它们搬到这里:它们钉住的是
// SecureArgs 与危险工具清单**本身**的行为,与「由哪个包暴露」无关,不该随垫片消失。

func TestClaudeSecureArgs_EmptyAllowedToolsOmitsFlag(t *testing.T) {
	// 调用方不传任何 allowed tool 时(少见但合法,例如纯文本 prompt),
	// --allowedTools 不该出现;但 --disallowedTools 必须仍在,否则危险工具就放开了。
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SecureArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(args, "--allowedTools") {
		t.Errorf("expected --allowedTools to be omitted when input is nil, got %v", args)
	}
	if !slices.Contains(args, "--disallowedTools") {
		t.Errorf("expected --disallowedTools to remain, got %v", args)
	}
}

func TestClaudeSecureArgs_BypassFlagPresent(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SecureArgs([]string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions flag, got %v", args)
	}
}

// TestClaudeDangerousDisallowedTools_CoversKnownAttackVectors 锁定「绝不能可达」的
// 工具最小集合。产品里新增危险工具时,这里也要跟着加。
func TestClaudeDangerousDisallowedTools_CoversKnownAttackVectors(t *testing.T) {
	required := []string{"Bash", "Task", "NotebookEdit", "KillShell", "BashOutput", "SlashCommand"}
	for _, tool := range required {
		if !slices.Contains(ClaudeDangerousDisallowedTools, tool) {
			t.Errorf("ClaudeDangerousDisallowedTools missing required tool %q", tool)
		}
	}
}
