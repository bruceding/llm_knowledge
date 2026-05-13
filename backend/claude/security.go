package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	// Hook all file-access tools to prevent bypass via Glob/Grep/LS
	hookedTools := []string{"Read", "Glob", "Grep", "LS"}
	preToolUseHooks := make([]HookMatcher, 0, len(hookedTools))
	for _, tool := range hookedTools {
		preToolUseHooks = append(preToolUseHooks, HookMatcher{
			Matcher: tool,
			Hooks: []Hook{
				{
					Type:    "command",
					Command: validatorPath,
				},
			},
		})
	}

	settings := HookSettings{}
	settings.Hooks.PreToolUse = preToolUseHooks

	jsonData, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write to temp file with PID (one file per backend process)
	// Use 0600 permissions to prevent other users from reading the hook config
	settingsPath := filepath.Join(os.TempDir(), fmt.Sprintf("claude-security-%d.json", os.Getpid()))
	if err := os.WriteFile(settingsPath, jsonData, 0600); err != nil {
		return "", fmt.Errorf("failed to write settings file: %w", err)
	}

	return settingsPath, nil
}

// GetSecurityConfig returns the global security configuration
func GetSecurityConfig() *SecurityConfig {
	return globalSecurityConfig
}

// GetSettingsPath returns the path to the settings.json file
// Returns empty string if security is not enabled
func GetSettingsPath() string {
	if globalSecurityConfig == nil {
		return ""
	}
	return globalSecurityConfig.SettingsPath
}

// HookSettings represents the hook configuration for settings.json
type HookSettings struct {
	Hooks struct {
		PreToolUse []HookMatcher `json:"PreToolUse"`
	} `json:"hooks"`
}

type HookMatcher struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
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

// IsSecurityEnabled returns true if security hooks are configured
func IsSecurityEnabled() bool {
	return globalSecurityConfig != nil && globalSecurityConfig.ScriptsDir != "" && globalSecurityConfig.SettingsPath != ""
}

// CleanupSecuritySettings removes the settings file (call on server shutdown)
func CleanupSecuritySettings() {
	if globalSecurityConfig != nil && globalSecurityConfig.SettingsPath != "" {
		os.Remove(globalSecurityConfig.SettingsPath)
		log.Printf("[security] Cleaned up settings file: %s", globalSecurityConfig.SettingsPath)
	}
}
