package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`   // pending, running, completed, failed
	Priority    int        `json:"priority"` // 1-5, 5 is highest
	CreatedAt   time.Time  `json:"created_at"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	Retries     int        `json:"retries"`
}

type TaskQueue struct {
	tasks     map[string]*Task
	mu        sync.RWMutex
	handlers  map[string]TaskHandler
	scheduler *TaskScheduler
}

type TaskHandler func(ctx context.Context, task *Task) (string, error)

type TaskScheduler struct {
	queue   *TaskQueue
	ticker  *time.Ticker
	stopCh  chan bool
	mu      sync.Mutex
	running bool
}

func NewTaskQueue() *TaskQueue {
	return &TaskQueue{
		tasks:    make(map[string]*Task),
		handlers: make(map[string]TaskHandler),
	}
}

func (q *TaskQueue) AddTask(title, desc string, priority int) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	task := &Task{
		ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Title:       title,
		Description: desc,
		Status:      "pending",
		Priority:    priority,
		CreatedAt:   time.Now(),
	}
	q.tasks[task.ID] = task
	q.saveTasks()
	return task
}

func (q *TaskQueue) AddScheduledTask(title, desc string, scheduledAt time.Time, priority int) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	task := &Task{
		ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Title:       title,
		Description: desc,
		Status:      "scheduled",
		Priority:    priority,
		CreatedAt:   time.Now(),
		ScheduledAt: &scheduledAt,
	}
	q.tasks[task.ID] = task
	q.saveTasks()
	return task
}

func (q *TaskQueue) GetTask(id string) (*Task, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	task, ok := q.tasks[id]
	return task, ok
}

func (q *TaskQueue) ListTasks() []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()

	tasks := make([]*Task, 0, len(q.tasks))
	for _, t := range q.tasks {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if t1, t2 := tasks[i].Priority, tasks[j].Priority; t1 != t2 {
			return t1 > t2
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	return tasks
}

func (q *TaskQueue) GetPendingTasks() []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var tasks []*Task
	for _, t := range q.tasks {
		if t.Status == "pending" || t.Status == "scheduled" {
			tasks = append(tasks, t)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if t1, t2 := tasks[i].Priority, tasks[j].Priority; t1 != t2 {
			return t1 > t2
		}
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	return tasks
}

func (q *TaskQueue) RunTask(ctx context.Context, id string) (string, error) {
	q.mu.Lock()
	task, ok := q.tasks[id]
	if !ok {
		q.mu.Unlock()
		return "", fmt.Errorf("task not found")
	}
	task.Status = "running"
	q.mu.Unlock()

	handler, ok := q.handlers[task.Title]
	if !ok {
		handler = q.defaultHandler
	}

	result, err := handler(ctx, task)
	task.Result = result
	if err != nil {
		task.Error = err.Error()
		task.Retries++
		if task.Retries < 3 {
			task.Status = "pending"
		} else {
			task.Status = "failed"
		}
	} else {
		task.Status = "completed"
		now := time.Now()
		task.CompletedAt = &now
	}

	q.saveTasks()
	return result, err
}

func (q *TaskQueue) RegisterHandler(name string, handler TaskHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[name] = handler
}

func (q *TaskQueue) defaultHandler(ctx context.Context, task *Task) (string, error) {
	llm := NewLLMClient("nvidia", "minimaxai/minimax-m2.5", "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")
	prompt := fmt.Sprintf("Complete task: %s\n\nDescription: %s", task.Title, task.Description)
	return llm.Complete(ctx, []Message{{Role: "user", Content: prompt}}, nil)
}

func (q *TaskQueue) saveTasks() {
	stateDir := "/root/.picoclaw/autonomous"
	os.MkdirAll(stateDir, 0755)
	data, _ := json.MarshalIndent(q.tasks, "", "  ")
	os.WriteFile(filepath.Join(stateDir, "tasks_queue.json"), data, 0644)
}

func (q *TaskQueue) loadTasks() {
	stateDir := "/root/.picoclaw/autonomous"
	data, err := os.ReadFile(filepath.Join(stateDir, "tasks_queue.json"))
	if err != nil {
		return
	}
	json.Unmarshal(data, &q.tasks)
}

func NewTaskScheduler(queue *TaskQueue) *TaskScheduler {
	return &TaskScheduler{
		queue:  queue,
		ticker: time.NewTicker(1 * time.Minute),
		stopCh: make(chan bool),
	}
}

func (s *TaskScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				s.stopCh <- true
				return
			case <-s.ticker.C:
				s.checkScheduledTasks(ctx)
			case <-s.stopCh:
				return
			}
		}
	}()
	fmt.Println("✓ Task Scheduler: Started")
}

func (s *TaskScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.ticker.Stop()
		s.stopCh <- true
		s.running = false
	}
}

func (s *TaskScheduler) checkScheduledTasks(ctx context.Context) {
	s.queue.mu.RLock()
	now := time.Now()
	for _, task := range s.queue.tasks {
		if task.Status == "scheduled" && task.ScheduledAt != nil {
			if now.After(*task.ScheduledAt) {
				task.Status = "pending"
				go s.queue.RunTask(ctx, task.ID)
			}
		}
	}
	s.queue.mu.RUnlock()
}

func (s *TaskScheduler) RunDailyTasks(ctx context.Context) {
	dailyTasks := []struct {
		title       string
		description string
		priority    int
	}{
		{"daily_learning", "Learn a new skill and track progress", 5},
		{"system_backup", "Backup important files", 4},
		{"code_review", "Review recent code changes", 3},
		{"update_docs", "Update documentation", 2},
	}

	for _, t := range dailyTasks {
		task := s.queue.AddTask(t.title, t.description, t.priority)
		go s.queue.RunTask(ctx, task.ID)
	}
}

type TaskTools struct {
	queue *TaskQueue
}

func NewTaskTools(queue *TaskQueue) *TaskTools {
	return &TaskTools{queue: queue}
}

func (t *TaskTools) HandleTaskCommand(args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	title, _ := args["title"].(string)
	desc, _ := args["description"].(string)
	id, _ := args["id"].(string)
	priority, _ := args["priority"].(int)

	if priority == 0 {
		priority = 3
	}

	switch action {
	case "add":
		task := t.queue.AddTask(title, desc, priority)
		return fmt.Sprintf("✅ Task added: %s (ID: %s)", task.Title, task.ID), nil
	case "list":
		tasks := t.queue.ListTasks()
		if len(tasks) == 0 {
			return "No tasks found", nil
		}
		var result string
		for _, task := range tasks {
			status := map[string]string{"pending": "⏳", "running": "🔄", "completed": "✅", "failed": "❌", "scheduled": "📅"}[task.Status]
			result += fmt.Sprintf("%s [%d] %s - %s\n", status, task.Priority, task.Title, task.ID)
		}
		return result, nil
	case "run":
		if id == "" {
			task := t.queue.GetPendingTasks()[0]
			if task == nil {
				return "No pending tasks", nil
			}
			id = task.ID
		}
		result, err := t.queue.RunTask(context.Background(), id)
		if err != nil {
			return fmt.Sprintf("❌ Error: %v", err), nil
		}
		return fmt.Sprintf("✅ Task completed: %s", result), nil
	case "schedule":
		hour, _ := args["hour"].(int)
		if hour == 0 {
			hour = 6
		}
		scheduledAt := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day()+1, hour, 0, 0, 0, time.UTC)
		task := t.queue.AddScheduledTask(title, desc, scheduledAt, priority)
		return fmt.Sprintf("📅 Task scheduled for %s (ID: %s)", scheduledAt.Format("2006-01-02 15:04"), task.ID), nil
	case "status":
		task, ok := t.queue.GetTask(id)
		if !ok {
			return "Task not found", nil
		}
		return fmt.Sprintf("📋 Task: %s\nStatus: %s\nPriority: %d\nCreated: %s\nResult: %s",
			task.Title, task.Status, task.Priority, task.CreatedAt.Format("2006-01-02 15:04"), task.Result), nil
	default:
		return "Usage: action (add|list|run|schedule|status)", nil
	}
}
