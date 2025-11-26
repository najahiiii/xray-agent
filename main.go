package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"log/slog"

	"github.com/najahiiii/xray-agent/internal/agent"
	"github.com/najahiiii/xray-agent/internal/agentsetup"
	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/logger"
	"github.com/najahiiii/xray-agent/internal/metrics"
	internalStats "github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"
	"github.com/najahiiii/xray-agent/internal/xraycore"
)

func main() {
	var cfgPath string
	var coreMode string
	var coreVersion string
	var doSetup bool
	var doConfigUpdate bool
	var ctlBaseURL string
	var ctlToken string
	var ctlServerSlug string
	var ctlTLSInsecure string

	flag.StringVar(&cfgPath, "config", "/etc/xray-agent/config.yaml", "path to config.yaml")
	flag.StringVar(&coreMode, "core", "", "manage xray-core: check|install (empty to run agent)")
	flag.StringVar(&coreVersion, "core-version", "", "xray-core target version (default latest)")
	flag.BoolVar(&doSetup, "setup", false, "install agent config and systemd unit, then exit")
	flag.BoolVar(&doConfigUpdate, "update-config", false, "update agent control config and exit")
	flag.StringVar(&ctlBaseURL, "control-base-url", "", "control base URL (for update-config)")
	flag.StringVar(&ctlToken, "control-token", "", "control bearer token (for update-config)")
	flag.StringVar(&ctlServerSlug, "control-server-slug", "", "control server slug (for update-config)")
	flag.StringVar(&ctlTLSInsecure, "control-tls-insecure", "false", "control TLS insecure (true/false) (for update-config)")
	flag.Parse()

	defaultCoreVersion := coreVersion
	if defaultCoreVersion == "" {
		defaultCoreVersion = config.DefaultXrayVersion
	}

	if doConfigUpdate {
		log := logger.New("info")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		var tlsPtr *bool
		switch ctlTLSInsecure {
		case "true", "1", "yes":
			v := true
			tlsPtr = &v
		case "false", "0", "no":
			v := false
			tlsPtr = &v
		default:
			fmt.Fprintf(os.Stderr, "invalid control-tls-insecure value: %s (use true/false)\n", ctlTLSInsecure)
			os.Exit(1)
		}
		if err := agentsetup.UpdateControl(ctx, agentsetup.UpdateControlOptions{
			ConfigPath:  cfgPath,
			BaseURL:     ctlBaseURL,
			Token:       ctlToken,
			ServerSlug:  ctlServerSlug,
			TLSInsecure: tlsPtr,
			Logger:      log,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "update config failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if coreMode != "" {
		log := logger.New("info")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		opts := xraycore.Options{
			Version: defaultCoreVersion,
			Logger:  log,
		}
		switch coreMode {
		case "check":
			res, err := xraycore.Check(ctx, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xray-core check: %v\n", err)
				os.Exit(1)
			}
			log.Info("xray check", "installed", res.InstalledVersion, "latest", res.LatestVersion, "update_available", res.UpdateAvailable)
			return
		case "install", "update":
			res, err := xraycore.InstallOrUpdate(ctx, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xray-core install: %v\n", err)
				os.Exit(1)
			}
			log.Info("xray install", "from", res.FromVersion, "to", res.ToVersion, "updated", res.Updated)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown core mode: %s\n", coreMode)
			os.Exit(1)
		}
	}

	if doSetup {
		log := logger.New("info")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		if err := agentsetup.Install(ctx, agentsetup.Options{Logger: log}); err != nil {
			fmt.Fprintf(os.Stderr, "agent setup failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Logging.Level)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	targetCoreVersion := coreVersion
	if targetCoreVersion == "" {
		targetCoreVersion = cfg.Xray.Version
		if targetCoreVersion == "" {
			targetCoreVersion = config.DefaultXrayVersion
		}
	}

	ensureCore(ctx, log, targetCoreVersion)

	ctrl := control.NewClient(cfg, log)
	xm := xray.NewManager(cfg, log)
	stats := internalStats.New(cfg, log)
	metricCollector := metrics.New(log)

	agt := agent.New(cfg, log, ctrl, xm, stats, metricCollector)

	agt.Start(ctx)

	<-ctx.Done()
	log.Info("agent stopped")
}

func ensureCore(ctx context.Context, log *slog.Logger, version string) {
	if version == "" {
		version = config.DefaultXrayVersion
	}
	opts := xraycore.Options{
		Version: version,
		Logger:  log,
	}
	res, err := xraycore.Check(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "xray-core check failed: %v\n", err)
		os.Exit(1)
	}
	if res.InstalledVersion == "" {
		log.Info("installing xray-core", "target", res.LatestVersion)
		if _, err := xraycore.InstallOrUpdate(ctx, opts); err != nil {
			fmt.Fprintf(os.Stderr, "xray-core install/update failed: %v\n", err)
			os.Exit(1)
		}
	} else if res.UpdateAvailable {
		log.Info("xray-core update available", "installed", res.InstalledVersion, "latest", res.LatestVersion)
	} else {
		log.Debug("xray-core up-to-date", "version", res.InstalledVersion)
	}
}
