package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	DefaultXrayVersion          = "v25.10.15"
	DefaultStateIntervalSec     = 15
	DefaultStatsIntervalSec     = 60
	DefaultHeartbeatIntervalSec = 30
	DefaultMetricsIntervalSec   = 30
	DefaultAPITimeoutSec        = 5
)

type Config struct {
	Control struct {
		BaseURL     string `yaml:"base_url"`
		Token       string `yaml:"token"`
		ServerSlug  string `yaml:"server_slug"`
		TLSInsecure bool   `yaml:"tls_insecure"`
	} `yaml:"control"`

	Xray struct {
		Version            string `yaml:"version"`
		APIServer          string `yaml:"api_server"`
		APITimeoutSec      int    `yaml:"api_timeout_sec"`
		StatsResetEachPush bool   `yaml:"stats_reset_each_push"`
		InboundTags        struct {
			VLESS  string `yaml:"vless"`
			VMESS  string `yaml:"vmess"`
			TROJAN string `yaml:"trojan"`
		} `yaml:"inbound_tags"`
	} `yaml:"xray"`

	GitHub struct {
		Token string `yaml:"token"`
	} `yaml:"github"`

	Intervals struct {
		StateSec     int `yaml:"state_sec"`
		StatsSec     int `yaml:"stats_sec"`
		HeartbeatSec int `yaml:"heartbeat_sec"`
		MetricsSec   int `yaml:"metrics_sec"`
	} `yaml:"intervals"`

	Logging struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.Control.BaseURL == "" || cfg.Control.Token == "" || cfg.Control.ServerSlug == "" {
		return nil, errors.New("control.base_url/token/server_slug required")
	}
	if cfg.Xray.APIServer == "" {
		return nil, errors.New("xray.api_server required")
	}
	if cfg.Xray.InboundTags.VLESS == "" || cfg.Xray.InboundTags.VMESS == "" || cfg.Xray.InboundTags.TROJAN == "" {
		return nil, fmt.Errorf("xray.inbound_tags (vless/vmess/trojan) required")
	}
	if cfg.Intervals.StateSec == 0 {
		cfg.Intervals.StateSec = DefaultStateIntervalSec
	}
	if cfg.Intervals.StatsSec == 0 {
		cfg.Intervals.StatsSec = DefaultStatsIntervalSec
	}
	if cfg.Intervals.HeartbeatSec == 0 {
		cfg.Intervals.HeartbeatSec = DefaultHeartbeatIntervalSec
	}
	if cfg.Intervals.MetricsSec == 0 {
		cfg.Intervals.MetricsSec = DefaultMetricsIntervalSec
	}
	if cfg.Xray.APITimeoutSec <= 0 {
		cfg.Xray.APITimeoutSec = DefaultAPITimeoutSec
	}
	if cfg.Xray.Version == "" {
		cfg.Xray.Version = DefaultXrayVersion
	}
	return &cfg, nil
}
