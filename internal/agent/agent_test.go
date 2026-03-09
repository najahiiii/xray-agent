package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type recordingHandler struct {
	handlerService.UnimplementedHandlerServiceServer
	adds    []string
	removes []string
}

func (r *recordingHandler) AlterInbound(ctx context.Context, req *handlerService.AlterInboundRequest) (*handlerService.AlterInboundResponse, error) {
	msg, err := req.Operation.GetInstance()
	if err != nil {
		return nil, err
	}
	switch op := msg.(type) {
	case *handlerService.AddUserOperation:
		r.adds = append(r.adds, op.User.Email)
	case *handlerService.RemoveUserOperation:
		r.removes = append(r.removes, op.Email)
	default:
		return nil, fmt.Errorf("unexpected op %T", op)
	}
	return &handlerService.AlterInboundResponse{}, nil
}

func startHandler(t *testing.T) (*recordingHandler, string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	rec := &recordingHandler{}
	handlerService.RegisterHandlerServiceServer(server, rec)
	go server.Serve(lis)
	return rec, lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

func newTestConfig(api string) *config.Config {
	cfg := &config.Config{}
	cfg.Control.BaseURL = "http://example"
	cfg.Control.Token = "t"
	cfg.Control.ServerSlug = "sg"
	cfg.Xray.APIServer = api
	cfg.Xray.APITimeoutSec = 1
	cfg.Xray.InboundTags.VLESS = "v"
	cfg.Xray.InboundTags.VMESS = "m"
	cfg.Xray.InboundTags.TROJAN = "t"
	cfg.Intervals.StateSec = 15
	cfg.Intervals.OnlineSec = 10
	cfg.Intervals.StatsSec = 60
	cfg.Intervals.HeartbeatSec = 30
	cfg.Intervals.MetricsSec = 30
	return cfg
}

func TestAgentSyncStateOnce(t *testing.T) {
	rec, addr, closeFn := startHandler(t)
	defer closeFn()

	cfg := newTestConfig(addr)

	stateResp := model.State{
		ConfigVersion: 1,
		Clients: []model.Client{
			{Proto: "vless", ID: "1", Email: "user@example.com"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(stateResp)
	}))
	defer srv.Close()
	cfg.Control.BaseURL = srv.URL

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctrl := control.NewClient(cfg, log, "v1.0.3", "v25.10.15")
	manager := xray.NewManager(cfg, log)
	collector := stats.New(cfg, log)

	a := New(cfg, log, ctrl, manager, collector, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := a.syncStateOnce(ctx); err != nil {
		t.Fatalf("syncStateOnce: %v", err)
	}

	if len(rec.adds) != 1 || rec.adds[0] != "user@example.com" {
		t.Fatalf("expected add, got %+v", rec.adds)
	}
	if !a.state.IsUnchanged(1, stateResp.Clients, nil) {
		t.Fatal("state store not updated")
	}
}

func TestSyncStateAfterRuntimeResetReappliesCachedClients(t *testing.T) {
	rec, addr, closeFn := startHandler(t)
	defer closeFn()

	cfg := newTestConfig(addr)

	stateResp := model.State{
		ConfigVersion: 7,
		Clients: []model.Client{
			{Proto: "vless", ID: "1", Email: "user@example.com"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(stateResp)
	}))
	defer srv.Close()
	cfg.Control.BaseURL = srv.URL

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctrl := control.NewClient(cfg, log, "v1.0.3", "v25.10.15")
	manager := xray.NewManager(cfg, log)
	collector := stats.New(cfg, log)

	a := New(cfg, log, ctrl, manager, collector, nil)
	a.state.Update(stateResp.ConfigVersion, stateResp.Clients, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := a.syncStateAfterRuntimeReset(ctx); err != nil {
		t.Fatalf("syncStateAfterRuntimeReset: %v", err)
	}

	if len(rec.adds) != 1 || rec.adds[0] != "user@example.com" {
		t.Fatalf("expected re-add after runtime reset, got %+v", rec.adds)
	}
}

func TestCollectOnlineSnapshot(t *testing.T) {
	addr, closeFn := statsTestServer(t, nil, map[string]map[string]int64{
		"User@example.com": {
			"203.0.113.10": time.Now().UTC().Unix(),
		},
	})
	defer closeFn()

	cfg := newTestConfig(addr)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	collector := stats.New(cfg, log)
	a := New(cfg, log, nil, nil, collector, nil)
	a.state.Update(1, []model.Client{{Proto: "vless", ID: "1", Email: "user@example.com"}}, nil)

	payload, err := a.collectOnlineSnapshot(context.Background())
	if err != nil {
		t.Fatalf("collectOnlineSnapshot: %v", err)
	}
	if payload == nil || len(payload.Users) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Users[0].Email != "user@example.com" {
		t.Fatalf("unexpected email: %+v", payload.Users[0])
	}
	if payload.Users[0].Proto != "vless" {
		t.Fatalf("unexpected proto: %+v", payload.Users[0])
	}
	if len(payload.Users[0].IPs) != 1 || payload.Users[0].IPs[0].Address != "203.0.113.10" {
		t.Fatalf("unexpected ips: %+v", payload.Users[0].IPs)
	}
}

func statsTestServer(t *testing.T, values map[string][2]int64, onlineIPs map[string]map[string]int64) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	statscommand.RegisterStatsServiceServer(server, &fakeAgentStatsServer{
		values:    values,
		onlineIPs: onlineIPs,
	})
	go server.Serve(lis)
	return lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

type fakeAgentStatsServer struct {
	statscommand.UnimplementedStatsServiceServer
	values    map[string][2]int64
	onlineIPs map[string]map[string]int64
}

func (f *fakeAgentStatsServer) QueryStats(ctx context.Context, req *statscommand.QueryStatsRequest) (*statscommand.QueryStatsResponse, error) {
	resp := &statscommand.QueryStatsResponse{}
	for email, usage := range f.values {
		resp.Stat = append(resp.Stat,
			&statscommand.Stat{Name: "user>>>" + email + ">>>traffic>>>uplink", Value: usage[0]},
			&statscommand.Stat{Name: "user>>>" + email + ">>>traffic>>>downlink", Value: usage[1]},
		)
	}
	return resp, nil
}

func (f *fakeAgentStatsServer) GetAllOnlineUsers(ctx context.Context, req *statscommand.GetAllOnlineUsersRequest) (*statscommand.GetAllOnlineUsersResponse, error) {
	users := make([]string, 0, len(f.onlineIPs))
	for email := range f.onlineIPs {
		users = append(users, "user>>>"+email+">>>online")
	}
	return &statscommand.GetAllOnlineUsersResponse{Users: users}, nil
}

func (f *fakeAgentStatsServer) GetStatsOnlineIpList(ctx context.Context, req *statscommand.GetStatsRequest) (*statscommand.GetStatsOnlineIpListResponse, error) {
	name := req.GetName()
	const prefix = "user>>>"
	const suffix = ">>>online"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return &statscommand.GetStatsOnlineIpListResponse{Name: name}, nil
	}
	email := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return &statscommand.GetStatsOnlineIpListResponse{
		Name: name,
		Ips:  f.onlineIPs[email],
	}, nil
}
