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
		fmt.Printf("⚠️ Could not load config: %v\n", err)
		return
	}

	var config struct {
		ModelList []struct {
			ModelName string `json:"model_name"`
			APIKey    string `json:"api_key"`
			APIBase   string `json:"api_base,omitempty"`
		} `json:"model_list"`
		Providers struct {
			Google struct {
				APIKey string `json:"api_key"`
			} `json:"google"`
			Nvidia struct {
				APIKey string `json:"api_key"`
			} `json:"nvidia"`
			OpenRouter struct {
				APIKey string `json:"api_key"`
			} `json:"openrouter"`
		} `json:"providers"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		fmt.Printf("⚠️ Could not parse config: %v\n", err)
		return
	}

	modelProviders = []ModelProvider{}

	googleKey := os.Getenv("GOOGLE_API_KEY")
	if googleKey == "" {
		googleKey = config.Providers.Google.APIKey
	}
	nvidiaKey := os.Getenv("NVIDIA_API_KEY")
	if nvidiaKey == "" {
		nvidiaKey = config.Providers.Nvidia.APIKey
	}
	openrouterKey := os.Getenv("OPENROUTER_API_KEY")
	if openrouterKey == "" {
		openrouterKey = config.Providers.OpenRouter.APIKey
	}

	for _, m := range config.ModelList {
		if m.APIKey != "" {
			modelProviders = append(modelProviders, ModelProvider{
				Provider: getProvider(m.ModelName),
				Model:    m.ModelName,
				APIKey:   m.APIKey,
				APIBase:  m.APIBase,
			})
		}
	}

	fmt.Printf("✅ Loaded %d models from config\n", len(modelProviders))
}

func getProvider(modelName string) string {
	modelLower := strings.ToLower(modelName)
	if strings.Contains(modelLower, "gemini") {
		return "google"
	}
	if strings.Contains(modelLower, "gpt-") || strings.Contains(modelLower, "o1-") || strings.Contains(modelLower, "o3-") {
		return "openai"
	}
	if strings.Contains(modelLower, "claude-") {
		return "anthropic"
	}
	if strings.Contains(modelLower, "openrouter") || strings.Contains(modelLower, ":free") {
		return "openrouter"
	}
	if strings.Contains(modelLower, "nvidia/") || strings.Contains(modelLower, "minimax") ||
		strings.Contains(modelLower, "nemotron") || strings.Contains(modelLower, "mistralai/") ||
		strings.Contains(modelLower, "meta/") || strings.Contains(modelLower, "deepseek") ||
		strings.Contains(modelLower, "qwen") || strings.Contains(modelLower, "gemma") {
		return "nvidia"
	}
	return "nvidia"
}

func init() {
	LoadModelsFromConfig()
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
		switch provider {
		case "google":
			apiKey = os.Getenv("GOOGLE_API_KEY")
		case "nvidia":
			apiKey = os.Getenv("NVIDIA_API_KEY")
		case "openrouter":
			apiKey = os.Getenv("OPENROUTER_API_KEY")
		}
	}

	baseURL := ""
	switch provider {
	case "nvidia":
		baseURL = os.Getenv("NVIDIA_API_BASE")
		if baseURL == "" {
			baseURL = "https://integrate.api.nvidia.com/v1"
		}
	case "openrouter":
		baseURL = os.Getenv("OPENROUTER_API_BASE")
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
	}

	return &LLMClient{
		provider: provider,
		model:    model,
		apiKey:   apiKey,
		apiBase:  baseURL,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *LLMClient) Complete(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	key := fmt.Sprintf("%s:%s", c.provider, c.model)

	if until, ok := cooldowns[key]; ok && time.Now().Before(until) {
		return "", fmt.Errorf("cooldown active for %v", until.Sub(time.Now()).Round(time.Second))
	}

	var resp string
	var err error

	switch c.provider {
	case "google":
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
			cooldowns[key] = time.Now().Add(10 * time.Second)
			retryCount[key]++
			return "", fmt.Errorf("rate_limited")
		}
		return "", err
	}

	retryCount[key] = 0
	return resp, nil
}

func (c *LLMClient) completeGemini(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("no API key for google")
	}

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

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gemini error %d: %s", resp.StatusCode, string(body))
	}

	var gr map[string]interface{}
	if err := json.Unmarshal(body, &gr); err != nil {
		return "", err
	}

	if candidates, ok := gr["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if content, ok := candidates[0].(map[string]interface{})["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
				if text, ok := parts[0].(map[string]interface{})["text"].(string); ok {
					return text, nil
				}
			}
		}
	}
	return "", fmt.Errorf("empty response")
}

func (c *LLMClient) completeNvidia(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("no API key for nvidia")
	}

	url := c.apiBase
	if url == "" {
		url = "https://integrate.api.nvidia.com/v1/chat/completions"
	}

	req := map[string]interface{}{"model": c.model, "messages": msgs, "temperature": 0.7, "max_tokens": 2048}

	jsonData, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var r map[string]interface{}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}

	if choices, ok := r["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, nil
			}
		}
	}
	return "", fmt.Errorf("empty response")
}

func (c *LLMClient) completeOpenRouter(ctx context.Context, msgs []Message, tools []ToolDefinition) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("no API key for openrouter")
	}

	url := c.apiBase
	if url == "" {
		url = "https://openrouter.ai/api/v1/chat/completions"
	}

	req := map[string]interface{}{"model": c.model, "messages": msgs, "temperature": 0.7, "max_tokens": 2048}

	jsonData, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return "", err
	}
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
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}

	if choices, ok := r["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, nil
			}
		}
	}
	return "", fmt.Errorf("empty response")
}

func GetCooldownStatus() string {
	result := "📊 Model Status:\n"
	count := 0
	for key, until := range cooldowns {
		if time.Now().Before(until) {
			result += fmt.Sprintf("⏳ %s: %v remaining\n", key, until.Sub(time.Now()).Round(time.Second))
			count++
		}
	}
	if count == 0 {
		result += "✅ All models available\n"
	}
	return result
}
