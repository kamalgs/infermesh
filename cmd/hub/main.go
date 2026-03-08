// Package main runs an embedded NATS hub server with model discovery.
//
// The hub inspects its own subscription table to find active
// llm.provider.<provider>.> subscriptions and responds to llm.models
// requests with the list of available providers (and specific models
// if providers subscribe to individual model subjects).
package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	port := 4222
	leafPort := 7422
	monitorPort := 8222

	opts := &natsserver.Options{
		Host:       "0.0.0.0",
		Port:       port,
		HTTPPort:   monitorPort,
		LeafNode:   natsserver.LeafNodeOpts{Port: leafPort},
		ServerName: "infermesh-hub",
		NoSigs:     true,
	}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		log.Error("failed to create nats server", "error", err)
		os.Exit(1)
	}

	ns.ConfigureLogger()
	ns.Start()

	if !ns.ReadyForConnections(10 * time.Second) {
		log.Error("nats server not ready")
		os.Exit(1)
	}

	log.Info("nats hub started",
		"client_port", port,
		"leafnode_port", leafPort,
		"monitor_port", monitorPort,
	)

	// Connect an internal client to serve model discovery
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		log.Error("failed to connect internal client", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	// Respond to llm.models requests with discovered providers/models
	_, err = nc.Subscribe("llm.models", func(msg *nats.Msg) {
		models := discoverModels(ns)
		data, _ := json.Marshal(models)
		msg.Respond(data)
	})
	if err != nil {
		log.Error("failed to subscribe to llm.models", "error", err)
		os.Exit(1)
	}

	log.Info("model discovery active", "subject", "llm.models")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Info("shutting down")
	nc.Close()
	ns.Shutdown()
}

// ModelsResponse is the OpenAI-compatible /v1/models response.
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Provider string `json:"provider"`
}

const subjectPrefix = "llm.provider."

// discoverModels queries the embedded NATS server's subscription table
// to find all llm.provider.* subscriptions and derives available models.
func discoverModels(ns *natsserver.Server) *ModelsResponse {
	subsz, err := ns.Subsz(&natsserver.SubszOptions{
		Subscriptions: true,
		Limit:         4096,
	})
	if err != nil || subsz == nil {
		return &ModelsResponse{Object: "list"}
	}

	// Track unique providers and specific models
	providers := map[string]bool{}
	models := map[string]bool{}

	for _, sub := range subsz.Subs {
		if !strings.HasPrefix(sub.Subject, subjectPrefix) {
			continue
		}

		rest := sub.Subject[len(subjectPrefix):]
		// rest is either "openai.>" (wildcard) or "openai.gpt-4o" (specific)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) == 0 {
			continue
		}

		provider := parts[0]
		providers[provider] = true

		if len(parts) == 2 && parts[1] != ">" {
			// Specific model subscription
			models[provider+"."+parts[1]] = true
		}
	}

	resp := &ModelsResponse{Object: "list"}

	// Add specific model entries
	for model := range models {
		parts := strings.SplitN(model, ".", 2)
		resp.Data = append(resp.Data, Model{
			ID:       model,
			Object:   "model",
			Provider: parts[0],
		})
	}

	// Add wildcard provider entries (for providers that accept any model)
	for provider := range providers {
		if !hasSpecificModels(models, provider) {
			resp.Data = append(resp.Data, Model{
				ID:       provider + ".*",
				Object:   "model",
				Provider: provider,
			})
		}
	}

	return resp
}

// hasSpecificModels checks if a provider has any specific model subscriptions.
func hasSpecificModels(models map[string]bool, provider string) bool {
	for model := range models {
		if strings.HasPrefix(model, provider+".") {
			return true
		}
	}
	return false
}
