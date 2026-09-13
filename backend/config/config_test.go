package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		if got := PiWebSearchConfigPath(); got != want {
			t.Errorf("PiWebSearchConfigPath() = %q, want %q", got, want)
		}
	})
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
	// pi 侧对这些值是抛错的(index.ts:288-313);Go 侧必须逐键回退默认名,
	// 不能把 pi 永远不会注册的名字写进 --tools 白名单
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
			name:    "toolNames 不是对象",
			content: `{"toolNames": ["web_search"]}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
		{
			name:    "source_check 的改名不被采纳(本设计不授予该工具)",
			content: `{"toolNames": {"sourceCheck": "renamed_source"}}`,
			want:    PiWebToolNames{"web_search", "fetch_content", "get_search_content"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writePiWebSearchConfig(t, tc.content)
			if got := LoadPiWebToolNames(); got != tc.want {
				t.Errorf("LoadPiWebToolNames() = %+v, want %+v", got, tc.want)
			}
		})
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
		t.Error("fetchContent.domainPolicy 的 allow 与 deny 都必须显式写出为空数组")
	}
}
