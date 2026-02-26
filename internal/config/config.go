package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port            int    `yaml:"port"`
	LibraryDir      string `yaml:"library_dir"`
	DataDir         string `yaml:"data_dir"`
	LogLevel        string `yaml:"log_level"`
	ComicVineAPIKey string `yaml:"comicvine_api_key"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Port:       8080,
		LibraryDir: filepath.Join(home, "Comics"),
		DataDir:    filepath.Join(home, ".longbox"),
		LogLevel:   "info",
	}
}

func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	// Environment variable overrides
	if v := os.Getenv("LONGBOX_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Port)
	}
	if v := os.Getenv("LONGBOX_LIBRARY_DIR"); v != "" {
		cfg.LibraryDir = v
	}
	if v := os.Getenv("LONGBOX_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("LONGBOX_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("LONGBOX_COMICVINE_API_KEY"); v != "" {
		cfg.ComicVineAPIKey = v
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	// Ensure covers subdirectory exists
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "covers"), 0755); err != nil {
		return nil, fmt.Errorf("creating covers directory: %w", err)
	}

	return &cfg, nil
}

func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "longbox.db")
}

func (c *Config) CoversDir() string {
	return filepath.Join(c.DataDir, "covers")
}
