package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPiProbe_MissingBinaryErrors 断言 Probe 对不在 PATH 里的二进制返回错误,
// 而不是静默成功 —— Task 7 的 Settings 开关要靠它把「切到不可用的 pi」拦成 400。
func TestPiProbe_MissingBinaryErrors(t *testing.T) {
	p := NewPiProtocol("definitely-not-a-real-binary-xyz", t.TempDir())
	err := p.Probe(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "npm install -g @earendil-works/pi-coding-agent") {
		t.Errorf("error should carry the install hint (Task 7 surfaces it as a 400 message), got: %v", err)
	}
}

// TestPiProbe_EmptyBinErrors 断言空二进制名不会 panic,也不会被当成可用。
func TestPiProbe_EmptyBinErrors(t *testing.T) {
	p := NewPiProtocol("", t.TempDir())
	if err := p.Probe(context.Background()); err == nil {
		t.Fatal("expected an error for an empty bin, got nil")
	}
}

// TestPiProbe_RealBinary 对真实 pi 做一次探测;不在 PATH 时跳过,不得让 CI 或
// 他人环境变红(计划全局约束)。
func TestPiProbe_RealBinary(t *testing.T) {
	p := NewPiProtocol("pi", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.Probe(ctx); err != nil {
		if strings.Contains(err.Error(), "not found in PATH") {
			t.Skipf("pi not installed: %v", err)
		}
		t.Fatalf("Probe against a real pi failed: %v", err)
	}
}

// TestClaudeProbeAlwaysAvailable 钉住 Claude 后端的既有行为:不探测,恒可用。
// 这保证 llmBackend=claude 时 Settings 保存路径与改造前逐条等价。
func TestClaudeProbeAlwaysAvailable(t *testing.T) {
	p := NewClaudeProtocol("definitely-not-a-real-binary-xyz", "")
	if err := p.Probe(context.Background()); err != nil {
		t.Errorf("ClaudeProtocol.Probe must stay a no-op, got: %v", err)
	}
}
