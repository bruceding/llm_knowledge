package claude

import (
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
		args := BuildSecureArgs(allowed)

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
	args := BuildSecureArgs([]string{"Read", "Glob"})

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
	args := BuildSecureArgs(nil)

	if slices.Contains(args, "--allowedTools") {
		t.Errorf("expected --allowedTools to be omitted when input is nil, got %v", args)
	}
	if !slices.Contains(args, "--disallowedTools") {
		t.Errorf("expected --disallowedTools to remain, got %v", args)
	}
}

func TestBuildSecureArgs_BypassFlagPresent(t *testing.T) {
	args := BuildSecureArgs([]string{"Read"})
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

// TestBuildSecureArgs_PanicsOnAllowedDangerousOverlap verifies BuildSecureArgs
// rejects programming errors where a caller passes a dangerous tool name into
// allowedTools. Without this guard, the CLI would receive conflicting
// --allowedTools and --disallowedTools entries with undefined precedence.
func TestBuildSecureArgs_PanicsOnAllowedDangerousOverlap(t *testing.T) {
	for _, dangerous := range DangerousDisallowedTools {
		t.Run(dangerous, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic when allowedTools contains %q, got none", dangerous)
				}
			}()
			BuildSecureArgs([]string{"Read", dangerous})
		})
	}
}
