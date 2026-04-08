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
	"sort"
	"strings"
	"sync"
	"time"
)

type LearningEngine struct {
	skillDir    string
	stateDir    string
	githubToken string
	githubRepo  string
	client      *http.Client
	skills      map[string]Skill
	mu          sync.RWMutex
}

type Skill struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Status       string    `json:"status"` // learning, learned, practicing
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at"`
	FilesUpdated []string  `json:"files_updated"`
	Improvements []string  `json:"improvements"`
	ToolsBuilt   []string  `json:"tools_built"`
}

type DailyReport struct {
	Date            string   `json:"date"`
	SkillsLearned   []string `json:"skills_learned"`
	SkillsPracticed []string `json:"skills_practiced"`
	FilesUpdated    []string `json:"files_updated"`
	ToolsBuilt      []string `json:"tools_built"`
	Improvements    []string `json:"improvements"`
	TotalTime       int      `json:"total_minutes"`
}

type WeeklyReport struct {
	StartDate          string        `json:"start_date"`
	EndDate            string        `json:"end_date"`
	DailyReports       []DailyReport `json:"daily_reports"`
	TotalSkillsLearned int           `json:"total_skills_learned"`
	TotalToolsBuilt    int           `json:"total_tools_built"`
	TopSkills          []string      `json:"top_skills"`
}

func NewLearningEngine(skillDir, stateDir, githubToken, githubRepo string) *LearningEngine {
	if skillDir == "" {
		skillDir = "/root/.picoclaw/workspace/skills"
	}
	if stateDir == "" {
		stateDir = "/root/.picoclaw/autonomous"
	}

	os.MkdirAll(skillDir, 0755)
	os.MkdirAll(stateDir, 0755)

	return &LearningEngine{
		skillDir:    skillDir,
		stateDir:    stateDir,
		githubToken: githubToken,
		githubRepo:  githubRepo,
		client:      &http.Client{Timeout: 30 * time.Second},
		skills:      make(map[string]Skill),
	}
}

func (e *LearningEngine) LearnSkill(ctx context.Context, skillName string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.skills[skillName]; exists {
		return fmt.Sprintf("Skill '%s' already exists", skillName), nil
	}

	skill := Skill{
		Name:        skillName,
		Description: fmt.Sprintf("Auto-learned skill: %s", skillName),
		Status:      "learning",
		StartedAt:   time.Now(),
	}

	e.skills[skillName] = skill

	go e.autoLearn(skillName)

	return fmt.Sprintf("Started learning: %s", skillName), nil
}

func (e *LearningEngine) autoLearn(skillName string) {
	llmClient := NewLLMClient("nvidia", "minimaxai/minimax-m2.5", os.Getenv("NVIDIA_API_KEY"))
	if llmClient.apiKey == "" {
		llmClient.apiKey = "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"
	}

	prompt := fmt.Sprintf(`Learn and implement the skill: %s

Create a practical implementation of this skill. 
- Write actual working code
- Create documentation
- Show what was learned
- Track files modified`, skillName)

	resp, err := llmClient.Complete(context.Background(), []Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		resp = fmt.Sprintf("Learning started for %s but LLM call failed: %v", skillName, err)
	}

	e.mu.Lock()
	if skill, ok := e.skills[skillName]; ok {
		skill.Status = "learned"
		skill.CompletedAt = time.Now()
		skill.Description = resp
		e.skills[skillName] = skill
	}
	e.mu.Unlock()

	e.saveState()
}

func (e *LearningEngine) GetSkills() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result strings.Builder
	result.WriteString("📚 Learned Skills:\n\n")

	skills := make([]Skill, 0, len(e.skills))
	for _, s := range e.skills {
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].CompletedAt.After(skills[j].CompletedAt)
	})

	for _, skill := range skills {
		status := "🔄"
		if skill.Status == "learned" {
			status = "✅"
		} else if skill.Status == "practicing" {
			status = "📈"
		}
		result.WriteString(fmt.Sprintf("%s %s\n   Files: %d, Tools: %d\n",
			status, skill.Name, len(skill.FilesUpdated), len(skill.ToolsBuilt)))
	}

	return result.String()
}

func (e *LearningEngine) BuildTool(ctx context.Context, toolName string) (string, error) {
	llmClient := NewLLMClient("nvidia", "minimaxai/minimax-m2.5", os.Getenv("NVIDIA_API_KEY"))
	if llmClient.apiKey == "" {
		llmClient.apiKey = "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"
	}

	prompt := fmt.Sprintf(`Build a useful CLI tool: %s

Create a practical, working tool in Go that can be published to GitHub.
Include:
- Complete implementation
- README
- Proper error handling
- Make it actually useful`, toolName)

	resp, err := llmClient.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		resp = fmt.Sprintf("Tool %s: Build attempted but LLM failed: %v", toolName, err)
	}

	toolDir := filepath.Join(e.skillDir, "tools", toolName)
	os.MkdirAll(toolDir, 0755)

	toolFile := filepath.Join(toolDir, "main.go")
	os.WriteFile(toolFile, []byte(resp), 0644)

	e.mu.Lock()
	if skill, ok := e.skills[toolName]; ok {
		skill.ToolsBuilt = append(skill.ToolsBuilt, toolFile)
		e.skills[toolName] = skill
	}
	e.mu.Unlock()

	if e.githubToken != "" && e.githubRepo != "" {
		go e.publishToGitHub(toolName, toolDir, resp)
	}

	return fmt.Sprintf("Built tool: %s at %s", toolName, toolDir), nil
}

func (e *LearningEngine) publishToGitHub(toolName, toolDir, code string) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/tools/%s", e.githubRepo, toolName)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"message": fmt.Sprintf("Add tool: %s", toolName),
		"content": encodeBase64(code),
	})

	req, _ := http.NewRequest("PUT", apiURL, bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+e.githubToken)
	req.Header.Set("Content-Type", "application/json")

	e.client.Do(req)
}

func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func (e *LearningEngine) GetDailyReport() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	report := DailyReport{
		Date:          time.Now().Format("2006-01-02"),
		SkillsLearned: []string{},
		FilesUpdated:  []string{},
		ToolsBuilt:    []string{},
		Improvements:  []string{},
	}

	for _, skill := range e.skills {
		if skill.CompletedAt.Format("2006-01-02") == report.Date {
			report.SkillsLearned = append(report.SkillsLearned, skill.Name)
			report.FilesUpdated = append(report.FilesUpdated, skill.FilesUpdated...)
			report.ToolsBuilt = append(report.ToolsBuilt, skill.ToolsBuilt...)
			report.Improvements = append(report.Improvements, skill.Improvements...)
		}
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data)
}

func (e *LearningEngine) GetWeeklyReport() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now()
	weekAgo := now.AddDate(0, 0, -7)

	report := WeeklyReport{
		StartDate:          weekAgo.Format("2006-01-02"),
		EndDate:            now.Format("2006-01-02"),
		DailyReports:       []DailyReport{},
		TotalSkillsLearned: 0,
		TotalToolsBuilt:    0,
	}

	for _, skill := range e.skills {
		if skill.CompletedAt.After(weekAgo) {
			report.TotalSkillsLearned++
			report.TotalToolsBuilt += len(skill.ToolsBuilt)
		}
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data)
}

func (e *LearningEngine) saveState() {
	stateFile := filepath.Join(e.stateDir, "learning_state.json")
	data, _ := json.MarshalIndent(e.skills, "", "  ")
	os.WriteFile(stateFile, data, 0644)
}

func (e *LearningEngine) loadState() {
	stateFile := filepath.Join(e.stateDir, "learning_state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &e.skills)
}

func (e *LearningEngine) AddImprovement(skillName, improvement string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if skill, ok := e.skills[skillName]; ok {
		skill.Improvements = append(skill.Improvements, improvement)
		e.skills[skillName] = skill
		e.saveState()
	}
}

func (e *LearningEngine) AddFileUpdated(skillName, file string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if skill, ok := e.skills[skillName]; ok {
		skill.FilesUpdated = append(skill.FilesUpdated, file)
		e.skills[skillName] = skill
		e.saveState()
	}
}
