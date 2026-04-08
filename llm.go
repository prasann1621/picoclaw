package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type LLMClient struct {
	provider string
	model    string
	apiKey   string
	client   *http.Client
	retry    int
	cooldown time.Duration
}

var (
	cooldowns  = make(map[string]time.Time)
	retryCount = make(map[string]int)
)

func NewLLMClient(provider, model, apiKey string) *LLMClient {
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	return &LLMClient{
		provider: provider,
		model:    model,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		retry:    3,
		cooldown: 5 * time.Second,
	}
}

func (c *LLMClient) Complete(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	key := fmt.Sprintf("%s:%s", c.provider, c.model)

	if until, ok := cooldowns[key]; ok && time.Now().Before(until) {
		remaining := until.Sub(time.Now())
		return "", fmt.Errorf("rate limited for %v more - trying fallback", remaining)
	}

	var lastErr error
	maxRetries := 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		var resp string
		var err error

		switch c.provider {
		case "google", "gemini":
			resp, err = c.completeGemini(ctx, msgs, tools)
		case "openai":
			resp, err = c.completeOpenAI(ctx, msgs, tools)
		case "anthropic":
			resp, err = c.completeAnthropic(ctx, msgs, tools)
		case "openrouter":
			resp, err = c.completeOpenRouter(ctx, msgs, tools)
		case "nvidia":
			resp, err = c.completeNvidia(ctx, msgs, tools)
		default:
			resp, err = c.completeNvidia(ctx, msgs, tools)
		}

		if err == nil {
			retryCount[key] = 0
			return resp, nil
		}

		lastErr = err
		errStr := strings.ToLower(err.Error())

		if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") || strings.Contains(errStr, "quota") {
			cooldowns[key] = time.Now().Add(time.Duration(30+attempt*30) * time.Second)
			retryCount[key]++

			fmt.Printf("⚠️ Rate limited on %s, cooldown %ds\n", key, 30+attempt*30)

			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "context deadline") {
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		return "", err
	}

	cooldowns[key] = time.Now().Add(60 * time.Second)
	return "", fmt.Errorf("all retries failed: %v", lastErr)
}

type GeminiRequest struct {
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	SystemInstruction *struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"systemInstruction,omitempty"`
	GenerationConfig struct {
		Temperature     float64       `json:"temperature"`
		MaxOutputTokens int           `json:"maxOutputTokens"`
		Tools           []interface{} `json:"tools,omitempty"`
	} `json:"generationConfig"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (c *LLMClient) completeGemini(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	apiKey := c.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		apiKey = "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y"
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", c.model, apiKey)

	req := GeminiRequest{
		GenerationConfig: struct {
			Temperature     float64       `json:"temperature"`
			MaxOutputTokens int           `json:"maxOutputTokens"`
			Tools           []interface{} `json:"tools,omitempty"`
		}{
			Temperature:     0.7,
			MaxOutputTokens: 4096,
		},
	}

	for _, msg := range msgs {
		if msg.Role == "system" {
			req.SystemInstruction = &struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}{
				Parts: []struct {
					Text string `json:"text"`
				}{{Text: msg.Content}},
			}
		} else {
			req.Contents = append(req.Contents, struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}{
				Parts: []struct {
					Text string `json:"text"`
				}{{Text: msg.Content}},
			})
		}
	}

	if len(tools) > 0 {
		toolsReq := make([]interface{}, len(tools))
		for i, tool := range tools {
			toolsReq[i] = map[string]interface{}{
				"functionDeclarations": []map[string]interface{}{
					{
						"name":        tool.Name,
						"description": tool.Description,
						"parameters":  tool.Parameters,
					},
				},
			}
		}
		req.GenerationConfig.Tools = toolsReq
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return "", err
	}

	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

type OpenAIRequest struct {
	Model       string                   `json:"model"`
	Messages    []Message                `json:"messages"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
	Temperature float64                  `json:"temperature"`
	MaxTokens   int                      `json:"max_tokens"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *LLMClient) completeOpenAI(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = c.apiKey
	}

	req := OpenAIRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.7,
		MaxTokens:   4096,
	}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var openaiResp OpenAIResponse
	json.Unmarshal(body, &openaiResp)

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}
	return openaiResp.Choices[0].Message.Content, nil
}

func (c *LLMClient) completeAnthropic(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	return "", fmt.Errorf("anthropic not implemented")
}

func (c *LLMClient) completeOpenRouter(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://openrouter.ai/api/v1/chat/completions"
	apiKey := c.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		apiKey = "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"
	}

	req := OpenAIRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.7,
		MaxTokens:   4096,
	}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://picoclaw.local")
	httpReq.Header.Set("X-Title", "Picoclaw")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var openaiResp OpenAIResponse
	json.Unmarshal(body, &openaiResp)

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}
	return openaiResp.Choices[0].Message.Content, nil
}

func (c *LLMClient) completeNvidia(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://integrate.api.nvidia.com/v1/chat/completions"
	apiKey := c.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("NVIDIA_API_KEY")
	}
	if apiKey == "" {
		apiKey = "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"
	}

	req := OpenAIRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.7,
		MaxTokens:   4096,
	}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var openaiResp OpenAIResponse
	json.Unmarshal(body, &openaiResp)

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}
	return openaiResp.Choices[0].Message.Content, nil
}
