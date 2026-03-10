package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/selfupdate"
)

func TestRestartAgentAndAckFailedWhenRestartTriggerFails(t *testing.T) {
	var ack model.AgentCommandAck
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		if !strings.HasSuffix(r.URL.Path, "/api/agents/sg/commands/cmd-1/ack") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			t.Fatalf("decode ack: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = server.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		cfg:  cfg,
		log:  logger,
		ctrl: control.NewClient(cfg, logger, "v-test", "v25.10.15"),
	}

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

	if ack.Status != model.AgentCommandAckFailed {
		t.Fatalf("expected FAILED status, got %s", ack.Status)
	}
	if !strings.Contains(ack.ErrorMessage, "schedule failed") {
		t.Fatalf("unexpected error message: %q", ack.ErrorMessage)
	}
	if mode, ok := ack.Result["mode"].(string); !ok || mode != "restart_schedule_failed" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}

func TestRestartAgentAndAckSucceededWhenRestartScheduled(t *testing.T) {
	var ack model.AgentCommandAck
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			t.Fatalf("decode ack: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = server.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		cfg:  cfg,
		log:  logger,
		ctrl: control.NewClient(cfg, logger, "v-test", "v25.10.15"),
	}

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

	if ack.Status != model.AgentCommandAckSucceeded {
		t.Fatalf("expected SUCCEEDED status, got %s", ack.Status)
	}
	if ack.ErrorMessage != "" {
		t.Fatalf("expected empty error message, got %q", ack.ErrorMessage)
	}
	if mode, ok := ack.Result["mode"].(string); !ok || mode != "restart_scheduled" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}

func TestUpdateAgentAndAckFailsWithoutTargetVersion(t *testing.T) {
	var ack model.AgentCommandAck
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			t.Fatalf("decode ack: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = server.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		cfg:  cfg,
		log:  logger,
		ctrl: control.NewClient(cfg, logger, "v1.0.5", "v25.10.15"),
	}

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

	if ack.Status != model.AgentCommandAckFailed {
		t.Fatalf("expected FAILED status, got %s", ack.Status)
	}
	if mode, ok := ack.Result["mode"].(string); !ok || mode != "invalid_payload" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}

func TestUpdateAgentAndAckTriggersRestartAfterInstall(t *testing.T) {
	var ack model.AgentCommandAck
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			t.Fatalf("decode ack: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = server.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"
	cfg.GitHub.Token = "gh-token"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Agent{
		cfg:  cfg,
		log:  logger,
		ctrl: control.NewClient(cfg, logger, "v1.0.5", "v25.10.15"),
	}

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

	if ack.Status != model.AgentCommandAckSucceeded {
		t.Fatalf("expected SUCCEEDED status, got %s", ack.Status)
	}
	if got, ok := ack.Result["target_version"].(string); !ok || got != "v1.0.6" {
		t.Fatalf("unexpected target version in result: %#v", ack.Result["target_version"])
	}
	if got, ok := ack.Result["mode"].(string); !ok || got != "update_installed_restart_scheduled" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}
