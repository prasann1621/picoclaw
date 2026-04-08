package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type GSDPlan struct {
	TaskID      string     `json:"task_id"`
	Task        string     `json:"task"`
	Goal        string     `json:"goal"`
	Steps       []GStep    `json:"steps"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Results     []string   `json:"results"`
	Errors      []string   `json:"errors"`
}

type GStep struct {
	Order       int        `json:"order"`
	Action      string     `json:"action"`
	Description string     `json:"description"`
	Status      string     `json:"status"` // pending, running, completed, failed
	Result      string     `json:"result,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type GSDEngine struct {
	plans map[string]*GSDPlan
	queue *TaskQueue
	llm   *LLMClient
	mu    sync.RWMutex
}

func NewGSDEngine(queue *TaskQueue) *GSDEngine {
	return &GSDEngine{
		plans: make(map[string]*GSDPlan),
		queue: queue,
		llm:   NewLLMClient("nvidia", "minimaxai/minimax-m2.5", "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"),
	}
}

func (g *GSDEngine) CreatePlan(taskDesc string) *GSDPlan {
	ctx := context.Background()

	prompt := fmt.Sprintf(`Create a GSD (Get Shit Done) plan for this task. Break it down into clear, actionable steps.

Task: %s

Respond in this exact format:
GOAL: <one sentence goal>
STEP 1: <action> - <what to do>
STEP 2: <action> - <what to do>
STEP 3: <action> - <what to do>
(Continue as needed, max 7 steps)`, taskDesc)

	resp, err := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return g.createSimplePlan(taskDesc)
	}

	plan := &GSDPlan{
		TaskID:    fmt.Sprintf("gsd-%d", time.Now().UnixNano()),
		Task:      taskDesc,
		Status:    "created",
		CreatedAt: time.Now(),
		Steps:     g.parseSteps(resp),
	}

	if plan.Goal == "" {
		plan.Goal = taskDesc
	}

	g.mu.Lock()
	g.plans[plan.TaskID] = plan
	g.mu.Unlock()

	return plan
}

func (g *GSDEngine) createSimplePlan(taskDesc string) *GSDPlan {
	return &GSDPlan{
		TaskID:    fmt.Sprintf("gsd-%d", time.Now().UnixNano()),
		Task:      taskDesc,
		Goal:      taskDesc,
		Status:    "created",
		CreatedAt: time.Now(),
		Steps: []GStep{
			{Order: 1, Action: "analyze", Description: "Analyze the task and requirements"},
			{Order: 2, Action: "execute", Description: "Execute the main task"},
			{Order: 3, Action: "verify", Description: "Verify the results"},
		},
	}
}

func (g *GSDEngine) parseSteps(response string) []GStep {
	lines := strings.Split(response, "\n")
	steps := []GStep{}
	order := 1

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "STEP") || strings.HasPrefix(strings.ToUpper(line), "GOAL") {
			if strings.HasPrefix(strings.ToUpper(line), "GOAL:") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				action := strings.TrimSpace(parts[1])
				if action != "" {
					steps = append(steps, GStep{
						Order:       order,
						Action:      action,
						Description: action,
						Status:      "pending",
					})
					order++
				}
			}
		}
	}

	if len(steps) == 0 {
		steps = []GStep{
			{Order: 1, Action: "execute", Description: "Execute the task"},
			{Order: 2, Action: "verify", Description: "Verify results"},
		}
	}

	return steps
}

func (g *GSDEngine) ExecutePlan(planID string, ctx context.Context) (string, error) {
	g.mu.RLock()
	plan, ok := g.plans[planID]
	g.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("plan not found: %s", planID)
	}

	plan.Status = "running"
	results := []string{}

	for i := range plan.Steps {
		step := &plan.Steps[i]
		step.Status = "running"
		now := time.Now()
		step.StartedAt = &now

		fmt.Printf("📋 GSD Step %d: %s\n", step.Order, step.Action)

		result := g.executeStep(ctx, step, plan.Task)
		step.Result = result
		results = append(results, fmt.Sprintf("Step %d (%s): %s", step.Order, step.Action, result))

		if strings.Contains(strings.ToLower(result), "error") || strings.Contains(strings.ToLower(result), "failed") {
			step.Status = "failed"
			plan.Errors = append(plan.Errors, result)
			break
		}

		step.Status = "completed"
		completed := time.Now()
		step.CompletedAt = &completed
	}

	plan.Results = results

	if plan.Status != "failed" {
		plan.Status = "completed"
		now := time.Now()
		plan.CompletedAt = &now
	}

	g.mu.Lock()
	g.plans[planID] = plan
	g.mu.Unlock()

	return g.formatResults(plan), nil
}

func (g *GSDEngine) executeStep(ctx context.Context, step *GStep, task string) string {
	action := strings.ToLower(step.Action)

	switch {
	case strings.Contains(action, "analyze") || strings.Contains(action, "review"):
		prompt := fmt.Sprintf("Analyze this task and provide insights:\n\n%s", task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp

	case strings.Contains(action, "search") || strings.Contains(action, "research"):
		prompt := fmt.Sprintf("Research and find relevant information for:\n\n%s", task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp

	case strings.Contains(action, "code") || strings.Contains(action, "implement") || strings.Contains(action, "write"):
		prompt := fmt.Sprintf("Write code or implementation for:\n\n%s\n\nInclude error handling and best practices.", task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp

	case strings.Contains(action, "test") || strings.Contains(action, "verify"):
		prompt := fmt.Sprintf("Create test cases and verify:\n\n%s", task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp

	case strings.Contains(action, "plan") || strings.Contains(action, "design"):
		prompt := fmt.Sprintf("Create a detailed plan for:\n\n%s", task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp

	default:
		prompt := fmt.Sprintf("Complete this step: %s\n\nTask: %s", step.Description, task)
		resp, _ := g.llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
		return resp
	}
}

func (g *GSDEngine) formatResults(plan *GSDPlan) string {
	result := fmt.Sprintf("📋 GSD Plan: %s\n\n🎯 Goal: %s\n\n", plan.TaskID[:12], plan.Goal)

	for _, step := range plan.Steps {
		status := map[string]string{"pending": "⏳", "running": "🔄", "completed": "✅", "failed": "❌"}[step.Status]
		result += fmt.Sprintf("%s Step %d: %s\n", status, step.Order, step.Action)
		if step.Result != "" {
			result += fmt.Sprintf("   Result: %s\n", truncate(step.Result, 100))
		}
	}

	result += "\n📊 Status: " + plan.Status

	return result
}

func (g *GSDEngine) GetPlanStatus(planID string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	plan, ok := g.plans[planID]
	if !ok {
		return "Plan not found"
	}

	return g.formatResults(plan)
}

func (g *GSDEngine) ListPlans() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.plans) == 0 {
		return "No GSD plans"
	}

	result := "📋 GSD Plans:\n"
	for id, plan := range g.plans {
		status := map[string]string{"created": "🆕", "running": "🔄", "completed": "✅", "failed": "❌"}[plan.Status]
		result += fmt.Sprintf("%s %s - %s\n", status, id[:12], truncate(plan.Task, 40))
	}

	return result
}
