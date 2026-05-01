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
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestUserConnectionE2E is the Tier 3 user-connection E2E harness: it
// asserts that a real Xray client can consume a Raven-subscribe-generated
// client config and route TCP traffic through the proxy chain end-to-end
// for each inbound shape we ship in production.
//
// One sub-test per variant. Each gets an isolated compose project (own
// network, own volumes). Selecting a single variant for CI matrix:
//
//	USER_CONNECTION_VARIANT=reality-vision E2E_USER_CONNECTION=1 go test ...
//
// Default: all known variants run serially.
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

	all := []string{"vless-tcp", "reality-vision", "xhttp", "reality-vision-vlessenc", "bridge-chain"}
	selected := strings.TrimSpace(os.Getenv("USER_CONNECTION_VARIANT"))
	variants := all
	if selected != "" {
		variants = []string{selected}
	}

	for _, v := range variants {
		v := v
		t.Run(v, func(t *testing.T) {
			runUserConnectionVariant(t, v)
		})
	}
}

type userConnectionVariant struct {
	srcInboundPath         string // path to a single-inbound JSON file (Ansible-rendered shape)
	userEmail              string // expected user email discovered by raven-subscribe
	needsContainerInternet bool   // Reality variants forward to a real TLS dest
	enableVLESSEncryption  bool   // generate fresh vlessenc keys, patch fixture decryption + raven config
}

func variantConfig(t *testing.T, repoRoot, variant string) userConnectionVariant {
	t.Helper()
	switch variant {
	case "vless-tcp":
		return userConnectionVariant{
			srcInboundPath: filepath.Join(repoRoot, "testdata", "xray", "e2e-user-config.d", "01_vless.json"),
			userEmail:      "alice@example.com",
		}
	case "reality-vision":
		return userConnectionVariant{
			srcInboundPath:         filepath.Join(repoRoot, "testdata", "ansible-rendered", "conf.d", "201-in-vless-reality-v2.json"),
			userEmail:              "test@raven.local",
			needsContainerInternet: true,
		}
	case "xhttp":
		return userConnectionVariant{
			srcInboundPath:         filepath.Join(repoRoot, "testdata", "ansible-rendered", "conf.d", "211-in-xhttp-v2.json"),
			userEmail:              "test@raven.local",
			needsContainerInternet: true,
		}
	case "reality-vision-vlessenc":
		return userConnectionVariant{
			srcInboundPath:         filepath.Join(repoRoot, "testdata", "ansible-rendered", "conf.d", "201-in-vless-reality-v2.json"),
			userEmail:              "test@raven.local",
			needsContainerInternet: true,
			enableVLESSEncryption:  true,
		}
	default:
		t.Fatalf("unknown variant %q", variant)
		return userConnectionVariant{}
	}
}

func runUserConnectionVariant(t *testing.T, variant string) {
	if variant == "bridge-chain" {
		// TODO: implement bridge-chain variant. Requires a second xray
		// service (bridge) with chain outbound to xray-eu, and a way to
		// override raven-subscribe's server_host so the generated URL
		// points at the bridge port. Tracked as future Tier 3 work.
		t.Skip("bridge-chain variant not yet implemented")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	repoRoot := mustRepoRoot(t)
	cfg := variantConfig(t, repoRoot, variant)

	if cfg.needsContainerInternet {
		if err := probeContainerInternet(ctx); err != nil {
			t.Skipf("variant %q requires Docker container outbound internet (Reality dest dial); probe failed: %v", variant, err)
		}
	}

	var (
		serverDecryption     string
		clientEncryption     string
		vlessClientEncMap    map[string]string
	)
	if cfg.enableVLESSEncryption {
		var err error
		serverDecryption, clientEncryption, err = generateVlessenc(ctx)
		if err != nil {
			t.Fatalf("generate vlessenc keys: %v", err)
		}
	}

	xrayConfigDir, inboundTag := buildVariantConfigDir(t, cfg.srcInboundPath, serverDecryption)

	if cfg.enableVLESSEncryption {
		vlessClientEncMap = map[string]string{inboundTag: clientEncryption}
	}

	env := composeTestEnv{
		repoRoot:              repoRoot,
		xrayConfigDir:         xrayConfigDir,
		vlessClientEncryption: vlessClientEncMap,
	}
	env.prepare(ctx, t)
	defer env.teardown(ctx, t)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", env.appPort)
	waitForHealth(t, baseURL+"/health", 90*time.Second)

	env.requestStatus(t, http.MethodPost, "/api/sync", adminToken, nil, http.StatusOK)

	body := env.requestStatus(t, http.MethodGet, "/api/users", adminToken, nil, http.StatusOK)
	users := decodeUsers(t, body)
	user, ok := findUser(users, cfg.userEmail)
	if !ok {
		t.Fatalf("%s not found in %v", cfg.userEmail, users)
	}
	if strings.TrimSpace(user.Token) == "" {
		t.Fatalf("%s has empty sub token: %+v", cfg.userEmail, user)
	}

	rawConfig := env.compactSubStatus(t, user.Token, "", http.StatusOK)
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

// buildVariantConfigDir reads a single-inbound fixture (Ansible-rendered or
// hand-written shape) and produces a self-contained xray config dir in
// t.TempDir() with that inbound + minimal outbounds + a routing rule that
// sends everything through freedom. Patches inbounds[*].listen to 0.0.0.0
// so xray-client (in the same compose network) can reach it via the service
// hostname. If decryptionOverride is non-empty, replaces inbounds[*].settings.decryption
// (used to inject a freshly-generated VLESS Encryption server key).
// Returns the config dir path and the first inbound tag.
func buildVariantConfigDir(t *testing.T, srcInboundFile, decryptionOverride string) (string, string) {
	t.Helper()

	raw, err := os.ReadFile(srcInboundFile) //nolint:gosec // path is test-controlled
	if err != nil {
		t.Fatalf("read fixture %s: %v", srcInboundFile, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse fixture %s: %v", srcInboundFile, err)
	}

	inbounds, _ := doc["inbounds"].([]interface{})
	if len(inbounds) == 0 {
		t.Fatalf("fixture has no inbounds: %s", srcInboundFile)
	}
	var inboundTags []string
	for _, ib := range inbounds {
		m, _ := ib.(map[string]interface{})
		if _, has := m["listen"]; has {
			m["listen"] = "0.0.0.0"
		}
		if tag, _ := m["tag"].(string); tag != "" {
			inboundTags = append(inboundTags, tag)
		}
		if decryptionOverride != "" {
			settings, _ := m["settings"].(map[string]interface{})
			if settings != nil {
				settings["decryption"] = decryptionOverride
			}
		}
	}
	if len(inboundTags) == 0 {
		t.Fatalf("no inbound tag in fixture: %s", srcInboundFile)
	}

	if _, has := doc["outbounds"]; !has {
		doc["outbounds"] = []map[string]interface{}{
			{"protocol": "freedom", "tag": "direct"},
			{"protocol": "blackhole", "tag": "block"},
		}
	}
	if _, has := doc["routing"]; !has {
		doc["routing"] = map[string]interface{}{
			"domainStrategy": "AsIs",
			"rules": []map[string]interface{}{
				{"type": "field", "inboundTag": inboundTags, "outboundTag": "direct"},
			},
		}
	}
	if _, has := doc["log"]; !has {
		doc["log"] = map[string]interface{}{"loglevel": "info"}
	}
	// Reality requires resolving the dest hostname to forward unverified
	// handshakes to. Some Docker network setups (Fedora + firewalld)
	// silently drop traffic to the embedded resolver 127.0.0.11:53; pin
	// public resolvers so the test does not depend on host networking.
	if _, has := doc["dns"]; !has {
		doc["dns"] = map[string]interface{}{
			"servers":       []string{"8.8.8.8", "1.1.1.1"},
			"queryStrategy": "UseIPv4",
		}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal rebuilt config: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "config.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config.d: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01_inbound.json"), out, 0o644); err != nil { //nolint:gosec
		t.Fatalf("write config.d: %v", err)
	}
	return dir, inboundTags[0]
}

// generateVlessenc invokes `xray vlessenc` in a throwaway container and
// returns a fresh (server decryption, client encryption) pair. Picks the
// last (Post-Quantum, ML-KEM-768) keypair from the output to mirror prod.
func generateVlessenc(ctx context.Context) (string, string, error) {
	xrayImage := strings.TrimSpace(os.Getenv("XRAY_IMAGE"))
	if xrayImage == "" {
		xrayImage = defaultXrayImage
	}
	gctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	//nolint:gosec // xrayImage is sourced from a controlled env var
	cmd := exec.CommandContext(gctx, "docker", "run", "--rm", xrayImage, "vlessenc")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("vlessenc generation: %w\n%s", err, string(out))
	}
	decRE := regexp.MustCompile(`"decryption":\s*"([^"]+)"`)
	encRE := regexp.MustCompile(`"encryption":\s*"([^"]+)"`)
	decMatches := decRE.FindAllStringSubmatch(string(out), -1)
	encMatches := encRE.FindAllStringSubmatch(string(out), -1)
	if len(decMatches) == 0 || len(encMatches) == 0 {
		return "", "", fmt.Errorf("no decryption/encryption found in vlessenc output:\n%s", string(out))
	}
	return decMatches[len(decMatches)-1][1], encMatches[len(encMatches)-1][1], nil
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

// probeContainerInternet runs a throwaway alpine container that tries to
// reach a known TLS endpoint. Returns nil iff the container can resolve and
// connect to the internet. On hosts where firewalld or NAT rules block the
// docker bridge from reaching the outside (a common Fedora-with-firewalld
// failure mode), this returns an error and the caller t.Skip's.
func probeContainerInternet(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	//nolint:gosec // image and args are constants
	cmd := exec.CommandContext(probeCtx, "docker", "run", "--rm", "alpine:3.20",
		"sh", "-c", "wget -q -T 5 -t 1 -O- https://www.google.com >/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
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
