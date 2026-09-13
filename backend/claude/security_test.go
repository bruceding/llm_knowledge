package claude

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBuildSecureArgs_AlwaysContainsDisallowedTools is the regression test for the
// production incident where --dangerously-skip-permissions silently neutralized
// --allowedTools, letting the model run Bash/Task/etc. The contract: every
// invocation MUST emit --disallowedTools containing the dangerous set.
func TestBuildSecureArgs_AlwaysContainsDisallowedTools(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"Read"},
		{"Read", "Glob", "Grep", "LS"},
		{"Read", "Write", "Edit"},
	}

	for _, allowed := range cases {
		args, err := BuildSecureArgs(allowed)
		if err != nil {
			t.Fatalf("BuildSecureArgs(%v) returned unexpected error: %v", allowed, err)
		}

		idx := slices.Index(args, "--disallowedTools")
		if idx < 0 || idx == len(args)-1 {
			t.Fatalf("BuildSecureArgs(%v) missing --disallowedTools value: %v", allowed, args)
		}
		value := args[idx+1]

		// Every dangerous tool must appear in the value (csv).
		toolSet := strings.Split(value, ",")
		for _, dangerous := range DangerousDisallowedTools {
			if !slices.Contains(toolSet, dangerous) {
				t.Errorf("BuildSecureArgs(%v) --disallowedTools missing %q (got %q)",
					allowed, dangerous, value)
			}
		}
	}
}

func TestBuildSecureArgs_AllowedToolsRespected(t *testing.T) {
	args, err := BuildSecureArgs([]string{"Read", "Glob"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := slices.Index(args, "--allowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("--allowedTools not present: %v", args)
	}
	if args[idx+1] != "Read,Glob" {
		t.Errorf("--allowedTools value = %q, want %q", args[idx+1], "Read,Glob")
	}
}

func TestBuildSecureArgs_EmptyAllowedToolsOmitsFlag(t *testing.T) {
	// When the caller passes no allowed tools (rare but legal — e.g., text-only
	// prompts), --allowedTools should not appear; --disallowedTools must still
	// appear so dangerous tools remain blocked.
	args, err := BuildSecureArgs(nil)
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

func TestBuildSecureArgs_BypassFlagPresent(t *testing.T) {
	args, err := BuildSecureArgs([]string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions flag, got %v", args)
	}
}

// TestDangerousDisallowedTools_CoversKnownAttackVectors locks in the minimum set
// of tools that must never be reachable. Adding a new dangerous tool to the
// product should add it here too.
func TestDangerousDisallowedTools_CoversKnownAttackVectors(t *testing.T) {
	required := []string{"Bash", "Task", "NotebookEdit", "KillShell", "BashOutput", "SlashCommand"}
	for _, tool := range required {
		if !slices.Contains(DangerousDisallowedTools, tool) {
			t.Errorf("DangerousDisallowedTools missing required tool %q", tool)
		}
	}
}

// TestBuildSecureArgs_RejectsAllowedDangerousOverlap verifies BuildSecureArgs
// rejects programming errors where a caller passes a dangerous tool name into
// allowedTools. Without this guard, the CLI would receive conflicting
// --allowedTools and --disallowedTools entries with undefined precedence.
//
// Returns an error rather than panicking so a buggy caller inside a goroutine
// can't crash the whole server process.
func TestBuildSecureArgs_RejectsAllowedDangerousOverlap(t *testing.T) {
	for _, dangerous := range DangerousDisallowedTools {
		t.Run(dangerous, func(t *testing.T) {
			args, err := BuildSecureArgs([]string{"Read", dangerous})
			if err == nil {
				t.Errorf("expected error when allowedTools contains %q, got args=%v", dangerous, args)
			}
			if args != nil {
				t.Errorf("expected nil args on error, got %v", args)
			}
		})
	}
}

// TestCleanupStaleSettings_AgeGated verifies that orphaned settings files
// older than the cutoff are removed while recent files (potentially in use by
// a parallel backend instance during a rolling restart) survive.
func TestCleanupStaleSettings_AgeGated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	stalePath := filepath.Join(tmp, "claude-security-stale.json")
	freshPath := filepath.Join(tmp, "claude-security-fresh.json")
	unrelatedPath := filepath.Join(tmp, "other-file.json")

	for _, p := range []string{stalePath, freshPath, unrelatedPath} {
		if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	// Backdate the stale file well past the cutoff. Fresh file gets default
	// (just-now) mtime so it's safely on the keep side. Not parallel-safe due
	// to t.Setenv on TMPDIR — do not add t.Parallel() to this test.
	old := time.Now().Add(-2 * staleSettingsCutoff)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", stalePath, err)
	}

	cleanupStaleSettings()

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("expected stale file removed, stat err=%v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("expected fresh file kept, stat err=%v", err)
	}
	if _, err := os.Stat(unrelatedPath); err != nil {
		t.Errorf("expected unrelated file kept, stat err=%v", err)
	}
}

// TestBuildSecureEnv_ResolvesSymlinks pins the macOS symlink-aware behavior:
// /tmp on macOS is a symlink to /private/tmp, but path-validator.py calls
// os.path.realpath(allowed_dir) and resolves it. If BuildSecureEnv left the
// raw path, every path inside ALLOWED_DIR would mismatch and be denied.
func TestBuildSecureEnv_ResolvesSymlinks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("symlink layout for /tmp is macOS-specific")
	}

	tmp := t.TempDir() // typically /var/folders/.../T/... → realpath /private/var/...
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", tmp, err)
	}
	if resolved == tmp {
		t.Skipf("temp dir %q has no symlink layer; nothing to verify", tmp)
	}

	env := BuildSecureEnv(tmp)
	want := "ALLOWED_DIR=" + resolved
	if !slices.Contains(env, want) {
		t.Errorf("BuildSecureEnv(%q) did not emit %q; env=%v", tmp, want, env)
	}
}

// TestPathValidator_WebFetchSSRF drives path-validator.py with WebFetch payloads
// and verifies that the SSRF defense correctly blocks private/internal targets
// while letting plausible public-internet URLs through. Uses literal IPs for the
// blocked cases so the test does not depend on the host's DNS resolver.
func TestPathValidator_WebFetchSSRF(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	const validatorPath = "../scripts/path-validator.py"
	if _, err := os.Stat(validatorPath); err != nil {
		t.Fatalf("validator script missing: %v", err)
	}

	cases := []struct {
		name      string
		url       string
		wantBlock bool
	}{
		// Loopback / metadata / RFC1918 / IPv6 / non-http schemes — all blocked.
		{"loopback_v4", "http://127.0.0.1:6379/", true},
		{"aws_metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"rfc1918_10", "http://10.0.0.1/", true},
		{"rfc1918_192", "http://192.168.1.1/", true},
		{"rfc1918_172", "http://172.16.0.1/", true},
		{"loopback_v6", "http://[::1]:8080/", true},
		{"unspecified", "http://0.0.0.0/", true},
		{"file_scheme", "file:///etc/passwd", true},
		{"gopher_scheme", "gopher://internal/", true},
		{"missing_host", "http:///path", true},
		// Public-internet literal IP — allowed (1.1.1.1 is Cloudflare DNS).
		{"public_ip", "http://1.1.1.1/", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("python3", validatorPath)
			cmd.Env = []string{"ALLOWED_DIR=/tmp"}
			payload := `{"tool_name":"WebFetch","tool_input":{"url":"` + tc.url + `"}}`
			cmd.Stdin = strings.NewReader(payload)
			err := cmd.Run()

			gotBlocked := false
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
				gotBlocked = true
			}
			if gotBlocked != tc.wantBlock {
				t.Errorf("url=%q: gotBlocked=%v want=%v (err=%v)", tc.url, gotBlocked, tc.wantBlock, err)
			}
		})
	}
}

// TestDangerousToolsCrossLanguageSync 校验三份实现的危险工具集合与敏感路径正则表保持对齐:
// Go 的 DangerousDisallowedTools、Python 的 ALWAYS_DENIED_TOOLS(Claude CLI hook)、
// TS 的 ALWAYS_DENIED_TOOLS(pi extension)。任一漂移都会产生静默的防御缺口
// (例如某工具在 CLI 被拦但 hook 兜底没拦,或反之)。本测试曾抓到 PR #42 评审报出的
// SlashCommand 漂移。
//
// 三语言不是简单的集合相等:Claude 与 pi 的工具名不同(Claude 的 Bash 对应 pi 的 bash),
// 且 Claude 的 Task/NotebookEdit/KillShell/BashOutput/SlashCommand 在 pi **没有对应物**
// (pi 内置工具仅 read/bash/powershell/edit/write/grep/find/ls)。因此 TS 侧用两个常量
// 显式表态:DENIED_TOOL_MAPPING(有对应物)与 DENIED_TOOLS_WITHOUT_PI_COUNTERPART(无对应物)。
// Go 侧每个危险工具必须落在其中之一,否则测试失败 —— 将来新增危险工具时逼出一次有意识
// 的决定,而不是静默漏掉 pi 侧。
func TestDangerousToolsCrossLanguageSync(t *testing.T) {
	const validatorPath = "../scripts/path-validator.py"

	body, err := os.ReadFile(validatorPath)
	if err != nil {
		t.Fatalf("read %s: %v", validatorPath, err)
	}

	// Match: ALWAYS_DENIED_TOOLS = frozenset({"a", "b", ...})
	re := regexp.MustCompile(`(?s)ALWAYS_DENIED_TOOLS\s*=\s*frozenset\(\{([^}]+)\}\)`)
	m := re.FindSubmatch(body)
	if m == nil {
		t.Fatalf("could not find ALWAYS_DENIED_TOOLS frozenset in %s", validatorPath)
	}

	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	var pyTools []string
	for _, tok := range tokenRe.FindAllSubmatch(m[1], -1) {
		pyTools = append(pyTools, string(tok[1]))
	}

	for _, goTool := range DangerousDisallowedTools {
		if !slices.Contains(pyTools, goTool) {
			t.Errorf("Go DangerousDisallowedTools has %q but Python ALWAYS_DENIED_TOOLS does not (drift)", goTool)
		}
	}
	for _, pyTool := range pyTools {
		if !slices.Contains(DangerousDisallowedTools, pyTool) {
			t.Errorf("Python ALWAYS_DENIED_TOOLS has %q but Go DangerousDisallowedTools does not (drift)", pyTool)
		}
	}

	// ---- 第三语言:pi extension ----
	const tsValidatorPath = "../scripts/pi-path-validator.ts"
	tsBody, err := os.ReadFile(tsValidatorPath)
	if err != nil {
		t.Fatalf("read %s: %v", tsValidatorPath, err)
	}

	tsDenied := tsStringArray(t, tsBody, "ALWAYS_DENIED_TOOLS")
	tsPiOnly := tsStringArray(t, tsBody, "PI_ONLY_DENIED_TOOLS")
	tsNoCounterpart := tsStringArray(t, tsBody, "DENIED_TOOLS_WITHOUT_PI_COUNTERPART")
	tsFileTools := tsStringArray(t, tsBody, "FILE_TOOLS")
	tsMapping := tsStringMap(t, tsBody, "DENIED_TOOL_MAPPING")

	// Go 的每个危险工具,TS 侧必须明确表态:有 pi 对应物且已进拒绝集合,或被显式记为无对应物。
	for _, goTool := range DangerousDisallowedTools {
		piName, mapped := tsMapping[goTool]
		switch {
		case mapped:
			if !slices.Contains(tsDenied, piName) {
				t.Errorf("Go %q maps to pi %q but TS ALWAYS_DENIED_TOOLS does not contain it (drift)", goTool, piName)
			}
		case slices.Contains(tsNoCounterpart, goTool):
			// 无对应物且已显式记录,符合预期
		default:
			t.Errorf("Go DangerousDisallowedTools has %q but the TS extension neither maps it via DENIED_TOOL_MAPPING nor lists it in DENIED_TOOLS_WITHOUT_PI_COUNTERPART (drift)", goTool)
		}
	}

	// 反向:TS 的映射键不能是 Go 侧已不存在的陈旧工具名。
	for claudeName := range tsMapping {
		if !slices.Contains(DangerousDisallowedTools, claudeName) {
			t.Errorf("TS DENIED_TOOL_MAPPING has stale Claude tool %q not in Go DangerousDisallowedTools (drift)", claudeName)
		}
	}

	// TS 的拒绝集合必须恰好等于「映射值 ∪ pi 独有」,不多不少。
	wantDenied := map[string]bool{}
	for _, piName := range tsMapping {
		wantDenied[piName] = true
	}
	for _, piName := range tsPiOnly {
		wantDenied[piName] = true
	}
	for _, denied := range tsDenied {
		if !wantDenied[denied] {
			t.Errorf("TS ALWAYS_DENIED_TOOLS has %q which is neither a mapped Claude tool nor in PI_ONLY_DENIED_TOOLS (drift)", denied)
		}
		delete(wantDenied, denied)
	}
	for missing := range wantDenied {
		t.Errorf("TS ALWAYS_DENIED_TOOLS is missing %q (drift)", missing)
	}

	// 拒绝集合与文件工具白名单必须不相交,否则白名单会放行本该拦下的工具。
	for _, denied := range tsDenied {
		if slices.Contains(tsFileTools, denied) {
			t.Errorf("TS FILE_TOOLS contains %q which is also in ALWAYS_DENIED_TOOLS (self-contradictory)", denied)
		}
	}

	// 敏感路径正则表:Python 与 TS 逐条同步(集合相等 + 条数相等,后者可抓重复项)。
	// TS 侧用 String.raw`...` 书写,反引号内文本与 Python 的 r'...' 逐字节相同,
	// 因此可以直接比源码文本、无需任何反转义。
	pyPatterns := pythonRawStringArray(t, body, "SENSITIVE_PATH_PATTERNS")
	tsPatterns := tsStringRawArray(t, tsBody, "SENSITIVE_PATH_PATTERNS")
	if len(pyPatterns) == 0 {
		t.Fatalf("could not find SENSITIVE_PATH_PATTERNS in %s", validatorPath)
	}
	if len(pyPatterns) != len(tsPatterns) {
		t.Errorf("sensitive pattern count drift: Python=%d TS=%d", len(pyPatterns), len(tsPatterns))
	}
	for _, pyPattern := range pyPatterns {
		if !slices.Contains(tsPatterns, pyPattern) {
			t.Errorf("Python SENSITIVE_PATH_PATTERNS has %q but the TS extension does not (drift)", pyPattern)
		}
	}
	for _, tsPattern := range tsPatterns {
		if !slices.Contains(pyPatterns, tsPattern) {
			t.Errorf("TS SENSITIVE_PATH_PATTERNS has %q but Python does not (drift)", tsPattern)
		}
	}
}

// tsStringArray 从 TS 源码里抠出 `const NAME ... = ["a", "b"];` 形式的字符串数组。
// 锚定 `const` 前缀,避免命中注释里对同名常量的提及。
func tsStringArray(t *testing.T, src []byte, name string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)const\s+` + name + `\b[^=]*=\s*\[(.*?)\]`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatalf("could not find TS array %s", name)
	}
	itemRe := regexp.MustCompile(`"([^"]+)"`)
	var out []string
	for _, tok := range itemRe.FindAllSubmatch(m[1], -1) {
		out = append(out, string(tok[1]))
	}
	return out
}

// tsStringRawArray 抠出 `const NAME ... = [String.raw` + "`" + `...` + "`" + `, ...];` 形式的数组。
func tsStringRawArray(t *testing.T, src []byte, name string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)const\s+` + name + `\b[^=]*=\s*\[(.*?)\n\];`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatalf("could not find TS array %s", name)
	}
	itemRe := regexp.MustCompile("String\\.raw`([^`]*)`")
	var out []string
	for _, tok := range itemRe.FindAllSubmatch(m[1], -1) {
		out = append(out, string(tok[1]))
	}
	return out
}

// tsStringMap 抠出 `const NAME ... = { Key: "value" };`。键在 TS 里不带引号。
func tsStringMap(t *testing.T, src []byte, name string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(`(?s)const\s+` + name + `\b[^=]*=\s*\{(.*?)\}`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatalf("could not find TS map %s", name)
	}
	entryRe := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:\s*"([^"]+)"`)
	out := map[string]string{}
	for _, tok := range entryRe.FindAllSubmatch(m[1], -1) {
		out[string(tok[1])] = string(tok[2])
	}
	return out
}

// pythonRawStringArray 抠出 Python 的 `NAME = [r'...', ...]`。
func pythonRawStringArray(t *testing.T, src []byte, name string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + name + `\s*=\s*\[(.*?)\n\]`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatalf("could not find Python list %s", name)
	}
	itemRe := regexp.MustCompile(`r'([^']*)'`)
	var out []string
	for _, tok := range itemRe.FindAllSubmatch(m[1], -1) {
		out = append(out, string(tok[1]))
	}
	return out
}

const piValidatorPath = "../scripts/pi-path-validator.ts"

// runPiPathValidator 以与 TestPathValidator_WebFetchSSRF 完全相同的手法驱动 TS extension:
// stdin 喂 JSON,deny 则 exit 2 并把决定写 stderr,放行 exit 0。
//
// cwd 参数用于复现生产布局:Go 侧 spawn 时 cmd.Dir = userDir 且 ALLOWED_DIR = realpath(userDir),
// pi 内部用 resolveToCwd(searchDir || ".", ctx.cwd) 解析相对路径(find.js:65、grep.js:57),
// 所以 hook 拿到的 ctx.cwd 就是 ALLOWED_DIR。测试里把两者设成一致才能复现该语义;
// 故意设成不一致的那条用例则用来验证 fail-closed。
func runPiPathValidator(t *testing.T, allowedDir, cwd, piWebTools, payload string) (bool, string) {
	t.Helper()
	// 必须先转绝对路径:下面会把 cmd.Dir 设成临时目录,相对的 ../scripts/... 会按
	// cmd.Dir 解析而找不到文件。
	absValidator, err := filepath.Abs(piValidatorPath)
	if err != nil {
		t.Fatalf("resolve validator path: %v", err)
	}
	cmd := exec.Command("node", absValidator)
	if cwd != "" {
		cmd.Dir = cwd
	}
	env := []string{"PATH=" + os.Getenv("PATH")}
	if allowedDir != "" {
		env = append(env, "ALLOWED_DIR="+allowedDir)
	}
	if piWebTools != "" {
		env = append(env, "PI_WEB_TOOLS="+piWebTools)
	}
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(payload)
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return false, ""
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("validator run failed (not a deny): %v; stderr=%s", err, stderr.String())
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("validator exited %d (want 0 or 2); stderr=%s", exitErr.ExitCode(), stderr.String())
	}
	return true, strings.TrimSpace(stderr.String())
}

func piPayload(toolName, inputJSON string) string {
	return `{"toolName":` + strconv.Quote(toolName) + `,"input":` + inputJSON + `}`
}

func piPathInput(p string) string {
	return `{"path":` + strconv.Quote(p) + `}`
}

// TestPiPathValidator_PathBoundary 验证 TS extension 的路径沙箱与 Python 版等价:
// 目录内放行、目录外拒绝、前缀碰撞拒绝、敏感路径拒绝、ALLOWED_DIR 缺失时全拒。
func TestPiPathValidator_PathBoundary(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	if _, err := os.Stat(piValidatorPath); err != nil {
		t.Fatalf("validator script missing: %v", err)
	}

	// 复现生产布局:users/1 是 ALLOWED_DIR,users/10 是同前缀的兄弟目录。
	// 朴素的 strings.HasPrefix(resolved, allowed) 会让 users/10 通过(".../users/10/x"
	// 确实以 ".../users/1" 开头),所以必须按 path.sep 做边界比较。
	base := t.TempDir()
	allowed := filepath.Join(base, "users", "1")
	sibling := filepath.Join(base, "users", "10")
	for _, dir := range []string{allowed, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inside := filepath.Join(allowed, "note.md")
	if err := os.WriteFile(inside, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	siblingFile := filepath.Join(sibling, "secret.txt")
	if err := os.WriteFile(siblingFile, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ALLOWED_DIR 内指向 /etc 的软链:realpath 解析后落在目录外(且命中敏感正则)。
	if err := os.Symlink("/etc", filepath.Join(allowed, "escape")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		allowedDir string
		cwd        string
		webTools   string
		payload    string
		wantBlock  bool
		wantReason string // 为空则只断言 blocked
	}{
		{"目录内已存在文件_放行", allowed, allowed, "", piPayload("read", piPathInput(inside)), false, ""},
		// write 的目标必然不存在:Node 的 realpathSync 对缺失路径抛 ENOENT,若让它冒泡,
		// 按 docs/extensions.md:2925「tool_call errors block the tool」会把所有新建文件的
		// 写操作一并拦掉。这条用例守住 realpathBestEffort 的逐级回退。
		{"目录内新建文件_放行", allowed, allowed, "", piPayload("write", `{"path":`+strconv.Quote(filepath.Join(allowed, "new.md"))+`,"content":"x"}`), false, ""},
		{"兄弟目录_前缀碰撞_拒绝", allowed, allowed, "", piPayload("read", piPathInput(siblingFile)), true, "outside allowed directory"},
		{"敏感路径_etc_passwd_拒绝", allowed, allowed, "", piPayload("read", piPathInput("/etc/passwd")), true, "sensitive"},
		// macOS 的 /etc 是 /private/etc 的软链;Linux 上该路径不存在,但正则照样命中
		// (realpathBestEffort 对不存在的路径退化为 normalize),因此两个平台都拒绝。
		{"敏感路径_private_etc_passwd_拒绝", allowed, allowed, "", piPayload("read", piPathInput("/private/etc/passwd")), true, "sensitive"},
		{"软链逃逸_拒绝", allowed, allowed, "", piPayload("read", piPathInput(filepath.Join(allowed, "escape", "passwd"))), true, "sensitive"},
		{"相对路径穿越_拒绝", allowed, allowed, "", piPayload("read", piPathInput("../../etc/passwd")), true, ""},
		// grep/find/ls 的 path 可选,缺省语义是 cwd;cwd == ALLOWED_DIR 时应当放行。
		{"grep省略path_按cwd解析_放行", allowed, allowed, "", piPayload("grep", `{"pattern":"x"}`), false, ""},
		// cwd 落在 ALLOWED_DIR 之外时,相对路径解析结果也在外面 → 必须拒绝(fail-closed),
		// 而不是拿 ALLOWED_DIR 当基准把它"算"回目录内。
		{"cwd在目录外_省略path_拒绝", allowed, base, "", piPayload("grep", `{"pattern":"x"}`), true, "outside allowed directory"},
		{"read缺失path_按cwd解析_放行", allowed, allowed, "", piPayload("read", `{}`), false, ""},
		{"bash_拒绝", allowed, allowed, "", piPayload("bash", `{"command":"ls"}`), true, "not permitted"},
		// 兜底优先于白名单:即使 PI_WEB_TOOLS 被误配成含 bash 也必须拦下。
		{"bash_即使被塞进PI_WEB_TOOLS_仍拒绝", allowed, allowed, "bash", piPayload("bash", `{"command":"ls"}`), true, "not permitted"},
		{"白名单外工具_拒绝", allowed, allowed, "", piPayload("source_check", `{}`), true, "not permitted"},
		{"web工具未列入PI_WEB_TOOLS_拒绝", allowed, allowed, "", piPayload("fetch_content", `{"url":"https://example.com/"}`), true, "not permitted"},
		{"web工具已列入PI_WEB_TOOLS_放行", allowed, allowed, "fetch_content", piPayload("fetch_content", `{"url":"https://example.com/"}`), false, ""},
		// ALLOWED_DIR 未配置 → 拒绝一切,包括联网工具(与 Python 版 main() 在分派到具体
		// 工具之前就 deny 的行为一致)。
		{"ALLOWED_DIR缺失_文件工具_拒绝", "", allowed, "", piPayload("read", piPathInput(inside)), true, "ALLOWED_DIR"},
		{"ALLOWED_DIR缺失_web工具_也拒绝", "", allowed, "fetch_content", piPayload("fetch_content", `{"url":"https://example.com/"}`), true, "ALLOWED_DIR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := runPiPathValidator(t, tc.allowedDir, tc.cwd, tc.webTools, tc.payload)
			if blocked != tc.wantBlock {
				t.Errorf("payload=%s: blocked=%v want=%v reason=%s", tc.payload, blocked, tc.wantBlock, reason)
				return
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("payload=%s: reason=%q does not contain %q", tc.payload, reason, tc.wantReason)
			}
		})
	}
}

// TestPiPathValidator_FetchContentURL 验证本次新增的关键控制:fetch_content 的 URL 校验。
//
// 堵的是 pi-web-access 的本地文件向量 —— video-extract.ts:337 的 readFile(info.absolutePath)
// (读到的内容随后在 :338-341 被 PUT 上传到 Gemini)与 :211-214 的
// execFileSync("ffmpeg", [..., "-i", filePath, ...])。--tools 白名单拦不住它,因为白名单
// 只决定工具能不能被调、不校验参数。
//
// 刻意**不做** IP/DNS 级 SSRF 断言(如 127.0.0.1、169.254.169.254):那是
// pi-web-access/ssrf-protection.ts 的职责,规格已论证它比 Python 版更严。因此这里
// http://1.1.1.1/ 与 http://127.0.0.1/ 都应当**放行**,由 pi-web-access 去拦后者。
func TestPiPathValidator_FetchContentURL(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	if _, err := os.Stat(piValidatorPath); err != nil {
		t.Fatalf("validator script missing: %v", err)
	}

	allowed := t.TempDir()
	const webTools = "fetch_content,renamed_fetch"

	cases := []struct {
		name       string
		toolName   string
		input      string
		wantBlock  bool
		wantReason string
	}{
		{"https_放行", "fetch_content", `{"url":"https://example.com/x"}`, false, ""},
		{"http_放行", "fetch_content", `{"url":"http://example.com/x"}`, false, ""},
		{"公网字面IP_放行_由pi-web-access负责SSRF", "fetch_content", `{"url":"http://1.1.1.1/"}`, false, ""},
		{"环回字面IP_本层放行_由pi-web-access负责SSRF", "fetch_content", `{"url":"http://127.0.0.1:6379/"}`, false, ""},
		{"绝对本地路径_拒绝", "fetch_content", `{"url":"/etc/passwd"}`, true, "not an absolute http(s) URL"},
		{"相对路径_拒绝", "fetch_content", `{"url":"../../x.mp4"}`, true, "not an absolute http(s) URL"},
		{"file协议_拒绝", "fetch_content", `{"url":"file:///etc/passwd"}`, true, "scheme"},
		{"data协议_拒绝", "fetch_content", `{"url":"data:text/plain,x"}`, true, "scheme"},
		{"gopher协议_拒绝", "fetch_content", `{"url":"gopher://internal/"}`, true, "scheme"},
		{"ftp协议_拒绝", "fetch_content", `{"url":"ftp://host/x"}`, true, "scheme"},
		{"javascript协议_拒绝", "fetch_content", `{"url":"javascript:alert(1)"}`, true, "scheme"},
		// Python 的 urlparse 对该输入 hostname 为 None 因而拒绝;WHATWG URL 会解析成
		// host="path",故 extension 里用显式正则对齐(EMPTY_AUTHORITY_RE)。
		{"空authority_拒绝_对齐Python", "fetch_content", `{"url":"http:///path"}`, true, "missing host"},
		{"空url_拒绝", "fetch_content", `{"url":""}`, true, "empty URL"},
		{"空urls数组_拒绝", "fetch_content", `{"urls":[]}`, true, "empty URL list"},
		{"urls任一非法_整体拒绝", "fetch_content", `{"urls":["https://a.example/","file:///etc/passwd"]}`, true, "scheme"},
		{"urls含相对路径_整体拒绝", "fetch_content", `{"urls":["https://a.example/","../../x.mp4"]}`, true, "not an absolute http(s) URL"},
		{"urls含非字符串_整体拒绝", "fetch_content", `{"urls":["https://a.example/",42]}`, true, "empty URL"},
		{"urls全部合法_放行", "fetch_content", `{"urls":["https://a.example/","https://b.example/"]}`, false, ""},
		// 工具名可被 web-search.json 的 toolNames 改写,所以校验按入参键名而非工具名;
		// 改名后的工具同样必须被校验,否则运维一改配置沙箱就静默失效。
		{"改名后的工具_仍被校验", "renamed_fetch", `{"url":"file:///etc/passwd"}`, true, "scheme"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := piPayload(tc.toolName, tc.input)
			blocked, reason := runPiPathValidator(t, allowed, allowed, webTools, payload)
			if blocked != tc.wantBlock {
				t.Errorf("payload=%s: blocked=%v want=%v reason=%s", payload, blocked, tc.wantBlock, reason)
				return
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("payload=%s: reason=%q does not contain %q", payload, reason, tc.wantReason)
			}
		})
	}
}
