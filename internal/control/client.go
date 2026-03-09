package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"

	"log/slog"
)

type Client struct {
	cfg          *config.Config
	client       *http.Client
	log          *slog.Logger
	agentVersion string
}

func NewClient(cfg *config.Config, log *slog.Logger, agentVersion string) *Client {
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{ //nolint:gosec
			InsecureSkipVerify: cfg.Control.TLSInsecure,
			MinVersion:         tls.VersionTLS12,
		},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &Client{
		cfg:          cfg,
		client:       &http.Client{Transport: tr, Timeout: 12 * time.Second},
		log:          log,
		agentVersion: agentVersion,
	}
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.cfg.Control.Token)
}

func (c *Client) GetState(ctx context.Context) (*model.State, error) {
	url := fmt.Sprintf("%s/api/agents/%s/state", c.cfg.Control.BaseURL, c.cfg.Control.ServerSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("state http %d: %s", resp.StatusCode, string(b))
	}

	var ds model.State
	if err := json.NewDecoder(resp.Body).Decode(&ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

func (c *Client) PostStats(ctx context.Context, p *model.StatsPush) error {
	url := fmt.Sprintf("%s/api/agents/%s/stats", c.cfg.Control.BaseURL, c.cfg.Control.ServerSlug)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(p); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post stats http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) PostOnlineUsers(ctx context.Context, p *model.OnlineUsersPush) error {
	if p == nil {
		return nil
	}
	url := fmt.Sprintf("%s/api/agents/%s/online", c.cfg.Control.BaseURL, c.cfg.Control.ServerSlug)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(p); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post online users http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) PostMetrics(ctx context.Context, p *model.ServerMetricPush) error {
	if p == nil {
		return nil
	}
	url := fmt.Sprintf("%s/api/agents/%s/metrics", c.cfg.Control.BaseURL, c.cfg.Control.ServerSlug)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(p); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post metrics http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) Heartbeat(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/agents/%s/heartbeat", c.cfg.Control.BaseURL, c.cfg.Control.ServerSlug)
	payload := model.HeartbeatPush{OK: true}
	if c.agentVersion != "" {
		payload.AgentVersion = c.agentVersion
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&payload); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *Client) GetNextCommand(ctx context.Context) (*model.AgentCommand, error) {
	url := fmt.Sprintf(
		"%s/api/agents/%s/commands/next",
		c.cfg.Control.BaseURL,
		c.cfg.Control.ServerSlug,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("next command http %d: %s", resp.StatusCode, string(b))
	}

	var payload struct {
		Command *model.AgentCommand `json:"command"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	return payload.Command, nil
}

func (c *Client) AckCommand(ctx context.Context, commandID string, ack *model.AgentCommandAck) error {
	if commandID == "" {
		return fmt.Errorf("command id required")
	}
	if ack == nil {
		return fmt.Errorf("ack payload required")
	}

	url := fmt.Sprintf(
		"%s/api/agents/%s/commands/%s/ack",
		c.cfg.Control.BaseURL,
		c.cfg.Control.ServerSlug,
		commandID,
	)
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(ack); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ack command http %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
