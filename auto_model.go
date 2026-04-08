package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ModelSelector struct {
	models    []ModelInfo
	current   int
	mu        sync.RWMutex
	stats     map[string]ModelStats
	failCount map[string]int
}

type ModelInfo struct {
	Provider    string
	Model       string
	APIKey      string
	Strengths   []string // coding, reasoning, fast, creative, analysis
	Speed       string   // fast, medium, slow
	ContextSize int
}

type ModelStats struct {
	TotalCalls   int
	SuccessCalls int
	FailCalls    int
	AvgLatency   time.Duration
	LastUsed     time.Time
}

func NewModelSelector() *ModelSelector {
	ms := &ModelSelector{
		models: []ModelInfo{
			{Provider: "google", Model: "gemini-2.0-flash", APIKey: "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y", Strengths: []string{"fast", "coding", "analysis"}, Speed: "fast", ContextSize: 1_000_000},
			{Provider: "google", Model: "gemini-1.5-flash", APIKey: "AIzaSyAz5EKsooNW0USah9eQPhdlNyNqTb8hW0Y", Strengths: []string{"fast", "creative"}, Speed: "fast", ContextSize: 1_000_000},
			{Provider: "nvidia", Model: "minimaxai/minimax-m2.5", APIKey: "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", Strengths: []string{"fast", "coding", "reasoning"}, Speed: "fast", ContextSize: 128_000},
			{Provider: "nvidia", Model: "nvidia/llama-3.1-nemotron-nano-8b-v1", APIKey: "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", Strengths: []string{"fast", "efficient"}, Speed: "fast", ContextSize: 128_000},
			{Provider: "nvidia", Model: "meta/llama-3.1-8b-instruct", APIKey: "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", Strengths: []string{"coding", "reasoning"}, Speed: "medium", ContextSize: 128_000},
			{Provider: "nvidia", Model: "mistralai/mistral-small-24b-instruct", APIKey: "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", Strengths: []string{"reasoning", "analysis"}, Speed: "medium", ContextSize: 128_000},
			{Provider: "nvidia", Model: "deepseek-ai/deepseek-r1-distill-qwen-7b", APIKey: "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", Strengths: []string{"reasoning", "analysis"}, Speed: "medium", ContextSize: 64_000},
			{Provider: "nvidia", Model: "qwen/qwen2.5-coder-32b-instruct", APIKey: "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", Strengths: []string{"coding", "fast"}, Speed: "medium", ContextSize: 128_000},
			{Provider: "nvidia", Model: "mistralai/mistral-large-2-instruct", APIKey: "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", Strengths: []string{"reasoning", "coding", "creative"}, Speed: "slow", ContextSize: 128_000},
			{Provider: "nvidia", Model: "nvidia/nemotron-4-340b-instruct", APIKey: "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", Strengths: []string{"reasoning", "analysis", "coding"}, Speed: "slow", ContextSize: 128_000},
			{Provider: "openrouter", Model: "deepseek/deepseek-r1:free", APIKey: "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", Strengths: []string{"reasoning", "free"}, Speed: "medium", ContextSize: 64_000},
		},
		stats:     make(map[string]ModelStats),
		failCount: make(map[string]int),
	}

	for _, m := range ms.models {
		ms.stats[m.Model] = ModelStats{}
	}

	return ms
}

func (ms *ModelSelector) SelectModel(input string) *ModelInfo {
	inputLower := strings.ToLower(input)
	wordCount := len(strings.Fields(input))

	var preferredStrengths []string

	switch {
	case strings.Contains(inputLower, "code") || strings.Contains(inputLower, "implement") ||
		strings.Contains(inputLower, "write") || strings.Contains(inputLower, "function") ||
		strings.Contains(inputLower, "api") || strings.Contains(inputLower, "debug"):
		preferredStrengths = append(preferredStrengths, "coding")

	case strings.Contains(inputLower, "reason") || strings.Contains(inputLower, "think") ||
		strings.Contains(inputLower, "explain") || strings.Contains(inputLower, "why") ||
		strings.Contains(inputLower, "analyze") || strings.Contains(inputLower, "logic"):
		preferredStrengths = append(preferredStrengths, "reasoning")

	case strings.Contains(inputLower, "create") || strings.Contains(inputLower, "write") ||
		strings.Contains(inputLower, "story") || strings.Contains(inputLower, "creative"):
		preferredStrengths = append(preferredStrengths, "creative")

	case strings.Contains(inputLower, "fast") || strings.Contains(inputLower, "quick") ||
		wordCount < 10:
		preferredStrengths = append(preferredStrengths, "fast")

	case strings.Contains(inputLower, "complex") || strings.Contains(inputLower, "detailed") ||
		wordCount > 500:
		preferredStrengths = append(preferredStrengths, "reasoning", "analysis")
	}

	if len(preferredStrengths) == 0 {
		preferredStrengths = []string{"fast", "coding"}
	}

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var bestModel *ModelInfo
	bestScore := -1

	for i := range ms.models {
		model := &ms.models[i]

		if ms.failCount[model.Model] >= 3 {
			continue
		}

		score := 0

		for _, strength := range preferredStrengths {
			for _, s := range model.Strengths {
				if s == strength {
					score += 10
					break
				}
			}
		}

		stats := ms.stats[model.Model]
		if stats.TotalCalls > 0 {
			successRate := float64(stats.SuccessCalls) / float64(stats.TotalCalls)
			score += int(successRate * 5)
		}

		if model.Speed == "fast" {
			score += 2
		}

		if model.Model == "gemini-2.0-flash" {
			score += 3
		}

		if score > bestScore {
			bestScore = score
			bestModel = model
		}
	}

	if bestModel == nil {
		bestModel = &ms.models[0]
	}

	return bestModel
}

func (ms *ModelSelector) RecordSuccess(model string, latency time.Duration) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	stats := ms.stats[model]
	stats.TotalCalls++
	stats.SuccessCalls++
	stats.AvgLatency = (stats.AvgLatency*time.Duration(stats.SuccessCalls-1) + latency) / time.Duration(stats.SuccessCalls)
	stats.LastUsed = time.Now()
	ms.stats[model] = stats
	ms.failCount[model] = 0
}

func (ms *ModelSelector) RecordFailure(model string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	stats := ms.stats[model]
	stats.TotalCalls++
	stats.FailCalls++
	ms.stats[model] = stats
	ms.failCount[model]++
}

func (ms *ModelSelector) GetBestWorkingModel() *ModelInfo {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	for _, model := range ms.models {
		if ms.failCount[model.Model] < 3 {
			return &model
		}
	}

	return &ms.models[0]
}

func (ms *ModelSelector) GetModelStats() string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	result := "📊 Model Performance:\n\n"
	for _, model := range ms.models {
		stats := ms.stats[model.Model]
		successRate := 0
		if stats.TotalCalls > 0 {
			successRate = (stats.SuccessCalls * 100) / stats.TotalCalls
		}
		fails := ms.failCount[model.Model]
		result += fmt.Sprintf("• %s: %d%% success (%d/%d) [fails: %d]\n",
			truncate(model.Model, 30), successRate, stats.SuccessCalls, stats.TotalCalls, fails)
	}
	return result
}

func (ms *ModelSelector) AutoSwitch(ctx context.Context, input string, msgs []Message, tools []ToolDefinition) (string, *LLMClient, error) {
	model := ms.SelectModel(input)

	fmt.Printf("🤖 Auto-selected model: %s (%s)\n", model.Model, model.Provider)

	client := NewLLMClient(model.Provider, model.Model, model.APIKey)
	start := time.Now()

	resp, err := client.Complete(ctx, msgs, tools)
	latency := time.Since(start)

	if err != nil {
		ms.RecordFailure(model.Model)

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") {
			fmt.Printf("⚠️ Rate limited on %s, trying next model...\n", model.Model)

			for _, nextModel := range ms.models {
				if nextModel.Model == model.Model {
					continue
				}
				if ms.failCount[nextModel.Model] >= 3 {
					continue
				}

				fmt.Printf("🔄 Trying: %s\n", nextModel.Model)
				nextClient := NewLLMClient(nextModel.Provider, nextModel.Model, nextModel.APIKey)
				start = time.Now()
				resp, err = nextClient.Complete(ctx, msgs, tools)
				latency = time.Since(start)

				if err == nil {
					ms.RecordSuccess(nextModel.Model, latency)
					return resp, nextClient, nil
				}
				ms.RecordFailure(nextModel.Model)
			}
		}

		return "", client, err
	}

	ms.RecordSuccess(model.Model, latency)
	return resp, client, nil
}
