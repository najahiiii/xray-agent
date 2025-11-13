package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClientStateAndPosts(t *testing.T) {
	state := model.State{ConfigVersion: 42}
	statsHit := false
	hbHit := false
	metricsHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("missing auth header: %s", got)
		}
		switch r.URL.Path {
		case "/api/agents/sg/state":
			if r.Method != http.MethodGet {
				t.Fatalf("state method %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(state)
		case "/api/agents/sg/stats":
			statsHit = true
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("users")) {
				t.Fatalf("stats body %s", string(body))
			}
		case "/api/agents/sg/metrics":
			metricsHit = true
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("cpu_percent")) {
				t.Fatalf("metrics body %s", string(body))
			}
		case "/api/agents/sg/heartbeat":
			hbHit = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = srv.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	client := NewClient(cfg, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := client.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if resp.ConfigVersion != 42 {
		t.Fatalf("unexpected config version %d", resp.ConfigVersion)
	}

	if err := client.PostStats(ctx, &model.StatsPush{}); err != nil {
		t.Fatalf("PostStats: %v", err)
	}
	if err := client.PostMetrics(ctx, &model.ServerMetricPush{CPUPercent: floatPtr(10)}); err != nil {
		t.Fatalf("PostMetrics: %v", err)
	}
	if err := client.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !statsHit || !hbHit || !metricsHit {
		t.Fatalf("expected stats, metrics, and heartbeat hits")
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
