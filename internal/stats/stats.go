package stats

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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
	pattern := fmt.Sprintf("user>>>%s>>>traffic>>>.*", regexp.QuoteMeta(email))
	resp, err := client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: pattern,
		Reset_:  c.cfg.Xray.StatsResetEachPush,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("stats query %s: %w", email, err)
	}

	var up, dn int64
	for _, stat := range resp.GetStat() {
		name := stat.GetName()
		switch {
		case strings.HasSuffix(name, ">>>uplink"):
			up = stat.GetValue()
		case strings.HasSuffix(name, ">>>downlink"):
			dn = stat.GetValue()
		}
	}
	return up, dn, nil
}
