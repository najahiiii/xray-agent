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
	routes := []model.RouteRule{
		{Tag: "r1", OutboundTag: "direct", Domain: []string{"domain:example.com"}},
	}
	if s.IsUnchanged(1, clients, routes) {
		t.Fatal("expected mismatch before update")
	}

	s.Update(1, clients, routes)
	if !s.IsUnchanged(1, clients, routes) {
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

	routeSnap := s.RoutesSnapshot()
	if len(routeSnap) != 1 || routeSnap["r1"].OutboundTag != "direct" {
		t.Fatalf("route snapshot mismatch: %+v", routeSnap)
	}

	// ensure changed when routes differ
	changedRoutes := []model.RouteRule{{Tag: "r1", OutboundTag: "blocked"}}
	if s.IsUnchanged(2, clients, changedRoutes) {
		t.Fatal("expected mismatch when routes differ or version changes")
	}
}
