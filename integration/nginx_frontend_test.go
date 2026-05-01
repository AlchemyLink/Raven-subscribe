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
	const mockCN = "mock-vless-reality-v2"
	certDir := generateMockCert(t, mockCN)

	hostPort := reservePort(t)
	projectName := fmt.Sprintf("ravene2enginx%d", time.Now().UnixNano())
	composeEnv := []string{
		"COMPOSE_PROJECT_NAME=" + projectName,
		"MOCK_REALITY_CERT_DIR=" + certDir,
		fmt.Sprintf("NGINX_FRONTEND_HOST_PORT=%d", hostPort),
	}

	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "down", "-v"); err != nil {
		t.Logf("compose down output: %s", out)
	}
	if out, err := runCmdEnv(ctx, repoRoot, composeEnv,
		"docker", "compose", "-f", "docker-compose.test.yml",
		"--profile", "e2e-edge-eu", "up", "-d", "nginx-frontend", "mock-reality"); err != nil {
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

	t.Run("happy_path_destination_com_routes_to_mock", func(t *testing.T) {
		// openssl s_server takes a moment after container start before it
		// accepts on :4444; first probe sometimes hits a half-open socket
		// and gets a TCP RST mid-handshake. Retry briefly to absorb that.
		var (
			cn  string
			err error
		)
		for attempt := 0; attempt < 10; attempt++ {
			cn, err = probeSNICertCN(ctx, hostPort, "destination.com")
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				dumpEdgeLogs(ctx, t, repoRoot, composeEnv)
				t.Fatalf("probe destination.com cancelled: %v", ctx.Err())
			case <-time.After(1 * time.Second):
			}
		}
		if err != nil {
			dumpEdgeLogs(ctx, t, repoRoot, composeEnv)
			t.Fatalf("probe destination.com after retries: %v", err)
		}
		if cn != mockCN {
			t.Fatalf("expected cert CN=%q for SNI=destination.com, got %q", mockCN, cn)
		}
	})

	t.Run("default_tcp_close_for_unmatched_sni", func(t *testing.T) {
		cn, err := probeSNICertCN(ctx, hostPort, "some-bogus-sni.invalid")
		// Either the connection hangs/resets (TLS handshake never completes)
		// or it succeeds reaching a backend it shouldn't. Anything that
		// returns mockCN is wrong.
		if err == nil && cn == mockCN {
			t.Fatalf("unmatched SNI should not reach mock backend, got cert CN=%q", cn)
		}
	})
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
