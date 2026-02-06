// Package config provides configuration management for MindCLI.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for MindCLI.
type Config struct {
	Sources    SourcesConfig    `yaml:"sources"`
	Embeddings EmbeddingsConfig `yaml:"embeddings"`
	Search     SearchConfig     `yaml:"search"`
	Indexing   IndexingConfig   `yaml:"indexing"`
	Storage    StorageConfig    `yaml:"storage"`
}

// SourcesConfig configures which data sources to index.
type SourcesConfig struct {
	Markdown  MarkdownSourceConfig  `yaml:"markdown"`
	PDF       PDFSourceConfig       `yaml:"pdf"`
	Email     EmailSourceConfig     `yaml:"email"`
	Browser   BrowserSourceConfig   `yaml:"browser"`
	Clipboard ClipboardSourceConfig `yaml:"clipboard"`
}

// MarkdownSourceConfig configures markdown/notes indexing.
type MarkdownSourceConfig struct {
	Enabled    bool     `yaml:"enabled"`
	Paths      []string `yaml:"paths"`
	Extensions []string `yaml:"extensions"`
	Ignore     []string `yaml:"ignore"`
}

// PDFSourceConfig configures PDF indexing.
type PDFSourceConfig struct {
	Enabled bool     `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
}

// EmailSourceConfig configures email indexing.
type EmailSourceConfig struct {
	Enabled bool     `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
	Formats []string `yaml:"formats"`
}

// BrowserSourceConfig configures browser history indexing.
type BrowserSourceConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Browsers       []string `yaml:"browsers"`
	IncludeContent bool     `yaml:"include_content"`
}

// ClipboardSourceConfig configures clipboard history.
type ClipboardSourceConfig struct {
	Enabled       bool `yaml:"enabled"`
	RetentionDays int  `yaml:"retention_days"`
	SkipPasswords bool `yaml:"skip_passwords"`
}

// EmbeddingsConfig configures the embedding provider.
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	OllamaURL string `yaml:"ollama_url"`
	OpenAIKey string `yaml:"openai_key"`
}

// SearchConfig configures search behavior.
type SearchConfig struct {
	HybridWeight float64 `yaml:"hybrid_weight"`
	ResultsLimit int     `yaml:"results_limit"`
}

// IndexingConfig configures the indexing pipeline.
type IndexingConfig struct {
	Workers int  `yaml:"workers"`
	Watch   bool `yaml:"watch"`
}

// StorageConfig configures where data is stored.
type StorageConfig struct {
	Path string `yaml:"path"`
}

// Default returns a Config with sensible defaults.
func Default() *Config {
	homeDir, _ := os.UserHomeDir()

	return &Config{
		Sources: SourcesConfig{
			Markdown: MarkdownSourceConfig{
				Enabled:    true,
				Paths:      []string{filepath.Join(homeDir, "notes")},
				Extensions: []string{".md", ".txt"},
				Ignore:     []string{"node_modules", ".git", ".obsidian"},
			},
			PDF: PDFSourceConfig{
				Enabled: true,
				Paths:   []string{filepath.Join(homeDir, "Documents")},
			},
			Email: EmailSourceConfig{
				Enabled: false,
				Paths:   []string{},
				Formats: []string{"mbox", "maildir"},
			},
			Browser: BrowserSourceConfig{
				Enabled:        true,
				Browsers:       []string{"chrome", "firefox", "safari"},
				IncludeContent: false,
			},
			Clipboard: ClipboardSourceConfig{
				Enabled:       true,
				RetentionDays: 30,
				SkipPasswords: true,
			},
		},
		Embeddings: EmbeddingsConfig{
			Provider:  "ollama",
			Model:     "nomic-embed-text",
			OllamaURL: "http://localhost:11434",
		},
		Search: SearchConfig{
			HybridWeight: 0.5,
			ResultsLimit: 50,
		},
		Indexing: IndexingConfig{
			Workers: 4,
			Watch:   true,
		},
		Storage: StorageConfig{
			Path: filepath.Join(homeDir, ".local", "share", "mindcli"),
		},
	}
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.Search.HybridWeight < 0 || c.Search.HybridWeight > 1 {
		return errors.New("search.hybrid_weight must be between 0 and 1")
	}
	if c.Search.ResultsLimit < 1 {
		return errors.New("search.results_limit must be at least 1")
	}
	if c.Indexing.Workers < 1 {
		return errors.New("indexing.workers must be at least 1")
	}
	if c.Embeddings.Provider != "ollama" && c.Embeddings.Provider != "openai" {
		return errors.New("embeddings.provider must be 'ollama' or 'openai'")
	}
	return nil
}

// Load loads configuration from the YAML file, falling back to defaults
// for any missing values.
func Load() (*Config, error) {
	cfg := Default()

	configPath, err := ConfigPath()
	if err != nil {
		return cfg, nil // Use defaults if we can't find config dir
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // No config file, use defaults
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save writes the configuration to the YAML file.
func (c *Config) Save() error {
	if err := EnsureConfigDir(); err != nil {
		return err
	}

	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// ConfigDir returns the directory where config files are stored.
func ConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mindcli"), nil
}

// ConfigPath returns the path to the main config file.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// EnsureConfigDir creates the config directory if it doesn't exist.
func EnsureConfigDir() error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0755)
}

// DataDir returns the data directory from config, creating it if needed.
func (c *Config) DataDir() (string, error) {
	if err := os.MkdirAll(c.Storage.Path, 0755); err != nil {
		return "", err
	}
	return c.Storage.Path, nil
}

// DatabasePath returns the path to the SQLite database.
func (c *Config) DatabasePath() (string, error) {
	dataDir, err := c.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "mindcli.db"), nil
}
