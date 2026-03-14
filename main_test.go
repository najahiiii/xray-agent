package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/najahiiii/xray-agent/internal/xraycore"
)

func TestResolveGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")

	if got := resolveGitHubToken("flag-token", "cfg-token"); got != "flag-token" {
		t.Fatalf("resolveGitHubToken() with flag: got %q want %q", got, "flag-token")
	}

	if got := resolveGitHubToken("", "cfg-token"); got != "cfg-token" {
		t.Fatalf("resolveGitHubToken() with config: got %q want %q", got, "cfg-token")
	}

	if got := resolveGitHubToken("", ""); got != "env-token" {
		t.Fatalf("resolveGitHubToken() with env: got %q want %q", got, "env-token")
	}
}

func TestParseBool(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantNil   bool
		wantValue bool
		wantErr   bool
	}{
		{name: "empty", input: "", wantNil: true},
		{name: "true", input: "true", wantValue: true},
		{name: "one", input: "1", wantValue: true},
		{name: "yes", input: "yes", wantValue: true},
		{name: "false", input: "false", wantValue: false},
		{name: "zero", input: "0", wantValue: false},
		{name: "no", input: "no", wantValue: false},
		{name: "invalid", input: "maybe", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBool(tc.input, "test_field")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseBool(%q): expected error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBool(%q): unexpected error: %v", tc.input, err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("parseBool(%q): got non-nil pointer", tc.input)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseBool(%q): got nil pointer", tc.input)
			}
			if *got != tc.wantValue {
				t.Fatalf("parseBool(%q): got %v want %v", tc.input, *got, tc.wantValue)
			}
		})
	}
}

func TestLoadConfigIfExistsMissing(t *testing.T) {
	if cfg, err := loadConfigIfExists(""); err != nil || cfg != nil {
		t.Fatalf("loadConfigIfExists(empty): got cfg=%v err=%v, want nil nil", cfg, err)
	}

	missing := filepath.Join(t.TempDir(), "missing-config.yaml")
	if cfg, err := loadConfigIfExists(missing); err != nil || cfg != nil {
		t.Fatalf("loadConfigIfExists(missing): got cfg=%v err=%v, want nil nil", cfg, err)
	}
}

func TestRunCoreCommandReturnsConfigError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("control: ["), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	err := runCoreCommand([]string{"--config", cfgPath})
	if err == nil {
		t.Fatal("runCoreCommand(): expected error for invalid config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Fatalf("runCoreCommand(): got err %q, want load config context", err)
	}
}

func TestEnsureCoreSkipsInstallWhenInstalledVersionPresent(t *testing.T) {
	originalInstalledVersion := xrayCoreInstalledVersion
	originalInstaller := xrayCoreInstaller
	t.Cleanup(func() {
		xrayCoreInstalledVersion = originalInstalledVersion
		xrayCoreInstaller = originalInstaller
	})

	xrayCoreInstalledVersion = func(context.Context) string { return "v25.10.15" }
	xrayCoreInstaller = func(context.Context, xraycore.Options) (*xraycore.InstallResult, error) {
		t.Fatal("xrayCoreInstaller should not be called when xray-core is already installed")
		return nil, nil
	}

	if err := ensureCore(context.Background(), slog.New(slog.NewTextHandler(ioDiscard{}, nil)), "v25.10.15", ""); err != nil {
		t.Fatalf("ensureCore(): unexpected error: %v", err)
	}
}

func TestEnsureCoreInstallsWhenMissing(t *testing.T) {
	originalInstalledVersion := xrayCoreInstalledVersion
	originalInstaller := xrayCoreInstaller
	t.Cleanup(func() {
		xrayCoreInstalledVersion = originalInstalledVersion
		xrayCoreInstaller = originalInstaller
	})

	xrayCoreInstalledVersion = func(context.Context) string { return "" }

	var gotVersion string
	var gotToken string
	xrayCoreInstaller = func(_ context.Context, opts xraycore.Options) (*xraycore.InstallResult, error) {
		gotVersion = opts.Version
		gotToken = opts.Token
		return &xraycore.InstallResult{ToVersion: opts.Version, Updated: true}, nil
	}

	if err := ensureCore(context.Background(), slog.New(slog.NewTextHandler(ioDiscard{}, nil)), "v25.10.15", "gh-token"); err != nil {
		t.Fatalf("ensureCore(): unexpected error: %v", err)
	}
	if gotVersion != "v25.10.15" {
		t.Fatalf("ensureCore(): installer version = %q, want %q", gotVersion, "v25.10.15")
	}
	if gotToken != "gh-token" {
		t.Fatalf("ensureCore(): installer token = %q, want %q", gotToken, "gh-token")
	}
}

func TestEnsureCoreReturnsInstallError(t *testing.T) {
	originalInstalledVersion := xrayCoreInstalledVersion
	originalInstaller := xrayCoreInstaller
	t.Cleanup(func() {
		xrayCoreInstalledVersion = originalInstalledVersion
		xrayCoreInstaller = originalInstaller
	})

	xrayCoreInstalledVersion = func(context.Context) string { return "" }
	xrayCoreInstaller = func(context.Context, xraycore.Options) (*xraycore.InstallResult, error) {
		return nil, errors.New("install failed")
	}

	err := ensureCore(context.Background(), slog.New(slog.NewTextHandler(ioDiscard{}, nil)), "v25.10.15", "")
	if err == nil || !strings.Contains(err.Error(), "install failed") {
		t.Fatalf("ensureCore(): got err %v, want install failure", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
