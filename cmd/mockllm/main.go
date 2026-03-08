package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := os.Getenv("MOCKLLM_ADDR")
	if addr == "" {
		addr = ":9999"
	}

	mux := http.NewServeMux()

	// OpenAI-compatible endpoint (used by OpenAI + Ollama adapters)
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)

		log.Info("openai-compatible request", "model", model)

		resp := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-mock-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": fmt.Sprintf("Hello from mock %s! This is a simulated response.", model),
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 15,
				"total_tokens":      25,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Anthropic Messages API endpoint
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		// Validate Anthropic headers
		if r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "invalid_request_error",
					"message": "missing anthropic-version header",
				},
			})
			return
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)

		log.Info("anthropic request", "model", model)

		resp := map[string]any{
			"id":    fmt.Sprintf("msg-mock-%d", time.Now().UnixNano()),
			"type":  "message",
			"model": model,
			"role":  "assistant",
			"content": []map[string]any{{
				"type": "text",
				"text": fmt.Sprintf("Hello from mock %s! This is a simulated response.", model),
			}},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 15,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Info("shutting down mock LLM server")
		server.Close()
	}()

	log.Info("mock LLM server starting", "addr", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}
