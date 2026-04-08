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
	cooldowns      = make(map[string]time.Time)
	retryCount     = make(map[string]int)
	modelProviders = []struct {
		provider string
		model    string
		apiKey   string
	}{
		{"google", "gemini-2.0-flash", "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y"},
		{"google", "gemini-1.5-flash", "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y"},
		{"google", "gemini-2.0-flash", "AIzaSyCwtbDpcMgjCKUov-WwYJS276Yney1Xsno"},
		{"nvidia", "minimaxai/minimax-m2.5", "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"},
		{"nvidia", "mistralai/mistral-small-24b-instruct", "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"},
		{"nvidia", "qwen/qwen2.5-7b-instruct", "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"},
		{"nvidia", "google/gemma-3-4b-it", "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"},
		{"nvidia", "meta/llama-3.1-8b-instruct", "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"},
		{"nvidia", "deepseek-ai/deepseek-r1-distill-qwen-7b", "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"},
		{"nvidia", "mistralai/mistral-7b-instruct-v0.3", "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"},
		{"openrouter", "deepseek/deepseek-r1:free", "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"},
		{"openrouter", "qwen/qwen3-4b:free", "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"},
		{"openrouter", "google/gemma-3-4b-it:free", "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"},
	}
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
		return "", fmt.Errorf("rate limited for %v more", remaining)
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
			strings.Contains(errStr, "too many requests") || strings.Contains(errStr, "quota") ||
			strings.Contains(errStr, "insufficient quota") || strings.Contains(errStr, "billing") {
			cooldownSec := 10 + attempt*5
			cooldowns[key] = time.Now().Add(time.Duration(cooldownSec) * time.Second)
			retryCount[key]++

			fmt.Printf("⚠️ Rate limited on %s, cooldown %ds, trying fallback...\n", key, cooldownSec)

			return c.tryFallback(ctx, msgs, tools)
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

func (c *LLMClient) tryFallback(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	currentKey := fmt.Sprintf("%s:%s", c.provider, c.model)

	for i, mp := range modelProviders {
		checkKey := fmt.Sprintf("%s:%s", mp.provider, mp.model)

		if checkKey == currentKey {
			continue
		}

		if until, ok := cooldowns[checkKey]; ok && time.Now().Before(until) {
			continue
		}

		if retryCount[checkKey] >= 5 {
			continue
		}

		fmt.Printf("🔄 Trying fallback %d: %s/%s\n", i+1, mp.provider, mp.model)

		fallbackClient := NewLLMClient(mp.provider, mp.model, mp.apiKey)
		resp, err := fallbackClient.Complete(ctx, msgs, tools)

		if err == nil {
			retryCount[checkKey] = 0
			return resp, nil
		}

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "quota") || strings.Contains(errStr, "insufficient") {
			cooldowns[checkKey] = time.Now().Add(30 * time.Second)
			retryCount[checkKey]++
		}

		if !strings.Contains(errStr, "rate limit") && !strings.Contains(errStr, "quota") {
			return "", err
		}
	}

	return "", fmt.Errorf("all model fallbacks exhausted")
}

func ClearCooldowns() {
	cooldowns = make(map[string]time.Time)
	retryCount = make(map[string]int)
	fmt.Println("✅ All model cooldowns cleared")
}

func GetModelStatus() string {
	result := "📊 Model Status:\n\n"
	now := time.Now()

	for key := range cooldowns {
		remaining := cooldowns[key].Sub(now)
		if remaining > 0 {
			result += fmt.Sprintf("⏳ %s: %v remaining\n", key, remaining.Round(time.Second))
		} else {
			result += fmt.Sprintf("✅ %s: Available\n", key)
		}
	}

	if len(cooldowns) == 0 {
		result += "All models available"
	}

	return result
}

func (c *LLMClient) completeGemini(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	apiKey := c.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", c.model, apiKey)

	req := map[string]interface{}{
		"contents": []map[string]interface{}{},
		"generationConfig": map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 4096,
		},
	}

	for _, msg := range msgs {
		if msg.Role == "system" {
			req["systemInstruction"] = map[string]interface{}{
				"parts": []map[string]string{{"text": msg.Content}},
			}
		} else {
			contents := req["contents"].([]map[string]interface{})
			contents = append(contents, map[string]interface{}{
				"parts": []map[string]string{{"text": msg.Content}},
			})
			req["contents"] = contents
		}
	}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var geminiResp map[string]interface{}
	json.Unmarshal(body, &geminiResp)

	candidates, ok := geminiResp["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	firstCandidate := candidates[0].(map[string]interface{})
	content := firstCandidate["content"].(map[string]interface{})
	parts := content["parts"].([]interface{})
	if len(parts) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return parts[0].(map[string]interface{})["text"].(string), nil
}

func (c *LLMClient) completeOpenAI(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://api.openai.com/v1/chat/completions"
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = c.apiKey
	}

	req := map[string]interface{}{
		"model":       c.model,
		"messages":    msgs,
		"temperature": 0.7,
		"max_tokens":  4096,
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
	var openaiResp map[string]interface{}
	json.Unmarshal(body, &openaiResp)

	choices, ok := openaiResp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	return choices[0].(map[string]interface{})["message"].(map[string]interface{})["content"].(string), nil
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

	req := map[string]interface{}{
		"model":       c.model,
		"messages":    msgs,
		"temperature": 0.7,
		"max_tokens":  4096,
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
	var openaiResp map[string]interface{}
	json.Unmarshal(body, &openaiResp)

	choices, ok := openaiResp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	return choices[0].(map[string]interface{})["message"].(map[string]interface{})["content"].(string), nil
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

	req := map[string]interface{}{
		"model":       c.model,
		"messages":    msgs,
		"temperature": 0.7,
		"max_tokens":  4096,
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
	var openaiResp map[string]interface{}
	json.Unmarshal(body, &openaiResp)

	choices, ok := openaiResp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("no response from model")
	}

	return choices[0].(map[string]interface{})["message"].(map[string]interface{})["content"].(string), nil
}
