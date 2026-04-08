package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type AutoFix struct {
	pico *Picoclaw
	mu   sync.RWMutex
}

func NewAutoFix(pico *Picoclaw) *AutoFix {
	return &AutoFix{pico: pico}
}

func (af *AutoFix) ExecuteFix(fixType string) (string, error) {
	switch fixType {
	case "process_restart":
		return af.restartProcess()
	case "service_restart":
		return af.restartService("")
	case "clear_cache":
		return af.clearCache()
	case "free_memory":
		return af.freeMemory()
	case "fix_permissions":
		return af.fixPermissions()
	case "restart_docker":
		return af.restartDocker()
	case "reboot":
		return af.rebootSystem()
	case "kill_zombie":
		return af.killZombies()
	case "disk_cleanup":
		return af.diskCleanup()
	case "fix_network":
		return af.fixNetwork()
	default:
		return "", fmt.Errorf("unknown fix type: %s", fixType)
	}
}

func (af *AutoFix) restartProcess() (string, error) {
	cmd := exec.Command("pkill", "-f", "picoclaw")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to stop picoclaw: %v", err)
	}

	go func() {
		exec.Command("nohup", "./picoclaw", "&").Run()
	}()

	return "Picoclaw process restarted", nil
}

func (af *AutoFix) restartService(serviceName string) (string, error) {
	if serviceName == "" {
		serviceName = "cron"
	}
	cmd := exec.Command("systemctl", "restart", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to restart %s: %v, output: %s", serviceName, err, output)
	}
	return fmt.Sprintf("Service %s restarted", serviceName), nil
}

func (af *AutoFix) clearCache() (string, error) {
	cmds := [][]string{
		{"sync"},
		{"sh", "-c", "echo 3 > /proc/sys/vm/drop_caches"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("cache clear failed: %v", err)
		}
	}
	return "Cache cleared successfully", nil
}

func (af *AutoFix) freeMemory() (string, error) {
	cmd := exec.Command("sh", "-c", "sync && echo 3 > /proc/sys/vm/drop_caches && swapoff -a && swapon -a")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("memory free failed: %v, output: %s", err, output)
	}
	return "Memory freed successfully", nil
}

func (af *AutoFix) fixPermissions() (string, error) {
	cmd := exec.Command("sh", "-c", "chmod -R 755 /root/picoclaw_python && chmod +x /root/picoclaw_python/picoclaw")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("permission fix failed: %v", err)
	}
	return "Permissions fixed", nil
}

func (af *AutoFix) restartDocker() (string, error) {
	cmd := exec.Command("systemctl", "restart", "docker")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker restart failed: %v, output: %s", err, output)
	}
	return "Docker restarted", nil
}

func (af *AutoFix) rebootSystem() (string, error) {
	cmd := exec.Command("reboot")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("reboot failed: %v", err)
	}
	return "System rebooting", nil
}

func (af *AutoFix) killZombies() (string, error) {
	cmd := exec.Command("ps", "-eo", "pid,stat", "--no-headers")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list processes: %v", err)
	}

	var zombies int
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, " Z ") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				cmd := exec.Command("kill", "-9", parts[0])
				cmd.Run()
				zombies++
			}
		}
	}

	return fmt.Sprintf("Killed %d zombie processes", zombies), nil
}

func (af *AutoFix) diskCleanup() (string, error) {
	cleanups := [][]string{
		{"apt-get", "clean"},
		{"journalctl", "--vacuum-time=7d"},
		{"docker", "system", "prune", "-f"},
		{"sh", "-c", "rm -rf /tmp/*"},
	}

	var cleaned int
	for _, args := range cleanups {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Run()
		cleaned++
	}

	return fmt.Sprintf("Cleaned up %d areas", cleaned), nil
}

func (af *AutoFix) fixNetwork() (string, error) {
	cmds := [][]string{
		{"ip", "link", "set", "eth0", "down"},
		{"ip", "link", "set", "eth0", "up"},
		{"dhclient", "eth0"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if err := cmd.Run(); err != nil {
			continue
		}
	}

	return "Network reset attempted", nil
}

func (af *AutoFix) DiagnoseAndFix(ctx context.Context) string {
	if af.pico.monitor == nil {
		return "Server monitor not initialized"
	}

	services := af.pico.monitor.GetServicesStatus()
	var fixes []string

	for _, svc := range services {
		if !svc.Running {
			switch svc.Name {
			case "picoclaw":
				if result, err := af.restartProcess(); err == nil {
					fixes = append(fixes, result)
				}
			case "docker":
				if result, err := af.restartDocker(); err == nil {
					fixes = append(fixes, result)
				}
			default:
				if result, err := af.restartService(svc.Name); err == nil {
					fixes = append(fixes, result)
				}
			}
		}
	}

	if len(fixes) == 0 {
		return "No fixes needed - all services running"
	}

	var msg strings.Builder
	msg.WriteString("🔧 Auto-Fix Results:\n\n")
	for _, fix := range fixes {
		msg.WriteString("✅ " + fix + "\n")
	}

	if af.pico.telegram != nil && len(af.pico.telegram.allowFrom) > 0 {
		af.pico.telegram.sendMessage(af.pico.telegram.allowFrom[0], msg.String())
	}

	return msg.String()
}
