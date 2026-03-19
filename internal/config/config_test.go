package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_EmptyPath_ReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr: got %q, want :8080", cfg.ListenAddr)
	}
	if cfg.ConfigDir != "/etc/xray/config.d" {
		t.Errorf("ConfigDir: got %q", cfg.ConfigDir)
	}
	if cfg.SyncInterval != 60 {
		t.Errorf("SyncInterval: got %d, want 60", cfg.SyncInterval)
	}
	if cfg.BalancerStrategy != "leastPing" {
		t.Errorf("BalancerStrategy: got %q, want leastPing", cfg.BalancerStrategy)
	}
}

func TestLoad_ValidFile_ReturnsParsedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	json := `{
		"server_host": "vpn.example.com",
		"listen_addr": ":9090",
		"admin_token": "secret"
	}`
	if err := os.WriteFile(path, []byte(json), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerHost != "vpn.example.com" {
		t.Errorf("ServerHost: got %q", cfg.ServerHost)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr: got %q", cfg.ListenAddr)
	}
	if cfg.AdminToken != "secret" {
		t.Errorf("AdminToken: got %q", cfg.AdminToken)
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if err.Error() == "" {
		t.Error("error message should not be empty")
	}
}

func TestLoad_InvalidJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{invalid json`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoad_MissingServerHost_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error when server_host is missing")
	}
	if err.Error() != "server_host is required in config" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidBalancerStrategy_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	json := `{"server_host":"x.com","balancer_strategy":"invalid"}`
	if err := os.WriteFile(path, []byte(json), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid balancer_strategy")
	}
}

func TestLoad_ValidBalancerStrategies(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"leastPing", "leastPing"},
		{"leastping", "leastPing"},
		{"random", "random"},
		{"roundRobin", "roundRobin"},
		{"roundrobin", "roundRobin"},
		{"leastLoad", "leastLoad"},
		{"leastload", "leastLoad"},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		json := `{"server_host":"x.com","balancer_strategy":"` + tt.input + `"}`
		if err := os.WriteFile(path, []byte(json), 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Errorf("Load with strategy %q: %v", tt.input, err)
			continue
		}
		if cfg.BalancerStrategy != tt.expected {
			t.Errorf("strategy %q: got %q, want %q", tt.input, cfg.BalancerStrategy, tt.expected)
		}
	}
}

func TestConfig_SubURL(t *testing.T) {
	cfg := &Config{BaseURL: "https://vpn.example.com"}
	got := cfg.SubURL("abc123")
	if got != "https://vpn.example.com/sub/abc123" {
		t.Errorf("SubURL: got %q, want https://vpn.example.com/sub/abc123", got)
	}
}
