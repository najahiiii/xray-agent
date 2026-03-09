package stats

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"log/slog"
)

type Collector struct {
	cfg *config.Config
	log *slog.Logger
}

const (
	onlineStatPrefix = "user>>>"
	onlineStatSuffix = ">>>online"
)

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
	if reset && c.log != nil {
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

func (c *Collector) OnlineUsers(ctx context.Context) ([]model.OnlineUserInfo, error) {
	conn, err := grpc.NewClient(c.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	conn.Connect()
	defer conn.Close()

	client := statscommand.NewStatsServiceClient(conn)
	resp, err := client.GetAllOnlineUsers(ctx, &statscommand.GetAllOnlineUsersRequest{})
	if err != nil {
		return nil, fmt.Errorf("online users query: %w", err)
	}

	users := make([]model.OnlineUserInfo, 0, len(resp.GetUsers()))
	for _, statName := range resp.GetUsers() {
		email, ok := emailFromOnlineStatName(statName)
		if !ok {
			if c.log != nil {
				c.log.Debug("skip unknown online stat name", "name", statName)
			}
			continue
		}

		ips, err := c.onlineUserIPs(ctx, client, statName)
		if err != nil {
			return nil, err
		}

		users = append(users, model.OnlineUserInfo{
			Email: email,
			IPs:   ips,
		})
	}

	slices.SortFunc(users, func(a, b model.OnlineUserInfo) int {
		return strings.Compare(strings.ToLower(a.Email), strings.ToLower(b.Email))
	})
	return users, nil
}

func (c *Collector) onlineUserIPs(ctx context.Context, client statscommand.StatsServiceClient, statName string) ([]model.OnlineUserIP, error) {
	resp, err := client.GetStatsOnlineIpList(ctx, &statscommand.GetStatsRequest{Name: statName})
	if err != nil {
		return nil, fmt.Errorf("online ip list %s: %w", statName, err)
	}

	ips := make([]model.OnlineUserIP, 0, len(resp.GetIps()))
	for address, unixTS := range resp.GetIps() {
		if address == "" {
			continue
		}
		ips = append(ips, model.OnlineUserIP{
			Address:    address,
			LastSeenAt: time.Unix(unixTS, 0).UTC(),
		})
	}

	slices.SortFunc(ips, func(a, b model.OnlineUserIP) int {
		return strings.Compare(a.Address, b.Address)
	})
	return ips, nil
}

func (c *Collector) SysStats(ctx context.Context) (*model.XraySysStats, error) {
	conn, err := grpc.NewClient(c.cfg.Xray.APIServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	conn.Connect()
	defer conn.Close()

	client := statscommand.NewStatsServiceClient(conn)
	resp, err := client.GetSysStats(ctx, &statscommand.SysStatsRequest{})
	if err != nil {
		return nil, fmt.Errorf("sys stats query: %w", err)
	}

	return &model.XraySysStats{
		NumGoroutine: resp.GetNumGoroutine(),
		NumGC:        resp.GetNumGC(),
		Alloc:        resp.GetAlloc(),
		TotalAlloc:   resp.GetTotalAlloc(),
		Sys:          resp.GetSys(),
		Mallocs:      resp.GetMallocs(),
		Frees:        resp.GetFrees(),
		LiveObjects:  resp.GetLiveObjects(),
		PauseTotalNs: resp.GetPauseTotalNs(),
		Uptime:       resp.GetUptime(),
	}, nil
}

func emailFromOnlineStatName(name string) (string, bool) {
	if !strings.HasPrefix(name, onlineStatPrefix) || !strings.HasSuffix(name, onlineStatSuffix) {
		return "", false
	}

	email := strings.TrimSuffix(strings.TrimPrefix(name, onlineStatPrefix), onlineStatSuffix)
	if email == "" {
		return "", false
	}
	return email, true
}
