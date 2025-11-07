package stats

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/najahiiii/xray-agent/internal/config"

	"log/slog"
	"os/exec"
)

type Collector struct {
	cfg *config.Config
	log *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *Collector {
	return &Collector{cfg: cfg, log: log}
}

func (c *Collector) QueryUserBytes(ctx context.Context, emails []string) (map[string][2]int64, error) {
	res := make(map[string][2]int64, len(emails))
	for _, email := range emails {
		up, err := c.fetch(ctx, email, "uplink")
		if err != nil {
			return nil, fmt.Errorf("uplink %s: %w", email, err)
		}
		dn, err := c.fetch(ctx, email, "downlink")
		if err != nil {
			return nil, fmt.Errorf("downlink %s: %w", email, err)
		}
		res[email] = [2]int64{up, dn}
	}
	return res, nil
}

func (c *Collector) fetch(ctx context.Context, email, dir string) (int64, error) {
	name := fmt.Sprintf("user>>>%s>>>traffic>>>%s", email, dir)
	args := []string{"api", "stats", "--server=" + c.cfg.Xray.APIServer, "--name=" + name}
	if c.cfg.Xray.StatsResetEachPush {
		args = append(args, "--reset")
	}

	cmd := exec.CommandContext(ctx, c.cfg.Xray.Binary, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		c.log.Error("xray api stats error", "email", email, "dir", dir, "out", out.String(), "err", err)
		return 0, err
	}

	txt := strings.TrimSpace(out.String())
	var value int64
	if _, err := fmt.Sscan(txt, &value); err != nil {
		return 0, fmt.Errorf("parse stats: %w", err)
	}
	return value, nil
}
