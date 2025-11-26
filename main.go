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

	flag.StringVar(&cfgPath, "config", "/etc/xray-agent/config.yaml", "path to config.yaml")
	flag.StringVar(&coreMode, "core", "", "manage xray-core: check|install (empty to run agent)")
	flag.StringVar(&coreVersion, "core-version", "", "xray-core target version (default latest)")
	flag.Parse()

	if coreMode != "" {
		log := logger.New("info")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		opts := xraycore.Options{
			Version: coreVersion,
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

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Logging.Level)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	ensureCore(ctx, log, coreVersion)

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
