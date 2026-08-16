package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http/httptest"
	"strings"
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

// redirectLogOutput captures log output during the test and restores the
// original writer afterwards, so the request-logger assertions can inspect
// what was (or was not) emitted.
func redirectLogOutput(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
}

// A successful, fast request (e.g. GET /api/config) must not produce any
// access-log output.
func TestRequestLoggerSilencesSuccess(t *testing.T) {
	app := newTestApp()

	var buf bytes.Buffer
	redirectLogOutput(t, &buf)

	req := httptest.NewRequest("GET", "/api/config", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if out := buf.String(); out != "" {
		t.Errorf("successful request produced log output: %q", out)
	}
}

// A failed request (e.g. an invalid publish) must produce a single access-log
// line containing the status code and the request path.
func TestRequestLoggerLogsErrors(t *testing.T) {
	app := newTestApp()

	var buf bytes.Buffer
	redirectLogOutput(t, &buf)

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
	out := buf.String()
	if out == "" {
		t.Fatal("error request produced no log output")
	}
	if !strings.Contains(out, "400") || !strings.Contains(out, "/api/publish") {
		t.Errorf("log output = %q, want it to contain status 400 and path", out)
	}
}
