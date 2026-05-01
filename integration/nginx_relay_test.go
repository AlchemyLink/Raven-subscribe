package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestNginxRelaySNI is the Tier 4 edge-ru E2E: it boots an RU-side nginx
// relay in front of the EU-side nginx-frontend (and its mock TLS upstreams)
// and asserts that SNI propagation through the 2-hop chain works as
// configured. SNIs that don't match local relay rules are passed through to
// nginx-frontend, which routes them to the matching mock; SNIs the relay
// claims as local terminate at mock-relay-local.
//
// Day 3 minimum slice — relay_bridge_enabled / relay_transparent_enabled
// are intentionally false in the snapshot fixture so the test does not
// require xray-bridge wiring. Transparent-bridge routing is backlog.
//
// Gate: E2E_NGINX_RELAY=1.
func TestNginxRelaySNI(t *testing.T) {
	if os.Getenv("E2E_NGINX_RELAY") != "1" {
		t.Skip("set E2E_NGINX_RELAY=1 to run nginx-relay SNI E2E")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary is not available")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	repoRoot := mustRepoRoot(t)

	mocks := []struct {
		envVar string
		cn     string
	}{
		{"MOCK_REALITY_CERT_DIR", "mock-vless-reality-v2"},
		{"MOCK_XHTTP_CERT_DIR", "mock-xhttp-reality-v2"},
		{"MOCK_FALLBACK_CERT_DIR", "mock-fallback"},
		{"MOCK_RELAY_LOCAL_CERT_DIR", "mock-relay-local"},
	}

	frontendHostPort := reservePort(t)
	relayHostPort := reservePort(t)
	projectName := fmt.Sprintf("ravene2erelay%d", time.Now().UnixNano())
	composeEnv := []string{
		"COMPOSE_PROJECT_NAME=" + projectName,
		fmt.Sprintf("NGINX_FRONTEND_HOST_PORT=%d", frontendHostPort),
		fmt.Sprintf("NGINX_RELAY_HOST_PORT=%d", relayHostPort),
	}
	for _, m := range mocks {
		composeEnv = append(composeEnv, m.envVar+"="+generateMockCert(t, m.cn))
	}

	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "--profile", "e2e-edge-ru", "down", "-v"); err != nil {
		t.Logf("compose down output: %s", out)
	}
	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "--profile", "e2e-edge-ru", "up", "-d",
		"nginx-relay", "nginx-frontend",
		"mock-reality", "mock-xhttp", "mock-fallback", "mock-relay-local"); err != nil {
		t.Fatalf("compose up edge-ru failed: %v\n%s", err, out)
	}
	defer func() {
		if out, err := runCmdEnv(context.Background(), repoRoot, composeEnv,
			"docker", "compose", "-f", "docker-compose.test.yml",
			"--profile", "e2e-edge-eu", "--profile", "e2e-edge-ru", "down", "-v"); err != nil {
			t.Logf("teardown: %s", out)
		}
	}()

	if err := waitForTCPErr(fmt.Sprintf("127.0.0.1:%d", relayHostPort), 30*time.Second); err != nil {
		dumpRelayLogs(ctx, t, repoRoot, composeEnv)
		t.Fatalf("nginx-relay not listening: %v", err)
	}

	cases := []struct {
		name       string
		sni        string
		expectedCN string
		// note describes what the assertion proves so logs are
		// self-explanatory when a mapping breaks.
		note string
	}{
		{
			"pass_through_reality_sni_lands_on_eu_mock",
			"destination.com",
			"mock-vless-reality-v2",
			"unmatched-by-relay SNI is forwarded to nginx-frontend → vless_reality_v2 upstream",
		},
		{
			"pass_through_xhttp_sni_lands_on_eu_mock",
			"addons.example",
			"mock-xhttp-reality-v2",
			"second pass-through SNI proves the matrix isn't accidentally pinned to one upstream",
		},
		{
			"local_sni_terminates_at_relay",
			"relay.test.local",
			"mock-relay-local",
			"relay_domain SNI is intercepted on the RU side and never reaches nginx-frontend",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cn, err := probeSNIWithRetry(ctx, relayHostPort, tc.sni)
			if err != nil {
				dumpRelayLogs(ctx, t, repoRoot, composeEnv)
				t.Fatalf("probe SNI=%s (%s): %v", tc.sni, tc.note, err)
			}
			if cn != tc.expectedCN {
				t.Fatalf("SNI=%s expected cert CN=%q, got %q (%s)", tc.sni, tc.expectedCN, cn, tc.note)
			}
		})
	}
}

func dumpRelayLogs(ctx context.Context, t *testing.T, repoRoot string, composeEnv []string) {
	t.Helper()
	for _, svc := range []string{
		"nginx-relay", "nginx-frontend",
		"mock-relay-local", "mock-reality", "mock-xhttp", "mock-fallback",
	} {
		out, _ := runCmdEnv(ctx, repoRoot, composeEnv,
			"docker", "compose", "-f", "docker-compose.test.yml",
			"--profile", "e2e-edge-eu", "--profile", "e2e-edge-ru",
			"logs", "--no-color", svc)
		t.Logf("---- %s logs ----\n%s", svc, out)
	}
}
