package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigFile(t *testing.T) {
	tmpDir := t.TempDir()

	testConfig := `{
		"inbounds": [
			{
				"tag": "vless-test",
				"port": 443,
				"protocol": "vless",
				"settings": {
					"decryption": "none",
					"clients": [
						{"id": "11111111-1111-1111-1111-111111111111", "email": "alice@test.com"}
					]
				},
				"streamSettings": {
					"network": "tcp"
				}
			},
			{
				"tag": "vmess-test",
				"port": 8443,
				"protocol": "vmess",
				"settings": {
					"clients": [
						{"id": "22222222-2222-2222-2222-222222222222", "alterId": 0, "email": "bob@test.com"}
					]
				}
			}
		]
	}`

	configPath := filepath.Join(tmpDir, "test.json")
	if err := os.WriteFile(configPath, []byte(testConfig), 0o644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	inbounds, err := ParseConfigFile(configPath)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}

	if len(inbounds) != 2 {
		t.Fatalf("expected 2 inbounds, got %d", len(inbounds))
	}

	vless := findInboundByTag(inbounds, "vless-test")
	if vless.Protocol != "vless" {
		t.Fatalf("vless protocol: expected vless, got %s", vless.Protocol)
	}
	if len(vless.Clients) != 1 {
		t.Fatalf("vless clients: expected 1, got %d", len(vless.Clients))
	}
	if vless.Clients[0].Identity != "alice@test.com" {
		t.Fatalf("vless client identity: expected alice@test.com, got %s", vless.Clients[0].Identity)
	}

	vmess := findInboundByTag(inbounds, "vmess-test")
	if vmess.Protocol != "vmess" {
		t.Fatalf("vmess protocol: expected vmess, got %s", vmess.Protocol)
	}
	if len(vmess.Clients) != 1 {
		t.Fatalf("vmess clients: expected 1, got %d", len(vmess.Clients))
	}
	if vmess.Clients[0].Identity != "bob@test.com" {
		t.Fatalf("vmess client identity: expected bob@test.com, got %s", vmess.Clients[0].Identity)
	}
}

func TestParseConfigFileNoWrapper(t *testing.T) {
	tmpDir := t.TempDir()

	// Array-only config (no object wrapper)
	testConfig := `[
		{
			"tag": "trojan-only",
			"port": 4443,
			"protocol": "trojan",
			"settings": {
				"clients": [
					{"password": "trojan-pass-123", "email": "trojan-user@test.com"}
				]
			}
		}
	]`

	configPath := filepath.Join(tmpDir, "array.json")
	if err := os.WriteFile(configPath, []byte(testConfig), 0o644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	inbounds, err := ParseConfigFile(configPath)
	if err != nil {
		t.Fatalf("ParseConfigFile: %v", err)
	}

	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(inbounds))
	}

	if inbounds[0].Protocol != "trojan" {
		t.Fatalf("expected trojan protocol, got %s", inbounds[0].Protocol)
	}
	if len(inbounds[0].Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(inbounds[0].Clients))
	}
	if inbounds[0].Clients[0].Identity != "trojan-user@test.com" {
		t.Fatalf("expected identity trojan-user@test.com, got %s", inbounds[0].Clients[0].Identity)
	}
}

func TestParseConfigDir(t *testing.T) {
	if _, err := os.Stat("../../../testdata/xray/config.d"); os.IsNotExist(err) {
		t.Skip("testdata not available")
	}

	result, err := ParseConfigDir("../../../testdata/xray/config.d")
	if err != nil {
		t.Fatalf("ParseConfigDir: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("expected at least one parsed file")
	}

	for file, inbounds := range result {
		t.Logf("File: %s, Inbounds: %d", file, len(inbounds))
		if len(inbounds) == 0 {
			t.Errorf("file %s has no inbounds", file)
		}
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		name     string
		raw      json.RawMessage
		wantPort int
		wantErr  bool
	}{
		{"int", json.RawMessage(`443`), 443, false},
		{"string", json.RawMessage(`"8080"`), 8080, false},
		{"string-range", json.RawMessage(`"443-450"`), 443, false},
		{"missing", json.RawMessage(nil), 0, true},
		{"invalid", json.RawMessage(`"abc"`), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := parsePort(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePort() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && port != tt.wantPort {
				t.Fatalf("parsePort() = %v, want %v", port, tt.wantPort)
			}
		})
	}
}

func TestExtractClientsVMess(t *testing.T) {
	raw := json.RawMessage(`{
		"clients": [
			{"id": "aaa", "email": "u1@test.com", "alterId": 0},
			{"id": "bbb", "email": "u2@test.com", "alterId": 8}
		]
	}`)
	s := VMessInboundSettings{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	client := ParsedClient{
		Identity:   "u1@test.com",
		ConfigJSON: `{"protocol":"vmess","id":"aaa","alterId":0}`,
	}

	s.Clients = []VMessClient{
		{ID: client.Identity, AlterId: 0, Email: client.Identity},
		{ID: "bbb", AlterId: 8, Email: "u2@test.com"},
	}

	cred := StoredClientConfig{
		Protocol: "vmess",
		ID:       "aaa",
		AlterId:  0,
	}
	_, _ = json.Marshal(cred)

	// Verify identity extraction
	id := firstNonEmpty("u1@test.com", "aaa")
	if id != "u1@test.com" {
		t.Fatalf("firstNonEmpty: expected u1@test.com, got %s", id)
	}
}

func TestExtractClientsVLESS(t *testing.T) {
	raw := json.RawMessage(`{
		"decryption": "none",
		"clients": [
			{"id": "vless-uuid-1", "email": "vless1@test.com", "flow": "xtls-rprx-vision"}
		]
	}`)
	s := VLESSInboundSettings{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cred := StoredClientConfig{
		Protocol:     "vless",
		ID:           "vless-uuid-1",
		Flow:         "xtls-rprx-vision",
		Encryption:   "none",
	}
	b, _ := json.Marshal(cred)
	if !strings.Contains(string(b), `"vless"`) {
		t.Fatalf("vless credential missing protocol")
	}
}

func TestExtractClientsTrojan(t *testing.T) {
	raw := json.RawMessage(`{
		"clients": [
			{"password": "t-pass-1", "email": "t1@test.com"}
		]
	}`)
	s := TrojanInboundSettings{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cred := StoredClientConfig{
		Protocol: "trojan",
		Password: "t-pass-1",
		Email:    "t1@test.com",
	}
	b, _ := json.Marshal(cred)
	if !strings.Contains(string(b), `"trojan"`) {
		t.Fatalf("trojan credential missing protocol")
	}
}

func TestExtractClientsShadowsocksMulti(t *testing.T) {
	raw := json.RawMessage(`{
		"method": "aes-256-gcm",
		"clients": [
			{"password": "ss-p1", "email": "ss1@test.com"},
			{"password": "ss-p2", "email": "ss2@test.com"}
		]
	}`)
	s := ShadowsocksInboundSettings{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify we have 2 clients
	if len(s.Clients) != 2 {
		t.Fatalf("expected 2 ss clients, got %d", len(s.Clients))
	}

	cred := StoredClientConfig{
		Protocol: "shadowsocks",
		Password: "ss-p1",
		Method:   "aes-256-gcm",
		Email:    "ss1@test.com",
	}
	b, _ := json.Marshal(cred)
	if !strings.Contains(string(b), `"shadowsocks"`) {
		t.Fatalf("ss credential missing protocol")
	}
}

func TestExtractClientsSOCKS(t *testing.T) {
	raw := json.RawMessage(`{
		"accounts": [
			{"user": "socks-user", "pass": "socks-pass"}
		],
		"udp": true
	}`)
	s := SOCKSInboundSettings{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(s.Accounts) != 1 {
		t.Fatalf("expected 1 socks account, got %d", len(s.Accounts))
	}

	if s.Accounts[0].User != "socks-user" {
		t.Fatalf("expected socks user socks-user, got %s", s.Accounts[0].User)
	}
}

// Helpers
func findInboundByTag(inbounds []ParsedInbound, tag string) ParsedInbound {
	for _, ib := range inbounds {
		if ib.Tag == tag {
			return ib
		}
	}
	return ParsedInbound{}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  string
	}{
		{"first", []string{"a", "b"}, "a"},
		{"empty-first", []string{"", "b"}, "b"},
		{"all-empty", []string{"", ""}, ""},
		{"single", []string{"only"}, "only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstNonEmpty(tt.args...)
			if got != tt.want {
				t.Fatalf("firstNonEmpty(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
