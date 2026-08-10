// Package store provides a concurrency-safe in-memory store for messages.
package store

import (
	"sync"

	"splitflap-web/internal/models"
)

// Store is a thread-safe map of message ID -> *Message.
type Store struct {
	muByID    sync.Mutex
	byID      map[string]*models.Message
}

// New returns an empty Store.
func New() *Store {
	return &Store{byID: make(map[string]*models.Message)}
}

// Add stores a message keyed by its ID. Existing IDs are overwritten.
// The stored pointer is shared; callers must take the lock via UpdateFields
// to mutate fields.
func (s *Store) Add(m *models.Message) {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	s.byID[m.ID] = m
}

// Get returns the message with the given ID or nil.
func (s *Store) Get(id string) *models.Message {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	return s.byID[id]
}

// ListAll returns a slice of all messages (pointers). The returned slice is
// safe to read concurrently but the contained Message pointers should be
// treated as snapshots.
func (s *Store) ListAll() []*models.Message {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	out := make([]*models.Message, 0, len(s.byID))
	for _, m := range s.byID {
		out = append(out, m)
	}
	return out
}

// ListActive returns all messages whose status is not Completed.
func (s *Store) ListActive() []*models.Message {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	out := make([]*models.Message, 0, len(s.byID))
	for _, m := range s.byID {
		if m.Status != models.StatusCompleted {
			out = append(out, m)
		}
	}
	return out
}

// MarkCompleted sets the status of the message with the given ID to
// Completed. It returns false if the ID is not present.
func (s *Store) MarkCompleted(id string) bool {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	m, ok := s.byID[id]
	if !ok {
		return false
	}
	m.Status = models.StatusCompleted
	return true
}

// Update runs fn under the store lock while holding a per-message lock so
// that callers can mutate multiple fields atomically. It is a no-op if the
// message does not exist. The fn return value is ignored.
func (s *Store) Update(id string, fn func(m *models.Message)) bool {
	s.muByID.Lock()
	defer s.muByID.Unlock()
	m, ok := s.byID[id]
	if !ok {
		return false
	}
	fn(m)
	return true
}