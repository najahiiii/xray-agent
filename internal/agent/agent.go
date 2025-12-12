package agent

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/metrics"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/state"
	"github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"

	"log/slog"
)

type Agent struct {
	cfg     *config.Config
	log     *slog.Logger
	ctrl    *control.Client
	xray    *xray.Manager
	stats   *stats.Collector
	metrics *metrics.Collector
	state   *state.Store
}

func New(cfg *config.Config, log *slog.Logger, ctrl *control.Client, xr *xray.Manager, statsCollector *stats.Collector, metricsCollector *metrics.Collector) *Agent {
	return &Agent{
		cfg:     cfg,
		log:     log,
		ctrl:    ctrl,
		xray:    xr,
		stats:   statsCollector,
		metrics: metricsCollector,
		state:   state.New(),
	}
}

func (a *Agent) Start(ctx context.Context) {
	go a.runStateLoop(ctx)
	go a.runStatsLoop(ctx)
	go a.runMetricsLoop(ctx)
	go a.runHeartbeatLoop(ctx)
}

func (a *Agent) runStateLoop(ctx context.Context) {
	intv := time.Duration(a.cfg.Intervals.StateSec) * time.Second
	if intv <= 0 {
		intv = 15 * time.Second
	}
	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		if err := a.syncStateOnce(ctx); err != nil {
			a.log.Warn("state-sync", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) syncStateOnce(ctx context.Context) error {
	ds, err := a.ctrl.GetState(ctx)
	if err != nil {
		return err
	}
	if a.state.IsUnchanged(ds.ConfigVersion, ds.Clients, ds.Routes) {
		a.log.Debug("state unchanged")
		return nil
	}

	current := a.state.ClientsSnapshot()
	currentRoutes := a.state.RoutesSnapshot()
	changed, err := a.xray.State(ctx, current, ds.Clients, currentRoutes, ds.Routes)
	if err != nil {
		return err
	}
	if changed {
		a.log.Info("applied clients/routes", "version", ds.ConfigVersion, "clients", len(ds.Clients), "routes", len(ds.Routes))
	}
	a.state.Update(ds.ConfigVersion, ds.Clients, ds.Routes)
	return nil
}

func (a *Agent) runStatsLoop(ctx context.Context) {
	intv := time.Duration(a.cfg.Intervals.StatsSec) * time.Second
	if intv <= 0 {
		intv = 60 * time.Second
	}
	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		emails := a.state.Emails()
		if len(emails) > 0 {
			slices.Sort(emails)
			if statsMap, err := a.stats.QueryUserBytes(ctx, emails); err != nil {
				a.log.Warn("stats query", "err", err)
			} else {
				users := make([]model.UserUsage, 0, len(statsMap))
				for _, email := range emails {
					if usage, ok := statsMap[email]; ok {
						lower := strings.ToLower(email)
						users = append(users, model.UserUsage{Email: lower, Uplink: usage[0], Downlink: usage[1]})
						a.log.Debug("usage sample", "email", lower, "uplink", usage[0], "downlink", usage[1])
					}
				}
				if len(users) > 0 {
					payload := &model.StatsPush{ServerTime: time.Now().UTC(), Users: users}
					if err := a.ctrl.PostStats(ctx, payload); err != nil {
						a.log.Warn("post stats", "err", err)
					} else {
						a.log.Debug("posted stats", "count", len(users))
					}
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) runHeartbeatLoop(ctx context.Context) {
	intv := time.Duration(a.cfg.Intervals.HeartbeatSec) * time.Second
	if intv <= 0 {
		intv = 30 * time.Second
	}
	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		if err := a.ctrl.Heartbeat(ctx); err != nil {
			a.log.Debug("heartbeat", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) runMetricsLoop(ctx context.Context) {
	if a.metrics == nil && a.stats == nil {
		return
	}

	intv := time.Duration(a.cfg.Intervals.MetricsSec) * time.Second
	if intv <= 0 {
		intv = 30 * time.Second
	}
	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		if sample := a.collectMetricsSample(ctx); sample != nil {
			if err := a.ctrl.PostMetrics(ctx, sample); err != nil {
				a.log.Warn("post metrics", "err", err)
			} else {
				a.log.Debug("posted metrics",
					"cpu", sample.CPUPercent,
					"mem", sample.MemoryPercent,
					"up_mbps", sample.BandwidthUpMbps,
					"down_mbps", sample.BandwidthDownMbps,
					"sys_stats", sample.XraySysStats != nil,
				)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) collectMetricsSample(ctx context.Context) *model.ServerMetricPush {
	var sample *model.ServerMetricPush
	if a.metrics != nil {
		sample = a.metrics.Sample(ctx)
	}

	if sysStats := a.collectXraySysStats(ctx); sysStats != nil {
		if sample == nil {
			sample = &model.ServerMetricPush{ServerTime: time.Now().UTC()}
		}
		sample.XraySysStats = sysStats
	}
	return sample
}

func (a *Agent) collectXraySysStats(ctx context.Context) *model.XraySysStats {
	if a.stats == nil {
		return nil
	}
	stats, err := a.stats.SysStats(ctx)
	if err != nil {
		a.log.Debug("xray sys stats", "err", err)
		return nil
	}
	return stats
}
