package state

import (
	"slices"
	"sync"

	"github.com/najahiiii/xray-agent/internal/model"
)

type Store struct {
	mu          sync.RWMutex
	lastVersion int64
	clients     map[string]model.Client
	routes      map[string]model.RouteRule
}

func New() *Store {
	return &Store{
		lastVersion: -1,
		clients:     map[string]model.Client{},
		routes:      map[string]model.RouteRule{},
	}
}

func (s *Store) IsUnchanged(version int64, clients []model.Client, routes []model.RouteRule) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if version != s.lastVersion || len(clients) != len(s.clients) || len(routes) != len(s.routes) {
		return false
	}
	for _, c := range clients {
		if existing, ok := s.clients[c.Email]; !ok || !equalClient(existing, c) {
			return false
		}
	}
	for _, r := range routes {
		if existing, ok := s.routes[r.Tag]; !ok || !equalRoute(existing, r) {
			return false
		}
	}
	return true
}

func (s *Store) Update(version int64, clients []model.Client, routes []model.RouteRule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[string]model.Client, len(clients))
	for _, c := range clients {
		next[c.Email] = c
	}
	nextRoutes := make(map[string]model.RouteRule, len(routes))
	for _, r := range routes {
		nextRoutes[r.Tag] = r
	}
	s.lastVersion = version
	s.clients = next
	s.routes = nextRoutes
}

func (s *Store) Emails() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	emails := make([]string, 0, len(s.clients))
	for email := range s.clients {
		emails = append(emails, email)
	}
	return emails
}

func (s *Store) ClientsSnapshot() map[string]model.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]model.Client, len(s.clients))
	for email, client := range s.clients {
		snapshot[email] = client
	}
	return snapshot
}

func (s *Store) RoutesSnapshot() map[string]model.RouteRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]model.RouteRule, len(s.routes))
	for tag, route := range s.routes {
		snapshot[tag] = route
	}
	return snapshot
}

func equalClient(a, b model.Client) bool {
	return a.Proto == b.Proto && a.ID == b.ID && a.Password == b.Password
}

func equalRoute(a, b model.RouteRule) bool {
	return a.Tag == b.Tag &&
		a.OutboundTag == b.OutboundTag &&
		a.BalancerTag == b.BalancerTag &&
		a.Port == b.Port &&
		a.SourcePort == b.SourcePort &&
		slicesEqual(a.Domain, b.Domain) &&
		slicesEqual(a.IP, b.IP) &&
		slicesEqual(a.InboundTag, b.InboundTag) &&
		slicesEqual(a.Protocol, b.Protocol)
}

func slicesEqual(a, b []string) bool {
	return slices.Equal(a, b)
}
