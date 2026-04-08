package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WeeklyToolBuilder struct {
	pico       *Picoclaw
	toolsDir   string
	githubRepo string
	token      string
	client     *http.Client
	mu         sync.RWMutex
	schedule   *WeeklySchedule
}

type WeeklySchedule struct {
	DayOfWeek    time.Weekday
	Hour         int
	Minute       int
	LastRun      time.Time
	NextRun      time.Time
	ToolsToBuild []string
}

type ToolSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Features    []string `json:"features"`
	Category    string   `json:"category"`
	Priority    int      `json:"priority"`
}

func NewWeeklyToolBuilder(pico *Picoclaw, githubToken, githubRepo string) *WeeklyToolBuilder {
	wtb := &WeeklyToolBuilder{
		pico:       pico,
		toolsDir:   "/root/.picoclaw/workspace/tools",
		githubRepo: githubRepo,
		token:      githubToken,
		client:     &http.Client{Timeout: 60 * time.Second},
		schedule: &WeeklySchedule{
			DayOfWeek:    time.Sunday,
			Hour:         2,
			Minute:       0,
			ToolsToBuild: []string{},
		},
	}

	os.MkdirAll(wtb.toolsDir, 0755)
	wtb.initToolSpecs()
	return wtb
}

func (wtb *WeeklyToolBuilder) initToolSpecs() {
	wtb.schedule.ToolsToBuild = []string{
		"server-health-checker",
		"log-analyzer",
		"backup-automation",
		"docker-manager",
		"ssl-cert-checker",
		"process-monitor",
		"disk-usage-reporter",
		"network-scanner",
		"security-auditor",
		"config-backup",
		"service-restarter",
		"cron-manager",
		"database-backup",
		"api-tester",
		"load-balancer",
	}
}

func (wtb *WeeklyToolBuilder) Start(ctx context.Context) {
	wtb.calculateNextRun()

	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Now().After(wtb.schedule.NextRun) {
					wtb.RunWeeklyBuild(ctx)
				}
			}
		}
	}()

	go func() {
		time.Sleep(5 * time.Second)
		wtb.RunWeeklyBuild(ctx)
	}()

	fmt.Println("✓ Weekly Tool Builder: Active")
}

func (wtb *WeeklyToolBuilder) calculateNextRun() {
	now := time.Now()
	for wtb.schedule.NextRun.Before(now) {
		wtb.schedule.NextRun = wtb.schedule.NextRun.AddDate(0, 0, 7)
	}
	if wtb.schedule.NextRun.IsZero() {
		wtb.schedule.NextRun = time.Date(now.Year(), now.Month(), now.Day(), wtb.schedule.Hour, wtb.schedule.Minute, 0, 0, now.Location())
		if now.After(wtb.schedule.NextRun) {
			wtb.schedule.NextRun = wtb.schedule.NextRun.AddDate(0, 0, 7)
		}
	}
}

func (wtb *WeeklyToolBuilder) RunWeeklyBuild(ctx context.Context) {
	wtb.mu.Lock()
	wtb.schedule.LastRun = time.Now()
	wtb.calculateNextRun()
	wtb.mu.Unlock()

	var results []string

	for i, toolName := range wtb.schedule.ToolsToBuild {
		result, err := wtb.BuildTool(ctx, toolName)
		if err != nil {
			results = append(results, fmt.Sprintf("❌ %s: %v", toolName, err))
		} else {
			results = append(results, fmt.Sprintf("✅ %s", result))
		}

		if i < len(wtb.schedule.ToolsToBuild)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	if wtb.pico.telegram != nil && len(wtb.pico.telegram.allowFrom) > 0 {
		report := "🔨 Weekly Tool Build Report\n\n"
		report += "Time: " + time.Now().Format("2006-01-02 15:04") + "\n"
		report += "Next Run: " + wtb.schedule.NextRun.Format("2006-01-02 15:04") + "\n\n"

		for _, r := range results {
			report += r + "\n"
		}

		wtb.pico.telegram.sendMessage(wtb.pico.telegram.allowFrom[0], report)
	}

	wtb.publishAllToGitHub(ctx)
}

func (wtb *WeeklyToolBuilder) BuildTool(ctx context.Context, toolName string) (string, error) {
	llm := NewLLMClient("nvidia", "minimaxai/minimax-m2.5", os.Getenv("NVIDIA_API_KEY"))
	if llm.apiKey == "" {
		llm.apiKey = "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"
	}

	category := wtb.categorizeTool(toolName)

	prompt := fmt.Sprintf(`Build a complete, production-ready CLI tool: %s

Category: %s
Tool Name: %s

Requirements:
1. Complete Go implementation with proper error handling
2. Comprehensive README.md with usage examples
3. Makefile for building
4. Example configurations
5. Unit tests structure
6. Dockerfile for containerization

Output format:
- main.go (complete code)
- README.md (documentation)
- Makefile (build instructions)
- config.yaml.example (example config)

Make it actually useful and production-ready.`, toolName, category, toolName)

	resp, err := llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return "", err
	}

	toolDir := filepath.Join(wtb.toolsDir, toolName)
	os.MkdirAll(toolDir, 0755)

	wtb.parseAndSaveTool(resp, toolDir)

	return toolName, nil
}

func (wtb *WeeklyToolBuilder) categorizeTool(toolName string) string {
	categories := map[string][]string{
		"monitoring":  {"health", "monitor", "checker", "reporter", "scanner"},
		"automation":  {"auto", "backup", "manager", "restarter"},
		"security":    {"security", "ssl", "audit"},
		"networking":  {"network", "load", "balancer"},
		"database":    {"database", "db"},
		"development": {"tester", "analyzer"},
	}

	for cat, keywords := range categories {
		for _, kw := range keywords {
			if strings.Contains(toolName, kw) {
				return cat
			}
		}
	}
	return "utilities"
}

func (wtb *WeeklyToolBuilder) parseAndSaveTool(code string, toolDir string) {
	files := map[string]string{
		"main.go":   "",
		"README.md": "",
		"Makefile":  "",
	}

	sections := strings.Split(code, "```")
	for i := 1; i < len(sections); i += 2 {
		if i+1 >= len(sections) {
			continue
		}
		content := strings.TrimSpace(sections[i+1])

		if strings.HasPrefix(sections[i], "go") {
			files["main.go"] = content
		} else if strings.HasPrefix(sections[i], "md") || strings.HasPrefix(sections[i], "markdown") {
			files["README.md"] = content
		} else if strings.HasPrefix(sections[i], "makefile") || strings.HasPrefix(sections[i], "make") {
			files["Makefile"] = content
		} else {
			files["main.go"] = content
		}
	}

	if files["main.go"] == "" {
		files["main.go"] = code
	}

	for filename, content := range files {
		if content != "" {
			os.WriteFile(filepath.Join(toolDir, filename), []byte(content), 0644)
		}
	}
}

func (wtb *WeeklyToolBuilder) publishAllToGitHub(ctx context.Context) {
	if wtb.token == "" || wtb.githubRepo == "" {
		return
	}

	entries, err := os.ReadDir(wtb.toolsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		toolName := entry.Name()
		toolDir := filepath.Join(wtb.toolsDir, toolName)

		toolFiles, _ := os.ReadDir(toolDir)
		for _, f := range toolFiles {
			if f.IsDir() {
				continue
			}
			content, _ := os.ReadFile(filepath.Join(toolDir, f.Name()))
			wtb.publishFileToGitHub(toolName, f.Name(), string(content))
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (wtb *WeeklyToolBuilder) publishFileToGitHub(toolName, filename, content string) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/tools/%s/%s",
		wtb.githubRepo, toolName, filename)

	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	body := map[string]interface{}{
		"message": fmt.Sprintf("Add tool: %s - %s", toolName, filename),
		"content": encoded,
	}

	data, _ := json.Marshal(body)

	req, _ := http.NewRequest("PUT", apiURL, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+wtb.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := wtb.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (wtb *WeeklyToolBuilder) GetScheduleInfo() string {
	wtb.mu.RLock()
	defer wtb.mu.RUnlock()

	return fmt.Sprintf(`📅 Weekly Tool Builder Schedule
• Day: %s
• Time: %02d:%02d
• Last Run: %s
• Next Run: %s
• Tools in queue: %d`,
		wtb.schedule.DayOfWeek,
		wtb.schedule.Hour, wtb.schedule.Minute,
		wtb.schedule.LastRun.Format("2006-01-02 15:04"),
		wtb.schedule.NextRun.Format("2006-01-02 15:04"),
		len(wtb.schedule.ToolsToBuild))
}

func (wtb *WeeklyToolBuilder) ForceBuild(ctx context.Context) string {
	wtb.RunWeeklyBuild(ctx)
	return "Weekly build triggered"
}
