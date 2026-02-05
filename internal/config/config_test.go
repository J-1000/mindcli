package config

import (
	"os"
	"path/filepath"
	"testing"
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
			name: "valid openai provider",
			modify: func(c *Config) {
				c.Embeddings.Provider = "openai"
			},
			wantErr: false,
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
