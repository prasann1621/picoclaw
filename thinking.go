package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Thinker struct {
	thoughts   []Thought
	Context    map[string]interface{}
	workingMem []string
	longTerm   map[string]string
}

type Thought struct {
	Timestamp time.Time
	Type      string // plan, analyze, review, decide, reflect, code, test
	Content   string
	Result    string
}

func NewThinker() *Thinker {
	return &Thinker{
		thoughts:   make([]Thought, 0),
		Context:    make(map[string]interface{}),
		workingMem: make([]string, 0, 10),
		longTerm:   make(map[string]string),
	}
}

func (t *Thinker) Think(ctx context.Context, input string, llm *LLMClient) string {
	t.addThought("analyze", "Analyzing: "+input)

	thoughts := t.analyzeInput(input)
	t.addThought("plan", "Planning steps for: "+input)

	var plan string
	if len(thoughts) > 3 {
		steps := []string{}
		for i, thought := range thoughts {
			if i >= 5 {
				break
			}
			steps = append(steps, fmt.Sprintf("%d. %s", i+1, thought))
		}
		plan = "Step-by-step plan:\n" + strings.Join(steps, "\n")
		t.addThought("plan", plan)
	}

	t.rememberWorking(fmt.Sprintf("Task: %s", input))

	result, err := llm.Complete(ctx, []Message{
		{Role: "system", Content: t.getSystemPrompt()},
		{Role: "user", Content: input},
	}, nil)
	if err != nil {
		result = "Thinking completed with some issues: " + err.Error()
	}

	if plan != "" {
		result = plan + "\n\n" + result
	}

	t.addThought("reflect", "Result: "+truncate(result, 100))
	t.rememberWorking(fmt.Sprintf("Result: %s", truncate(result, 200)))

	return result
}

func (t *Thinker) analyzeInput(input string) []string {
	input = strings.ToLower(input)
	thoughts := []string{}

	keywords := map[string][]string{
		"code":     {"write code", "implement", "create function", "build", "develop"},
		"debug":    {"fix", "bug", "error", "issue", "problem"},
		"review":   {"review", "check", "analyze", "examine", "audit"},
		"plan":     {"how to", "best way", "strategy", "approach", "design"},
		"learn":    {"learn", "understand", "study", "explore"},
		"deploy":   {"deploy", "release", "ship", "push to production"},
		"test":     {"test", "verify", "validate", "ensure"},
		"refactor": {"refactor", "improve", "optimize", "cleanup"},
	}

	for category, words := range keywords {
		for _, word := range words {
			if strings.Contains(input, word) {
				switch category {
				case "code":
					thoughts = append(thoughts, "Need to write executable code with proper error handling")
					thoughts = append(thoughts, "Consider edge cases and input validation")
				case "debug":
					thoughts = append(thoughts, "Identify root cause, not just symptoms")
					thoughts = append(thoughts, "Check logs and recent changes")
				case "review":
					thoughts = append(thoughts, "Check for security vulnerabilities")
					thoughts = append(thoughts, "Verify edge cases and error handling")
				case "plan":
					thoughts = append(thoughts, "Consider trade-offs between approaches")
					thoughts = append(thoughts, "Account for failure scenarios")
				case "learn":
					thoughts = append(thoughts, "Research best practices and recent developments")
					thoughts = append(thoughts, "Consider practical applications")
				}
				break
			}
		}
	}

	if len(thoughts) == 0 {
		thoughts = append(thoughts, "General task - use best judgment")
		thoughts = append(thoughts, "Consider safety and security implications")
	}

	return thoughts
}

func (t *Thinker) getSystemPrompt() string {
	return `You are a senior developer with 10+ years of experience. Think like a human:

1. Before coding: Understand requirements, identify edge cases, plan structure
2. While coding: Write clean, maintainable code with proper error handling  
3. After coding: Review for bugs, security, performance
4. When stuck: Break problem down, search docs, try different approaches
5. Always: Consider "what could go wrong?", validate inputs, handle errors gracefully

Your responses should be:
- Practical and actionable
- Include code examples when helpful
- Explain the "why" not just the "what"
- Consider edge cases and failure modes
- Be concise but complete`
}

func (t *Thinker) addThought(thoughtType, content string) {
	t.thoughts = append(t.thoughts, Thought{
		Timestamp: time.Now(),
		Type:      thoughtType,
		Content:   content,
	})

	if len(t.thoughts) > 100 {
		t.thoughts = t.thoughts[len(t.thoughts)-100:]
	}
}

func (t *Thinker) rememberWorking(item string) {
	t.workingMem = append(t.workingMem, item)
	if len(t.workingMem) > 10 {
		t.workingMem = t.workingMem[len(t.workingMem)-10:]
	}
}

func (t *Thinker) rememberLongTerm(key, value string) {
	t.longTerm[key] = value
}

func (t *Thinker) recall(key string) string {
	return t.longTerm[key]
}

func (t *Thinker) GetThoughts() string {
	var result string
	for i := len(t.thoughts) - 1; i >= 0; i-- {
		if i < len(t.thoughts)-10 {
			break
		}
		ts := t.thoughts[i].Timestamp.Format("15:04")
		result += fmt.Sprintf("[%s] %s: %s\n", ts, t.thoughts[i].Type, t.thoughts[i].Content)
	}
	return result
}

func (t *Thinker) GetWorkingMemory() string {
	return "Working Memory:\n" + strings.Join(t.workingMem, "\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
