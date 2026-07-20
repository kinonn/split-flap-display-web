// Package config loads the split-flap backend configuration from an
// optional app.conf file, environment variables, and defaults.
package config

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// Config holds all resolved configuration values.
type Config struct {
	MQTTBrokerHost          string
	MQTTBrokerPort          int
	MQTTClientID            string
	PublishTopic            string
	SubscribeTopic          string
	DefaultDisplayDuration  int
	DefaultTargetDisplayCount int
	IdleMessage             string
	IdleMode                string
	IdlePublishInterval     int
	SchedulerEnabled        bool
}

// Defaults applied when neither file nor env var sets a key.
func defaults() Config {
	return Config{
		MQTTBrokerHost:            "localhost",
		MQTTBrokerPort:            1883,
		MQTTClientID:              "splitflap-web",
		PublishTopic:              "splitflap/splitflap/set",
		SubscribeTopic:            "splitflap/splitflap/state",
		DefaultDisplayDuration:    10,
		DefaultTargetDisplayCount: 6,
		IdleMessage:               "WELCOME",
		IdleMode:                  "publish",
		IdlePublishInterval:       10,
		SchedulerEnabled:          true,
	}
}

// Load reads app.conf (if present) and applies environment variable overrides.
func Load(path string) Config {
	cfg := defaults()

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			log.Printf("config: loading configuration from %s", path)
			if err := loadFile(path, &cfg); err != nil {
				log.Printf("config: failed to parse %s: %v", path, err)
			} else {
				log.Printf("config: successfully loaded %s", path)
			}
		} else {
			log.Printf("config: %s not found, using defaults", path)
		}
	}

	applyEnv(&cfg)
	return cfg
}

func loadFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		applyKey(cfg, key, val)
	}
	return scanner.Err()
}

func applyEnv(cfg *Config) {
	for _, key := range []string{
		"MQTT_BROKER_HOST", "MQTT_BROKER_PORT", "MQTT_CLIENT_ID",
		"PUBLISH_TOPIC", "SUBSCRIBE_TOPIC",
		"DEFAULT_DISPLAY_DURATION", "DEFAULT_TARGET_DISPLAY_COUNT",
		"IDLE_MESSAGE", "IDLE_MODE", "IDLE_PUBLISH_INTERVAL",
		"SCHEDULER_ENABLED",
	} {
		if v, ok := os.LookupEnv(key); ok {
			applyKey(cfg, key, v)
		}
	}
}

func applyKey(cfg *Config, key, val string) {
	switch strings.ToUpper(key) {
	case "MQTT_BROKER_HOST":
		cfg.MQTTBrokerHost = val
	case "MQTT_BROKER_PORT":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.MQTTBrokerPort = n
		}
	case "MQTT_CLIENT_ID":
		cfg.MQTTClientID = val
	case "PUBLISH_TOPIC":
		cfg.PublishTopic = val
	case "SUBSCRIBE_TOPIC":
		cfg.SubscribeTopic = val
	case "DEFAULT_DISPLAY_DURATION":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.DefaultDisplayDuration = n
		}
	case "DEFAULT_TARGET_DISPLAY_COUNT":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.DefaultTargetDisplayCount = n
		}
	case "IDLE_MESSAGE":
		cfg.IdleMessage = val
	case "IDLE_MODE":
		cfg.IdleMode = val
	case "IDLE_PUBLISH_INTERVAL":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.IdlePublishInterval = n
		}
	case "SCHEDULER_ENABLED":
		if b, err := strconv.ParseBool(val); err == nil {
			cfg.SchedulerEnabled = b
		}
	}
}

// Log writes a human-readable summary of the resolved configuration.
func (c Config) Log() {
	log.Printf("configuration resolved:")
	log.Printf("  MQTT_BROKER_HOST=%q", c.MQTTBrokerHost)
	log.Printf("  MQTT_BROKER_PORT=%d", c.MQTTBrokerPort)
	log.Printf("  MQTT_CLIENT_ID=%q", c.MQTTClientID)
	log.Printf("  PUBLISH_TOPIC=%q", c.PublishTopic)
	log.Printf("  SUBSCRIBE_TOPIC=%q", c.SubscribeTopic)
	log.Printf("  DEFAULT_DISPLAY_DURATION=%d", c.DefaultDisplayDuration)
	log.Printf("  DEFAULT_TARGET_DISPLAY_COUNT=%d", c.DefaultTargetDisplayCount)
	log.Printf("  IDLE_MESSAGE=%q", c.IdleMessage)
	log.Printf("  IDLE_MODE=%q", c.IdleMode)
	log.Printf("  IDLE_PUBLISH_INTERVAL=%d", c.IdlePublishInterval)
	log.Printf("  SCHEDULER_ENABLED=%t", c.SchedulerEnabled)
}

// String renders a multiline summary of the config (used for diagnostics).
func (c Config) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "MQTT_BROKER_HOST=%s\n", c.MQTTBrokerHost)
	fmt.Fprintf(&sb, "MQTT_BROKER_PORT=%d\n", c.MQTTBrokerPort)
	fmt.Fprintf(&sb, "MQTT_CLIENT_ID=%s\n", c.MQTTClientID)
	fmt.Fprintf(&sb, "PUBLISH_TOPIC=%s\n", c.PublishTopic)
	fmt.Fprintf(&sb, "SUBSCRIBE_TOPIC=%s\n", c.SubscribeTopic)
	fmt.Fprintf(&sb, "DEFAULT_DISPLAY_DURATION=%d\n", c.DefaultDisplayDuration)
	fmt.Fprintf(&sb, "DEFAULT_TARGET_DISPLAY_COUNT=%d\n", c.DefaultTargetDisplayCount)
	fmt.Fprintf(&sb, "IDLE_MESSAGE=%s\n", c.IdleMessage)
	fmt.Fprintf(&sb, "IDLE_MODE=%s\n", c.IdleMode)
	fmt.Fprintf(&sb, "IDLE_PUBLISH_INTERVAL=%d\n", c.IdlePublishInterval)
	fmt.Fprintf(&sb, "SCHEDULER_ENABLED=%t\n", c.SchedulerEnabled)
	return sb.String()
}