package pdf2zh

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	installStatus     string = "not_installed" // not_installed, installing, installed, failed
	installStatusMux  sync.RWMutex
	installLog        []string
	installLogMux     sync.Mutex
)

// GetInstallStatus returns current pdf2zh installation status
func GetInstallStatus() string {
	installStatusMux.RLock()
	defer installStatusMux.RUnlock()
	return installStatus
}

// GetInstallLog returns installation log messages
func GetInstallLog() []string {
	installLogMux.Lock()
	defer installLogMux.Unlock()
	return installLog
}

// addInstallLog adds a message to installation log
func addInstallLog(msg string) {
	installLogMux.Lock()
	installLog = append(installLog, msg)
	installLogMux.Unlock()
	log.Printf("[pdf2zh] %s", msg)
}

// installPDF2Zh performs the actual installation
func CheckAndInstall(venvDir string) {
	// Check if venv exists and pdf2zh is installed
	venvExists := false
	pdf2zhInstalled := false

	// Check venv directory
	if _, err := os.Stat(venvDir); err == nil {
		venvExists = true
		// Check if pdf2zh is in the venv
		pdf2zhPath := filepath.Join(venvDir, "bin", "pdf2zh")
		if _, err := os.Stat(pdf2zhPath); err == nil {
			pdf2zhInstalled = true
		}
	}

	if venvExists && pdf2zhInstalled {
		installStatusMux.Lock()
		installStatus = "installed"
		installStatusMux.Unlock()
		addInstallLog("pdf2zh already installed at " + venvDir)
		return
	}

	// Need to install - start async installation
	installStatusMux.Lock()
	installStatus = "installing"
	installStatusMux.Unlock()

	go installPDF2Zh(venvDir)
}

// installPDF2Zh performs the actual installation
func installPDF2Zh(venvDir string) {
	addInstallLog("Starting pdf2zh installation...")

	// Step 1: Check Python 3.12 availability (start.sh should have verified this)
	pythonBin := "python3.12"
	if _, err := exec.LookPath(pythonBin); err != nil {
		addInstallLog("Python 3.12 not found. Run start.sh to check dependencies, or install manually.")
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}

	// Create venv
	addInstallLog(fmt.Sprintf("Creating Python virtual environment at %s", venvDir))
	cmd := exec.Command(pythonBin, "-m", "venv", venvDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		addInstallLog(fmt.Sprintf("Failed to create venv: %v\n%s", err, output))
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}
	addInstallLog("Virtual environment created successfully")

	// Step 2: Install pdf2zh in venv (qpdf dependency handled by start.sh)
	addInstallLog("Installing pdf2zh package...")
	installCmd := fmt.Sprintf("source '%s/bin/activate' && pip install pdf2zh", venvDir)
	cmd = exec.Command("bash", "-c", installCmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		addInstallLog(fmt.Sprintf("Failed to create stdout pipe: %v", err))
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		addInstallLog(fmt.Sprintf("Failed to create stderr pipe: %v", err))
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}

	if err := cmd.Start(); err != nil {
		addInstallLog(fmt.Sprintf("Failed to start pip install: %v", err))
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}

	// Read output
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Successfully") || strings.Contains(line, "Installing") {
				addInstallLog(line)
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// Only log errors, not warnings
			if strings.Contains(line, "error") || strings.Contains(line, "ERROR") {
				addInstallLog("Error: " + line)
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		addInstallLog(fmt.Sprintf("pip install failed: %v", err))
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}

	// Verify installation
	pdf2zhPath := filepath.Join(venvDir, "bin", "pdf2zh")
	if _, err := os.Stat(pdf2zhPath); err != nil {
		addInstallLog("pdf2zh binary not found after installation")
		installStatusMux.Lock()
		installStatus = "failed"
		installStatusMux.Unlock()
		return
	}

	installStatusMux.Lock()
	installStatus = "installed"
	installStatusMux.Unlock()
	addInstallLog("pdf2zh installation completed successfully!")
	addInstallLog("Binary location: " + pdf2zhPath)
}

// IsReady returns true if pdf2zh is ready to use
func IsReady(venvDir string) bool {
	installStatusMux.RLock()
	status := installStatus
	installStatusMux.RUnlock()

	if status == "installed" {
		return true
	}

	// Double check by looking at the actual files
	pdf2zhPath := filepath.Join(venvDir, "bin", "pdf2zh")
	if _, err := os.Stat(pdf2zhPath); err == nil {
		installStatusMux.Lock()
		installStatus = "installed"
		installStatusMux.Unlock()
		return true
	}

	return false
}