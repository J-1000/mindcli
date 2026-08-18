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
	if cfg.Sources.HTML.Enabled || cfg.Sources.DOCX.Enabled || cfg.Sources.EPUB.Enabled ||
		cfg.Sources.Org.Enabled || cfg.Sources.Code.Enabled || cfg.Sources.PDF.OCREnabled ||
		cfg.Sources.Email.ExtractAttachments {
		t.Error("extended parsing, OCR, and attachment extraction must be opt-in")
	}
	if cfg.Sources.DOCX.MaxDecompressedBytes < cfg.Sources.DOCX.MaxFileBytes || cfg.Sources.Code.MaxFiles < 1 {
		t.Fatal("extended source bounds were not initialized")
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
			name: "invalid browser response limit",
			modify: func(c *Config) {
				c.Sources.Browser.MaxResponseBytes = 0
			},
			wantErr: true,
		},
		{
			name: "invalid browser timeout",
			modify: func(c *Config) {
				c.Sources.Browser.RequestTimeoutSeconds = 0
			},
			wantErr: true,
		},
		{
			name: "invalid browser concurrency",
			modify: func(c *Config) {
				c.Sources.Browser.FetchConcurrency = 0
			},
			wantErr: true,
		},
		{
			name: "invalid browser page limit",
			modify: func(c *Config) {
				c.Sources.Browser.MaxPages = 0
			},
			wantErr: true,
		},
		{
			name: "invalid browser retention",
			modify: func(c *Config) {
				c.Sources.Browser.RetentionDays = 0
			},
			wantErr: true,
		},
		{
			name: "invalid DOCX expansion limit",
			modify: func(c *Config) {
				c.Sources.DOCX.MaxDecompressedBytes = 0
			},
			wantErr: true,
		},
		{
			name: "invalid OCR page limit",
			modify: func(c *Config) {
				c.Sources.PDF.OCRMaxPages = 0
			},
			wantErr: true,
		},
		{
			name: "invalid attachment depth",
			modify: func(c *Config) {
				c.Sources.Email.MaxArchiveDepth = -1
			},
			wantErr: true,
		},
		{
			name: "invalid code file limit",
			modify: func(c *Config) {
				c.Sources.Code.MaxFiles = 0
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
	tmpDir := t.TempDir()
	t.Setenv("MINDCLI_CONFIG_DIR", filepath.Join(tmpDir, "mindcli"))

	err := EnsureConfigDir()
	if err != nil {
		t.Errorf("EnsureConfigDir() error = %v", err)
	}

	// Verify the directory exists
	dir, _ := ConfigDir()
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		t.Errorf("EnsureConfigDir() did not create directory: %s", dir)
	} else if err != nil {
		t.Fatalf("stat config directory: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("config directory mode = %o, want 700", got)
	}
}

func TestSaveUsesPrivatePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "mindcli")
	configPath := filepath.Join(configDir, "config.yaml")
	t.Setenv("MINDCLI_CONFIG_DIR", configDir)
	t.Setenv("MINDCLI_CONFIG_PATH", configPath)

	cfg := Default()
	cfg.Embeddings.OpenAIKey = "secret-key"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new config mode = %o, want 600", got)
	}

	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("saving existing config: %v", err)
	}
	info, err = os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("existing config mode after Save = %o, want 600", got)
	}
}

func TestConfigDataDir(t *testing.T) {
	tmpDir := t.TempDir()

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
	info, err := os.Stat(dataDir)
	if os.IsNotExist(err) {
		t.Error("DataDir() did not create the directory")
	} else if err != nil {
		t.Fatalf("stat data directory: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("data directory mode = %o, want 700", got)
	}
}

func TestCaptureInboxDefaultsExpandsAndValidates(t *testing.T) {
	cfg := Default()
	if cfg.Capture.Inbox == "" || !filepath.IsAbs(cfg.Capture.Inbox) {
		t.Fatalf("default capture inbox = %q", cfg.Capture.Inbox)
	}

	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("MINDCLI_CAPTURE_INBOX", "~/MindCLI Test Inbox")
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(loaded.Capture.Inbox, "~") || !strings.HasSuffix(loaded.Capture.Inbox, "MindCLI Test Inbox") {
		t.Fatalf("expanded capture inbox = %q", loaded.Capture.Inbox)
	}
	loaded.Capture.Inbox = " "
	if err := loaded.Validate(); err == nil || !strings.Contains(err.Error(), "capture.inbox") {
		t.Fatalf("Validate() error = %v, want capture inbox error", err)
	}
}

func TestConfigDatabasePath(t *testing.T) {
	tmpDir := t.TempDir()

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

func TestBrowserSourceDefaults(t *testing.T) {
	browser := Default().Sources.Browser
	if browser.IncludeContent {
		t.Error("browser page fetching must be disabled by default")
	}
	if browser.MaxResponseBytes != 2<<20 || browser.RequestTimeoutSeconds != 10 {
		t.Errorf("browser response bounds = %d bytes/%d seconds", browser.MaxResponseBytes, browser.RequestTimeoutSeconds)
	}
	if browser.FetchConcurrency != 4 || browser.MaxPages != 5000 || browser.RetentionDays != 365 {
		t.Errorf("browser work/retention bounds = %+v", browser)
	}
}

func TestPrivacyDefaults(t *testing.T) {
	cfg := Default()
	if len(cfg.Privacy.RedactPatterns) != 0 {
		t.Errorf("Expected empty redact_patterns by default, got %v", cfg.Privacy.RedactPatterns)
	}
}

func TestLLMModelYAMLRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

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
	tmpDir := t.TempDir()

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
	t.Setenv("MINDCLI_SOURCES_BROWSER_INCLUDE_CONTENT", "true")
	t.Setenv("MINDCLI_SOURCES_BROWSER_ALLOWED_DOMAINS", "example.com,docs.example.org")
	t.Setenv("MINDCLI_SOURCES_BROWSER_DENIED_DOMAINS", "private.example.com")
	t.Setenv("MINDCLI_SOURCES_BROWSER_MAX_RESPONSE_BYTES", "123456")
	t.Setenv("MINDCLI_SOURCES_BROWSER_REQUEST_TIMEOUT_SECONDS", "7")
	t.Setenv("MINDCLI_SOURCES_BROWSER_FETCH_CONCURRENCY", "2")
	t.Setenv("MINDCLI_SOURCES_BROWSER_MAX_PAGES", "900")
	t.Setenv("MINDCLI_SOURCES_BROWSER_RETENTION_DAYS", "45")
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
	browser := cfg.Sources.Browser
	if !browser.IncludeContent || browser.MaxResponseBytes != 123456 || browser.RequestTimeoutSeconds != 7 ||
		browser.FetchConcurrency != 2 || browser.MaxPages != 900 || browser.RetentionDays != 45 {
		t.Errorf("Sources.Browser overrides = %+v", browser)
	}
	if got := strings.Join(browser.AllowedDomains, ","); got != "example.com,docs.example.org" {
		t.Errorf("Sources.Browser.AllowedDomains = %q", got)
	}
	if got := strings.Join(browser.DeniedDomains, ","); got != "private.example.com" {
		t.Errorf("Sources.Browser.DeniedDomains = %q", got)
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
	tmpDir := t.TempDir()

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

func TestProfileNamesPathsAndDefaultsAreIsolated(t *testing.T) {
	for _, valid := range []string{"default", "work", "Personal_2", "client-a"} {
		if got, err := ValidateProfileName(valid); err != nil || got != valid {
			t.Errorf("ValidateProfileName(%q) = %q, %v", valid, got, err)
		}
	}
	for _, invalid := range []string{"", "../work", ".hidden", "work/personal", "naïve", strings.Repeat("x", MaxProfileNameRunes+1)} {
		if _, err := ValidateProfileName(invalid); err == nil {
			t.Errorf("ValidateProfileName(%q) succeeded", invalid)
		}
	}

	defaultConfig := Default()
	work, err := DefaultForProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if work.ActiveProfile != "work" || work.Storage.Path == defaultConfig.Storage.Path || work.Capture.Inbox == defaultConfig.Capture.Inbox {
		t.Fatalf("work defaults are not isolated: %+v vs %+v", work, defaultConfig)
	}
	if !strings.HasSuffix(work.Storage.Path, filepath.Join("profiles", "work")) || !strings.HasSuffix(work.Capture.Inbox, filepath.Join("MindCLI Inbox", "work")) {
		t.Fatalf("work profile paths = storage %q, inbox %q", work.Storage.Path, work.Capture.Inbox)
	}
}

func TestProfileConfigRoundTripAndSafeListing(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINDCLI_CONFIG_DIR", configDir)
	t.Setenv("MINDCLI_CONFIG_PATH", "")
	t.Setenv("MINDCLI_STORAGE_PATH", "")
	t.Setenv("MINDCLI_CAPTURE_INBOX", "")

	work, err := DefaultForProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	work.Search.ResultsLimit = 17
	work.Storage.Path = filepath.Join(t.TempDir(), "work-data")
	if err := work.SaveProfile("work"); err != nil {
		t.Fatal(err)
	}
	path, err := ConfigPathForProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(configDir, "profiles", "work.yaml") {
		t.Fatalf("work config path = %q", path)
	}
	loaded, err := LoadProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveProfile != "work" || loaded.Search.ResultsLimit != 17 || loaded.Storage.Path != work.Storage.Path {
		t.Fatalf("loaded work profile = %+v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("profile config mode = %v", info.Mode().Perm())
	}

	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.WriteFile(filepath.Join(profilesDir, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, ".hidden.yaml"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(profilesDir, "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	profiles, err := ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(profiles, ",") != "default,work" {
		t.Fatalf("safe profile list = %#v", profiles)
	}
}

func TestLoadUsesProfileEnvironment(t *testing.T) {
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("MINDCLI_PROFILE", "personal")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveProfile != "personal" || !strings.HasSuffix(cfg.Storage.Path, filepath.Join("profiles", "personal")) {
		t.Fatalf("environment-selected profile = %+v", cfg)
	}
	t.Setenv("MINDCLI_PROFILE", "../escape")
	if _, err := Load(); err == nil {
		t.Fatal("invalid environment profile loaded")
	}
}
