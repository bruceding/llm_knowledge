package claude

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
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
