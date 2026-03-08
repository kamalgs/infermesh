package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// ModelsResponse mirrors the hub's response type.
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Provider string `json:"provider"`
}

func startHubWithDiscovery(t *testing.T) (*natsserver.Server, *nats.Conn) {
	t.Helper()

	opts := &natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("start nats: %v", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		ns.Shutdown()
		t.Fatalf("connect: %v", err)
	}

	// Set up the discovery responder (same logic as cmd/hub)
	nc.Subscribe("llm.models", func(msg *nats.Msg) {
		resp := discoverModelsFromServer(ns)
		data, _ := json.Marshal(resp)
		msg.Respond(data)
	})

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return ns, nc
}

func discoverModelsFromServer(ns *natsserver.Server) *ModelsResponse {
	subsz, err := ns.Subsz(&natsserver.SubszOptions{
		Subscriptions: true,
		Limit:         4096,
	})
	if err != nil || subsz == nil {
		return &ModelsResponse{Object: "list"}
	}

	providers := map[string]bool{}
	models := map[string]bool{}

	for _, sub := range subsz.Subs {
		if !strings.HasPrefix(sub.Subject, "llm.provider.") {
			continue
		}
		rest := sub.Subject[len("llm.provider."):]
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) == 0 {
			continue
		}
		provider := parts[0]
		providers[provider] = true
		if len(parts) == 2 && parts[1] != ">" {
			models[provider+"."+parts[1]] = true
		}
	}

	resp := &ModelsResponse{Object: "list"}
	for model := range models {
		parts := strings.SplitN(model, ".", 2)
		resp.Data = append(resp.Data, Model{
			ID:       model,
			Object:   "model",
			Provider: parts[0],
		})
	}
	for provider := range providers {
		hasSpecific := false
		for model := range models {
			if strings.HasPrefix(model, provider+".") {
				hasSpecific = true
				break
			}
		}
		if !hasSpecific {
			resp.Data = append(resp.Data, Model{
				ID:       provider + ".*",
				Object:   "model",
				Provider: provider,
			})
		}
	}
	return resp
}

func TestDiscovery_WildcardProviders(t *testing.T) {
	ns, nc := startHubWithDiscovery(t)

	// Simulate providers subscribing with wildcards
	nc2, _ := nats.Connect(ns.ClientURL())
	t.Cleanup(func() { nc2.Close() })

	nc2.QueueSubscribe("llm.provider.openai.>", "provider-openai", func(msg *nats.Msg) {})
	nc2.QueueSubscribe("llm.provider.anthropic.>", "provider-anthropic", func(msg *nats.Msg) {})
	nc2.Flush()

	// Give NATS a moment to propagate subscriptions
	time.Sleep(50 * time.Millisecond)

	msg, err := nc.Request("llm.models", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp ModelsResponse
	json.Unmarshal(msg.Data, &resp)

	if resp.Object != "list" {
		t.Errorf("object: got %q", resp.Object)
	}

	// Should see two wildcard providers
	providersSeen := map[string]bool{}
	for _, m := range resp.Data {
		providersSeen[m.Provider] = true
	}

	if !providersSeen["openai"] {
		t.Error("missing openai provider")
	}
	if !providersSeen["anthropic"] {
		t.Error("missing anthropic provider")
	}
}

func TestDiscovery_SpecificModels(t *testing.T) {
	ns, nc := startHubWithDiscovery(t)

	nc2, _ := nats.Connect(ns.ClientURL())
	t.Cleanup(func() { nc2.Close() })

	// Subscribe to specific model subjects
	nc2.QueueSubscribe("llm.provider.openai.gpt-4o", "provider-openai", func(msg *nats.Msg) {})
	nc2.QueueSubscribe("llm.provider.openai.gpt-4o-mini", "provider-openai", func(msg *nats.Msg) {})
	nc2.QueueSubscribe("llm.provider.ollama.llama3:8b", "provider-ollama", func(msg *nats.Msg) {})
	nc2.Flush()
	time.Sleep(50 * time.Millisecond)

	msg, err := nc.Request("llm.models", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp ModelsResponse
	json.Unmarshal(msg.Data, &resp)

	modelIDs := map[string]bool{}
	for _, m := range resp.Data {
		modelIDs[m.ID] = true
	}

	if !modelIDs["openai.gpt-4o"] {
		t.Errorf("missing openai.gpt-4o, got: %v", modelIDs)
	}
	if !modelIDs["openai.gpt-4o-mini"] {
		t.Errorf("missing openai.gpt-4o-mini, got: %v", modelIDs)
	}
	if !modelIDs["ollama.llama3:8b"] {
		t.Errorf("missing ollama.llama3:8b, got: %v", modelIDs)
	}
}

func TestDiscovery_NoProviders(t *testing.T) {
	_, nc := startHubWithDiscovery(t)

	msg, err := nc.Request("llm.models", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp ModelsResponse
	json.Unmarshal(msg.Data, &resp)

	if resp.Object != "list" {
		t.Errorf("object: got %q", resp.Object)
	}
	// The discovery subscriber itself and llm.models sub may show up,
	// but no llm.provider.* subs should be found
	for _, m := range resp.Data {
		t.Errorf("unexpected model: %s", m.ID)
	}
}

func TestDiscovery_MixedWildcardAndSpecific(t *testing.T) {
	ns, nc := startHubWithDiscovery(t)

	nc2, _ := nats.Connect(ns.ClientURL())
	t.Cleanup(func() { nc2.Close() })

	// Anthropic uses wildcard, openai uses specific models
	nc2.QueueSubscribe("llm.provider.anthropic.>", "provider-anthropic", func(msg *nats.Msg) {})
	nc2.QueueSubscribe("llm.provider.openai.gpt-4o", "provider-openai", func(msg *nats.Msg) {})
	nc2.Flush()
	time.Sleep(50 * time.Millisecond)

	msg, err := nc.Request("llm.models", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var resp ModelsResponse
	json.Unmarshal(msg.Data, &resp)

	modelIDs := map[string]bool{}
	for _, m := range resp.Data {
		modelIDs[m.ID] = true
	}

	// Anthropic should show as wildcard, openai as specific model
	if !modelIDs["anthropic.*"] {
		t.Errorf("missing anthropic.*, got: %v", modelIDs)
	}
	if !modelIDs["openai.gpt-4o"] {
		t.Errorf("missing openai.gpt-4o, got: %v", modelIDs)
	}
}
