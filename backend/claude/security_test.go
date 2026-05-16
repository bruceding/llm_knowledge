package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestBuildSecureArgs_AlwaysContainsDisallowedTools is the regression test for the
// production incident where --dangerously-skip-permissions silently neutralized
// --allowedTools, letting the model run Bash/Task/etc. The contract: every
// invocation MUST emit --disallowedTools containing the dangerous set.
func TestBuildSecureArgs_AlwaysContainsDisallowedTools(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"Read"},
		{"Read", "Glob", "Grep", "LS"},
		{"Read", "Write", "Edit"},
	}

	for _, allowed := range cases {
		args, err := BuildSecureArgs(allowed)
		if err != nil {
			t.Fatalf("BuildSecureArgs(%v) returned unexpected error: %v", allowed, err)
		}

		idx := slices.Index(args, "--disallowedTools")
		if idx < 0 || idx == len(args)-1 {
			t.Fatalf("BuildSecureArgs(%v) missing --disallowedTools value: %v", allowed, args)
		}
		value := args[idx+1]

		// Every dangerous tool must appear in the value (csv).
		toolSet := strings.Split(value, ",")
		for _, dangerous := range DangerousDisallowedTools {
			if !slices.Contains(toolSet, dangerous) {
				t.Errorf("BuildSecureArgs(%v) --disallowedTools missing %q (got %q)",
					allowed, dangerous, value)
			}
		}
	}
}

func TestBuildSecureArgs_AllowedToolsRespected(t *testing.T) {
	args, err := BuildSecureArgs([]string{"Read", "Glob"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := slices.Index(args, "--allowedTools")
	if idx < 0 || idx == len(args)-1 {
		t.Fatalf("--allowedTools not present: %v", args)
	}
	if args[idx+1] != "Read,Glob" {
		t.Errorf("--allowedTools value = %q, want %q", args[idx+1], "Read,Glob")
	}
}

func TestBuildSecureArgs_EmptyAllowedToolsOmitsFlag(t *testing.T) {
	// When the caller passes no allowed tools (rare but legal — e.g., text-only
	// prompts), --allowedTools should not appear; --disallowedTools must still
	// appear so dangerous tools remain blocked.
	args, err := BuildSecureArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if slices.Contains(args, "--allowedTools") {
		t.Errorf("expected --allowedTools to be omitted when input is nil, got %v", args)
	}
	if !slices.Contains(args, "--disallowedTools") {
		t.Errorf("expected --disallowedTools to remain, got %v", args)
	}
}

func TestBuildSecureArgs_BypassFlagPresent(t *testing.T) {
	args, err := BuildSecureArgs([]string{"Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "--dangerously-skip-permissions") {
		t.Errorf("expected --dangerously-skip-permissions flag, got %v", args)
	}
}

// TestDangerousDisallowedTools_CoversKnownAttackVectors locks in the minimum set
// of tools that must never be reachable. Adding a new dangerous tool to the
// product should add it here too.
func TestDangerousDisallowedTools_CoversKnownAttackVectors(t *testing.T) {
	required := []string{"Bash", "Task", "NotebookEdit", "KillShell", "BashOutput", "SlashCommand"}
	for _, tool := range required {
		if !slices.Contains(DangerousDisallowedTools, tool) {
			t.Errorf("DangerousDisallowedTools missing required tool %q", tool)
		}
	}
}

// TestBuildSecureArgs_RejectsAllowedDangerousOverlap verifies BuildSecureArgs
// rejects programming errors where a caller passes a dangerous tool name into
// allowedTools. Without this guard, the CLI would receive conflicting
// --allowedTools and --disallowedTools entries with undefined precedence.
//
// Returns an error rather than panicking so a buggy caller inside a goroutine
// can't crash the whole server process.
func TestBuildSecureArgs_RejectsAllowedDangerousOverlap(t *testing.T) {
	for _, dangerous := range DangerousDisallowedTools {
		t.Run(dangerous, func(t *testing.T) {
			args, err := BuildSecureArgs([]string{"Read", dangerous})
			if err == nil {
				t.Errorf("expected error when allowedTools contains %q, got args=%v", dangerous, args)
			}
			if args != nil {
				t.Errorf("expected nil args on error, got %v", args)
			}
		})
	}
}

// TestCleanupStaleSettings_AgeGated verifies that orphaned settings files
// older than the cutoff are removed while recent files (potentially in use by
// a parallel backend instance during a rolling restart) survive.
func TestCleanupStaleSettings_AgeGated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	stalePath := filepath.Join(tmp, "claude-security-stale.json")
	freshPath := filepath.Join(tmp, "claude-security-fresh.json")
	unrelatedPath := filepath.Join(tmp, "other-file.json")

	for _, p := range []string{stalePath, freshPath, unrelatedPath} {
		if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	// Backdate the stale file well past the cutoff. Fresh file gets default
	// (just-now) mtime so it's safely on the keep side. Not parallel-safe due
	// to t.Setenv on TMPDIR — do not add t.Parallel() to this test.
	old := time.Now().Add(-2 * staleSettingsCutoff)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", stalePath, err)
	}

	cleanupStaleSettings()

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("expected stale file removed, stat err=%v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("expected fresh file kept, stat err=%v", err)
	}
	if _, err := os.Stat(unrelatedPath); err != nil {
		t.Errorf("expected unrelated file kept, stat err=%v", err)
	}
}

// TestBuildSecureEnv_ResolvesSymlinks pins the macOS symlink-aware behavior:
// /tmp on macOS is a symlink to /private/tmp, but path-validator.py calls
// os.path.realpath(allowed_dir) and resolves it. If BuildSecureEnv left the
// raw path, every path inside ALLOWED_DIR would mismatch and be denied.
func TestBuildSecureEnv_ResolvesSymlinks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("symlink layout for /tmp is macOS-specific")
	}

	tmp := t.TempDir() // typically /var/folders/.../T/... → realpath /private/var/...
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", tmp, err)
	}
	if resolved == tmp {
		t.Skipf("temp dir %q has no symlink layer; nothing to verify", tmp)
	}

	env := BuildSecureEnv(tmp)
	want := "ALLOWED_DIR=" + resolved
	if !slices.Contains(env, want) {
		t.Errorf("BuildSecureEnv(%q) did not emit %q; env=%v", tmp, want, env)
	}
}

// TestPathValidator_WebFetchSSRF drives path-validator.py with WebFetch payloads
// and verifies that the SSRF defense correctly blocks private/internal targets
// while letting plausible public-internet URLs through. Uses literal IPs for the
// blocked cases so the test does not depend on the host's DNS resolver.
func TestPathValidator_WebFetchSSRF(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	const validatorPath = "../scripts/path-validator.py"
	if _, err := os.Stat(validatorPath); err != nil {
		t.Fatalf("validator script missing: %v", err)
	}

	cases := []struct {
		name      string
		url       string
		wantBlock bool
	}{
		// Loopback / metadata / RFC1918 / IPv6 / non-http schemes — all blocked.
		{"loopback_v4", "http://127.0.0.1:6379/", true},
		{"aws_metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"rfc1918_10", "http://10.0.0.1/", true},
		{"rfc1918_192", "http://192.168.1.1/", true},
		{"rfc1918_172", "http://172.16.0.1/", true},
		{"loopback_v6", "http://[::1]:8080/", true},
		{"unspecified", "http://0.0.0.0/", true},
		{"file_scheme", "file:///etc/passwd", true},
		{"gopher_scheme", "gopher://internal/", true},
		{"missing_host", "http:///path", true},
		// Public-internet literal IP — allowed (1.1.1.1 is Cloudflare DNS).
		{"public_ip", "http://1.1.1.1/", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("python3", validatorPath)
			cmd.Env = []string{"ALLOWED_DIR=/tmp"}
			payload := `{"tool_name":"WebFetch","tool_input":{"url":"` + tc.url + `"}}`
			cmd.Stdin = strings.NewReader(payload)
			err := cmd.Run()

			gotBlocked := false
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
				gotBlocked = true
			}
			if gotBlocked != tc.wantBlock {
				t.Errorf("url=%q: gotBlocked=%v want=%v (err=%v)", tc.url, gotBlocked, tc.wantBlock, err)
			}
		})
	}
}

// TestDangerousToolsCrossLanguageSync verifies that the Go DangerousDisallowedTools
// list and the Python ALWAYS_DENIED_TOOLS frozenset stay aligned. Drift between
// them produces silent gaps in defense (e.g., a tool blocked in CLI but not in
// the hook backstop, or vice versa). This test caught the SlashCommand drift
// reported in PR #42 review.
func TestDangerousToolsCrossLanguageSync(t *testing.T) {
	const validatorPath = "../scripts/path-validator.py"

	body, err := os.ReadFile(validatorPath)
	if err != nil {
		t.Fatalf("read %s: %v", validatorPath, err)
	}

	// Match: ALWAYS_DENIED_TOOLS = frozenset({"a", "b", ...})
	re := regexp.MustCompile(`(?s)ALWAYS_DENIED_TOOLS\s*=\s*frozenset\(\{([^}]+)\}\)`)
	m := re.FindSubmatch(body)
	if m == nil {
		t.Fatalf("could not find ALWAYS_DENIED_TOOLS frozenset in %s", validatorPath)
	}

	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	var pyTools []string
	for _, tok := range tokenRe.FindAllSubmatch(m[1], -1) {
		pyTools = append(pyTools, string(tok[1]))
	}

	for _, goTool := range DangerousDisallowedTools {
		if !slices.Contains(pyTools, goTool) {
			t.Errorf("Go DangerousDisallowedTools has %q but Python ALWAYS_DENIED_TOOLS does not (drift)", goTool)
		}
	}
	for _, pyTool := range pyTools {
		if !slices.Contains(DangerousDisallowedTools, pyTool) {
			t.Errorf("Python ALWAYS_DENIED_TOOLS has %q but Go DangerousDisallowedTools does not (drift)", pyTool)
		}
	}
}
