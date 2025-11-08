package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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

const (
	applyModeHandler = "handler_service"
)

type Manager struct {
	cfg      *config.Config
	log      *slog.Logger
	lockPath string
	// needsReload is set when a previous reload attempt failed so that we can retry
	// even if the config file already matches the desired JSON.
	needsReload bool
}

func NewManager(cfg *config.Config, log *slog.Logger) *Manager {
	lockPath := cfg.Xray.LockFile
	if lockPath == "" {
		lockPath = cfg.Xray.ConfigPath + ".lock"
	}
	if !filepath.IsAbs(lockPath) {
		lockPath = filepath.Clean(lockPath)
	}
	return &Manager{cfg: cfg, log: log, lockPath: lockPath}
}

func (m *Manager) ApplyDesired(ctx context.Context, current map[string]model.DesiredClient, desired []model.DesiredClient) (bool, error) {
	switch m.cfg.Xray.ApplyMode {
	case applyModeHandler:
		return m.applyViaHandler(ctx, current, desired)
	default:
		return m.applyViaConfig(ctx, desired)
	}
}

func (m *Manager) applyViaConfig(ctx context.Context, clients []model.DesiredClient) (bool, error) {
	release, err := m.acquireLock(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	cfgPath := m.cfg.Xray.ConfigPath

	orig, err := os.ReadFile(cfgPath)
	if err != nil {
		return false, err
	}

	var root map[string]any
	if err := json.Unmarshal(orig, &root); err != nil {
		return false, err
	}

	inbounds, ok := root["inbounds"].([]any)
	if !ok {
		return false, errors.New("invalid xray config: inbounds missing")
	}

	byProto := map[string][]model.DesiredClient{
		"vless":  {},
		"vmess":  {},
		"trojan": {},
	}
	for _, c := range clients {
		byProto[c.Proto] = append(byProto[c.Proto], c)
	}

	for proto, arr := range byProto {
		if len(arr) > 1 {
			sort.Slice(arr, func(i, j int) bool { return arr[i].Email < arr[j].Email })
			byProto[proto] = arr
		}
	}

	setClients := func(tag string, entries []any) error {
		if tag == "" {
			return fmt.Errorf("inbound tag for %s not configured", tag)
		}
		found := false
		for _, ib := range inbounds {
			mInbound, _ := ib.(map[string]any)
			if mInbound == nil {
				continue
			}
			if mInbound["tag"] == tag {
				settings, _ := mInbound["settings"].(map[string]any)
				if settings == nil {
					return fmt.Errorf("inbound %s has no settings", tag)
				}
				settings["clients"] = entries
				found = true
			}
		}
		if !found {
			return fmt.Errorf("inbound tag %s not found", tag)
		}
		return nil
	}

	if err := setClients(m.cfg.Xray.InboundTags.VLESS, buildClientEntries(byProto["vless"], "vless")); err != nil {
		return false, err
	}
	if err := setClients(m.cfg.Xray.InboundTags.VMESS, buildClientEntries(byProto["vmess"], "vmess")); err != nil {
		return false, err
	}
	if err := setClients(m.cfg.Xray.InboundTags.TROJAN, buildClientEntries(byProto["trojan"], "trojan")); err != nil {
		return false, err
	}

	ext := filepath.Ext(cfgPath)
	base := strings.TrimSuffix(cfgPath, ext)
	if ext == "" {
		ext = ".json"
	}
	tmp := fmt.Sprintf("%s.tmp-%d%s", base, time.Now().UnixNano(), ext)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return false, err
	}

	newBytes := buf.Bytes()
	if sameJSON(orig, newBytes) {
		if m.needsReload {
			if err := m.reloadWithBackoff(); err != nil {
				return false, err
			}
			m.needsReload = false
		}
		return false, nil
	}

	if err := os.WriteFile(tmp, newBytes, 0o644); err != nil {
		return false, err
	}
	if err := m.testConfig(tmp); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := m.reloadWithBackoff(); err != nil {
		m.needsReload = true
		return true, err
	}
	m.needsReload = false
	return true, nil
}

func (m *Manager) applyViaHandler(ctx context.Context, current map[string]model.DesiredClient, desired []model.DesiredClient) (bool, error) {
	adds, removes := diffClients(current, desired)
	if len(adds) == 0 && len(removes) == 0 {
		return false, nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, m.apiTimeout())
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, m.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false, err
	}
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

func (m *Manager) removeUser(ctx context.Context, client handlerService.HandlerServiceClient, c model.DesiredClient) error {
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

func (m *Manager) addUser(ctx context.Context, client handlerService.HandlerServiceClient, c model.DesiredClient) error {
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

func buildUser(c model.DesiredClient) (*protocol.User, error) {
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

func diffClients(current map[string]model.DesiredClient, desired []model.DesiredClient) (adds, removes []model.DesiredClient) {
	desiredMap := make(map[string]model.DesiredClient, len(desired))
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

func equalClient(a, b model.DesiredClient) bool {
	return a.Proto == b.Proto && a.ID == b.ID && a.Password == b.Password
}

func (m *Manager) acquireLock(ctx context.Context) (func(), error) {
	f, err := os.OpenFile(m.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	wait := 200 * time.Millisecond
	for attempts := 0; attempts < 5; attempts++ {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		} else if errors.Is(err, syscall.EWOULDBLOCK) {
			select {
			case <-ctx.Done():
				_ = f.Close()
				return nil, ctx.Err()
			case <-time.After(wait):
				wait *= 2
				continue
			}
		} else {
			_ = f.Close()
			return nil, err
		}
	}
	_ = f.Close()
	return nil, errors.New("timeout acquiring config lock")
}

func (m *Manager) reloadWithBackoff() error {
	delay := 500 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := m.reload(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(delay)
		delay *= 2
	}
	return lastErr
}

func (m *Manager) testConfig(path string) error {
	cmd := exec.Command(m.cfg.Xray.Binary, "-test", "-config", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.log.Error("xray -test failed", "out", string(out), "err", err)
		return err
	}
	return nil
}

func (m *Manager) reload() error {
	if m.cfg.Xray.ReloadCmd != "" {
		cmd := exec.Command("bash", "-lc", m.cfg.Xray.ReloadCmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("reload cmd failed: %v, out=%s", err, string(out))
		}
		return nil
	}

	if _, err := exec.Command("systemctl", "reload", "xray").CombinedOutput(); err == nil {
		return nil
	}

	if out2, err2 := exec.Command("systemctl", "restart", "xray").CombinedOutput(); err2 != nil {
		return fmt.Errorf("restart failed: %v, out=%s", err2, string(out2))
	}
	return nil
}

func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if err := json.Compact(&ca, a); err != nil {
		return false
	}
	if err := json.Compact(&cb, b); err != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

func buildClientEntries(clients []model.DesiredClient, proto string) []any {
	entries := make([]any, 0, len(clients))
	for _, c := range clients {
		switch proto {
		case "vless":
			entries = append(entries, map[string]any{"id": c.ID, "email": c.Email})
		case "vmess":
			entries = append(entries, map[string]any{"id": c.ID, "email": c.Email, "alterId": 0})
		case "trojan":
			entries = append(entries, map[string]any{"password": c.Password, "email": c.Email})
		}
	}
	return entries
}

func (m *Manager) apiTimeout() time.Duration {
	return time.Duration(m.cfg.Xray.APITimeoutSec) * time.Second
}
