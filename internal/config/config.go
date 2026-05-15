package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/prowlarr"
	"gopkg.in/yaml.v3"
)

type BacklogConfig struct {
	MaxConcurrentDownloads int    `yaml:"max_concurrent_downloads"`
	MaxRetries             int    `yaml:"max_retries"`
	RetryBackoffMinutes    []int  `yaml:"retry_backoff_minutes"`
	AnnualSubfolder        string `yaml:"annual_subfolder"`
	EnableVariants         bool   `yaml:"enable_variants"`
}

type Config struct {
	Port                int           `yaml:"port"`
	LibraryDir          string        `yaml:"library_dir"`
	DataDir             string        `yaml:"data_dir"`
	LogLevel            string        `yaml:"log_level"`
	ComicVineAPIKey     string        `yaml:"comicvine_api_key"`
	MetronUsername      string        `yaml:"metron_username"`
	MetronAPIToken      string        `yaml:"metron_api_token"`
	ProwlarrURL         string        `yaml:"prowlarr_url"`
	ProwlarrAPIKey      string        `yaml:"prowlarr_api_key"`
	ProwlarrCategory    string        `yaml:"prowlarr_category"`
	SessionLifetimeDays int           `yaml:"session_lifetime_days"`
	Backlog             BacklogConfig `yaml:"backlog"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Port:             22526,
		LibraryDir:       filepath.Join(home, "Comics"),
		DataDir:          filepath.Join(home, ".longbox"),
		LogLevel:         "info",
		ProwlarrCategory: prowlarr.DefaultCategory,
		Backlog: BacklogConfig{
			MaxConcurrentDownloads: 25,
			MaxRetries:             3,
			RetryBackoffMinutes:    []int{5, 15, 60},
			AnnualSubfolder:        "Annuals",
			EnableVariants:         true,
		},
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
	if v := os.Getenv("LONGBOX_METRON_USERNAME"); v != "" {
		cfg.MetronUsername = v
	}
	if v := os.Getenv("LONGBOX_METRON_API_TOKEN"); v != "" {
		cfg.MetronAPIToken = v
	}
	if v := os.Getenv("LONGBOX_PROWLARR_URL"); v != "" {
		cfg.ProwlarrURL = v
	}
	if v := os.Getenv("LONGBOX_PROWLARR_API_KEY"); v != "" {
		cfg.ProwlarrAPIKey = v
	}
	if v := os.Getenv("LONGBOX_PROWLARR_CATEGORY"); v != "" {
		cfg.ProwlarrCategory = v
	}
	if v := os.Getenv("LONGBOX_SESSION_LIFETIME_DAYS"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.SessionLifetimeDays)
	}
	if v := os.Getenv("LONGBOX_BACKLOG_MAX_CONCURRENT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Backlog.MaxConcurrentDownloads)
	}
	if v := os.Getenv("LONGBOX_BACKLOG_MAX_RETRIES"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Backlog.MaxRetries)
	}
	if v := os.Getenv("LONGBOX_BACKLOG_RETRY_BACKOFF"); v != "" {
		cfg.Backlog.RetryBackoffMinutes = parseCSVInts(v)
	}
	if v := os.Getenv("LONGBOX_BACKLOG_ANNUAL_SUBFOLDER"); v != "" {
		cfg.Backlog.AnnualSubfolder = v
	}
	if v := os.Getenv("LONGBOX_BACKLOG_ENABLE_VARIANTS"); v != "" {
		if strings.EqualFold(v, "0") || strings.EqualFold(v, "false") {
			cfg.Backlog.EnableVariants = false
		} else {
			cfg.Backlog.EnableVariants = true
		}
	}

	// Ensure data directory exists (0700: only owner can access DB with API keys)
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	// Ensure covers subdirectory exists
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "covers"), 0700); err != nil {
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

func (c *Config) SessionLifetime() time.Duration {
	days := c.SessionLifetimeDays
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

func parseCSVInts(v string) []int {
	parts := strings.Split(v, ",")
	result := make([]int, 0, len(parts))
	for _, p := range parts {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err == nil {
			result = append(result, n)
		}
	}
	if len(result) == 0 {
		return []int{5, 15, 60}
	}
	return result
}
