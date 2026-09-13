package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 本文件把 Plan 1 建立、Plan 2 最容易破坏的核心不变式变成可执行的断言:
//
//	**后端差异只能落在 agent 包的两个 Protocol 实现内。**
//
// claude / api / ingest 三个包里既不得出现「后端是哪个」的判断,也不得出现硬编码的
// claude 二进制名 —— 否则管理员在 Settings 里把后端切到 pi,那条路径依旧 spawn claude,
// 而且**不会报错**,只是行为悄悄不对。
//
// 扫描源码而不是靠约定,是因为这类泄漏在编译期与运行期都不报错:
// `exec.Command("claude", ...)` 是完全合法的 Go,类型系统看不出它绕过了 Protocol。

// execClaudeLiteral 匹配硬编码的 claude 二进制名。
var execClaudeLiteral = regexp.MustCompile(`exec\.Command(?:Context)?\([^)]*"claude"`)

// newClaudeProtocolCall 匹配绕过 resolver 的直接构造。
var newClaudeProtocolCall = regexp.MustCompile(`\bagent\.NewClaudeProtocol\(`)

// execClaudeAllowlist 是允许保留 `exec.Command("claude"` 的文件(相对 backend/)。
//
// 每一条都必须在下方注明豁免依据;**「Task N 待办」类的条目必须在该任务里删除**,
// 否则本测试会因为「豁免项已不再命中」而变红 —— 这是故意的,防止豁免清单只增不减。
var execClaudeAllowlist = map[string]string{
	// 规格明确豁免:不把 pi 加进 dependencies checker,也不改前端对
	// /api/dependencies/status 的消费(该接口至今无人调用,属既有孤儿功能)。
	"dependencies/checker.go": "规格豁免:dependencies checker 保持 claude 专有,本次不扩大范围",

	// TODO(Task 8):PDF 逐页转 Markdown 这一处要改走 agent.Current() + OnceArgs +
	// proto.Bin() + prompt 入 stdin(计划 D2/D3),并删掉硬编码的 --model sonnet。
	// Task 8 完成后必须删除本条,否则本测试会以「豁免项已不再命中」失败。
	"api/documents.go": "Task 8 待办:改走 agent.Current()",
}

// newClaudeProtocolAllowlist 是允许直接构造 ClaudeProtocol 的文件。
var newClaudeProtocolAllowlist = map[string]string{
	// BuildSecureArgs / BuildSecureEnv 两个兼容垫片,供尚未改造的调用点使用。
	// Task 8 把 api/documents.go 迁到 agent.Current() 之后,这两个垫片将只剩测试
	// 调用点,届时应一并删除并移除本条豁免。
	"claude/security.go": "兼容垫片 BuildSecureArgs/BuildSecureEnv;Task 8 之后应删除",
}

// backendPiExemptDir 是唯一允许出现 BackendPi 的目录。
const backendPiExemptDir = "agent"

func TestInvariant_NoBackendKnowledgeOutsideAgentPackage(t *testing.T) {
	root := ".." // backend/

	hitsExec := map[string][]string{}
	hitsNewProto := map[string][]string{}
	hitsBackendPi := map[string][]string{}
	scanned := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// 根节点不能跳:WalkDir 首次回调就是 root 本身,而 root 是 "..",
			// 其 d.Name() 也是 ".." —— 不排除根就会把它当隐藏目录整个跳掉,
			// 扫到 0 个文件。下面的 scanned 下限断言就是为了把这类
			// 「扫描范围错了但测试依旧绿」的情况变成硬失败。
			if path != root && (name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil // 只扫生产代码:测试里出现这些字面量是正当的
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++

		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // 注释里讨论这些字面量是正当的(本文件自己就有)
			}
			loc := rel + ":" + strconv.Itoa(i+1)

			if execClaudeLiteral.MatchString(line) {
				hitsExec[rel] = append(hitsExec[rel], loc+"  "+trimmed)
			}
			if newClaudeProtocolCall.MatchString(line) {
				hitsNewProto[rel] = append(hitsNewProto[rel], loc+"  "+trimmed)
			}
			// 规则 3:agent 包外不得引用后端种类常量、也不得比较 Backend()。
			// 匹配的是**限定名** agent.BackendPi 而不是裸的 BackendPi:后者会把
			// api 层自己定义的常量 llmBackendPi 误伤(实测踩过)—— 那是 API 契约里
			// 的取值字面量,不是「后端是哪个」的判断。包内引用是不限定的,
			// 所以本规则只对 agent/ 以外的文件生效。
			outsideAgent := filepath.Dir(rel) != backendPiExemptDir
			if outsideAgent && (strings.Contains(line, "agent.BackendPi") ||
				strings.Contains(line, "agent.BackendClaude") ||
				strings.Contains(line, ".Backend() ==") ||
				strings.Contains(line, ".Backend() !=")) {
				hitsBackendPi[rel] = append(hitsBackendPi[rel], loc+"  "+trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 backend/ 失败: %v", err)
	}
	if scanned < 20 {
		t.Fatalf("只扫到 %d 个 .go 文件,扫描范围显然不对(root=%s),本测试会因此变成假绿", scanned, root)
	}

	// 规则 1:硬编码 claude 二进制名
	for rel, lines := range hitsExec {
		if reason, ok := execClaudeAllowlist[rel]; ok {
			t.Logf("豁免 %s:%s", rel, reason)
			continue
		}
		t.Errorf("不变式违背:%s 直接 spawn 硬编码的 \"claude\",绕过了 Protocol —— "+
			"后端切到 pi 时这条路径依旧跑 claude 且不会报错。应改用 agent.Current() 取得 Protocol,"+
			"并用 proto.Bin() 作为二进制名。命中:\n  %s",
			rel, strings.Join(lines, "\n  "))
	}

	// 规则 2:绕过 resolver 直接构造 ClaudeProtocol
	for rel, lines := range hitsNewProto {
		if reason, ok := newClaudeProtocolAllowlist[rel]; ok {
			t.Logf("豁免 %s:%s", rel, reason)
			continue
		}
		t.Errorf("不变式违背:%s 直接构造 ClaudeProtocol,绕过了 agent.Current() —— "+
			"后端开关对这条路径无效。命中:\n  %s", rel, strings.Join(lines, "\n  "))
	}

	// 规则 3:agent 包外不得判断后端种类
	for rel, lines := range hitsBackendPi {
		t.Errorf("不变式违背:%s 引用了 agent.Backend* 或比较了 Backend()。后端差异只能落在 agent 包的两个 "+
			"Protocol 实现内;`if proto.Backend() == agent.BackendPi` 这类分支一旦出现在 "+
			"claude/api/ingest,每加一个后端就要改遍所有调用点。命中:\n  %s",
			rel, strings.Join(lines, "\n  "))
	}

	// 豁免清单不得留死条目:否则清单只增不减,Task 8 做完也没人记得回来删
	assertAllowlistStillNeeded(t, "execClaudeAllowlist", execClaudeAllowlist, hitsExec)
	assertAllowlistStillNeeded(t, "newClaudeProtocolAllowlist", newClaudeProtocolAllowlist, hitsNewProto)
}

// assertAllowlistStillNeeded 断言豁免清单里的每一项都仍然命中。
// 已不再命中的条目必须删除 —— 留着就等于给未来的泄漏开了一张空白通行证。
func assertAllowlistStillNeeded(t *testing.T, listName string, allowlist map[string]string, hits map[string][]string) {
	t.Helper()
	var stale []string
	for rel := range allowlist {
		if len(hits[rel]) == 0 {
			stale = append(stale, rel)
		}
	}
	if len(stale) == 0 {
		return
	}
	sort.Strings(stale)
	t.Errorf("%s 里下列条目已不再命中,必须删除(留着等于给未来的泄漏开空白通行证):%s",
		listName, strings.Join(stale, ", "))
}

// TestInvariant_AllSpawnSitesGoThroughProtocolBin 是规则 1 的正向对照:
// 生产代码里的每一处子进程 spawn 都必须用 proto.Bin()(或 buildCmdWithEnv 的
// Protocol 形参),而不是一个字符串字段。
//
// 没有这条正向断言,规则 1 只能证明「没有硬编码 "claude" 字面量」,证明不了
// 「用的是 Protocol 给的二进制」—— 比如把 c.BinPath 直接传给 exec.CommandContext
// 就绕过了两条规则却照样是泄漏(Task 5 之前的 client.go 正是这个形状)。
func TestInvariant_AllSpawnSitesGoThroughProtocolBin(t *testing.T) {
	// 每个文件必须命中的正向证据。注意 query_pool.go 里**不会**出现 proto.Bin()
	// 字面量 —— 它把 Protocol 交给 session.go 的 buildCmdWithEnv,由后者取 Bin()。
	// 所以它要断言的是「传的是 proto」而不是「调了 Bin()」。
	sites := []struct {
		file string
		want string
	}{
		{"claude/session.go", "exec.CommandContext(ctx, proto.Bin(), args...)"},
		{"claude/client.go", "exec.CommandContext(ctx, proto.Bin(), args...)"},
		{"claude/query_pool.go", "buildCmdWithEnv(ctx, proto, args, userDir, env)"},
	}
	for _, s := range sites {
		data, err := os.ReadFile(filepath.Join("..", s.file))
		if err != nil {
			t.Fatalf("读取 %s: %v", s.file, err)
		}
		body := string(data)
		if !strings.Contains(body, s.want) {
			t.Errorf("%s 里找不到 %s:该文件的 spawn 点必须经 Protocol 取二进制名,"+
				"否则后端开关对它无效", s.file, s.want)
		}
		// BinPath 字段仍保留(测试与 ingest 用它构造 Client),但**不得**再被
		// 直接递给 exec.CommandContext
		for i, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "exec.CommandContext") && strings.Contains(line, "BinPath") {
				t.Errorf("%s:%d 仍把 BinPath 直接递给 exec.CommandContext,绕过了 proto.Bin(): %s",
					s.file, i+1, strings.TrimSpace(line))
			}
		}
	}
}
