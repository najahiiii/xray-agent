package control

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/socketproto"
	"log/slog"
)

const (
	socketHandshakeTimeout = 10 * time.Second
	socketWriteTimeout     = 10 * time.Second
	socketPongTimeout      = 75 * time.Second
	socketPingInterval     = 25 * time.Second
	socketFlushInterval    = time.Second
	socketMaxMessageBytes  = 4 << 20
	socketPendingBatchSize = 256
	socketReconnectMin     = time.Second
	socketReconnectMax     = 30 * time.Second
)

// SocketClient owns the single persistent agent-to-panel WebSocket, durable
// delivery outbox, and inbound state/command channels.
type SocketClient struct {
	cfg          *config.Config
	log          *slog.Logger
	store        *socketStore
	socketURL    string
	agentVersion string

	versionMu       sync.RWMutex
	xrayCoreVersion string
	configVersion   atomic.Int64
	connected       atomic.Bool
	commandMu       sync.Mutex
	seenCommands    map[string]struct{}

	states    chan *model.State
	commands  chan *model.AgentCommand
	ephemeral chan socketproto.Envelope
	wake      chan struct{}
	acked     chan string
}

func NewSocketClient(cfg *config.Config, log *slog.Logger, agentVersion, xrayCoreVersion string) (*SocketClient, error) {
	socketURL, err := resolveSocketURL(cfg)
	if err != nil {
		return nil, err
	}
	outboxPath := cfg.Control.OutboxPath
	if outboxPath == "" {
		outboxPath = config.DefaultOutboxPath
	}
	store, err := openSocketStore(outboxPath)
	if err != nil {
		return nil, err
	}
	client := &SocketClient{
		cfg:             cfg,
		log:             log,
		store:           store,
		socketURL:       socketURL,
		agentVersion:    strings.TrimSpace(agentVersion),
		xrayCoreVersion: normalizeTaggedVersion(xrayCoreVersion),
		states:          make(chan *model.State, 1),
		commands:        make(chan *model.AgentCommand, 64),
		seenCommands:    make(map[string]struct{}),
		ephemeral:       make(chan socketproto.Envelope, 32),
		wake:            make(chan struct{}, 1),
		acked:           make(chan string, 256),
	}
	client.configVersion.Store(-1)
	return client, nil
}

func (c *SocketClient) Close() error {
	return c.store.Close()
}

func (c *SocketClient) States() <-chan *model.State {
	return c.states
}

func (c *SocketClient) Commands() <-chan *model.AgentCommand {
	return c.commands
}

func (c *SocketClient) Connected() bool {
	return c.connected.Load()
}

func (c *SocketClient) AgentVersion() string {
	return c.agentVersion
}

func (c *SocketClient) SetXrayCoreVersion(version string) {
	c.versionMu.Lock()
	c.xrayCoreVersion = normalizeTaggedVersion(version)
	c.versionMu.Unlock()
}

func (c *SocketClient) SetConfigVersion(version int64) {
	c.configVersion.Store(version)
}

func (c *SocketClient) Run(ctx context.Context) {
	backoff := socketReconnectMin
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := c.connect(ctx)
		if err != nil {
			c.log.Warn("socket connect failed", "url", c.socketURL, "err", err, "retry_in", backoff)
			if !waitForReconnect(ctx, withJitter(backoff)) {
				return
			}
			backoff = min(backoff*2, socketReconnectMax)
			continue
		}

		backoff = socketReconnectMin
		c.connected.Store(true)
		c.log.Info("socket connected", "url", c.socketURL)
		err = c.serveConnection(ctx, conn)
		c.connected.Store(false)
		_ = conn.Close()
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("socket disconnected", "err", err)
		if !waitForReconnect(ctx, withJitter(backoff)) {
			return
		}
		backoff = min(backoff*2, socketReconnectMax)
	}
}

func (c *SocketClient) connect(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: socketHandshakeTimeout,
		TLSClientConfig: &tls.Config{ //nolint:gosec
			InsecureSkipVerify: c.cfg.Control.TLSInsecure,
			MinVersion:         tls.VersionTLS12,
		},
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.cfg.Control.Token)
	headers.Set("X-Agent-Slug", c.cfg.Control.ServerSlug)
	headers.Set("X-Agent-Protocol", fmt.Sprintf("%d", socketproto.Version))

	conn, response, err := dialer.DialContext(ctx, c.socketURL, headers)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("websocket handshake http %d: %w", response.StatusCode, err)
		}
		return nil, err
	}
	return conn, nil
}

func (c *SocketClient) serveConnection(ctx context.Context, conn *websocket.Conn) error {
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn.SetReadLimit(socketMaxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(socketPongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(socketPongTimeout))
	})

	writerErr := make(chan error, 1)
	go func() {
		writerErr <- c.runWriter(connectionCtx, conn)
	}()

	readerErr := make(chan error, 1)
	go func() {
		readerErr <- c.runReader(connectionCtx, conn)
	}()

	select {
	case <-ctx.Done():
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "agent shutting down"), time.Now().Add(socketWriteTimeout))
		return ctx.Err()
	case err := <-writerErr:
		return err
	case err := <-readerErr:
		return err
	}
}

func (c *SocketClient) runWriter(ctx context.Context, conn *websocket.Conn) error {
	hello, err := c.helloEnvelope()
	if err != nil {
		return err
	}
	if err := writeSocketJSON(conn, hello); err != nil {
		return err
	}

	sent := make(map[string]struct{})
	if err := c.flushPending(conn, sent); err != nil {
		return err
	}

	pingTicker := time.NewTicker(socketPingInterval)
	defer pingTicker.Stop()
	flushTicker := time.NewTicker(socketFlushInterval)
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case envelope := <-c.ephemeral:
			if err := writeSocketJSON(conn, envelope); err != nil {
				return err
			}
		case <-c.wake:
			if err := c.flushPending(conn, sent); err != nil {
				return err
			}
		case messageID := <-c.acked:
			delete(sent, messageID)
		case <-flushTicker.C:
			if err := c.flushPending(conn, sent); err != nil {
				return err
			}
		case <-pingTicker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(socketWriteTimeout)); err != nil {
				return err
			}
		}
	}
}

func (c *SocketClient) runReader(ctx context.Context, conn *websocket.Conn) error {
	for {
		var envelope socketproto.Envelope
		if err := conn.ReadJSON(&envelope); err != nil {
			return err
		}
		if envelope.Version != socketproto.Version {
			return fmt.Errorf("unsupported socket protocol version %d", envelope.Version)
		}

		switch envelope.Type {
		case socketproto.TypeAck:
			var ack socketproto.Ack
			if err := json.Unmarshal(envelope.Payload, &ack); err != nil {
				return fmt.Errorf("decode socket ack: %w", err)
			}
			if _, err := c.store.Ack(ack.MessageID); err != nil {
				return fmt.Errorf("persist socket ack: %w", err)
			}
			select {
			case c.acked <- ack.MessageID:
			case <-ctx.Done():
				return ctx.Err()
			}
			c.signalWriter()
		case socketproto.TypeDesiredState:
			var state model.State
			if err := json.Unmarshal(envelope.Payload, &state); err != nil {
				return fmt.Errorf("decode desired state: %w", err)
			}
			c.publishLatestState(ctx, &state)
		case socketproto.TypeCommand:
			var command model.AgentCommand
			if err := json.Unmarshal(envelope.Payload, &command); err != nil {
				return fmt.Errorf("decode agent command: %w", err)
			}
			completedAck, err := c.store.CompletedCommandAck(command.ID)
			if err != nil {
				return fmt.Errorf("read completed command %s: %w", command.ID, err)
			}
			if completedAck != nil {
				if err := c.QueueCommandAck(command.ID, completedAck); err != nil {
					return fmt.Errorf("requeue completed command %s: %w", command.ID, err)
				}
				continue
			}
			if !c.markCommandSeen(command.ID) {
				continue
			}
			select {
			case c.commands <- &command:
			case <-ctx.Done():
				return ctx.Err()
			}
		case socketproto.TypePing:
			_ = c.queueEphemeral(socketproto.TypePong, map[string]any{"reply_to": envelope.ID})
		case socketproto.TypeHelloAck, socketproto.TypePong:
			// The WebSocket control-frame pong is authoritative for liveness.
		case socketproto.TypeError:
			c.log.Warn("socket gateway error", "payload", string(envelope.Payload))
		default:
			c.log.Debug("ignoring unknown socket message", "type", envelope.Type)
		}
	}
}

func (c *SocketClient) flushPending(conn *websocket.Conn, sent map[string]struct{}) error {
	for {
		pending, err := c.store.Pending(sent, socketPendingBatchSize)
		if err != nil {
			return fmt.Errorf("read socket outbox: %w", err)
		}
		for _, envelope := range pending {
			if err := writeSocketJSON(conn, envelope); err != nil {
				return err
			}
			sent[envelope.ID] = struct{}{}
		}
		if len(pending) < socketPendingBatchSize {
			return nil
		}
	}
}

func (c *SocketClient) helloEnvelope() (socketproto.Envelope, error) {
	c.versionMu.RLock()
	xrayCoreVersion := c.xrayCoreVersion
	c.versionMu.RUnlock()
	return socketproto.NewEnvelope(socketproto.TypeHello, socketproto.Hello{
		ServerSlug:      c.cfg.Control.ServerSlug,
		AgentVersion:    c.agentVersion,
		XrayCoreVersion: xrayCoreVersion,
		ConfigVersion:   c.configVersion.Load(),
	})
}

func (c *SocketClient) QueueStatsSample(serverTime time.Time, current map[string][2]int64) error {
	envelope, err := c.store.EnqueueStatsSample(serverTime, current)
	if err == nil && envelope != nil {
		c.signalWriter()
	}
	return err
}

func (c *SocketClient) QueueOnline(payload *model.OnlineUsersPush) error {
	if payload == nil {
		return nil
	}
	_, err := c.store.EnqueueLatest(socketproto.TypeOnline, payload)
	if err == nil {
		c.signalWriter()
	}
	return err
}

func (c *SocketClient) QueueMetrics(payload *model.ServerMetricPush) error {
	if payload == nil {
		return nil
	}
	_, err := c.store.Enqueue(socketproto.TypeMetrics, payload)
	if err == nil {
		c.signalWriter()
	}
	return err
}

func (c *SocketClient) QueueHeartbeat() error {
	c.versionMu.RLock()
	xrayCoreVersion := c.xrayCoreVersion
	c.versionMu.RUnlock()
	return c.queueEphemeral(socketproto.TypeHeartbeat, model.HeartbeatPush{
		OK:              true,
		AgentVersion:    c.agentVersion,
		XrayCoreVersion: xrayCoreVersion,
	})
}

func (c *SocketClient) QueueStateApplied(configVersion int64) error {
	_, err := c.store.EnqueueLatest(socketproto.TypeStateApplied, socketproto.StateApplied{ConfigVersion: configVersion})
	if err == nil {
		c.SetConfigVersion(configVersion)
		c.signalWriter()
	}
	return err
}

func (c *SocketClient) QueueCommandAck(commandID string, ack *model.AgentCommandAck) error {
	if ack == nil {
		return fmt.Errorf("command ack required")
	}
	_, err := c.store.EnqueueCommandAck(commandID, ack)
	if err == nil {
		c.signalWriter()
	}
	return err
}

func (c *SocketClient) PendingCount() (int, error) {
	return c.store.Count()
}

func (c *SocketClient) queueEphemeral(messageType string, payload any) error {
	if !c.connected.Load() && messageType == socketproto.TypeHeartbeat {
		return nil
	}
	envelope, err := socketproto.NewEnvelope(messageType, payload)
	if err != nil {
		return err
	}
	select {
	case c.ephemeral <- envelope:
		return nil
	default:
		return fmt.Errorf("socket ephemeral queue full")
	}
}

func (c *SocketClient) publishLatestState(ctx context.Context, state *model.State) {
	select {
	case c.states <- state:
		return
	default:
	}
	select {
	case <-c.states:
	default:
	}
	select {
	case c.states <- state:
	case <-ctx.Done():
	}
}

func (c *SocketClient) signalWriter() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *SocketClient) markCommandSeen(commandID string) bool {
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	if _, exists := c.seenCommands[commandID]; exists {
		return false
	}
	c.seenCommands[commandID] = struct{}{}
	return true
}

func writeSocketJSON(conn *websocket.Conn, value any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(socketWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(value)
}

func resolveSocketURL(cfg *config.Config) (string, error) {
	raw := strings.TrimSpace(cfg.Control.SocketURL)
	if raw == "" {
		raw = strings.TrimSpace(cfg.Control.BaseURL)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse control socket url: %w", err)
	}
	if cfg.Control.SocketURL == "" {
		switch parsed.Scheme {
		case "http":
			parsed.Scheme = "ws"
		case "https":
			parsed.Scheme = "wss"
		default:
			return "", fmt.Errorf("control base_url must use http or https")
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + config.DefaultSocketPath
		parsed.RawQuery = ""
		parsed.Fragment = ""
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return "", fmt.Errorf("control socket_url must use ws or wss")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("control socket url host required")
	}
	return parsed.String(), nil
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func withJitter(delay time.Duration) time.Duration {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return delay
	}
	maxJitter := max(delay/2, time.Millisecond)
	jitter := time.Duration(binary.BigEndian.Uint64(value[:]) % uint64(maxJitter))
	return delay + jitter
}
