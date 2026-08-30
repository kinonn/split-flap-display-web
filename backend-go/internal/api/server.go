// Package api implements the HTTP surface of the split-flap backend
// using Fiber: REST endpoints, static file serving, and an SSE stream.
package api

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"

	"splitflap-web/internal/config"
	"splitflap-web/internal/models"
	"splitflap-web/internal/mqttclient"
	"splitflap-web/internal/queue"
	"splitflap-web/internal/scheduler"
)

// Server holds the dependencies passed to every handler.
type Server struct {
	Cfg       config.Config
	MQTT      *mqttclient.Client
	Scheduler *scheduler.Scheduler
	// Spawning a staticDir as explicit field for clarity.
	StaticDir string

	// app is the Fiber application created by New; used by Shutdown.
	app *fiber.App

	// sseMu guards sseCancels, which tracks the cancel function of every
	// active SSE stream so they can be cancelled on shutdown. Entries are
	// keyed by the stream's ctx.Done() channel (comparable) so duplicate
	// registration/unregistration is safe.
	sseMu      sync.Mutex
	sseCancels map[<-chan struct{}]context.CancelFunc
}

// Shutdown gracefully stops the HTTP server. Active SSE streams are
// cancelled first: Fiber's Shutdown waits for all live connections to
// finish, and SSE connections are long-lived by design, so without this
// shutdown would block forever whenever a client is connected.
func (s *Server) Shutdown() error {
	s.sseMu.Lock()
	for _, cancel := range s.sseCancels {
		cancel()
	}
	s.sseCancels = nil
	s.sseMu.Unlock()
	return s.app.ShutdownWithTimeout(5 * time.Second)
}

// slowRequestThreshold is the latency above which a successful request is
// considered noteworthy and logged. Ordinary fast requests are not logged at
// all; only errors (non-2xx) and slow requests appear in the access log.
const slowRequestThreshold = 500 * time.Millisecond

// requestLogger emits an access log line only for requests that are either
// unsuccessful (status >= 400) or unusually slow, keeping the logs quiet while
// preserving error and performance visibility. The SSE stream is long-lived by
// design, so it is excluded from the slow-request log (but still logged if it
// fails with a non-2xx status).
func requestLogger() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		latency := time.Since(start)

		isSSE := c.Path() == "/api/scheduler/stream"
		if status >= 400 || (latency > slowRequestThreshold && !isSSE) {
			log.Printf("%s | %d | %s | %s | %s | %s",
				time.Now().Format("15:04:05"), status, latency,
				c.IP(), c.Method(), c.Path())
		}
		return err
	}
}

// New returns a Fiber app with all routes wired up.
func New(s *Server) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "split-flap-web",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // streaming endpoints cannot have a write timeout
		IdleTimeout:  60 * time.Second,
	})
	app.Use(requestLogger())

	app.Get("/api/config", s.handleConfig)
	app.Post("/api/publish", s.handlePublish)
	app.Get("/api/messages/current", s.handleCurrent)
	app.Get("/api/messages/display-state", s.handleDisplayState)
	app.Delete("/api/messages/:id", s.handleDeleteMessage)
	app.Get("/api/scheduler/status", s.handleSchedulerStatus)
	app.Get("/api/scheduler/stream", s.handleSSE)

	if s.StaticDir != "" {
		app.Static("/static", s.StaticDir)
		app.Get("/", func(c *fiber.Ctx) error {
			return c.SendFile(s.StaticDir + "/index.html")
		})
	}
	s.app = app
	return app
}

// --- Handlers ----------------------------------------------------------

func (s *Server) handleConfig(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"publish_topic":                s.Cfg.PublishTopic,
		"subscribe_topic":              s.Cfg.SubscribeTopic,
		"broker_host":                  s.Cfg.MQTTBrokerHost,
		"connected":                    s.MQTT.Connected(),
		"default_display_duration":     s.Cfg.DefaultDisplayDuration,
		"default_target_display_count": s.Cfg.DefaultTargetDisplayCount,
		"idle_message":                 s.Cfg.IdleMessage,
		"idle_mode":                    s.Cfg.IdleMode,
		"scheduler_enabled":            s.Cfg.SchedulerEnabled,
	})
}

type publishRequest struct {
	Text               string  `json:"text"`
	TargetDisplayCount *int    `json:"target_display_count"`
	DisplayDuration    *int    `json:"display_duration"`
	Priority           *string `json:"priority"`
	Align              *string `json:"align"`
}

func (s *Server) handlePublish(c *fiber.Ctx) error {
	var req publishRequest
	body := bytes.TrimSpace(c.Body())
	if len(body) > 0 {
		if strings.HasPrefix(strings.ToLower(string(c.Request().Header.ContentType())), "text/plain") {
			// Raw text bodies are used verbatim as the message text.
			req.Text = string(body)
		} else if err := c.BodyParser(&req); err != nil {
			// A non-empty body that cannot be parsed must not be silently
			// queued as an empty message: that would wipe the physical
			// display while reporting success to the caller.
			return sendError(c, 400, "invalid JSON body")
		}
	}

	// The message text is used exactly as received: no trimming, no fallback.
	// An empty string is valid and displays as a blank (space-filled) display.
	text := req.Text

	var priority *models.Priority
	if req.Priority != nil {
		p, ok := models.ParsePriority(*req.Priority)
		if !ok {
			return sendError(c, 400, "priority must be 'normal' or 'high'")
		}
		pp := p
		priority = &pp
	}

	// align is optional; default (nil/empty/"left") means publish as-is.
	// Validation happens in the scheduler, but we pre-validate here for a
	// specific 400 message.
	if req.Align != nil {
		if _, ok := models.ParseAlign(*req.Align); !ok {
			return sendError(c, 400, "align must be 'left', 'center' or 'right'")
		}
	}

	user := "unknown"
	if email := c.Get("Cf-Access-Authenticated-User-Email"); email != "" {
		if i := strings.Index(email, "@"); i > 0 {
			// strings.Clone owns the bytes: c.Get returns a byte-string that
			// aliases fasthttp's reused header buffer, so a bare subslice
			// would silently pick up later requests' header data.
			user = strings.Clone(email[:i])
		} else {
			user = strings.Clone(email)
		}
	}

	var align string
	if req.Align != nil {
		align = *req.Align
	}
	id, err := s.Scheduler.AddMessage(text, req.TargetDisplayCount, req.DisplayDuration, priority, user, align)
	if err != nil {
		ve, ok := err.(*scheduler.ValidationError)
		var qfe *scheduler.QueueFullError
		switch {
		case ok:
			return sendError(c, 400, ve.Error())
		case errors.As(err, &qfe):
			return sendError(c, 429, qfe.Error())
		default:
			return sendError(c, 500, err.Error())
		}
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

func (s *Server) handleCurrent(c *fiber.Ctx) error {
	m := s.Scheduler.GetCurrentMessage()
	if m == nil {
		return c.Status(200).JSON(nil)
	}
	return c.JSON(m.ToDTO())
}

func (s *Server) handleDisplayState(c *fiber.Ctx) error {
	msg, ok := s.MQTT.GetLatestMessage()
	if !ok {
		return c.Status(200).JSON(nil)
	}
	return c.JSON(fiber.Map{"message": msg})
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Server) handleDeleteMessage(c *fiber.Ctx) error {
	id := c.Params("id")
	if !uuidRe.MatchString(id) {
		return sendError(c, 400, "invalid uuid")
	}
	if !s.Scheduler.RemoveMessage(id) {
		return sendError(c, 404, "message not found")
	}
	return c.JSON(fiber.Map{"status": "ok"})
}

func (s *Server) handleSchedulerStatus(c *fiber.Ctx) error {
	current := s.Scheduler.GetCurrentMessage()
	var curr interface{}
	if current != nil {
		dto := current.ToDTO()
		curr = &dto
	}
	return c.JSON(fiber.Map{
		"state":             s.Scheduler.State(),
		"current":           curr,
		"queueSize":         len(s.Scheduler.GetActiveMessages()),
		"highPriorityCount": s.Scheduler.HighPriorityCount(),
	})
}

// --- SSE ----------------------------------------------------------------

func (s *Server) handleSSE(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	schedQ := s.Scheduler.SubscribeQueue()
	mqttQ := s.MQTT.SubscribeDisplayState()

	merged := queue.New(1000)

	ctx, cancel := context.WithCancel(context.Background())
	s.registerSSE(ctx.Done(), cancel)

	// writerStarted is set once fasthttp invokes the stream writer. If the
	// writer never runs (client vanished before the response began), the
	// fallback below cancels the relays and drops the subscriptions after
	// a grace period so they cannot leak. Note: the fasthttp request
	// context must NOT be touched from this goroutine (c.Context().Done()
	// panics once fasthttp has released the RequestCtx).
	var writerStarted atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		if !writerStarted.Load() {
			cancel()
			s.Scheduler.UnsubscribeQueue(schedQ)
			s.MQTT.UnsubscribeDisplayState(mqttQ)
			s.unregisterSSE(ctx.Done())
		}
	}()

	spawnRelay := func(src *queue.Queue) {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-src.Notify():
					for {
						ev, ok := src.Pop()
						if !ok {
							break
						}
						merged.Push(ev)
					}
				}
			}
		}()
	}
	spawnRelay(schedQ)
	spawnRelay(mqttQ)

	c.Response().SetBodyStreamWriter(func(w *bufio.Writer) {
		writerStarted.Store(true)
		// Cleanup happens when the writer function exits (i.e. the
		// client disconnects or the response is finalised).
		defer cancel()
		defer s.unregisterSSE(ctx.Done())
		defer s.Scheduler.UnsubscribeQueue(schedQ)
		defer s.MQTT.UnsubscribeDisplayState(mqttQ)

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		flushErr := func() bool {
			if err := w.Flush(); err != nil {
				log.Printf("sse: flush failed: %v", err)
				return false
			}
			return true
		}
		writeEvent := func(ev queue.Event) bool {
			if _, err := w.WriteString("event: " + ev.Name + "\n"); err != nil {
				return false
			}
			if _, err := w.WriteString("data: "); err != nil {
				return false
			}
			if _, err := w.Write(ev.Data); err != nil {
				return false
			}
			if _, err := w.WriteString("\n\n"); err != nil {
				return false
			}
			return flushErr()
		}
		// Emit any pre-seeded events that are already buffered.
		for {
			ev, ok := merged.Pop()
			if !ok {
				break
			}
			if !writeEvent(ev) {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				// Server shutdown or client context cancelled: end the
				// stream so Shutdown is not blocked by this connection.
				return
			case <-ticker.C:
				if _, err := w.WriteString(":keepalive\n\n"); err != nil {
					return
				}
				if !flushErr() {
					return
				}
			case <-merged.Notify():
				for {
					ev, ok := merged.Pop()
					if !ok {
						break
					}
					if !writeEvent(ev) {
						return
					}
				}
			}
		}
	})
	return nil
}

// registerSSE tracks an active SSE stream's cancel function.
func (s *Server) registerSSE(done <-chan struct{}, cancel context.CancelFunc) {
	s.sseMu.Lock()
	if s.sseCancels == nil {
		s.sseCancels = make(map[<-chan struct{}]context.CancelFunc)
	}
	s.sseCancels[done] = cancel
	s.sseMu.Unlock()
}

// unregisterSSE removes a finished SSE stream's cancel function.
func (s *Server) unregisterSSE(done <-chan struct{}) {
	s.sseMu.Lock()
	delete(s.sseCancels, done)
	s.sseMu.Unlock()
}

// sendError writes a JSON error body matching the FastAPI-style shape the
// frontend expects ({ "detail": "<message>" }).
func sendError(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"detail": msg})
}
