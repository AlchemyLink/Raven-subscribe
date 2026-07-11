package xray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
)

func TestResolveCredentials_DefaultsToInsecure(t *testing.T) {
	SetNodeCredentials(nil)
	c := resolveCredentials("10.7.0.9:10085")
	if got := c.Info().SecurityProtocol; got != "insecure" {
		t.Fatalf("unset addr: security protocol %q, want insecure", got)
	}
}

func TestSetNodeCredentials_ResolvesPerAddr(t *testing.T) {
	tlsCreds := credentials.NewTLS(nil)
	SetNodeCredentials(map[string]credentials.TransportCredentials{
		"10.7.0.1:10085": tlsCreds,
	})
	t.Cleanup(func() { SetNodeCredentials(nil) })

	if got := resolveCredentials("10.7.0.1:10085").Info().SecurityProtocol; got != "tls" {
		t.Errorf("configured addr: security protocol %q, want tls", got)
	}
	if got := resolveCredentials("10.7.0.2:10085").Info().SecurityProtocol; got != "insecure" {
		t.Errorf("other addr: security protocol %q, want insecure", got)
	}
}

func TestBuildTLSCredentials_MissingCA(t *testing.T) {
	if _, err := BuildTLSCredentials("/no/such/ca.pem", "/no/c.pem", "/no/c.key", ""); err == nil {
		t.Fatal("expected error for missing ca_cert, got nil")
	}
}

func TestBuildTLSCredentials_BadPEM(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildTLSCredentials(ca, "/no/c.pem", "/no/c.key", ""); err == nil {
		t.Fatal("expected error for non-PEM ca_cert, got nil")
	}
}

func TestBuildTLSCredentials_HappyPath(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client.key")
	writeTestKeypair(t, caPath, certPath, keyPath)

	creds, err := BuildTLSCredentials(caPath, certPath, keyPath, "eu-1.internal")
	if err != nil {
		t.Fatalf("BuildTLSCredentials: %v", err)
	}
	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Errorf("security protocol %q, want tls", got)
	}
}

// writeTestKeypair generates a self-signed cert usable as both a CA bundle and a
// client keypair, writing PEM files for each path.
func writeTestKeypair(t *testing.T, caPath, certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "raven-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IsCA:         true,
		DNSNames:     []string{"eu-1.internal"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	for path, data := range map[string][]byte{caPath: certPEM, certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
