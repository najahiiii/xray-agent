package xray

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	routerService "github.com/xtls/xray-core/app/router/command"
	"google.golang.org/grpc"
)

type handlerOp struct {
	tag   string
	kind  string
	email string
}

type fakeHandlerServer struct {
	handlerService.UnimplementedHandlerServiceServer
	ops []handlerOp
}

type routeOp struct {
	tag  string
	kind string
}

type fakeRoutingServer struct {
	routerService.UnimplementedRoutingServiceServer
	ops []routeOp
}

func (f *fakeHandlerServer) AlterInbound(ctx context.Context, req *handlerService.AlterInboundRequest) (*handlerService.AlterInboundResponse, error) {
	msg, err := req.Operation.GetInstance()
	if err != nil {
		return nil, err
	}
	switch op := msg.(type) {
	case *handlerService.AddUserOperation:
		f.ops = append(f.ops, handlerOp{tag: req.Tag, kind: "add", email: op.User.Email})
	case *handlerService.RemoveUserOperation:
		f.ops = append(f.ops, handlerOp{tag: req.Tag, kind: "remove", email: op.Email})
	default:
		return nil, fmt.Errorf("unknown op %T", op)
	}
	return &handlerService.AlterInboundResponse{}, nil
}

func (f *fakeRoutingServer) AddRule(ctx context.Context, req *routerService.AddRuleRequest) (*routerService.AddRuleResponse, error) {
	if _, err := req.Config.GetInstance(); err != nil {
		return nil, err
	}
	f.ops = append(f.ops, routeOp{kind: "add"})
	return &routerService.AddRuleResponse{}, nil
}

func (f *fakeRoutingServer) RemoveRule(ctx context.Context, req *routerService.RemoveRuleRequest) (*routerService.RemoveRuleResponse, error) {
	f.ops = append(f.ops, routeOp{tag: req.RuleTag, kind: "remove"})
	return &routerService.RemoveRuleResponse{}, nil
}

func startAPIServer(t *testing.T) (*fakeHandlerServer, *fakeRoutingServer, string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	fs := &fakeHandlerServer{}
	rs := &fakeRoutingServer{}
	handlerService.RegisterHandlerServiceServer(server, fs)
	routerService.RegisterRoutingServiceServer(server, rs)
	go server.Serve(lis)
	return fs, rs, lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

func TestManagerState(t *testing.T) {
	fs, _, addr, closeFn := startAPIServer(t)
	defer closeFn()

	cfg := &config.Config{}
	cfg.Xray.APIServer = addr
	cfg.Xray.APITimeoutSec = 1
	cfg.Xray.InboundTags.VLESS = "vless-tag"

	mgr := NewManager(cfg, nil)
	current := map[string]model.Client{
		"a@example.com": {Proto: "vless", ID: "1", Email: "a@example.com"},
	}
	desired := []model.Client{
		{Proto: "vless", ID: "2", Email: "b@example.com"},
	}

	changed, err := mgr.State(context.Background(), current, desired, map[string]model.RouteRule{}, nil)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if len(fs.ops) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(fs.ops))
	}
	if fs.ops[0].kind != "remove" || fs.ops[0].email != "a@example.com" {
		t.Fatalf("unexpected ops: %+v", fs.ops)
	}
	if fs.ops[1].kind != "remove" || fs.ops[1].email != "b@example.com" {
		t.Fatalf("unexpected ops: %+v", fs.ops)
	}
	if fs.ops[2].kind != "add" || fs.ops[2].email != "b@example.com" {
		t.Fatalf("unexpected ops: %+v", fs.ops)
	}
}

func TestManagerStatePreRemovesStaleRouteBeforeAdd(t *testing.T) {
	_, rs, addr, closeFn := startAPIServer(t)
	defer closeFn()

	cfg := &config.Config{}
	cfg.Xray.APIServer = addr
	cfg.Xray.APITimeoutSec = 1

	mgr := NewManager(cfg, nil)
	desiredRoutes := []model.RouteRule{
		{Tag: "re-route-ipv4", OutboundTag: "direct", IP: []string{"8.8.8.8/32"}},
	}

	changed, err := mgr.State(
		context.Background(),
		map[string]model.Client{},
		nil,
		map[string]model.RouteRule{},
		desiredRoutes,
	)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !changed {
		t.Fatal("expected change")
	}

	if len(rs.ops) != 2 {
		t.Fatalf("expected 2 route operations, got %d", len(rs.ops))
	}
	if rs.ops[0].kind != "remove" || rs.ops[0].tag != "re-route-ipv4" {
		t.Fatalf("unexpected route ops: %+v", rs.ops)
	}
	if rs.ops[1].kind != "add" {
		t.Fatalf("unexpected route ops: %+v", rs.ops)
	}
}
