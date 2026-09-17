package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"llm-knowledge/config"
	"llm-knowledge/db"
)

// resolverTTL 是 GlobalSettings.LLMBackend 的缓存时长。
//
// 不做缓存的话每次 spawn 都要打一次 DB;缓存太久则管理员在 Settings 里切换后端后
// 迟迟不生效。5s 是规格定的值,配合 Invalidate() 的主动失效,实际语义是:
// 「新建的会话最迟 5s 后用上新后端,而 PUT 保存成功那一刻立即生效」。
//
// 已运行的会话不受影响,直到 SSE 断开 30s 后被既有清理循环回收 —— **不主动踢会话**
// (规格「Settings 与生效时机」)。
//
// 声明为 var 而不是 const,是为了让测试能把它缩短到毫秒级来验证 TTL 行为,
// 否则测一次过期要真等 5 秒。
var resolverTTL = 5 * time.Second

// ResolverOptions 是 Init 的入参。
type ResolverOptions struct {
	ClaudeBin  string // config.ClaudeBin
	PiBin      string // config.PiBin
	ScriptsDir string // main.go 的 LLM_SCRIPTS_DIR;PiProtocol 据此定位沙箱 extension

	// ClaudeSettingsPath 返回 claude 的安全 settings.json 路径,即
	// claude.GetSettingsPath()。**必须是函数而不是字符串值**:
	//
	// agent 不能 import claude(claude 已 import agent,会成环),所以这个值只能由
	// 调用方传进来。若传的是「调用 Init 时求好的字符串」,就等于给 main.go 加了一条
	// 隐式顺序约束 —— agent.Init 必须晚于 claude.InitSecurityConfig。而违反它的
	// 后果是**静默 fail-open**:GetSettingsPath() 返回空串,ClaudeProtocol 就不加
	// --settings,path-validator.py 的 hook 整个不生效,而服务照常启动、没有任何
	// 报错。传函数把这条顺序约束彻底消掉。
	ClaudeSettingsPath func() string
}

var (
	resolverMu    sync.Mutex
	resolverOpts  ResolverOptions
	resolverReady bool

	cachedProto Protocol
	cachedAt    time.Time
)

// backendNameProvider 读出当前配置的后端名。
// 声明为 var 是给测试留的接缝:agent 包的测试不想起一个 sqlite 就能覆盖
// claude / pi / 未知值 / 空值四条分支。
var backendNameProvider = readBackendName

// Init 在服务启动时调用一次(main.go)。
//
// 重复调用是安全的:它整体替换配置并丢弃缓存,语义等价于 Init + Invalidate。
// 测试正是依赖这一点来注入假的二进制路径。
func Init(opts ResolverOptions) {
	resolverMu.Lock()
	defer resolverMu.Unlock()
	resolverOpts = opts
	resolverReady = true
	cachedProto, cachedAt = nil, time.Time{}
}

// Invalidate 让缓存立即失效,下一次 Current() 强制重读 DB。
// 由 PUT /api/admin/settings 保存成功后调用(Task 7)。
func Invalidate() {
	resolverMu.Lock()
	defer resolverMu.Unlock()
	cachedProto, cachedAt = nil, time.Time{}
}

// Current 返回当前生效的 Protocol。
//
// 所有 spawn 点都必须经由本函数取得 Protocol,不得再直接 NewClaudeProtocol ——
// 否则后端开关对那条路径无效。claude/api/ingest 三个包里**不得**出现
// `if proto.Backend() == agent.BackendPi` 这类判断(Plan 1 建立的核心不变式),
// 后端差异只能落在本包的两个 Protocol 实现内。
//
// 未调用 Init 时返回明确错误,而不是静默构造一个 bin 为空的 ClaudeProtocol:
// 后者会一路走到 exec 才失败,错误信息里看不出是接线漏了。
func Current() (Protocol, error) {
	resolverMu.Lock()
	defer resolverMu.Unlock()

	if !resolverReady {
		return nil, errors.New("agent.Init was never called; the LLM backend resolver is not configured " +
			"(main.go must call agent.Init with the claude/pi binary paths before any session is started)")
	}

	// TTL 内直接返回缓存 —— **先判缓存、再读后端配置**。
	// 顺序不可颗倒:本缓存要挡住的正是「每次 spawn 都打一次 DB」,如果把
	// backendNameProvider() 放在前面,DB 依旧每调必读,缓存就只剩「不重建
	// Protocol」这一点收益(那部分由 TestResolver_CachesProtocolInstanceWithinTTL 守)。
	// 代价是 TTL 窗口内看不到 DB 的后端变更 —— 那正是 5s TTL 的语义,
	// 而「保存后立即生效」由 Invalidate() 保证。
	if cachedProto != nil && time.Since(cachedAt) < resolverTTL {
		return cachedProto, nil
	}

	name := normalizeBackendName(backendNameProvider())
	proto := buildProtocolLocked(name)
	cachedProto, cachedAt = proto, time.Now()
	return proto, nil
}

// normalizeBackendName 把 DB 里的取值收敛成 Backend 常量。
//
// **未知值与空值一律回退 claude**(fail-safe 到既有行为):DB 里可能是迁移前的空串、
// 手工写坏的值、或某个未来版本才有的后端名。回退比报错好 —— 报错会让整条聊天链路
// 因为一个配置字段而全挂,而回退到 claude 至少保持改造前的行为。
func normalizeBackendName(raw string) string {
	if raw == string(BackendPi) {
		return string(BackendPi)
	}
	return string(BackendClaude)
}

// buildProtocolLocked 构造对应后端的 Protocol。调用方必须持有 resolverMu。
func buildProtocolLocked(name string) Protocol {
	if name == string(BackendPi) {
		return NewPiProtocol(resolverOpts.PiBin, resolverOpts.ScriptsDir)
	}
	settingsPath := ""
	if resolverOpts.ClaudeSettingsPath != nil {
		settingsPath = resolverOpts.ClaudeSettingsPath()
	}
	return NewClaudeProtocol(resolverOpts.ClaudeBin, settingsPath)
}

// readBackendName 从 GlobalSettings 读后端名。
//
// **只读**:这里刻意用 First 而不是 api.ensureGlobalSettings 的 FirstOrCreate ——
// 解析 Protocol 是一条热路径,不该有「读配置顺手建了一行」这种写副作用,更不该
// 在只读副本或迁移未跑的环境下失败。
//
// 任何异常(DB 未初始化、表还没迁移出行、查询报错)都回退 claude 并留一条日志。
// 日志只在读到**非空但无法识别**的值时才打:空值是迁移前的正常状态,不该刷屏。
func readBackendName() string {
	if db.DB == nil {
		return string(BackendClaude)
	}
	var settings db.GlobalSettings
	if err := db.DB.First(&settings).Error; err != nil {
		// 表为空(单例行还没创建)属正常状态:回退默认后端,不打日志
		return string(BackendClaude)
	}
	name := settings.LLMBackend
	switch name {
	case "", string(BackendClaude), string(BackendPi):
		return name
	default:
		log.Printf("[agent] GlobalSettings.LLMBackend = %q 不是已知后端(claude/pi),已回退 claude。请检查该行的写入来源", name)
		return name
	}
}

// ProbePi 在管理员把后端切到 pi **之前**做一次可用性校验,nil 表示可以切。
//
// 不能走 Current():那时生效的后端还是旧值,探测不到目标后端。本函数用 Init 时
// 登记的 PiBin / ScriptsDir 直接构造一个 PiProtocol 来探测。
//
// 三段校验,由浅入深、由便宜到贵:
//
//  1. 二进制可用:LookPath + `pi --version`(PiProtocol.Probe,5s 超时)
//  2. 部署完整:SessionArgs 会因沙箱 extension 缺失而报错(fail-closed)。
//     把风险登记 R6 从「首次聊天才炸」提前到「切换时就 400」
//  3. 配置不会让 pi 整体拒绝启动:config.ValidatePiWebConfig 的 problem 为空
//
// 第 3 段是风险登记 R8 的处置,也是本函数存在的主要理由:R8 的后果是「切换探测
// 通过、随后每个请求都失败」,而 `pi --version` 在 pi 的 main.js:483-486 提前
// exit(0)、**根本不加载扩展**,所以第 1 段在结构上就探测不到它。
//
// 这里刻意**不**用「spawn 一次真 pi 看它起不起得来」来做第 3 段,尽管那能覆盖更多
// 失败模式:①慢(扩展加载 + npm 包解析,秒级到数十秒),放在 PUT handler 里容易撞
// HTTP 超时;②有副作用(会在 pi 的 agent 目录里留下会话文件,并触发已加载包的
// 加载期代码,即 R1);③实测 pi 在 rpc 模式下 stdin EOF 后并不退出(探针等了 25s
// 仍需外部 kill),要可靠收尾就得自己管进程生命周期。
// 权衡下来:Go 与 pi-web-access 读的是同一个文件(不变式 I1 保证),所以 Go 完全
// 能算出 pi 会不会在 resolveToolNames 上抛错 —— 用零成本、确定性的静态判定换掉
// 一次昂贵且不确定的动态探测。「pi 能否真的带着扩展启动」留给 Task 11 的集成测试,
// 那才是它该被验证的地方(CI 里跑,不占用户的一次 Settings 保存)。
func ProbePi(ctx context.Context) error {
	resolverMu.Lock()
	opts, ready := resolverOpts, resolverReady
	resolverMu.Unlock()
	if !ready {
		return errors.New("agent.Init was never called; cannot probe the pi backend")
	}

	proto := NewPiProtocol(opts.PiBin, opts.ScriptsDir)
	if err := proto.Probe(ctx); err != nil {
		return err
	}
	// 沙箱 extension 缺失 → SessionArgs 报错(R6 的 fail-closed 前置校验)
	if _, err := proto.SessionArgs("", nil); err != nil {
		return err
	}
	if _, problem := config.ValidatePiWebConfig(); problem != "" {
		return fmt.Errorf("pi 会因这份配置整体拒绝启动:%s", problem)
	}
	return nil
}
