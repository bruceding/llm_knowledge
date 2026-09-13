package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llm-knowledge/agent"
	"llm-knowledge/db"

	"github.com/labstack/echo/v4"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAdminSettingsTestDB(t *testing.T) {
	t.Helper()
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect test database: %v", err)
	}
	db.DB = testDB
	if err := testDB.AutoMigrate(&db.GlobalSettings{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
}

// writeFakePi 造一个假的 pi 可执行文件。marker 非空时,脚本会把 "called" 追加进去,
// 于是「探测到底有没有被触发」变成一个可观察的事实,而不是靠推断。
func writeFakePi(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pi")
	script := "#!/bin/sh\n"
	if marker != "" {
		script += "echo called >> " + marker + "\n"
	}
	script += "echo 0.99.9\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	return path
}

// setupPiProbeEnv 准备一个「pi 可用且部署完整」的环境,返回假 pi 的路径。
// ProbePi 有三段校验,前两段需要:假 pi 可执行、沙箱 extension 存在。
// 第三段(web-search.json)默认给一份合规配置,免得探测被 R8 那段拦下。
func setupPiProbeEnv(t *testing.T, marker string) string {
	t.Helper()
	piBin := writeFakePi(t, marker)

	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "pi-path-validator.ts"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write sandbox stub: %v", err)
	}

	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	good := `{"commands":{"websearch":{"enabled":false},"curator":{"enabled":false},"search":{"enabled":false},"google-account":{"enabled":false}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "web-search.json"), []byte(good), 0o644); err != nil {
		t.Fatalf("write web-search.json: %v", err)
	}

	agent.Init(agent.ResolverOptions{
		ClaudeBin:  "/nonexistent/claude",
		PiBin:      piBin,
		ScriptsDir: scripts,
	})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })
	return piBin
}

func putGlobalSettings(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h := &AdminSettingsHandler{}
	if err := h.UpdateGlobalSettings(c); err != nil {
		t.Fatalf("UpdateGlobalSettings returned error: %v", err)
	}
	return rec
}

func getGlobalSettings(t *testing.T) map[string]any {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h := &AdminSettingsHandler{}
	if err := h.GetGlobalSettings(c); err != nil {
		t.Fatalf("GetGlobalSettings returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET body is not valid JSON: %v (%s)", err, rec.Body.String())
	}
	return out
}

// TestGetGlobalSettings_IncludesLLMBackend 断言 GET 响应含 llmBackend,且它永远是
// claude/pi 之一 —— 不能把空串漏给前端,否则 Settings 里的下拉框会显示为空白。
//
// 空串是真实可能出现的:GORM 对带 default 标签的零值字段会从 INSERT 里省略、
// 交给 DB 填默认值,于是 FirstOrCreate 刚建出来的那个内存对象里它仍是 ""。
func TestGetGlobalSettings_IncludesLLMBackend(t *testing.T) {
	setupAdminSettingsTestDB(t)

	got := getGlobalSettings(t)
	backend, ok := got["llmBackend"].(string)
	if !ok {
		t.Fatalf("响应缺少 llmBackend 字段或类型不对: %v", got["llmBackend"])
	}
	if backend != "claude" {
		t.Errorf("llmBackend = %q, want \"claude\"(新建行的默认值;空串会让前端下拉框显示为空白)", backend)
	}
}

// TestUpdateGlobalSettings_RejectsUnknownBackend 断言非法取值被 400 拦下,
// 而不是静默忽略 —— 静默忽略会让调用方以为切换成功了。
func TestUpdateGlobalSettings_RejectsUnknownBackend(t *testing.T) {
	setupAdminSettingsTestDB(t)
	setupPiProbeEnv(t, "")

	for _, bad := range []string{"gemini", "PI", "Pi", " pi ", "claude-pi", "''"} {
		rec := putGlobalSettings(t, `{"llmBackend":"`+bad+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("llmBackend=%q → status %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
			continue
		}
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("400 响应体不是合法 JSON: %v", err)
		}
		if !strings.Contains(out["error"], "llmBackend") {
			t.Errorf("400 消息必须点名 llmBackend 以便前端定位, got %q", out["error"])
		}
	}

	// 非法取值不得写进 DB
	got := getGlobalSettings(t)
	if got["llmBackend"] != "claude" {
		t.Errorf("非法取值被写进了 DB: llmBackend = %v, want claude", got["llmBackend"])
	}
}

// TestUpdateGlobalSettings_403And400AreDistinguishable 是规格「管理员权限模型」
// 小节的硬要求:403(没权限)与 400(有权限但取值非法)的消息必须可区分。
// 中间件的 403 文案是 "admin access required"。
func TestUpdateGlobalSettings_403And400AreDistinguishable(t *testing.T) {
	setupAdminSettingsTestDB(t)
	setupPiProbeEnv(t, "")

	rec := putGlobalSettings(t, `{"llmBackend":"gemini"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if strings.Contains(out["error"], "admin access required") {
		t.Errorf("400 的消息不得与中间件的 403 文案相同,否则前端无法区分「没权限」与「取值非法」: %q", out["error"])
	}
	if out["error"] == "" {
		t.Error("400 必须带消息")
	}
}

// TestUpdateGlobalSettings_ClaudeDoesNotProbe 断言切到 claude 时**不触发探测**。
//
// 这不是省时间的问题:claude 是默认后端与回退值,给它加探测会让「pi 已经坏了、
// 切回 claude 自救」这条路也被堵住 —— 那正是运维最需要的逃生门。
func TestUpdateGlobalSettings_ClaudeDoesNotProbe(t *testing.T) {
	setupAdminSettingsTestDB(t)
	marker := filepath.Join(t.TempDir(), "probe-marker")
	piBin := setupPiProbeEnv(t, marker)
	_ = piBin

	// 先切到 pi,再切回 claude,确保走的是「切换」而不是「值没变所以跳过」
	if rec := putGlobalSettings(t, `{"llmBackend":"pi"}`); rec.Code != http.StatusOK {
		t.Fatalf("切到 pi 失败: %d %s", rec.Code, rec.Body.String())
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("清理 marker: %v", err)
	}

	rec := putGlobalSettings(t, `{"llmBackend":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("切回 claude → status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("切到 claude 不应触发 pi 探测(那会堵住「pi 坏了切回 claude 自救」这条路),但假 pi 被调用了")
	}
	if got := getGlobalSettings(t); got["llmBackend"] != "claude" {
		t.Errorf("llmBackend = %v, want claude", got["llmBackend"])
	}
}

// TestUpdateGlobalSettings_PiUnavailableIs400 断言 pi 不可用时被 400 拦住,
// 且消息里带安装指引(规格明确要求)。
func TestUpdateGlobalSettings_PiUnavailableIs400(t *testing.T) {
	setupAdminSettingsTestDB(t)
	scripts := t.TempDir()
	os.WriteFile(filepath.Join(scripts, "pi-path-validator.ts"), []byte("// stub\n"), 0o644)
	agent.Init(agent.ResolverOptions{
		ClaudeBin:  "/nonexistent/claude",
		PiBin:      "/nonexistent/pi",
		ScriptsDir: scripts,
	})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })

	rec := putGlobalSettings(t, `{"llmBackend":"pi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pi 不可用 → status %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.Contains(out["error"], "npm install -g @earendil-works/pi-coding-agent") {
		t.Errorf("400 消息必须含安装指引, got %q", out["error"])
	}
	// 不得写进 DB:否则全局后端会指向一个起不来的 pi,所有聊天请求 500
	if got := getGlobalSettings(t); got["llmBackend"] != "claude" {
		t.Errorf("探测失败却写进了 DB: llmBackend = %v, want claude", got["llmBackend"])
	}
}

// TestUpdateGlobalSettings_PiAvailableIs200 是 happy path,也是上面几个用例的对照组:
// 探测通过时确实能切成功。没有它,「400 拦住了」就分不清是校验生效还是探测恒失败。
func TestUpdateGlobalSettings_PiAvailableIs200(t *testing.T) {
	setupAdminSettingsTestDB(t)
	marker := filepath.Join(t.TempDir(), "probe-marker")
	setupPiProbeEnv(t, marker)

	rec := putGlobalSettings(t, `{"llmBackend":"pi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pi 可用时切换应成功, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("切到 pi 必须真的探测过(否则 400 那几条用例的通过就证明不了探测生效)")
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["llmBackend"] != "pi" {
		t.Errorf("响应里的 llmBackend = %v, want pi", out["llmBackend"])
	}
	// 必须落库,否则重启后开关就丢了
	var row db.GlobalSettings
	db.DB.First(&row)
	if row.LLMBackend != "pi" {
		t.Errorf("DB 里 LLMBackend = %q, want pi", row.LLMBackend)
	}
	// 落库的值必须能被 resolver 读到(这是 agent.Current() 的唯一输入);
	// 走真实 API 而不是开个测试专用口子,才能证明写与读是同一个字段
	agent.Invalidate()
	proto, err := agent.Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if proto.Backend() != agent.BackendPi {
		t.Errorf("resolver 读到的后端 = %q, want pi —— 说明写入与读取不是同一个字段", proto.Backend())
	}
}

// TestUpdateGlobalSettings_R8ConfigIsRejected 断言风险登记 R8 在切换时就被拦住:
// web-search.json 的 toolNames 非法会让 pi-web-access 扩展加载失败、pi 以退出码 1
// 退出(整个后端不可用),而 `pi --version` 探测不到它。
//
// 这正是 agent.ProbePi 第三段校验存在的理由。
func TestUpdateGlobalSettings_R8ConfigIsRejected(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"toolNames 值不是字符串", `{"toolNames":{"webSearch":42}}`},
		{"toolNames 为 null", `{"toolNames":null}`},
		{"跨键重名(pi 只对已启用的键查重)", `{"toolNames":{"webSearch":"x","fetchContent":"x"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupAdminSettingsTestDB(t)
			setupPiProbeEnv(t, "")
			// 用坏配置覆盖掉 setupPiProbeEnv 写的那份合规配置
			path := filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "web-search.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write bad config: %v", err)
			}

			rec := putGlobalSettings(t, `{"llmBackend":"pi"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s → status %d, want 400(body: %s)。这份配置会让 pi exit 1、整个后端不可用",
					tc.name, rec.Code, rec.Body.String())
			}
			var out map[string]string
			json.Unmarshal(rec.Body.Bytes(), &out)
			if !strings.Contains(out["error"], "web-search.json") {
				t.Errorf("400 消息必须指出是哪个配置文件, got %q", out["error"])
			}
			if got := getGlobalSettings(t); got["llmBackend"] != "claude" {
				t.Errorf("探测失败却写进了 DB: %v", got["llmBackend"])
			}
		})
	}
}

// TestUpdateGlobalSettings_R8DuplicateRespectsEnabledFlag 钉住重名检测必须按
// tools.*.enabled 过滤 —— pi 的 resolveToolNames(index.ts:303-311)只对**已启用**的
// 键查重名。不过滤就会对 pi 其实接受的重名误报,把一次合法的后端切换拦成 400。
func TestUpdateGlobalSettings_R8DuplicateRespectsEnabledFlag(t *testing.T) {
	setupAdminSettingsTestDB(t)
	setupPiProbeEnv(t, "")
	path := filepath.Join(os.Getenv("PI_CODING_AGENT_DIR"), "web-search.json")

	// sourceCheck 被显式关掉,于是它与 webSearch 重名对 pi 是合法的
	off := `{"toolNames":{"webSearch":"x","sourceCheck":"x"},"tools":{"sourceCheck":{"enabled":false}}}`
	if err := os.WriteFile(path, []byte(off), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if rec := putGlobalSettings(t, `{"llmBackend":"pi"}`); rec.Code != http.StatusOK {
		t.Errorf("已禁用的键参与重名对 pi 是合法的,不该拦成 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 对照组:同一个重名,但 sourceCheck 是启用的(默认) → pi 会抛错 → 必须 400
	on := `{"toolNames":{"webSearch":"x","sourceCheck":"x"}}`
	if err := os.WriteFile(path, []byte(on), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if rec := putGlobalSettings(t, `{"llmBackend":"pi"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("启用的键重名会让 pi exit 1,必须 400, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestUpdateGlobalSettings_MissingSandboxIs400 断言沙箱 extension 缺失时切换被拦住
// (风险登记 R6)。否则管理员切过去之后,每次聊天才在 spawn 阶段失败。
func TestUpdateGlobalSettings_MissingSandboxIs400(t *testing.T) {
	setupAdminSettingsTestDB(t)
	piBin := writeFakePi(t, "")
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	agent.Init(agent.ResolverOptions{
		ClaudeBin:  "/nonexistent/claude",
		PiBin:      piBin,
		ScriptsDir: t.TempDir(), // 目录存在但没有 pi-path-validator.ts
	})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })

	rec := putGlobalSettings(t, `{"llmBackend":"pi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("沙箱缺失 → status %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var out map[string]string
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.Contains(out["error"], "pi-path-validator.ts") {
		t.Errorf("400 消息必须指出缺的是哪个文件, got %q", out["error"])
	}
}

// TestUpdateGlobalSettings_EmptyBackendKeepsCurrent 断言空串表示「不改」,
// 沿用本 handler 既有的部分更新风格 —— 否则前端只改翻译设置时会把后端重置掉。
func TestUpdateGlobalSettings_EmptyBackendKeepsCurrent(t *testing.T) {
	setupAdminSettingsTestDB(t)
	setupPiProbeEnv(t, "")

	if rec := putGlobalSettings(t, `{"llmBackend":"pi"}`); rec.Code != http.StatusOK {
		t.Fatalf("切到 pi 失败: %d %s", rec.Code, rec.Body.String())
	}
	// 只改翻译模型,不带 llmBackend
	rec := putGlobalSettings(t, `{"translationModel":"some-model"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["llmBackend"] != "pi" {
		t.Errorf("不带 llmBackend 的请求把它改成了 %v, want pi(空串必须表示不改)", out["llmBackend"])
	}
	if out["translationModel"] != "some-model" {
		t.Errorf("translationModel = %v, want some-model", out["translationModel"])
	}
}

// TestUpdateGlobalSettings_InvalidateCalledOnBackendChange 断言保存成功后 resolver
// 缓存被主动失效,否则新后端要等 TTL(5s)过期才生效。
func TestUpdateGlobalSettings_InvalidateCalledOnBackendChange(t *testing.T) {
	setupAdminSettingsTestDB(t)
	setupPiProbeEnv(t, "")

	// 先让 resolver 缓存住 claude
	agent.Invalidate()
	if proto, err := agent.Current(); err != nil || proto.Backend() != agent.BackendClaude {
		t.Fatalf("预热缓存失败: proto=%v err=%v", proto, err)
	}

	if rec := putGlobalSettings(t, `{"llmBackend":"pi"}`); rec.Code != http.StatusOK {
		t.Fatalf("切到 pi 失败: %d %s", rec.Code, rec.Body.String())
	}

	// 若 PUT 没调 Invalidate,这里拿到的仍是缓存里的 claude
	proto, err := agent.Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if proto.Backend() != agent.BackendPi {
		t.Errorf("保存成功后 Current() 仍返回 %q, want pi —— 说明没调 agent.Invalidate(),新后端要等 TTL 过期才生效", proto.Backend())
	}
}
