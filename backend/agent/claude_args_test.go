package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClaudeSessionArgs_AlwaysContainsDisallowedTools(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("sys prompt", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx := slices.Index(args, "--disallowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("missing --disallowedTools value: %v", args)
	}
	value := args[idx+1]

	// Every dangerous tool must appear in the value (csv).
	toolSet := strings.Split(value, ",")
	for _, dangerous := range ClaudeDangerousDisallowedTools {
		if !slices.Contains(toolSet, dangerous) {
			t.Errorf("--disallowedTools missing %q (got %q)", dangerous, value)
		}
	}
}

func TestClaudeSessionArgs_AllowedToolsRespected(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("", []string{"Read", "Glob", "Grep", "LS"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx := slices.Index(args, "--allowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("--allowedTools not present: %v", args)
	}
	if args[idx+1] != "Read,Glob,Grep,LS" {
		t.Errorf("--allowedTools value = %q, want %q", args[idx+1], "Read,Glob,Grep,LS")
	}
}

func TestClaudeSessionArgs_RejectsAllowedDangerousOverlap(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	for _, dangerous := range ClaudeDangerousDisallowedTools {
		t.Run(dangerous, func(t *testing.T) {
			args, err := p.SessionArgs("", []string{"Read", dangerous})
			if err == nil {
				t.Errorf("expected error when allowedTools contains %q, got args=%v", dangerous, args)
			}
			if args != nil {
				t.Errorf("expected nil args on error, got %v", args)
			}
		})
	}
}

func TestClaudeSessionArgs_ContainsStreamJSONFlags(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.SessionArgs("", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"--output-format", "stream-json", "--input-format", "--verbose",
		"--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
}

func TestClaudeResumeArgs_ContainsResumeID(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.ResumeArgs("prev-id-123", "", []string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || args[i+1] != "prev-id-123" {
		t.Fatalf("expected --resume prev-id-123 in args: %v", args)
	}
}

// TestClaudeOnceArgs_PrintModeMatchesClientSend pins the flag sequence to be
// byte-identical to claude.Client.Send (--print + stream-json + secure flags).
func TestClaudeOnceArgs_PrintModeMatchesClientSend(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("", []string{"Read", "Write", "Edit"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantPrefix := []string{
		"--print", "--output-format", "stream-json", "--verbose",
		"--allowedTools", "Read,Write,Edit",
	}
	if len(args) < len(wantPrefix) {
		t.Fatalf("args too short: %v", args)
	}
	for i, want := range wantPrefix {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], want, args)
		}
	}
	for _, want := range []string{"--disallowedTools", "--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
	for _, unwanted := range []string{"-p", "--system-prompt"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("did not expect %q in args: %v", unwanted, args)
		}
	}
}

// TestClaudeOnceArgs_TextModeMatchesSendSimpleWithRead pins the flag sequence to
// match claude.Client.SendSimpleWithRead (-p + secure flags, prompt appended by
// the caller).
func TestClaudeOnceArgs_TextModeMatchesSendSimpleWithRead(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("", []string{"Read"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) == 0 || args[0] != "-p" {
		t.Fatalf("expected args[0] == \"-p\", got: %v", args)
	}
	i := slices.Index(args, "--allowedTools")
	if i < 0 || i == len(args)-1 || args[i+1] != "Read" {
		t.Errorf("expected --allowedTools Read in args: %v", args)
	}
	for _, want := range []string{"--disallowedTools", "--dangerously-skip-permissions"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
	for _, unwanted := range []string{"--print", "--output-format", "--verbose", "--system-prompt"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("did not expect %q in args: %v", unwanted, args)
		}
	}
}

// TestClaudeOnceArgs_SystemPromptAppended verifies --system-prompt is appended
// after the secure flags, so it can never displace the security flag block.
func TestClaudeOnceArgs_SystemPromptAppended(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	args, err := p.OnceArgs("sys prompt", []string{"Read"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	i := slices.Index(args, "--system-prompt")
	if i < 0 || i == len(args)-1 || args[i+1] != "sys prompt" {
		t.Fatalf("expected adjacent --system-prompt \"sys prompt\" in args: %v", args)
	}
	secure := slices.Index(args, "--dangerously-skip-permissions")
	if secure < 0 || i < secure {
		t.Errorf("expected --system-prompt (idx %d) after secure flags (idx %d): %v", i, secure, args)
	}
}

func TestClaudeEnv_ResolvesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(filepath.Dir(tmp), "agent-env-link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	defer os.Remove(link)

	p := &ClaudeProtocol{bin: "claude"}
	env := p.Env(link)
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	want := "ALLOWED_DIR=" + resolved
	if !slices.Contains(env, want) {
		t.Fatalf("expected %q in env, got: %v", want, env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "ALLOWED_DIR=") && e != want {
			t.Fatalf("duplicate/shadowing ALLOWED_DIR entry: %q", e)
		}
	}
}

func TestClaudeEnv_EmptyDirReturnsNil(t *testing.T) {
	p := &ClaudeProtocol{bin: "claude"}
	if env := p.Env(""); env != nil {
		t.Fatalf("expected nil env for empty allowedDir, got: %v", env)
	}
}
