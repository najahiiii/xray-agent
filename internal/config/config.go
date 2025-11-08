package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Control struct {
		BaseURL     string `yaml:"base_url"`
		Token       string `yaml:"token"`
		ServerSlug  string `yaml:"server_slug"`
		TLSInsecure bool   `yaml:"tls_insecure"`
	} `yaml:"control"`

	Xray struct {
		Binary             string `yaml:"binary"`
		APIServer          string `yaml:"api_server"`
		APITimeoutSec      int    `yaml:"api_timeout_sec"`
		StatsResetEachPush bool   `yaml:"stats_reset_each_push"`
		InboundTags        struct {
			VLESS  string `yaml:"vless"`
			VMESS  string `yaml:"vmess"`
			TROJAN string `yaml:"trojan"`
		} `yaml:"inbound_tags"`
	} `yaml:"xray"`

	Intervals struct {
		DesiredStateSec int `yaml:"desired_state_sec"`
		StatsSec        int `yaml:"stats_sec"`
		HeartbeatSec    int `yaml:"heartbeat_sec"`
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
	if cfg.Xray.Binary == "" || cfg.Xray.APIServer == "" {
		return nil, errors.New("xray.binary/api_server required")
	}
	if cfg.Xray.InboundTags.VLESS == "" || cfg.Xray.InboundTags.VMESS == "" || cfg.Xray.InboundTags.TROJAN == "" {
		return nil, fmt.Errorf("xray.inbound_tags (vless/vmess/trojan) required")
	}
	if cfg.Intervals.DesiredStateSec == 0 {
		cfg.Intervals.DesiredStateSec = 15
	}
	if cfg.Intervals.StatsSec == 0 {
		cfg.Intervals.StatsSec = 60
	}
	if cfg.Intervals.HeartbeatSec == 0 {
		cfg.Intervals.HeartbeatSec = 30
	}
	if cfg.Xray.APITimeoutSec <= 0 {
		cfg.Xray.APITimeoutSec = 5
	}
	return &cfg, nil
}
