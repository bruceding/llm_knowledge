package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"llm-knowledge/config"
)

// newPiTestProtocol 构造一个密封的 PiProtocol:沙箱 extension 存在,且
// PI_CODING_AGENT_DIR 指向一个空的临时目录 —— 于是 web-search.json 不存在、
// LoadPiWebToolNames() 落到默认名。
//
// 必须显式钉住 PI_CODING_AGENT_DIR:否则测试会读开发机/CI 上真实的
// ~/.pi/agent/web-search.json,一旦运维改过 toolNames,下面所有关于工具名的断言
// 都会随环境漂移。
func newPiTestProtocol(t *testing.T) *PiProtocol {
	t.Helper()
	return NewPiProtocol("pi", newPiTestScriptsDir(t))
}

// newPiTestScriptsDir 返回一个含沙箱 extension 的临时 scripts 目录。
// 文件内容无关紧要:*Args 只 os.Stat 它是否存在。
func newPiTestScriptsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// 空目录会让 config 落到默认工具名(文件不存在 = pi 侧同样当作 {})
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, piSandboxExtensionName), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write stub extension: %v", err)
	}
	return dir
}

// writePiWebSearchConfig 在密封的 agent 目录里写一份 web-search.json。
func writePiWebSearchConfig(t *testing.T, content string) {
	t.Helper()
	path := config.PiWebSearchConfigPath()
	if path == "" {
		t.Fatal("expected a non-empty web-search.json path under the test agent dir")
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write web-search.json: %v", err)
	}
}

// toolsFlagValue 取出 --tools 的值。
func toolsFlagValue(t *testing.T, args []string) string {
	t.Helper()
	i := slices.Index(args, "--tools")
	if i < 0 || i == len(args)-1 {
		t.Fatalf("expected --tools <value> in args: %v", args)
	}
	return args[i+1]
}

// envValue 从 Env() 的返回里取某个键的值;第二个返回值表示该键是否存在。
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

// TestPiSessionArgs_HardeningFlagsComplete 断言规格「pi 进程配方」里每个必须出现
// 的旗标都在,且 --mode rpc 打头。
func TestPiSessionArgs_HardeningFlagsComplete(t *testing.T) {
	p := newPiTestProtocol(t)
	args, err := p.SessionArgs("sys prompt", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(args) < 2 || args[0] != "--mode" || args[1] != "rpc" {
		t.Errorf("expected args to start with --mode rpc, got: %v", args)
	}
	for _, want := range []string{"--no-skills", "--no-prompt-templates", "--no-context-files", "-na"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
	i := slices.Index(args, "-e")
	if i < 0 || i == len(args)-1 {
		t.Fatalf("expected -e <path> in args: %v", args)
	}
	if got := args[i+1]; !strings.HasSuffix(got, piSandboxExtensionName) || !filepath.IsAbs(got) {
		t.Errorf("expected -e to be an absolute path ending in %s, got %q", piSandboxExtensionName, got)
	}
	si := slices.Index(args, "--system-prompt")
	if si < 0 || si == len(args)-1 || args[si+1] != "sys prompt" {
		t.Errorf("expected adjacent --system-prompt \"sys prompt\": %v", args)
	}
}

// TestPiArgs_NeverContainForbiddenFlags 断言计划 Task 2 的「禁含旗标」清单。
//
// --no-extensions 会连带关掉 pi-web-access(联网能力来自它);
// --dangerously-skip-permissions 与 --verbose 是 Claude 专有、pi 不需要;
// --model/--provider 按规格决策 3 一律不传(沿用 pi 全局配置);
// --session-dir 按计划 D4 改由 env 注入。
func TestPiArgs_NeverContainForbiddenFlags(t *testing.T) {
	p := newPiTestProtocol(t)
	forbidden := []string{
		"--no-extensions", "-ne",
		"--dangerously-skip-permissions",
		"--verbose",
		"--model", "--provider",
		"--session-dir",
	}

	cases := map[string][]string{}
	session, err := p.SessionArgs("sp", []string{"Read"})
	if err != nil {
		t.Fatalf("SessionArgs: %v", err)
	}
	cases["SessionArgs"] = session
	resume, err := p.ResumeArgs("prev-id", "sp", []string{"Read"})
	if err != nil {
		t.Fatalf("ResumeArgs: %v", err)
	}
	cases["ResumeArgs"] = resume
	onceJSON, err := p.OnceArgs("sp", []string{"Read", "Write", "Edit"}, true, "sonnet")
	if err != nil {
		t.Fatalf("OnceArgs(print): %v", err)
	}
	cases["OnceArgs_print"] = onceJSON
	onceText, err := p.OnceArgs("sp", []string{"Read"}, false, "sonnet")
	if err != nil {
		t.Fatalf("OnceArgs(text): %v", err)
	}
	cases["OnceArgs_text"] = onceText

	for name, args := range cases {
		for _, bad := range forbidden {
			if slices.Contains(args, bad) {
				t.Errorf("%s: must not contain %q (full: %v)", name, bad, args)
			}
		}
	}

	// model hint 被忽略:即使传了 "sonnet",值本身也不得出现在 argv 里
	for _, name := range []string{"OnceArgs_print", "OnceArgs_text"} {
		if slices.Contains(cases[name], "sonnet") {
			t.Errorf("%s: pi must ignore the model hint, but argv contains it: %v", name, cases[name])
		}
	}
}

// TestPiArgs_ToolTiers 逐档钉住规格「pi 进程配方」的四档白名单,含工具名映射
// (Read→read、Glob→find、Grep→grep、LS→ls、Write→write、Edit→edit)与
// 「联网只给交互会话」这条规则。
func TestPiArgs_ToolTiers(t *testing.T) {
	p := newPiTestProtocol(t)
	web := "web_search,fetch_content,get_search_content"

	tests := []struct {
		name string
		got  func() ([]string, error)
		want string
	}{
		{
			name: "文档问答:read + web",
			got:  func() ([]string, error) { return p.SessionArgs("", []string{"Read"}) },
			want: "read," + web,
		},
		{
			name: "自由问答:read,find,grep,ls + web",
			got:  func() ([]string, error) { return p.SessionArgs("", []string{"Read", "Glob", "Grep", "LS"}) },
			want: "read,find,grep,ls," + web,
		},
		{
			name: "ingest Send/SendWithTools:read,write,edit 且不含 web",
			got:  func() ([]string, error) { return p.OnceArgs("", []string{"Read", "Write", "Edit"}, true, "") },
			want: "read,write,edit",
		},
		{
			name: "SendSimpleWithRead:只有 read",
			got:  func() ([]string, error) { return p.OnceArgs("", []string{"Read"}, false, "") },
			want: "read",
		},
		{
			name: "resume 与新建会话同档",
			got:  func() ([]string, error) { return p.ResumeArgs("prev", "", []string{"Read"}) },
			want: "read," + web,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := tc.got()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := toolsFlagValue(t, args); got != tc.want {
				t.Errorf("--tools = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPiArgs_NeverGrantSourceCheck 钉住规格决策:source_check 永不授予。
//
// 它在 pi-web-access 里**默认是启用的**(index.ts:271-274),所以白名单是
// 两道防线中的一道(另一道是 web-search.json.sample 的
// tools.sourceCheck.enabled=false)。即使运维把 sourceCheck 改名成与已授予工具
// 同名,本断言也必须成立。
func TestPiArgs_NeverGrantSourceCheck(t *testing.T) {
	configs := []string{
		`{}`,
		`{"toolNames":{"sourceCheck":"source_check"}}`,
		`{"toolNames":{"sourceCheck":"web_search"}}`,
	}
	for _, cfg := range configs {
		scripts := newPiTestScriptsDir(t)
		writePiWebSearchConfig(t, cfg)
		// 配置写完再构造:NewPiProtocol 在构造时解析工具名(M-7)
		p := NewPiProtocol("pi", scripts)
		must := mustArgs(t)

		for _, args := range [][]string{
			must(p.SessionArgs("", []string{"Read"})),
			must(p.OnceArgs("", []string{"Read", "Write", "Edit"}, true, "")),
		} {
			for _, name := range strings.Split(toolsFlagValue(t, args), ",") {
				if name == "source_check" {
					t.Errorf("config %s: --tools must never contain source_check, got %q",
						cfg, toolsFlagValue(t, args))
				}
			}
		}
	}
}

// mustArgs 返回一个将 ([]string, error) 收窄为 []string 的助手。
// 做成闭包而不是普通函数:Go 只允许 f(g()) 形式的多值展开,带额外的 t 实参就不行。
func mustArgs(t *testing.T) func([]string, error) []string {
	t.Helper()
	return func(args []string, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return args
	}
}

// TestPiArgs_UnmappedToolNameIsError 断言翻译表外的 Claude 工具名返回错误而不是
// 被静默丢弃。Bash 是最重要的一个:pi 有 bash/powershell,但翻译表故意不含它们,
// 于是「调用方传 Bash」变成显式错误,而不是悄悄授予一个 shell。
func TestPiArgs_UnmappedToolNameIsError(t *testing.T) {
	p := newPiTestProtocol(t)
	for _, bad := range []string{"Bash", "Task", "NotebookEdit", "KillShell", "BashOutput", "SlashCommand", "WebFetch", ""} {
		if _, err := p.SessionArgs("", []string{bad}); err == nil {
			t.Errorf("SessionArgs(tools=[%q]) expected error, got nil", bad)
		}
		if _, err := p.OnceArgs("", []string{bad}, true, ""); err == nil {
			t.Errorf("OnceArgs(tools=[%q]) expected error, got nil", bad)
		}
	}

	// bash/powershell 绝不因任何输入进入白名单
	for _, tools := range [][]string{{"Read"}, {"Read", "Write", "Edit"}, {"Read", "Glob", "Grep", "LS"}} {
		args, err := p.SessionArgs("", tools)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, name := range strings.Split(toolsFlagValue(t, args), ",") {
			if name == "bash" || name == "powershell" {
				t.Errorf("tools=%v: %q must never be granted", tools, name)
			}
		}
	}
}

// TestPiResumeArgs_SessionFlag 断言 resume 分支追加 --session <id>,且其余旗标
// 与新建会话一致(pi 没有 --resume;--session 接受会话文件路径或 UUID 前缀)。
func TestPiResumeArgs_SessionFlag(t *testing.T) {
	p := newPiTestProtocol(t)
	args, err := p.ResumeArgs("prev-session-id", "", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--session")
	if i < 0 || i == len(args)-1 || args[i+1] != "prev-session-id" {
		t.Fatalf("expected adjacent --session prev-session-id: %v", args)
	}
	if slices.Contains(args, "--resume") {
		t.Errorf("pi has no --resume flag: %v", args)
	}

	fresh, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(fresh, "--session") {
		t.Errorf("SessionArgs must not contain --session: %v", fresh)
	}
}

// TestPiOnceArgs_ModeSelection 断言 print=true → --mode json、print=false → -p,
// 且 -p 位于**末尾**:pi 的参数解析(cli/args.js:172-176)对 -p 会贪婪吞掉紧随
// 其后那个不以 "-" 开头的实参当作 message,放末尾让误吞在结构上不可能发生。
func TestPiOnceArgs_ModeSelection(t *testing.T) {
	p := newPiTestProtocol(t)

	jsonArgs, err := p.OnceArgs("", []string{"Read"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jsonArgs) < 2 || jsonArgs[0] != "--mode" || jsonArgs[1] != "json" {
		t.Errorf("print=true expected --mode json first, got: %v", jsonArgs)
	}
	if slices.Contains(jsonArgs, "-p") {
		t.Errorf("print=true must not contain -p: %v", jsonArgs)
	}

	textArgs, err := p.OnceArgs("", []string{"Read"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(textArgs, "--mode") {
		t.Errorf("print=false must not contain --mode: %v", textArgs)
	}
	if textArgs[len(textArgs)-1] != "-p" {
		t.Errorf("print=false expected -p last (pi's -p greedily eats the next non-flag arg), got: %v", textArgs)
	}
}

// TestPiOnceArgs_ContainsNoPromptText 是计划 D2 的闸门:prompt 一律走 stdin,
// 绝不进 argv(argv 对 ps 可见,而 prompt 含用户上传的文档内容;且受 ARG_MAX 限制)。
func TestPiOnceArgs_ContainsNoPromptText(t *testing.T) {
	p := newPiTestProtocol(t)
	prompt := "请把这份文档逐页转成 Markdown"
	must := mustArgs(t)

	for _, args := range [][]string{
		must(p.OnceArgs("", []string{"Read"}, true, "")),
		must(p.OnceArgs("", []string{"Read"}, false, "")),
		must(p.SessionArgs("", []string{"Read"})),
	} {
		for _, a := range args {
			if strings.Contains(a, prompt) {
				t.Errorf("argv must not carry prompt text, got element %q in %v", a, args)
			}
		}
	}
}

// TestPiArgs_FailClosedWithoutSandboxExtension 是 R6 的核心防线:沙箱 extension
// 缺失时三个 *Args 一律报错,绝不产出一条没有工具调用拦截的 argv。
// 照抄 claude/security.go:131-133 的先例。
func TestPiArgs_FailClosedWithoutSandboxExtension(t *testing.T) {
	cases := map[string]string{
		"目录存在但文件缺失":      t.TempDir(),
		"scriptsDir 为空串": "",
		"目录不存在":          filepath.Join(t.TempDir(), "no-such-dir"),
	}
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

	for name, scriptsDir := range cases {
		t.Run(name, func(t *testing.T) {
			p := NewPiProtocol("pi", scriptsDir)
			if _, err := p.SessionArgs("", []string{"Read"}); err == nil {
				t.Error("SessionArgs expected error when sandbox extension is missing")
			}
			if _, err := p.ResumeArgs("prev", "", []string{"Read"}); err == nil {
				t.Error("ResumeArgs expected error when sandbox extension is missing")
			}
			if _, err := p.OnceArgs("", []string{"Read"}, true, ""); err == nil {
				t.Error("OnceArgs expected error when sandbox extension is missing")
			}
			if _, err := p.OnceArgs("", []string{"Read"}, false, ""); err == nil {
				t.Error("OnceArgs(text) expected error when sandbox extension is missing")
			}
		})
	}
}

// TestPiArgs_SandboxExtensionPathIsAbsolute 堵住「Go 的 Stat 与 pi 的 -e 用不同
// 基准解析相对路径」这条 fail-open 通路:Go 以服务进程 CWD 解析,pi 以子进程
// cwd(= userDir)解析。两者不一致时 Stat 可能通过而 pi 加载不到 extension。
func TestPiArgs_SandboxExtensionPathIsAbsolute(t *testing.T) {
	// 在一个干净的 CWD 里放一份相对路径的 scripts 目录,避免污染仓库
	t.Chdir(t.TempDir()) // Go 1.24+,测试结束自动恢复原 CWD
	const relative = "relative-scripts"
	if err := os.MkdirAll(relative, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(relative, piSandboxExtensionName), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

	p := NewPiProtocol("pi", relative)
	args, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := args[slices.Index(args, "-e")+1]
	if !filepath.IsAbs(got) {
		t.Errorf("-e value must be absolute so Go's Stat and pi resolve the same path, got %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("-e value %q must actually exist: %v", got, err)
	}
}

// TestPiInitCommands_GetState 钉住计划 D1:pi 在 rpc 模式下启动后不主动输出任何行,
// 也不发 --mode json 那个 session 头行,所以必须主动发 get_state 去取 sessionId。
func TestPiInitCommands_GetState(t *testing.T) {
	p := newPiTestProtocol(t)
	cmds := p.InitCommands()
	if len(cmds) != 1 {
		t.Fatalf("expected exactly 1 init command, got %d: %q", len(cmds), cmds)
	}
	line := string(cmds[0])
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("init command must end with a newline (JSONL framing), got %q", line)
	}

	var got struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &got); err != nil {
		t.Fatalf("init command is not valid JSON: %v (%q)", err, line)
	}
	if got.Type != "get_state" {
		t.Errorf("type = %q, want get_state", got.Type)
	}
	if got.ID == "" {
		t.Error("expected a non-empty id (pi echoes it back for correlation)")
	}

	// Claude 侧必须是 no-op,否则既有 claude 路径会多写一行 stdin
	c := &ClaudeProtocol{bin: "claude"}
	if got := c.InitCommands(); got != nil {
		t.Errorf("ClaudeProtocol.InitCommands() = %v, want nil", got)
	}
}

// TestPiEnv_InjectedKeys 断言 Env() 注入的键齐全、ALLOWED_DIR 经 realpath 解析、
// 且会话目录与 ALLOWED_DIR 同源(不可能漂移到另一个目录)。
func TestPiEnv_InjectedKeys(t *testing.T) {
	p := newPiTestProtocol(t)
	dir := t.TempDir()

	env := p.Env(dir)
	if env == nil {
		t.Fatal("Env(dir) returned nil for a non-empty dir")
	}

	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}

	allowed, ok := envValue(env, "ALLOWED_DIR")
	if !ok {
		t.Fatal("ALLOWED_DIR missing from Env()")
	}
	if allowed != resolved {
		t.Errorf("ALLOWED_DIR = %q, want the realpath-resolved %q", allowed, resolved)
	}
	if !filepath.IsAbs(allowed) {
		t.Errorf("ALLOWED_DIR must be absolute, got %q", allowed)
	}

	sessionDir, ok := envValue(env, "PI_CODING_AGENT_SESSION_DIR")
	if !ok {
		t.Fatal("PI_CODING_AGENT_SESSION_DIR missing from Env() (计划 D4:会话目录走 env 而非 --session-dir)")
	}
	if want := filepath.Join(allowed, piSessionDirName); sessionDir != want {
		t.Errorf("PI_CODING_AGENT_SESSION_DIR = %q, want %q (must derive from the same realpath as ALLOWED_DIR)", sessionDir, want)
	}
	if !filepath.IsAbs(sessionDir) {
		t.Errorf("PI_CODING_AGENT_SESSION_DIR must be absolute, got %q", sessionDir)
	}

	webTools, ok := envValue(env, "PI_WEB_TOOLS")
	if !ok {
		t.Fatal("PI_WEB_TOOLS missing from Env()")
	}
	// 与 --tools 的 web 部分同源:同一个 p.webTools
	if want := strings.Join(p.WebTools(), ","); webTools != want {
		t.Errorf("PI_WEB_TOOLS = %q, want %q", webTools, want)
	}
	sessionArgs, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("SessionArgs: %v", err)
	}
	for _, name := range p.WebTools() {
		if !slices.Contains(strings.Split(toolsFlagValue(t, sessionArgs), ","), name) {
			t.Errorf("PI_WEB_TOOLS entry %q must also be granted by --tools (%q)", name, toolsFlagValue(t, sessionArgs))
		}
	}

	// Env() 不得创建会话目录:那会在用户数据目录里凭空多出目录
	if _, err := os.Stat(sessionDir); err == nil {
		t.Errorf("Env() must not create %q", sessionDir)
	}
}

// TestPiEnv_EmptyAllowedDirReturnsNil 断言 fail-closed 的一半:allowedDir 为空时
// 返回 nil,于是子进程里没有 ALLOWED_DIR,沙箱 extension 拒绝一切工具调用。
func TestPiEnv_EmptyAllowedDirReturnsNil(t *testing.T) {
	p := newPiTestProtocol(t)
	if env := p.Env(""); env != nil {
		t.Errorf("Env(\"\") = %v, want nil", env)
	}
}

// TestPiEnv_OverridesInheritedKeys 断言注入是**权威**的:父进程里已存在的同名键
// 必须先被剔除,而不是靠「后写的覆盖先写的」这种实现细节。
func TestPiEnv_OverridesInheritedKeys(t *testing.T) {
	t.Setenv("ALLOWED_DIR", "/stale/allowed")
	t.Setenv("PI_WEB_TOOLS", "stale_tool")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "/stale/sessions")
	p := newPiTestProtocol(t)
	dir := t.TempDir()

	env := p.Env(dir)
	for _, key := range []string{"ALLOWED_DIR", "PI_WEB_TOOLS", "PI_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_DIR"} {
		count := 0
		for _, e := range env {
			if strings.HasPrefix(e, key+"=") {
				count++
			}
		}
		if count > 1 {
			t.Errorf("%s appears %d times in Env(); inherited value must be filtered out, not shadowed", key, count)
		}
	}
	if v, _ := envValue(env, "PI_WEB_TOOLS"); v == "stale_tool" {
		t.Error("stale PI_WEB_TOOLS survived into the child env")
	}
	if v, _ := envValue(env, "ALLOWED_DIR"); v == "/stale/allowed" {
		t.Error("stale ALLOWED_DIR survived into the child env")
	}
}

// TestPiEnv_AgentDirInvariantI1 是不变式 I1 的守护测试:每一次 pi spawn,注入的
// PI_CODING_AGENT_DIR 必须逐字等于 filepath.Dir(config.PiWebSearchConfigPath())
// 在 Go 进程内解析出的目录,且必须是「派生」而不是重算。
//
// 关键断言不是「等于同一个公式」(那是同义反复),而是:把注入值与 web-search.json
// 文件名拼回去,必须逐字得到 Go 自己读的那个配置路径 —— 即子进程与 Go 必读同一文件。
func TestPiEnv_AgentDirInvariantI1(t *testing.T) {
	t.Run("PI_CODING_AGENT_DIR 已设置", func(t *testing.T) {
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		p := NewPiProtocol("pi", newPiTestScriptsDirOnly(t))

		configPath := config.PiWebSearchConfigPath()
		injected, ok := envValue(p.Env(t.TempDir()), "PI_CODING_AGENT_DIR")
		if !ok {
			t.Fatal("PI_CODING_AGENT_DIR not injected")
		}
		if want := filepath.Dir(configPath); injected != want {
			t.Errorf("injected = %q, want filepath.Dir(PiWebSearchConfigPath()) = %q", injected, want)
		}
		if got := filepath.Join(injected, "web-search.json"); got != configPath {
			t.Errorf("I1 broken: child would read %q while Go reads %q", got, configPath)
		}
		if injected != agentDir {
			t.Errorf("injected = %q, want the configured %q", injected, agentDir)
		}
	})

	t.Run("PI_CODING_AGENT_DIR 未设置", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "")
		p := NewPiProtocol("pi", newPiTestScriptsDirOnly(t))

		configPath := config.PiWebSearchConfigPath()
		if configPath == "" {
			t.Skip("os.UserHomeDir() unavailable; PiWebSearchConfigPath returns empty")
		}
		injected, ok := envValue(p.Env(t.TempDir()), "PI_CODING_AGENT_DIR")
		if !ok {
			t.Fatal("PI_CODING_AGENT_DIR not injected")
		}
		if want := filepath.Dir(configPath); injected != want {
			t.Errorf("injected = %q, want %q", injected, want)
		}
		if got := filepath.Join(injected, "web-search.json"); got != configPath {
			t.Errorf("I1 broken: child would read %q while Go reads %q", got, configPath)
		}
		// 未设置时必须落到 <home>/.pi/agent,而不是某个硬编码或重算出来的路径
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		if want := filepath.Join(home, ".pi", "agent"); injected != want {
			t.Errorf("injected = %q, want %q", injected, want)
		}
	})
}

// newPiTestScriptsDirOnly 只准备沙箱 extension,不动 PI_CODING_AGENT_DIR
// (供需要自己控制该 env 的用例使用)。
func newPiTestScriptsDirOnly(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, piSandboxExtensionName), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write stub extension: %v", err)
	}
	return dir
}

// TestPiEnv_RejectsBadAgentDir 覆盖计划 Task 2 新增的两条把关:含 ~ 与相对路径。
//
// 含 ~:pi 的 getAgentDir() 会 expandTildePath 展开,而 pi-web-access 的
// utils.ts:13-14 **不做** tilde 展开 —— 注入原值会让 auth 目录与 web-search 目录分家。
// 相对路径:Go 以服务进程 CWD 解析,pi 子进程以 cwd(= userDir)解析,必然不一致。
// 这里按服务进程 CWD 绝对化(与 Go 读文件时的基准同一个),两侧才读同一份配置。
//
// 两种情形都必须先剔除继承来的坏值,否则子进程照样拿得到它。
func TestPiEnv_RejectsBadAgentDir(t *testing.T) {
	t.Run("含 ~ 时不注入且剔除继承值", func(t *testing.T) {
		scripts := newPiTestScriptsDirOnly(t)
		t.Setenv("PI_CODING_AGENT_DIR", "~/somewhere")
		p := NewPiProtocol("pi", scripts)

		env := p.Env(t.TempDir())
		if v, ok := envValue(env, "PI_CODING_AGENT_DIR"); ok {
			t.Errorf("must not inject a ~-prefixed value (pi expands it, pi-web-access does not), got %q", v)
		}
		for _, e := range env {
			if strings.HasPrefix(e, "PI_CODING_AGENT_DIR=") {
				t.Errorf("inherited PI_CODING_AGENT_DIR must be filtered out, got %q", e)
			}
		}
	})

	t.Run("相对路径按服务进程 CWD 绝对化", func(t *testing.T) {
		scripts := newPiTestScriptsDirOnly(t)
		t.Chdir(t.TempDir()) // Go 1.24+;让 CWD 可控,以便断言绝对化的基准
		t.Setenv("PI_CODING_AGENT_DIR", "relative-agent-dir")
		p := NewPiProtocol("pi", scripts)

		v, ok := envValue(p.Env(t.TempDir()), "PI_CODING_AGENT_DIR")
		if !ok {
			t.Fatal("expected the relative value to be absolutized and injected")
		}
		if !filepath.IsAbs(v) {
			t.Errorf("injected value must be absolute, got %q", v)
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		if want := filepath.Join(cwd, "relative-agent-dir"); v != want {
			t.Errorf("injected = %q, want %q (same base Go used to read web-search.json)", v, want)
		}
	})
}

// TestPiWebTools_DedupedByName 是计划 M-2 的处置:config 的 Names() 不去重,
// 而重名在 pi 侧是致命的(resolveToolNames,index.ts:303-311 → 扩展加载失败 →
// pi exit 1)。PiProtocol 按首次出现去重,保证自己产出的 --tools 与 PI_WEB_TOOLS
// 良构。
func TestPiWebTools_DedupedByName(t *testing.T) {
	scripts := newPiTestScriptsDirOnly(t)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	writePiWebSearchConfig(t, `{"toolNames":{"webSearch":"x","fetchContent":"x"}}`)

	p := NewPiProtocol("pi", scripts)
	got := p.WebTools()
	want := []string{"x", "get_search_content"}
	if !slices.Equal(got, want) {
		t.Errorf("WebTools() = %v, want %v (deduped by first occurrence)", got, want)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[n] {
			t.Errorf("duplicate tool name %q survived dedupe: %v", n, got)
		}
		seen[n] = true
	}

	// 去重后的名字必须与 --tools、PI_WEB_TOOLS 一致
	args, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("SessionArgs: %v", err)
	}
	if wantTools := "read,x,get_search_content"; toolsFlagValue(t, args) != wantTools {
		t.Errorf("--tools = %q, want %q", toolsFlagValue(t, args), wantTools)
	}
	if v, _ := envValue(p.Env(t.TempDir()), "PI_WEB_TOOLS"); v != "x,get_search_content" {
		t.Errorf("PI_WEB_TOOLS = %q, want %q", v, "x,get_search_content")
	}
}

// TestPiWebTools_RenamedByConfig 断言工具名来自 web-search.json 的 toolNames
// (单一事实来源),而不是在 agent 包里硬编码字面量。
func TestPiWebTools_RenamedByConfig(t *testing.T) {
	scripts := newPiTestScriptsDirOnly(t)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	writePiWebSearchConfig(t, `{"toolNames":{"webSearch":"ws","fetchContent":"fc","getSearchContent":"gsc"}}`)

	p := NewPiProtocol("pi", scripts)
	if want := []string{"ws", "fc", "gsc"}; !slices.Equal(p.WebTools(), want) {
		t.Errorf("WebTools() = %v, want %v", p.WebTools(), want)
	}
	args, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("SessionArgs: %v", err)
	}
	if want := "read,ws,fc,gsc"; toolsFlagValue(t, args) != want {
		t.Errorf("--tools = %q, want %q", toolsFlagValue(t, args), want)
	}
}

// captureAgentLog 把 log 输出临时改到缓冲区,返回取内容的闭包。
// 与 config 包测试里的 captureConfigLog 同手法。
func captureAgentLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return buf.String
}

// writeAgentDirWebSearchConfig 在当前 PI_CODING_AGENT_DIR 里写一份 web-search.json。
func writeAgentDirWebSearchConfig(t *testing.T, content string) {
	t.Helper()
	path := config.PiWebSearchConfigPath()
	if path == "" {
		t.Fatal("PI_CODING_AGENT_DIR 未生效,拿不到配置路径")
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 web-search.json: %v", err)
	}
}

// piAllCommandsDisabled 是部署模板里那段 commands 配置(四个全关)。
const piAllCommandsDisabled = `{"commands":{"websearch":{"enabled":false},"curator":{"enabled":false},"search":{"enabled":false},"google-account":{"enabled":false}}}`

// TestNewPiProtocol_WarnsWhenWebCommandsEnabled 断言 R9 开放项选定的方案 (b) 落地:
// 构造 PiProtocol 时若部署的 web-search.json 没关掉 pi-web-access 的扩展命令,
// 服务日志里必须留一行;关掉了就不该响(否则告警会被运维当噪声忽略)。
func TestNewPiProtocol_WarnsWhenWebCommandsEnabled(t *testing.T) {
	t.Run("命令仍开着时必须告警", func(t *testing.T) {
		getLog := captureAgentLog(t)
		scripts := newPiTestScriptsDirOnly(t)
		agentDir := t.TempDir()
		t.Setenv("PI_CODING_AGENT_DIR", agentDir) // 目录里没有 web-search.json → 四个命令全开

		NewPiProtocol("pi", scripts)

		logged := getLog()
		if logged == "" {
			t.Fatal("web-search.json 缺失时四个扩展命令默认全开,用户发一条 /curator 就能执行扩展代码,必须留下告警")
		}
		// 告警必须可操作:指出是哪个文件、哪些命令、怎么修
		for _, want := range []string{
			filepath.Join(agentDir, "web-search.json"), // 配置路径,便于定位
			"/curator", // 命令名带斜杠,与用户实际输入一致
			"/websearch",
			"enabled",                // 修法
			"web-search.json.sample", // 仓库里的模板
			"--system-prompt",        // 后果之一:绕过我们注入的系统提示
		} {
			if !strings.Contains(logged, want) {
				t.Errorf("告警缺少 %q,运维无法据此定位或修复。实际: %s", want, logged)
			}
		}
	})

	t.Run("模板配置下不告警", func(t *testing.T) {
		getLog := captureAgentLog(t)
		scripts := newPiTestScriptsDirOnly(t)
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
		writeAgentDirWebSearchConfig(t, piAllCommandsDisabled)

		NewPiProtocol("pi", scripts)

		// 注意 resolvePiWebTools 的重名告警不应触发(这份配置没有 toolNames)
		if logged := getLog(); logged != "" {
			t.Errorf("四个命令都已显式关闭,不应告警以免噪声淹没真信号。实际: %s", logged)
		}
	})

	t.Run("只关一部分时告警且只报没关的", func(t *testing.T) {
		getLog := captureAgentLog(t)
		scripts := newPiTestScriptsDirOnly(t)
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
		writeAgentDirWebSearchConfig(t, `{"commands":{"curator":{"enabled":false},"search":{"enabled":false}}}`)

		NewPiProtocol("pi", scripts)

		logged := getLog()
		// 必须断言**命令清单那一段**而不是整条消息:正文里另有一句解释性的
		// 「其中 /curator 会拉起浏览器」,它对任何告警都在,用 Contains 判
		// "/curator" 会命中那句解释而不是清单,断言就失去判别力(实测踩过)。
		const wantList = "扩展命令 /websearch, /google-account ——"
		if !strings.Contains(logged, wantList) {
			t.Errorf("告警的命令清单必须只列仍开着的两个。期望含 %q,实际: %s", wantList, logged)
		}
		const wantFix = "commands.websearch/commands.google-account"
		if !strings.Contains(logged, wantFix) {
			t.Errorf("修法提示也必须只列仍开着的两个。期望含 %q,实际: %s", wantFix, logged)
		}
	})

	t.Run("同一发现不随 resolver 刷新重复刷屏", func(t *testing.T) {
		getLog := captureAgentLog(t)
		scripts := newPiTestScriptsDirOnly(t)
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

		// resolver 的 5s TTL 会反复构造 PiProtocol;同一条发现只该响一次
		for i := 0; i < 3; i++ {
			NewPiProtocol("pi", scripts)
		}

		if n := strings.Count(getLog(), "没有关掉 pi-web-access 的扩展命令"); n != 1 {
			t.Errorf("同一条发现应只告警 1 次(否则流量期间每 5s 一行、一天上万行,运维会直接忽略),实际 %d 次", n)
		}
	})
}
