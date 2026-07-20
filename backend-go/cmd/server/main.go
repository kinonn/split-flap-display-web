// Command server launches the split-flap-display-web backend.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"splitflap-web/internal/api"
	"splitflap-web/internal/config"
	"splitflap-web/internal/mqttclient"
	"splitflap-web/internal/scheduler"
)

func main() {
	confPath := "backend-go/app.conf"
	if p, err := filepath.Abs("app.conf"); err == nil {
		confPath = p
	}
	cfg := config.Load(confPath)
	cfg.Log()

	// Locate frontend/static relative to the working directory. The Docker
	// container runs the binary from /app; the static dir lives under
	// frontend/static.
	staticDir := "frontend/static"

	mq := mqttclient.New(cfg.MQTTBrokerHost, cfg.MQTTBrokerPort, cfg.MQTTClientID, cfg.SubscribeTopic)
	sched := scheduler.New(mq, cfg.PublishTopic, cfg.DefaultDisplayDuration, cfg.DefaultTargetDisplayCount, cfg.IdleMessage, cfg.IdleMode, cfg.IdlePublishInterval)

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	log.Printf("starting mqtt client...")
	mq.Start(rootCtx)

	if cfg.SchedulerEnabled {
		log.Printf("starting scheduler...")
		sched.Start(rootCtx)
	}

	srv := api.New(&api.Server{
		Cfg:       cfg,
		MQTT:      mq,
		Scheduler: sched,
		StaticDir: staticDir,
	})

	addr := ":8100"
	log.Printf("listening on %s", addr)

	go func() {
		if err := srv.Listen(addr); err != nil {
			log.Fatalf("http server error: %v", err)
		}
	}()

	// Wait for SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("shutdown signal received")
	if cfg.SchedulerEnabled {
		sched.Stop()
	}
	mq.Stop()
	_ = srv.Shutdown()
	log.Printf("stopped")
}
