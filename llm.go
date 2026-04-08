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

var modelProviders []ModelProvider
var cooldowns = make(map[string]time.Time)
var retryCount = make(map[string]int)

type ModelProvider struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	APIBase  string `json:"api_base,omitempty"`
}

func LoadModelsFromConfig() {
	data, err := os.ReadFile("/root/.picoclaw/config.json")
	if err != nil {
		fmt.Printf("Could not load config: %v\n", err)
		return
	}

	var config struct {
		ModelList []struct {
			ModelName string `json:"model_name"`
			APIKey    string `json:"api_key"`
			APIBase   string `json:"api_base,omitempty"`
		} `json:"model_list"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		fmt.Printf("Could not parse config: %v\n", err)
		return
	}

	modelProviders = []ModelProvider{}

	for _, m := range config.ModelList {
		if m.APIKey == "" {
			continue
		}

		provider := "nvidia"
		if strings.Contains(m.ModelName, "gemini") {
			provider = "google"
		} else if strings.Contains(m.ModelName, "gpt-") {
			provider = "openai"
		} else if strings.Contains(m.ModelName, "claude-") {
			provider = "anthropic"
		} else if strings.Contains(m.ModelName, "openrouter") || strings.Contains(m.ModelName, "free") {
			provider = "openrouter"
		}

		modelProviders = append(modelProviders, ModelProvider{
			Provider: provider,
			Model:    m.ModelName,
			APIKey:   m.APIKey,
			APIBase:  m.APIBase,
		})
	}

	fmt.Printf("✅ Loaded %d models from config\n", len(modelProviders))
}

func init() {
	LoadModelsFromConfig()

	if len(modelProviders) == 0 {
		modelProviders = []ModelProvider{
			{"google", "gemini-2.0-flash", "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y", ""},
			{"google", "gemini-1.5-flash", "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y", ""},
			{"nvidia", "minimaxai/minimax-m2.5", "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", "https://integrate.api.nvidia.com/v1"},
			{"nvidia", "nvidia/nemotron-3-nano-30b-a3b", "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", "https://integrate.api.nvidia.com/v1"},
			{"nvidia", "meta/llama-3.1-8b-instruct", "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", "https://integrate.api.nvidia.com/v1"},
		}
		fmt.Println("⚠️ Using default models (config not loaded)")
	}
}

type LLMClient struct {
	provider string
	model    string
	apiKey   string
	apiBase  string
	client   *http.Client
}

func NewLLMClient(provider, model, apiKey string) *LLMClient {
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	return &LLMClient{
		provider: provider,
		model:    model,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *LLMClient) Complete(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	key := fmt.Sprintf("%s:%s", c.provider, c.model)

	if until, ok := cooldowns[key]; ok && time.Now().Before(until) {
		return "", fmt.Errorf("cooldown: %v remaining", until.Sub(time.Now()).Round(time.Second))
	}

	var resp string
	var err error

	switch c.provider {
	case "google", "gemini":
		resp, err = c.completeGemini(ctx, msgs, tools)
	case "nvidia":
		resp, err = c.completeNvidia(ctx, msgs, tools)
	case "openrouter":
		resp, err = c.completeOpenRouter(ctx, msgs, tools)
	default:
		resp, err = c.completeNvidia(ctx, msgs, tools)
	}

	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "quota") {
			cooldowns[key] = time.Now().Add(15 * time.Second)
			retryCount[key]++
			return c.tryAllModels(ctx, msgs, tools)
		}
		return "", err
	}

	retryCount[key] = 0
	return resp, nil
}

func (c *LLMClient) tryAllModels(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	fmt.Printf("🔄 Trying all available models (%d total)...\n", len(modelProviders))

	used := make(map[string]bool)
	used[fmt.Sprintf("%s:%s", c.provider, c.model)] = true

	for i, mp := range modelProviders {
		if used[fmt.Sprintf("%s:%s", mp.Provider, mp.Model)] {
			continue
		}
		if retryCount[fmt.Sprintf("%s:%s", mp.Provider, mp.Model)] >= 3 {
			continue
		}

		key := fmt.Sprintf("%s:%s", mp.Provider, mp.Model)
		if until, ok := cooldowns[key]; ok && time.Now().Before(until) {
			continue
		}

		fmt.Printf("🎯 [%d] Trying %s/%s\n", i+1, mp.Provider, mp.Model)

		client := NewLLMClient(mp.Provider, mp.Model, mp.APIKey)
		resp, err := client.Complete(ctx, msgs, tools)

		if err == nil {
			fmt.Printf("✅ Success: %s/%s\n", mp.Provider, mp.Model)
			return resp, nil
		}

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "quota") {
			cooldowns[key] = time.Now().Add(15 * time.Second)
			retryCount[key]++
			fmt.Printf("⚠️ Rate limited: %s\n", mp.Model)
		} else {
			fmt.Printf("❌ Error: %s - %v\n", mp.Model, err)
			return "", err
		}

		time.Sleep(300 * time.Millisecond)
	}

	return "", fmt.Errorf("all models rate limited or failed")
}

func (c *LLMClient) completeGemini(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", c.model, c.apiKey)

	req := map[string]interface{}{
		"contents":         []map[string]interface{}{},
		"generationConfig": map[string]interface{}{"temperature": 0.7, "maxOutputTokens": 2048},
	}

	for _, msg := range msgs {
		if msg.Role == "system" {
			req["systemInstruction"] = map[string]interface{}{"parts": []map[string]string{{"text": msg.Content}}}
		} else {
			contents := req["contents"].([]map[string]interface{})
			contents = append(contents, map[string]interface{}{"parts": []map[string]string{{"text": msg.Content}}})
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
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var gr map[string]interface{}
	json.Unmarshal(body, &gr)

	if candidates, ok := gr["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if content, ok := candidates[0].(map[string]interface{})["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
				if text, ok := parts[0].(map[string]interface{})["text"].(string); ok {
					return text, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no response")
}

func (c *LLMClient) completeNvidia(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://integrate.api.nvidia.com/v1/chat/completions"
	if c.apiBase != "" {
		url = c.apiBase + "/chat/completions"
	}

	req := map[string]interface{}{"model": c.model, "messages": msgs, "temperature": 0.7, "max_tokens": 2048}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var r map[string]interface{}
	json.Unmarshal(body, &r)

	if choices, ok := r["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, nil
			}
		}
	}
	return "", fmt.Errorf("no response")
}

func (c *LLMClient) completeOpenRouter(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	url := "https://openrouter.ai/api/v1/chat/completions"

	req := map[string]interface{}{"model": c.model, "messages": msgs, "temperature": 0.7, "max_tokens": 2048}

	jsonData, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://picoclaw.local")
	httpReq.Header.Set("X-Title", "Picoclaw")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var r map[string]interface{}
	json.Unmarshal(body, &r)

	if choices, ok := r["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, nil
			}
		}
	}
	return "", fmt.Errorf("no response")
}
