package xraycore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionsWithDefaults(t *testing.T) {
	opts := Options{}
	opts.withDefaults()

	if opts.Repo != defaultRepo {
		t.Fatalf("Repo = %q, want %q", opts.Repo, defaultRepo)
	}
	if opts.BinDir != defaultBinDir {
		t.Fatalf("BinDir = %q, want %q", opts.BinDir, defaultBinDir)
	}
	if opts.ConfigPath != defaultConfigPath {
		t.Fatalf("ConfigPath = %q, want %q", opts.ConfigPath, defaultConfigPath)
	}
	if opts.ServicePath != defaultServicePath {
		t.Fatalf("ServicePath = %q, want %q", opts.ServicePath, defaultServicePath)
	}
	if opts.ShareDir != defaultShareDir {
		t.Fatalf("ShareDir = %q, want %q", opts.ShareDir, defaultShareDir)
	}
	if strings.TrimSpace(opts.Arch) == "" {
		t.Fatal("Arch should be set by withDefaults()")
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "v1.8.0", want: "1.8.0"},
		{in: "  v1.8.0  ", want: "1.8.0"},
		{in: "1.8.0", want: "1.8.0"},
		{in: "", want: ""},
	}

	for _, tc := range cases {
		got := normalizeVersion(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEnsureTagPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "1.8.0", want: "v1.8.0"},
		{in: "v1.8.0", want: "v1.8.0"},
		{in: " 1.8.0 ", want: "v1.8.0"},
		{in: "", want: ""},
	}

	for _, tc := range cases {
		got := ensureTagPrefix(tc.in)
		if got != tc.want {
			t.Fatalf("ensureTagPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPickAssetURLs(t *testing.T) {
	rel := &releaseInfo{
		Assets: []releaseAsset{
			{Name: "Xray-linux-64.zip", BrowserDownloadURL: "https://example.com/xray.zip"},
			{Name: "Xray-linux-64.zip.dgst", BrowserDownloadURL: "https://example.com/xray.zip.dgst"},
			{Name: "Xray-linux-arm64-v8a.zip", BrowserDownloadURL: "https://example.com/other.zip"},
		},
	}

	zipURL, dgstURL, err := pickAssetURLs(rel, "linux-64")
	if err != nil {
		t.Fatalf("pickAssetURLs() error = %v", err)
	}
	if zipURL != "https://example.com/xray.zip" {
		t.Fatalf("zipURL = %q, want %q", zipURL, "https://example.com/xray.zip")
	}
	if dgstURL != "https://example.com/xray.zip.dgst" {
		t.Fatalf("dgstURL = %q, want %q", dgstURL, "https://example.com/xray.zip.dgst")
	}
}

func TestPickAssetURLsMissingAsset(t *testing.T) {
	rel := &releaseInfo{
		Assets: []releaseAsset{
			{Name: "Xray-linux-64.zip", BrowserDownloadURL: "https://example.com/xray.zip"},
		},
	}

	if _, _, err := pickAssetURLs(rel, "linux-64"); err == nil {
		t.Fatal("pickAssetURLs() expected error for missing dgst asset")
	}
}

func TestVerifySHA256(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "xray.zip")
	dgstPath := filepath.Join(tmpDir, "xray.zip.dgst")

	content := []byte("test-xray-zip-content")
	if err := os.WriteFile(zipPath, content, 0o600); err != nil {
		t.Fatalf("WriteFile(zip) error = %v", err)
	}

	sum := sha256.Sum256(content)
	dgst := fmt.Sprintf("SHA256 (Xray-linux-64.zip) = %x\n", sum)
	if err := os.WriteFile(dgstPath, []byte(dgst), 0o600); err != nil {
		t.Fatalf("WriteFile(dgst) error = %v", err)
	}

	if err := verifySHA256(zipPath, dgstPath); err != nil {
		t.Fatalf("verifySHA256() error = %v", err)
	}
}

func TestVerifySHA256Mismatch(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "xray.zip")
	dgstPath := filepath.Join(tmpDir, "xray.zip.dgst")

	if err := os.WriteFile(zipPath, []byte("actual-content"), 0o600); err != nil {
		t.Fatalf("WriteFile(zip) error = %v", err)
	}
	if err := os.WriteFile(dgstPath, []byte("SHA256 (Xray-linux-64.zip) = 0000000000000000000000000000000000000000000000000000000000000000"), 0o600); err != nil {
		t.Fatalf("WriteFile(dgst) error = %v", err)
	}

	err := verifySHA256(zipPath, dgstPath)
	if err == nil {
		t.Fatal("verifySHA256() expected mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("verifySHA256() error = %v, want mismatch message", err)
	}
}
