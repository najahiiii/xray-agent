package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	"log/slog"
)

type Manager struct {
	cfg *config.Config
	log *slog.Logger
}

func NewManager(cfg *config.Config, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

func (m *Manager) ApplyDesired(clients []model.DesiredClient) (bool, error) {
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

	tmp := fmt.Sprintf("%s.tmp-%d", cfgPath, time.Now().UnixNano())
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return false, err
	}

	newBytes := buf.Bytes()
	if sameJSON(orig, newBytes) {
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
	if err := m.reload(); err != nil {
		return true, err
	}
	return true, nil
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
