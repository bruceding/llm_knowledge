package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// SecurityConfig manages file access security for Claude CLI sessions
type SecurityConfig struct {
	ScriptsDir string // Directory containing hook scripts (e.g., /opt/llm-knowledge/scripts)
}

// Global security config instance (initialized at startup)
var globalSecurityConfig *SecurityConfig

// InitSecurityConfig initializes the global security configuration
// scriptsDir should be the absolute path to the scripts directory
// If scriptsDir is empty, it defaults to the binary's directory + "/scripts"
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
			globalSecurityConfig = &SecurityConfig{ScriptsDir: ""}
			return nil
		}
	}

	globalSecurityConfig = &SecurityConfig{ScriptsDir: scriptsDir}
	log.Printf("[security] Initialized with scripts directory: %s", scriptsDir)
	return nil
}

// GetSecurityConfig returns the global security configuration
func GetSecurityConfig() *SecurityConfig {
	return globalSecurityConfig
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
	Prompt  string `json:"prompt,omitempty"`
}

// GenerateSecuritySettings generates a settings.json content with security hooks
// Returns the JSON content and the path to the temporary settings file
func GenerateSecuritySettings(allowedDir string) (string, string, error) {
	if globalSecurityConfig == nil || globalSecurityConfig.ScriptsDir == "" {
		// No security config - return empty settings (no hooks)
		return "{}", "", nil
	}

	validatorPath := filepath.Join(globalSecurityConfig.ScriptsDir, "path-validator.py")

	// Check if validator exists
	if _, err := os.Stat(validatorPath); os.IsNotExist(err) {
		log.Printf("[security] Warning: path-validator.py not found at %s", validatorPath)
		return "{}", "", nil
	}

	settings := HookSettings{}
	settings.Hooks.PreToolUse = []HookMatcher{
		{
			Matcher: "Read",
			Hooks: []Hook{
				{
					Type:    "command",
					Command: validatorPath,
				},
			},
		},
	}

	jsonData, err := json.Marshal(settings)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write to temporary file
	settingsPath := filepath.Join(os.TempDir(), fmt.Sprintf("claude-security-%d.json", os.Getpid()))
	if err := os.WriteFile(settingsPath, jsonData, 0644); err != nil {
		return "", "", fmt.Errorf("failed to write settings file: %w", err)
	}

	log.Printf("[security] Generated settings file: %s (allowedDir=%s)", settingsPath, allowedDir)
	return string(jsonData), settingsPath, nil
}

// CleanupSecuritySettings removes the temporary settings file
func CleanupSecuritySettings(settingsPath string) {
	if settingsPath != "" {
		os.Remove(settingsPath)
	}
}

// BuildSecureEnv builds environment variables for Claude CLI with security settings
// Returns a slice of environment variables to append to cmd.Env
func BuildSecureEnv(allowedDir string) []string {
	env := []string{}

	if allowedDir != "" {
		env = append(env, fmt.Sprintf("ALLOWED_DIR=%s", allowedDir))
	}

	return env
}

// IsSecurityEnabled returns true if security hooks are configured
func IsSecurityEnabled() bool {
	return globalSecurityConfig != nil && globalSecurityConfig.ScriptsDir != ""
}