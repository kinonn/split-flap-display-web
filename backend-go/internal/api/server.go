// Package api implements the HTTP surface of the split-flap backend
// using Fiber: REST endpoints, static file serving, and an SSE stream.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"

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
}

// New returns a Fiber app with all routes wired up.
func New(s *Server) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "split-flap-web",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // streaming endpoints cannot have a write timeout
		IdleTimeout:  60 * time.Second,
	})
	app.Use(logger.New())

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
	return app
}

// --- Handlers ----------------------------------------------------------

func (s *Server) handleConfig(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"publish_topic":               s.Cfg.PublishTopic,
		"subscribe_topic":             s.Cfg.SubscribeTopic,
		"broker_host":                 s.Cfg.MQTTBrokerHost,
		"connected":                  s.MQTT.Connected(),
		"default_display_duration":    s.Cfg.DefaultDisplayDuration,
		"default_target_display_count": s.Cfg.DefaultTargetDisplayCount,
		"idle_message":               s.Cfg.IdleMessage,
		"idle_mode":                  s.Cfg.IdleMode,
		"scheduler_enabled":           s.Cfg.SchedulerEnabled,
	})
}

type publishRequest struct {
	Text                string  `json:"text"`
	Payload            string  `json:"payload"`
	TargetDisplayCount *int    `json:"target_display_count"`
	DisplayDuration    *int    `json:"display_duration"`
	Priority           *string `json:"priority"`
}

var emailRe = regexp.MustCompile(`^[^@]+@`)

func (s *Server) handlePublish(c *fiber.Ctx) error {
	var req publishRequest
	if err := c.BodyParser(&req); err != nil {
		// Some clients post empty bodies or raw text; tolerate parse errors.
		_ = err
	}

	// Determine text: prefer "text", fall back to "payload".
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = strings.TrimSpace(req.Payload)
	}

	var priority *models.Priority
	if req.Priority != nil {
		p, ok := models.ParsePriority(*req.Priority)
		if !ok {
			return c.Status(400).SendString("priority must be 'normal' or 'high'")
		}
		pp := p
		priority = &pp
	}

	user := "unknown"
	if email := c.Get("Cf-Access-Authenticated-User-Email"); email != "" {
		if i := strings.Index(email, "@"); i > 0 {
			user = email[:i]
		} else {
			user = email
		}
	}

	id, err := s.Scheduler.AddMessage(text, req.TargetDisplayCount, req.DisplayDuration, priority, user)
	if err != nil {
		ve, ok := err.(*scheduler.ValidationError)
		switch {
		case ok:
			return c.Status(400).SendString(ve.Error())
		case text == "":
			return c.Status(400).SendString("text must be non-empty")
		default:
			return c.Status(500).SendString(err.Error())
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
		return c.Status(400).SendString("invalid uuid")
	}
	if !s.Scheduler.RemoveMessage(id) {
		return c.Status(404).SendString("message not found")
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
		"state":              s.Scheduler.State(),
		"current":            curr,
		"queueSize":          len(s.Scheduler.GetActiveMessages()),
		"highPriorityCount":  s.Scheduler.HighPriorityCount(),
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
		// Cleanup happens when the writer function exits (i.e. the
		// client disconnects or the response is finalised).
		defer cancel()
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

// helper for marshaling null responses in handlers.
func toJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}