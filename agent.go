package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Agent struct {
	config    *AgentConfig
	tools     map[string]Tool
	messages  []Message
	mu        sync.RWMutex
	ctx       context.Context
	llmClient *LLMClient
	thinker   *Thinker
	modelSel  *ModelSelector
}

type AgentConfig struct {
	Name         string
	Model        string
	Provider     string
	APIKey       string
	Instructions string
	Temperature  float64
	MaxTokens    int
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Tool func(ctx context.Context, args map[string]interface{}) (string, error)

type ToolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

func NewAgent(cfg *AgentConfig) *Agent {
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	return &Agent{
		config:    cfg,
		tools:     make(map[string]Tool),
		messages:  make([]Message, 0),
		ctx:       context.Background(),
		llmClient: NewLLMClient(cfg.Provider, cfg.Model, cfg.APIKey),
		thinker:   NewThinker(),
		modelSel:  NewModelSelector(),
	}
}

func (a *Agent) RegisterTool(name string, tool Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools[name] = tool
}

func (a *Agent) GetTools() []ToolDefinition {
	a.mu.RLock()
	defer a.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(a.tools))
	for name, tool := range a.tools {
		desc := getToolDescription(tool)
		defs = append(defs, ToolDefinition{
			Name:        name,
			Description: desc,
			Parameters: Parameters{
				Type:       "object",
				Properties: map[string]Property{},
				Required:   []string{},
			},
		})
	}
	return defs
}

func (a *Agent) Run(userInput string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.messages = append(a.messages, Message{
		Role:    "user",
		Content: userInput,
	})

	systemMsg := a.config.Instructions
	if systemMsg == "" {
		systemMsg = a.thinker.getSystemPrompt()
	}

	msgs := append([]Message{{Role: "system", Content: systemMsg}}, a.messages...)

	tools := a.GetTools()

	response, err := a.llmClient.Complete(a.ctx, msgs, tools)
	if err != nil {
		return "", fmt.Errorf("LLM call failed after retries: %w", err)
	}

	a.messages = append(a.messages, Message{
		Role:    "assistant",
		Content: response,
	})

	a.thinker.rememberWorking(fmt.Sprintf("User: %s", truncate(userInput, 100)))
	a.thinker.rememberWorking(fmt.Sprintf("Response: %s", truncate(response, 100)))

	return response, nil
}

func getToolDescription(tool Tool) string {
	return "A tool available for use"
}

type MessageHistory struct {
	Messages []Message
	mu       sync.RWMutex
}

func NewMessageHistory() *MessageHistory {
	return &MessageHistory{
		Messages: make([]Message, 0),
	}
}

func (h *MessageHistory) Add(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Messages = append(h.Messages, msg)
}

func (h *MessageHistory) GetAll() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]Message, len(h.Messages))
	copy(result, h.Messages)
	return result
}

func (h *MessageHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Messages = h.Messages[:0]
}

type ToolRegistry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) Register(name string, tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

type AgentContext struct {
	Data map[string]interface{}
	mu   sync.RWMutex
}

func NewAgentContext() *AgentContext {
	return &AgentContext{
		Data: make(map[string]interface{}),
	}
}

func (c *AgentContext) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Data[key] = value
}

func (c *AgentContext) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.Data[key]
	return val, ok
}

func (c *AgentContext) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Data = make(map[string]interface{})
}

type AgentLoop struct {
	agent      *Agent
	maxRetries int
	timeout    time.Duration
}

func NewAgentLoop(agent *Agent) *AgentLoop {
	return &AgentLoop{
		agent:      agent,
		maxRetries: 3,
		timeout:    60 * time.Second,
	}
}

func (l *AgentLoop) RunWithRetry(input string) (string, error) {
	var lastErr error
	for i := 0; i < l.maxRetries; i++ {
		_, cancel := context.WithTimeout(l.agent.ctx, l.timeout)
		defer cancel()

		result, err := l.agent.Run(input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		fmt.Printf("Attempt %d failed: %v\n", i+1, err)
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	return "", fmt.Errorf("failed after %d retries: %w", l.maxRetries, lastErr)
}

type Response struct {
	Content  string
	Messages []Message
	ToolCall *ToolCall
}

type ToolCall struct {
	Name      string
	Arguments map[string]interface{}
}
