package control

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
	"github.com/najahiiii/xray-agent/internal/socketproto"
)

func TestSocketStorePersistsAndAcknowledgesEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	store, err := openSocketStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	event, err := store.Enqueue(socketproto.TypeMetrics, &model.ServerMetricPush{ServerTime: time.Now().UTC()})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store, err = openSocketStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store.Close()
	pending, err := store.Pending(map[string]struct{}{}, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != event.ID {
		t.Fatalf("unexpected persisted events: %+v", pending)
	}
	removed, err := store.Ack(event.ID)
	if err != nil || !removed {
		t.Fatalf("ack: removed=%v err=%v", removed, err)
	}
	if count, err := store.Count(); err != nil || count != 0 {
		t.Fatalf("unexpected count after ack: count=%d err=%v", count, err)
	}
}

func TestSocketStoreStatsBaselinesAreDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	store, err := openSocketStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	first, err := store.EnqueueStatsSample(time.Now().UTC(), map[string][2]int64{
		"User@Example.com": {100, 200},
	})
	if err != nil {
		t.Fatalf("first sample: %v", err)
	}
	assertStatsEnvelope(t, first, "user@example.com", 100, 200)
	_, _ = store.Ack(first.ID)

	second, err := store.EnqueueStatsSample(time.Now().UTC(), map[string][2]int64{
		"user@example.com": {150, 260},
	})
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	assertStatsEnvelope(t, second, "user@example.com", 50, 60)
	_, _ = store.Ack(second.ID)

	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	store, err = openSocketStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store.Close()

	third, err := store.EnqueueStatsSample(time.Now().UTC(), map[string][2]int64{
		"user@example.com": {10, 20},
	})
	if err != nil {
		t.Fatalf("counter reset sample: %v", err)
	}
	assertStatsEnvelope(t, third, "user@example.com", 10, 20)
}

func TestSocketStoreCoalescesLatestSnapshot(t *testing.T) {
	store, err := openSocketStore(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	first, err := store.EnqueueLatest(socketproto.TypeOnline, &model.OnlineUsersPush{
		ServerTime: time.Now().UTC(),
		Users:      []model.OnlineUserInfo{{Email: "online@example.com"}},
	})
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	latest, err := store.EnqueueLatest(socketproto.TypeOnline, &model.OnlineUsersPush{
		ServerTime: time.Now().UTC().Add(time.Second),
		Users:      []model.OnlineUserInfo{},
	})
	if err != nil {
		t.Fatalf("enqueue latest: %v", err)
	}
	pending, err := store.Pending(map[string]struct{}{first.ID: {}}, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != latest.ID {
		t.Fatalf("expected only latest snapshot, got %+v", pending)
	}
	var payload model.OnlineUsersPush
	if err := json.Unmarshal(pending[0].Payload, &payload); err != nil {
		t.Fatalf("decode latest online snapshot: %v", err)
	}
	if len(payload.Users) != 0 {
		t.Fatalf("expected offline snapshot, got %+v", payload.Users)
	}
}

func TestSocketStoreRemembersCompletedCommand(t *testing.T) {
	store, err := openSocketStore(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	want := &model.AgentCommandAck{Status: model.AgentCommandAckSucceeded, Result: map[string]any{"mode": "done"}}
	if _, err := store.EnqueueCommandAck("cmd-1", want); err != nil {
		t.Fatalf("enqueue command ack: %v", err)
	}
	got, err := store.CompletedCommandAck("cmd-1")
	if err != nil {
		t.Fatalf("load completed command: %v", err)
	}
	if got == nil || got.Status != want.Status || got.Result["mode"] != "done" {
		t.Fatalf("unexpected completed ack: %+v", got)
	}
}

func assertStatsEnvelope(t *testing.T, envelope *socketproto.Envelope, email string, up, down int64) {
	t.Helper()
	if envelope == nil || envelope.Type != socketproto.TypeStats {
		t.Fatalf("unexpected stats envelope: %+v", envelope)
	}
	var payload model.StatsPush
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode stats payload: %v", err)
	}
	if len(payload.Users) != 1 {
		t.Fatalf("unexpected users: %+v", payload.Users)
	}
	user := payload.Users[0]
	if user.Email != email || user.Uplink != up || user.Downlink != down {
		t.Fatalf("unexpected usage: %+v", user)
	}
}
