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
	confPath := findConfigPath()
	cfg := config.Load(confPath)
	cfg.Log()

	staticDir := findStaticDir()

	mq := mqttclient.New(cfg.MQTTBrokerHost, cfg.MQTTBrokerPort, cfg.MQTTClientID, cfg.SubscribeTopic)
	sched := scheduler.New(mq, cfg.PublishTopic, cfg.DefaultDisplayDuration, cfg.DefaultTargetDisplayCount, cfg.DisplayWidth, cfg.IdleMessage, cfg.IdleMode, cfg.IdlePublishInterval)

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

func findConfigPath() string {
	candidates := []string{
		"app.conf",
		"backend-go/app.conf",
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "app.conf"),
			filepath.Join(exeDir, "backend-go", "app.conf"),
			filepath.Join(exeDir, "..", "app.conf"),
			filepath.Join(exeDir, "..", "backend-go", "app.conf"),
		)
	}
	// Also try parent of cwd for `cd backend-go && go run` case
	candidates = append(candidates, "../backend-go/app.conf", "../app.conf")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if p, err := filepath.Abs(c); err == nil {
				return p
			}
			return c
		}
	}
	if p, err := filepath.Abs("app.conf"); err == nil {
		return p
	}
	return "backend-go/app.conf"
}

func findStaticDir() string {
	candidates := []string{
		"frontend/static",
		"../frontend/static",
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "frontend", "static"),
			filepath.Join(exeDir, "..", "frontend", "static"),
			filepath.Join(exeDir, "..", "..", "frontend", "static"),
			filepath.Join(exeDir, "backend-go", "frontend", "static"),
		)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return "frontend/static"
}
