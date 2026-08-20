// Package models defines the data structures used by the scheduler,
// store, and HTTP API.
package models

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MessageStatus enumerates message lifecycle states.
type MessageStatus string

const (
	StatusPending   MessageStatus = "Pending"
	StatusActive    MessageStatus = "Active"
	StatusCompleted MessageStatus = "Completed"
)

// Priority enumerates message priority levels. A higher rank wins during
// selection.
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// PriorityRank maps a priority to its numeric rank (0=normal, 1=high).
func PriorityRank(p Priority) int {
	if p == PriorityHigh {
		return 1
	}
	return 0
}

// ParsePriority parses and validates a priority string.
func ParsePriority(s string) (Priority, bool) {
	switch strings.ToLower(s) {
	case "normal", "":
		return PriorityNormal, true
	case "high":
		return PriorityHigh, true
	default:
		return "", false
	}
}

// ParseAlign validates an alignment value. Empty or "left" means no padding
// (the current behaviour); "center" and "right" pad the text to the display
// width at publish time. Unknown values return ok=false.
func ParseAlign(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "left":
		return "", true
	case "center":
		return "center", true
	case "right":
		return "right", true
	default:
		return "", false
	}
}

// Message represents a single queue entry destined for the display.
//
// The fields Status, DisplayCount and LastDisplayedAt are mutated by the
// scheduler goroutine (when a message is shown) and by the HTTP handler (when
// a message is deleted), while concurrent readers (ToDTO, queue/history
// snapshots) run in other goroutines. mu guards only those three mutable
// fields; the remaining fields are set once at construction and never change,
// so they are safe to read without the lock.
type Message struct {
	// mu guards Status, DisplayCount and LastDisplayedAt.
	mu sync.RWMutex

	ID                 string        `json:"id"`
	Message            string        `json:"message"`
	CreatedAt          time.Time     `json:"-"`
	Status             MessageStatus `json:"status"`
	DisplayDuration    int           `json:"displayDuration"`
	TargetDisplayCount int           `json:"targetDisplayCount"`
	DisplayCount       int           `json:"displayCount"`
	LastDisplayedAt    *time.Time    `json:"-"`
	Priority           Priority      `json:"priority"`
	User               string        `json:"user"`
	// Align is a publish-time rendering hint ("", "center" or "right"); it is
	// not part of the JSON API surface.
	Align string `json:"-"`
}

// MarkDisplayed records that the message was just shown once: it stamps
// LastDisplayedAt, increments DisplayCount, and promotes Pending -> Active
// on first display. Callers must hold no other Message lock.
func (m *Message) MarkDisplayed(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastDisplayedAt = &now
	m.DisplayCount++
	if m.Status == StatusPending {
		m.Status = StatusActive
	}
}

// MarkCompleted transitions the message to Completed. Safe for concurrent
// use with MarkDisplayed and readers.
func (m *Message) MarkCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Status = StatusCompleted
}

// DisplayCount returns the current display count (reading under lock).
func (m *Message) DisplayCountSafe() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.DisplayCount
}

// IsCompleted reports whether the message has reached the Completed state.
func (m *Message) IsCompleted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Status == StatusCompleted
}

// StatusSafe returns the current status (reading under lock).
func (m *Message) StatusSafe() MessageStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Status
}

// SetDisplayCount sets the display count to n (writing under lock).
// It is intended for tests that need to simulate a specific count without
// going through MarkDisplayed.
func (m *Message) SetDisplayCount(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DisplayCount = n
}

// NewMessage constructs a Message with sensible defaults, generating a
// new UUID and the current timestamp.
func NewMessage(text string, targetDisplayCount, displayDuration int, priority Priority, user, align string) *Message {
	return &Message{
		ID:                 uuid.NewString(),
		Message:            text,
		CreatedAt:          time.Now(),
		Status:             StatusPending,
		DisplayDuration:    displayDuration,
		TargetDisplayCount: targetDisplayCount,
		DisplayCount:       0,
		LastDisplayedAt:    nil,
		Priority:           priority,
		User:               user,
		Align:              align,
	}
}

// MessageDTO is the JSON-serialisable representation described by the spec.
type MessageDTO struct {
	ID                 string        `json:"id"`
	Message            string        `json:"message"`
	CreatedAt          string        `json:"createdAt"`
	Status             MessageStatus `json:"status"`
	DisplayDuration    int           `json:"displayDuration"`
	TargetDisplayCount int           `json:"targetDisplayCount"`
	DisplayCount       int           `json:"displayCount"`
	LastDisplayedAt    *string        `json:"lastDisplayedAt"`
	LastDisplayedTime  *string        `json:"lastDisplayedTime"`
	Priority           Priority      `json:"priority"`
	User               string        `json:"user"`
}

const (
	// RFC3339NoOffset is a compact ISO 8601 format for serialisation.
	isoFormat = "2006-01-02T15:04:05"
)

// ToDTO converts a Message to its JSON DTO form. It reads the mutable fields
// under the read lock so concurrent scheduler/delete mutations don't tear reads.
func (m *Message) ToDTO() MessageDTO {
	m.mu.RLock()
	defer m.mu.RUnlock()
	createdAt := m.CreatedAt.Format(isoFormat)
	var lastAt *string
	var lastTime *string
	if m.LastDisplayedAt != nil {
		la := m.LastDisplayedAt.Format(isoFormat)
		lt := m.LastDisplayedAt.Format("15:04")
		lastAt = &la
		lastTime = &lt
	}
	return MessageDTO{
		ID:                 m.ID,
		Message:            m.Message,
		CreatedAt:          createdAt,
		Status:             m.Status,
		DisplayDuration:    m.DisplayDuration,
		TargetDisplayCount: m.TargetDisplayCount,
		DisplayCount:       m.DisplayCount,
		LastDisplayedAt:    lastAt,
		LastDisplayedTime:  lastTime,
		Priority:           m.Priority,
		User:               m.User,
	}
}

// HistoryEntry is persisted in the scheduler's history ring buffer.
type HistoryEntry struct {
	ID       string   `json:"id"`
	Time     string   `json:"time"`
	User     string   `json:"user"`
	Message  string   `json:"message"`
	Priority Priority `json:"priority"`
}

// NewHistoryEntry constructs a HistoryEntry from a message.
func NewHistoryEntry(m *Message) HistoryEntry {
	return HistoryEntry{
		ID:       m.ID,
		Time:     m.CreatedAt.Format("15:04"),
		User:     m.User,
		Message:  m.Message,
		Priority: m.Priority,
	}
}

// MQTTHistoryEntry is persisted in the MQTT client's history ring buffer.
type MQTTHistoryEntry struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
}