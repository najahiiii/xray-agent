package state

import (
	"testing"

	"github.com/najahiiii/xray-agent/internal/model"
)

func TestStoreLifecycle(t *testing.T) {
	s := New()

	clients := []model.Client{
		{Proto: "vless", ID: "1", Email: "a"},
		{Proto: "vmess", ID: "2", Email: "b"},
	}
	if s.IsUnchanged(1, clients) {
		t.Fatal("expected mismatch before update")
	}

	s.Update(1, clients)
	if !s.IsUnchanged(1, clients) {
		t.Fatal("expected store to consider state unchanged")
	}

	emails := s.Emails()
	if len(emails) != 2 {
		t.Fatalf("emails len=%d", len(emails))
	}

	snap := s.ClientsSnapshot()
	if len(snap) != 2 || snap["a"].ID != "1" {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
}
