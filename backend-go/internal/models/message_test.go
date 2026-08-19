package models

import (
	"sync"
	"testing"
	"time"
)

// TestMessageConcurrentReadWrite hammers the mutable display-state fields
// (Status, DisplayCount, LastDisplayedAt) from concurrent writer and reader
// goroutines. This exercises the same producers/consumers as the live system
// (scheduler MarkDisplayed/MarkCompleted vs HTTP ToDTO snapshots). Run with
// -race to catch a reintroduced data race; without it, this still asserts the
// invariants hold under contention.
func TestMessageConcurrentReadWrite(t *testing.T) {
	m := NewMessage("HELLO", 5, 1, PriorityNormal, "alice", "")

	var wg sync.WaitGroup

	// Writers: a few goroutines alternate displayed/completed transitions.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				m.MarkDisplayed(time.Now())
				if i%5 == 0 {
					m.MarkCompleted()
				}
			}
		}()
	}

	// Readers: snapshot the message and read the current mutable state.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				d := m.ToDTO()
				if d.Message != "HELLO" {
					t.Errorf("immutable Message field torn: %q", d.Message)
				}
				// A torn read of DisplayCount can only ever be observed as
				// some intermediate non-negative value; an out-of-range
				// value indicates a field tearing on a non-atomic width.
				if d.DisplayCount < 0 {
					t.Errorf("torn DisplayCount read: %d", d.DisplayCount)
				}
				// Exercise the completion check path used by the scheduler.
				if m.IsCompleted() {
					_ = m.DisplayCountSafe()
				}
			}
		}()
	}

	wg.Wait()
}
