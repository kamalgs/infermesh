package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamalgs/infermesh/api"
)

func main() {
	proxyURL := os.Getenv("PROXY_URL")
	if proxyURL == "" {
		proxyURL = "http://localhost:19080"
	}
	model := os.Getenv("MODEL")
	if model == "" {
		model = "openai.qwen2.5:0.5b"
	}

	fmt.Printf("InferMesh Chat — %s via %s\n", model, proxyURL)
	fmt.Println("Type /quit to exit, /model <name> to switch models, /clear to reset history.")
	fmt.Println()

	client := &http.Client{Timeout: 120 * time.Second}
	scanner := bufio.NewScanner(os.Stdin)
	var history []api.Message

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "/quit":
			return
		case input == "/clear":
			history = nil
			fmt.Println("History cleared.")
			continue
		case strings.HasPrefix(input, "/model "):
			model = strings.TrimSpace(strings.TrimPrefix(input, "/model "))
			fmt.Printf("Switched to %s\n", model)
			continue
		}

		history = append(history, api.Message{Role: "user", Content: input})

		resp, err := chatCompletion(client, proxyURL, model, history)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			// Remove the failed user message so we can retry
			history = history[:len(history)-1]
			continue
		}

		reply := resp.Choices[0].Message.Content
		history = append(history, api.Message{Role: "assistant", Content: reply})

		fmt.Printf("\n%s\n\n", reply)
	}
}

func chatCompletion(client *http.Client, proxyURL, model string, messages []api.Message) (*api.ChatResponse, error) {
	req := api.ChatRequest{
		Model:    model,
		Messages: messages,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, proxyURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}

	if httpResp.StatusCode != http.StatusOK {
		var errResp api.ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("%s", errResp.Error.Message)
		}
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var chatResp api.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	return &chatResp, nil
}
