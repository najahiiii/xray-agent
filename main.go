package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	_ "embed"
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

const defaultConfigPath = "/etc/xray-agent/config.yaml"

//go:embed version
var embeddedVersion string

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return
	}

	switch args[0] {
	case "core":
		coreCommand(args[1:])
	case "setup":
		setupCommand(args[1:])
	case "update-config":
		updateConfigCommand(args[1:])
	case "run":
		runAgent(args[1:])
	case "version", "-v", "--version":
		printVersion()
	default:
		printHelp()
	}
}

func coreCommand(args []string) {
	fs := flag.NewFlagSet("core", flag.ExitOnError)
	action := fs.String("action", "check", "core action: check|install")
	version := fs.String("version", "", "target xray-core version (default internal)")
	ghTokenFlag := fs.String("github-token", "", "GitHub token (optional)")
	cfgPath := fs.String("config", defaultConfigPath, "config path (optional, to read defaults)")
	fs.Parse(args)

	log := logger.New("info")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var cfgFromFile *config.Config
	if c, err := loadConfigIfExists(*cfgPath); err == nil {
		cfgFromFile = c
	}

	targetVersion := *version
	if targetVersion == "" {
		if cfgFromFile != nil && cfgFromFile.Xray.Version != "" {
			targetVersion = cfgFromFile.Xray.Version
		} else {
			targetVersion = config.DefaultXrayVersion
		}
	}
	cfgToken := ""
	if cfgFromFile != nil {
		cfgToken = cfgFromFile.GitHub.Token
	}
	targetToken := resolveGitHubToken(*ghTokenFlag, cfgToken)

	opts := xraycore.Options{
		Version: targetVersion,
		Token:   targetToken,
		Logger:  log,
	}

	switch *action {
	case "check":
		res, err := xraycore.Check(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xray-core check: %v\n", err)
			os.Exit(1)
		}
		log.Info("xray-core check", "installed", res.InstalledVersion, "latest", res.LatestVersion, "update_available", res.UpdateAvailable)
	case "install", "update":
		res, err := xraycore.InstallOrUpdate(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xray-core install: %v\n", err)
			os.Exit(1)
		}
		log.Info("xray-core install", "from", res.FromVersion, "to", res.ToVersion, "updated", res.Updated)
	default:
		fmt.Fprintf(os.Stderr, "unknown core action: %s\n", *action)
		os.Exit(1)
	}
}

func setupCommand(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path (default /etc/xray-agent/config.yaml)")
	servicePath := fs.String("service", "", "systemd service path (default /usr/lib/systemd/system/xray-agent.service)")
	binPath := fs.String("bin", "", "binary install path (default /usr/local/bin/xray-agent)")
	ghToken := fs.String("github-token", "", "GitHub token to save into config (optional)")
	ctlBase := fs.String("control-base-url", "", "control base URL (optional)")
	ctlToken := fs.String("control-token", "", "control bearer token (optional)")
	ctlSlug := fs.String("control-server-slug", "", "control server slug (optional)")
	ctlTLS := fs.String("control-tls-insecure", "", "control TLS insecure (true/false, optional)")
	fs.Parse(args)

	tlsPtr, err := parseBool(*ctlTLS, "control-tls-insecure")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log := logger.New("info")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts := agentsetup.Options{
		ConfigPath:  *cfgPath,
		ServicePath: *servicePath,
		BinPath:     *binPath,
		GitHubToken: *ghToken,
		BaseURL:     *ctlBase,
		Token:       *ctlToken,
		ServerSlug:  *ctlSlug,
		TLSInsecure: tlsPtr,
		Logger:      log,
	}
	if err := agentsetup.Install(ctx, opts); err != nil {
		fmt.Fprintf(os.Stderr, "agent setup failed: %v\n", err)
		os.Exit(1)
	}
}

func updateConfigCommand(args []string) {
	fs := flag.NewFlagSet("update-config", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "config path")
	ctlBase := fs.String("control-base-url", "", "control base URL")
	ctlToken := fs.String("control-token", "", "control bearer token")
	ctlSlug := fs.String("control-server-slug", "", "control server slug")
	ctlTLS := fs.String("control-tls-insecure", "", "control TLS insecure (true/false)")
	ghToken := fs.String("github-token", "", "GitHub token to persist (optional)")
	restart := fs.Bool("restart", true, "restart xray-agent service after update")
	fs.Parse(args)

	tlsPtr, err := parseBool(*ctlTLS, "control-tls-insecure")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log := logger.New("info")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	err = agentsetup.UpdateControl(ctx, agentsetup.UpdateControlOptions{
		ConfigPath:  *cfgPath,
		BaseURL:     *ctlBase,
		Token:       *ctlToken,
		ServerSlug:  *ctlSlug,
		TLSInsecure: tlsPtr,
		GitHubToken: *ghToken,
		Logger:      log,
		Restart:     *restart,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "update config failed: %v\n", err)
		os.Exit(1)
	}
}

func runAgent(args []string) {
	runAgentArgs(args)
}

func runAgentArgs(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	coreVersionFlag := fs.String("core-version", "", "xray-core target version (default config/default)")
	ghTokenFlag := fs.String("github-token", "", "GitHub token for core downloads (optional)")
	fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Logging.Level)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	targetCoreVersion := *coreVersionFlag
	if targetCoreVersion == "" {
		targetCoreVersion = cfg.Xray.Version
		if targetCoreVersion == "" {
			targetCoreVersion = config.DefaultXrayVersion
		}
	}
	targetGitHubToken := resolveGitHubToken(*ghTokenFlag, cfg.GitHub.Token)

	ensureCore(ctx, log, targetCoreVersion, targetGitHubToken)

	ctrl := control.NewClient(cfg, log)
	xm := xray.NewManager(cfg, log)
	stats := internalStats.New(cfg, log)
	metricCollector := metrics.New(log)

	agt := agent.New(cfg, log, ctrl, xm, stats, metricCollector)
	agt.Start(ctx)

	<-ctx.Done()
	log.Info("agent stopped")
}

func ensureCore(ctx context.Context, log *slog.Logger, version string, ghToken string) {
	if version == "" {
		version = config.DefaultXrayVersion
	}
	opts := xraycore.Options{
		Version: version,
		Logger:  log,
		Token:   ghToken,
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

func resolveGitHubToken(flagVal string, cfgVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	return os.Getenv("GITHUB_TOKEN")
}

func loadConfigIfExists(path string) (*config.Config, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return config.Load(path)
}

func printHelp() {
	fmt.Println("Usage: xray-agent <subcommand> [options]")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  run            Start the agent (default config path /etc/xray-agent/config.yaml)")
	fmt.Println("  setup          Install config/binary/systemd unit")
	fmt.Println("  update-config  Update control/github config and restart agent")
	fmt.Println("  core           Manage xray-core (check/install)")
	fmt.Println("  version        Show agent version and commit")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  xray-agent run --config /etc/xray-agent/config.yaml")
	fmt.Println("  xray-agent setup --control-base-url https://panel --control-token TOKEN --control-server-slug slug --github-token ghp_xxx")
	fmt.Println("  xray-agent update-config --control-base-url https://panel --control-token TOKEN --control-server-slug slug")
	fmt.Println("  xray-agent core --action install --version v25.10.15")
	fmt.Println()
	printVersion()
}

func printVersion() {
	fmt.Printf("xray-agent %s (commit %s)\n", strings.TrimSpace(embeddedVersion), buildCommit())
	fmt.Println()
	fmt.Println("Copyright (C) 2025 Ahmad Thoriq Najahi <me@najahi.dev>.")
	fmt.Println("License GPLv3+: GNU GPL version 3 or later <http://gnu.org/licenses/gpl.html>")
}

func buildCommit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "unknown"
}

func parseBool(value string, field string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	switch strings.ToLower(value) {
	case "true", "1", "yes":
		v := true
		return &v, nil
	case "false", "0", "no":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid %s value: %s (use true/false)", field, value)
	}
}
