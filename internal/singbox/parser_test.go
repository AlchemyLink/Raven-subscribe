package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfig_Hysteria2(t *testing.T) {
	cfg := `{
		"inbounds": [
			{
				"type": "hysteria2",
				"tag": "hy2-in",
				"listen_port": 8443,
				"up_mbps": 100,
				"down_mbps": 100,
				"users": [
					{"name": "user1@test.com", "password": "pass1"},
					{"name": "user2@test.com", "password": "pass2"}
				],
				"obfs": {"type": "salamander", "password": "obfspass"},
				"tls": {"enabled": true, "server_name": "example.com"}
			}
		]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	inbounds, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}

	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(inbounds))
	}

	ib := inbounds[0]
	if ib.Tag != "hy2-in" {
		t.Errorf("tag: got %q, want %q", ib.Tag, "hy2-in")
	}
	if ib.Protocol != "hysteria2" {
		t.Errorf("protocol: got %q, want %q", ib.Protocol, "hysteria2")
	}
	if ib.Port != 8443 {
		t.Errorf("port: got %d, want 8443", ib.Port)
	}
	if len(ib.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(ib.Clients))
	}

	if ib.Clients[0].Identity != "user1@test.com" {
		t.Errorf("client[0] identity: got %q", ib.Clients[0].Identity)
	}

	// Verify stored config JSON has correct fields
	var cred storedHysteria2Config
	if err := json.Unmarshal([]byte(ib.Clients[0].ConfigJSON), &cred); err != nil {
		t.Fatalf("unmarshal cred: %v", err)
	}
	if cred.Password != "pass1" {
		t.Errorf("password: got %q, want %q", cred.Password, "pass1")
	}
	if cred.ServerName != "example.com" {
		t.Errorf("server_name: got %q, want %q", cred.ServerName, "example.com")
	}
	if cred.ObfsType != "salamander" {
		t.Errorf("obfs_type: got %q, want %q", cred.ObfsType, "salamander")
	}
	if cred.ObfsPassword != "obfspass" {
		t.Errorf("obfs_password: got %q", cred.ObfsPassword)
	}
	if cred.UpMbps != 100 {
		t.Errorf("up_mbps: got %d, want 100", cred.UpMbps)
	}

	// Verify raw JSON doesn't contain user passwords
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(ib.RawJSON), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["users"]; ok {
		t.Error("raw inbound JSON must not contain users (passwords)")
	}
}

func TestParseConfig_SkipsUnknownInbounds(t *testing.T) {
	cfg := `{
		"inbounds": [
			{"type": "vless", "tag": "vless-in", "listen_port": 443},
			{"type": "hysteria2", "tag": "hy2-in", "listen_port": 8443,
			 "users": [{"name": "u@test.com", "password": "p"}],
			 "tls": {"enabled": true}}
		]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	inbounds, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound (only hysteria2), got %d", len(inbounds))
	}
	if inbounds[0].Tag != "hy2-in" {
		t.Errorf("expected hy2-in, got %q", inbounds[0].Tag)
	}
}

func TestParseConfig_MissingFile(t *testing.T) {
	_, err := ParseConfig("/nonexistent/config.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseConfig_NoObfs(t *testing.T) {
	cfg := `{
		"inbounds": [
			{
				"type": "hysteria2",
				"tag": "hy2-plain",
				"listen_port": 443,
				"users": [{"name": "user@test.com", "password": "secret"}],
				"tls": {"enabled": true, "server_name": "example.com"}
			}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	inbounds, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(inbounds) != 1 || len(inbounds[0].Clients) != 1 {
		t.Fatalf("expected 1 inbound with 1 client")
	}
	var cred storedHysteria2Config
	if err := json.Unmarshal([]byte(inbounds[0].Clients[0].ConfigJSON), &cred); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cred.ObfsType != "" {
		t.Errorf("expected empty ObfsType without obfs, got %q", cred.ObfsType)
	}
	if cred.ServerName != "example.com" {
		t.Errorf("ServerName: got %q, want example.com", cred.ServerName)
	}
}

func TestParseConfig_EmptyTag_DefaultsToHysteria2In(t *testing.T) {
	cfg := `{
		"inbounds": [
			{
				"type": "hysteria2",
				"listen_port": 8443,
				"users": [{"name": "u@test.com", "password": "p"}]
			}
		]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	inbounds, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if inbounds[0].Tag != "hysteria2-in" {
		t.Errorf("tag: got %q, want hysteria2-in", inbounds[0].Tag)
	}
}

func TestParseConfig_EmptyInbounds(t *testing.T) {
	cfg := `{"inbounds": []}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	inbounds, err := ParseConfig(path)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(inbounds) != 0 {
		t.Errorf("expected 0 inbounds, got %d", len(inbounds))
	}
}
