package stats

import (
	"context"
	"fmt"

	"github.com/najahiiii/xray-agent/internal/config"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"log/slog"
)

type Collector struct {
	cfg *config.Config
	log *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *Collector {
	return &Collector{cfg: cfg, log: log}
}

func (c *Collector) QueryUserBytes(ctx context.Context, emails []string) (map[string][2]int64, error) {
	conn, err := grpc.NewClient(c.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	conn.Connect()
	defer conn.Close()

	client := statscommand.NewStatsServiceClient(conn)
	res := make(map[string][2]int64, len(emails))
	for _, email := range emails {
		up, dn, err := c.fetch(ctx, client, email)
		if err != nil {
			return nil, err
		}
		res[email] = [2]int64{up, dn}
	}
	return res, nil
}

func (c *Collector) fetch(ctx context.Context, client statscommand.StatsServiceClient, email string) (int64, int64, error) {
	up, err := c.querySingle(ctx, client, fmt.Sprintf("user>>>%s>>>traffic>>>uplink", email))
	if err != nil {
		return 0, 0, err
	}
	down, err := c.querySingle(ctx, client, fmt.Sprintf("user>>>%s>>>traffic>>>downlink", email))
	if err != nil {
		return 0, 0, err
	}
	return up, down, nil
}

func (c *Collector) querySingle(ctx context.Context, client statscommand.StatsServiceClient, name string) (int64, error) {
	reset := c.cfg.Xray.StatsResetEachPush
	if reset {
		c.log.Debug("stats reset enabled, resetting counters", "name", name)
	}
	resp, err := client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: name,
		Reset_:  reset,
	})
	if err != nil {
		return 0, fmt.Errorf("stats query %s: %w", name, err)
	}
	for _, stat := range resp.GetStat() {
		if stat.GetName() == name {
			return stat.GetValue(), nil
		}
	}
	return 0, nil
}
