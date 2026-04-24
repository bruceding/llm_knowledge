package config

import "os"

type Config struct {
	DataDir   string // ~/.llm-knowledge
	Port      string
	ClaudeBin string // claude binary path
}

func Load() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DataDir:   home + "/.llm-knowledge",
		Port:      "3456",
		ClaudeBin: "claude",
	}
}