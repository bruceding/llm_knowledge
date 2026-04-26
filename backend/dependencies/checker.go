package dependencies

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DependencyStatus represents the status of a single dependency
type DependencyStatus struct {
	Name        string    // "claude", "anthropic-agent-skills"
	Status      string    // "installed", "not_installed", "installing", "failed"
	Message     string    // Status message or installation instructions
	InstalledAt time.Time // When the dependency was confirmed installed
}

var (
	statuses     = make(map[string]*DependencyStatus)
	statusesMux  sync.RWMutex
	installLog   []string
	installLogMux sync.Mutex
)

// GetStatuses returns all dependency statuses
func GetStatuses() map[string]*DependencyStatus {
	statusesMux.RLock()
	defer statusesMux.RUnlock()

	// Return copy to avoid race conditions
	result := make(map[string]*DependencyStatus)
	for k, v := range statuses {
		result[k] = v
	}
	return result
}

// GetInstallLog returns installation log messages
func GetInstallLog() []string {
	installLogMux.Lock()
	defer installLogMux.Unlock()
	return installLog
}

// addLog adds a message to installation log
func addLog(msg string) {
	installLogMux.Lock()
	installLog = append(installLog, msg)
	installLogMux.Unlock()
	log.Printf("[dependencies] %s", msg)
}

// setStatus updates a dependency's status
func setStatus(name, status, message string) {
	statusesMux.Lock()
	defer statusesMux.Unlock()

	if statuses[name] == nil {
		statuses[name] = &DependencyStatus{Name: name}
	}
	statuses[name].Status = status
	statuses[name].Message = message
	if status == "installed" {
		statuses[name].InstalledAt = time.Now()
	}
}

// CheckAll checks all dependencies asynchronously
func CheckAll() {
 setStatus("claude", "checking", "Checking Claude CLI...")
 setStatus("anthropic-agent-skills", "pending", "Waiting for Claude CLI check...")

 // Claude CLI check must complete before plugin check
 go func() {
  checkClaudeCLI()
  // Only check plugin after Claude CLI is confirmed installed
  statusesMux.RLock()
  claudeStatus := statuses["claude"]
  statusesMux.RUnlock()

  if claudeStatus != nil && claudeStatus.Status == "installed" {
   setStatus("anthropic-agent-skills", "checking", "Checking plugin...")
   checkPlugin()
  }
 }()
}

// checkClaudeCLI checks if Claude CLI is installed
func checkClaudeCLI() {
 addLog("Checking Claude CLI installation...")

 cmd := exec.Command("claude", "--version")
 output, err := cmd.CombinedOutput()

 if err != nil {
  addLog(fmt.Sprintf("Claude CLI not found: %v", err))
  setStatus("claude", "not_installed",
   "Claude CLI is required. Install from: https://claude.ai/code or run: npm install -g @anthropic-ai/claude-code")
  return
 }

 version := strings.TrimSpace(string(output))
 addLog(fmt.Sprintf("Claude CLI found: %s", version))
 setStatus("claude", "installed", version)
}

// checkPlugin checks if anthropic-agent-skills plugin is installed and enabled
func checkPlugin() {
 addLog("Checking anthropic-agent-skills plugin...")

 // Run claude plugin list
 cmd := exec.Command("claude", "plugin", "list")
 output, err := cmd.CombinedOutput()

 if err != nil {
  addLog(fmt.Sprintf("Failed to check plugins: %v", err))
  setStatus("anthropic-agent-skills", "not_installed",
   "Failed to check plugin status. Please run: claude plugin install example-skills@anthropic-agent-skills")
  return
 }

 // Parse output to find example-skills@anthropic-agent-skills
 outputStr := string(output)
 if isPluginEnabled(outputStr, "example-skills@anthropic-agent-skills") {
  addLog("anthropic-agent-skills plugin is installed and enabled")
  setStatus("anthropic-agent-skills", "installed", "example-skills@anthropic-agent-skills enabled")
  return
 }

 // Plugin not installed or not enabled
 addLog("anthropic-agent-skills plugin not found or disabled")

 // Try to install automatically
 setStatus("anthropic-agent-skills", "installing", "Installing plugin...")
 installPlugin()
}

// isPluginEnabled checks if a plugin is enabled in the plugin list output
// Output format is multiline:
//   ❯ example-skills@anthropic-agent-skills
//     Version: 1ed29a03dc85
//     Scope: user
//     Status: ✔ enabled
func isPluginEnabled(output, pluginName string) bool {
 lines := strings.Split(output, "\n")
 foundPlugin := false

 for _, line := range lines {
  // Check if we found the plugin line
  if strings.Contains(line, pluginName) {
   foundPlugin = true
   continue
  }

  // If we found the plugin, check subsequent lines for enabled status
  if foundPlugin {
   if strings.Contains(line, "Status:") && strings.Contains(line, "enabled") {
    return true
   }
   // If we hit another plugin (line starts with "❯"), stop searching
   if strings.HasPrefix(strings.TrimSpace(line), "❯") {
    foundPlugin = false
   }
  }
 }
 return false
}

// installPlugin installs the anthropic-agent-skills plugin
func installPlugin() {
 addLog("Installing anthropic-agent-skills plugin...")

 cmd := exec.Command("claude", "plugin", "install", "example-skills@anthropic-agent-skills")
 var stdout, stderr bytes.Buffer
 cmd.Stdout = &stdout
 cmd.Stderr = &stderr

 err := cmd.Run()
 if err != nil {
  addLog(fmt.Sprintf("Plugin install failed: %v\nstderr: %s", err, stderr.String()))
  setStatus("anthropic-agent-skills", "failed",
   fmt.Sprintf("Installation failed. Please run manually: claude plugin install example-skills@anthropic-agent-skills"))
  return
 }

 addLog(fmt.Sprintf("Plugin installed successfully: %s", stdout.String()))
 setStatus("anthropic-agent-skills", "installed", "example-skills@anthropic-agent-skills installed")
}

// IsReady returns true if all critical dependencies are installed
func IsReady() bool {
 statusesMux.RLock()
 defer statusesMux.RUnlock()

 claude := statuses["claude"]
 plugin := statuses["anthropic-agent-skills"]

 return claude != nil && claude.Status == "installed" &&
  plugin != nil && plugin.Status == "installed"
}

// IsClaudeReady returns true if Claude CLI is installed
func IsClaudeReady() bool {
 statusesMux.RLock()
 defer statusesMux.RUnlock()

 claude := statuses["claude"]
 return claude != nil && claude.Status == "installed"
}