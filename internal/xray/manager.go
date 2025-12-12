package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	handlerService "github.com/xtls/xray-core/app/proxyman/command"
	routerService "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
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

func (m *Manager) State(ctx context.Context, currentClients map[string]model.Client, desiredClients []model.Client, currentRoutes map[string]model.RouteRule, desiredRoutes []model.RouteRule) (bool, error) {
	clientsChanged, err := m.applyViaHandler(ctx, currentClients, desiredClients)
	if err != nil {
		return false, err
	}

	routesChanged, err := m.applyRoutes(ctx, currentRoutes, desiredRoutes)
	if err != nil {
		return clientsChanged, err
	}

	return clientsChanged || routesChanged, nil
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
	// ensure we don't leave stale runtime users (e.g., after agent restart)
	_ = m.removeUser(ctx, client, c)

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

func (m *Manager) applyRoutes(ctx context.Context, current map[string]model.RouteRule, desired []model.RouteRule) (bool, error) {
	adds, removes := diffRoutes(current, desired)
	if len(adds) == 0 && len(removes) == 0 {
		return false, nil
	}

	conn, err := grpc.NewClient(m.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false, err
	}
	conn.Connect()
	defer conn.Close()

	client := routerService.NewRoutingServiceClient(conn)

	for _, r := range removes {
		if err := m.removeRoute(ctx, client, r); err != nil {
			return false, err
		}
	}
	for _, r := range adds {
		if err := m.addRoute(ctx, client, r); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *Manager) removeRoute(ctx context.Context, client routerService.RoutingServiceClient, r model.RouteRule) error {
	if r.Tag == "" {
		return fmt.Errorf("route tag required for removal")
	}
	req := &routerService.RemoveRuleRequest{RuleTag: r.Tag}
	callCtx, cancel := context.WithTimeout(ctx, m.apiTimeout())
	defer cancel()

	_, err := client.RemoveRule(callCtx, req)
	return err
}

func (m *Manager) addRoute(ctx context.Context, client routerService.RoutingServiceClient, r model.RouteRule) error {
	tmsg, err := buildRoutingConfig(r)
	if err != nil {
		return err
	}

	req := &routerService.AddRuleRequest{
		Config:       tmsg,
		ShouldAppend: true,
	}
	callCtx, cancel := context.WithTimeout(ctx, m.apiTimeout())
	defer cancel()

	_, err = client.AddRule(callCtx, req)
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

func diffClients(current map[string]model.Client, dc []model.Client) (adds, removes []model.Client) {
	Map := make(map[string]model.Client, len(dc))
	for _, c := range dc {
		Map[c.Email] = c
	}
	for email, cur := range current {
		if want, ok := Map[email]; !ok || !equalClient(cur, want) {
			removes = append(removes, cur)
		}
	}
	for _, want := range dc {
		if cur, ok := current[want.Email]; !ok || !equalClient(cur, want) {
			adds = append(adds, want)
		}
	}
	return
}

func equalClient(a, b model.Client) bool {
	return a.Proto == b.Proto && a.ID == b.ID && a.Password == b.Password
}

func diffRoutes(current map[string]model.RouteRule, desired []model.RouteRule) (adds, removes []model.RouteRule) {
	desiredMap := make(map[string]model.RouteRule, len(desired))
	for _, r := range desired {
		desiredMap[r.Tag] = r
	}
	for tag, cur := range current {
		if want, ok := desiredMap[tag]; !ok || !equalRouteRule(cur, want) {
			removes = append(removes, cur)
		}
	}
	for _, want := range desired {
		if cur, ok := current[want.Tag]; !ok || !equalRouteRule(cur, want) {
			adds = append(adds, want)
		}
	}
	return
}

func equalRouteRule(a, b model.RouteRule) bool {
	return a.Tag == b.Tag &&
		a.OutboundTag == b.OutboundTag &&
		a.BalancerTag == b.BalancerTag &&
		a.Port == b.Port &&
		a.SourcePort == b.SourcePort &&
		slices.Equal(a.Domain, b.Domain) &&
		slices.Equal(a.IP, b.IP) &&
		slices.Equal(a.InboundTag, b.InboundTag) &&
		slices.Equal(a.Protocol, b.Protocol)
}

func buildRoutingConfig(r model.RouteRule) (*serial.TypedMessage, error) {
	if r.Tag == "" {
		return nil, fmt.Errorf("route tag required")
	}
	if r.OutboundTag == "" && r.BalancerTag == "" {
		return nil, fmt.Errorf("route %s: outbound_tag or balancer_tag required", r.Tag)
	}

	fieldRule := map[string]any{
		"type":    "field",
		"ruleTag": r.Tag,
	}
	if r.OutboundTag != "" {
		fieldRule["outboundTag"] = r.OutboundTag
	}
	if r.BalancerTag != "" {
		fieldRule["balancerTag"] = r.BalancerTag
	}
	if len(r.Domain) > 0 {
		fieldRule["domain"] = r.Domain
	}
	if len(r.IP) > 0 {
		fieldRule["ip"] = r.IP
	}
	if r.Port != "" {
		fieldRule["port"] = r.Port
	}
	if r.SourcePort != "" {
		fieldRule["sourcePort"] = r.SourcePort
	}
	if len(r.InboundTag) > 0 {
		fieldRule["inboundTag"] = r.InboundTag
	}
	if len(r.Protocol) > 0 {
		fieldRule["protocol"] = r.Protocol
	}

	rawRule, err := json.Marshal(fieldRule)
	if err != nil {
		return nil, err
	}

	rc := conf.RouterConfig{
		RuleList: []json.RawMessage{rawRule},
	}
	cfg, err := rc.Build()
	if err != nil {
		return nil, err
	}

	tmsg := serial.ToTypedMessage(cfg)
	if tmsg == nil {
		return nil, fmt.Errorf("route %s: failed to create typed message", r.Tag)
	}
	return tmsg, nil
}

func (m *Manager) apiTimeout() time.Duration {
	return time.Duration(m.cfg.Xray.APITimeoutSec) * time.Second
}
