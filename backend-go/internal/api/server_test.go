package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"splitflap-web/internal/config"
	"splitflap-web/internal/mqttclient"
	"splitflap-web/internal/scheduler"
)

// newTestApp wires a Server with a scheduler that is never started, so
// AddMessage succeeds without touching MQTT.
func newTestApp() *fiber.App {
	cfg := config.Load("nonexistent.conf")
	mq := mqttclient.New("localhost", 11883, "test-client", "test/state")
	sched := scheduler.New(mq, "test/set", cfg.DefaultDisplayDuration, cfg.DefaultTargetDisplayCount, cfg.DisplayWidth, "WELCOME", "keep", 1)
	return New(&Server{Cfg: cfg, MQTT: mq, Scheduler: sched})
}

func TestPublishAlignInvalid(t *testing.T) {
	app := newTestApp()

	req := httptest.NewRequest("POST", "/api/publish",
		bytes.NewBufferString(`{"text":"HI","align":"middle"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["detail"] != "align must be 'left', 'center' or 'right'" {
		t.Errorf("detail = %q", body["detail"])
	}
}

func TestPublishAlignAccepted(t *testing.T) {
	app := newTestApp()

	req := httptest.NewRequest("POST", "/api/publish",
		bytes.NewBufferString(`{"text":"31C","align":"center"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || out.ID == "" {
		t.Errorf("response = %+v", out)
	}
}

// Backward compatibility: a legacy payload without align must behave exactly
// as before (accepted, no padding requested).
func TestPublishLegacyPayloadNoAlign(t *testing.T) {
	app := newTestApp()

	req := httptest.NewRequest("POST", "/api/publish",
		bytes.NewBufferString(`{"text":"HELLO"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// An empty text is accepted and queued (it renders as a blank display).
func TestPublishEmptyAccepted(t *testing.T) {
	app := newTestApp()

	req := httptest.NewRequest("POST", "/api/publish",
		bytes.NewBufferString(`{"text":""}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || out.ID == "" {
		t.Errorf("response = %+v", out)
	}
}

// The legacy `payload` field is no longer used: a payload-only request is
// treated as an empty message (renders as a blank display).
func TestPublishPayloadFieldIgnored(t *testing.T) {
	app := newTestApp()

	req := httptest.NewRequest("POST", "/api/publish",
		bytes.NewBufferString(`{"payload":"HI"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
