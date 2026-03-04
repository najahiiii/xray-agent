package agentsetup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/najahiiii/xray-agent/internal/config"
)

func TestOptionsWithDefaults(t *testing.T) {
	opts := Options{}
	opts.withDefaults()

	if opts.ConfigPath != defaultConfigPath {
		t.Fatalf("ConfigPath = %q, want %q", opts.ConfigPath, defaultConfigPath)
	}
	if opts.ServicePath != defaultServicePath {
		t.Fatalf("ServicePath = %q, want %q", opts.ServicePath, defaultServicePath)
	}
	if opts.BinPath != defaultBinPath {
		t.Fatalf("BinPath = %q, want %q", opts.BinPath, defaultBinPath)
	}
}

func TestApplyOptionalFields(t *testing.T) {
	cfg := &config.Config{}
	cfg.Control.BaseURL = "https://old.example.com"
	cfg.Control.Token = "old-token"
	cfg.Control.ServerSlug = "old-slug"
	cfg.Control.TLSInsecure = false
	cfg.GitHub.Token = "old-gh"

	insecure := true
	opts := Options{
		GitHubToken: "new-gh",
		BaseURL:     "https://new.example.com",
		Token:       "new-token",
		ServerSlug:  "new-slug",
		TLSInsecure: &insecure,
	}

	applyOptionalFields(cfg, opts)

	if cfg.GitHub.Token != "new-gh" {
		t.Fatalf("GitHub.Token = %q, want %q", cfg.GitHub.Token, "new-gh")
	}
	if cfg.Control.BaseURL != "https://new.example.com" {
		t.Fatalf("Control.BaseURL = %q, want %q", cfg.Control.BaseURL, "https://new.example.com")
	}
	if cfg.Control.Token != "new-token" {
		t.Fatalf("Control.Token = %q, want %q", cfg.Control.Token, "new-token")
	}
	if cfg.Control.ServerSlug != "new-slug" {
		t.Fatalf("Control.ServerSlug = %q, want %q", cfg.Control.ServerSlug, "new-slug")
	}
	if !cfg.Control.TLSInsecure {
		t.Fatal("Control.TLSInsecure = false, want true")
	}
}

func TestApplyOptionalFieldsDoesNotOverrideEmptyValues(t *testing.T) {
	cfg := &config.Config{}
	cfg.Control.BaseURL = "https://existing.example.com"
	cfg.Control.Token = "existing-token"
	cfg.Control.ServerSlug = "existing-slug"
	cfg.Control.TLSInsecure = true
	cfg.GitHub.Token = "existing-gh"

	applyOptionalFields(cfg, Options{})

	if cfg.GitHub.Token != "existing-gh" {
		t.Fatalf("GitHub.Token changed unexpectedly: %q", cfg.GitHub.Token)
	}
	if cfg.Control.BaseURL != "https://existing.example.com" {
		t.Fatalf("Control.BaseURL changed unexpectedly: %q", cfg.Control.BaseURL)
	}
	if cfg.Control.Token != "existing-token" {
		t.Fatalf("Control.Token changed unexpectedly: %q", cfg.Control.Token)
	}
	if cfg.Control.ServerSlug != "existing-slug" {
		t.Fatalf("Control.ServerSlug changed unexpectedly: %q", cfg.Control.ServerSlug)
	}
	if !cfg.Control.TLSInsecure {
		t.Fatal("Control.TLSInsecure changed unexpectedly")
	}
}

func TestWriteFileCreatesParentsAndWritesContent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "dir", "config.yaml")
	data := []byte("hello-agent")

	if err := writeFile(target, data, 0o600); err != nil {
		t.Fatalf("writeFile() error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("file content = %q, want %q", string(got), string(data))
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want %o", info.Mode().Perm(), 0o600)
	}
}

func TestLoadConfigMissingFileUsesEmbeddedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-config.yaml")

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("loadConfig() returned nil config")
	}
	if cfg.Control.BaseURL == "" || cfg.Control.Token == "" || cfg.Control.ServerSlug == "" {
		t.Fatalf("embedded control config not loaded: %+v", cfg.Control)
	}
	if cfg.Xray.APIServer == "" {
		t.Fatal("embedded xray api_server not loaded")
	}
}
