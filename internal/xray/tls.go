package xray

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// nodeCreds maps a node's gRPC api_addr to the transport credentials used to
// dial it. It is populated once at startup (SetNodeCredentials) from the
// multi-node TLS config and only ever read afterwards. dialXrayAPI consults it;
// an api_addr with no entry dials plaintext (insecure) — the correct default
// for a WireGuard/loopback node and for the legacy single-node local dial.
var (
	nodeCredsMu sync.RWMutex
	nodeCreds   map[string]credentials.TransportCredentials
)

// SetNodeCredentials installs the per-node transport credentials keyed by
// api_addr. It replaces any previous set wholesale and is intended to be called
// exactly once during startup, before the control plane dials any node. Passing
// nil or an empty map returns every dial to plaintext.
func SetNodeCredentials(creds map[string]credentials.TransportCredentials) {
	nodeCredsMu.Lock()
	defer nodeCredsMu.Unlock()
	nodeCreds = creds
}

// resolveCredentials returns the transport credentials to dial apiAddr with,
// defaulting to plaintext when no mTLS was configured for that address.
func resolveCredentials(apiAddr string) credentials.TransportCredentials {
	nodeCredsMu.RLock()
	defer nodeCredsMu.RUnlock()
	if c, ok := nodeCreds[apiAddr]; ok {
		return c
	}
	return insecure.NewCredentials()
}

// BuildTLSCredentials assembles mutual-TLS client credentials for a gRPC dial:
// it presents the client cert/key and verifies the server against caCertPath.
// serverName, when non-empty, overrides the SAN the server cert is matched
// against (otherwise gRPC uses the dial target's host). TLS 1.3 is required.
func BuildTLSCredentials(caCertPath, clientCertPath, clientKeyPath, serverName string) (credentials.TransportCredentials, error) {
	// #nosec G304 -- paths come from the operator's own config file, not user input.
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read ca_cert %s: %w", caCertPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("ca_cert %s: no PEM certificates found", caCertPath)
	}
	cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client keypair (%s / %s): %w", clientCertPath, clientKeyPath, err)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}), nil
}
