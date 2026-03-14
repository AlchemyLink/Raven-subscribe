package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	adminToken      = "e2e-admin-token"
	defaultXrayImage = "ghcr.io/xtls/xray-core:latest"
	xrayInboundPort  = 18443
)

func TestDockerE2ESubscriptionFlow(t *testing.T) {
	if os.Getenv("E2E_DOCKER") != "1" {
		t.Skip("set E2E_DOCKER=1 to run docker end-to-end tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	repoRoot := mustRepoRoot(t)
	configDir := filepath.Join(repoRoot, "testdata", "xray", "config.d")
	if _, err := os.Stat(configDir); err != nil {
		t.Fatalf("missing test xray config directory: %v", err)
	}

	tmpDir := t.TempDir()
	dbDir := filepath.Join(tmpDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("create db dir: %v", err)
	}
	appBinPath := filepath.Join(tmpDir, "xray-subscription")
	buildCmd := fmt.Sprintf("CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o %q .", appBinPath)
	if out, err := runCmd(ctx, repoRoot, "sh", "-c", buildCmd); err != nil {
		t.Fatalf("build app binary failed: %v\n%s", err, out)
	}

	appConfigPath := filepath.Join(tmpDir, "config.json")
	hostPort := reservePort(t)
	appConfig := fmt.Sprintf(`{
  "listen_addr": ":8080",
  "server_host": "127.0.0.1",
  "config_dir": "/etc/xray/config.d",
  "db_path": "/var/lib/xray-subscription/db.sqlite",
  "sync_interval_seconds": 60,
  "base_url": "http://127.0.0.1:%d",
  "admin_token": "%s",
  "balancer_strategy": "leastPing",
  "balancer_probe_url": "https://www.gstatic.com/generate_204",
  "balancer_probe_interval": "30s"
}
`, hostPort, adminToken)
	if err := os.WriteFile(appConfigPath, []byte(appConfig), 0o644); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	xrayImage := strings.TrimSpace(os.Getenv("XRAY_IMAGE"))
	if xrayImage == "" {
		xrayImage = defaultXrayImage
	}

	if out, err := runCmd(ctx, repoRoot, "docker", "pull", xrayImage); err != nil {
		t.Fatalf("pull xray image failed: %v\n%s", err, out)
	}

	xrayContainerName := fmt.Sprintf("xray-e2e-%d", time.Now().UnixNano())
	appContainerName := fmt.Sprintf("raven-subscribe-e2e-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = runCmd(cleanupCtx, repoRoot, "docker", "rm", "-f", appContainerName)
		_, _ = runCmd(cleanupCtx, repoRoot, "docker", "rm", "-f", xrayContainerName)
	})

	if out, err := runCmd(
		ctx,
		repoRoot,
		"docker", "run", "-d", "--name", xrayContainerName,
		"-p", fmt.Sprintf("%d:%d", xrayInboundPort, xrayInboundPort),
		"-v", fmt.Sprintf("%s:/etc/xray/config.d:ro", configDir),
		xrayImage,
		"run", "-confdir", "/etc/xray/config.d",
	); err != nil {
		t.Fatalf("start xray container failed: %v\n%s", err, out)
	}

	if err := waitForContainerRunning(ctx, repoRoot, xrayContainerName, 30*time.Second); err != nil {
		t.Fatalf("xray container did not become running: %v", err)
	}
	if err := waitForTCP(fmt.Sprintf("127.0.0.1:%d", xrayInboundPort), 30*time.Second); err != nil {
		t.Fatalf("xray TCP port not ready: %v", err)
	}

	if out, err := runCmd(
		ctx,
		repoRoot,
		"docker", "run", "-d", "--name", appContainerName,
		"-p", fmt.Sprintf("%d:8080", hostPort),
		"-v", fmt.Sprintf("%s:/etc/xray/config.d:ro", configDir),
		"-v", fmt.Sprintf("%s:/etc/xray-subscription/config.json:ro", appConfigPath),
		"-v", fmt.Sprintf("%s:/var/lib/xray-subscription", dbDir),
		"-v", fmt.Sprintf("%s:/usr/local/bin/xray-subscription:ro", appBinPath),
		"alpine:3.20",
		"/usr/local/bin/xray-subscription",
		"-config", "/etc/xray-subscription/config.json",
	); err != nil {
		t.Fatalf("start app container failed: %v\n%s", err, out)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	waitForHealth(t, baseURL+"/health", 60*time.Second)

	usersResp := struct {
		Users []struct {
			User struct {
				Username string `json:"username"`
				Token    string `json:"token"`
			} `json:"user"`
			SubURL string `json:"sub_url"`
		}
	}{}
	body := doJSONRequest(t, "GET", baseURL+"/api/users", adminToken)
	if err := json.Unmarshal(body, &usersResp.Users); err != nil {
		t.Fatalf("decode users response: %v; body=%s", err, string(body))
	}
	if len(usersResp.Users) < 2 {
		t.Fatalf("expected at least two users from xray config, got %d", len(usersResp.Users))
	}

	containsUser := func(name string) bool {
		for _, u := range usersResp.Users {
			if u.User.Username == name {
				return true
			}
		}
		return false
	}
	if !containsUser("alice@example.com") || !containsUser("bob@example.com") {
		t.Fatalf("expected users alice@example.com and bob@example.com, got %+v", usersResp.Users)
	}

	var aliceToken string
	for _, u := range usersResp.Users {
		if u.User.Username == "alice@example.com" {
			aliceToken = u.User.Token
			break
		}
	}
	if strings.TrimSpace(aliceToken) == "" {
		t.Fatal("alice token not found in /api/users response")
	}

	linksTxtURL := fmt.Sprintf("%s/sub/%s/links.txt", baseURL, aliceToken)
	linksTxt := string(doRawRequest(t, "GET", linksTxtURL, ""))
	if !strings.Contains(linksTxt, "vless://") {
		t.Fatalf("expected vless link in links.txt, got: %s", linksTxt)
	}
	if !strings.Contains(linksTxt, fmt.Sprintf(":%d?", xrayInboundPort)) {
		t.Fatalf("expected links.txt to include test xray port %d, got: %s", xrayInboundPort, linksTxt)
	}

	linksB64URL := fmt.Sprintf("%s/sub/%s/links.b64", baseURL, aliceToken)
	linksB64 := strings.TrimSpace(string(doRawRequest(t, "GET", linksB64URL, "")))
	decoded, err := base64.StdEncoding.DecodeString(linksB64)
	if err != nil {
		t.Fatalf("decode links.b64: %v; value=%s", err, linksB64)
	}
	if !strings.Contains(string(decoded), "vless://") {
		t.Fatalf("decoded links.b64 must contain vless://, got: %s", string(decoded))
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(cwd)
}

func reservePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("failed to resolve reserved TCP address")
	}
	return addr.Port
}

func waitForHealth(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("health check did not become ready in %s: %s", timeout, url)
}

func waitForContainerRunning(ctx context.Context, repoRoot string, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCmd(ctx, repoRoot, "docker", "inspect", "-f", "{{.State.Running}}", containerName)
		if err == nil && strings.TrimSpace(out) == "true" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container %s is not running after %s", containerName, timeout)
}

func waitForTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("tcp endpoint %s is not reachable after %s", addr, timeout)
}

func doJSONRequest(t *testing.T, method, url, adminToken string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if adminToken != "" {
		req.Header.Set("X-Admin-Token", adminToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s failed with status %d: %s", method, url, resp.StatusCode, string(body))
	}
	return body
}

func doRawRequest(t *testing.T, method, url, adminToken string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if adminToken != "" {
		req.Header.Set("X-Admin-Token", adminToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s failed with status %d: %s", method, url, resp.StatusCode, string(body))
	}
	return body
}

func runCmd(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
