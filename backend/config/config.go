package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	DataDir   string // ~/.llm-knowledge
	Port      string
	ClaudeBin string // claude binary path
}

func Load() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory if home directory cannot be determined
		home = "."
	}
	return &Config{
		DataDir:   filepath.Join(home, ".llm-knowledge"),
		Port:      "3456",
		ClaudeBin: "claude",
	}
}