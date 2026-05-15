package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	cryptorand "crypto/rand"
)

// SecurityConfig manages file access security for Claude CLI sessions
type SecurityConfig struct {
	ScriptsDir   string // Directory containing hook scripts (e.g., /opt/llm-knowledge/scripts)
	SettingsPath string // Path to the generated settings.json file (generated once at startup)
}

// Global security config instance (initialized at startup)
var globalSecurityConfig *SecurityConfig

// InitSecurityConfig initializes the global security configuration
// scriptsDir should be the absolute path to the scripts directory
// If scriptsDir is empty, it defaults to the binary's directory + "/scripts"
// Settings file is generated once at startup and reused for all sessions
func InitSecurityConfig(scriptsDir string) error {
	if scriptsDir == "" {
		// Try to find scripts directory relative to working directory
		// This handles both development and production environments
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}

		// Check common locations
		candidates := []string{
			filepath.Join(cwd, "scripts"),
			filepath.Join(cwd, "backend", "scripts"),
			"/opt/llm-knowledge/scripts",
		}

		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				scriptsDir = candidate
				break
			}
		}

		if scriptsDir == "" {
			log.Printf("[security] Warning: scripts directory not found, security hooks disabled")
			// Don't return error - allow running without security hooks
			globalSecurityConfig = &SecurityConfig{ScriptsDir: "", SettingsPath: ""}
			return nil
		}
	}

	// Generate settings file once at startup
	settingsPath, err := generateSettingsFile(scriptsDir)
	if err != nil {
		log.Printf("[security] Warning: failed to generate settings file: %v", err)
		// Still initialize, but without settings path
		globalSecurityConfig = &SecurityConfig{ScriptsDir: scriptsDir, SettingsPath: ""}
		return nil
	}

	globalSecurityConfig = &SecurityConfig{
		ScriptsDir:   scriptsDir,
		SettingsPath: settingsPath,
	}
	log.Printf("[security] Initialized with scripts directory: %s, settings: %s", scriptsDir, settingsPath)
	return nil
}

// generateSettingsFile creates the settings.json file with security hooks
// Called once at startup, returns the path to the generated file
func generateSettingsFile(scriptsDir string) (string, error) {
	validatorPath := filepath.Join(scriptsDir, "path-validator.py")

	// Check if validator exists
	if _, err := os.Stat(validatorPath); os.IsNotExist(err) {
		return "", fmt.Errorf("path-validator.py not found at %s", validatorPath)
	}

	// Hook all file-access and execution tools as defense-in-depth. Even if
	// --disallowedTools is misconfigured, the hook will deny these tools.
	//
	// The always-denied half is derived from DangerousDisallowedTools so the
	// CLI flag list and the hook list cannot drift apart. ALWAYS_DENIED_TOOLS
	// in path-validator.py must mirror DangerousDisallowedTools as well.
	pathValidatedTools := []string{"Read", "Write", "Edit", "Glob", "Grep", "LS"}
	hookedTools := append(pathValidatedTools, DangerousDisallowedTools...)
	preToolUseHooks := make([]HookMatcher, 0, len(hookedTools))
	for _, tool := range hookedTools {
		preToolUseHooks = append(preToolUseHooks, HookMatcher{
			Matcher: tool,
			Hooks: []Hook{
				{
					Type:    "command",
					Command: fmt.Sprintf("python3 \"%s\"", validatorPath),
					Timeout: 5,
				},
			},
		})
	}

	settings := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": preToolUseHooks,
		},
	}

	jsonData, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write to temp file with PID (one file per backend process)
	// Use 0600 permissions to prevent other users from reading the hook config
	rnd := make([]byte, 8)
	if _, err := cryptorand.Read(rnd); err != nil {
		return "", fmt.Errorf("failed to generate random suffix: %w", err)
	}
	settingsPath := filepath.Join(os.TempDir(), fmt.Sprintf("claude-security-%x.json", rnd))
	if err := os.WriteFile(settingsPath, jsonData, 0600); err != nil {
		return "", fmt.Errorf("failed to write settings file: %w", err)
	}

	return settingsPath, nil
}

// GetSettingsPath returns the path to the settings.json file
// Returns empty string if security is not enabled
func GetSettingsPath() string {
	if globalSecurityConfig == nil {
		return ""
	}
	return globalSecurityConfig.SettingsPath
}

type HookMatcher struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// BuildSecureEnv builds environment variables for Claude CLI with security settings
// Returns a complete environment slice with ALLOWED_DIR set, filtering any
// pre-existing ALLOWED_DIR from the parent environment to prevent shadowing.
func BuildSecureEnv(allowedDir string) []string {
	if allowedDir == "" {
		return nil
	}

	// Filter out any existing ALLOWED_DIR from parent environment to prevent shadowing
	baseEnv := os.Environ()
	filtered := make([]string, 0, len(baseEnv))
	for _, e := range baseEnv {
		if !strings.HasPrefix(e, "ALLOWED_DIR=") {
			filtered = append(filtered, e)
		}
	}

	return append(filtered, fmt.Sprintf("ALLOWED_DIR=%s", allowedDir))
}

// DangerousDisallowedTools is the canonical list of tools that must never be allowed
// in production sessions. --disallowedTools takes precedence over
// --dangerously-skip-permissions, so listing them here yields a hard block.
//
// WebFetch/WebSearch are intentionally NOT included — they are useful and cannot
// directly read local files. SSRF risk for WebFetch is tracked separately.
var DangerousDisallowedTools = []string{
	"Bash",
	"Task",
	"NotebookEdit",
	"KillShell",
	"BashOutput",
	"SlashCommand",
}

// BuildSecureArgs returns the standard set of CLI args that enforce the project's
// security model: a tool whitelist, an explicit blacklist of dangerous tools, the
// permissions bypass, and the security settings file (when configured).
//
// Callers should append their own --output-format / --input-format / --print /
// --resume / --system-prompt flags around the returned slice.
//
// Returns an error if allowedTools contains any entry from DangerousDisallowedTools.
// That is a programming error (the conflicting flags would leave behavior up to CLI
// internals), but we surface it as an error rather than panic so a buggy caller
// inside a goroutine can't crash the whole server process.
func BuildSecureArgs(allowedTools []string) ([]string, error) {
	for _, t := range allowedTools {
		if slices.Contains(DangerousDisallowedTools, t) {
			return nil, fmt.Errorf("BuildSecureArgs: allowedTools contains dangerous tool %q; "+
				"this conflicts with --disallowedTools and must be a programming error", t)
		}
	}

	args := make([]string, 0, 8)

	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowedTools, ","))
	}

	args = append(args,
		"--disallowedTools", strings.Join(DangerousDisallowedTools, ","),
		"--dangerously-skip-permissions",
	)

	if settingsPath := GetSettingsPath(); settingsPath != "" {
		args = append(args, "--settings", settingsPath)
	}

	return args, nil
}

// CleanupSecuritySettings removes the settings file (call on server shutdown)
func CleanupSecuritySettings() {
	if globalSecurityConfig != nil && globalSecurityConfig.SettingsPath != "" {
		os.Remove(globalSecurityConfig.SettingsPath)
		log.Printf("[security] Cleaned up settings file: %s", globalSecurityConfig.SettingsPath)
	}
}
