package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kamalgs/infermesh/internal/config"
	"github.com/kamalgs/infermesh/internal/gateway"
	"github.com/kamalgs/infermesh/internal/provider/openai"
	"github.com/nats-io/nats.go"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Load config
	cfgPath := os.Getenv("GATEWAY_CONFIG")
	if cfgPath == "" {
		cfgPath = "configs/gateway.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Error("failed to load config", "path", cfgPath, "error", err)
		os.Exit(1)
	}

	// Connect to NATS
	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		log.Error("failed to connect to nats", "url", cfg.NATS.URL, "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	log.Info("connected to nats", "url", cfg.NATS.URL)

	// Start provider adapters
	// Each adapter subscribes to its own NATS subject — they are decoupled
	// from the gateway and could run as separate processes.
	if provCfg, ok := cfg.Providers["openai"]; ok {
		adapter := openai.NewAdapter(provCfg, log.With("component", "provider-openai"))
		sub, err := adapter.Subscribe(nc)
		if err != nil {
			log.Error("failed to start openai adapter", "error", err)
			os.Exit(1)
		}
		defer sub.Drain()
	}

	// Start gateway service
	gw := gateway.New(nc, cfg, log.With("component", "gateway"))
	if err := gw.Start(); err != nil {
		log.Error("failed to start gateway", "error", err)
		os.Exit(1)
	}
	defer gw.Stop()

	log.Info("gateway service ready")

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")
}
