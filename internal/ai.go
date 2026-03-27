package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"multi-tenant-bot/internal/models"
)

type AIClient interface {
	Chat(ctx context.Context, messages []models.AIMessage) (string, error)
}

type GroqClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

type groqChatRequest struct {
	Model    string           `json:"model"`
	Messages []models.AIMessage `json:"messages"`
}

type groqChatResponse struct {
	Choices []struct {
		Message models.AIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewGroqClient(apiKey string) *GroqClient {
	return &GroqClient{
		apiKey: apiKey,
		model:  "llama-3.3-70b-versatile",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *GroqClient) Chat(ctx context.Context, messages []models.AIMessage) (string, error) {
	reqBody, err := json.Marshal(groqChatRequest{
		Model:    c.model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("marshal ai request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "", fmt.Errorf("build ai request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call ai provider: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ai response: %w", err)
	}

	var parsed groqChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode ai response: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("groq error: %s", parsed.Error.Message)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("groq status %d", resp.StatusCode)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty ai response")
	}

	return parsed.Choices[0].Message.Content, nil
}
