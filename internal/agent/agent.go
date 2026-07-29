package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/metrics"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/state"
	"github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"
	"github.com/najahiiii/xray-agent/internal/xraycore"

	"log/slog"
)

var xrayCoreChecker = xraycore.Check

type Agent struct {
	cfg     *config.Config
	log     *slog.Logger
	socket  socketControl
	xray    *xray.Manager
	stats   *stats.Collector
	metrics *metrics.Collector
	state   *state.Store
	syncMu  sync.Mutex
}

type socketControl interface {
	Run(context.Context)
	States() <-chan *model.State
	Commands() <-chan *model.AgentCommand
	AgentVersion() string
	SetXrayCoreVersion(string)
	SetConfigVersion(int64)
	QueueStatsSample(time.Time, map[string][2]int64) error
	QueueOnline(*model.OnlineUsersPush) error
	QueueMetrics(*model.ServerMetricPush) error
	QueueHeartbeat() error
	QueueStateApplied(int64) error
	QueueCommandAck(string, *model.AgentCommandAck) error
}

func New(cfg *config.Config, log *slog.Logger, socket socketControl, xr *xray.Manager, statsCollector *stats.Collector, metricsCollector *metrics.Collector) *Agent {
	return &Agent{
		cfg:     cfg,
		log:     log,
		socket:  socket,
		xray:    xr,
		stats:   statsCollector,
		metrics: metricsCollector,
		state:   state.New(),
	}
}

func (a *Agent) Start(ctx context.Context) {
	if a.socket == nil {
		a.log.Error("socket control is required")
		return
	}
	go a.socket.Run(ctx)
	go a.runStateLoop(ctx)
	go a.runCommandLoop(ctx)
	go a.runOnlineLoop(ctx)
	go a.runStatsLoop(ctx)
	go a.runMetricsLoop(ctx)
	go a.runHeartbeatLoop(ctx)
	go a.runCoreUpdateLoop(ctx)
}

func (a *Agent) runStateLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case desired := <-a.socket.States():
			if desired == nil {
				continue
			}
			if err := a.applyDesiredState(ctx, desired, false); err != nil {
				a.log.Warn("socket state apply", "version", desired.ConfigVersion, "err", err)
				continue
			}
			if err := a.socket.QueueStateApplied(desired.ConfigVersion); err != nil {
				a.log.Warn("queue state acknowledgment", "version", desired.ConfigVersion, "err", err)
			}
		}
	}
}

func (a *Agent) syncStateAfterRuntimeReset(ctx context.Context) error {
	desired := a.state.DesiredStateSnapshot()
	return a.applyDesiredState(ctx, &desired, true)
}

func (a *Agent) applyDesiredState(ctx context.Context, ds *model.State, assumeEmptyRuntime bool) error {
	if ds == nil {
		return nil
	}

	a.syncMu.Lock()
	defer a.syncMu.Unlock()

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
	if a.socket != nil {
		a.socket.SetConfigVersion(ds.ConfigVersion)
	}
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
			a.collectAndQueueStats(ctx, emails)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) collectAndQueueStats(ctx context.Context, emails []string) {
	statsMap, err := a.stats.QueryUserBytes(ctx, emails)
	if err != nil {
		a.log.Warn("stats query", "err", err)
		return
	}
	if err := a.socket.QueueStatsSample(time.Now().UTC(), statsMap); err != nil {
		a.log.Warn("queue stats", "err", err)
		return
	}
	a.log.Debug("queued cumulative stats sample", "count", len(statsMap))
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
			if err := a.socket.QueueOnline(payload); err != nil {
				a.log.Warn("queue online users", "err", err)
			} else {
				a.log.Debug("queued online users", "count", len(payload.Users))
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
		if err := a.socket.QueueHeartbeat(); err != nil {
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
			if err := a.socket.QueueMetrics(sample); err != nil {
				a.log.Warn("queue metrics", "err", err)
			} else {
				a.log.Debug("queued metrics")
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) runCoreUpdateLoop(ctx context.Context) {
	intv := time.Duration(a.cfg.Intervals.CoreCheckSec) * time.Second
	if intv == 0 {
		intv = time.Duration(config.DefaultCoreCheckIntervalSec) * time.Second
	}
	if intv < 0 {
		return
	}

	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	var (
		lastInstalled       string
		lastLatest          string
		lastUpdateAvailable bool
		hasLastResult       bool
	)

	for {
		res, err := a.checkCoreUpdateOnce(ctx)
		if err != nil {
			a.log.Warn("xray-core update check failed", "err", err)
		} else if res != nil {
			if res.InstalledVersion != "" {
				a.setXrayCoreVersion(res.InstalledVersion)
			}

			if !hasLastResult ||
				lastInstalled != res.InstalledVersion ||
				lastLatest != res.LatestVersion ||
				lastUpdateAvailable != res.UpdateAvailable {
				if res.UpdateAvailable {
					a.log.Info("xray-core update available", "installed", res.InstalledVersion, "latest", res.LatestVersion)
				} else {
					a.log.Debug("xray-core up-to-date", "installed", res.InstalledVersion, "latest", res.LatestVersion)
				}

				lastInstalled = res.InstalledVersion
				lastLatest = res.LatestVersion
				lastUpdateAvailable = res.UpdateAvailable
				hasLastResult = true
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) setXrayCoreVersion(version string) {
	if a.socket != nil {
		a.socket.SetXrayCoreVersion(version)
	}
}

func (a *Agent) checkCoreUpdateOnce(ctx context.Context) (*xraycore.CheckResult, error) {
	return xrayCoreChecker(ctx, xraycore.Options{
		Token: a.cfg.GitHub.Token,
	})
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
