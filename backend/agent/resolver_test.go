package agent

import (
	"strings"
	"testing"
	"time"

	"llm-knowledge/db"
)

// resetResolver 把 resolver 的进程级全局状态恢复成「未 Init」。
// 每个用例都必须在 Cleanup 里调用它,否则 agent.Init 留下的状态会串到后面的用例
// (包括本包其他测试),而那种串味在失败信息里完全看不出来。
func resetResolver(t *testing.T) {
	t.Helper()
	resolverMu.Lock()
	resolverReady = false
	resolverOpts = ResolverOptions{}
	cachedProto, cachedAt = nil, time.Time{}
	resolverMu.Unlock()
	t.Cleanup(func() {
		resolverMu.Lock()
		resolverReady = false
		resolverOpts = ResolverOptions{}
		cachedProto, cachedAt = nil, time.Time{}
		backendNameProvider = readBackendName
		resolverTTL = 5 * time.Second
		resolverMu.Unlock()
	})
}

// stubBackend 用给定的取值序列替换 backendNameProvider,返回一个计数闭包,
// 用于断言「缓存有没有真的挡住 DB 读取」。
func stubBackend(t *testing.T, name string) func() int {
	t.Helper()
	calls := 0
	backendNameProvider = func() string {
		calls++
		return name
	}
	return func() int { return calls }
}

func TestResolver_CurrentWithoutInitReturnsExplicitError(t *testing.T) {
	resetResolver(t)

	_, err := Current()
	if err == nil {
		t.Fatal("未调用 Init 时 Current() 必须报错")
	}
	// 错误必须点名 agent.Init,否则接线漏了的时候只会在 exec 阶段以
	// "exec: no command" 之类的信息失败,看不出根因
	if msg := err.Error(); !strings.Contains(msg, "agent.Init") {
		t.Errorf("错误信息必须点名 agent.Init 以便定位接线遗漏, got: %s", msg)
	}
}

func TestResolver_BackendSelection(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		want       Backend
	}{
		{"claude 显式选择", "claude", BackendClaude},
		{"pi 显式选择", "pi", BackendPi},
		{"空值回退 claude(迁移前的正常状态)", "", BackendClaude},
		{"未知值回退 claude(fail-safe 到既有行为)", "gemini", BackendClaude},
		// 大小写敏感:取值校验在 API 层(Task 7)完成,resolver 不做宽松匹配,
		// 否则 DB 里被手工写坏的 "PI" 会静默生效,与 API 层的校验结果不一致
		{"大写 PI 不是合法取值,回退 claude", "PI", BackendClaude},
		{"带空格不是合法取值,回退 claude", " pi ", BackendClaude},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetResolver(t)
			stubBackend(t, tc.configured)
			Init(ResolverOptions{ClaudeBin: "/bin/claude-stub", PiBin: "/bin/pi-stub", ScriptsDir: t.TempDir()})

			proto, err := Current()
			if err != nil {
				t.Fatalf("Current() 意外报错: %v", err)
			}
			if proto.Backend() != tc.want {
				t.Errorf("Backend() = %q, want %q", proto.Backend(), tc.want)
			}
			if wantBin := binFor(tc.want); proto.Bin() != wantBin {
				t.Errorf("Bin() = %q, want %q(后端选错时二进制也会跟着错)", proto.Bin(), wantBin)
			}
		})
	}
}

func binFor(b Backend) string {
	if b == BackendPi {
		return "/bin/pi-stub"
	}
	return "/bin/claude-stub"
}

func TestResolver_TTLCacheAvoidsPerSpawnDBRead(t *testing.T) {
	resetResolver(t)
	calls := stubBackend(t, "claude")
	// 缩短 TTL,否则测一次过期要真等 5 秒
	resolverTTL = 20 * time.Millisecond
	Init(ResolverOptions{ClaudeBin: "/bin/claude-stub"})

	for i := 0; i < 5; i++ {
		if _, err := Current(); err != nil {
			t.Fatalf("Current() 第 %d 次报错: %v", i, err)
		}
	}
	if got := calls(); got != 1 {
		t.Errorf("TTL 内 5 次 Current() 只该读 1 次后端配置,实际 %d 次(每次 spawn 都打一次 DB 是本缓存要避免的)", got)
	}

	time.Sleep(40 * time.Millisecond)
	if _, err := Current(); err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if got := calls(); got != 2 {
		t.Errorf("TTL 过期后必须重读,期望累计 2 次,实际 %d 次", got)
	}
}

func TestResolver_InvalidateForcesImmediateReread(t *testing.T) {
	resetResolver(t)
	configured := "claude"
	calls := 0
	backendNameProvider = func() string { calls++; return configured }
	resolverTTL = time.Hour // 拉长 TTL,证明是 Invalidate 而不是过期起了作用
	Init(ResolverOptions{ClaudeBin: "/bin/claude-stub", PiBin: "/bin/pi-stub", ScriptsDir: t.TempDir()})

	proto, err := Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if proto.Backend() != BackendClaude {
		t.Fatalf("初始后端 = %q, want claude", proto.Backend())
	}

	// 模拟 PUT /api/admin/settings 保存成功:DB 已改,随后调 Invalidate
	configured = "pi"
	Invalidate()

	proto, err = Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if proto.Backend() != BackendPi {
		t.Errorf("Invalidate 后必须立即读到新后端, got %q want pi(TTL 是 1 小时,所以不可能是过期生效)", proto.Backend())
	}
	if calls != 2 {
		t.Errorf("后端配置应被读取 2 次(初次 + Invalidate 后),实际 %d 次", calls)
	}
}

func TestResolver_CachesProtocolInstanceWithinTTL(t *testing.T) {
	resetResolver(t)
	stubBackend(t, "pi")
	resolverTTL = time.Hour
	scripts := t.TempDir()
	Init(ResolverOptions{PiBin: "/bin/pi-stub", ScriptsDir: scripts})

	first, err := Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	second, err := Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	// 必须是同一个实例:NewPiProtocol 会读磁盘解析 web 工具名(计划 M-7),
	// 每次 Current() 都重建就等于把那份 I/O 与可能的告警搬回每次请求
	if first != second {
		t.Error("TTL 内两次 Current() 必须返回同一个 Protocol 实例,否则 M-7「解析一次并持有」失效")
	}
	pi, ok := first.(*PiProtocol)
	if !ok {
		t.Fatalf("期望 *PiProtocol, got %T", first)
	}
	if pi.sandboxExtPath == "" {
		t.Error("ScriptsDir 必须传进 PiProtocol,否则沙箱 extension 定位不到")
	}
}

// TestResolver_ClaudeSettingsPathIsLateBound 钉住 ResolverOptions.ClaudeSettingsPath
// 必须是**函数**而不是字符串值。
//
// 这条不是风格问题:claude 的 settings.json 路径由 claude.InitSecurityConfig 在
// 启动时算出,而 agent 不能 import claude(会成环),所以只能由 main.go 传进来。
// 若 Init 收的是「调用时求好的字符串」,就等于给 main.go 加了一条隐式顺序约束
// (agent.Init 必须晚于 InitSecurityConfig),而违反它的后果是**静默 fail-open**:
// 路径为空 → ClaudeProtocol 不加 --settings → path-validator.py 的 hook 整个
// 不生效 → 服务照常启动、没有任何报错。
func TestResolver_ClaudeSettingsPathIsLateBound(t *testing.T) {
	resetResolver(t)
	stubBackend(t, "claude")
	resolverTTL = time.Hour

	path := "" // 模拟「Init 时安全配置还没生成」
	Init(ResolverOptions{
		ClaudeBin:          "/bin/claude-stub",
		ClaudeSettingsPath: func() string { return path },
	})

	proto, err := Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if cp, ok := proto.(*ClaudeProtocol); !ok {
		t.Fatalf("期望 *ClaudeProtocol, got %T", proto)
	} else if cp.settingsPath != "" {
		t.Errorf("settingsPath = %q, want 空(此时安全配置尚未生成)", cp.settingsPath)
	}

	// 安全配置随后生成 —— 无需重新 Init,下一次解析就该看到它
	path = "/run/llm/settings.json"
	Invalidate()

	proto, err = Current()
	if err != nil {
		t.Fatalf("Current() 报错: %v", err)
	}
	if cp := proto.(*ClaudeProtocol); cp.settingsPath != "/run/llm/settings.json" {
		t.Errorf("settingsPath = %q, want /run/llm/settings.json —— 说明路径被在 Init 时求值固化了,那会让启动顺序错误变成静默 fail-open", cp.settingsPath)
	}
}

// TestResolver_ReadBackendNameIsReadOnlyAndFailSafe 断言两件事:
//  1. db.DB 未初始化时回退 claude,不 panic(测试环境与迁移未跑的生产环境都会遇到)
//  2. 读路径是**只读**的:刻意不用 api.ensureGlobalSettings 的 FirstOrCreate,
//     因为解析 Protocol 是热路径,不该有「读配置顺手建了一行」这种写副作用
func TestResolver_ReadBackendNameIsReadOnlyAndFailSafe(t *testing.T) {
	if db.DB != nil {
		t.Skip("db.DB 已被其他测试初始化,无法验证 nil 分支")
	}
	if got := readBackendName(); got != string(BackendClaude) {
		t.Errorf("db.DB 为 nil 时 readBackendName() = %q, want %q", got, string(BackendClaude))
	}
	if got := normalizeBackendName(readBackendName()); got != string(BackendClaude) {
		t.Errorf("归一化后 = %q, want %q", got, string(BackendClaude))
	}
}
