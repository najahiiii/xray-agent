package config

import (
	"os"
	"path/filepath"
	"testing"
)

const baseYAML = `
control:
  base_url: "https://panel.example.com"
  token: "token"
  server_slug: "sg-1"
  tls_insecure: false

xray:
  binary: "/usr/bin/xray"
  api_server: "127.0.0.1:10085"
  version: ""
  inbound_tags:
    vless: "vless"
    vmess: "vmess"
    trojan: "trojan"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, baseYAML+`
intervals:
  state_sec: 0
  stats_sec: 0
  heartbeat_sec: 0
  metrics_sec: 0
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Intervals.StateSec != 15 || cfg.Intervals.StatsSec != 60 || cfg.Intervals.HeartbeatSec != 30 || cfg.Intervals.MetricsSec != 30 {
		t.Fatalf("unexpected defaults: %+v", cfg.Intervals)
	}
	if cfg.Xray.APITimeoutSec != 5 {
		t.Fatalf("expected default API timeout, got %d", cfg.Xray.APITimeoutSec)
	}
	if cfg.Xray.Version != DefaultXrayVersion {
		t.Fatalf("expected default xray version %s, got %s", DefaultXrayVersion, cfg.Xray.Version)
	}
}

func TestLoadMissingFields(t *testing.T) {
	path := writeConfig(t, `
control: {}
xray: {}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing fields")
	}
}
