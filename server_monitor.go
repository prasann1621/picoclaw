package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type ServerMonitor struct {
	pico           *Picoclaw
	checks         map[string]*HealthCheck
	lastResults    map[string]*CheckResult
	mu             sync.RWMutex
	client         *http.Client
	notifyTelegram func(string)
}

type HealthCheck struct {
	Name        string
	CheckFunc   func() *CheckResult
	Interval    time.Duration
	Enabled     bool
	AlertOnFail bool
}

type CheckResult struct {
	Name      string
	Status    string
	Message   string
	Timestamp time.Time
	Error     error
}

type ServiceStatus struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Status  string `json:"status"`
	Uptime  string `json:"uptime"`
	Message string `json:"message"`
}

func NewServerMonitor(pico *Picoclaw) *ServerMonitor {
	return &ServerMonitor{
		pico:        pico,
		checks:      make(map[string]*HealthCheck),
		lastResults: make(map[string]*CheckResult),
		client:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *ServerMonitor) Start(ctx context.Context) {
	m.registerDefaultChecks()

	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.runAllChecks(ctx)
			}
		}
	}()

	m.runAllChecks(ctx)
	fmt.Println("✓ Server Monitor: Active")
}

func (m *ServerMonitor) registerDefaultChecks() {
	m.checks["cpu"] = &HealthCheck{
		Name:        "CPU Usage",
		CheckFunc:   m.checkCPU,
		Interval:    30 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["memory"] = &HealthCheck{
		Name:        "Memory",
		CheckFunc:   m.checkMemory,
		Interval:    30 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["disk"] = &HealthCheck{
		Name:        "Disk Space",
		CheckFunc:   m.checkDisk,
		Interval:    60 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["processes"] = &HealthCheck{
		Name:        "Critical Processes",
		CheckFunc:   m.checkProcesses,
		Interval:    30 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["network"] = &HealthCheck{
		Name:        "Network Connectivity",
		CheckFunc:   m.checkNetwork,
		Interval:    60 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["docker"] = &HealthCheck{
		Name:        "Docker Services",
		CheckFunc:   m.checkDocker,
		Interval:    60 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
	m.checks["services"] = &HealthCheck{
		Name:        "System Services",
		CheckFunc:   m.checkServices,
		Interval:    60 * time.Second,
		Enabled:     true,
		AlertOnFail: true,
	}
}

func (m *ServerMonitor) checkCPU() *CheckResult {
	cmd := exec.Command("sh", "-c", "top -bn1 | head -5 | tail -1 | awk '{print $2}'")
	output, err := cmd.Output()
	if err != nil {
		return &CheckResult{Name: "CPU", Status: "error", Message: err.Error(), Timestamp: time.Now()}
	}
	cpu := strings.TrimSpace(string(output))
	return &CheckResult{Name: "CPU", Status: "ok", Message: "CPU: " + cpu + "%", Timestamp: time.Now()}
}

func (m *ServerMonitor) checkMemory() *CheckResult {
	cmd := exec.Command("sh", "-c", "free -m | head -2 | tail -1 | awk '{print $3\" / \"$2\" MB used\"}'")
	output, err := cmd.Output()
	if err != nil {
		return &CheckResult{Name: "Memory", Status: "error", Message: err.Error(), Timestamp: time.Now()}
	}
	mem := strings.TrimSpace(string(output))
	return &CheckResult{Name: "Memory", Status: "ok", Message: "RAM: " + mem, Timestamp: time.Now()}
}

func (m *ServerMonitor) checkDisk() *CheckResult {
	cmd := exec.Command("sh", "-c", "df -h / | tail -1 | awk '{print $5\" used on \"$1}'")
	output, err := cmd.Output()
	if err != nil {
		return &CheckResult{Name: "Disk", Status: "error", Message: err.Error(), Timestamp: time.Now()}
	}
	disk := strings.TrimSpace(string(output))
	return &CheckResult{Name: "Disk", Status: "ok", Message: "Disk: " + disk, Timestamp: time.Now()}
}

func (m *ServerMonitor) checkProcesses() *CheckResult {
	criticalProcs := []string{"picoclaw", "docker"}
	var failed []string

	for _, proc := range criticalProcs {
		cmd := exec.Command("pgrep", "-f", proc)
		if err := cmd.Run(); err != nil {
			failed = append(failed, proc)
		}
	}

	if len(failed) > 0 {
		return &CheckResult{Name: "Processes", Status: "warning", Message: "Missing: " + strings.Join(failed, ", "), Timestamp: time.Now()}
	}
	return &CheckResult{Name: "Processes", Status: "ok", Message: "All critical processes running", Timestamp: time.Now()}
}

func (m *ServerMonitor) checkNetwork() *CheckResult {
	hosts := []string{"8.8.8.8", "github.com"}
	for _, host := range hosts {
		cmd := exec.Command("ping", "-c", "1", "-W", "2", host)
		if err := cmd.Run(); err == nil {
			return &CheckResult{Name: "Network", Status: "ok", Message: "Connected (ping to " + host + ")", Timestamp: time.Now()}
		}
	}
	return &CheckResult{Name: "Network", Status: "warning", Message: "Limited connectivity", Timestamp: time.Now()}
}

func (m *ServerMonitor) checkDocker() *CheckResult {
	cmd := exec.Command("docker", "ps")
	if err := cmd.Run(); err != nil {
		return &CheckResult{Name: "Docker", Status: "warning", Message: "Docker not accessible", Timestamp: time.Now()}
	}

	cmd = exec.Command("sh", "-c", "docker ps --format '{{.Names}}' | wc -l")
	output, _ := cmd.Output()
	count := strings.TrimSpace(string(output))

	return &CheckResult{Name: "Docker", Status: "ok", Message: "Running containers: " + count, Timestamp: time.Now()}
}

func (m *ServerMonitor) checkServices() *CheckResult {
	services := []string{"cron", "systemd-journald"}
	var failed []string

	for _, svc := range services {
		cmd := exec.Command("pgrep", "-x", svc)
		if err := cmd.Run(); err != nil {
			failed = append(failed, svc)
		}
	}

	if len(failed) > 0 {
		return &CheckResult{Name: "Services", Status: "warning", Message: "Issues with: " + strings.Join(failed, ", "), Timestamp: time.Now()}
	}
	return &CheckResult{Name: "Services", Status: "ok", Message: "All critical services running", Timestamp: time.Now()}
}

func (m *ServerMonitor) runAllChecks(ctx context.Context) {
	for name, check := range m.checks {
		if !check.Enabled {
			continue
		}
		result := check.CheckFunc()
		m.mu.Lock()
		m.lastResults[name] = result
		m.mu.Unlock()

		if check.AlertOnFail && result.Status != "ok" && m.pico.telegram != nil {
			m.alertIssue(result)
		}
	}
}

func (m *ServerMonitor) alertIssue(result *CheckResult) {
	alertMsg := fmt.Sprintf("🚨 Server Alert\n\n%s: %s\nTime: %s",
		result.Name, result.Message, result.Timestamp.Format("15:04:05"))

	if m.pico.telegram != nil {
		for _, id := range m.pico.telegram.allowFrom {
			m.pico.telegram.sendMessage(id, alertMsg)
		}
	}
}

func (m *ServerMonitor) GetStatus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var status strings.Builder
	status.WriteString("🖥️ Server Health Status\n\n")

	for name, result := range m.lastResults {
		icon := "✅"
		if result.Status == "warning" {
			icon = "⚠️"
		} else if result.Status == "error" {
			icon = "❌"
		}
		status.WriteString(fmt.Sprintf("%s %s: %s\n", icon, name, result.Message))
	}

	return status.String()
}

func (m *ServerMonitor) GetServicesStatus() []ServiceStatus {
	var services []ServiceStatus

	serviceChecks := []struct {
		name  string
		check func() bool
	}{
		{"picoclaw", func() bool { return exec.Command("pgrep", "-f", "picoclaw").Run() == nil }},
		{"docker", func() bool { return exec.Command("docker", "ps").Run() == nil }},
		{"nginx", func() bool { return exec.Command("pgrep", "-x", "nginx").Run() == nil }},
		{"mysql", func() bool { return exec.Command("pgrep", "-x", "mysqld").Run() == nil }},
		{"postgresql", func() bool { return exec.Command("pgrep", "-x", "postgres").Run() == nil }},
		{"redis", func() bool { return exec.Command("pgrep", "-x", "redis").Run() == nil }},
	}

	for _, s := range serviceChecks {
		status := ServiceStatus{Name: s.name, Running: s.check()}
		if status.Running {
			status.Status = "running"
			status.Message = "OK"
		} else {
			status.Status = "stopped"
			status.Message = "Not running"
		}
		services = append(services, status)
	}

	return services
}
