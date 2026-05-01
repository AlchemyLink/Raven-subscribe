package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUserConnectionE2E is the Tier 3 user-connection E2E harness: it
// asserts that a real Xray client can consume a Raven-subscribe-generated
// client config and route TCP traffic through the proxy chain end-to-end.
//
// Variants are selected by USER_CONNECTION_VARIANT (default "vless-tcp").
// The vless-tcp variant uses the baseline plain VLESS+TCP inbound from
// docker/xray/config.d and does not need a real Reality target.
//
// Gate: E2E_USER_CONNECTION=1.
func TestUserConnectionE2E(t *testing.T) {
	if os.Getenv("E2E_USER_CONNECTION") != "1" {
		t.Skip("set E2E_USER_CONNECTION=1 to run user-connection E2E")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary is not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl binary is not available")
	}

	variant := strings.TrimSpace(os.Getenv("USER_CONNECTION_VARIANT"))
	if variant == "" {
		variant = "vless-tcp"
	}
	if variant != "vless-tcp" {
		t.Skipf("variant %q not yet implemented (Day 1 ships vless-tcp only)", variant)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	repoRoot := mustRepoRoot(t)
	env := composeTestEnv{
		repoRoot: repoRoot,
		// Use a minimal config.d that won't crash-loop xray (the legacy fixture
		// at testdata/xray/config.d references missing TLS certs — fine for
		// read-only API tests, fatal for tests that need a running xray).
		xrayConfigDir: filepath.Join(repoRoot, "testdata", "xray", "e2e-user-config.d"),
	}
	env.prepare(ctx, t)
	defer env.teardown(ctx, t)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", env.appPort)
	waitForHealth(t, baseURL+"/health", 90*time.Second)

	env.requestStatus(t, http.MethodPost, "/api/sync", adminToken, nil, http.StatusOK)

	body := env.requestStatus(t, http.MethodGet, "/api/users", adminToken, nil, http.StatusOK)
	users := decodeUsers(t, body)
	alice, ok := findUser(users, "alice@example.com")
	if !ok {
		t.Fatalf("alice@example.com not found in %v", users)
	}
	if strings.TrimSpace(alice.Token) == "" {
		t.Fatalf("alice has empty sub token: %+v", alice)
	}

	rawConfig := env.compactSubStatus(t, alice.Token, "", http.StatusOK)
	clientConfig := rewriteClientConfigForContainer(t, rawConfig, "xray")

	socksPort := reservePort(t)
	clientConfigPath := filepath.Join(env.tmpDir, "xray-client.json")
	// 0644 so the container's xray user can read; this file holds only test
	// credentials, no production secrets.
	if err := os.WriteFile(clientConfigPath, clientConfig, 0o644); err != nil { //nolint:gosec
		t.Fatalf("write xray-client config: %v", err)
	}

	composeEnv := append(env.composeEnv(),
		"XRAY_CLIENT_CONFIG_PATH="+clientConfigPath,
		fmt.Sprintf("XRAY_CLIENT_HOST_PORT=%d", socksPort),
	)

	if out, err := runCmdEnv(ctx, env.repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-user", "up", "-d", "xray-client", "test-target"); err != nil {
		t.Fatalf("compose up e2e-user profile failed: %v\n%s", err, out)
	}

	if err := waitForTCPErr(fmt.Sprintf("127.0.0.1:%d", socksPort), 30*time.Second); err != nil {
		dumpClientLogs(ctx, t, env.repoRoot, composeEnv)
		t.Fatalf("xray-client socks5 port not ready: %v", err)
	}

	if err := curlThroughSocks(ctx, socksPort, "http://test-target/get"); err != nil {
		dumpClientLogs(ctx, t, env.repoRoot, composeEnv)
		t.Fatalf("traffic check failed: %v", err)
	}
}

// rewriteClientConfigForContainer adapts the Raven-subscribe-generated client
// config for use inside the xray-client docker container:
//   - outbounds[*].settings.{vnext,servers}[*].address: 127.0.0.1 → target
//     (so the client reaches xray-server via compose service hostname)
//   - inbounds[*].listen: 127.0.0.1 → 0.0.0.0
//     (Docker port mapping only forwards from 0.0.0.0 bindings)
func rewriteClientConfigForContainer(t *testing.T, raw []byte, target string) []byte {
	t.Helper()
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse client config: %v body=%s", err, string(raw))
	}

	addrRewrites := 0
	if outbounds, ok := doc["outbounds"].([]interface{}); ok {
		for _, ob := range outbounds {
			m, _ := ob.(map[string]interface{})
			settings, _ := m["settings"].(map[string]interface{})
			if settings == nil {
				continue
			}
			for _, key := range []string{"vnext", "servers"} {
				list, _ := settings[key].([]interface{})
				for _, item := range list {
					sm, _ := item.(map[string]interface{})
					if addr, _ := sm["address"].(string); addr == "127.0.0.1" {
						sm["address"] = target
						addrRewrites++
					}
				}
			}
		}
	}
	if addrRewrites == 0 {
		t.Fatalf("no 127.0.0.1 outbound addresses found; client config does not match expected shape:\n%s", string(raw))
	}

	listenRewrites := 0
	if inbounds, ok := doc["inbounds"].([]interface{}); ok {
		for _, ib := range inbounds {
			m, _ := ib.(map[string]interface{})
			if listen, _ := m["listen"].(string); listen == "127.0.0.1" {
				m["listen"] = "0.0.0.0"
				listenRewrites++
			}
		}
	}
	if listenRewrites == 0 {
		t.Fatalf("no 127.0.0.1 inbound listen found; client config does not match expected shape:\n%s", string(raw))
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal rewritten config: %v", err)
	}
	return out
}

func waitForTCPErr(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("tcp %s did not become ready within %s", addr, timeout)
}

func curlThroughSocks(ctx context.Context, socksPort int, target string) error {
	//nolint:gosec // controlled command, args are not attacker-controlled in tests
	cmd := exec.CommandContext(ctx, "curl", "-fsS",
		"--max-time", "20",
		"--proxy", fmt.Sprintf("socks5h://127.0.0.1:%d", socksPort),
		target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("curl failed: %w\noutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), `"url"`) {
		return fmt.Errorf("unexpected curl response (no \"url\" field):\n%s", string(out))
	}
	return nil
}

func dumpClientLogs(ctx context.Context, t *testing.T, repoRoot string, composeEnv []string) {
	t.Helper()
	for _, svc := range []string{"xray-client", "xray", "test-target"} {
		out, _ := runCmdEnv(ctx, repoRoot, composeEnv,
			"docker", "compose", "-f", "docker-compose.test.yml",
			"--profile", "e2e-user", "logs", "--no-color", svc)
		t.Logf("---- %s logs ----\n%s", svc, out)
	}
}
