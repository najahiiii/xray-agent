package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/najahiiii/xray-agent/internal/agent"
	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/logger"
	internalstats "github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"
)

func main() {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", "/etc/xray-agent/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Logging.Level)
	ctrl := control.NewClient(cfg, log)
	xm := xray.NewManager(cfg, log)
	stats := internalstats.New(cfg, log)

	agt := agent.New(cfg, log, ctrl, xm, stats)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	agt.Start(ctx)

	<-ctx.Done()
	log.Info("agent stopped")
}
