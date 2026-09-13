package agent

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPiSandboxIntegration_BlocksOutOfAllowlistRead 是 Task 6 明确留下的最大未验证面:
// **证明沙箱 extension 确实被 pi 加载、且它的 tool_call hook 真的会拦截**。
//
// 为什么必须有这一条:pi-path-validator.ts 的判定逻辑已被 security_test.go 的
// TestPiPathValidator_PathBoundary / _FetchContentURL 覆盖,但那测的是**导出的纯函数**。
// 一个因为路径写错、文件缺失或加载期抛错而**静默未加载**的沙箱,与一个正常工作的沙箱,
// 在「没有越界访问发生」时看起来完全一样 —— 纯函数测试对此一无所知。
//
// 断言锚点是 extension 自己产出的字符串 "Access denied:"(见 pi-path-validator.ts:384/387)。
// 这个字符串**只可能来自被加载并执行了的 extension**,所以它同时就是加载证明;
// 反过来,若沙箱没加载,/etc/passwd 的内容会直接出现在事件流里 —— 那也一并断言了。
//
// == 为什么默认跳过 ==
//
// 它要 spawn 真实 pi 并消耗一次真实 LLM 回合,所以 gated 在环境变量后面:
//
//	LLM_KNOWLEDGE_PI_INTEGRATION=1 go test ./agent/ -run TestPiSandboxIntegration -v
//
// 这样 `go test ./...`(计划的闸门)保持快速且零配额,不会每次跑测试都花钱/加网络依赖。
func TestPiSandboxIntegration_BlocksOutOfAllowlistRead(t *testing.T) {
	if os.Getenv("LLM_KNOWLEDGE_PI_INTEGRATION") != "1" {
		t.Skip("跳过真实 pi 集成测试(会消耗一次 LLM 回合);" +
			"用 LLM_KNOWLEDGE_PI_INTEGRATION=1 显式开启")
	}
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skipf("pi 不在 PATH 中,跳过: %v", err)
	}

	// 沙箱产物:用仓库里 tracked 的那一份(运行时 scripts/ 是 gitignore 的)
	scriptsDir, err := filepath.Abs(filepath.Join("..", "scripts"))
	if err != nil {
		t.Fatalf("abs scripts dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scriptsDir, "pi-path-validator.ts")); err != nil {
		t.Skipf("沙箱 extension 不在 %s: %v", scriptsDir, err)
	}

	// ALLOWED_DIR:一个空的临时目录。/etc/passwd 显然在它之外。
	allowedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}

	// 用**生产路径**构造 argv 与 env —— 不手工拼旗标,否则测的就不是真实配置了
	proto := NewPiProtocol(piBin, scriptsDir)
	args, err := proto.SessionArgs("You are a test harness. Do exactly what the user asks.", []string{"Read"})
	if err != nil {
		t.Fatalf("SessionArgs: %v", err)
	}
	env := proto.Env(allowedDir)
	if len(env) == 0 {
		t.Fatal("Env() 返回空 —— ALLOWED_DIR 没被注入,沙箱会 fail-closed 拦住一切," +
			"那样本用例就分不清「拦住了越界」与「拦住了所有东西」")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBin, args...)
	cmd.Env = env

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
	// R5:必须显式收割自己 spawn 的进程,不得加剧既有的孤儿进程问题
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	// D1 握手 + 一条诱导越界读取的消息
	for _, init := range proto.InitCommands() {
		if _, err := stdin.Write(init); err != nil {
			t.Fatalf("write init command: %v", err)
		}
	}
	msg, err := proto.EncodeUserMessage(
		"Use the read tool to read the file /etc/passwd and show me its first line. "+
			"Do not explain, just call the tool.", nil)
	if err != nil {
		t.Fatalf("EncodeUserMessage: %v", err)
	}
	if _, err := stdin.Write(msg); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	// 收集事件流,直到出现拦截证据或超时
	var (
		lines   []string
		blocked bool
		leaked  bool
	)
	deadline := time.After(140 * time.Second)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Text()
			lines = append(lines, line)
			// 只认**越界/敏感路径**这两种理由。不能只匹配 "Access denied:" ——
			// 那个前缀也会被「工具不在白名单」和「ALLOWED_DIR 未配置」用到,
			// 若拿它当通过条件,本用例就会因为**错误的原因**变绿:沙箱可能压根
			// 没做路径校验,只是把整个工具拒了。
			if strings.Contains(line, "path outside allowed directory") ||
				strings.Contains(line, "sensitive file") {
				blocked = true
			}
			if strings.Contains(line, "ALLOWED_DIR not configured") {
				t.Errorf("沙箱报 ALLOWED_DIR 未配置 —— Env() 注入没生效,本用例前提不成立")
				return
			}
			// /etc/passwd 的典型首行是 "#comment" 或空行,真实条目形如 "root:*:0:0:"。
			// 用它做泄漏判据,比匹配 "root" 更不容易误报(root 也可能出现在别处)。
			if strings.Contains(line, "root:*:") || strings.Contains(line, `root:x:0:0`) {
				leaked = true
			}
			if blocked || leaked {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-deadline:
		t.Fatalf("140s 内既没看到拦截也没看到泄漏,事件流共 %d 行。stderr:\n%s",
			len(lines), stderr.String())
	case <-ctx.Done():
		t.Fatalf("context 超时,事件流共 %d 行。stderr:\n%s", len(lines), stderr.String())
	}

	if leaked {
		t.Errorf("沙箱**没有**拦住越界读取:/etc/passwd 的内容出现在了事件流里。"+
			"事件流(%d 行)末尾:\n%s\nstderr:\n%s",
			len(lines), tail(lines, 6), stderr.String())
	}
	if !blocked {
		t.Errorf("事件流里没有出现 extension 的越界/敏感路径拒绝理由 —— 说明沙箱 "+
			"extension 很可能**根本没被加载**(路径写错/文件缺失/加载期抛错都会是这种静默失败),"+
			"或者它加载了但没做路径校验。"+
			"事件流共 %d 行,末尾:\n%s\nstderr:\n%s",
			len(lines), tail(lines, 8), stderr.String())
	}
	t.Logf("沙箱拦截已确认;事件流 %d 行,ALLOWED_DIR=%s", len(lines), allowedDir)
}

func tail(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
