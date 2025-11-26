package agentsetup

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/najahiiii/xray-agent/internal/config"

	"gopkg.in/yaml.v3"
	"log/slog"
)

const (
	defaultConfigPath  = "/etc/xray-agent/config.yaml"
	defaultServicePath = "/usr/lib/systemd/system/xray-agent.service"
)

//go:embed assets/config.yaml
var embeddedConfig []byte

//go:embed assets/xray-agent.service
var embeddedService []byte

type Options struct {
	ConfigPath  string
	ServicePath string
	Logger      *slog.Logger
}

func (o *Options) withDefaults() {
	if o.ConfigPath == "" {
		o.ConfigPath = defaultConfigPath
	}
	if o.ServicePath == "" {
		o.ServicePath = defaultServicePath
	}
}

// Install writes config (if absent) and installs/enables the systemd unit.
func Install(ctx context.Context, opts Options) error {
	opts.withDefaults()
	log := opts.Logger

	if _, err := os.Stat(opts.ConfigPath); os.IsNotExist(err) {
		if log != nil {
			log.Info("writing agent config", "path", opts.ConfigPath)
		}
		if err := writeFile(opts.ConfigPath, embeddedConfig, 0o600); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("check config: %w", err)
	} else if log != nil {
		log.Info("config already exists", "path", opts.ConfigPath)
	}

	if log != nil {
		log.Info("installing systemd unit", "path", opts.ServicePath)
	}
	if err := writeFile(opts.ServicePath, embeddedService, 0o644); err != nil {
		return fmt.Errorf("write service: %w", err)
	}

	if err := runCmd(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := runCmd(ctx, "systemctl", "enable", "--now", "xray-agent"); err != nil {
		return fmt.Errorf("systemctl enable --now xray-agent: %w", err)
	}
	if log != nil {
		log.Info("agent service installed and started")
	}
	return nil
}

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type UpdateControlOptions struct {
	ConfigPath  string
	BaseURL     string
	Token       string
	ServerSlug  string
	TLSInsecure *bool
	Logger      *slog.Logger
}

// UpdateControl updates control.* fields in the agent config. Creates the config from the embedded sample if missing.
func UpdateControl(ctx context.Context, opts UpdateControlOptions) error {
	path := opts.ConfigPath
	if path == "" {
		path = defaultConfigPath
	}
	log := opts.Logger

	if opts.BaseURL == "" && opts.Token == "" && opts.ServerSlug == "" && opts.TLSInsecure == nil {
		return fmt.Errorf("no control fields provided for update")
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if opts.BaseURL != "" {
		cfg.Control.BaseURL = opts.BaseURL
	}
	if opts.Token != "" {
		cfg.Control.Token = opts.Token
	}
	if opts.ServerSlug != "" {
		cfg.Control.ServerSlug = opts.ServerSlug
	}
	if opts.TLSInsecure != nil {
		cfg.Control.TLSInsecure = *opts.TLSInsecure
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := writeFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if log != nil {
		log.Info("updated agent config control fields", "path", path)
	}
	return nil
}

func loadConfig(path string) (*config.Config, error) {
	// If file exists, load with defaults via config.Load
	if _, err := os.Stat(path); err == nil {
		return config.Load(path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Otherwise start from embedded sample
	var cfg config.Config
	if err := yaml.Unmarshal(embeddedConfig, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal embedded config: %w", err)
	}
	// apply defaults like config.Load would
	tmpPath := filepath.Join(os.TempDir(), "xray-agent-embedded-config.yaml")
	if err := os.WriteFile(tmpPath, embeddedConfig, 0o600); err == nil {
		defer os.Remove(tmpPath)
		if loaded, err := config.Load(tmpPath); err == nil {
			return loaded, nil
		}
	}
	return &cfg, nil
}
