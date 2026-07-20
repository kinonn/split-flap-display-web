// Package queue implements a small bounded queue with "drop oldest then
// retry once" insertion semantics. It is used by the scheduler and MQTT
// client for their pub/sub subscriber queues.
package queue

import "sync"

// Event is a single payload pushed to subscribers. The interpretation of
// the fields is up to the producer; consumers forward them unchanged.
type Event struct {
	// Name is the SSE event name (e.g. "current", "queue").
	Name string
	// Data is the already-serialised JSON payload emitted on the `data:`
	// line of the SSE stream.
	Data []byte
}

// Queue is a thread-safe FIFO ring buffer.
type Queue struct {
	mu  sync.Mutex
	cap int
	buf []Event
	// notifiable is signaled (non-blocking) whenever an item is queued.
	// We use a single buffered channel of size 1 so multiple signals collapse
	// into a single notification.
	notify chan struct{}
}

// New returns a Queue with the given maximum capacity.
func New(capacity int) *Queue {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue{
		cap:    capacity,
		buf:    make([]Event, 0, capacity),
		notify: make(chan struct{}, 1),
	}
}

// Push appends an event. If the queue is full, the oldest entry is dropped
// and the push is retried once.
func (q *Queue) Push(e Event) {
	q.mu.Lock()
	if len(q.buf) < q.cap {
		q.buf = append(q.buf, e)
		q.mu.Unlock()
		q.signal()
		return
	}
	// Drop oldest and retry once.
	if len(q.buf) > 0 {
		q.buf = q.buf[1:]
		q.buf = append(q.buf, e)
	}
	q.mu.Unlock()
	q.signal()
}

func (q *Queue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// Notify returns a channel that receives a value whenever items may be
// available. Multiple pushes may collapse to a single receive here.
func (q *Queue) Notify() <-chan struct{} { return q.notify }

// Pop returns the next event and a ok flag. It does not block.
func (q *Queue) Pop() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.buf) == 0 {
		return Event{}, false
	}
	e := q.buf[0]
	q.buf = q.buf[1:]
	return e, true
}

// Drain returns and removes all currently buffered events.
func (q *Queue) Drain() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.buf
	q.buf = make([]Event, 0, q.cap)
	return out
}

// Len returns the number of buffered events.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf)
}