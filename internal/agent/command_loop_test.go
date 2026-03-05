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
		ctrl: control.NewClient(cfg, logger, "v-test"),
	}

	originalRunner := systemctlRunner
	systemctlRunner = func(_ context.Context, _ ...string) error {
		return errors.New("restart failed")
	}
	t.Cleanup(func() {
		systemctlRunner = originalRunner
	})

	err := a.restartAgentAndAck("cmd-1", time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("restartAgentAndAck returned error: %v", err)
	}

	if ack.Status != model.AgentCommandAckFailed {
		t.Fatalf("expected FAILED status, got %s", ack.Status)
	}
	if !strings.Contains(ack.ErrorMessage, "restart failed") {
		t.Fatalf("unexpected error message: %q", ack.ErrorMessage)
	}
	if mode, ok := ack.Result["mode"].(string); !ok || mode != "restart_trigger_failed" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}

func TestRestartAgentAndAckSucceededWhenRestartTriggerAccepted(t *testing.T) {
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
		ctrl: control.NewClient(cfg, logger, "v-test"),
	}

	originalRunner := systemctlRunner
	systemctlRunner = func(_ context.Context, args ...string) error {
		if len(args) != 3 {
			t.Fatalf("unexpected args: %#v", args)
		}
		expected := []string{"restart", "--no-block", "xray-agent"}
		if args[0] != expected[0] || args[1] != expected[1] || args[2] != expected[2] {
			t.Fatalf("unexpected args: %#v", args)
		}
		return nil
	}
	t.Cleanup(func() {
		systemctlRunner = originalRunner
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
	if mode, ok := ack.Result["mode"].(string); !ok || mode != "restart_triggered" {
		t.Fatalf("unexpected mode: %#v", ack.Result["mode"])
	}
}
