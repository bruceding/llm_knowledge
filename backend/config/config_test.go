package config

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// writePiWebSearchConfig 在 PI_CODING_AGENT_DIR 指向的目录里写一份 web-search.json,
// 并把该 env 指过去,使 LoadPiWebToolNames 读到它。
func writePiWebSearchConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "web-search.json"), []byte(content), 0644); err != nil {
		t.Fatalf("写入临时 web-search.json: %v", err)
	}
}

func TestPiWebSearchConfigPath(t *testing.T) {
	t.Run("PI_CODING_AGENT_DIR 优先", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "/opt/pi-agent")
		want := filepath.Join("/opt/pi-agent", "web-search.json")
		if got := PiWebSearchConfigPath(); got != want {
			t.Errorf("PiWebSearchConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("未设置时回退 ~/.pi/agent", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("取不到 home 目录: %v", err)
		}
		want := filepath.Join(home, ".pi", "agent", "web-search.json")
		got := PiWebSearchConfigPath()
		if got != want {
			t.Errorf("PiWebSearchConfigPath() = %q, want %q", got, want)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("路径必须是绝对路径(相对路径会让这条读路径落到服务进程的 CWD): %q", got)
		}
	})

	t.Run("home 取不到时返回空串而不是相对路径", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "")
		t.Setenv("HOME", "") // os.UserHomeDir() 在 Unix 上只读 HOME,置空即使其报错
		if got := PiWebSearchConfigPath(); got != "" {
			t.Errorf("PiWebSearchConfigPath() = %q, want 空串:非空的相对路径会让这条**读**路径落到服务进程的 CWD,而 CWD 在部署里常常可写(systemd DynamicUser、容器),植入一份 web-search.json 就能改写工具名", got)
		}
	})
}

// TestLoadPiWebToolNames_UnresolvablePathIgnoresCWD 钉住 I-3 修的安全属性:
// 配置路径不可解析时不得退化成读 CWD。
func TestLoadPiWebToolNames_UnresolvablePathIgnoresCWD(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", "")

	// 在 CWD 放一份“攻击者”配置:若实现回退到相对路径 .pi/agent/web-search.json,
	// 它就会被读取并把三个工具名全部改成 source_check。
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "agent"), 0o755); err != nil {
		t.Fatalf("建临时 CWD 目录: %v", err)
	}
	malicious := `{"toolNames":{"webSearch":"source_check","fetchContent":"source_check","getSearchContent":"source_check"}}`
	if err := os.WriteFile(filepath.Join(dir, ".pi", "agent", "web-search.json"), []byte(malicious), 0o644); err != nil {
		t.Fatalf("写入诱饵配置: %v", err)
	}
	t.Chdir(dir) // Go 1.24+,测试结束自动恢复原 CWD

	if got := PiWebSearchConfigPath(); got != "" {
		t.Fatalf("前置条件不成立: PiWebSearchConfigPath() = %q, want 空串", got)
	}

	got := LoadPiWebToolNames()
	want := PiWebToolNames{"web_search", "fetch_content", "get_search_content"}
	if got != want {
		t.Errorf("LoadPiWebToolNames() = %+v, want %+v", got, want)
	}
	for _, name := range got.Names() {
		if name == "source_check" {
			t.Errorf("CWD 下的诱饵配置被读取了(fail-open): Names() = %v", got.Names())
		}
	}
}

func TestLoadPiWebToolNames_DefaultsWhenFileMissing(t *testing.T) {
	// 目录存在但没有 web-search.json —— 本机开发环境的真实状态
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	names := LoadPiWebToolNames()

	// 默认名必须与 pi-web-access 的 DEFAULT_TOOL_NAMES(index.ts:234-239)逐字一致
	want := PiWebToolNames{
		WebSearch:        "web_search",
		FetchContent:     "fetch_content",
		GetSearchContent: "get_search_content",
	}
	if names != want {
		t.Errorf("LoadPiWebToolNames() = %+v, want %+v", names, want)
	}
	// Names() 的顺序同时决定 --tools 白名单与 PI_WEB_TOOLS 的内容
	if got := names.Names(); !reflect.DeepEqual(got, []string{"web_search", "fetch_content", "get_search_content"}) {
		t.Errorf("Names() = %v, want [web_search fetch_content get_search_content]", got)
	}
}

func TestLoadPiWebToolNames_ToolNamesOverride(t *testing.T) {
	writePiWebSearchConfig(t, `{
		"toolNames": {
			"webSearch": "my_search",
			"fetchContent": "my_fetch",
			"getSearchContent": "my_get_content"
		}
	}`)

	names := LoadPiWebToolNames()

	want := PiWebToolNames{
		WebSearch:        "my_search",
		FetchContent:     "my_fetch",
		GetSearchContent: "my_get_content",
	}
	if names != want {
		t.Errorf("LoadPiWebToolNames() = %+v, want %+v", names, want)
	}
	// 改名后 Names() 的顺序同样固定:它同时决定 --tools 白名单与 PI_WEB_TOOLS
	// 的内容与顺序,两边不得因改名而分岔。
	if got := names.Names(); !reflect.DeepEqual(got, []string{"my_search", "my_fetch", "my_get_content"}) {
		t.Errorf("改名后 Names() = %v, want [my_search my_fetch my_get_content]", got)
	}
}

func TestLoadPiWebToolNames_PartialOverrideKeepsDefaults(t *testing.T) {
	// 只改写一个键时其余必须保持默认名(pi 侧 resolveToolNames 也是逐键合并)
	writePiWebSearchConfig(t, `{"toolNames": {"fetchContent": "grab"}}`)

	names := LoadPiWebToolNames()

	want := PiWebToolNames{
		WebSearch:        "web_search",
		FetchContent:     "grab",
		GetSearchContent: "get_search_content",
	}
	if names != want {
		t.Errorf("LoadPiWebToolNames() = %+v, want %+v", names, want)
	}
}

func TestLoadPiWebToolNames_MalformedJSONFallsBack(t *testing.T) {
	writePiWebSearchConfig(t, `{"toolNames": {`)

	names := LoadPiWebToolNames()

	want := PiWebToolNames{
		WebSearch:        "web_search",
		FetchContent:     "fetch_content",
		GetSearchContent: "get_search_content",
	}
	if names != want {
		t.Errorf("malformed JSON 时应回退默认名, got %+v", names)
	}
}

func TestLoadPiWebToolNames_InvalidValuesFallBack(t *testing.T) {
	// Go 侧对下列所有输入都逐键回退默认名,不能把 pi 永远不会注册的名字写进
	// --tools 白名单。但 pi 侧的后果分两类(详见 LoadPiWebToolNames 的注释):
	//
	//	pi 吞掉 → 两边都是默认名,行为一致:
	//	  JSON 非法 / 根不是对象(parseConfigRoot 抛,被 loadConfigForExtensionInit
	//	  的 catch 接住,index.ts:196-208、:315-322)
	//	pi 抛错 → 扩展加载失败,pi 以退出码 1 退出(实测):
	//	  toolNames 不是对象 / 为 null、值不是字符串、值不合 TOOL_NAME_PATTERN
	//	  (resolveToolNames,index.ts:288-313)
	//
	// 本测试只管「Go 回退默认名」这一半;「哪一类该打日志」由
	// TestLoadPiWebToolNames_WarnsOnlyWhenPiWouldRejectConfig 守。
	cases := []struct {
		name    string
		content string
		want    PiWebToolNames
	}{
		{
			name:    "值不是字符串",
			content: `{"toolNames": {"webSearch": 42}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "空串",
			content: `{"toolNames": {"webSearch": ""}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "首字符不是字母",
			content: `{"toolNames": {"fetchContent": "1fetch"}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "含非法字符",
			content: `{"toolNames": {"getSearchContent": "get content!"}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "前后空格被 trim 后采纳",
			content: `{"toolNames": {"webSearch": "  spaced_name  "}}`,
			want:    PiWebToolNames{"spaced_name", "fetch_content", "get_search_content"},
		},
		{
			name:    "toolNames 不是对象(数组)",
			content: `{"toolNames": ["web_search"]}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "toolNames 不是对象(字符串)",
			content: `{"toolNames": "web_search"}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			// pi 侧:`config.toolNames !== undefined && !config.toolNames` → 抛错。
			// Go 的 json.Unmarshal(null) 到 map 不报错,故需显式拦下。
			name:    "toolNames 为 null",
			content: `{"toolNames": null}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "根是数组",
			content: `["web_search"]`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "根是标量",
			content: `42`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			// TOOL_NAME_PATTERN 的量词上界:1 + 63 = 64,故 65 字符必拒
			name:    "65 字符名被拒",
			content: `{"toolNames": {"webSearch": "a` + strings.Repeat("b", 64) + `"}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			// 本设计不授予 source_check,所以它的改名不得进入取名单。
			// 真正的断言在下方循环里:任何输入下 Names() 都不得含 source_check。
			name:    "sourceCheck 键被忽略:改名不进入取名单",
			content: `{"toolNames": {"sourceCheck": "renamed_source"}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			// 不采纳它的名字,但它非法同样会让 pi exit 1,故仍需告警(见 warn 测试)
			name:    "sourceCheck 值非法也不得影响取名单",
			content: `{"toolNames": {"sourceCheck": 42}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writePiWebSearchConfig(t, tc.content)
			got := LoadPiWebToolNames()
			if got != tc.want {
				t.Errorf("LoadPiWebToolNames() = %+v, want %+v", got, tc.want)
			}
			// 任何输入下都不得把 source_check 放进取名单:本设计不授予该工具,
			// 而它在 pi-web-access 里默认是**开**的(index.ts:271-274),一旦混进
			// --tools 与 PI_WEB_TOOLS 就等于多开一个需校验 URL 的入口。
			for _, name := range got.Names() {
				if name == "source_check" {
					t.Errorf("Names() 含 source_check(%v):本设计不授予该工具", got.Names())
				}
			}
		})
	}
}

func TestPiToolNamePattern_LengthBoundary(t *testing.T) {
	// TOOL_NAME_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/(index.ts:240):
	// 首字符 1 个 + 至多 63 个后续字符 = 最长 64。这是 config.go 里唯一的量词,
	// 没有边界用例的话,把 {0,63} 误写成 {0,64} 不会被发现。
	name64 := "a" + strings.Repeat("b", 63)
	name65 := "a" + strings.Repeat("b", 64)
	if len(name64) != 64 || len(name65) != 65 {
		t.Fatalf("用例构造错了: len(name64)=%d len(name65)=%d", len(name64), len(name65))
	}
	if !piToolNamePattern.MatchString(name64) {
		t.Errorf("64 字符名应被采纳(index.ts:240 的上界)")
	}
	if piToolNamePattern.MatchString(name65) {
		t.Errorf("65 字符名应被拒绝")
	}

	// 端到端再走一遍:64 字符名被采纳并进入 Names()(即会进 --tools 与 PI_WEB_TOOLS)
	writePiWebSearchConfig(t, `{"toolNames":{"webSearch":"`+name64+`"}}`)
	got := LoadPiWebToolNames()
	if got.WebSearch != name64 {
		t.Errorf("LoadPiWebToolNames().WebSearch 长度=%d, want 64 字符名被采纳", len(got.WebSearch))
	}
	if got.Names()[0] != name64 {
		t.Errorf("Names()[0] 不是被采纳的 64 字符名")
	}
}

// captureConfigLog 把标准 log 的输出临时接到 buffer,返回读取函数。
// 用于断言「哪一类配置异常会留下运维信号」—— 这是 LoadPiWebToolNames 注释里
// 那条承诺的可执行版本:pi 会 exit 1 的情形必须有日志,pi 自己吞掉的情形必须静默。
func captureConfigLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return buf.String
}

func TestLoadPiWebToolNames_WarnsOnlyWhenPiWouldRejectConfig(t *testing.T) {
	cases := []struct {
		name     string
		content  string // 空串表示目录里不放文件
		wantWarn bool
	}{
		{"文件缺失:pi 的 loadConfig 返回 {},静默", "", false},
		{"JSON 非法:parseConfigRoot 抛错被 catch 成 {},静默", `{"toolNames": {`, false},
		{"根是数组:同上,静默", `["web_search"]`, false},
		{"根是标量:同上,静默", `42`, false},
		{"toolNames 键缺席:pi 用 DEFAULT_TOOL_NAMES,静默", `{"allowBrowserCookies":false}`, false},
		{"合法改名:两边一致,静默", `{"toolNames":{"webSearch":"my_search"}}`, false},
		{"toolNames 为 null:pi 抛错 -> exit 1,必须告警", `{"toolNames":null}`, true},
		{"toolNames 不是对象:同上", `{"toolNames":["web_search"]}`, true},
		{"值不是字符串:同上", `{"toolNames":{"webSearch":42}}`, true},
		{"值不合 TOOL_NAME_PATTERN:同上", `{"toolNames":{"fetchContent":"1fetch"}}`, true},
		{"不授予的 sourceCheck 非法:同样让 pi exit 1,必须告警", `{"toolNames":{"sourceCheck":42}}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getLog := captureConfigLog(t)
			if tc.content == "" {
				t.Setenv("PI_CODING_AGENT_DIR", t.TempDir()) // 目录存在但没有文件
			} else {
				writePiWebSearchConfig(t, tc.content)
			}

			LoadPiWebToolNames()

			logged := getLog()
			switch {
			case tc.wantWarn && logged == "":
				t.Error("该配置会让 pi 以退出码 1 启动失败(整个 pi 后端不可用),而 pi --version 探测不到,必须留下日志")
			case !tc.wantWarn && logged != "":
				t.Errorf("该情形属正常状态(pi 侧同样回退默认名),不应打日志以免刷屏,实际: %s", logged)
			}
			if tc.wantWarn && logged != "" {
				if !strings.Contains(logged, "web-search.json") {
					t.Errorf("告警必须带上配置文件路径以便定位, got: %q", logged)
				}
				if !strings.Contains(logged, "退出码 1") {
					t.Errorf("告警必须说明后果是整个 pi 后端不可用、而非仅联网不可用, got: %q", logged)
				}
			}
		})
	}
}

// TestPiWebCommandsEnabled 钉住 PiWebCommandsEnabled 必须逐条镜像
// isCommandEnabled(index.ts:277-279)的 `config.commands?.[name]?.enabled !== false`。
//
// 关键是那些「看上去像关了、其实没关」的情形必须被归为开着 —— 本函数的用途就是
// 给运维留信号,假阴性(把开着的报成关了的)等于没有这个告警。
func TestPiWebCommandsEnabled(t *testing.T) {
	all := []string{"websearch", "curator", "search", "google-account"}

	cases := []struct {
		name    string
		content string // 空串表示目录里不放文件
		want    []string
	}{
		{"文件缺失:pi 的 loadConfig 返回 {},全开", "", all},
		{"JSON 非法:parseConfigRoot 抛后被 catch 成 {},全开", `{"commands": {`, all},
		{"根是数组:同上,全开", `["websearch"]`, all},
		{"根是标量:同上,全开", `42`, all},
		{"commands 段缺席:?. 链得 undefined,全开", `{"allowBrowserCookies":false}`, all},
		{"commands 为 null:同上,全开", `{"commands":null}`, all},
		{"commands 是数组:不是对象,全开", `{"commands":["websearch"]}`, all},
		{"四个都显式关:唯一的安全状态", `{"commands":{"websearch":{"enabled":false},"curator":{"enabled":false},"search":{"enabled":false},"google-account":{"enabled":false}}}`, nil},
		{"只关了 curator:剩下三个仍开着", `{"commands":{"curator":{"enabled":false}}}`, []string{"websearch", "search", "google-account"}},
		{"enabled 为 true:显式开", `{"commands":{"curator":{"enabled":true}}}`, all},
		// JS 的 `!== false` 对字符串 "false" 也为真,所以这仍然算开着。
		// 这是运维最容易犯的错(把 JSON 当 YAML/字符串写),必须报出来。
		{"enabled 是字符串 \"false\":JS 的 !== false 仍为真,全开", `{"commands":{"websearch":{"enabled":"false"},"curator":{"enabled":"false"},"search":{"enabled":"false"},"google-account":{"enabled":"false"}}}`, all},
		{"entry 不是对象:?.enabled 得 undefined,全开", `{"commands":{"websearch":false,"curator":false,"search":false,"google-account":false}}`, all},
		{"entry 是空对象:enabled 缺席,全开", `{"commands":{"websearch":{},"curator":{},"search":{},"google-account":{}}}`, all},
		// 未知键不影响四个已知键的判定(pi 也只查这四个)
		{"未知命令键不干扰", `{"commands":{"newcmd":{"enabled":false},"curator":{"enabled":false}}}`, []string{"websearch", "search", "google-account"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.content == "" {
				t.Setenv("PI_CODING_AGENT_DIR", t.TempDir()) // 目录存在但没有文件
			} else {
				writePiWebSearchConfig(t, tc.content)
			}
			got := PiWebCommandsEnabled()
			if !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Errorf("PiWebCommandsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPiWebCommandsEnabled_MatchesSample 把部署模板与本函数接上:模板必须让
// 本函数返回空。否则模板自己就是个 fail-open 的例子 —— 运维照它部署完,
// 告警依旧会响。
func TestPiWebCommandsEnabled_MatchesSample(t *testing.T) {
	data, err := os.ReadFile("../scripts/web-search.json.sample")
	if err != nil {
		t.Fatalf("读取部署模板: %v", err)
	}
	writePiWebSearchConfig(t, string(data))
	if got := PiWebCommandsEnabled(); len(got) != 0 {
		t.Errorf("部署模板应当关掉全部四个命令,但本函数仍报开着: %v", got)
	}
}

// TestWebSearchSample_PinsSecurityKeys 断言部署模板把设计要求的安全键显式钉死。
//
// 计划文档原本把这条列在 Task 6,但模板由 Task 1 产出,测试跟着产物走,否则模板
// 在两个任务之间无人守护。
//
// 各键的 schema 依据 pi-web-access v0.29.0 源码,不是照设计文档推测:
//   - allowBrowserCookies 在根级:gemini-web-config.ts:4 的 CONFIG_PATH 就是
//     getWebSearchConfigPath(),:77 与 :100 读根级 allowBrowserCookies 且判定为
//     === true(故 false 与缺席等效,但模板要显式写出防误配)
//   - ssrf.allowRanges / ssrf.trustEnvProxy:index.ts:178-183 的类型定义,
//     ssrf-protection.ts:112-134 的 loadSsrfConfig 默认值即 {[], false}
//   - tools.sourceCheck.enabled:index.ts:271-274 的 config.tools?.[key]?.enabled
//     覆盖分支;sourceCheck 默认是**开**的,必须显式关
//   - fetchContent.domainPolicy.allow/deny:ssrf-protection.ts:67-85。
//     注意设计文档的表格写的是 fetchContent.deny/.allow,少了 domainPolicy 这一层,
//     以源码为准。空数组等价于 DEFAULT_DOMAIN_POLICY(ssrf-protection.ts:65),
//     且 assertDomainPolicy(:265-273)只在 allow 非空时才做白名单,故 [] 不限制任何域名
//   - commands.{websearch,curator,search,google-account}.enabled:index.ts:172 的
//     类型定义与 :277-279 的 isCommandEnabled(`config.commands?.[name]?.enabled
//     !== false` —— **默认是开的**)。这四个命令必须显式关,理由见
//     TestWebSearchSample_DisablesExtensionCommands 的注释
func TestWebSearchSample_PinsSecurityKeys(t *testing.T) {
	// 模板放在 tracked 的 backend/scripts/,与 path-validator.py 同目录。仓库根的
	// scripts/ 被 .gitignore 的 /scripts/ 整体忽略,只承载部署产物;放那里的文件
	// 不会进版本库,新克隆里根本不存在。
	const samplePath = "../scripts/web-search.json.sample"

	data, err := os.ReadFile(samplePath)
	if err != nil {
		t.Fatalf("读取部署模板 %s: %v", samplePath, err)
	}

	// 全部用指针,以区分「显式写死」与「靠默认值」——模板的意义就在于显式,
	// 键缺席即视为失败
	var sample struct {
		AllowBrowserCookies *bool `json:"allowBrowserCookies"`
		SSRF                *struct {
			AllowRanges   *[]string `json:"allowRanges"`
			TrustEnvProxy *bool     `json:"trustEnvProxy"`
		} `json:"ssrf"`
		Tools *struct {
			SourceCheck *struct {
				Enabled *bool `json:"enabled"`
			} `json:"sourceCheck"`
		} `json:"tools"`
		FetchContent *struct {
			DomainPolicy *struct {
				Allow *[]string `json:"allow"`
				Deny  *[]string `json:"deny"`
			} `json:"domainPolicy"`
		} `json:"fetchContent"`
		Commands *map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(data, &sample); err != nil {
		t.Fatalf("模板必须是合法 JSON 对象(pi 的 parseConfigRoot 要求对象,数组会抛错): %v", err)
	}

	if sample.AllowBrowserCookies == nil {
		t.Error("模板必须显式写出 allowBrowserCookies")
	} else if *sample.AllowBrowserCookies {
		t.Error("allowBrowserCookies 必须为 false:开启后任何用户的文档问答都能外泄运维者本人的浏览器 cookie")
	}

	if sample.SSRF == nil {
		t.Fatal("模板必须显式写出 ssrf 段")
	}
	if sample.SSRF.AllowRanges == nil {
		t.Error("ssrf.allowRanges 必须显式写出(防误配放宽内网网段)")
	} else if len(*sample.SSRF.AllowRanges) != 0 {
		t.Errorf("ssrf.allowRanges 必须为空数组, got %v", *sample.SSRF.AllowRanges)
	}
	if sample.SSRF.TrustEnvProxy == nil {
		t.Error("ssrf.trustEnvProxy 必须显式写出")
	} else if *sample.SSRF.TrustEnvProxy {
		t.Error("ssrf.trustEnvProxy 必须为 false")
	}

	if sample.Tools == nil || sample.Tools.SourceCheck == nil || sample.Tools.SourceCheck.Enabled == nil {
		t.Fatal("模板必须显式写出 tools.sourceCheck.enabled")
	}
	if *sample.Tools.SourceCheck.Enabled {
		t.Error("sourceCheck 必须关闭:本设计不授予该工具,--tools 白名单里也没有它")
	}

	if sample.FetchContent == nil || sample.FetchContent.DomainPolicy == nil {
		t.Fatal("模板必须显式写出 fetchContent.domainPolicy(运维收紧域名时的落点)")
	}
	if sample.FetchContent.DomainPolicy.Allow == nil || sample.FetchContent.DomainPolicy.Deny == nil {
		// 只要求「显式写出」,不要求为空:allow 为空与缺席等价
		// (ssrf-protection.ts:65 的 DEFAULT_DOMAIN_POLICY,:265-273 的
		// assertDomainPolicy 仅在 allow 非空时才做白名单),所以将来运维往模板里
		// 填真实域名收紧策略时,本断言不应变红。
		t.Error("fetchContent.domainPolicy 的 allow 与 deny 都必须显式写出(允许为空数组;为空等价于不限制任何域名)")
	}

	// commands 这四个键钉住的是一条**规格与实现计划都未覆盖**的注入面:
	// rpc 模式下以 `/` 开头的用户消息会被 pi 当成扩展命令派发执行。源码链路
	// (pi 0.85.1):
	//
	//	modes/rpc/rpc-mode.js:301-304  session.prompt(command.message, {...})
	//	                             —— **没有**传 expandPromptTemplates
	//	core/agent-session.js:822      ... ?? true(默认为真)
	//	                  :828-834   if (expandPromptTemplates && text.startsWith("/"))
	//	                                 _tryExecuteExtensionCommand(text)
	//	                  :954-961   getCommand(name) 命中即执行
	//
	// 三道既有防线都拦不住它:`--tools` 只管工具调用(命令是扩展自己的 JS);
	// `--no-skills`/`--no-prompt-templates` 只关 skill 与模板;沙箱 extension 的
	// `input` hook 在 :839-851,位于 :828 的命令派发**之后**。
	//
	// 而 Claude 侧的对应物 SlashCommand 早就在 ClaudeDangerousDisallowedTools 里被
	// 硬阻断 —— 所以关掉它是追平两个后端的安全强度,不是额外收紧。
	// 已实测的可利用面:pi-web-access 自己注册了 4 个命令(index.ts:3164 websearch、
	// :3427 curator、:3469 google-account、:3517 search),其中 `/curator` 会拉起
	// 浏览器,且扩展命令自行驱动 LLM、绕过我们注入的 --system-prompt。
	//
	// 残留风险(已记进计划 R9):其他已加载包(如本机的 pi-subagents)的命令不由
	// 本模板覆盖,只能靠 R1 的运维隔离。故意**不**在 EncodeUserMessage 里改写以
	// `/` 开头的用户文本:那会污染 LLM 输入与 DB 里的历史消息,且与 claude
	// 后端的「/ 就是普通文本」不一致。
	//
	// 这四个命令名取自 index.ts:277 的 isCommandEnabled 形参类型,与上述四处
	// registerCommand 一一对应。用遍历而不是四条并列断言,是为了让「模板里多出
	// 一个未知的 enabled:true 命令」也变红 —— 升级 pi-web-access 后它若新增命令,
	// 默认就是开的。
	wantDisabled := []string{"websearch", "curator", "search", "google-account"}
	if sample.Commands == nil {
		t.Fatalf("模板必须显式写出 commands 段:这四个扩展命令默认是开的(index.ts:278 的 `!== false`)," +
			"而 rpc 模式下以 / 开头的用户消息会被当成扩展命令派发执行(见上方注释的源码链路)")
	}
	for _, name := range wantDisabled {
		cmd, ok := (*sample.Commands)[name]
		if !ok || cmd.Enabled == nil {
			t.Errorf("commands.%s.enabled 必须显式写出", name)
		} else if *cmd.Enabled {
			t.Errorf("commands.%s.enabled 必须为 false", name)
		}
	}
	for name, cmd := range *sample.Commands {
		if !slices.Contains(wantDisabled, name) {
			t.Errorf("commands 里出现本测试未知的命令名 %q:请核实 pi-web-access 当前版本的 registerCommand 清单并更新本用例", name)
		}
		if cmd.Enabled == nil || *cmd.Enabled {
			t.Errorf("commands.%s.enabled 必须显式为 false", name)
		}
	}
}
