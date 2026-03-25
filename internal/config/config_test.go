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
	if cfg.XrayConfigFilePerm() != 0o600 {
		t.Errorf("XrayConfigFilePerm default: got %o, want 0600", cfg.XrayConfigFilePerm())
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
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte(`{invalid json`), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
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
		if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
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

func TestConfig_IsXrayEnabled(t *testing.T) {
	// nil pointer → default true
	boolFalse := false
	boolTrue := true
	tests := []struct {
		cfg  *Config
		want bool
	}{
		{&Config{}, true},
		{&Config{XrayEnabled: &boolTrue}, true},
		{&Config{XrayEnabled: &boolFalse}, false},
	}
	for _, tt := range tests {
		got := tt.cfg.IsXrayEnabled()
		if got != tt.want {
			t.Errorf("IsXrayEnabled: got %v, want %v", got, tt.want)
		}
	}
}

func TestConfig_IsSingboxEnabled(t *testing.T) {
	boolFalse := false
	boolTrue := true
	tests := []struct {
		cfg  *Config
		want bool
	}{
		{&Config{}, false},
		{&Config{SingboxConfig: "/etc/sing-box/config.json"}, true},
		{&Config{SingboxConfig: "  "}, false},
		{&Config{SingboxEnabled: &boolTrue}, true},
		{&Config{SingboxEnabled: &boolFalse, SingboxConfig: "/path"}, false},
	}
	for _, tt := range tests {
		got := tt.cfg.IsSingboxEnabled()
		if got != tt.want {
			t.Errorf("IsSingboxEnabled(singbox_config=%q, enabled=%v): got %v, want %v",
				tt.cfg.SingboxConfig, tt.cfg.SingboxEnabled, got, tt.want)
		}
	}
}

func TestConfig_SubURLs(t *testing.T) {
	cfg := &Config{BaseURL: "https://vpn.example.com"}
	urls := cfg.SubURLs("mytoken")
	if urls.Full != "https://vpn.example.com/sub/mytoken" {
		t.Errorf("Full: got %q", urls.Full)
	}
	if urls.LinksText != "https://vpn.example.com/sub/mytoken/links.txt" {
		t.Errorf("LinksText: got %q", urls.LinksText)
	}
	if urls.LinksB64 != "https://vpn.example.com/sub/mytoken/links.b64" {
		t.Errorf("LinksB64: got %q", urls.LinksB64)
	}
	if urls.Compact != "https://vpn.example.com/c/mytoken" {
		t.Errorf("Compact: got %q", urls.Compact)
	}
	if urls.Singbox != "https://vpn.example.com/sub/mytoken/singbox" {
		t.Errorf("Singbox: got %q", urls.Singbox)
	}
	if urls.Hysteria2 != "https://vpn.example.com/sub/mytoken/hysteria2" {
		t.Errorf("Hysteria2: got %q", urls.Hysteria2)
	}
}

func TestLoad_XrayConfigFileMode(t *testing.T) {
	tests := []struct {
		raw      string
		wantMode os.FileMode
		wantErr  bool
	}{
		{`"0644"`, 0o644, false},
		{`"644"`, 0o644, false},
		{`"0o755"`, 0o755, false},
		{`"0755"`, 0o755, false},
		{`""`, 0o600, false},
		{`"0800"`, 0, true},
		{`"888"`, 0, true},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		var modeLine string
		if tt.raw == `""` {
			modeLine = ""
		} else {
			modeLine = `,"xray_config_file_mode":` + tt.raw
		}
		json := `{"server_host":"x.com"` + modeLine + `}`
		if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg, err := Load(path)
		if tt.wantErr {
			if err == nil {
				t.Errorf("raw %s: expected error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("raw %s: Load: %v", tt.raw, err)
			continue
		}
		if cfg.XrayConfigFilePerm() != tt.wantMode {
			t.Errorf("raw %s: got mode %o, want %o", tt.raw, cfg.XrayConfigFilePerm(), tt.wantMode)
		}
	}
}

func TestLoad_VLESSClientEncryption_TrimsKeysAndValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	json := `{
		"server_host": "vpn.example.com",
		"admin_token": "x",
		"vless_client_encryption": {
			"  vless-reality-in  ": "  client-string-here  ",
			"bad": "   ",
			"  ": "skipped"
		}
	}`
	if err := os.WriteFile(path, []byte(json), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := cfg.VLESSClientEncryption["vless-reality-in"]
	if !ok || got != "client-string-here" {
		t.Fatalf("VLESSClientEncryption: got %q ok=%v, want client-string-here", got, ok)
	}
	if len(cfg.VLESSClientEncryption) != 1 {
		t.Errorf("expected 1 map entry after normalize, got %d", len(cfg.VLESSClientEncryption))
	}
}
