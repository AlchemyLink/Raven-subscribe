package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNginxFrontendSNI is the Tier 4 edge-eu E2E: it boots nginx with a
// snapshot of the Ansible-rendered nginx_frontend stream.conf in front of a
// mock TLS upstream and asserts that SNI routing lands on the right backend
// (verified via the upstream's certificate subject CN) and that an unmatched
// SNI hits the default tcp_close branch.
//
// Gate: E2E_NGINX_FRONTEND=1.
func TestNginxFrontendSNI(t *testing.T) {
	if os.Getenv("E2E_NGINX_FRONTEND") != "1" {
		t.Skip("set E2E_NGINX_FRONTEND=1 to run nginx-frontend SNI E2E")
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
	}

	hostPort := reservePort(t)
	projectName := fmt.Sprintf("ravene2enginx%d", time.Now().UnixNano())
	composeEnv := []string{
		"COMPOSE_PROJECT_NAME=" + projectName,
		fmt.Sprintf("NGINX_FRONTEND_HOST_PORT=%d", hostPort),
	}
	for _, m := range mocks {
		composeEnv = append(composeEnv, m.envVar+"="+generateMockCert(t, m.cn))
	}

	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "down", "-v"); err != nil {
		t.Logf("compose down output: %s", out)
	}
	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "up", "-d",
		"nginx-frontend", "mock-reality", "mock-xhttp", "mock-fallback"); err != nil {
		t.Fatalf("compose up edge-eu failed: %v\n%s", err, out)
	}
	defer func() {
		if out, err := runCmdEnv(context.Background(), repoRoot, composeEnv,
			"docker", "compose", "-f", "docker-compose.test.yml",
			"--profile", "e2e-edge-eu", "down", "-v"); err != nil {
			t.Logf("teardown: %s", out)
		}
	}()

	if err := waitForTCPErr(fmt.Sprintf("127.0.0.1:%d", hostPort), 30*time.Second); err != nil {
		dumpEdgeLogs(ctx, t, repoRoot, composeEnv)
		t.Fatalf("nginx-frontend not listening: %v", err)
	}

	cases := []struct {
		name       string
		sni        string
		expectedCN string
	}{
		{"reality_v2_routes_to_mock_reality", "destination.com", "mock-vless-reality-v2"},
		{"xhttp_routes_to_mock_xhttp", "addons.example", "mock-xhttp-reality-v2"},
		{"fallback_stackoverflow_routes_to_mock_fallback", "stackoverflow.com", "mock-fallback"},
		{"fallback_superuser_routes_to_mock_fallback", "superuser.com", "mock-fallback"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cn, err := probeSNIWithRetry(ctx, hostPort, tc.sni)
			if err != nil {
				dumpEdgeLogs(ctx, t, repoRoot, composeEnv)
				t.Fatalf("probe SNI=%s: %v", tc.sni, err)
			}
			if cn != tc.expectedCN {
				t.Fatalf("SNI=%s expected cert CN=%q, got %q", tc.sni, tc.expectedCN, cn)
			}
		})
	}

	t.Run("default_tcp_close_for_unmatched_sni", func(t *testing.T) {
		cn, err := probeSNICertCN(ctx, hostPort, "some-bogus-sni.invalid")
		// The connection should reset before TLS completes — anything that
		// returns one of our mock CNs means routing leaked.
		if err == nil {
			for _, m := range mocks {
				if cn == m.cn {
					t.Fatalf("unmatched SNI leaked to mock backend, got cert CN=%q", cn)
				}
			}
		}
	})
}

// probeSNIWithRetry wraps probeSNICertCN with a short retry loop. openssl
// s_server takes a moment after container start before it accepts on its
// listen port; the first dial through nginx sometimes hits a half-open
// socket and the kernel sends RST mid-handshake.
func probeSNIWithRetry(ctx context.Context, hostPort int, sni string) (string, error) {
	var (
		cn      string
		lastErr error
	)
	for attempt := 0; attempt < 10; attempt++ {
		var err error
		cn, err = probeSNICertCN(ctx, hostPort, sni)
		if err == nil {
			return cn, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return "", lastErr
}

// generateMockCert writes a self-signed RSA cert with CN=cn into a fresh
// temp dir and returns the absolute path of that dir (containing
// cert.pem + key.pem). socat/openssl s_server in the mock container reads
// from this dir via the MOCK_REALITY_CERT_DIR bind mount.
func generateMockCert(t *testing.T, cn string) string {
	t.Helper()
	parent := t.TempDir()
	// t.TempDir creates 0700 — the docker volume bind-mount needs the
	// path to be traversable by the alpine/openssl container's root user.
	// Container root can't bypass user-namespace remapping if Docker is
	// configured for it, so widen perms explicitly.
	if err := os.Chmod(parent, 0o755); err != nil { //nolint:gosec
		t.Fatalf("chmod tempdir: %v", err)
	}
	dir := filepath.Join(parent, "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec
		t.Fatalf("mkdir cert dir: %v", err)
	}
	//nolint:gosec // openssl args are constants + a controlled CN
	cmd := exec.Command("openssl", "req", "-x509",
		"-newkey", "rsa:2048",
		"-keyout", filepath.Join(dir, "key.pem"),
		"-out", filepath.Join(dir, "cert.pem"),
		"-days", "3650", "-nodes",
		"-subj", "/CN="+cn)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("openssl gen cert: %v\n%s", err, string(out))
	}
	return dir
}

// probeSNICertCN opens a TLS connection to nginx with the given SNI and
// returns the subject CN of the leaf cert presented by the upstream. Useful
// because the only way to identify which upstream nginx routed to is the
// cert chain it returns (each mock has a distinct self-signed cert).
func probeSNICertCN(ctx context.Context, hostPort int, sni string) (string, error) {
	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // self-signed test certs by design
			ServerName:         sni,
		},
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(probeCtx, "tcp", fmt.Sprintf("127.0.0.1:%d", hostPort))
	if err != nil {
		return "", fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close() //nolint:errcheck
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return "", fmt.Errorf("unexpected conn type %T", conn)
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("no peer certificates")
	}
	return commonName(state.PeerCertificates[0].Subject.String()), nil
}

func commonName(subject string) string {
	for _, part := range strings.Split(subject, ",") {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "CN=") {
			return strings.TrimPrefix(p, "CN=")
		}
	}
	return ""
}

func dumpEdgeLogs(ctx context.Context, t *testing.T, repoRoot string, composeEnv []string) {
	t.Helper()
	for _, svc := range []string{"nginx-frontend", "mock-reality"} {
		out, _ := runCmdEnv(ctx, repoRoot, composeEnv,
			"docker", "compose", "-f", "docker-compose.test.yml",
			"--profile", "e2e-edge-eu", "logs", "--no-color", svc)
		t.Logf("---- %s logs ----\n%s", svc, out)
	}
}

// Suppress unused import warnings — x509 is used implicitly via tls.Conn.PeerCertificates.
var _ = x509.Certificate{}
