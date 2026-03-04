package main

import (
	"path/filepath"
	"testing"
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
