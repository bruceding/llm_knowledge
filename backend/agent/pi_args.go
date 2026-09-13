package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llm-knowledge/config"
)

// PiProtocol 是 pi CLI(@earendil-works/pi-coding-agent)的 Protocol 实现。
//
// 与 ClaudeProtocol 的关键差异都不外泄:上层拿到的仍是同一组归一化旗标语义
// (SessionArgs / ResumeArgs / OnceArgs / Env / InitCommands)。
//
// 注意本类型在 Task 3(stdin 编码)与 Task 4(ParseLine)完成之前**尚未**满足
// Protocol 接口,故此处不写 `var _ Protocol = (*PiProtocol)(nil)`;该断言由
// Task 4 补上。
type PiProtocol struct {
	bin string

	// sandboxExtPath 是沙箱 extension 的绝对路径。fail-closed:文件不存在时
	// 三个 *Args 一律返回错误,绝不产出一条没有工具调用拦截的 argv。
	sandboxExtPath string

	// webTools 是授予的联网工具名,在构造时解析一次并持有(计划 M-7):
	// 若三个 *Args 各解析一次,等于每次请求 3 次磁盘读 + 3 次可能的告警刷屏;
	// 解析一次也顺带关掉 I1 的时间窗(解析名字与注入 env 之间配置被改)。
	webTools []string
}

const (
	// piSandboxExtensionName 是沙箱 extension 的文件名。tracked 源在
	// backend/scripts/,部署时复制到运行时 scripts/(即 LLM_SCRIPTS_DIR)。
	piSandboxExtensionName = "pi-path-validator.ts"

	// piSessionDirName 是每用户 pi 会话存储目录的名字,落在 userDir 内。
	// 经 Env() 的 PI_CODING_AGENT_SESSION_DIR 注入,理由见该方法注释(计划 D4)。
	piSessionDirName = ".pi-sessions"

	// piProbeTimeout 是 `pi --version` 的超时。
	piProbeTimeout = 5 * time.Second
)

// piToolNameByClaude 把调用方使用的 Claude 工具名翻译成 pi 的内置工具名。
//
// pi 的内置工具只有 read/bash/powershell/edit/write/grep/find/ls,不存在
// Task/NotebookEdit/KillShell/BashOutput/SlashCommand 的对应物;bash 与
// powershell 故意不在本表内 —— 白名单是「翻译表 ∪ web 工具」,表里没有就
// 授予不到,等价于 Claude 侧 --disallowedTools 的硬阻断。
//
// Glob→find 是有意的:pi 没有 glob 工具,find 是「按 glob 模式找文件」的等价物
// (其 pattern/glob 入参只在已校验的根目录内部匹配,见 Task 6 的核实记录)。
var piToolNameByClaude = map[string]string{
	"Read":  "read",
	"Glob":  "find",
	"Grep":  "grep",
	"LS":    "ls",
	"Write": "write",
	"Edit":  "edit",
}

// NewPiProtocol 构造 pi 后端。
//
// scriptsDir 来自 main.go 的 LLM_SCRIPTS_DIR,由 agent.Init 传入 —— 与
// ClaudeProtocol 的 settingsPath 对称,**不在 agent 包里自己读 env**。
func NewPiProtocol(bin, scriptsDir string) *PiProtocol {
	warnPiWebCommandsEnabled()
	return &PiProtocol{
		bin:            bin,
		sandboxExtPath: piSandboxExtensionPath(scriptsDir),
		webTools:       resolvePiWebTools(),
	}
}

// piCommandWarned 记录已告警过的「配置路径 + 仍启用的命令集合」组合。
//
// **与 Task 1 的 warnInvalid 有意不同:那边不去重,这边必须去重。** 差别在触发频率:
// warnInvalid 只在 toolNames 非法时才响(罕见、且后果是整个 pi 后端 exit 1);
// 而本告警在**默认状态**下就会响 —— web-search.json 不存在时四个命令全开。
// NewPiProtocol 由 resolver 构造,带 5s TTL,于是流量期间不去重就是每 5s 一行、
// 一天上万行 —— 那不叫信号,叫噪声,而噪声会被运维直接忽略。
//
// 代价:同一组合只告警一次,所以「修好 → 又改坏成同一样子」不会再次告警。
// 取这个取舍是因为本告警的目的是「部署时提醒一次」,不是持续监控。
var (
	piCommandWarnMu sync.Mutex
	piCommandWarned = map[string]bool{}
)

// warnPiWebCommandsEnabled 在部署的 web-search.json 没关掉 pi-web-access 的扩展
// 命令时留一条告警(计划 R9 的开放项,选定方案 b:只告警,不阻止启动)。
//
// 不采用 fail-closed(直接报错拒绝启动)的理由:那会把部署脆弱性转移到可用性上 ——
// 运维漏一个键就让整个 pi 后端起不来,而这个键与工具名不同,它不影响功能正确性。
// 与 Task 1 对 toolNames 的处置一致:规格禁止的是返回 error,不禁止日志。
func warnPiWebCommandsEnabled() {
	enabled := config.PiWebCommandsEnabled()
	if len(enabled) == 0 {
		return
	}
	path := config.PiWebSearchConfigPath()
	key := path + "|" + strings.Join(enabled, ",")

	piCommandWarnMu.Lock()
	defer piCommandWarnMu.Unlock()
	if piCommandWarned[key] {
		return
	}
	piCommandWarned[key] = true

	cmds := make([]string, 0, len(enabled))
	for _, name := range enabled {
		cmds = append(cmds, "/"+name)
	}
	log.Printf("[agent] %s 没有关掉 pi-web-access 的扩展命令 %s —— rpc 模式下**任何用户发一条以该名字开头的文档问答消息**,就会直接执行扩展代码而不进 LLM。`--tools` 白名单、`--no-skills`/`--no-prompt-templates`、沙箱 extension 的 input hook 全拦不住它(链路见 config.PiWebCommandsEnabled 的注释)。其中 /curator 会拉起浏览器,且扩展命令自行驱动 LLM、绕过我们注入的 --system-prompt。修法:在该文件里把 commands.%s 均写成 {\"enabled\": false}(可参考仓库里的 backend/scripts/web-search.json.sample)。注:本告警只覆盖 pi-web-access 自己的命令;其他已加载包(如 pi-subagents 的 /run,它会 spawn 一个不带沙箱 extension 的子进程)只能靠运维隔离:生产的 PI_CODING_AGENT_DIR 里 settings.json 的 packages 只保留 pin 过的 pi-web-access",
		path, strings.Join(cmds, ", "), strings.Join(enabled, "/commands."))
}

func (p *PiProtocol) Backend() Backend { return BackendPi }
func (p *PiProtocol) Bin() string      { return p.bin }

// piSandboxExtensionPath 返回沙箱 extension 的**绝对**路径。
//
// 必须绝对:pi 子进程的 cwd 是 userDir,而 Go 侧的 os.Stat 以服务进程 CWD 解析
// 相对路径。两者基准不同时会出现「Stat 通过(服务 CWD 下确有该文件)但 pi 加载
// 不到(userDir 下没有)」,即一条完全没有工具调用拦截的 pi 路径 —— 风险登记 R6
// 描述的 fail-open。绝对化让 Stat 与 pi 看到的是同一个路径。
func piSandboxExtensionPath(scriptsDir string) string {
	path := filepath.Join(scriptsDir, piSandboxExtensionName)
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// resolvePiWebTools 从 config 取联网工具名(单一事实来源),并按首次出现去重。
//
// 去重是计划 M-2 的处置。config.PiWebToolNames.Names() **不去重**:
// {"toolNames":{"webSearch":"x","fetchContent":"x"}} 会得到 ["x","x",...]。
// 重名在 pi 侧是致命的 —— resolveToolNames(index.ts:303-311)对**已启用**的键
// 检测到重名即抛错,扩展加载失败,pi 以退出码 1 退出(整个后端不可用,而
// `pi --version` 探测不到,见 Task 1 的 I-1)。Go 侧无法阻止 pi 读同一份配置而
// 失败,但至少:①自己产出的 --tools 与 PI_WEB_TOOLS 必须是良构的;②必须告警,
// 因为 Task 1 的 warnInvalid 只覆盖单键非法,不覆盖跨键重名,这条路径此前是静默的。
func resolvePiWebTools() []string {
	names := config.LoadPiWebToolNames().Names()
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			log.Printf("[agent] pi 联网工具名重复:%q 在 %s 里出现多次 —— Go 侧已按首次出现去重,但 pi-web-access 的 resolveToolNames(index.ts:303-311)会在同一份配置上抛错,导致扩展加载失败、pi 以退出码 1 退出,整个 pi 后端不可用(含不用 web 工具的 ingest 链路),而 `pi --version` 探测不到。请修正该文件",
				n, config.PiWebSearchConfigPath())
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// WebTools 返回授予的联网工具名(只读用途:测试与诊断)。
func (p *PiProtocol) WebTools() []string { return p.webTools }

// piAllowlist 把调用方给的 Claude 工具名翻译成 pi 工具名,grantWeb 为真时追加
// 联网工具名。
//
// 四档白名单(规格「pi 进程配方」)由调用方传的 tools 与本函数的 grantWeb 共同
// 决定,agent 包不需要知道调用方是谁:
//
//	文档问答   SessionArgs(["Read"])                        → read + web
//	自由问答   SessionArgs(["Read","Glob","Grep","LS"])      → read,find,grep,ls + web
//	ingest    OnceArgs(["Read","Write","Edit"])              → read,write,edit(不含 web)
//	只读一次   OnceArgs(["Read"], print=false)                → read
//
// 即「联网只给交互会话」:一次性调用全是摘要/分节/翻译/PDF 转换,不需要联网,
// 少给一档就少一个需要校验 URL 的入口。
//
// source_check 永不授予(规格决策)。注意它在 pi-web-access 里**默认是启用的**
// (index.ts:271-274),所以「不给」需要两道防线:这里的白名单不含它,加上部署
// 模板 web-search.json.sample 里的 tools.sourceCheck.enabled=false(Task 1 已固化)。
func (p *PiProtocol) piAllowlist(tools []string, grantWeb bool) ([]string, error) {
	out := make([]string, 0, len(tools)+len(p.webTools))
	for _, t := range tools {
		piName, ok := piToolNameByClaude[t]
		if !ok {
			return nil, fmt.Errorf("PiProtocol: no pi counterpart for tool %q; "+
				"pi builtin tools are read/bash/powershell/edit/write/grep/find/ls, "+
				"and bash/powershell are deliberately not grantable", t)
		}
		out = append(out, piName)
	}
	if grantWeb {
		out = append(out, p.webTools...)
	}
	return out, nil
}

// requireSandbox 照抄 claude/security.go:131-133 的 fail-closed 先例:
// 沙箱 extension 不在就报错,绝不启动一个没有工具调用拦截的 pi 进程。
func (p *PiProtocol) requireSandbox() error {
	if _, err := os.Stat(p.sandboxExtPath); err != nil {
		return fmt.Errorf("pi sandbox extension not found at %s: refusing to spawn pi "+
			"without tool-call interception (deploy backend/scripts/%s to the runtime "+
			"scripts dir, i.e. LLM_SCRIPTS_DIR): %w",
			p.sandboxExtPath, piSandboxExtensionName, err)
	}
	return nil
}

// hardenedArgs 返回每个 pi 调用都必须带的那组硬化旗标。
//
// 旗标顺序照规格「pi 进程配方」。**故意不含**:
//   - --no-extensions:联网能力来自全局安装的 pi-web-access 包,关掉扩展发现会
//     连带关掉它;而 `-e npm:pi-web-access` 也不行 —— docs/packages.md:45 明写
//     该形式 "installs to a temporary directory for the current run only",在
//     「每 session 一个子进程」模型下等于每次开会话都重装
//   - --dangerously-skip-permissions:pi 内置工具无权限询问,该危险旗标整体不需要
//   - --verbose:pi 无此要求
//   - --model / --provider:沿用 pi 全局配置(规格决策 3),见 OnceArgs 的 model hint
//   - --session-dir:改由 Env() 注入 PI_CODING_AGENT_SESSION_DIR,见该处注释(计划 D4)
func (p *PiProtocol) hardenedArgs(tools []string, grantWeb bool) ([]string, error) {
	allow, err := p.piAllowlist(tools, grantWeb)
	if err != nil {
		return nil, err
	}
	if err := p.requireSandbox(); err != nil {
		return nil, err
	}
	return []string{
		"--tools", strings.Join(allow, ","),
		"--no-skills",
		"--no-prompt-templates",
		"--no-context-files",
		"-e", p.sandboxExtPath,
		"-na",
	}, nil
}

// SessionArgs 构造多轮交互会话的旗标(pi 的 --mode rpc)。
//
// rpc 模式下 pi 用 stdin 走 JSON-RPC,并且**不读管道 stdin 当 prompt**
// (main.js:701-703 显式跳过),所以后续消息一律由 EncodeUserMessage 编码成
// prompt 命令写入。
func (p *PiProtocol) SessionArgs(sysPrompt string, tools []string) ([]string, error) {
	hardened, err := p.hardenedArgs(tools, true)
	if err != nil {
		return nil, err
	}
	args := append([]string{"--mode", "rpc"}, hardened...)
	if sysPrompt != "" {
		args = append(args, "--system-prompt", sysPrompt)
	}
	return args, nil
}

// ResumeArgs 在 SessionArgs 基础上追加 --session <id>。
//
// 与 Claude 的 --resume 不同,pi 的 --session 接受「会话文件路径或 UUID 前缀」,
// 且必须能在 --session-dir(此处即 Env 注入的 <userDir>/.pi-sessions)里找到。
// 切换后端后存量 ID 对 pi 无意义,进程会启动即失败,由既有 onResumeFailed
// 降级链清掉缓存 ID(规格「旧 session ID 处理」)。
func (p *PiProtocol) ResumeArgs(prevSessionID, sysPrompt string, tools []string) ([]string, error) {
	args, err := p.SessionArgs(sysPrompt, tools)
	if err != nil {
		return nil, err
	}
	return append(args, "--session", prevSessionID), nil
}

// OnceArgs 构造一次性调用的旗标。print=true 用 --mode json(需要事件流,
// 对应 Client.Send/SendWithTools);print=false 用 -p(纯文本输出,对应
// SendSimpleWithRead)。两种情况下 prompt 都由调用方写入 stdin(计划 D2):
// main.js:701-708 在非 rpc 模式读管道 stdin,initial-message.js:6-18 把它
// **前置**拼进初始 prompt,所以 argv 里绝不能再带 prompt,否则两段会被拼接。
//
// model hint 被**忽略**(规格决策 3:沿用 pi 全局配置,不传 --model/--provider)。
// 断言产出里不含这两个旗标。
//
// -p 放在最后:pi 的参数解析(cli/args.js:172-176)对 -p 会贪婪吞掉紧随其后
// 那个不以 "-" 开头的实参当作 message。放在末尾让这类误吞在结构上不可能发生。
func (p *PiProtocol) OnceArgs(sysPrompt string, tools []string, print bool, model string) ([]string, error) {
	hardened, err := p.hardenedArgs(tools, false)
	if err != nil {
		return nil, err
	}
	var args []string
	if print {
		args = append([]string{"--mode", "json"}, hardened...)
	} else {
		args = hardened
	}
	if sysPrompt != "" {
		args = append(args, "--system-prompt", sysPrompt)
	}
	if !print {
		args = append(args, "-p")
	}
	return args, nil
}

// InitCommands 返回 spawn 后要立刻写入 stdin 的 get_state 命令(计划 D1)。
//
// pi 在 rpc 模式下启动后不主动输出任何行(实测 spawn 后 2s 内 0 行),docs/json.md
// 里那个 {"type":"session",...} 头行只属于 --mode json。因此 sessionId 只能主动
// 去取。PiProtocol.ParseLine 会把本命令的 response 归一化成
// StreamEvent{Type:"system", Subtype:"init", SessionID:...},使上层既有的
// waitForInit / onSessionID 别名注册 / local-<UnixNano> fallback / onResumeFailed
// 降级链全部无需改动,claude 包也就不必知道后端差异。
//
// 行自带结尾换行,与 Encode* 的约定一致。id 固定为 "init-1":每个子进程一条
// stdin 流、一次握手,无需唯一性;pi 会在 response 里回显该 id。
func (p *PiProtocol) InitCommands() [][]byte {
	return [][]byte{[]byte(`{"id":"init-1","type":"get_state"}` + "\n")}
}

// Env 构造 pi 子进程环境。
//
// 继承当前环境,先剔除下面四个键的既有值再注入 —— 不靠「后写的覆盖先写的」这种
// 实现细节(Node 的 process.env 按数组顺序后者胜,但那是实现细节,且 ClaudeProtocol
// 对 ALLOWED_DIR 已有显式过滤的先例)。
//
// allowedDir 经 filepath.EvalSymlinks 解析,以匹配沙箱 extension 自身 realpath 的
// 结果(macOS 上 /tmp 是 /private/tmp 的符号链接)。
//
// 注入的四个键:
//
//   - ALLOWED_DIR:沙箱根。未设置时 extension 拒绝一切工具调用(fail-closed),
//     故 allowedDir 为空时本函数返回 nil,与 ClaudeProtocol 一致。
//   - PI_WEB_TOOLS:与 --tools 里的 web 部分同源(同一个 p.webTools),供 extension
//     判定哪些工具属于联网类而只需校验 URL。
//   - PI_CODING_AGENT_SESSION_DIR:**计划 D4,对规格的一处偏离。** 规格写的是
//     `--session-dir <userDir>/.pi-sessions`,但 Plan 1 冻结的
//     SessionArgs(sysPrompt, tools) 签名里没有 userDir。照字面实现只有两条路,
//     都更差:①传相对值 .pi-sessions —— 它能工作(pi 从不 process.chdir,
//     实测 grep 零命中;cli/args.js:88-89 逐字取值,SessionManager 只做
//     normalizePath 不绝对化,utils/paths.js:58-80),但把正确性挂在「子进程 cwd
//     恰好等于 userDir」这个隐式耦合上;②给三个 *Args 加 userDir 形参 —— 波及
//     ClaudeProtocol 与所有调用点,直接违反「claude 路径行为不变」。
//     改走 env:pi 的优先级是 旗标 > PI_CODING_AGENT_SESSION_DIR > settings
//     (main.js:531-534),不传旗标即由本 env 生效,且它**压过** settings.json 里
//     可能被运维设过的 sessionDir。Env(allowedDir) 本就拿到 userDir 且已做
//     EvalSymlinks,于是会话目录与 ALLOWED_DIR 同源同 realpath,不可能漂移。
//   - PI_CODING_AGENT_DIR:见 piAgentDirForChild(不变式 I1)。
func (p *PiProtocol) Env(allowedDir string) []string {
	if allowedDir == "" {
		return nil
	}
	resolved := allowedDir
	if r, err := filepath.EvalSymlinks(allowedDir); err == nil {
		resolved = r
	}

	base := os.Environ()
	out := make([]string, 0, len(base)+4)
	for _, e := range base {
		switch {
		case strings.HasPrefix(e, "ALLOWED_DIR="),
			strings.HasPrefix(e, "PI_WEB_TOOLS="),
			strings.HasPrefix(e, "PI_CODING_AGENT_DIR="),
			strings.HasPrefix(e, "PI_CODING_AGENT_SESSION_DIR="):
			// 丢弃,由下面注入权威值
		default:
			out = append(out, e)
		}
	}

	out = append(out,
		"ALLOWED_DIR="+resolved,
		"PI_WEB_TOOLS="+strings.Join(p.webTools, ","),
		"PI_CODING_AGENT_SESSION_DIR="+filepath.Join(resolved, piSessionDirName),
	)
	if dir := piAgentDirForChild(); dir != "" {
		out = append(out, "PI_CODING_AGENT_DIR="+dir)
	}
	return out
}

// piAgentDirForChild 落实不变式 I1:返回值必须是
// filepath.Dir(config.PiWebSearchConfigPath()) **派生**出来的目录,不重算、
// 不重新读 env、不硬编码 ~/.pi/agent、不用 Settings/DB 里的另一个路径。
//
// I1 成立即彻底消除「Go 读的配置文件 ≠ pi 子进程读的配置文件」这条漂移:
// pi-web-access 的 getWebSearchConfigDir()(utils.ts:13-14)在
// PI_CODING_AGENT_DIR 非空时无条件返回它,注入后子进程内 XDG/legacy 两级不可达。
//
// 返回 ""(即不注入)的三种情形,都是因为注入一个坏值比不注入更糟 —— 不注入时
// pi 的 getAgentDir()(config.js:421-427)与 pi-web-access 的第 4 级回退都落到
// <home>/.pi/agent,两侧仍然一致:
//
//   - 配置路径为空:os.UserHomeDir() 失败(Task 1 的 I-3 修复让它返回 "" 而不是
//     ".",正是为了不落到服务进程 CWD)
//   - 以 ~ 开头:pi 的 getAgentDir() 会 expandTildePath 展开,而 pi-web-access 的
//     utils.ts:13-14 **不做** tilde 展开 —— 含 ~ 会让 auth 目录与 web-search 目录分家
//   - 相对路径且无法绝对化
//
// 相对路径本身是**可以**救的:filepath.Abs 以服务进程 CWD 为基准,而 Go 读
// web-search.json 时用的正是同一基准,所以绝对化后两侧仍然读同一个文件(I1 的
// 「必须绝对」一支)。这也比原值透传好:透传时 pi 会按子进程 cwd(= userDir)解析,
// 每个用户各解一份,必然与 Go 读的那份不一致。
func piAgentDirForChild() string {
	path := config.PiWebSearchConfigPath()
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	if strings.HasPrefix(dir, "~") {
		log.Printf("[agent] PI_CODING_AGENT_DIR 含 ~(%s):pi 的 getAgentDir() 会展开而 pi-web-access 不会,注入会让 auth 目录与 web-search.json 目录分家。已跳过注入,两侧一并回退 <home>/.pi/agent", dir)
		return ""
	}
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			log.Printf("[agent] PI_CODING_AGENT_DIR 是相对路径(%s)且无法绝对化:%v。已跳过注入,两侧一并回退 <home>/.pi/agent", dir, err)
			return ""
		}
		log.Printf("[agent] PI_CODING_AGENT_DIR 是相对路径(%s):pi 子进程的 cwd 是 userDir,透传会让它按 userDir 解析而与 Go 读的那份配置分家。已按服务进程 CWD 绝对化为 %s", dir, abs)
		dir = abs
	}
	return dir
}

// Probe 探测 pi 是否可用:LookPath + `pi --version`(5s 超时)。
//
// **已知盲区(风险登记 R8):** `pi --version` 在 main.js:483-486 提前 exit(0)、
// 不加载扩展,因此探测不到 web-search.json 的 toolNames 非法 —— 那会让扩展加载
// 失败、pi 以退出码 1 退出,整个 pi 后端不可用。Task 1 已在 Go 侧对该支告警,
// Task 10 写入运维警示。要在探测里覆盖它,得追加一次带扩展的最小 spawn(代价是
// 探测变慢),按计划留给 Task 7 决定。
func (p *PiProtocol) Probe(ctx context.Context) error {
	if _, err := exec.LookPath(p.bin); err != nil {
		return fmt.Errorf("pi not found in PATH (looked for %q): install with "+
			"npm install -g @earendil-works/pi-coding-agent: %w", p.bin, err)
	}
	cctx, cancel := context.WithTimeout(ctx, piProbeTimeout)
	defer cancel()
	if out, err := exec.CommandContext(cctx, p.bin, "--version").CombinedOutput(); err != nil {
		return fmt.Errorf("pi --version failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
