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
	onlineHit := false
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
		case "/api/agents/sg/online":
			onlineHit = true
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("last_seen_at")) {
				t.Fatalf("online body %s", string(body))
			}
		case "/api/agents/sg/metrics":
			metricsHit = true
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("cpu_percent")) {
				t.Fatalf("metrics body %s", string(body))
			}
		case "/api/agents/sg/heartbeat":
			hbHit = true
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte(`"ok":true`)) {
				t.Fatalf("heartbeat body %s", string(body))
			}
			if !bytes.Contains(body, []byte(`"agent_version":"v1.0.3"`)) {
				t.Fatalf("heartbeat body %s", string(body))
			}
			if !bytes.Contains(body, []byte(`"xray_core_version":"v25.10.15"`)) {
				t.Fatalf("heartbeat body %s", string(body))
			}
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

	client := NewClient(cfg, testLogger(), "v1.0.3", "v25.10.15")
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
	if err := client.PostOnlineUsers(ctx, &model.OnlineUsersPush{
		Users: []model.OnlineUserInfo{{
			Email: "user@example.com",
			IPs: []model.OnlineUserIP{{
				Address:    "203.0.113.10",
				LastSeenAt: time.Now().UTC(),
			}},
		}},
	}); err != nil {
		t.Fatalf("PostOnlineUsers: %v", err)
	}
	if err := client.PostMetrics(ctx, &model.ServerMetricPush{CPUPercent: floatPtr(10)}); err != nil {
		t.Fatalf("PostMetrics: %v", err)
	}
	if err := client.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !statsHit || !onlineHit || !hbHit || !metricsHit {
		t.Fatalf("expected stats, online, metrics, and heartbeat hits")
	}
}

func TestClientSetXrayCoreVersionUpdatesHeartbeatPayload(t *testing.T) {
	var heartbeat model.HeartbeatPush
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/sg/heartbeat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read heartbeat body: %v", err)
		}
		if err := json.Unmarshal(body, &heartbeat); err != nil {
			t.Fatalf("decode heartbeat body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = srv.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	client := NewClient(cfg, testLogger(), "v1.0.3", "v25.10.15")
	client.SetXrayCoreVersion("v26.2.6")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	if heartbeat.XrayCoreVersion != "v26.2.6" {
		t.Fatalf("unexpected xray core version: %s", heartbeat.XrayCoreVersion)
	}
}

func TestClientSetXrayCoreVersionNormalizesHeartbeatPayload(t *testing.T) {
	var heartbeat model.HeartbeatPush
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/sg/heartbeat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read heartbeat body: %v", err)
		}
		if err := json.Unmarshal(body, &heartbeat); err != nil {
			t.Fatalf("decode heartbeat body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.Control.BaseURL = srv.URL
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg"

	client := NewClient(cfg, testLogger(), "v1.0.3", "26.2.6")
	client.SetXrayCoreVersion("26.3.27")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	if heartbeat.XrayCoreVersion != "v26.3.27" {
		t.Fatalf("unexpected normalized xray core version: %s", heartbeat.XrayCoreVersion)
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
