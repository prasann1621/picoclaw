package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ResearchTask struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Priority    int        `json:"priority"`
	Status      string     `json:"status"`
	Results     string     `json:"results,omitempty"`
	Fixes       []string   `json:"fixes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type MazgaonResearch struct {
	tasks     map[string]*ResearchTask
	gsdEngine *GSDEngine
	llm       *LLMClient
}

func NewMazgaonResearch(gsd *GSDEngine) *MazgaonResearch {
	return &MazgaonResearch{
		tasks:     make(map[string]*ResearchTask),
		gsdEngine: gsd,
		llm:       NewLLMClient("nvidia", "minimaxai/minimax-m2.5", "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"),
	}
}

func (m *MazgaonResearch) RunAllResearch() string {
	tasks := []struct {
		id          string
		title       string
		description string
		priority    int
	}{
		{"mazgaon-001", "SEO Analysis", "Analyze SEO for aaplemazgaon.quintoxsolutions.com - meta tags, keywords, schema, performance, mobile", 5},
		{"mazgaon-002", "Content Enhancement", "Research content gaps - heritage articles, historical images, interactive maps, multilingual", 4},
		{"mazgaon-003", "Performance Optimization", "Research Three.js/Vanta.js/GSAP optimizations - lazy loading, CDN, image compression", 4},
		{"mazgaon-004", "UX/UI Improvements", "Research UX - navigation, accessibility WCAG, dark mode, responsive issues", 3},
		{"mazgaon-005", "Technical Debt Audit", "Audit codebase - deprecated APIs, broken links, security, outdated dependencies", 4},
		{"mazgaon-006", "Multi-page Strategy", "Plan multi-page: History timeline, Heritage walk map, Community stories, Gallery, Events", 4},
		{"mazgaon-007", "Competitor Analysis", "Research similar heritage websites - features, SEO, content, engagement", 2},
		{"mazgaon-008", "Local SEO Strategy", "Research local SEO - Google Business, backlinks, community, events", 3},
	}

	var results string
	for _, t := range tasks {
		fmt.Printf("🔍 Running research: %s\n", t.title)
		result := m.runResearch(t.id, t.title, t.description)
		m.tasks[t.id] = result

		status := "✅"
		if result.Status == "failed" {
			status = "❌"
		}

		results += fmt.Sprintf("%s %s\n   Issues: %d | Fixes: %d\n\n",
			status, result.Title, len(result.Fixes), len(result.Fixes))

		if len(result.Fixes) > 0 {
			results += "Suggested Fixes:\n"
			for i, fix := range result.Fixes {
				if i >= 5 {
					break
				}
				results += fmt.Sprintf("   • %s\n", fix)
			}
			results += "\n"
		}

		time.Sleep(2 * time.Second)
	}

	return results
}

func (m *MazgaonResearch) runResearch(id, title, desc string) *ResearchTask {
	task := &ResearchTask{
		ID:          id,
		Title:       title,
		Description: desc,
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	prompt := fmt.Sprintf(`You are a senior SEO and web development expert. Research and analyze this website: https://aaplemazgaon.quintoxsolutions.com/

Task: %s

Provide:
1. Current status analysis (1-2 sentences)
2. 5-7 specific issues found (format: "Issue: Description")
3. 5-7 recommended fixes (format: "Fix: What to do")

Focus on: SEO, performance, UX, accessibility, content, technical issues.`, title)

	resp, err := m.llm.Complete(context.Background(), []Message{{Role: "user", Content: prompt}}, nil)

	if err != nil {
		task.Status = "failed"
		task.Results = fmt.Sprintf("Research failed: %v", err)
		return task
	}

	task.Results = resp
	task.Status = "completed"
	now := time.Now()
	task.CompletedAt = &now

	task.Fixes = m.parseFixes(resp)

	return task
}

func (m *MazgaonResearch) parseFixes(resp string) []string {
	lines := strings.Split(resp, "\n")
	fixes := []string{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "FIX:") {
			fix := strings.TrimPrefix(strings.ToUpper(line), "FIX:")
			fix = strings.TrimSpace(fix)
			if fix != "" {
				fixes = append(fixes, fix)
			}
		}
	}

	if len(fixes) == 0 {
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), "recommend") ||
				strings.Contains(strings.ToLower(line), "should") ||
				strings.Contains(strings.ToLower(line), "need to") {
				fixes = append(fixes, strings.TrimSpace(line))
			}
		}
	}

	return fixes
}

func (m *MazgaonResearch) GetFullReport() string {
	result := "📊 MAZGAON WEBSITE RESEARCH REPORT\n"
	result += "===================================\n\n"

	for _, task := range m.tasks {
		status := map[string]string{"running": "🔄", "completed": "✅", "failed": "❌"}[task.Status]

		result += fmt.Sprintf("%s %s\n", status, task.Title)
		result += fmt.Sprintf("   ID: %s\n", task.ID)

		if task.CompletedAt != nil {
			result += fmt.Sprintf("   Completed: %s\n", task.CompletedAt.Format("2006-01-02 15:04"))
		}

		if len(task.Fixes) > 0 {
			result += fmt.Sprintf("   Fixes Found: %d\n", len(task.Fixes))
		}
		result += "\n"
	}

	result += "\n📋 DETAILED FINDINGS:\n"
	result += "====================\n\n"

	for _, task := range m.tasks {
		result += fmt.Sprintf("## %s\n%s\n\n", task.Title, task.Results)
	}

	return result
}

func (m *MazgaonResearch) GetFixesList() []string {
	var allFixes []string
	fixesByPriority := make(map[int][]string)

	for _, task := range m.tasks {
		for _, fix := range task.Fixes {
			fixesByPriority[task.Priority] = append(fixesByPriority[task.Priority], fix)
		}
	}

	for i := 5; i >= 1; i-- {
		allFixes = append(allFixes, fixesByPriority[i]...)
	}

	return allFixes
}
