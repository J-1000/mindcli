package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg == nil {
		t.Fatal("Default() returned nil")
	}

	// Check some default values
	if !cfg.Sources.Markdown.Enabled {
		t.Error("Expected markdown to be enabled by default")
	}

	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("Expected default provider 'ollama', got %q", cfg.Embeddings.Provider)
	}

	if cfg.Search.HybridWeight != 0.5 {
		t.Errorf("Expected default hybrid_weight 0.5, got %f", cfg.Search.HybridWeight)
	}

	if cfg.Indexing.Workers != 4 {
		t.Errorf("Expected default workers 4, got %d", cfg.Indexing.Workers)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid default config",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "invalid hybrid_weight too low",
			modify: func(c *Config) {
				c.Search.HybridWeight = -0.1
			},
			wantErr: true,
		},
		{
			name: "invalid hybrid_weight too high",
			modify: func(c *Config) {
				c.Search.HybridWeight = 1.1
			},
			wantErr: true,
		},
		{
			name: "valid hybrid_weight at boundary 0",
			modify: func(c *Config) {
				c.Search.HybridWeight = 0
			},
			wantErr: false,
		},
		{
			name: "valid hybrid_weight at boundary 1",
			modify: func(c *Config) {
				c.Search.HybridWeight = 1
			},
			wantErr: false,
		},
		{
			name: "invalid results_limit",
			modify: func(c *Config) {
				c.Search.ResultsLimit = 0
			},
			wantErr: true,
		},
		{
			name: "invalid workers",
			modify: func(c *Config) {
				c.Indexing.Workers = 0
			},
			wantErr: true,
		},
		{
			name: "invalid embeddings provider",
			modify: func(c *Config) {
				c.Embeddings.Provider = "invalid"
			},
			wantErr: true,
		},
		{
			name: "valid openai provider with key",
			modify: func(c *Config) {
				c.Embeddings.Provider = "openai"
				c.Embeddings.OpenAIKey = "sk-test"
			},
			wantErr: false,
		},
		{
			name: "openai provider missing key",
			modify: func(c *Config) {
				c.Embeddings.Provider = "openai"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigDir(t *testing.T) {
	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error = %v", err)
	}

	if dir == "" {
		t.Error("ConfigDir() returned empty string")
	}

	if !filepath.IsAbs(dir) {
		t.Errorf("ConfigDir() returned non-absolute path: %s", dir)
	}

	if filepath.Base(dir) != "mindcli" {
		t.Errorf("ConfigDir() should end with 'mindcli', got %s", filepath.Base(dir))
	}
}

func TestConfigPath(t *testing.T) {
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}

	if filepath.Base(path) != "config.yaml" {
		t.Errorf("ConfigPath() should end with 'config.yaml', got %s", filepath.Base(path))
	}
}

func TestEnsureConfigDir(t *testing.T) {
	// Save original and restore after test
	origConfigDir, _ := os.UserConfigDir()

	// Create a temp directory for testing
	tmpDir, err := os.MkdirTemp("", "mindcli-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// We can't easily override UserConfigDir, so just test that EnsureConfigDir
	// doesn't error on the real config dir
	err = EnsureConfigDir()
	if err != nil {
		t.Errorf("EnsureConfigDir() error = %v", err)
	}

	// Verify the directory exists
	dir, _ := ConfigDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("EnsureConfigDir() did not create directory: %s", dir)
	}

	_ = origConfigDir // silence unused warning
}

func TestConfigDataDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Default()
	cfg.Storage.Path = filepath.Join(tmpDir, "data")

	dataDir, err := cfg.DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	if dataDir != cfg.Storage.Path {
		t.Errorf("DataDir() = %q, want %q", dataDir, cfg.Storage.Path)
	}

	// Verify directory was created
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Error("DataDir() did not create the directory")
	}
}

func TestConfigDatabasePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Default()
	cfg.Storage.Path = filepath.Join(tmpDir, "data")

	dbPath, err := cfg.DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}

	expectedPath := filepath.Join(cfg.Storage.Path, "mindcli.db")
	if dbPath != expectedPath {
		t.Errorf("DatabasePath() = %q, want %q", dbPath, expectedPath)
	}
}

func TestMarkdownSourceDefaults(t *testing.T) {
	cfg := Default()
	md := cfg.Sources.Markdown

	// Check extensions
	expectedExts := map[string]bool{".md": true, ".txt": true}
	for _, ext := range md.Extensions {
		if !expectedExts[ext] {
			t.Errorf("Unexpected extension in defaults: %s", ext)
		}
	}

	// Check ignore patterns
	expectedIgnore := map[string]bool{
		"node_modules": true,
		".git":         true,
		".obsidian":    true,
	}
	for _, pattern := range md.Ignore {
		if !expectedIgnore[pattern] {
			t.Errorf("Unexpected ignore pattern in defaults: %s", pattern)
		}
	}
}

func TestEmbeddingsDefaults(t *testing.T) {
	cfg := Default()

	if cfg.Embeddings.Provider != "ollama" {
		t.Errorf("Expected default provider 'ollama', got %q", cfg.Embeddings.Provider)
	}

	if cfg.Embeddings.Model != "nomic-embed-text" {
		t.Errorf("Expected default model 'nomic-embed-text', got %q", cfg.Embeddings.Model)
	}

	if cfg.Embeddings.LLMModel != "llama3.2" {
		t.Errorf("Expected default llm_model 'llama3.2', got %q", cfg.Embeddings.LLMModel)
	}

	if cfg.Embeddings.OllamaURL != "http://localhost:11434" {
		t.Errorf("Expected default ollama_url 'http://localhost:11434', got %q", cfg.Embeddings.OllamaURL)
	}
}

func TestEmailSourceDefaults(t *testing.T) {
	cfg := Default()
	email := cfg.Sources.Email

	if email.MaskSensitivePreview != true {
		t.Errorf("Expected mask_sensitive_preview true, got %v", email.MaskSensitivePreview)
	}
	if len(email.Ignore) != 0 {
		t.Errorf("Expected empty email ignore list by default, got %v", email.Ignore)
	}
}

func TestPrivacyDefaults(t *testing.T) {
	cfg := Default()
	if len(cfg.Privacy.RedactPatterns) != 0 {
		t.Errorf("Expected empty redact_patterns by default, got %v", cfg.Privacy.RedactPatterns)
	}
}

func TestLLMModelYAMLRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-config-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a config with a custom LLM model
	configContent := []byte(`embeddings:
  provider: ollama
  model: nomic-embed-text
  llm_model: mistral
  ollama_url: http://localhost:11434
`)
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, configContent, 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Parse it
	cfg := Default()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	if cfg.Embeddings.LLMModel != "mistral" {
		t.Errorf("LLMModel = %q, want 'mistral'", cfg.Embeddings.LLMModel)
	}

	// Marshal back and verify it round-trips
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	cfg2 := Default()
	if err := yaml.Unmarshal(out, cfg2); err != nil {
		t.Fatalf("Failed to re-parse config: %v", err)
	}

	if cfg2.Embeddings.LLMModel != "mistral" {
		t.Errorf("After round-trip, LLMModel = %q, want 'mistral'", cfg2.Embeddings.LLMModel)
	}
}

func TestClipboardSourceDefaults(t *testing.T) {
	cfg := Default()
	clip := cfg.Sources.Clipboard

	if !clip.Enabled {
		t.Error("Expected clipboard to be enabled by default")
	}

	if clip.RetentionDays != 30 {
		t.Errorf("Expected retention_days 30, got %d", clip.RetentionDays)
	}

	if !clip.SkipPasswords {
		t.Error("Expected skip_passwords to be true by default")
	}
}

func TestLoadAppliesEnvOverrides(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-config-env-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use an isolated config file to avoid machine-specific config affecting the test.
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("search:\n  hybrid_weight: 0.2\n"), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	t.Setenv("MINDCLI_CONFIG_PATH", configPath)
	t.Setenv("MINDCLI_SEARCH_HYBRID_WEIGHT", "0.9")
	t.Setenv("MINDCLI_INDEXING_WORKERS", "8")
	t.Setenv("MINDCLI_STORAGE_PATH", filepath.Join(tmpDir, "data"))
	t.Setenv("MINDCLI_SOURCES_MARKDOWN_PATHS", "/tmp/notes,/tmp/wiki")
	t.Setenv("MINDCLI_SOURCES_EMAIL_IGNORE", "private,secret")
	t.Setenv("MINDCLI_SOURCES_EMAIL_MASK_SENSITIVE_PREVIEW", "false")
	t.Setenv("MINDCLI_PRIVACY_REDACT_PATTERNS", "token-[0-9]+,secret-[a-z]+")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Search.HybridWeight != 0.9 {
		t.Errorf("Search.HybridWeight = %v, want 0.9", cfg.Search.HybridWeight)
	}

	if cfg.Indexing.Workers != 8 {
		t.Errorf("Indexing.Workers = %d, want 8", cfg.Indexing.Workers)
	}

	wantStorage := filepath.Join(tmpDir, "data")
	if cfg.Storage.Path != wantStorage {
		t.Errorf("Storage.Path = %q, want %q", cfg.Storage.Path, wantStorage)
	}

	if len(cfg.Sources.Markdown.Paths) != 2 {
		t.Fatalf("Sources.Markdown.Paths length = %d, want 2", len(cfg.Sources.Markdown.Paths))
	}
	if cfg.Sources.Markdown.Paths[0] != "/tmp/notes" || cfg.Sources.Markdown.Paths[1] != "/tmp/wiki" {
		t.Errorf("Sources.Markdown.Paths = %#v, want [/tmp/notes /tmp/wiki]", cfg.Sources.Markdown.Paths)
	}
	if got := strings.Join(cfg.Sources.Email.Ignore, ","); got != "private,secret" {
		t.Errorf("Sources.Email.Ignore = %q, want %q", got, "private,secret")
	}
	if cfg.Sources.Email.MaskSensitivePreview {
		t.Errorf("Sources.Email.MaskSensitivePreview = true, want false")
	}
	if got := strings.Join(cfg.Privacy.RedactPatterns, ","); got != "token-[0-9]+,secret-[a-z]+" {
		t.Errorf("Privacy.RedactPatterns = %q, want %q", got, "token-[0-9]+,secret-[a-z]+")
	}
}

func TestLoadExpandsTildePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	// Point at a non-existent config file so defaults + env overrides are used.
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(t.TempDir(), "absent.yaml"))
	t.Setenv("MINDCLI_STORAGE_PATH", "~/data/mindcli")
	t.Setenv("MINDCLI_SOURCES_MARKDOWN_PATHS", "~/notes,/abs/path")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	wantStorage := filepath.Join(home, "data", "mindcli")
	if cfg.Storage.Path != wantStorage {
		t.Errorf("Storage.Path = %q, want %q", cfg.Storage.Path, wantStorage)
	}
	wantMarkdown := filepath.Join(home, "notes")
	if cfg.Sources.Markdown.Paths[0] != wantMarkdown {
		t.Errorf("Sources.Markdown.Paths[0] = %q, want %q", cfg.Sources.Markdown.Paths[0], wantMarkdown)
	}
	if cfg.Sources.Markdown.Paths[1] != "/abs/path" {
		t.Errorf("Sources.Markdown.Paths[1] = %q, want /abs/path", cfg.Sources.Markdown.Paths[1])
	}
}

func TestLoadAppliesEnvOverridesWithoutConfigFile(t *testing.T) {
	// Env overrides must apply even when no config file exists on disk.
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(t.TempDir(), "absent.yaml"))
	t.Setenv("MINDCLI_INDEXING_WORKERS", "7")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Indexing.Workers != 7 {
		t.Errorf("Indexing.Workers = %d, want 7 (env override without config file)", cfg.Indexing.Workers)
	}
}

func TestConfigPathAndDirFromEnv(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-config-path-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	customDir := filepath.Join(tmpDir, "mycfg")
	customPath := filepath.Join(customDir, "custom.yaml")

	t.Setenv("MINDCLI_CONFIG_DIR", customDir)
	t.Setenv("MINDCLI_CONFIG_PATH", customPath)

	gotDir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error = %v", err)
	}
	if gotDir != customDir {
		t.Errorf("ConfigDir() = %q, want %q", gotDir, customDir)
	}

	gotPath, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if gotPath != customPath {
		t.Errorf("ConfigPath() = %q, want %q", gotPath, customPath)
	}

	if err := EnsureConfigDir(); err != nil {
		t.Fatalf("EnsureConfigDir() error = %v", err)
	}
	if _, err := os.Stat(customDir); err != nil {
		t.Fatalf("EnsureConfigDir() did not create %q: %v", customDir, err)
	}
}
