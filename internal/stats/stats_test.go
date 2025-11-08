package stats

import (
	"context"
	"net"
	"testing"

	"github.com/najahiiii/xray-agent/internal/config"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type fakeStatsServer struct {
	statscommand.UnimplementedStatsServiceServer
	values map[string][2]int64
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

func startStatsServer(t *testing.T, values map[string][2]int64) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	statscommand.RegisterStatsServiceServer(server, &fakeStatsServer{values: values})
	go server.Serve(lis)
	return lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

func TestCollectorQueryUserBytes(t *testing.T) {
	addr, closeFn := startStatsServer(t, map[string][2]int64{
		"user@example.com": {100, 200},
	})
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
