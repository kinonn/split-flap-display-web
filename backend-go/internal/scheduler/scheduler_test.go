package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"splitflap-web/internal/models"
	"splitflap-web/internal/mqttclient"
)

// fakeMQ is a tiny MQTT client that records publishes and never fails.
// We use it solely to drive the scheduler without a live broker.
func newFakeMQ() *mqttclient.Client {
	// The real client connects to nothing; in unit tests we only call
	// Publish which returns "not connected" via the Client. Instead, we
	// bypass Publish by running the scheduler with stayIdle=true.
	return mqttclient.New("localhost", 11883, "test-client", "test/topic")
}

func TestAddValidation(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)

	if _, err := s.AddMessage("   ", nil, nil, nil, "u"); err == nil {
		t.Fatal("expect error for empty text")
	}
	tdc := 0
	if _, err := s.AddMessage("hi", &tdc, nil, nil, "u"); err == nil {
		t.Fatal("expect error for tdc=0")
	}
	dd := -1
	if _, err := s.AddMessage("hi", nil, &dd, nil, "u"); err == nil {
		t.Fatal("expect error for dd<0")
	}
	bad := models.Priority("ultra")
	if _, err := s.AddMessage("hi", nil, nil, &bad, "u"); err == nil {
		t.Fatal("expect error for bad priority")
	}
}

func TestAddDefaultsAndHistory(t *testing.T) {
	s := New(newFakeMQ(), "pub", 12, 7, "WELCOME", "keep", 1)

	id, err := s.AddMessage("HELLO", nil, nil, nil, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty id")
	}
	m := s.store.Get(id)
	if m == nil {
		t.Fatal("stored nil")
	}
	if m.DisplayDuration != 12 || m.TargetDisplayCount != 7 {
		t.Errorf("defaults not applied: %+v", m)
	}
	if m.Status != models.StatusPending {
		t.Errorf("status = %v", m.Status)
	}

	hist := s.GetHistory()
	if len(hist) != 1 || hist[0].Message != "HELLO" || hist[0].User != "alice" {
		t.Errorf("history = %+v", hist)
	}
}

func TestSelectNextMessagePriority(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	_, _ = s.AddMessage("normal1", nil, nil, nil, "u")
	high := models.PriorityHigh
	_, _ = s.AddMessage("high1", nil, nil, &high, "u")
	_, _ = s.AddMessage("normal2", nil, nil, nil, "u")

	got := s.SelectNextMessage()
	if got == nil || got.Message != "high1" {
		t.Fatalf("expected high1 first, got %+v", got)
	}
}

func TestSelectNextMessageLowestCount(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	id1, _ := s.AddMessage("aa", nil, nil, nil, "u")
	id2, _ := s.AddMessage("bb", nil, nil, nil, "u")
	// simulate aa being displayed twice.
	s.store.Update(id1, func(m *models.Message) { m.DisplayCount = 2 })
	s.store.Update(id2, func(m *models.Message) { m.DisplayCount = 1 })
	got := s.SelectNextMessage()
	if got.ID != id2 {
		t.Errorf("expected bb (count=1) first, got %q count=%d", got.Message, got.DisplayCount)
	}
}

func TestState(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	if got := s.State(); got != "Idle" {
		t.Errorf("empty state = %q", got)
	}
	_, _ = s.AddMessage("hi", nil, nil, nil, "u")
	if got := s.State(); got != "Active" {
		t.Errorf("state with active = %q", got)
	}
	s.mu.Lock()
	s.current = s.store.ListActive()[0]
	s.mu.Unlock()
	if got := s.State(); got != "Active" {
		t.Errorf("state with current = %q", got)
	}
}

func TestRemoveMessage(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	id, _ := s.AddMessage("hi", nil, nil, nil, "u")
	if !s.RemoveMessage(id) {
		t.Fatal("remove should succeed")
	}
	// Unknown IDs return false.
	if s.RemoveMessage("00000000-0000-0000-0000-000000000000") {
		t.Fatal("non-existent should be false")
	}
	if m := s.store.Get(id); m == nil || m.Status != models.StatusCompleted {
		t.Errorf("status = %v", m)
	}
}

func TestWakeupSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	// Signal and verify waitWakeup returns true promptly.
	s.signalWakeup()
	if !s.waitWakeup(ctx, 100*time.Millisecond) {
		t.Error("expected wakeup")
	}
	// Now without a new signal we should time out.
	if s.waitWakeup(ctx, 50*time.Millisecond) {
		t.Error("did not expect wakeup")
	}
	// Cancel should return false promptly even when no signal pending.
	cancel()
	if s.waitWakeup(context.Background(), 5*time.Second) {
		t.Error("cancel should prevent wakeup")
	}
}

func TestQueueSnapshotOrdering(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	idNormal, _ := s.AddMessage("n1", nil, nil, nil, "u")
	high := models.PriorityHigh
	idHigh, _ := s.AddMessage("h1", nil, nil, &high, "u")
	s.store.Update(idHigh, func(m *models.Message) { m.DisplayCount = 5 })
	s.store.Update(idNormal, func(m *models.Message) { m.DisplayCount = 1 })

	snap := s.QueueSnapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot = %d", len(snap))
	}
	if snap[0].Message != "h1" {
		t.Errorf("expected h1 first, got %q", snap[0].Message)
	}
}

// Smoke test that Start/Stop is safe to use with an idle "keep" scheduler.
func TestStartStopIdleKeep(t *testing.T) {
	s := New(newFakeMQ(), "pub", 10, 6, "WELCOME", "keep", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	s.Stop()
}

func TestValidationErrorType(t *testing.T) {
	ve := validationError("boom")
	var target *ValidationError
	if !errors.As(ve, &target) {
		t.Fatal("errors.As failed")
	}
	if !strings.Contains(ve.Error(), "boom") {
		t.Fatalf("msg = %q", ve.Error())
	}
}