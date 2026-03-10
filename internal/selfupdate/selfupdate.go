package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"log/slog"
)

const (
	defaultRepo       = "najahiiii/xray-agent"
	checksumsAsset    = "checksums.txt"
	downloadTimeout   = 60 * time.Second
	releaseAPITimeout = 20 * time.Second
)

type Options struct {
	Repo       string
	Version    string
	Token      string
	BinaryPath string
	GOOS       string
	GOARCH     string
	Logger     *slog.Logger
}

type InstallResult struct {
	FromVersion string
	ToVersion   string
	Updated     bool
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func (o *Options) withDefaults() error {
	if o.Repo == "" {
		o.Repo = defaultRepo
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.BinaryPath == "" {
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		o.BinaryPath = binaryPath
	}
	return nil
}

func InstallOrUpdate(
	ctx context.Context,
	currentVersion string,
	opts Options,
) (*InstallResult, error) {
	if err := opts.withDefaults(); err != nil {
		return nil, err
	}

	log := opts.Logger
	release, targetVersion, err := fetchRelease(ctx, opts)
	if err != nil {
		return nil, err
	}

	if normalizeVersion(currentVersion) == normalizeVersion(targetVersion) {
		if log != nil {
			log.Info("agent already at target version", "version", currentVersion)
		}
		return &InstallResult{
			FromVersion: currentVersion,
			ToVersion:   targetVersion,
			Updated:     false,
		}, nil
	}

	assetName, err := assetNameFor(opts.GOOS, opts.GOARCH)
	if err != nil {
		return nil, err
	}
	binaryURL, checksumURL, err := pickAssetURLs(release, assetName)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "xray-agent-selfupdate-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	binaryPath := filepath.Join(tmpDir, assetName)
	checksumsPath := filepath.Join(tmpDir, checksumsAsset)

	if err := download(ctx, binaryURL, binaryPath, opts.Token); err != nil {
		return nil, fmt.Errorf("download agent binary: %w", err)
	}
	if err := download(ctx, checksumURL, checksumsPath, opts.Token); err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyChecksum(binaryPath, checksumsPath, assetName); err != nil {
		return nil, err
	}
	if err := installBinary(binaryPath, opts.BinaryPath); err != nil {
		return nil, err
	}

	if log != nil {
		log.Info(
			"agent binary updated",
			"from",
			currentVersion,
			"to",
			targetVersion,
			"path",
			opts.BinaryPath,
		)
	}

	return &InstallResult{
		FromVersion: currentVersion,
		ToVersion:   targetVersion,
		Updated:     true,
	}, nil
}

func assetNameFor(goos string, goarch string) (string, error) {
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)

	switch {
	case goos == "" || goarch == "":
		return "", errors.New("goos and goarch required")
	case goos != "linux":
		return "", fmt.Errorf("unsupported agent update platform: %s/%s", goos, goarch)
	case goarch != "amd64" && goarch != "arm64":
		return "", fmt.Errorf("unsupported agent update architecture: %s/%s", goos, goarch)
	default:
		return fmt.Sprintf("xray-agent_%s_%s", goos, goarch), nil
	}
}

func fetchRelease(ctx context.Context, opts Options) (*releaseInfo, string, error) {
	client := &http.Client{Timeout: releaseAPITimeout}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", opts.Repo)
	tag := ""
	if opts.Version != "" {
		tag = ensureTagPrefix(opts.Version)
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", opts.Repo, tag)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("github release http %d: %s", resp.StatusCode, string(body))
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, "", err
	}

	version := release.TagName
	if tag != "" {
		version = tag
	}
	return &release, version, nil
}

func pickAssetURLs(rel *releaseInfo, assetName string) (string, string, error) {
	var binaryURL string
	var checksumURL string

	for _, asset := range rel.Assets {
		switch asset.Name {
		case assetName:
			binaryURL = asset.BrowserDownloadURL
		case checksumsAsset:
			checksumURL = asset.BrowserDownloadURL
		}
	}

	if binaryURL == "" || checksumURL == "" {
		return "", "", fmt.Errorf("release assets not found for %s", assetName)
	}
	return binaryURL, checksumURL, nil
}

func download(ctx context.Context, url string, dest string, token string) error {
	client := &http.Client{Timeout: downloadTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download %s: http %d", url, resp.StatusCode)
	}

	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func verifyChecksum(binaryPath string, checksumsPath string, assetName string) error {
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}

	want := parseChecksum(checksums, assetName)
	if want == "" {
		return fmt.Errorf("checksum for %s not found", assetName)
	}

	file, err := os.Open(binaryPath)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	got := fmt.Sprintf("%x", hash.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch for %s: want %s got %s", assetName, want, got)
	}

	return nil
}

func parseChecksum(data []byte, assetName string) string {
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		fields := strings.Fields(string(bytes.TrimSpace(line)))
		if len(fields) >= 2 && fields[len(fields)-1] == assetName {
			return fields[0]
		}

		// Support "SHA256 (asset) = hash" style lines.
		text := string(bytes.TrimSpace(line))
		prefix := fmt.Sprintf("SHA256 (%s) = ", assetName)
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	return ""
}

func installBinary(downloadedBinaryPath string, targetBinaryPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetBinaryPath), 0o755); err != nil {
		return err
	}

	src, err := os.Open(downloadedBinaryPath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp(filepath.Dir(targetBinaryPath), filepath.Base(targetBinaryPath)+".new-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, src); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Chmod(0o755); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, targetBinaryPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}

func ensureTagPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

func normalizeVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}
