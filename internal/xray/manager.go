package xray

import (
	"context"
	"fmt"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"log/slog"
)

type Manager struct {
	cfg *config.Config
	log *slog.Logger
}

func NewManager(cfg *config.Config, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

func (m *Manager) State(ctx context.Context, current map[string]model.Client, desired []model.Client) (bool, error) {
	return m.applyViaHandler(ctx, current, desired)
}

func (m *Manager) applyViaHandler(ctx context.Context, current map[string]model.Client, desired []model.Client) (bool, error) {
	adds, removes := diffClients(current, desired)
	if len(adds) == 0 && len(removes) == 0 {
		return false, nil
	}

	conn, err := grpc.NewClient(m.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false, err
	}
	conn.Connect()
	defer conn.Close()

	client := handlerService.NewHandlerServiceClient(conn)

	for _, c := range removes {
		if err := m.removeUser(ctx, client, c); err != nil {
			return false, err
		}
	}
	for _, c := range adds {
		if err := m.addUser(ctx, client, c); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *Manager) removeUser(ctx context.Context, client handlerService.HandlerServiceClient, c model.Client) error {
	tag := m.tagForProto(c.Proto)
	if tag == "" {
		return fmt.Errorf("inbound tag for proto %s not configured", c.Proto)
	}
	req := &handlerService.AlterInboundRequest{
		Tag:       tag,
		Operation: serial.ToTypedMessage(&handlerService.RemoveUserOperation{Email: c.Email}),
	}
	callCtx, cancel := context.WithTimeout(ctx, m.apiTimeout())
	defer cancel()

	_, err := client.AlterInbound(callCtx, req)
	return err
}

func (m *Manager) addUser(ctx context.Context, client handlerService.HandlerServiceClient, c model.Client) error {
	user, err := buildUser(c)
	if err != nil {
		return err
	}
	tag := m.tagForProto(c.Proto)
	if tag == "" {
		return fmt.Errorf("inbound tag for proto %s not configured", c.Proto)
	}
	req := &handlerService.AlterInboundRequest{
		Tag:       tag,
		Operation: serial.ToTypedMessage(&handlerService.AddUserOperation{User: user}),
	}
	callCtx, cancel := context.WithTimeout(ctx, m.apiTimeout())
	defer cancel()

	_, err = client.AlterInbound(callCtx, req)
	return err
}

func (m *Manager) tagForProto(proto string) string {
	switch proto {
	case "vless":
		return m.cfg.Xray.InboundTags.VLESS
	case "vmess":
		return m.cfg.Xray.InboundTags.VMESS
	case "trojan":
		return m.cfg.Xray.InboundTags.TROJAN
	default:
		return ""
	}
}

func buildUser(c model.Client) (*protocol.User, error) {
	user := &protocol.User{Email: c.Email}
	switch c.Proto {
	case "vless":
		user.Account = serial.ToTypedMessage(&vless.Account{Id: c.ID, Encryption: "none"})
	case "vmess":
		user.Account = serial.ToTypedMessage(&vmess.Account{Id: c.ID})
	case "trojan":
		user.Account = serial.ToTypedMessage(&trojan.Account{Password: c.Password})
	default:
		return nil, fmt.Errorf("unsupported proto %s", c.Proto)
	}
	return user, nil
}

func diffClients(current map[string]model.Client, desired []model.Client) (adds, removes []model.Client) {
	desiredMap := make(map[string]model.Client, len(desired))
	for _, c := range desired {
		desiredMap[c.Email] = c
	}
	for email, cur := range current {
		if want, ok := desiredMap[email]; !ok || !equalClient(cur, want) {
			removes = append(removes, cur)
		}
	}
	for _, want := range desired {
		if cur, ok := current[want.Email]; !ok || !equalClient(cur, want) {
			adds = append(adds, want)
		}
	}
	return
}

func equalClient(a, b model.Client) bool {
	return a.Proto == b.Proto && a.ID == b.ID && a.Password == b.Password
}

func (m *Manager) apiTimeout() time.Duration {
	return time.Duration(m.cfg.Xray.APITimeoutSec) * time.Second
}
