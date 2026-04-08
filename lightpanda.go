package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func toolLightpandaFetch(ctx context.Context, args map[string]interface{}) (string, error) {
	url, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url is required")
	}

	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	output, err := fetchWithLightpanda(ctx, url)
	if err != nil {
		output, err = fetchWithHeadlessChrome(ctx, url)
		if err != nil {
			return "", fmt.Errorf("all browsers failed: %v", err)
		}
	}

	return output, nil
}

func fetchWithLightpanda(ctx context.Context, url string) (string, error) {
	if _, err := exec.LookPath("lightpanda"); err != nil {
		return "", fmt.Errorf("lightpanda not installed: %v", err)
	}

	tmpFile := "/tmp/lightpanda_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".html"
	defer os.Remove(tmpFile)

	cmd := exec.CommandContext(ctx, "lightpanda", "screenshot", "--url", url, "--output", tmpFile)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func fetchWithHeadlessChrome(ctx context.Context, url string) (string, error) {
	chromePaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"google-chrome",
		"chromium",
	}

	var chromePath string
	for _, p := range chromePaths {
		if _, err := exec.LookPath(p); err == nil {
			chromePath = p
			break
		}
	}

	if chromePath == "" {
		return "", fmt.Errorf("no headless browser available")
	}

	cmd := exec.CommandContext(ctx, chromePath,
		"--headless",
		"--dump-dom",
		"--disable-gpu",
		"--no-sandbox",
		url,
	)

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

func isLightpandaAvailable() bool {
	_, err := exec.LookPath("lightpanda")
	return err == nil
}

func installLightpanda() error {
	cmd := exec.Command("sh", "-c", "curl -fsSL https://github.com/nicholaswmin/lightpanda/releases/latest/download/lightpanda-linux-x64.tar.gz | tar xz -C /usr/local/bin")
	return cmd.Run()
}

type LightpandaBrowser struct {
	Headless bool
	Timeout  time.Duration
}

func NewLightpandaBrowser() *LightpandaBrowser {
	return &LightpandaBrowser{
		Headless: true,
		Timeout:  30 * time.Second,
	}
}

func (b *LightpandaBrowser) Fetch(url string) (string, error) {
	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.Timeout)
	defer cancel()

	var lastErr error
	for _, method := range []func(context.Context, string) (string, error){
		fetchWithLightpanda,
		fetchWithHeadlessChrome,
	} {
		content, err := method(ctx, url)
		if err == nil {
			return content, nil
		}
		lastErr = err
	}

	return "", fmt.Errorf("all browser methods failed: %v", lastErr)
}

func (b *LightpandaBrowser) Screenshot(url, outputPath string) error {
	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lightpanda", "screenshot", "--url", url, "--output", outputPath)
	return cmd.Run()
}

func (b *LightpandaBrowser) Evaluate(url, script string) (string, error) {
	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}

	tmpFile := "/tmp/lp_script_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".js"
	defer os.Remove(tmpFile)

	if err := os.WriteFile(tmpFile, []byte(script), 0644); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lightpanda", "evaluate", "--url", url, "--script", tmpFile)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

func (b *LightpandaBrowser) Click(url, selector string) error {
	script := fmt.Sprintf(`document.querySelector('%s').click();`, selector)
	return b.evaluateScript(url, script)
}

func (b *LightpandaBrowser) FillForm(url, selector, value string) error {
	script := fmt.Sprintf(`document.querySelector('%s').value = '%s';`, selector, value)
	return b.evaluateScript(url, script)
}

func (b *LightpandaBrowser) evaluateScript(url, script string) error {
	_, err := b.Evaluate(url, script)
	return err
}

func (b *LightpandaBrowser) Scroll(url string, pixels int) error {
	script := fmt.Sprintf("window.scrollBy(0, %d);", pixels)
	return b.evaluateScript(url, script)
}

func (b *LightpandaBrowser) GetCookies(url string) ([]string, error) {
	script := `JSON.stringify(document.cookie)`
	output, err := b.Evaluate(url, script)
	if err != nil {
		return nil, err
	}
	return strings.Split(output, "; "), nil
}

func (b *LightpandaBrowser) SearchGoogle(query string) ([]SearchResult, error) {
	url := fmt.Sprintf("https://www.google.com/search?q=%s", strings.ReplaceAll(query, " ", "+"))
	content, err := b.Fetch(url)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "href=\"/url?q=") {
			start := strings.Index(line, "href=\"/url?q=") + 12
			end := strings.Index(line[start:], "\"")
			if end > 0 {
				results = append(results, SearchResult{URL: line[start : start+end]})
			}
		}
	}

	return results, nil
}

type SearchResult struct {
	Title string
	URL   string
}
