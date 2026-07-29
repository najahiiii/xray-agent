package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/selfupdate"
	"github.com/najahiiii/xray-agent/internal/xraycore"
)

type fakeSocketControl struct {
	agentVersion    string
	xrayCoreVersion string
	ack             model.AgentCommandAck
	heartbeatHits   int
	states          chan *model.State
	commands        chan *model.AgentCommand
}

func newFakeSocketControl(agentVersion, xrayCoreVersion string) *fakeSocketControl {
	return &fakeSocketControl{
		agentVersion:    agentVersion,
		xrayCoreVersion: xrayCoreVersion,
		states:          make(chan *model.State),
		commands:        make(chan *model.AgentCommand),
	}
}

func (f *fakeSocketControl) Run(context.Context)                                   {}
func (f *fakeSocketControl) States() <-chan *model.State                           { return f.states }
func (f *fakeSocketControl) Commands() <-chan *model.AgentCommand                  { return f.commands }
func (f *fakeSocketControl) AgentVersion() string                                  { return f.agentVersion }
func (f *fakeSocketControl) SetXrayCoreVersion(version string)                     { f.xrayCoreVersion = version }
func (f *fakeSocketControl) SetConfigVersion(int64)                                {}
func (f *fakeSocketControl) QueueStatsSample(time.Time, map[string][2]int64) error { return nil }
func (f *fakeSocketControl) QueueOnline(*model.OnlineUsersPush) error              { return nil }
func (f *fakeSocketControl) QueueMetrics(*model.ServerMetricPush) error            { return nil }
func (f *fakeSocketControl) QueueStateApplied(int64) error                         { return nil }
func (f *fakeSocketControl) QueueHeartbeat() error {
	f.heartbeatHits++
	return nil
}
func (f *fakeSocketControl) QueueCommandAck(_ string, ack *model.AgentCommandAck) error {
	f.ack = *ack
	return nil
}

func newCommandTestAgent(agentVersion, xrayCoreVersion string) (*Agent, *fakeSocketControl) {
	cfg := &config.Config{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	socket := newFakeSocketControl(agentVersion, xrayCoreVersion)
	return &Agent{cfg: cfg, log: logger, socket: socket}, socket
}

func TestRestartAgentAndAckFailedWhenRestartTriggerFails(t *testing.T) {
	a, socket := newCommandTestAgent("v-test", "v25.10.15")

	originalScheduler := agentRestartScheduler
	agentRestartScheduler = func(_ context.Context) error {
		return errors.New("schedule failed")
	}
	t.Cleanup(func() {
		agentRestartScheduler = originalScheduler
	})

	err := a.restartAgentAndAck("cmd-1", time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("restartAgentAndAck returned error: %v", err)
	}

	if socket.ack.Status != model.AgentCommandAckFailed {
		t.Fatalf("expected FAILED status, got %s", socket.ack.Status)
	}
	if !strings.Contains(socket.ack.ErrorMessage, "schedule failed") {
		t.Fatalf("unexpected error message: %q", socket.ack.ErrorMessage)
	}
	if mode, ok := socket.ack.Result["mode"].(string); !ok || mode != "restart_schedule_failed" {
		t.Fatalf("unexpected mode: %#v", socket.ack.Result["mode"])
	}
}

func TestRestartAgentAndAckSucceededWhenRestartScheduled(t *testing.T) {
	a, socket := newCommandTestAgent("v-test", "v25.10.15")

	originalScheduler := agentRestartScheduler
	agentRestartScheduler = func(_ context.Context) error {
		return nil
	}
	t.Cleanup(func() {
		agentRestartScheduler = originalScheduler
	})

	err := a.restartAgentAndAck("cmd-2", time.Date(2026, time.March, 5, 12, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("restartAgentAndAck returned error: %v", err)
	}

	if socket.ack.Status != model.AgentCommandAckSucceeded {
		t.Fatalf("expected SUCCEEDED status, got %s", socket.ack.Status)
	}
	if socket.ack.ErrorMessage != "" {
		t.Fatalf("expected empty error message, got %q", socket.ack.ErrorMessage)
	}
	if mode, ok := socket.ack.Result["mode"].(string); !ok || mode != "restart_scheduled" {
		t.Fatalf("unexpected mode: %#v", socket.ack.Result["mode"])
	}
}

func TestUpdateAgentAndAckFailsWithoutTargetVersion(t *testing.T) {
	a, socket := newCommandTestAgent("v1.0.5", "v25.10.15")

	originalRunner := systemctlRunner
	originalUpdater := agentUpdater
	systemctlRunner = func(_ context.Context, _ ...string) error {
		t.Fatal("systemctlRunner should not be called")
		return nil
	}
	agentUpdater = func(_ context.Context, _ string, _ selfupdate.Options) (*selfupdate.InstallResult, error) {
		t.Fatal("agentUpdater should not be called")
		return nil, nil
	}
	t.Cleanup(func() {
		systemctlRunner = originalRunner
		agentUpdater = originalUpdater
	})

	err := a.updateAgentAndAck("cmd-update-1", time.Date(2026, time.March, 11, 8, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatalf("updateAgentAndAck returned error: %v", err)
	}

	if socket.ack.Status != model.AgentCommandAckFailed {
		t.Fatalf("expected FAILED status, got %s", socket.ack.Status)
	}
	if mode, ok := socket.ack.Result["mode"].(string); !ok || mode != "invalid_payload" {
		t.Fatalf("unexpected mode: %#v", socket.ack.Result["mode"])
	}
}

func TestUpdateAgentAndAckTriggersRestartAfterInstall(t *testing.T) {
	a, socket := newCommandTestAgent("v1.0.5", "v25.10.15")
	a.cfg.GitHub.Token = "gh-token"

	originalScheduler := agentRestartScheduler
	originalUpdater := agentUpdater
	agentRestartScheduler = func(_ context.Context) error {
		return nil
	}
	agentUpdater = func(_ context.Context, currentVersion string, opts selfupdate.Options) (*selfupdate.InstallResult, error) {
		if currentVersion != "v1.0.5" {
			t.Fatalf("unexpected current version: %s", currentVersion)
		}
		if opts.Version != "v1.0.6" {
			t.Fatalf("unexpected target version: %s", opts.Version)
		}
		if opts.Token != "gh-token" {
			t.Fatalf("unexpected github token: %s", opts.Token)
		}
		return &selfupdate.InstallResult{
			FromVersion: "v1.0.5",
			ToVersion:   "v1.0.6",
			Updated:     true,
		}, nil
	}
	t.Cleanup(func() {
		agentRestartScheduler = originalScheduler
		agentUpdater = originalUpdater
	})

	err := a.updateAgentAndAck(
		"cmd-update-2",
		time.Date(2026, time.March, 11, 8, 5, 0, 0, time.UTC),
		map[string]any{"target_version": "1.0.6"},
	)
	if err != nil {
		t.Fatalf("updateAgentAndAck returned error: %v", err)
	}

	if socket.ack.Status != model.AgentCommandAckSucceeded {
		t.Fatalf("expected SUCCEEDED status, got %s", socket.ack.Status)
	}
	if got, ok := socket.ack.Result["target_version"].(string); !ok || got != "v1.0.6" {
		t.Fatalf("unexpected target version in result: %#v", socket.ack.Result["target_version"])
	}
	if got, ok := socket.ack.Result["mode"].(string); !ok || got != "update_installed_restart_scheduled" {
		t.Fatalf("unexpected mode: %#v", socket.ack.Result["mode"])
	}
}

func TestUpdateCoreAndAckRestartsCoreAndRefreshesHeartbeat(t *testing.T) {
	a, socket := newCommandTestAgent("v1.0.5", "v26.1.23")
	a.cfg.GitHub.Token = "gh-token"

	originalRunner := systemctlRunner
	originalUpdater := coreUpdater
	originalSyncer := coreRestartSyncer
	systemctlRunner = func(_ context.Context, args ...string) error {
		if len(args) != 2 || args[0] != "restart" || args[1] != "xray" {
			t.Fatalf("unexpected systemctl args: %v", args)
		}
		return nil
	}
	coreUpdater = func(_ context.Context, opts xraycore.Options) (*xraycore.InstallResult, error) {
		if opts.Version != "v26.2.6" {
			t.Fatalf("unexpected target version: %s", opts.Version)
		}
		if opts.Token != "gh-token" {
			t.Fatalf("unexpected github token: %s", opts.Token)
		}
		return &xraycore.InstallResult{
			FromVersion: "v26.1.23",
			ToVersion:   "v26.2.6",
			Updated:     true,
		}, nil
	}
	coreRestartSyncer = func(_ *Agent, _ context.Context) error {
		return nil
	}
	t.Cleanup(func() {
		systemctlRunner = originalRunner
		coreUpdater = originalUpdater
		coreRestartSyncer = originalSyncer
	})

	err := a.updateCoreAndAck(
		"cmd-core-1",
		time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC),
		map[string]any{"target_version": "26.2.6"},
	)
	if err != nil {
		t.Fatalf("updateCoreAndAck returned error: %v", err)
	}

	if socket.ack.Status != model.AgentCommandAckSucceeded {
		t.Fatalf("expected SUCCEEDED status, got %s", socket.ack.Status)
	}
	if got, ok := socket.ack.Result["target_version"].(string); !ok || got != "v26.2.6" {
		t.Fatalf("unexpected target version in result: %#v", socket.ack.Result["target_version"])
	}
	if got, ok := socket.ack.Result["mode"].(string); !ok || got != "update_installed_restart_completed" {
		t.Fatalf("unexpected mode: %#v", socket.ack.Result["mode"])
	}
	if socket.heartbeatHits != 1 {
		t.Fatalf("expected 1 heartbeat refresh, got %d", socket.heartbeatHits)
	}
	if socket.xrayCoreVersion != "v26.2.6" {
		t.Fatalf("unexpected xray core version in heartbeat: %s", socket.xrayCoreVersion)
	}
}
