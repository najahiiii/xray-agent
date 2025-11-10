package agent

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/state"
	"github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"

	"log/slog"
)

type Agent struct {
	cfg   *config.Config
	log   *slog.Logger
	ctrl  *control.Client
	xray  *xray.Manager
	stats *stats.Collector
	state *state.Store
}

func New(cfg *config.Config, log *slog.Logger, ctrl *control.Client, xr *xray.Manager, collector *stats.Collector) *Agent {
	return &Agent{
		cfg:   cfg,
		log:   log,
		ctrl:  ctrl,
		xray:  xr,
		stats: collector,
		state: state.New(),
	}
}

func (a *Agent) Start(ctx context.Context) {
	go a.runStateLoop(ctx)
	go a.runStatsLoop(ctx)
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
	if a.state.IsUnchanged(ds.ConfigVersion, ds.Clients) {
		a.log.Debug("state unchanged")
		return nil
	}

	current := a.state.ClientsSnapshot()
	changed, err := a.xray.State(ctx, current, ds.Clients)
	if err != nil {
		return err
	}
	if changed {
		a.log.Info("applied clients", "version", ds.ConfigVersion, "count", len(ds.Clients))
	}
	a.state.Update(ds.ConfigVersion, ds.Clients)
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
