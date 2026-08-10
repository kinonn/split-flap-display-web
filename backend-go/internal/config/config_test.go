package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.conf")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg := Load("nonexistent.conf")
	if cfg.MQTTBrokerHost != "localhost" {
		t.Errorf("host = %q", cfg.MQTTBrokerHost)
	}
	if cfg.MQTTBrokerPort != 1883 {
		t.Errorf("port = %d", cfg.MQTTBrokerPort)
	}
	if cfg.DefaultTargetDisplayCount != 6 {
		t.Errorf("tdc = %d", cfg.DefaultTargetDisplayCount)
	}
	if !cfg.SchedulerEnabled {
		t.Errorf("scheduler_enabled = %v", cfg.SchedulerEnabled)
	}
}

func TestFile(t *testing.T) {
	p := writeConf(t, `# comment
MQTT_BROKER_HOST=broker.local
MQTT_BROKER_PORT=1234
DEFAULT_TARGET_DISPLAY_COUNT=2
`)
	cfg := Load(p)
	if cfg.MQTTBrokerHost != "broker.local" {
		t.Errorf("host = %q", cfg.MQTTBrokerHost)
	}
	if cfg.MQTTBrokerPort != 1234 {
		t.Errorf("port = %d", cfg.MQTTBrokerPort)
	}
	if cfg.DefaultTargetDisplayCount != 2 {
		t.Errorf("tdc = %d", cfg.DefaultTargetDisplayCount)
	}
}

func TestEnvOverride(t *testing.T) {
	p := writeConf(t, `MQTT_BROKER_HOST=from-file`)
	t.Setenv("MQTT_BROKER_HOST", "from-env")
	t.Setenv("DEFAULT_DISPLAY_DURATION", "42")
	cfg := Load(p)
	if cfg.MQTTBrokerHost != "from-env" {
		t.Errorf("host = %q", cfg.MQTTBrokerHost)
	}
	if cfg.DefaultDisplayDuration != 42 {
		t.Errorf("dur = %d", cfg.DefaultDisplayDuration)
	}
}