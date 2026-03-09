package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
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
	// statsSnapshot keeps the last seen cumulative counters when StatsResetEachPush is disabled.
	statsSnapshot map[string][2]int64
	syncMu        sync.Mutex
}

func New(cfg *config.Config, log *slog.Logger, ctrl *control.Client, xr *xray.Manager, statsCollector *stats.Collector, metricsCollector *metrics.Collector) *Agent {
	return &Agent{
		cfg:           cfg,
		log:           log,
		ctrl:          ctrl,
		xray:          xr,
		stats:         statsCollector,
		metrics:       metricsCollector,
		state:         state.New(),
		statsSnapshot: map[string][2]int64{},
	}
}

func (a *Agent) Start(ctx context.Context) {
	go a.runStateLoop(ctx)
	go a.runOnlineLoop(ctx)
	go a.runStatsLoop(ctx)
	go a.runMetricsLoop(ctx)
	go a.runHeartbeatLoop(ctx)
	go a.runCommandLoop(ctx)
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
	return a.syncState(ctx, false)
}

func (a *Agent) syncStateAfterRuntimeReset(ctx context.Context) error {
	return a.syncState(ctx, true)
}

func (a *Agent) syncState(ctx context.Context, assumeEmptyRuntime bool) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	ds, err := a.ctrl.GetState(ctx)
	if err != nil {
		return err
	}

	normalizedRoutes, duplicateRouteTags := model.NormalizeRouteRules(ds.Routes)
	if len(duplicateRouteTags) > 0 {
		a.log.Warn(
			"state contains duplicate route tags; keeping last occurrence",
			"tags",
			duplicateRouteTags,
		)
	}

	if !assumeEmptyRuntime && a.state.IsUnchanged(ds.ConfigVersion, ds.Clients, normalizedRoutes) {
		a.log.Debug("state unchanged")
		return nil
	}

	current := a.state.ClientsSnapshot()
	currentRoutes := a.state.RoutesSnapshot()
	if assumeEmptyRuntime {
		current = map[string]model.Client{}
		currentRoutes = map[string]model.RouteRule{}
		if a.log != nil {
			a.log.Info(
				"forcing full state reapply after xray runtime reset",
				"version",
				ds.ConfigVersion,
				"clients",
				len(ds.Clients),
				"routes",
				len(normalizedRoutes),
			)
		}
	}

	changed, err := a.xray.State(ctx, current, ds.Clients, currentRoutes, normalizedRoutes)
	if err != nil {
		return err
	}
	if changed {
		a.log.Info("applied clients/routes", "version", ds.ConfigVersion, "clients", len(ds.Clients), "routes", len(normalizedRoutes))
	}
	a.state.Update(ds.ConfigVersion, ds.Clients, normalizedRoutes)
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
				if !a.cfg.Xray.StatsResetEachPush {
					statsMap = a.normalizeStatsDeltas(statsMap)
				} else if len(a.statsSnapshot) > 0 {
					clear(a.statsSnapshot)
				}

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

func (a *Agent) runOnlineLoop(ctx context.Context) {
	if a.stats == nil {
		return
	}

	intv := time.Duration(a.cfg.Intervals.OnlineSec) * time.Second
	if intv <= 0 {
		intv = 10 * time.Second
	}
	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		payload, err := a.collectOnlineSnapshot(ctx)
		if err != nil {
			a.log.Warn("online query", "err", err)
		} else if payload != nil {
			if err := a.ctrl.PostOnlineUsers(ctx, payload); err != nil {
				a.log.Warn("post online users", "err", err)
			} else {
				a.log.Debug("posted online users", "count", len(payload.Users))
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

func (a *Agent) collectOnlineSnapshot(ctx context.Context) (*model.OnlineUsersPush, error) {
	users, err := a.stats.OnlineUsers(ctx)
	if err != nil {
		return nil, err
	}

	clients := a.state.ClientsSnapshot()
	byEmail := make(map[string]model.Client, len(clients))
	for email, client := range clients {
		byEmail[strings.ToLower(email)] = client
	}

	for idx := range users {
		users[idx].Email = strings.ToLower(users[idx].Email)
		if client, ok := byEmail[users[idx].Email]; ok && users[idx].Proto == "" {
			users[idx].Proto = client.Proto
		}
	}

	slices.SortFunc(users, func(a, b model.OnlineUserInfo) int {
		return strings.Compare(a.Email, b.Email)
	})

	return &model.OnlineUsersPush{
		ServerTime: time.Now().UTC(),
		Users:      users,
	}, nil
}

func (a *Agent) normalizeStatsDeltas(current map[string][2]int64) map[string][2]int64 {
	if len(current) == 0 {
		clear(a.statsSnapshot)
		return current
	}

	normalized := make(map[string][2]int64, len(current))
	present := make(map[string]struct{}, len(current))

	for email, usage := range current {
		key := strings.ToLower(email)
		present[key] = struct{}{}

		prev, found := a.statsSnapshot[key]
		uplink := int64(0)
		downlink := int64(0)
		if found {
			uplink = usageCounterDelta(prev[0], usage[0])
			downlink = usageCounterDelta(prev[1], usage[1])
		}

		normalized[email] = [2]int64{uplink, downlink}
		a.statsSnapshot[key] = usage
	}

	for email := range a.statsSnapshot {
		if _, ok := present[email]; !ok {
			delete(a.statsSnapshot, email)
		}
	}

	return normalized
}

func usageCounterDelta(prev, curr int64) int64 {
	if curr <= 0 {
		return 0
	}
	if curr >= prev {
		return curr - prev
	}
	return curr
}
