package xraycore

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	_ "embed"
	"log/slog"
)

const (
	defaultRepo        = "XTLS/Xray-core"
	defaultBinDir      = "/usr/local/bin"
	defaultConfigPath  = "/etc/xray/config.json"
	defaultServicePath = "/etc/systemd/system/xray.service"
	defaultShareDir    = "/usr/local/share/xray"
)

//go:embed assets/xray-config-sample.json
var embeddedSampleConfig []byte

//go:embed assets/xray.service
var embeddedServiceUnit []byte

type Options struct {
	// GitHub release options
	Repo string
	Arch string
	// optional tag, e.g. v1.8.24
	Version string
	// optional GitHub token
	Token string

	// Install paths
	BinDir      string
	ConfigPath  string
	ServicePath string
	ShareDir    string

	// Controls
	Logger *slog.Logger
}

type CheckResult struct {
	InstalledVersion string
	LatestVersion    string
	UpdateAvailable  bool
}

type InstallResult struct {
	FromVersion string
	ToVersion   string
	Updated     bool
}

func (o *Options) withDefaults() {
	if o.Repo == "" {
		o.Repo = defaultRepo
	}
	if o.BinDir == "" {
		o.BinDir = defaultBinDir
	}
	if o.ConfigPath == "" {
		o.ConfigPath = defaultConfigPath
	}
	if o.ServicePath == "" {
		o.ServicePath = defaultServicePath
	}
	if o.ShareDir == "" {
		o.ShareDir = defaultShareDir
	}
	if o.Arch == "" {
		o.Arch = detectArch()
	}
}

func Check(ctx context.Context, opts Options) (*CheckResult, error) {
	opts.withDefaults()
	log := opts.Logger

	installed := installedVersion(ctx)
	latest, err := fetchLatestVersion(ctx, opts)
	if err != nil {
		return nil, err
	}

	upToDate := normalizeVersion(installed) == normalizeVersion(latest)
	if log != nil {
		log.Debug("xray core check", "installed", installed, "latest", latest, "up_to_date", upToDate)
	}
	return &CheckResult{
		InstalledVersion: installed,
		LatestVersion:    latest,
		UpdateAvailable:  !upToDate,
	}, nil
}

func InstallOrUpdate(ctx context.Context, opts Options) (*InstallResult, error) {
	opts.withDefaults()
	log := opts.Logger

	installed := installedVersion(ctx)
	release, targetVersion, err := fetchRelease(ctx, opts)
	if err != nil {
		return nil, err
	}

	if normalizeVersion(installed) == normalizeVersion(targetVersion) {
		if log != nil {
			log.Info("xray core already at target version", "version", installed)
		}
		return &InstallResult{FromVersion: installed, ToVersion: targetVersion, Updated: false}, nil
	}

	if log != nil {
		log.Info("installing xray core", "from", installed, "to", targetVersion, "arch", opts.Arch)
	}

	tmpDir, err := os.MkdirTemp("", "xraycore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	zipURL, dgstURL, err := pickAssetURLs(release, opts.Arch)
	if err != nil {
		return nil, err
	}

	zipPath := filepath.Join(tmpDir, "xray.zip")
	dgstPath := filepath.Join(tmpDir, "xray.zip.dgst")

	if err := download(ctx, zipURL, zipPath, opts.Token); err != nil {
		return nil, fmt.Errorf("download zip: %w", err)
	}
	if err := download(ctx, dgstURL, dgstPath, opts.Token); err != nil {
		return nil, fmt.Errorf("download dgst: %w", err)
	}
	if err := verifySHA256(zipPath, dgstPath); err != nil {
		return nil, err
	}

	unzipDir := filepath.Join(tmpDir, "unzipped")
	if err := unzip(zipPath, unzipDir); err != nil {
		return nil, fmt.Errorf("unzip: %w", err)
	}

	if err := createWorkDirs(opts); err != nil {
		return nil, err
	}
	if err := installBinaryAndData(unzipDir, opts); err != nil {
		return nil, err
	}
	if err := copySampleConfig(opts); err != nil {
		return nil, err
	}
	if err := testConfig(ctx, opts); err != nil {
		return nil, err
	}
	if err := installSystemdService(opts); err != nil {
		return nil, err
	}

	if log != nil {
		log.Info("xray core installed", "version", targetVersion)
	}
	return &InstallResult{FromVersion: installed, ToVersion: targetVersion, Updated: true}, nil
}

func detectArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "linux-64"
	case "arm64":
		return "linux-arm64-v8a"
	case "arm":
		return "linux-arm32-v7a"
	default:
		return runtime.GOARCH
	}
}

func fetchLatestVersion(ctx context.Context, opts Options) (string, error) {
	release, version, err := fetchRelease(ctx, opts)
	if err != nil {
		return "", err
	}
	if version == "" && release.TagName != "" {
		return release.TagName, nil
	}
	return version, nil
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func fetchRelease(ctx context.Context, opts Options) (*releaseInfo, string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", opts.Repo)
	if opts.Version != "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", opts.Repo, opts.Version)
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
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("github release http %d: %s", resp.StatusCode, string(b))
	}

	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, "", err
	}
	version := rel.TagName
	if opts.Version != "" {
		version = opts.Version
	}
	return &rel, version, nil
}

func pickAssetURLs(rel *releaseInfo, arch string) (zipURL, dgstURL string, err error) {
	zipPattern := fmt.Sprintf("^Xray-%s\\.zip$", arch)
	dgstPattern := fmt.Sprintf("^Xray-%s\\.zip\\.dgst$", arch)

	for _, a := range rel.Assets {
		switch {
		case regexp.MustCompile(zipPattern).MatchString(a.Name):
			zipURL = a.BrowserDownloadURL
		case regexp.MustCompile(dgstPattern).MatchString(a.Name):
			dgstURL = a.BrowserDownloadURL
		}
	}
	if zipURL == "" || dgstURL == "" {
		return "", "", fmt.Errorf("asset not found for arch=%s", arch)
	}
	return zipURL, dgstURL, nil
}

func download(ctx context.Context, url, dest, token string) error {
	client := &http.Client{Timeout: 60 * time.Second}
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
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifySHA256(zipPath, dgstPath string) error {
	dgstBytes, err := os.ReadFile(dgstPath)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?i)\b([a-f0-9]{64})\b`)
	m := re.FindSubmatch(dgstBytes)
	if len(m) < 2 {
		return errors.New("sha256 not found in dgst file")
	}
	want := string(m[1])

	file, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch: want %s got %s", want, got)
	}
	return nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	for _, f := range r.File {
		outPath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			w.Close()
			return err
		}
		rc.Close()
		w.Close()
	}
	return nil
}

func createWorkDirs(opts Options) error {
	for _, dir := range []string{"/etc/xray", "/var/log/xray", "/var/lib/xray", opts.ShareDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func installBinaryAndData(unzipDir string, opts Options) error {
	src := filepath.Join(unzipDir, "xray")
	dest := filepath.Join(opts.BinDir, "xray")
	if err := os.MkdirAll(opts.BinDir, 0o755); err != nil {
		return err
	}
	if err := copyFile(src, dest, 0o755); err != nil {
		return err
	}

	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		srcPath := filepath.Join(unzipDir, name)
		if _, err := os.Stat(srcPath); err == nil {
			destPath := filepath.Join(opts.ShareDir, name)
			if err := copyFile(srcPath, destPath, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func copySampleConfig(opts Options) error {
	if _, err := os.Stat(opts.ConfigPath); err == nil {
		return nil
	}
	return writeBytes(opts.ConfigPath, embeddedSampleConfig, 0o644)
}

func installSystemdService(opts Options) error {
	if err := writeBytes(opts.ServicePath, embeddedServiceUnit, 0o644); err != nil {
		return err
	}
	if err := runCmd(exec.Command("systemctl", "daemon-reload")); err != nil {
		return err
	}
	return runCmd(exec.Command("systemctl", "enable", "--now", "xray"))
}

func testConfig(ctx context.Context, opts Options) error {
	cmd := exec.CommandContext(ctx, filepath.Join(opts.BinDir, "xray"), "-test", "-config", opts.ConfigPath)
	return runCmd(cmd)
}

func runCmd(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dest string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeBytes(dest, data, perm)
}

func writeBytes(dest string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, perm)
}

func installedVersion(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "xray", "-version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) >= 2 {
		return fields[1]
	}
	return ""
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
