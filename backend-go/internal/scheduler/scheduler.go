// Package scheduler implements the queueing, MQTT publishing and idle
// behaviour of the split-flap display backend.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"splitflap-web/internal/models"
	"splitflap-web/internal/mqttclient"
	"splitflap-web/internal/queue"
	"splitflap-web/internal/store"
)

// ErrInvalidInput is returned by AddMessage for invalid arguments.
var ErrInvalidInput = errors.New("invalid input")

// Scheduler is the scheduling core.
type Scheduler struct {
	mq                    *mqttclient.Client
	publishTopic          string
	defaultDisplayDur     int
	defaultTargetCount    int
	idleMessage           string
	idleMode              string
	idleInterval          int

	store                 *store.Store
	mu                    sync.Mutex
	current               *models.Message
	wakeup                chan struct{}

	// SSE subscriber set.
	subsMu sync.Mutex
	subs   map[*queue.Queue]struct{}

	// History ring buffer, max 50, prepend.
	histMu sync.Mutex
	hist   []models.HistoryEntry

	running atomic.Bool
	cancel   context.CancelFunc
	done     chan struct{}
}

// New constructs a new scheduler. The scheduler is not started; call Start.
func New(mq *mqttclient.Client,
	publishTopic string,
	defaultDisplayDur, defaultTargetCount int,
	idleMessage, idleMode string,
	idleInterval int,
) *Scheduler {
	return &Scheduler{
		mq:                 mq,
		publishTopic:       publishTopic,
		defaultDisplayDur:  defaultDisplayDur,
		defaultTargetCount: defaultTargetCount,
		idleMessage:        idleMessage,
		idleMode:           idleMode,
		idleInterval:       idleInterval,
		store:              store.New(),
		wakeup:             make(chan struct{}, 1),
		subs:               make(map[*queue.Queue]struct{}),
		hist:               make([]models.HistoryEntry, 0, 50),
		done:               make(chan struct{}),
	}
}

// --- Public accessors ------------------------------------------------------

// GetActiveMessages returns the list of non-Completed messages.
func (s *Scheduler) GetActiveMessages() []*models.Message {
	return s.store.ListActive()
}

// GetAllMessages returns every stored message.
func (s *Scheduler) GetAllMessages() []*models.Message {
	return s.store.ListAll()
}

// GetHistory returns a copy of the history ring buffer.
func (s *Scheduler) GetHistory() []models.HistoryEntry {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	out := make([]models.HistoryEntry, len(s.hist))
	copy(out, s.hist)
	return out
}

// GetCurrentMessage returns the currently-displayed message, or nil.
func (s *Scheduler) GetCurrentMessage() *models.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// State returns "Active" if a message is current or any message is active;
// otherwise "Idle".
func (s *Scheduler) State() string {
	s.mu.Lock()
	hasCurrent := s.current != nil
	s.mu.Unlock()
	if hasCurrent {
		return "Active"
	}
	if len(s.store.ListActive()) > 0 {
		return "Active"
	}
	return "Idle"
}

// HighPriorityCount returns the number of non-Completed high-priority
// messages.
func (s *Scheduler) HighPriorityCount() int {
	count := 0
	for _, m := range s.store.ListActive() {
		if m.Priority == models.PriorityHigh {
			count++
		}
	}
	return count
}

// QueueSnapshot returns the active messages sorted in queue order.
func (s *Scheduler) QueueSnapshot() []models.MessageDTO {
	active := s.store.ListActive()
	dtos := make([]models.MessageDTO, 0, len(active))
	for _, m := range active {
		dtos = append(dtos, m.ToDTO())
	}
	// Sort by queue order: high priority first, then display_count asc,
	// then created_at asc.
	sort.SliceStable(dtos, func(i, j int) bool {
		ri := prioritySortRank(models.Priority(dtos[i].Priority))
		rj := prioritySortRank(models.Priority(dtos[j].Priority))
		if ri != rj {
			return ri < rj
		}
		if dtos[i].DisplayCount != dtos[j].DisplayCount {
			return dtos[i].DisplayCount < dtos[j].DisplayCount
		}
		return dtos[i].CreatedAt < dtos[j].CreatedAt
	})
	return dtos
}

// prioritySortRank maps high -> 0, normal -> 1 (used for ascending sort).
func prioritySortRank(p models.Priority) int {
	if p == models.PriorityHigh {
		return 0
	}
	return 1
}

// HistorySnapshot returns DTOs of all history entries (already-sorted
// format: most recent first).
func (s *Scheduler) HistorySnapshot() []models.HistoryEntry {
	return s.GetHistory()
}

// --- Message ops ----------------------------------------------------------

// AddMessage validates the input, creates a new Message in the store,
// records history, wakes the scheduler if idle, and notifies SSE
// subscribers. It returns the new message ID and a validation error.
func (s *Scheduler) AddMessage(text string, targetDisplayCount, displayDuration *int, priority *models.Priority, user string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", validationError("text must be non-empty")
	}
	tdc := s.defaultTargetCount
	if targetDisplayCount != nil {
		if *targetDisplayCount <= 0 {
			return "", validationError("target_display_count must be > 0")
		}
		tdc = *targetDisplayCount
	}
	dd := s.defaultDisplayDur
	if displayDuration != nil {
		if *displayDuration <= 0 {
			return "", validationError("display_duration must be > 0")
		}
		dd = *displayDuration
	}
	pr := models.PriorityNormal
	if priority != nil {
		if *priority != models.PriorityNormal && *priority != models.PriorityHigh {
			return "", validationError("priority must be 'normal' or 'high'")
		}
		pr = *priority
	}

	m := models.NewMessage(strings.TrimSpace(text), tdc, dd, pr, user)
	s.store.Add(m)
	s.recordHistory(m)
	s.signalWakeup()

	subs := s.collectSubscribers()
	qData, _ := json.Marshal(struct {
		Type     string                `json:"type"`
		Messages []models.MessageDTO   `json:"messages"`
	}{Type: "queue", Messages: s.QueueSnapshot()})
	hData, _ := json.Marshal(struct {
		Type     string                `json:"type"`
		Messages []models.HistoryEntry `json:"messages"`
	}{Type: "history", Messages: s.HistorySnapshot()})
	for _, q := range subs {
		q.Push(queue.Event{Name: "queue", Data: qData})
		q.Push(queue.Event{Name: "history", Data: hData})
	}

	return m.ID, nil
}

// RemoveMessage marks the message as Completed. Returns true if found.
func (s *Scheduler) RemoveMessage(id string) bool {
	ok := s.store.MarkCompleted(id)
	if !ok {
		return false
	}
	subs := s.collectSubscribers()
	qData, _ := json.Marshal(struct {
		Type     string              `json:"type"`
		Messages []models.MessageDTO `json:"messages"`
	}{Type: "queue", Messages: s.QueueSnapshot()})
	for _, q := range subs {
		q.Push(queue.Event{Name: "queue", Data: qData})
	}
	return true
}

// --- SSE subscriber system -----------------------------------------------

// SubscribeQueue registers a queue to receive SSE lifecycle events.
// The queue is seeded with current/queue/history snapshots.
func (s *Scheduler) SubscribeQueue() *queue.Queue {
	q := queue.New(500)
	s.mu.Lock()
	curr := s.currentSnapshot()
	s.mu.Unlock()
	currData, _ := marshalCurrent(curr)
	qData, _ := json.Marshal(struct {
		Type     string              `json:"type"`
		Messages []models.MessageDTO `json:"messages"`
	}{Type: "queue", Messages: s.QueueSnapshot()})
	hData, _ := json.Marshal(struct {
		Type     string                `json:"type"`
		Messages []models.HistoryEntry `json:"messages"`
	}{Type: "history", Messages: s.HistorySnapshot()})
	q.Push(queue.Event{Name: "current", Data: currData})
	q.Push(queue.Event{Name: "queue", Data: qData})
	q.Push(queue.Event{Name: "history", Data: hData})

	s.subsMu.Lock()
	s.subs[q] = struct{}{}
	s.subsMu.Unlock()
	return q
}

// UnsubscribeQueue removes the given queue from the subscriber set.
func (s *Scheduler) UnsubscribeQueue(q *queue.Queue) {
	s.subsMu.Lock()
	delete(s.subs, q)
	s.subsMu.Unlock()
}

// currentSnapshot returns a MessageDTO or nil marshalled-ready pointer
// along with its raw JSON for the "current" event.
func (s *Scheduler) currentSnapshot() (ptr *models.MessageDTO) {
	if s.current == nil {
		return nil
	}
	dto := s.current.ToDTO()
	return &dto
}

func marshalCurrent(curr *models.MessageDTO) ([]byte, error) {
	return json.Marshal(struct {
		Type    string               `json:"type"`
		Message *models.MessageDTO  `json:"message"`
	}{Type: "current", Message: curr})
}

func (s *Scheduler) collectSubscribers() []*queue.Queue {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	out := make([]*queue.Queue, 0, len(s.subs))
	for q := range s.subs {
		out = append(out, q)
	}
	return out
}

func (s *Scheduler) notify(event queue.Event) {
	for _, q := range s.collectSubscribers() {
		q.Push(event)
	}
}

// --- History -------------------------------------------------------------

func (s *Scheduler) recordHistory(m *models.Message) {
	e := models.NewHistoryEntry(m)
	s.histMu.Lock()
	defer s.histMu.Unlock()
	// Prepend.
	s.hist = append([]models.HistoryEntry{e}, s.hist...)
	if len(s.hist) > 50 {
		s.hist = s.hist[:50]
	}
}

// --- Wake up -------------------------------------------------------------

func (s *Scheduler) signalWakeup() {
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}

// drainWakeup consumes any pending wakeup signal. Must be called only when
// we are sure no wait will be performed immediately afterwards.
func (s *Scheduler) drainWakeup() {
	for {
		select {
		case <-s.wakeup:
		default:
			return
		}
	}
}

// waitWakeup blocks for up to d. Returns true if woken by a new message.
// The provided context lets the runner cancel the wait on shutdown.
func (s *Scheduler) waitWakeup(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.wakeup:
		// Clear remaining signals (buffer size 1 so we are done).
		s.drainWakeup()
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// --- Selection ------------------------------------------------------------

// SelectNextMessage returns the next message to display according to the
// sort key (-priority_rank, display_count, created_at). Returns nil if no
// message is selectable.
func (s *Scheduler) SelectNextMessage() *models.Message {
	active := s.store.ListActive()
	if len(active) == 0 {
		return nil
	}
	sort.SliceStable(active, func(i, j int) bool {
		ai, aj := active[i], active[j]
		ri := -models.PriorityRank(ai.Priority)
		rj := -models.PriorityRank(aj.Priority)
		if ri != rj {
			return ri < rj
		}
		if ai.DisplayCount != aj.DisplayCount {
			return ai.DisplayCount < aj.DisplayCount
		}
		return ai.CreatedAt.Before(aj.CreatedAt)
	})
	return active[0]
}

// --- Tick ---------------------------------------------------------------

// schedulerTick runs a single iteration of the scheduling loop.
func (s *Scheduler) schedulerTick(ctx context.Context) error {
	m := s.SelectNextMessage()
	if m == nil {
		s.handleIdle(ctx)
		return nil
	}

	s.mu.Lock()
	s.current = m
	s.mu.Unlock()

	currData, _ := json.Marshal(struct {
		Type    string              `json:"type"`
		Message *models.MessageDTO  `json:"message"`
	}{Type: "current", Message: ptrToDTO(m)})
	s.notify(queue.Event{Name: "current", Data: currData})

	// Attempt publish.
	if err := s.mq.Publish(s.publishTopic, m.Message, 0); err != nil {
		log.Printf("scheduler: publish failed: %v", err)
		s.mu.Lock()
		s.current = nil
		s.mu.Unlock()
		nullCurr, _ := json.Marshal(struct {
			Type    string             `json:"type"`
			Message *models.MessageDTO `json:"message"`
		}{Type: "current", Message: nil})
		s.notify(queue.Event{Name: "current", Data: nullCurr})
		// backoff
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	// Successful publish: record last_displayed_at, increment count,
	// update status.
	now := time.Now()
	s.store.Update(m.ID, func(mm *models.Message) {
		mm.LastDisplayedAt = &now
		mm.DisplayCount = mm.DisplayCount + 1
		if mm.DisplayCount >= 1 && mm.Status == models.StatusPending {
			mm.Status = models.StatusActive
		}
	})
	// After update, the in-memory `m` is the same stored pointer.
	m.LastDisplayedAt = &now
	m.DisplayCount = m.DisplayCount + 1
	if m.DisplayCount >= 1 && m.Status == models.StatusPending {
		m.Status = models.StatusActive
	}

	// Emit current (with lastDisplayedAt).
	currData2, _ := json.Marshal(struct {
		Type    string              `json:"type"`
		Message *models.MessageDTO  `json:"message"`
	}{Type: "current", Message: ptrToDTO(m)})
	s.notify(queue.Event{Name: "current", Data: currData2})

	// Emit queue snapshot BEFORE the dwell sleep.
	qData, _ := json.Marshal(struct {
		Type     string              `json:"type"`
		Messages []models.MessageDTO `json:"messages"`
	}{Type: "queue", Messages: s.QueueSnapshot()})
	s.notify(queue.Event{Name: "queue", Data: qData})

	// Dwell: sleep full display_duration. NO early wake.
	select {
	case <-time.After(time.Duration(m.DisplayDuration) * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	// After dwell: check completion.
	if m.DisplayCount >= m.TargetDisplayCount {
		s.store.MarkCompleted(m.ID)
		m.Status = models.StatusCompleted
		s.mu.Lock()
		s.current = nil
		s.mu.Unlock()
		qDataDone, _ := json.Marshal(struct {
			Type     string              `json:"type"`
			Messages []models.MessageDTO `json:"messages"`
		}{Type: "queue", Messages: s.QueueSnapshot()})
		s.notify(queue.Event{Name: "queue", Data: qDataDone})
	} else {
		// Refresh history (next tick will reselect based on counts).
		// per spec not needed here.
	}

	// Notify current (after dwell, possibly null).
	s.mu.Lock()
	var curr *models.MessageDTO
	if s.current != nil {
		dto := s.current.ToDTO()
		curr = &dto
	}
	s.mu.Unlock()
	currData3, _ := json.Marshal(struct {
		Type    string             `json:"type"`
		Message *models.MessageDTO `json:"message"`
	}{Type: "current", Message: curr})
	s.notify(queue.Event{Name: "current", Data: currData3})
	return nil
}

// handleIdle is invoked when select_next_message returns null.
func (s *Scheduler) handleIdle(ctx context.Context) {
	s.mu.Lock()
	if s.current != nil {
		s.current = nil
		s.mu.Unlock()
		nullCurr, _ := json.Marshal(struct {
			Type    string             `json:"type"`
			Message *models.MessageDTO `json:"message"`
		}{Type: "current", Message: nil})
		s.notify(queue.Event{Name: "current", Data: nullCurr})
	} else {
		s.mu.Unlock()
	}

	switch s.idleMode {
	case "keep":
		s.waitWakeup(ctx, 1*time.Second)
	case "publish", "":
		if err := s.mq.Publish(s.publishTopic, s.idleMessage, 0); err != nil {
			// Silently ignore idle publish errors.
			_ = err
		}
		s.waitWakeup(ctx, time.Duration(s.idleInterval)*time.Second)
	default:
		// Unknown idle_mode: behave as "keep" to avoid busy-looping.
		s.waitWakeup(ctx, 1*time.Second)
	}
}

// --- Run loop ------------------------------------------------------------

// Start launches the scheduler loop goroutine. Idempotent.
func (s *Scheduler) Start(ctx context.Context) {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go func() {
		defer close(s.done)
		for {
			if err := s.schedulerTick(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Printf("scheduler: tick error: %v", err)
				select {
				case <-time.After(1 * time.Second):
				case <-ctx.Done():
					return
				}
				continue
			}
			// Check for shutdown between ticks: handleIdle returns nil
			// even if the ctx was canceled.
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
}

// Stop cancels the scheduler loop and waits for it to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	<-s.done
}

// --- Helpers -------------------------------------------------------------

func ptrToDTO(m *models.Message) *models.MessageDTO {
	if m == nil {
		return nil
	}
	dto := m.ToDTO()
	return &dto
}

func validationError(msg string) error {
	return &ValidationError{Msg: msg}
}

// ValidationError is returned for invalid user input. Its message is
// intended to be sent verbatim to the HTTP client.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }