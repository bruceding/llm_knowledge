package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestPiCommandGateIntegration_DeployedConfigDisablesExtensionCommands 验证风险登记 R9 的
// 处置是否真的生效:**部署模板 `web-search.json` 关掉 `commands.*.enabled` 之后,pi-web-access
// 的四个扩展命令确实不再注册**,因此 rpc 模式下以 `/` 开头的用户消息不可能被派发去执行扩展代码。
//
// == 为什么这件事必须实测 ==
//
// 链路(Task 3 已对 pi 0.85.1 源码核实):`modes/rpc/rpc-mode.js:301-304` 调 `session.prompt()`
// 时**没传** `expandPromptTemplates`,而 `core/agent-session.js:822` 的默认值是 `true`,于是
// `:828-834` 一旦 `text.startsWith("/")` 就调 `_tryExecuteExtensionCommand`(`:954-961`,
// `getCommand` 命中即执行)。四道既有防线全拦不住:`--tools` 只管工具调用(命令是扩展自己的 JS)、
// `--no-skills`/`--no-prompt-templates` 只关 skill 与模板、沙箱 extension 的 `input` hook 在
// `:839-851`(位于命令派发**之后**)。唯一收口就是这份配置文件。
//
// 而 pi-web-access 侧:`isCommandEnabled`(`index.ts:277-279`)是 `!== false`,**默认开**;
// 四处注册都被它门控(`:3164` websearch、`:3427` curator、`:3469` google-account、`:3517` search),
// 形如 `if (isCommandEnabled(initConfig, "curator")) pi.registerCommand("curator", {...})`。
// 「注册被门控」意味着 rpc 的 `get_commands` 就是一个**不需要 LLM 回合**的判据。
//
// == 四腿设计(每腿一次真实 spawn,全部零配额) ==
//
//	A 部署模板        → 四个命令都不在 get_commands 里
//	B 对照组(计划要求)→ 同一份配置只把四个 enabled 改成 true → 四个命令**都在**
//	C 负对照          → 部署模板 + 一个非法 toolNames → pi **exit 1**(R8 的既有实测行为)
//	D 端到端          → 用部署模板发 `/curator hello` 与 `/search foo` → 落到模型层失败,没被当命令执行
//
// B 证明「这套临时 agent 目录确实能加载 pi-web-access」;C 证明「A 那次运行里扩展**读过**这份
// web-search.json」。少了 C,A 的绿就有歧义 —— 2026-09-13 的第一次尝试正是栽在这里:把
// `PI_CODING_AGENT_DIR` 指到空临时目录会让 pi 读不到 `settings.json`,于是 pi-web-access
// 根本没被加载,两组都返回同样的「没有那四个命令」,结论不确定、无法采信。
//
// == 残留(本测试**没有**覆盖,计划里记明) ==
//
//  1. D 腿的**对照组**(把 curator 打开、真发一条、断言命令确实执行)会拉起浏览器,需人确认时机,
//     因此不在自动化里做;B 腿的注册级对照是它的零配额替身。
//  2. 其他已加载包注册的命令(本机 `pi-subagents` 的 `/run`,以及 pi 自带的 inline `llama`)
//     不在 web-search.json 的管辖内,只能靠 R1 的运维隔离(生产用独立 `PI_CODING_AGENT_DIR`,
//     其 `settings.json` 的 `packages` 只保留 pin 过的 pi-web-access)。
//
// == 为什么 gated ==
//
// 它 spawn 真实 pi 并加载第三方扩展的**加载期代码**(R1/R9 的残留面),且要求本机装了
// pi-web-access;不消耗 LLM 配额,但不适合塞进默认的 `go test ./...`:
//
//	LLM_KNOWLEDGE_PI_INTEGRATION=1 go test ./agent/ -run TestPiCommandGateIntegration -v
func TestPiCommandGateIntegration_DeployedConfigDisablesExtensionCommands(t *testing.T) {
	if os.Getenv("LLM_KNOWLEDGE_PI_INTEGRATION") != "1" {
		t.Skip("跳过真实 pi 集成测试(需本机装 pi-web-access);" +
			"用 LLM_KNOWLEDGE_PI_INTEGRATION=1 显式开启")
	}
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("pi 不在 PATH 中,跳过: %v", err)
	}

	// 部署模板:命令名从模板自己的 commands 段取,不在此处硬编码 ——
	// 模板的内容由 config_test.go 的 TestWebSearchSample_PinsSecurityKeys 钉住。
	samplePath := filepath.Join("..", "scripts", "web-search.json.sample")
	raw, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("读部署模板 %s: %v", samplePath, err)
	}
	var sample struct {
		Commands map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatalf("解析部署模板: %v", err)
	}
	if len(sample.Commands) == 0 {
		t.Fatalf("部署模板 %s 里没有 commands 段 —— 本用例前提不成立", samplePath)
	}
	var names []string
	for name, entry := range sample.Commands {
		if entry.Enabled == nil || *entry.Enabled {
			t.Fatalf("部署模板里命令 %q 没有写成 {\"enabled\": false}(实际 %+v)—— "+
				"R9 的处置不成立,任何用户发一条以 /%s 开头的文档问答消息就能执行扩展代码",
				name, entry.Enabled, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)

	// 本机 pi-web-access:通过 agent 目录解析(与 pi 自己的解析路径一致),没装就跳过。
	agentDir := piAgentDirForTest(t)
	pkgDir := filepath.Join(agentDir, "npm", "node_modules", "pi-web-access")
	if _, err := os.Stat(pkgDir); err != nil {
		t.Skipf("pi-web-access 不在 %s,跳过: %v", pkgDir, err)
	}
	t.Logf("agentDir=%s;pi-web-access=%s", agentDir, piWebAccessVersion(pkgDir))

	// 一次 spawn 一个临时 agent 目录。mutate 为 nil 表示直接用部署模板。
	// 返回 (agentDir, home):spawn 时 env 只给这两个 + PATH,故零配额由构造保证。
	setup := func(t *testing.T, mutate func(t *testing.T, path string)) (string, string) {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "agent")
		home := filepath.Join(root, "home")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir agent dir: %v", err)
		}
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatalf("mkdir home: %v", err)
		}
		// npm:pi-web-access 是从 <agentDir>/npm/node_modules 解析的,所以把真实 npm 目录
		// 软链进来;settings.json 的 packages 只留 pi-web-access(R1 的运维隔离要求)。
		if err := os.Symlink(filepath.Join(agentDir, "npm"), filepath.Join(dir, "npm")); err != nil {
			t.Fatalf("symlink npm: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"),
			[]byte(`{"packages":["npm:pi-web-access"]}`+"\n"), 0o644); err != nil {
			t.Fatalf("write settings.json: %v", err)
		}
		cfg := filepath.Join(dir, "web-search.json")
		if err := os.WriteFile(cfg, raw, 0o644); err != nil {
			t.Fatalf("write web-search.json: %v", err)
		}
		if mutate != nil {
			mutate(t, cfg)
		}
		return dir, home
	}

	enableAllCommands := func(t *testing.T, path string) {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("解析模板以便构造对照组: %v", err)
		}
		commands, _ := doc["commands"].(map[string]any)
		for _, entry := range commands {
			if m, ok := entry.(map[string]any); ok {
				m["enabled"] = true
			}
		}
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("序列化对照组配置: %v", err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("写对照组配置: %v", err)
		}
	}

	t.Run("A_部署模板下四个命令都不注册", func(t *testing.T) {
		dir, home := setup(t, nil)
		run := spawnPiRPC(t, piBin, dir, home, `{"type":"get_commands","id":"c1"}`)
		resp := run.expectResponse(t, "c1", "get_commands")
		if !resp.Success {
			t.Fatalf("get_commands 失败(那样「四个命令不在里面」就是空洞的绿):%s", resp.Error)
		}
		got := commandNames(resp)
		for _, name := range names {
			if contains(got, name) {
				t.Errorf("部署模板关掉了 %q,但它仍然被注册了。get_commands=%v\n全部输出:\n%s",
					name, got, run.dump())
			}
		}
		t.Logf("get_commands=%v(四个命令 %v 均不在)", got, names)
	})

	t.Run("B_对照组_启用时四个命令都注册", func(t *testing.T) {
		dir, home := setup(t, enableAllCommands)
		run := spawnPiRPC(t, piBin, dir, home, `{"type":"get_commands","id":"c1"}`)
		resp := run.expectResponse(t, "c1", "get_commands")
		if !resp.Success {
			t.Fatalf("对照组 get_commands 失败:%s", resp.Error)
		}
		got := commandNames(resp)
		for _, name := range names {
			if !contains(got, name) {
				t.Errorf("对照组里 %q 没被注册 —— 说明这套临时 agent 目录**没能加载 pi-web-access**,"+
					"A 腿的绿因此不可采信(2026-09-13 第一次尝试的失败模式)。get_commands=%v\nstderr:\n%s",
					name, got, run.stderr)
			}
		}
		t.Logf("对照组 get_commands=%v", got)
	})

	t.Run("C_负对照_扩展确实读了这份配置", func(t *testing.T) {
		// R8 的既有实测:toolNames 非法 → pi-web-access 加载期抛错 → pi 以退出码 1 退出。
		// 用它证明「这套 setup 里扩展确实加载并解析了 web-search.json」,
		// 于是 A 腿的「命令不在」只能来自 enabled:false,而不是来自扩展没加载。
		breakToolNames := func(t *testing.T, path string) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("解析模板: %v", err)
			}
			doc["toolNames"] = map[string]any{"webSearch": "bad name!"}
			out, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatalf("序列化: %v", err)
			}
			if err := os.WriteFile(path, out, 0o644); err != nil {
				t.Fatalf("写配置: %v", err)
			}
		}
		dir, home := setup(t, breakToolNames)
		run := spawnPiRPC(t, piBin, dir, home, `{"type":"get_commands","id":"c1"}`)
		if run.exitCode != 1 {
			t.Errorf("非法 toolNames 下 pi 退出码 = %d,期望 1(R8)。"+
				"若 pi-web-access 改了这条失败模式,本腿就证明不了「配置被读过」,A 腿的绿重新变得有歧义。"+
				"\n输出:\n%s\nstderr:\n%s", run.exitCode, run.dump(), run.stderr)
		}
		if !strings.Contains(run.stderr, "pi-web-access") {
			t.Errorf("stderr 里没提到 pi-web-access,失败原因可能不是扩展加载:\n%s", run.stderr)
		}
		t.Logf("负对照按预期 exit 1;stderr 首行:%s", firstLine(run.stderr))
	})

	t.Run("D_端到端_斜杠消息落到模型层而非命令派发", func(t *testing.T) {
		// 本腿**不给任何凭据**(env 只有 PATH / HOME=临时目录 / PI_CODING_AGENT_DIR),
		// 所以 prompt 必然失败在模型层。这个失败本身就是「没被当成扩展命令执行」的证据:
		// 命令派发走的是扩展自己的 JS,不需要模型凭据。同时它保证零配额。
		dir, home := setup(t, nil)
		run := spawnPiRPC(t, piBin, dir, home,
			`{"type":"prompt","id":"p1","message":"/curator hello"}`,
			`{"type":"prompt","id":"p2","message":"/search foo"}`)
		if strings.Contains(run.dump(), `"extension_error"`) {
			t.Errorf("出现 extension_error —— 扩展在执行期抛错了,本腿的判据不再干净。输出:\n%s", run.dump())
		}
		for _, id := range []string{"p1", "p2"} {
			resp := run.expectResponse(t, id, "prompt")
			if resp.Success {
				t.Errorf("%s 的 prompt 居然成功了 —— 在没有凭据的前提下,这只可能是被当成扩展命令执行了"+
					"(命令派发跑的是扩展自己的 JS,不需模型凭据)。输出:\n%s", id, run.dump())
				continue
			}
			// 失败还必须**落在模型/凭据层**。只断言 success=false 不够:命令被派发后
			// 自己失败(比如 curator 起不来浏览器)也可能回 success=false,
			// 那样本腿就会因为错误的原因变绿。
			if !mentionsModelLayer(resp.Error) {
				t.Errorf("%s 的 prompt 失败在未知层(error 不含模型/凭据相关字样)——"+
					"无法排除「被当成命令执行后自己失败」。error=%q\n输出:\n%s", id, resp.Error, run.dump())
				continue
			}
			t.Logf("%s → success=false,失败在模型层:%s", id, firstLine(resp.Error))
		}
	})
}

// mentionsModelLayer 判断 error 文本是否指向模型/凭据层。用一组关键词的并集而不是精确匹配
// "No API key found for the selected model":那是 pi 0.85.1 的具体措词,改一个字就会让
// 本用例因为无关的文案变动而失败。任一关键词命中即认为失败发生在模型层。
func mentionsModelLayer(errText string) bool {
	lower := strings.ToLower(errText)
	for _, marker := range []string{"api key", "model", "login", "provider"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// piAgentDirForTest 返回本机的 pi agent 目录。与 config.PiWebSearchConfigPath 的两级回退同源
// (PI_CODING_AGENT_DIR,否则 ~/.pi/agent),但**只用于找已安装的 pi-web-access**;
// spawn 时一律用临时目录,不碰开发者本机的配置。
func piAgentDirForTest(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("拿不到 HOME,无法定位 pi-web-access: %v", err)
	}
	return filepath.Join(home, ".pi", "agent")
}

// piWebAccessVersion 读安装目录的 package.json 版本,只用于日志:R9/R8 的结论都依赖
// 具体版本的行为(部署文档要求 pin),出问题时需要知道当时跑的是哪一版。
func piWebAccessVersion(pkgDir string) string {
	raw, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return "unknown(读 package.json 失败)"
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Version == "" {
		return "unknown(解析 package.json 失败)"
	}
	return pkg.Version
}

// rpcResponse 是 rpc 的 `type:"response"` 行,只声明本用例需要的字段(未知字段忽略,
// 与 ParseLine 对未知事件的宽容一致)。
type rpcResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		Commands []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"commands"`
	} `json:"data"`
}

// piRPCRun 是一次 spawn 的全部可观测结果。
type piRPCRun struct {
	lines     []string
	responses map[string]rpcResponse
	exitCode  int
	stderr    string
}

func (r piRPCRun) dump() string { return strings.Join(r.lines, "\n") }

// expectResponse 取出指定 id 的 response;缺失即失败(而不是静默当成「没有命令」)。
func (r piRPCRun) expectResponse(t *testing.T, id, command string) rpcResponse {
	t.Helper()
	resp, ok := r.responses[id]
	if !ok {
		t.Fatalf("没收到 id=%s 的 response(pi 可能在启动期就退出了,exit=%d)。"+
			"\n输出:\n%s\nstderr:\n%s", id, r.exitCode, r.dump(), r.stderr)
	}
	if resp.Command != command {
		t.Fatalf("id=%s 的 response 是 %q,期望 %q", id, resp.Command, command)
	}
	return resp
}

// spawnPiRPC 用最小 argv/env 启动一次 `pi --mode rpc`,把 lines 逐条写进 stdin,
// 读回输出直到进程退出或超时,然后**显式收割进程**(R5:不得加剧孤儿进程问题)。
//
// argv 故意只有 `--mode rpc --no-session`:本用例要证的是 web-search.json 的 commands 段
// 与扩展注册之间的因果,与生产 argv 的其他旗标无关 —— 而且 R9 的整个要点就是
// `--tools`/`--no-skills`/`--no-prompt-templates` **都管不到命令**。
func spawnPiRPC(t *testing.T, piBin, agentDir, home string, lines ...string) piRPCRun {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, piBin, "--mode", "rpc", "--no-session")
	// 最小 env:临时 HOME 下没有 auth.json、也没有任何 provider 的 API key,
	// 因此这次运行**不可能**发出真实模型请求(零配额由构造保证,不依赖断言)。
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"PI_CODING_AGENT_DIR=" + agentDir,
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn pi: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	run := piRPCRun{responses: map[string]rpcResponse{}}
	for _, line := range lines {
		if _, err := stdin.Write([]byte(line + "\n")); err != nil {
			// 进程可能已经因为加载期失败而退出(C 腿就是这种):那时写不进去是正常的,
			// 退出码与 stderr 才是判据。
			t.Logf("写 stdin 失败(进程可能已退出):%v", err)
			break
		}
	}

	// 读满所有期望的 response 就收工;进程先退出(EOF)也算收工。
	want := len(lines)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			text := scanner.Text()
			run.lines = append(run.lines, text)
			var resp rpcResponse
			if err := json.Unmarshal([]byte(text), &resp); err != nil || resp.Type != "response" {
				continue
			}
			if resp.ID != "" {
				run.responses[resp.ID] = resp
			}
			if len(run.responses) >= want {
				return
			}
		}
	}()

	// 收割进程并取回退出码/stderr。Wait() 同时会等 exec 的 stderr 拷贝协程结束,
	// 因此 run.stderr 必须在它之后取(否则 -race 下与拷贝协程竞争)。
	// C 腿期望非零退出码,所以这里不判错,只记录。
	finish := func() {
		_ = stdin.Close()
		waitErr := cmd.Wait()
		run.stderr = stderr.String()
		run.exitCode = 0
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				run.exitCode = exitErr.ExitCode()
			} else {
				run.exitCode = -1
				run.stderr += "\n(等待进程时出错(非退出类): " + waitErr.Error() + ")"
			}
		}
	}

	select {
	case <-readDone:
	case <-ctx.Done():
		// 先收割再读 run:读取协程还在往 run.lines 追加,不等它结束就取用会有数据竞争。
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-readDone
		finish()
		t.Fatalf("90s 内没读完 %d 条 response(已收到 %d)。输出:\n%s\nstderr:\n%s",
			want, len(run.responses), run.dump(), run.stderr)
	}

	finish()
	return run
}

func commandNames(resp rpcResponse) []string {
	out := make([]string, 0, len(resp.Data.Commands))
	for _, c := range resp.Data.Commands {
		out = append(out, c.Name)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
