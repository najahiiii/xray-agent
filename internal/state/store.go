package state

import "github.com/najahiiii/xray-agent/internal/model"

import "sync"

type Store struct {
	mu          sync.RWMutex
	lastVersion int64
	emails      map[string]struct{}
}

func New() *Store {
	return &Store{emails: map[string]struct{}{}}
}

func (s *Store) IsUnchanged(version int64, clients []model.DesiredClient) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if version != s.lastVersion {
		return false
	}
	if len(clients) != len(s.emails) {
		return false
	}
	for _, c := range clients {
		if _, ok := s.emails[c.Email]; !ok {
			return false
		}
	}
	return true
}

func (s *Store) Update(version int64, clients []model.DesiredClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastVersion = version
	emails := make(map[string]struct{}, len(clients))
	for _, c := range clients {
		emails[c.Email] = struct{}{}
	}
	s.emails = emails
}

func (s *Store) Emails() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.emails))
	for email := range s.emails {
		out = append(out, email)
	}
	return out
}
