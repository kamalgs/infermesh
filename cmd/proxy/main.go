package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kamalgs/nats-llm-gateway/internal/proxy"
	"github.com/nats-io/nats.go"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL // nats://localhost:4222
	}

	addr := os.Getenv("PROXY_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Error("failed to connect to nats", "url", natsURL, "error", err)
		os.Exit(1)
	}
	defer nc.Close()
	log.Info("connected to nats", "url", natsURL)

	// Start HTTP proxy
	p := proxy.New(nc, addr, log.With("component", "proxy"))

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Info("shutting down proxy")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	log.Info("proxy ready", "addr", addr)
	if err := p.Start(); !errors.Is(err, http.ErrServerClosed) {
		log.Error("proxy failed", "error", err)
		os.Exit(1)
	}
}
