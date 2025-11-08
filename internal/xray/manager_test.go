package xray

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
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

func startHandlerServer(t *testing.T) (*fakeHandlerServer, string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	fs := &fakeHandlerServer{}
	handlerService.RegisterHandlerServiceServer(server, fs)
	go server.Serve(lis)
	return fs, lis.Addr().String(), func() {
		server.Stop()
		_ = lis.Close()
	}
}

func TestManagerState(t *testing.T) {
	fs, addr, closeFn := startHandlerServer(t)
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

	changed, err := mgr.State(context.Background(), current, desired)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if len(fs.ops) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(fs.ops))
	}
	if fs.ops[0].kind != "remove" || fs.ops[0].email != "a@example.com" {
		t.Fatalf("unexpected ops: %+v", fs.ops)
	}
	if fs.ops[1].kind != "add" || fs.ops[1].email != "b@example.com" {
		t.Fatalf("unexpected ops: %+v", fs.ops)
	}
}
