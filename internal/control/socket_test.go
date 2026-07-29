package control

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/najahiiii/xray-agent/internal/config"
	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/socketproto"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSocketClientHandshakeReplayAndInboundMessages(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Agent-Slug") != "sg-1" || r.Header.Get("X-Agent-Protocol") != "1" {
			http.Error(w, "invalid agent headers", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var hello socketproto.Envelope
		if err := conn.ReadJSON(&hello); err != nil {
			serverErr <- err
			return
		}
		if hello.Type != socketproto.TypeHello {
			serverErr <- &testSocketError{"expected hello, got " + hello.Type}
			return
		}

		var queued socketproto.Envelope
		if err := conn.ReadJSON(&queued); err != nil {
			serverErr <- err
			return
		}
		if queued.Type != socketproto.TypeMetrics {
			serverErr <- &testSocketError{"expected metrics replay, got " + queued.Type}
			return
		}
		ack, _ := socketproto.NewEnvelope(socketproto.TypeAck, socketproto.Ack{MessageID: queued.ID})
		if err := conn.WriteJSON(ack); err != nil {
			serverErr <- err
			return
		}

		state, _ := socketproto.NewEnvelope(socketproto.TypeDesiredState, model.State{ConfigVersion: 9})
		if err := conn.WriteJSON(state); err != nil {
			serverErr <- err
			return
		}
		commandPayload := model.AgentCommand{ID: "cmd-1", Type: model.AgentCommandTypeRestartCore}
		command, _ := socketproto.NewEnvelope(socketproto.TypeCommand, commandPayload)
		if err := conn.WriteJSON(command); err != nil {
			serverErr <- err
			return
		}
		if err := conn.WriteJSON(command); err != nil {
			serverErr <- err
			return
		}

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Control.SocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	cfg.Control.Token = "token"
	cfg.Control.ServerSlug = "sg-1"
	cfg.Control.OutboxPath = filepath.Join(t.TempDir(), "outbox.db")
	client, err := NewSocketClient(cfg, testLogger(), "v1.2.0", "v26.3.27")
	if err != nil {
		t.Fatalf("new socket client: %v", err)
	}
	defer client.Close()
	if err := client.QueueMetrics(&model.ServerMetricPush{ServerTime: time.Now().UTC()}); err != nil {
		t.Fatalf("queue metrics: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()

	select {
	case state := <-client.States():
		if state.ConfigVersion != 9 {
			t.Fatalf("unexpected state: %+v", state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for desired state")
	}

	select {
	case command := <-client.Commands():
		if command.ID != "cmd-1" {
			t.Fatalf("unexpected command: %+v", command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
	}
	select {
	case duplicate := <-client.Commands():
		t.Fatalf("duplicate command delivered: %+v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		count, err := client.PendingCount()
		if err != nil {
			t.Fatalf("pending count: %v", err)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox was not acknowledged, count=%d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("socket client did not stop")
	}
	select {
	case err := <-serverErr:
		t.Fatalf("socket server: %v", err)
	default:
	}
}

type testSocketError struct{ message string }

func (e *testSocketError) Error() string { return e.message }
