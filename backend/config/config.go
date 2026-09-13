package config

import (
	"encoding/json"
	"flag"
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
)

// piToolNamePattern 与 pi-web-access 的 TOOL_NAME_PATTERN 一致(index.ts:240)。
// 不合式的名字在 pi 侧会抛错导致扩展加载失败,因此 Go 侧也不采纳,直接用默认名,
// 避免把一个 pi 永远不会注册的名字写进 --tools 白名单。
var piToolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// PiWebSearchConfigPath 返回 pi-web-access 读取的 web-search.json 路径。
//
// 对齐 pi-web-access 的 getWebSearchConfigDir()(utils.ts:10-26):优先
// PI_CODING_AGENT_DIR,否则回退 ~/.pi/agent。utils.ts 里那条 XDG_CONFIG_HOME
// 分支故意不镜像 —— 设计文档要求 Env() 显式注入 PI_CODING_AGENT_DIR(「显式
// 钉死,使安全配置位置确定化」),pi 路径下第一分支必然命中,XDG 分支不可达。
func PiWebSearchConfigPath() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "web-search.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// 与 Load() 一致:取不到 home 时退回当前目录,让调用方按「文件不存在」处理
		home = "."
	}
	return filepath.Join(home, ".pi", "agent", "web-search.json")
}

// LoadPiWebToolNames 解析联网工具名。文件缺失、JSON 非法、toolNames 不是对象、
// 某个值不是合式字符串 —— 一律静默回退默认名,不返回 error。
//
// 宽容是刻意的,且与 pi 侧的严格不矛盾:pi-web-access 对这些情况是抛错的
// (index.ts:288-313 的 resolveToolNames),配置写错时扩展加载即失败、联网工具
// 整体不可用;Go 侧回退默认名只会让白名单里的名字与真实注册名不符,而 --tools
// 是 fail-closed 的(名字不匹配 = 工具调不到),最坏结果是联网功能不可用,
// 不会放行未预期的工具。
func LoadPiWebToolNames() PiWebToolNames {
	names := PiWebToolNames{
		WebSearch:        piDefaultWebSearchTool,
		FetchContent:     piDefaultFetchContentTool,
		GetSearchContent: piDefaultGetSearchContentTool,
	}

	data, err := os.ReadFile(PiWebSearchConfigPath())
	if err != nil {
		return names
	}

	// 键名是驼峰,与 pi-web-access 的 ToolNames 类型一致(index.ts:227-232)。
	// 用 RawMessage 区分「键缺席」与「值非法」:两者都保持默认名。
	var parsed struct {
		ToolNames struct {
			WebSearch        json.RawMessage `json:"webSearch"`
			FetchContent     json.RawMessage `json:"fetchContent"`
			GetSearchContent json.RawMessage `json:"getSearchContent"`
		} `json:"toolNames"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return names
	}

	apply := func(raw json.RawMessage, current *string) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return
		}
		// pi 侧同样先 trim 再校验(index.ts:297-301)
		if trimmed := strings.TrimSpace(value); piToolNamePattern.MatchString(trimmed) {
			*current = trimmed
		}
	}
	apply(parsed.ToolNames.WebSearch, &names.WebSearch)
	apply(parsed.ToolNames.FetchContent, &names.FetchContent)
	apply(parsed.ToolNames.GetSearchContent, &names.GetSearchContent)
	return names
}

// Names 按固定顺序返回授予的工具名,供 --tools 白名单与 PI_WEB_TOOLS 共用,
// 使两条路径不可能给出不同的集合或顺序。
func (n PiWebToolNames) Names() []string {
	return []string{n.WebSearch, n.FetchContent, n.GetSearchContent}
}