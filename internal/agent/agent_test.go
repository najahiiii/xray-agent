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
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/control"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/stats"
	"github.com/najahiiii/xray-agent/internal/xray"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
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
	ctrl := control.NewClient(cfg, log)
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
