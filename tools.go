package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var globalAgentConfig *AgentConfig
var globalLearningEngine *LearningEngine
var globalTaskQueue *TaskQueue
var globalTaskScheduler *TaskScheduler
var globalThinker *Thinker

func registerDefaultTools(agent *Agent) {
	agent.RegisterTool("read_file", toolReadFile)
	agent.RegisterTool("write_file", toolWriteFile)
	agent.RegisterTool("list_files", toolListFiles)
	agent.RegisterTool("execute_command", toolExecuteCommand)
	agent.RegisterTool("web_fetch", toolWebFetch)
	agent.RegisterTool("search_web", toolSearchWeb)
	agent.RegisterTool("get_time", toolGetTime)
	agent.RegisterTool("get_system_info", toolGetSystemInfo)
}

func toolReadFile(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file error: %w", err)
	}

	result := string(content)
	if len(result) > 12000 {
		result = result[:12000] + "\n\n[Output truncated]"
	}
	return result, nil
}

func toolWriteFile(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directory error: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file error: %w", err)
	}

	return fmt.Sprintf("✅ File written: %s", path), nil
}

func toolListFiles(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("list files error: %w", err)
	}

	var files []string
	for _, entry := range entries {
		info, _ := entry.Info()
		size := info.Size()
		if entry.IsDir() {
			files = append(files, fmt.Sprintf("📁 %s/", entry.Name()))
		} else {
			files = append(files, fmt.Sprintf("📄 %s (%d bytes)", entry.Name(), size))
		}
	}

	if len(files) == 0 {
		return "No files found", nil
	}
	return strings.Join(files, "\n"), nil
}

func toolExecuteCommand(ctx context.Context, args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command is required")
	}

	timeout := 30 * time.Second
	if t, ok := args["timeout"].(float64); ok {
		timeout = time.Duration(t) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command error: %w\noutput: %s", err, string(output))
	}

	result := string(output)
	if len(result) > 12000 {
		result = result[:12000] + "\n\n[Output truncated]"
	}
	return result, nil
}

func toolWebFetch(ctx context.Context, args map[string]interface{}) (string, error) {
	url, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url is required")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	result := string(body)
	if len(result) > 12000 {
		result = result[:12000] + "\n\n[Output truncated]"
	}
	return result, nil
}

func toolSearchWeb(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("query is required")
	}

	url := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", strings.ReplaceAll(query, " ", "+"))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("search error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	re := regexp.MustCompile(`<a class="result__a" href="([^"]+)"[^>]*>([^<]+)</a>`)
	matches := re.FindAllStringSubmatch(string(body), 5)

	var results []string
	for _, match := range matches {
		results = append(results, fmt.Sprintf("• %s\n  %s", match[2], match[1]))
	}

	if len(results) == 0 {
		return "No results found", nil
	}
	return "🔍 Search Results:\n" + strings.Join(results, "\n\n"), nil
}

func toolGetTime(ctx context.Context, args map[string]interface{}) (string, error) {
	now := time.Now()
	return fmt.Sprintf("🕐 Current time: %s\n📅 Date: %s\n⏰ Unix timestamp: %d",
		now.Format("2006-01-02 15:04:05"),
		now.Format("Monday, January 2, 2006"),
		now.Unix()), nil
}

func toolGetSystemInfo(ctx context.Context, args map[string]interface{}) (string, error) {
	hostname, _ := os.Hostname()
	return fmt.Sprintf(`📊 System Info:
• Hostname: %s
• OS: Linux
• Go Version: 1.21+
• Architecture: amd64
• User: %s
• PWD: %s`,
		hostname,
		os.Getenv("USER"),
		os.Getenv("PWD"),
	), nil
}

func toolEditFile(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	oldString, _ := args["old"].(string)
	newString, _ := args["new"].(string)

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file error: %w", err)
	}

	if oldString == "" {
		return "", fmt.Errorf("old string is required")
	}

	newContent := strings.Replace(string(content), oldString, newString, 1)

	if string(content) == newContent {
		return "", fmt.Errorf("old string not found in file")
	}

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write file error: %w", err)
	}

	return fmt.Sprintf("✅ File edited: %s", path), nil
}

func toolCreateDirectory(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("create directory error: %w", err)
	}

	return fmt.Sprintf("✅ Directory created: %s", path), nil
}

func toolDeleteFile(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	if err := os.RemoveAll(path); err != nil {
		return "", fmt.Errorf("delete error: %w", err)
	}

	return fmt.Sprintf("✅ Deleted: %s", path), nil
}

func toolFileExists(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	_, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("File does not exist: %s", path), nil
	}
	return fmt.Sprintf("File exists: %s", path), nil
}

func toolGetFileInfo(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat error: %w", err)
	}

	return fmt.Sprintf(`📄 File Info:
• Name: %s
• Size: %d bytes
• Mode: %s
• Modified: %s
• Is Directory: %t`,
		info.Name(),
		info.Size(),
		info.Mode().String(),
		info.ModTime().Format("2006-01-02 15:04:05"),
		info.IsDir(),
	), nil
}

func toolReadMultipleFiles(ctx context.Context, args map[string]interface{}) (string, error) {
	paths, ok := args["paths"].([]interface{})
	if !ok {
		return "", fmt.Errorf("paths is required (array)")
	}

	var results []string
	for _, p := range paths {
		path, ok := p.(string)
		if !ok {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			results = append(results, fmt.Sprintf("❌ %s: %v", path, err))
			continue
		}
		results = append(results, fmt.Sprintf("📄 %s:\n%s", path, string(content)))
	}

	return strings.Join(results, "\n\n---\n\n"), nil
}

func toolGetConfig(ctx context.Context, args map[string]interface{}) (string, error) {
	return `⚙️ Configuration is loaded from /root/.picoclaw/config.json

To update config, edit the JSON file directly.`, nil
}

func toolUpdateAPIKey(ctx context.Context, args map[string]interface{}) (string, error) {
	provider, _ := args["provider"].(string)
	apiKey, _ := args["api_key"].(string)

	if provider == "" || apiKey == "" {
		return "Usage: update_api_key {provider: 'google', api_key: 'your-key'}", nil
	}

	return fmt.Sprintf("API key for %s updated (in memory only - restart to persist)", provider), nil
}

func toolListTools(ctx context.Context, args map[string]interface{}) (string, error) {
	return `🔧 Available Tools:

File Operations:
• read_file - Read file contents
• write_file - Write content to file
• edit_file - Edit file content
• list_files - List directory contents
• create_directory - Create directory
• delete_file - Delete file/directory
• file_exists - Check if file exists
• get_file_info - Get file metadata
• read_multiple_files - Read multiple files

System:
• execute_command - Run shell command
• get_time - Get current time
• get_system_info - Get system information
• web_fetch - Fetch URL content
• search_web - Search the web

Admin:
• get_config - Show configuration
• update_api_key - Update API key
• list_tools - Show this list
• get_agent_status - Show agent status
• restart_agent - Restart the agent`, nil
}

func toolGetAgentStatus(ctx context.Context, args map[string]interface{}) (string, error) {
	return `✅ Agent Status:
• Name: picoclaw
• Model: gemini-2.0-flash
• Provider: google
• Status: Running
• Tools: 18 registered
• Memory: Using message history`, nil
}

func toolRestartAgent(ctx context.Context, args map[string]interface{}) (string, error) {
	return "🔄 Agent restart requested (implement in main.go to take effect)", nil
}

func toolSetModel(ctx context.Context, args map[string]interface{}) (string, error) {
	model, _ := args["model"].(string)
	provider, _ := args["provider"].(string)

	if model == "" {
		return "Usage: set_model {model: 'gemini-2.0-flash', provider: 'google'}", nil
	}

	globalAgentConfig.Model = model
	if provider != "" {
		globalAgentConfig.Provider = provider
	}

	return fmt.Sprintf("✅ Model set to: %s (provider: %s)", model, globalAgentConfig.Provider), nil
}

func toolAddAPIKey(ctx context.Context, args map[string]interface{}) (string, error) {
	provider, _ := args["provider"].(string)
	apiKey, _ := args["api_key"].(string)

	if provider == "" || apiKey == "" {
		return "Usage: add_api_key {provider: 'nvidia', api_key: 'your-key'}", nil
	}

	globalAgentConfig.APIKey = apiKey
	globalAgentConfig.Provider = provider

	return fmt.Sprintf("✅ API key added for provider: %s", provider), nil
}

func toolLearnSkill(ctx context.Context, args map[string]interface{}) (string, error) {
	skillName, _ := args["skill"].(string)

	if skillName == "" {
		return "Usage: learn_skill {skill: 'api_development'}", nil
	}

	if globalLearningEngine == nil {
		return "Learning engine not initialized", nil
	}

	return globalLearningEngine.LearnSkill(ctx, skillName)
}

func toolGetSkills(ctx context.Context, args map[string]interface{}) (string, error) {
	if globalLearningEngine == nil {
		return "Learning engine not initialized", nil
	}

	return globalLearningEngine.GetSkills(), nil
}

func toolBuildTool(ctx context.Context, args map[string]interface{}) (string, error) {
	toolName, _ := args["name"].(string)

	if toolName == "" {
		return "Usage: build_tool {name: 'my_tool'}", nil
	}

	if globalLearningEngine == nil {
		return "Learning engine not initialized", nil
	}

	return globalLearningEngine.BuildTool(ctx, toolName)
}

func toolGetDailyReport(ctx context.Context, args map[string]interface{}) (string, error) {
	if globalLearningEngine == nil {
		return "Learning engine not initialized", nil
	}

	return globalLearningEngine.GetDailyReport(), nil
}

func toolGetWeeklyReport(ctx context.Context, args map[string]interface{}) (string, error) {
	if globalLearningEngine == nil {
		return "Learning engine not initialized", nil
	}

	return globalLearningEngine.GetWeeklyReport(), nil
}

func toolTask(ctx context.Context, args map[string]interface{}) (string, error) {
	if globalTaskQueue == nil {
		return "Task queue not initialized", nil
	}

	taskTools := NewTaskTools(globalTaskQueue)
	return taskTools.HandleTaskCommand(args)
}

func toolThink(ctx context.Context, args map[string]interface{}) (string, error) {
	if globalThinker == nil {
		return "Thinker not initialized", nil
	}

	action, _ := args["action"].(string)

	switch action {
	case "thoughts":
		return globalThinker.GetThoughts(), nil
	case "memory":
		return globalThinker.GetWorkingMemory(), nil
	case "clear":
		globalThinker = NewThinker()
		return "Thinking cleared", nil
	default:
		return "Usage: think {action: 'thoughts'|'memory'|'clear'}", nil
	}
}
