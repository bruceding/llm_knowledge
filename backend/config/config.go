package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	DataDir       string // ~/.llm-knowledge
	LogDir        string // ~/.llm-knowledge/logs
	Port          string
	ClaudeBin     string // claude binary path
	PiBin         string // pi binary path
	PDF2ZhVenvDir string // pdf2zh venv directory
}

func Load() *Config {
	// Command line flags
	port := flag.String("port", "", "Server port (default: 3456)")
	flag.Parse()

	// If flag not set, check environment variable
	portValue := *port
	if portValue == "" {
		portValue = os.Getenv("PORT")
	}
	if portValue == "" {
		portValue = "3456"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory if home directory cannot be determined
		home = "."
	}
	dataDir := filepath.Join(home, ".llm-knowledge")

	// Read Claude binary path from environment, default to "claude"
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	// Read pi binary path from environment, default to "pi"
	piBin := os.Getenv("PI_BIN")
	if piBin == "" {
		piBin = "pi"
	}

	// PDF2Zh venv directory - default to ~/.llm-knowledge/.venv
	pdf2zhVenvDir := os.Getenv("PDF2ZH_VENV_DIR")
	if pdf2zhVenvDir == "" {
		pdf2zhVenvDir = filepath.Join(dataDir, ".venv")
	}

	return &Config{
		DataDir:       dataDir,
		LogDir:        filepath.Join(dataDir, "logs"),
		Port:          portValue,
		ClaudeBin:     claudeBin,
		PiBin:         piBin,
		PDF2ZhVenvDir: pdf2zhVenvDir,
	}
}

// GetUserDir returns the per-user directory path: ~/.llm-knowledge/users/{userId}/
func GetUserDir(dataDir string, userId uint) string {
	return filepath.Join(dataDir, "users", strconv.FormatUint(uint64(userId), 10))
}

// PiWebToolNames 是 pi-web-access 注册的联网工具名。
//
// 本类型是这些名字的单一事实来源:解析结果一路用于 pi 的 --tools 白名单,另一路
// 经 PI_WEB_TOOLS env 传给沙箱 extension。工具名可被 web-search.json 的
// toolNames 改写,两边若各自硬编码字面量,改名后就会漂移。
//
// 只解析这三个。pi-web-access 还注册第四个工具 source_check,故意不解析也不
// 授予:它是研究场景专用,文档问答用不上,且多一个需要校验 URL 的入口(见
// docs/superpowers/specs/2026-09-12-pi-backend-switch-design.md「联网能力」一节
// 的决策表)。它在 backend/scripts/web-search.json.sample 里由
// tools.sourceCheck.enabled = false 显式关闭。
type PiWebToolNames struct {
	WebSearch        string
	FetchContent     string
	GetSearchContent string
}

// 默认工具名,与 pi-web-access v0.29.0 的 DEFAULT_TOOL_NAMES 逐字一致
// (index.ts:234-239)。
const (
	piDefaultWebSearchTool        = "web_search"
	piDefaultFetchContentTool     = "fetch_content"
	piDefaultGetSearchContentTool = "get_search_content"
	// sourceCheck 的名字本设计不采纳,但重名检测需要它的默认值参与
	// (pi 的 resolveToolNames 对**已启用**的四个键查重名,index.ts:303-311)
	piDefaultSourceCheckTool = "source_check"
)

// piToolNamePattern 与 pi-web-access 的 TOOL_NAME_PATTERN 一致(index.ts:240)。
// 不合式的名字在 pi 侧会抛错导致扩展加载失败,因此 Go 侧也不采纳,直接用默认名,
// 避免把一个 pi 永远不会注册的名字写进 --tools 白名单。
var piToolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// PiWebSearchConfigPath 返回 pi-web-access 读取的 web-search.json 路径。
//
// 只镜像 getWebSearchConfigDir()(utils.ts:10-26)四级回退里的第 1 级
// (PI_CODING_AGENT_DIR)与第 4 级(~/.pi/agent),中间的 XDG_CONFIG_HOME 与
// legacy ~/.pi 两级**有意不实现**,理由两条:
//
//   - 那两级是 pi-web-access 独有的怪癖。pi 本体的 getAgentDir()
//     (config.js:421-427)只有「PI_CODING_AGENT_DIR(经 expandTildePath 展开)
//     否则 ~/.pi/agent」,没有 XDG 回退,所以两级实现与 pi 本体语义一致。
//   - 只要下面的不变式 I1 成立,子进程内第 2、3 级就不可达(utils.ts:13-14 在
//     PI_CODING_AGENT_DIR 非空时直接返回它),Go 与 pi-web-access 必读同一文件。
//
// **I1(对 Task 2 的书面约束,尚未实现)**:每一次 pi spawn,PiProtocol.Env()
// 注入的 PI_CODING_AGENT_DIR 必须逐字等于 filepath.Dir(PiWebSearchConfigPath())
// 在 Go 进程内解析出的目录,且必须是「派生」而不是重算 ——
// "PI_CODING_AGENT_DIR=" + filepath.Dir(config.PiWebSearchConfigPath());
// 禁止重新读 env、禁止硬编码 ~/.pi/agent、禁止用 Settings/DB 里的另一个路径。
// 该值还必须非空、绝对、不含 ~:utils.ts:13-14 不做 tilde 展开,而 pi 的
// getAgentDir() 会做(config.js:408-409、:422-425),含 ~ 会让两者读到不同目录。
//
// 在 I1 落地之前:若 PI_CODING_AGENT_DIR 未设置而 XDG_CONFIG_HOME 已设置,
// pi-web-access 会去读 $XDG_CONFIG_HOME/pi/web-search.json,而本函数读
// ~/.pi/agent/web-search.json —— 两者可能不是同一个文件。另:本函数不校验
// PI_CODING_AGENT_DIR 是否为绝对路径(相对值会让 Go 按服务进程 CWD 解析,而
// pi 子进程的 cwd 是 userDir,两边必然不一致);这属 I1 的「必须绝对」一支,
// 由 Task 2 在注入前把关。
//
// home 取不到时返回 ""(而不是 "." 之类的相对路径):本函数决定的是**读**哪个
// 文件,相对路径会让它落到服务进程的 CWD,而 CWD 在部署里常常可写(systemd
// DynamicUser、容器),植入一份 web-search.json 就能改写工具名、同时击穿
// --tools 白名单与沙箱 extension 两道 source_check 防线。调用方见空串即用默认名。
// Load()(:36-40)里那个 home 回退不适用于此处:那里 home 用于算 dataDir
// (写入目标),语义不同。
func PiWebSearchConfigPath() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "web-search.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "web-search.json")
}

// LoadPiWebToolNames 解析联网工具名。任何异常一律回退默认名,不返回 error
// (规格要求:配置缺失或解析失败不报错,与 pi-web-access 的 loadSsrfConfig 的
// 宽容行为一致)。
//
// 但「回退默认名」在 pi 侧的后果分两类,不能混为一谈:
//
//	pi 吞掉(Go 与 pi 都落到默认名,行为一致 —— 属正常状态,不打日志):
//	  - 文件不存在             → loadConfig()(index.ts:210-213)返回 {}
//	  - JSON 非法 / 根不是对象  → parseConfigRoot(:196-208)抛,但被
//	                              loadConfigForExtensionInit(:315-322)catch 成 {}
//	pi 抛错(Go 回退默认名,而 pi 会整体拒绝启动 —— 必须给运维留信号):
//	  - toolNames 不是对象 / 为 null、某个值不是字符串或不合 TOOL_NAME_PATTERN、
//	    已注册键重名 → resolveToolNames(index.ts:288-313)抛错
//
// 第二类的后果不是「联网功能不可用」,而是「整个 pi 后端不可用」:
// resolveToolNames 在 index.ts:1068 被调用,位于 loadConfigForExtensionInit 的
// try/catch **之外**,而四个工具的注册都在其后(:1789 web_search、
// :2387 source_check、:2486 fetch_content、:2830 get_search_content),所以抛错
// 即一个工具都不注册;pi 把它记为扩展加载错误(core/extensions/loader.js:483-486
// → dist/main.js:631-634 转成 type:"error" 诊断),而 main.js:722 的
// hasRuntimeErrors 一旦为真就在 :726-731 直接 process.exit(1)。这段在所有 mode
// 的公共启动路径上(含 --mode rpc),连不用 web 工具的 ingest 链路一起死。
//
// 2026-09-13 实测(pi 0.85.1;临时 PI_CODING_AGENT_DIR,auth.json/settings.json/
// npm/bin 均为指向真实目录的只读 symlink,两组唯一差异是 web-search.json):
//
//	{"allowBrowserCookies":false}     → 退出码 0,stderr 为空,rpc 模式正常输出事件
//	{"toolNames":{"webSearch":42}}    → 退出码 1,stderr:
//	  Error: Failed to load extension ".../pi-web-access/index.ts": Failed to
//	  load extension: toolNames.webSearch in ".../web-search.json" must be a string
//	  Hint: Start without extensions using "pi -ne".
//
// 更麻烦的是它**探测不到**:pi --version 在 dist/main.js:483-486 提前 exit(0),
// 根本不加载扩展,所以 Task 2 的 Probe 与 Task 7 的切换探测都会通过,随后每个
// 请求才失败、Go 侧零日志。因此第二类在此打一条 log —— 规格禁止的是返回
// error,不禁止日志;第一类保持静默,否则每次调用都会刷屏。
func LoadPiWebToolNames() PiWebToolNames {
	names, problem := ValidatePiWebConfig()
	if problem != "" {
		// 前缀在这里加而不是在 ValidatePiWebConfig 里:那个函数返回的字符串还要
		// 直接进 PUT /api/admin/settings 的 400 响应体,带日志前缀会很怪。
		log.Printf("[config] %s", problem)
	}
	return names
}

// ValidatePiWebConfig 与 LoadPiWebToolNames 读同一份配置、用**同一套判定**,区别只是
// 把「会让 pi 整体拒绝启动」的问题作为返回值交出来,而不是只打日志。
//
// 存在的理由:管理员在 Settings 里把后端切到 pi 时,api 层需要据此把风险登记 R8
// 那种「切换探测通过、随后每个请求都失败」的情形提前拦成 400。而 R8 的探测盲区是
// 结构性的 —— `pi --version` 在 main.js:483-486 提前 exit(0)、根本不加载扩展。
// 与其在 HTTP handler 里 spawn 一次真 pi(慢、有副作用、且实测 pi 在 rpc 模式下
// stdin EOF 后并不退出),不如让 Go 侧把它**已经能看见**的那份配置判定复用一遍:
// Go 与 pi-web-access 读的是同一个文件(不变式 I1 保证),所以 Go 能算出 pi 会不会抛错。
//
// 返回的 problem 为空串表示「pi 不会因这份配置拒绝启动」。
func ValidatePiWebConfig() (names PiWebToolNames, problem string) {
	names = PiWebToolNames{
		WebSearch:        piDefaultWebSearchTool,
		FetchContent:     piDefaultFetchContentTool,
		GetSearchContent: piDefaultGetSearchContentTool,
	}

	path := PiWebSearchConfigPath()
	if path == "" {
		// 配置位置不可解析(PI_CODING_AGENT_DIR 未设置且 home 取不到)。
		// 显式判空,不依赖 os.ReadFile("") 报错 —— 尤其不能退化成读 CWD。
		return names, ""
	}

	// 用 defer + 具名返回值组装 problem,于是下面那些提前 return 的分支不必各自
	// 拼消息,也不会漏掉(漏掉就等于该情形静默放过)。
	var reasons []string
	defer func() {
		if len(reasons) == 0 {
			return
		}
		problem = fmt.Sprintf("%s: %s —— Go 侧沿用默认工具名;但 pi-web-access 会在同一处配置上抛错(resolveToolNames, index.ts:288-313),导致扩展加载失败、pi 以退出码 1 退出,整个 pi 后端不可用(含不用 web 工具的 ingest 链路),而 `pi --version` 探测不到。请修正该文件",
			path, strings.Join(reasons, ";"))
	}()

	data, err := os.ReadFile(path)
	if err != nil {
		return names, "" // 文件不存在或不可读:pi 侧同样当作 {} 处理,属正常状态
	}

	// 两级解析,以便区分「根不是对象」(pi 吞掉)与「toolNames 非法」(pi 抛错)。
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return names, "" // JSON 非法或根不是对象:pi 的 parseConfigRoot 抛后被 catch 成 {}
	}

	// toolEnabled 镜像 isToolEnabled(index.ts:271-274)的 `config.tools?.[key]?.enabled
	// !== false`:只有显式 false 才算关。重名检测必须按它过滤 —— pi 的
	// resolveToolNames(:303-311)只对**已启用**的键查重名,不过滤就会对 pi 其实
	// 接受的重名误报,进而把一次合法的后端切换拦成 400。
	toolEnabled := func(key string) bool {
		rawTools, ok := root["tools"]
		if !ok {
			return true
		}
		var tools map[string]json.RawMessage
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return true
		}
		rawEntry, ok := tools[key]
		if !ok {
			return true
		}
		var entry struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			return true
		}
		return entry.Enabled == nil || *entry.Enabled
	}

	rawToolNames, present := root["toolNames"]
	if !present {
		return names, "" // 键缺席:pi 用 DEFAULT_TOOL_NAMES,与 Go 一致,四个默认名互不重复
	}

	// 到这里 toolNames 键存在。它一旦非法,pi 会 exit 1(见函数注释),所以往下的
	// 每条非法分支都必须留下 reason。
	warnInvalid := func(reason string) { reasons = append(reasons, reason) }

	if string(rawToolNames) == "null" {
		// pi 侧:`config.toolNames !== undefined && !config.toolNames` → 抛错。
		// Go 侧 json.Unmarshal(null) 到 map 不报错,故必须显式拦下。
		warnInvalid("toolNames 为 null")
		return names, ""
	}
	// 键名是驼峰,与 pi-web-access 的 ToolNames 类型一致(index.ts:227-232)。
	var toolNames map[string]json.RawMessage
	if err := json.Unmarshal(rawToolNames, &toolNames); err != nil {
		warnInvalid("toolNames 不是 JSON 对象")
		return names, ""
	}

	// valid 返回某个键的合法工具名;键缺席或非法都返回 nil(即沿用默认名),
	// 区别只在于非法时留下 reason —— 那正是 pi 会 exit 1 的情形。
	valid := func(key string) *string {
		raw, ok := toolNames[key]
		if !ok {
			return nil
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			warnInvalid(fmt.Sprintf("toolNames.%s 不是字符串", key))
			return nil
		}
		// pi 侧同样先 trim 再校验(index.ts:297-301)。注意 strings.TrimSpace 与
		// JS 的 String.prototype.trim() 字符集不同:Go 剥 U+0085(NEL)但不剥
		// U+FEFF(BOM),JS 相反。两个方向的后果都只是名字与 pi 实际注册名不符,
		// 而 --tools 是 fail-closed 的(名字不匹配 = 工具调不到),不构成安全问题,
		// 故不为此引入逐字符对齐。
		trimmed := strings.TrimSpace(value)
		if !piToolNamePattern.MatchString(trimmed) {
			warnInvalid(fmt.Sprintf("toolNames.%s = %q 不合 %s", key, value, piToolNamePattern))
			return nil
		}
		return &trimmed
	}

	if v := valid("webSearch"); v != nil {
		names.WebSearch = *v
	}
	if v := valid("fetchContent"); v != nil {
		names.FetchContent = *v
	}
	if v := valid("getSearchContent"); v != nil {
		names.GetSearchContent = *v
	}
	// sourceCheck 不采纳(本设计不授予该工具),但它非法同样会让 pi exit 1,
	// 校验一次只为留下 reason。pi 的 resolveToolNames 也只校验 ToolNames 的四个
	// 已知键(index.ts:293 遍历 DEFAULT_TOOL_NAMES 的键),未知键被忽略不抛错,
	// 所以这里同样不校验未知键 —— 否则会产生 pi 侧根本不会失败的假告警。
	sourceCheck := valid("sourceCheck")

	// 重名检测:pi 的 resolveToolNames(index.ts:303-311)对**已启用**的键查到重名
	// 即抛错 → 扩展加载失败 → pi exit 1。这是 Task 1 原先没覆盖的一条(Task 2 的
	// resolvePiWebTools 只在运行时去重并告警,拦不住切换动作本身),故补在此处。
	// 四个键的生效名字都要参与:sourceCheck 虽然不被本设计采纳,但只要它在配置里
	// 是启用的,pi 就会拿它参与查重。
	resolved := map[string]string{
		"webSearch":        names.WebSearch,
		"fetchContent":     names.FetchContent,
		"getSearchContent": names.GetSearchContent,
		"sourceCheck":      piDefaultSourceCheckTool,
	}
	if sourceCheck != nil {
		resolved["sourceCheck"] = *sourceCheck
	}
	seen := map[string]string{}
	for _, key := range []string{"webSearch", "sourceCheck", "fetchContent", "getSearchContent"} {
		if !toolEnabled(key) {
			continue
		}
		name := resolved[key]
		if prev, dup := seen[name]; dup {
			warnInvalid(fmt.Sprintf("toolNames.%s 与 toolNames.%s 重名(都解析为 %q)", key, prev, name))
			continue
		}
		seen[name] = key
	}

	return names, ""
}

// Names 按固定顺序返回授予的工具名,供 --tools 白名单与 PI_WEB_TOOLS 共用,
// 使两条路径不可能给出不同的集合或顺序。
func (n PiWebToolNames) Names() []string {
	return []string{n.WebSearch, n.FetchContent, n.GetSearchContent}
}

// piWebCommandNames 是 pi-web-access 注册的四个扩展命令名。
// 取自 index.ts:277 的 isCommandEnabled 形参类型
// ("websearch" | "curator" | "search" | "google-account"),与 :3164/:3427/:3517/:3469
// 四处 pi.registerCommand 一一对应。
var piWebCommandNames = []string{"websearch", "curator", "search", "google-account"}

// PiWebCommandsEnabled 返回部署的 web-search.json 里**仍然启用**的 pi-web-access
// 扩展命令名。与 LoadPiWebToolNames 一样不返回 error。
//
// 判定完全镜像 isCommandEnabled(index.ts:277-279)的 `config.commands?.[name]?.enabled
// !== false`,因此下列情形**全部算开着**:整个文件不存在、JSON 非法、根不是对象、
// commands 段缺席、commands 为 null/非对象、某个命令键缺席、enabled 不是布尔、
// enabled 为 true。只有显式的 `"enabled": false` 才算关。
//
// 为什么这件事需要 Go 侧留信号:rpc 模式下以 `/` 开头的用户消息会被 pi 当扩展
// 命令派发执行并直接跑那段 JS —— 链路是 modes/rpc/rpc-mode.js:301-304 调
// session.prompt() 时**没传** expandPromptTemplates,core/agent-session.js:822 的
// 默认值是 true,:828-834 一命中就调 _tryExecuteExtensionCommand(:954-961)。
// 四道既有防线全拦不住:`--tools` 只管工具调用(命令不是工具)、
// `--no-skills`/`--no-prompt-templates` 只关 skill 与模板、沙箱 extension 的
// `input` hook 在 :839-851(位于命令派发**之后**)、`-na` 与此无关。
// Claude 侧的对应物 SlashCommand 早就在 ClaudeDangerousDisallowedTools 里硬阻断,
// 所以这是追平两个后端的安全强度。
//
// 而唯一的收口就是这份配置文件,它在运维手里、不在仓库里:漏配时服务照常启动、
// 文档问答照常工作、零信号,属静默 fail-open。本函数只负责**检测**,
// 告警由 agent 包在构造 PiProtocol 时发出。
//
// 注意本函数**盖不到其他包**:web-search.json 只有 pi-web-access 读,所以它只能
// 关 pi-web-access 自己的命令。其他已加载包(例如 pi-subagents 注册的 /run,它
// 会 spawn 一个不带我们沙箱 extension 的子进程)只能靠运维隔离 ——
// 生产用独立 PI_CODING_AGENT_DIR,其 settings.json 的 packages 只含 pin 过的
// pi-web-access。详见计划的风险登记 R9 与 R1。
func PiWebCommandsEnabled() []string {
	allOn := func() []string { return append([]string(nil), piWebCommandNames...) }

	path := PiWebSearchConfigPath()
	if path == "" {
		return allOn() // home 取不到:无从判定,按最不安全的一侧报
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return allOn() // 文件不存在/不可读:pi 的 loadConfig 返回 {},全部走默认(开)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		// JSON 非法或根不是对象(数组/标量):pi 的 parseConfigRoot 抛后被
		// loadConfigForExtensionInit catch 成 {},同样等于全开。
		return allOn()
	}
	rawCommands, ok := root["commands"]
	if !ok {
		return allOn()
	}
	var commands map[string]json.RawMessage
	if err := json.Unmarshal(rawCommands, &commands); err != nil {
		return allOn() // commands 为 null / 数组 / 标量:pi 的 ?. 链同样走到 undefined
	}

	enabled := make([]string, 0, len(piWebCommandNames))
	for _, name := range piWebCommandNames {
		raw, ok := commands[name]
		if !ok {
			enabled = append(enabled, name)
			continue
		}
		var entry struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			enabled = append(enabled, name) // entry 不是对象:?.enabled 得 undefined
			continue
		}
		if entry.Enabled == nil || *entry.Enabled {
			enabled = append(enabled, name)
		}
	}
	return enabled
}
