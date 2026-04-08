package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type TelegramBot struct {
	token     string
	allowFrom []int64
	pico      *Picoclaw
	client    *http.Client
	updates   chan Update
	mu        sync.Mutex
}

type Update struct {
	UpdateID int64      `json:"update_id"`
	Message  *TgMessage `json:"message"`
}

type TgMessage struct {
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
	From User   `json:"from"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID int64 `json:"id"`
}

func NewTelegramBot(token string, allowFrom []int64, pico *Picoclaw) *TelegramBot {
	return &TelegramBot{
		token:     token,
		allowFrom: allowFrom,
		pico:      pico,
		client:    &http.Client{Timeout: 30 * time.Second},
		updates:   make(chan Update, 100),
	}
}

func (t *TelegramBot) Start(ctx context.Context) {
	go t.getUpdates(ctx)
	go t.handleUpdates(ctx)
}

func (t *TelegramBot) getUpdates(ctx context.Context) {
	offset := int64(0)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=60", t.token)
			if offset > 0 {
				url += fmt.Sprintf("&offset=%d", offset)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp, err := t.client.Do(req)
			if err != nil {
				fmt.Printf("Telegram error: %v\n", err)
				continue
			}

			var result struct {
				OK     bool     `json:"ok"`
				Result []Update `json:"result"`
			}
			body, _ := io.ReadAll(resp.Body)
			json.Unmarshal(body, &result)
			resp.Body.Close()

			if result.OK {
				for _, update := range result.Result {
					t.updates <- update
					offset = update.UpdateID + 1
				}
			}
		}
	}
}

func (t *TelegramBot) handleUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-t.updates:
			if update.Message == nil {
				continue
			}
			msg := update.Message

			if len(t.allowFrom) > 0 {
				allowed := false
				for _, id := range t.allowFrom {
					if msg.From.ID == id {
						allowed = true
						break
					}
				}
				if !allowed {
					t.sendMessage(msg.Chat.ID, "⛔ You are not authorized to use this bot.")
					continue
				}
			}

			go t.processMessage(msg)
		}
	}
}

func (t *TelegramBot) processMessage(msg *TgMessage) {
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID

	if text == "" {
		return
	}

	if strings.HasPrefix(text, "/") {
		t.handleCommand(msg)
		return
	}

	t.sendMessage(chatID, "🧠 Thinking...")

	input := text
	thinker := t.pico.agent.thinker

	thoughts := thinker.analyzeInput(input)
	thinking := "📝 Analysis:\n"
	for i, thought := range thoughts {
		if i >= 5 {
			break
		}
		thinking += fmt.Sprintf("• %s\n", thought)
	}
	t.sendMessage(chatID, thinking)
	time.Sleep(500 * time.Millisecond)

	response, err := t.pico.agent.Run(text)
	if err != nil {
		t.sendMessage(chatID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if len(response) > 4000 {
		for i := 0; i < len(response); i += 4000 {
			end := i + 4000
			if end > len(response) {
				end = len(response)
			}
			t.sendMessage(chatID, response[i:end])
		}
	} else {
		t.sendMessage(chatID, response)
	}
}

func (t *TelegramBot) handleCommand(msg *TgMessage) {
	text := strings.TrimSpace(msg.Text)
	chatID := msg.Chat.ID
	parts := strings.Fields(text)
	command := parts[0]
	args := parts[1:]

	helpText := `📖 Picoclaw v2.0 Commands:

/start, /help   - Show this help
/status         - Agent status
/tools          - List tools
/skill <name>   - Learn a new skill
/skills         - List learned skills
/build <name>   - Build a tool
/report         - Get daily report
/weekly         - Get weekly report
/todo add|list|run|plan - Task management
/mazgaon        - Run website research for aaplemazgaon.quintoxsolutions.com
/search <query> - Web search
/time           - Current time
/config         - Show config
/models         - List available models
/set_model <name> - Switch model
/restart        - Restart agent`

	switch command {
	case "/start", "/help":
		t.sendMessage(chatID, helpText)
	case "/status":
		status := fmt.Sprintf(`✅ Agent Status:
• Name: %s
• Model: %s (%s)
• Tools: %d`,
			t.pico.agent.config.Name,
			t.pico.agent.config.Model,
			t.pico.agent.config.Provider,
			len(t.pico.agent.tools),
		)
		t.sendMessage(chatID, status)
	case "/tools":
		tools := t.pico.tools.List()
		t.sendMessage(chatID, "🔧 Tools:\n"+strings.Join(tools, "\n"))
	case "/skills":
		if t.pico.learning != nil {
			t.sendMessage(chatID, t.pico.learning.GetSkills())
		} else {
			t.sendMessage(chatID, "Learning engine not enabled")
		}
	case "/skill":
		if len(args) == 0 {
			t.sendMessage(chatID, "Usage: /skill <skill_name>")
			return
		}
		skillName := strings.Join(args, " ")
		if t.pico.learning != nil {
			result, _ := t.pico.learning.LearnSkill(context.Background(), skillName)
			t.sendMessage(chatID, result)
		} else {
			t.sendMessage(chatID, "Learning engine not enabled")
		}
	case "/build":
		if len(args) == 0 {
			t.sendMessage(chatID, "Usage: /build <tool_name>")
			return
		}
		toolName := strings.Join(args, " ")
		if t.pico.learning != nil {
			result, _ := t.pico.learning.BuildTool(context.Background(), toolName)
			t.sendMessage(chatID, result)
		} else {
			t.sendMessage(chatID, "Learning engine not enabled")
		}
	case "/report":
		if t.pico.learning != nil {
			t.sendMessage(chatID, t.pico.learning.GetDailyReport())
		} else {
			t.sendMessage(chatID, "Learning engine not enabled")
		}
	case "/weekly":
		if t.pico.learning != nil {
			t.sendMessage(chatID, t.pico.learning.GetWeeklyReport())
		} else {
			t.sendMessage(chatID, "Learning engine not enabled")
		}
	case "/time":
		now := time.Now()
		t.sendMessage(chatID, fmt.Sprintf("🕐 %s", now.Format("2006-01-02 15:04:05")))
	case "/search":
		if len(args) == 0 {
			t.sendMessage(chatID, "Usage: /search <query>")
			return
		}
		query := strings.Join(args, " ")
		result, _ := toolSearchWeb(context.Background(), map[string]interface{}{"query": query})
		if len(result) > 4000 {
			result = result[:4000]
		}
		t.sendMessage(chatID, "🔍 Results:\n"+result)
	case "/config":
		cfg := fmt.Sprintf(`⚙️ Config:
• Model: %s
• Provider: %s`,
			t.pico.config.Model,
			t.pico.config.Provider,
		)
		t.sendMessage(chatID, cfg)
	case "/models":
		t.sendMessage(chatID, `📋 Available Models:

google: gemini-2.0-flash, gemini-1.5-flash
openai: gpt-4o, gpt-4o-mini
anthropic: claude-3-5-sonnet
nvidia: minimax-m2.5, nemotron-3-super
openrouter: deepseek/deepseek-r1, meta-llama/llama-3.3

Use /set_model <provider/model> to switch`)
	case "/set_model":
		if len(args) == 0 {
			t.sendMessage(chatID, "Usage: /set_model <provider/model>")
			return
		}
		model := args[0]
		t.pico.agent.config.Model = model
		if len(args) > 1 {
			t.pico.agent.config.Provider = args[1]
		}
		t.sendMessage(chatID, fmt.Sprintf("✅ Model set to: %s", model))
	case "/add_key":
		if len(args) < 2 {
			t.sendMessage(chatID, "Usage: /add_key <provider> <api_key>")
			return
		}
		provider := args[0]
		apiKey := args[1]
		t.pico.agent.config.Provider = provider
		t.pico.agent.config.APIKey = apiKey
		t.sendMessage(chatID, fmt.Sprintf("✅ API key set for: %s", provider))
	case "/restart":
		t.sendMessage(chatID, "🔄 Restarting...")
		t.pico.agent = NewAgent(t.pico.agent.config)
		registerAllTools(t.pico)
		t.sendMessage(chatID, "✅ Restarted!")
	case "/todo", "/taskdo":
		if len(args) == 0 {
			t.sendMessage(chatID, `📋 Todo Commands:
/todo add <task> - Add task to queue
/todo list - List pending tasks
/todo run - Run next task with auto model
/todo plan <task> - Create GSD plan for task`)
			return
		}
		action := args[0]
		taskArgs := args[1:]

		switch action {
		case "add", "new":
			if len(taskArgs) == 0 {
				t.sendMessage(chatID, "Usage: /todo add <task description>")
				return
			}
			taskDesc := strings.Join(taskArgs, " ")
			if t.pico.taskQueue != nil {
				task := t.pico.taskQueue.AddTask("todo", taskDesc, 3)
				t.pico.taskQueue.SetTaskData(task.ID, "auto_retry", true)
				t.sendMessage(chatID, fmt.Sprintf("✅ Task added to queue: %s (ID: %s)\nWill auto-run when rate limit clears", taskDesc, task.ID))
			}
		case "list", "ls":
			if t.pico.taskQueue != nil {
				tasks := t.pico.taskQueue.ListTasks()
				if len(tasks) == 0 {
					t.sendMessage(chatID, "No tasks in queue")
				} else {
					list := "📋 Pending Tasks:\n"
					for i, task := range tasks {
						if i >= 10 {
							break
						}
						list += fmt.Sprintf("%d. %s (ID: %s)\n", i+1, task.Title, task.ID)
					}
					t.sendMessage(chatID, list)
				}
			}
		case "run", "do", "exec":
			if t.pico.taskQueue != nil {
				t.sendMessage(chatID, "🚀 Running task with auto model selection...")
				pending := t.pico.taskQueue.GetPendingTasks()
				if len(pending) == 0 {
					t.sendMessage(chatID, "No pending tasks")
					return
				}
				task := pending[0]

				go func() {
					result := t.runTaskWithAutoRetry(task.ID)
					t.sendMessage(chatID, fmt.Sprintf("✅ Task completed:\n%s", result))
				}()
			}
		case "plan", "gsd":
			if len(taskArgs) == 0 {
				t.sendMessage(chatID, "Usage: /todo plan <task>")
				return
			}
			taskDesc := strings.Join(taskArgs, " ")
			t.sendMessage(chatID, "🧠 Creating GSD plan...")

			plan := t.createGSDPlan(taskDesc)
			if t.pico.taskQueue != nil {
				task := t.pico.taskQueue.AddTask("gsd_plan", taskDesc, 5)
				t.pico.taskQueue.SetTaskData(task.ID, "plan", plan)
				t.sendMessage(chatID, fmt.Sprintf("📝 GSD Plan for: %s\n\n%s", taskDesc, plan))
			}
		default:
			t.sendMessage(chatID, "Unknown /todo command. Use: add, list, run, plan")
		}
	case "/mazgaon":
		t.sendMessage(chatID, "🔍 Starting deep research for aaplemazgaon.quintoxsolutions.com...\n\nThis will analyze SEO, content, performance, UX, technical debt, and create a multi-page strategy.")

		go func() {
			t.sendMessage(chatID, "📊 Step 1/8: SEO Analysis...")
			results := t.pico.mazgaonRes.RunAllResearch()

			t.sendMessage(chatID, fmt.Sprintf("✅ Research Complete!\n\n%s", truncate(results, 4000)))

			fixes := t.pico.mazgaonRes.GetFixesList()
			if len(fixes) > 0 {
				fixMsg := "📋 TOP FIXES TO IMPLEMENT:\n\n"
				for i, fix := range fixes {
					if i >= 10 {
						break
					}
					fixMsg += fmt.Sprintf("%d. %s\n", i+1, fix)
				}
				t.sendMessage(chatID, fixMsg)
			}

			fullReport := t.pico.mazgaonRes.GetFullReport()
			if len(fullReport) > 4000 {
				t.sendMessage(chatID, "📄 Full report (continued):\n"+fullReport[:4000])
				t.sendMessage(chatID, fullReport[4000:])
			} else {
				t.sendMessage(chatID, fullReport)
			}
		}()
	default:
		t.sendMessage(chatID, "Unknown. /help for commands.")
	}
}

func (t *TelegramBot) runTaskWithAutoRetry(taskID string) string {
	ctx := context.Background()
	maxRetries := 5

	for i := 0; i < maxRetries; i++ {
		task, ok := t.pico.taskQueue.GetTask(taskID)
		if !ok {
			return "Task not found"
		}

		task.Status = "running"
		t.pico.taskQueue.saveTasks()

		plan := t.pico.gsdEngine.CreatePlan(task.Description)

		result, err := t.pico.gsdEngine.ExecutePlan(plan.TaskID, ctx)
		if err != nil {
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") {
				t.sendMessage(t.allowFrom[0], fmt.Sprintf("⏳ Rate limited, retrying in %d seconds...", (i+1)*10))
				time.Sleep(time.Duration(i+1) * 10 * time.Second)
				continue
			}
			task.Status = "failed"
			task.Error = err.Error()
			t.pico.taskQueue.saveTasks()
			return fmt.Sprintf("Task failed: %v", err)
		}

		task.Status = "completed"
		task.Result = result
		t.pico.taskQueue.saveTasks()
		return result
	}

	return "Task failed after max retries"
}

func (t *TelegramBot) createGSDPlan(taskDesc string) string {
	plan := t.pico.gsdEngine.CreatePlan(taskDesc)
	return t.pico.gsdEngine.formatResults(plan)
}

func (t *TelegramBot) sendMessage(chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	data := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	jsonData, _ := json.Marshal(data)
	t.client.Post(url, "application/json", strings.NewReader(string(jsonData)))
}

func (t *TelegramBot) sendDailyReport(report string) {
	for _, id := range t.allowFrom {
		t.sendMessage(id, "📊 Daily Learning Report:\n\n"+report)
	}
}

func (t *TelegramBot) sendWeeklyReport(report string) {
	for _, id := range t.allowFrom {
		t.sendMessage(id, "📈 Weekly Learning Report:\n\n"+report)
	}
}

type Scheduler struct {
	learning   *LearningEngine
	telegram   *TelegramBot
	lastDaily  time.Time
	lastWeekly time.Time
	mu         sync.Mutex
}

func NewScheduler(learning *LearningEngine, telegram *TelegramBot) *Scheduler {
	return &Scheduler{
		learning:   learning,
		telegram:   telegram,
		lastDaily:  time.Now(),
		lastWeekly: time.Now(),
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	dailyTicker := time.NewTicker(24 * time.Hour)
	weeklyTicker := time.NewTicker(7 * 24 * time.Hour)
	defer dailyTicker.Stop()
	defer weeklyTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-dailyTicker.C:
			s.runDailyLearning()
			if s.telegram != nil {
				report := s.learning.GetDailyReport()
				s.telegram.sendDailyReport(report)
			}
		case <-weeklyTicker.C:
			if s.telegram != nil {
				report := s.learning.GetWeeklyReport()
				s.telegram.sendWeeklyReport(report)
			}
		case <-time.After(time.Until(s.nextDailyRun())):
			s.runDailyLearning()
		}
	}
}

func (s *Scheduler) nextDailyRun() time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 6, 0, 0, 0, now.Location())
	return next
}

func (s *Scheduler) runDailyLearning() {
	if s.learning == nil {
		return
	}

	skills := []string{
		"api_development",
		"database_optimization",
		"container_orchestration",
		"monitoring_setup",
		"security_hardening",
	}

	for _, skill := range skills {
		s.learning.LearnSkill(context.Background(), skill)
		time.Sleep(5 * time.Second)
	}
}

func loadTasks() []map[string]interface{} {
	data, err := os.ReadFile("/root/.picoclaw/tasks.json")
	if err != nil {
		return []map[string]interface{}{}
	}
	var tasks []map[string]interface{}
	json.Unmarshal(data, &tasks)
	return tasks
}
