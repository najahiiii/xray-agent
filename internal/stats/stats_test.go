package stats

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type fakeStatsServer struct {
	statscommand.UnimplementedStatsServiceServer
	values    map[string][2]int64
	onlineIPs map[string]map[string]int64
}

func (f *fakeStatsServer) QueryStats(ctx context.Context, req *statscommand.QueryStatsRequest) (*statscommand.QueryStatsResponse, error) {
	resp := &statscommand.QueryStatsResponse{}
	for email, usage := range f.values {
		resp.Stat = append(resp.Stat,
			&statscommand.Stat{Name: "user>>>" + email + ">>>traffic>>>uplink", Value: usage[0]},
			&statscommand.Stat{Name: "user>>>" + email + ">>>traffic>>>downlink", Value: usage[1]},
		)
	}
	return resp, nil
}

func (f *fakeStatsServer) GetAllOnlineUsers(ctx context.Context, req *statscommand.GetAllOnlineUsersRequest) (*statscommand.GetAllOnlineUsersResponse, error) {
	users := make([]string, 0, len(f.onlineIPs))
	for email := range f.onlineIPs {
		users = append(users, "user>>>"+email+">>>online")
	}
	slices.Sort(users)
	return &statscommand.GetAllOnlineUsersResponse{Users: users}, nil
}

func (f *fakeStatsServer) GetStatsOnlineIpList(ctx context.Context, req *statscommand.GetStatsRequest) (*statscommand.GetStatsOnlineIpListResponse, error) {
	email, ok := emailFromOnlineStatName(req.GetName())
	if !ok {
		return &statscommand.GetStatsOnlineIpListResponse{Name: req.GetName()}, nil
	}
	return &statscommand.GetStatsOnlineIpListResponse{
		Name: req.GetName(),
		Ips:  f.onlineIPs[email],
	}, nil
}

func startStatsServer(t *testing.T, values map[string][2]int64, onlineIPs map[string]map[string]int64) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	statscommand.RegisterStatsServiceServer(server, &fakeStatsServer{values: values, onlineIPs: onlineIPs})
	go server.Serve(lis)
	return lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

func TestCollectorQueryUserBytes(t *testing.T) {
	addr, closeFn := startStatsServer(t, map[string][2]int64{
		"user@example.com": {100, 200},
	}, nil)
	defer closeFn()

	cfg := &config.Config{}
	cfg.Xray.APIServer = addr
	cfg.Xray.APITimeoutSec = 1

	col := New(cfg, nil)
	out, err := col.QueryUserBytes(context.Background(), []string{"user@example.com"})
	if err != nil {
		t.Fatalf("QueryUserBytes: %v", err)
	}
	got := out["user@example.com"]
	if got[0] != 100 || got[1] != 200 {
		t.Fatalf("unexpected stats: %v", got)
	}
}

func TestCollectorOnlineUsers(t *testing.T) {
	now := time.Now().UTC().Unix()
	addr, closeFn := startStatsServer(
		t,
		nil,
		map[string]map[string]int64{
			"user@example.com": {
				"198.51.100.10": now,
				"203.0.113.5":   now - 3,
			},
		},
	)
	defer closeFn()

	cfg := &config.Config{}
	cfg.Xray.APIServer = addr
	cfg.Xray.APITimeoutSec = 1

	col := New(cfg, nil)
	out, err := col.OnlineUsers(context.Background())
	if err != nil {
		t.Fatalf("OnlineUsers: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("unexpected online users len=%d", len(out))
	}
	if out[0].Email != "user@example.com" {
		t.Fatalf("unexpected online email: %+v", out[0])
	}
	if len(out[0].IPs) != 2 {
		t.Fatalf("unexpected ip len=%d", len(out[0].IPs))
	}
	if out[0].IPs[0].Address != "198.51.100.10" || out[0].IPs[1].Address != "203.0.113.5" {
		t.Fatalf("unexpected ip payload: %+v", out[0].IPs)
	}
}
