package state

import (
	"sync"

	"github.com/najahiiii/xray-agent/internal/model"
)

type Store struct {
	mu          sync.RWMutex
	lastVersion int64
	clients     map[string]model.DesiredClient
}

func New() *Store {
	return &Store{clients: map[string]model.DesiredClient{}}
}

func (s *Store) IsUnchanged(version int64, clients []model.DesiredClient) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if version != s.lastVersion || len(clients) != len(s.clients) {
		return false
	}
	for _, c := range clients {
		if existing, ok := s.clients[c.Email]; !ok || !equalClient(existing, c) {
			return false
		}
	}
	return true
}

func (s *Store) Update(version int64, clients []model.DesiredClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[string]model.DesiredClient, len(clients))
	for _, c := range clients {
		next[c.Email] = c
	}
	s.lastVersion = version
	s.clients = next
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

func (s *Store) ClientsSnapshot() map[string]model.DesiredClient {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]model.DesiredClient, len(s.clients))
	for email, client := range s.clients {
		snapshot[email] = client
	}
	return snapshot
}

func equalClient(a, b model.DesiredClient) bool {
	return a.Proto == b.Proto && a.ID == b.ID && a.Password == b.Password
}
