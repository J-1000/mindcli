// Package config provides configuration management for MindCLI.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for MindCLI.
type Config struct {
	Sources    SourcesConfig    `yaml:"sources"`
	Embeddings EmbeddingsConfig `yaml:"embeddings"`
	Search     SearchConfig     `yaml:"search"`
	Indexing   IndexingConfig   `yaml:"indexing"`
	Storage    StorageConfig    `yaml:"storage"`
	Privacy    PrivacyConfig    `yaml:"privacy"`
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
	Enabled              bool     `yaml:"enabled"`
	Paths                []string `yaml:"paths"`
	Formats              []string `yaml:"formats"`
	Ignore               []string `yaml:"ignore"`
	MaskSensitivePreview bool     `yaml:"mask_sensitive_preview"`
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

// EmbeddingsConfig configures the embedding provider and LLM.
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	LLMModel  string `yaml:"llm_model"`
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

// PrivacyConfig configures privacy controls for displaying content.
type PrivacyConfig struct {
	RedactPatterns []string `yaml:"redact_patterns"`
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
				Enabled:              false,
				Paths:                []string{},
				Formats:              []string{"mbox", "maildir"},
				Ignore:               []string{},
				MaskSensitivePreview: true,
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
			LLMModel:  "llama3.2",
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
		Privacy: PrivacyConfig{
			RedactPatterns: []string{},
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

	applyEnvOverrides(cfg)

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
	if dir := os.Getenv("MINDCLI_CONFIG_DIR"); dir != "" {
		return expandUserPath(dir), nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mindcli"), nil
}

// ConfigPath returns the path to the main config file.
func ConfigPath() (string, error) {
	if path := os.Getenv("MINDCLI_CONFIG_PATH"); path != "" {
		return expandUserPath(path), nil
	}

	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// EnsureConfigDir creates the config directory if it doesn't exist.
func EnsureConfigDir() error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
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

func applyEnvOverrides(cfg *Config) {
	// Storage
	setStringFromEnv("MINDCLI_STORAGE_PATH", &cfg.Storage.Path)

	// Indexing
	setIntFromEnv("MINDCLI_INDEXING_WORKERS", &cfg.Indexing.Workers)
	setBoolFromEnv("MINDCLI_INDEXING_WATCH", &cfg.Indexing.Watch)

	// Search
	setFloat64FromEnv("MINDCLI_SEARCH_HYBRID_WEIGHT", &cfg.Search.HybridWeight)
	setIntFromEnv("MINDCLI_SEARCH_RESULTS_LIMIT", &cfg.Search.ResultsLimit)

	// Embeddings
	setStringFromEnv("MINDCLI_EMBEDDINGS_PROVIDER", &cfg.Embeddings.Provider)
	setStringFromEnv("MINDCLI_EMBEDDINGS_MODEL", &cfg.Embeddings.Model)
	setStringFromEnv("MINDCLI_EMBEDDINGS_LLM_MODEL", &cfg.Embeddings.LLMModel)
	setStringFromEnv("MINDCLI_EMBEDDINGS_OLLAMA_URL", &cfg.Embeddings.OllamaURL)
	setStringFromEnv("MINDCLI_EMBEDDINGS_OPENAI_KEY", &cfg.Embeddings.OpenAIKey)

	// Sources: markdown
	setBoolFromEnv("MINDCLI_SOURCES_MARKDOWN_ENABLED", &cfg.Sources.Markdown.Enabled)
	setCSVFromEnv("MINDCLI_SOURCES_MARKDOWN_PATHS", &cfg.Sources.Markdown.Paths)
	setCSVFromEnv("MINDCLI_SOURCES_MARKDOWN_EXTENSIONS", &cfg.Sources.Markdown.Extensions)
	setCSVFromEnv("MINDCLI_SOURCES_MARKDOWN_IGNORE", &cfg.Sources.Markdown.Ignore)

	// Sources: pdf
	setBoolFromEnv("MINDCLI_SOURCES_PDF_ENABLED", &cfg.Sources.PDF.Enabled)
	setCSVFromEnv("MINDCLI_SOURCES_PDF_PATHS", &cfg.Sources.PDF.Paths)

	// Sources: email
	setBoolFromEnv("MINDCLI_SOURCES_EMAIL_ENABLED", &cfg.Sources.Email.Enabled)
	setCSVFromEnv("MINDCLI_SOURCES_EMAIL_PATHS", &cfg.Sources.Email.Paths)
	setCSVFromEnv("MINDCLI_SOURCES_EMAIL_FORMATS", &cfg.Sources.Email.Formats)
	setCSVFromEnv("MINDCLI_SOURCES_EMAIL_IGNORE", &cfg.Sources.Email.Ignore)
	setBoolFromEnv("MINDCLI_SOURCES_EMAIL_MASK_SENSITIVE_PREVIEW", &cfg.Sources.Email.MaskSensitivePreview)

	// Sources: browser
	setBoolFromEnv("MINDCLI_SOURCES_BROWSER_ENABLED", &cfg.Sources.Browser.Enabled)
	setCSVFromEnv("MINDCLI_SOURCES_BROWSER_BROWSERS", &cfg.Sources.Browser.Browsers)
	setBoolFromEnv("MINDCLI_SOURCES_BROWSER_INCLUDE_CONTENT", &cfg.Sources.Browser.IncludeContent)

	// Sources: clipboard
	setBoolFromEnv("MINDCLI_SOURCES_CLIPBOARD_ENABLED", &cfg.Sources.Clipboard.Enabled)
	setIntFromEnv("MINDCLI_SOURCES_CLIPBOARD_RETENTION_DAYS", &cfg.Sources.Clipboard.RetentionDays)
	setBoolFromEnv("MINDCLI_SOURCES_CLIPBOARD_SKIP_PASSWORDS", &cfg.Sources.Clipboard.SkipPasswords)

	// Privacy
	setCSVFromEnv("MINDCLI_PRIVACY_REDACT_PATTERNS", &cfg.Privacy.RedactPatterns)
}

func setStringFromEnv(name string, dst *string) {
	if val, ok := os.LookupEnv(name); ok && strings.TrimSpace(val) != "" {
		*dst = strings.TrimSpace(val)
	}
}

func setBoolFromEnv(name string, dst *bool) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(val))
	if err == nil {
		*dst = parsed
	}
}

func setIntFromEnv(name string, dst *int) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err == nil {
		*dst = parsed
	}
}

func setFloat64FromEnv(name string, dst *float64) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
	if err == nil {
		*dst = parsed
	}
}

func setCSVFromEnv(name string, dst *[]string) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parts := strings.Split(val, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	*dst = values
}

func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}

	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}

	return path
}
