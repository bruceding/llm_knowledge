package config

import (
	"flag"
	"os"
	"path/filepath"
)

type Config struct {
	DataDir   string // ~/.llm-knowledge
	LogDir    string // ~/.llm-knowledge/logs
	Port      string
	ClaudeBin string // claude binary path
}

func Load() *Config {
	// Command line flags
	port := flag.String("port", "", "Server port (default: 3456)")
	flag.Parse()

	// If flag not set, check environment variable
	portValue := *port
	if portValue == "" {
		portValue = os.Getenv("PORT")
	}
	if portValue == "" {
		portValue = "3456"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory if home directory cannot be determined
		home = "."
	}
	dataDir := filepath.Join(home, ".llm-knowledge")

	// Read Claude binary path from environment, default to "claude"
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	return &Config{
		DataDir:   dataDir,
		LogDir:    filepath.Join(dataDir, "logs"),
		Port:      portValue,
		ClaudeBin: claudeBin,
	}
}