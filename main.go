package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

const version = "2.0.0"

type Picoclaw struct {
	agent         *Agent
	config        *Config
	telegram      *TelegramBot
	tools         *ToolRegistry
	ctx           context.Context
	cancel        context.CancelFunc
	running       bool
	mu            sync.RWMutex
	learning      *LearningEngine
	scheduler     *Scheduler
	taskQueue     *TaskQueue
	taskSched     *TaskScheduler
	gsdEngine     *GSDEngine
	mazgaonRes    *MazgaonResearch
	monitor       *ServerMonitor
	autoFix       *AutoFix
	bucketQueue   *BucketQueue
	weeklyBuilder *WeeklyToolBuilder
}

type Config struct {
	Model        string         `json:"model_name"`
	Provider     string         `json:"provider"`
	APIKey       string         `json:"api_key"`
	Instructions string         `json:"instructions"`
	Temperature  float64        `json:"temperature"`
	MaxTokens    int            `json:"max_tokens"`
	Telegram     TelegramConfig `json:"telegram"`
	Learning     LearningConfig `json:"learning"`
}

type TelegramConfig struct {
	Enabled   bool    `json:"enabled"`
	Token     string  `json:"token"`
	AllowFrom []int64 `json:"allow_from"`
}

type LearningConfig struct {
	Enabled     bool   `json:"enabled"`
	SkillDir    string `json:"skill_dir"`
	StateDir    string `json:"state_dir"`
	GitHubToken string `json:"github_token"`
	GitHubRepo  string `json:"github_repo"`
}

func main() {
	fmt.Println(`
██╗      ██████╗ ██████╗ ██████╗ ██╗   ██╗███████╗███████╗██╗     ███████╗██╗  ██╗
██║     ██╔═══██╗██╔══██╗██╔══██╗██║   ██║██╔════╝██╔════╝██║     ██╔════╝╚██╗██╔╝
██║     ██║   ██║██████╔╝██║  ██║██║   ██║█████╗  ███████╗██║     █████╗   ╚███╔╝ 
██║     ██║   ██║██╔══██╗██║  ██║██║   ██║██╔══╝  ╚════██║██║     ██╔══╝   ██╔██╗ 
██████╗╚██████╔╝██║  ██║██████╔╝╚██████╔╝███████╗███████║███████╗███████╗██╔╝ ██╗
╚══════╝ ╚═════╝ ╚═╝  ╚═╝╚═════╝  ╚═════╝ ╚══════╝╚══════╝╚══════╝╚══════╝╚═╝  ╚═╝
 Picoclaw v2.0.0 - AutoAgent-Inspired Implementation with Learning System
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	cfg := loadConfig()

	globalAgentConfig = &AgentConfig{
		Name:         "picoclaw",
		Model:        cfg.Model,
		Provider:     cfg.Provider,
		APIKey:       cfg.APIKey,
		Instructions: cfg.Instructions,
		Temperature:  cfg.Temperature,
		MaxTokens:    cfg.MaxTokens,
	}

	agent := NewAgent(globalAgentConfig)

	pico := &Picoclaw{
		agent:  agent,
		config: cfg,
		tools:  NewToolRegistry(),
		ctx:    ctx,
		cancel: cancel,
	}

	registerAllTools(pico)

	pico.mu.Lock()
	pico.running = true
	pico.mu.Unlock()

	fmt.Printf("✓ Agent: %s\n", agent.config.Name)
	fmt.Printf("✓ Model: %s (%s)\n", agent.config.Model, agent.config.Provider)
	fmt.Printf("✓ Tools: %d registered\n", len(agent.tools))

	if cfg.Telegram.Enabled && cfg.Telegram.Token != "" {
		pico.telegram = NewTelegramBot(cfg.Telegram.Token, cfg.Telegram.AllowFrom, pico)
		go pico.telegram.Start(ctx)
		fmt.Println("✓ Telegram: Connected")
	}

	if cfg.Learning.Enabled {
		pico.learning = NewLearningEngine(cfg.Learning.SkillDir, cfg.Learning.StateDir, cfg.Learning.GitHubToken, cfg.Learning.GitHubRepo)
		globalLearningEngine = pico.learning
		pico.scheduler = NewScheduler(pico.learning, pico.telegram)
		go pico.scheduler.Start(ctx)
		fmt.Println("✓ Learning Engine: Active")
	}

	pico.taskQueue = NewTaskQueue()
	pico.taskQueue.loadTasks()
	pico.taskSched = NewTaskScheduler(pico.taskQueue)
	go pico.taskSched.Start(ctx)
	fmt.Println("✓ Task Queue: Active")

	pico.gsdEngine = NewGSDEngine(pico.taskQueue)
	fmt.Println("✓ GSD Engine: Active")

	pico.mazgaonRes = NewMazgaonResearch(pico.gsdEngine)
	globalMazgaonResearch = pico.mazgaonRes
	fmt.Println("✓ Mazgaon Research: Ready")

	pico.monitor = NewServerMonitor(pico)
	go pico.monitor.Start(ctx)

	pico.autoFix = NewAutoFix(pico)

	pico.bucketQueue = NewBucketQueue(pico)
	pico.bucketQueue.StartWorkers(ctx, 3)

	if cfg.Learning.GitHubToken != "" {
		pico.weeklyBuilder = NewWeeklyToolBuilder(pico, cfg.Learning.GitHubToken, cfg.Learning.GitHubRepo)
		go pico.weeklyBuilder.Start(ctx)
	}

	fmt.Println("✓ Picoclaw ready! Press Ctrl+C to stop")

	<-ctx.Done()
}

func loadConfig() *Config {
	data, err := os.ReadFile("/root/.picoclaw/config.json")
	if err != nil {
		fmt.Println("Warning: Using default config")
		return defaultConfig()
	}

	var raw struct {
		Agents struct {
			Defaults Config `json:"defaults"`
		} `json:"agents"`
		Telegram struct {
			Enabled   bool    `json:"enabled"`
			Token     string  `json:"token"`
			AllowFrom []int64 `json:"allow_from"`
		} `json:"telegram"`
		Learning struct {
			Enabled     bool   `json:"enabled"`
			SkillDir    string `json:"skill_dir"`
			StateDir    string `json:"state_dir"`
			GitHubToken string `json:"github_token"`
			GitHubRepo  string `json:"github_repo"`
		} `json:"learning"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Printf("Error parsing config: %v\n", err)
		return defaultConfig()
	}

	cfg := &raw.Agents.Defaults
	cfg.Telegram = raw.Telegram
	cfg.Learning = raw.Learning
	return cfg
}

func defaultConfig() *Config {
	return &Config{
		Model:       "gemini-2.0-flash",
		Provider:    "google",
		Temperature: 0.7,
		MaxTokens:   4096,
		Learning: LearningConfig{
			Enabled:  true,
			SkillDir: "/root/.picoclaw/workspace/skills",
			StateDir: "/root/.picoclaw/autonomous",
		},
	}
}

func registerAllTools(pico *Picoclaw) {
	pico.agent.RegisterTool("read_file", toolReadFile)
	pico.agent.RegisterTool("write_file", toolWriteFile)
	pico.agent.RegisterTool("list_files", toolListFiles)
	pico.agent.RegisterTool("execute_command", toolExecuteCommand)
	pico.agent.RegisterTool("web_fetch", toolWebFetch)
	pico.agent.RegisterTool("search_web", toolSearchWeb)
	pico.agent.RegisterTool("get_time", toolGetTime)
	pico.agent.RegisterTool("get_system_info", toolGetSystemInfo)
	pico.agent.RegisterTool("edit_file", toolEditFile)
	pico.agent.RegisterTool("create_directory", toolCreateDirectory)
	pico.agent.RegisterTool("delete_file", toolDeleteFile)
	pico.agent.RegisterTool("file_exists", toolFileExists)
	pico.agent.RegisterTool("get_file_info", toolGetFileInfo)
	pico.agent.RegisterTool("read_multiple_files", toolReadMultipleFiles)
	pico.agent.RegisterTool("get_config", toolGetConfig)
	pico.agent.RegisterTool("update_api_key", toolUpdateAPIKey)
	pico.agent.RegisterTool("list_tools", toolListTools)
	pico.agent.RegisterTool("get_agent_status", toolGetAgentStatus)
	pico.agent.RegisterTool("restart_agent", toolRestartAgent)
	pico.agent.RegisterTool("set_model", toolSetModel)
	pico.agent.RegisterTool("add_api_key", toolAddAPIKey)
	pico.agent.RegisterTool("learn_skill", toolLearnSkill)
	pico.agent.RegisterTool("get_skills", toolGetSkills)
	pico.agent.RegisterTool("build_tool", toolBuildTool)
	pico.agent.RegisterTool("get_daily_report", toolGetDailyReport)
	pico.agent.RegisterTool("get_weekly_report", toolGetWeeklyReport)
	pico.agent.RegisterTool("task", toolTask)
	pico.agent.RegisterTool("think", toolThink)
	pico.agent.RegisterTool("mazgaon", toolMazgaonResearch)

	globalThinker = pico.agent.thinker

	pico.tools.Register("read_file", nil)
	pico.tools.Register("write_file", nil)
	pico.tools.Register("list_files", nil)
	pico.tools.Register("execute_command", nil)
	pico.tools.Register("web_fetch", nil)
	pico.tools.Register("search_web", nil)
	pico.tools.Register("get_time", nil)
	pico.tools.Register("get_system_info", nil)
	pico.tools.Register("edit_file", nil)
	pico.tools.Register("create_directory", nil)
	pico.tools.Register("delete_file", nil)
	pico.tools.Register("file_exists", nil)
	pico.tools.Register("get_file_info", nil)
	pico.tools.Register("read_multiple_files", nil)
	pico.tools.Register("get_config", nil)
	pico.tools.Register("update_api_key", nil)
	pico.tools.Register("list_tools", nil)
	pico.tools.Register("get_agent_status", nil)
	pico.tools.Register("restart_agent", nil)
	pico.tools.Register("set_model", nil)
	pico.tools.Register("add_api_key", nil)
	pico.tools.Register("learn_skill", nil)
	pico.tools.Register("get_skills", nil)
	pico.tools.Register("build_tool", nil)
	pico.tools.Register("get_daily_report", nil)
	pico.tools.Register("get_weekly_report", nil)
}
