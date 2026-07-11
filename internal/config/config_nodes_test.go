package config

import (
	"os"
	"path/filepath"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestResolvedNodes_ImplicitLocalWhenEmpty(t *testing.T) {
	cfg := &Config{
		ServerHost:        "eu.example.com",
		XrayAPIAddr:       "127.0.0.1:10085",
		APIUserInboundTag: "vless-reality-in",
	}
	nodes := cfg.ResolvedNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 implicit node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Name != "local" {
		t.Errorf("implicit node name: got %q, want local", n.Name)
	}
	if n.APIAddr != "127.0.0.1:10085" {
		t.Errorf("APIAddr: got %q", n.APIAddr)
	}
	if n.InboundTag != "vless-reality-in" {
		t.Errorf("InboundTag: got %q", n.InboundTag)
	}
	if n.PublicHost != "eu.example.com" {
		t.Errorf("PublicHost: got %q", n.PublicHost)
	}
	if !n.IsEnabled() {
		t.Error("implicit node should be enabled")
	}
	if n.DeployMode() != "grpc" {
		t.Errorf("DeployMode default: got %q, want grpc", n.DeployMode())
	}
}

func TestResolvedNodes_ExplicitPassthrough(t *testing.T) {
	cfg := &Config{
		ServerHost: "eu.example.com",
		Nodes: []NodeConfig{
			{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-reality-in", PublicHost: "eu1.example.com", PublicPort: 443},
			{Name: "eu-2", APIAddr: "10.7.0.2:10085", InboundTag: "vless-reality-in", PublicHost: "eu2.example.com", PublicPort: 443, Enabled: boolPtr(false)},
		},
	}
	nodes := cfg.ResolvedNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "eu-1" || nodes[1].Name != "eu-2" {
		t.Errorf("unexpected node names: %q, %q", nodes[0].Name, nodes[1].Name)
	}
	if !nodes[0].IsEnabled() {
		t.Error("eu-1 should be enabled")
	}
	if nodes[1].IsEnabled() {
		t.Error("eu-2 explicitly disabled should not be enabled")
	}
}

func TestValidateNodes(t *testing.T) {
	cases := []struct {
		name    string
		nodes   []NodeConfig
		wantErr bool
	}{
		{
			name:    "empty is single-node, valid",
			nodes:   nil,
			wantErr: false,
		},
		{
			name:    "private grpc ok",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "10.7.0.1:10085"}},
			wantErr: false,
		},
		{
			name:    "loopback grpc ok",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "127.0.0.1:10085"}},
			wantErr: false,
		},
		{
			name:    "empty name rejected",
			nodes:   []NodeConfig{{Name: "", APIAddr: "10.7.0.1:10085"}},
			wantErr: true,
		},
		{
			name: "duplicate name rejected",
			nodes: []NodeConfig{
				{Name: "eu-1", APIAddr: "10.7.0.1:10085"},
				{Name: "eu-1", APIAddr: "10.7.0.2:10085"},
			},
			wantErr: true,
		},
		{
			name:    "empty api_addr rejected",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: ""}},
			wantErr: true,
		},
		{
			name:    "public grpc without flag rejected",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "203.0.113.5:10085"}},
			wantErr: true,
		},
		{
			name:    "public grpc with allow flag ok",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "203.0.113.5:10085", AllowPublicGRPC: true}},
			wantErr: false,
		},
		{
			name:    "invalid deploy mode rejected",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "10.7.0.1:10085", Deploy: &NodeDeploy{Mode: "carrier-pigeon"}}},
			wantErr: true,
		},
		{
			name:    "public addr with ssh_rsync mode ok (no grpc footgun)",
			nodes:   []NodeConfig{{Name: "eu-1", APIAddr: "203.0.113.5:22", Deploy: &NodeDeploy{Mode: "ssh_rsync"}}},
			wantErr: false,
		},
		{
			name: "public grpc guarded by mTLS ok (tls satisfies footgun guard)",
			nodes: []NodeConfig{{Name: "eu-1", APIAddr: "203.0.113.5:10085",
				TLS: &NodeTLS{CACert: "/e/ca.pem", ClientCert: "/e/c.pem", ClientKey: "/e/c.key"}}},
			wantErr: false,
		},
		{
			name: "tls block missing ca_cert rejected",
			nodes: []NodeConfig{{Name: "eu-1", APIAddr: "10.7.0.1:10085",
				TLS: &NodeTLS{ClientCert: "/e/c.pem", ClientKey: "/e/c.key"}}},
			wantErr: true,
		},
		{
			name: "tls block missing client_key rejected",
			nodes: []NodeConfig{{Name: "eu-1", APIAddr: "203.0.113.5:10085",
				TLS: &NodeTLS{CACert: "/e/ca.pem", ClientCert: "/e/c.pem"}}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNodes(tc.nodes)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_RejectsInvalidNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Public grpc api_addr without allow_public_grpc must fail at load.
	body := `{"server_host":"eu.example.com","nodes":[{"name":"eu-1","api_addr":"203.0.113.5:10085","inbound_tag":"vless-reality-in","public_host":"eu1.example.com","public_port":443}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to reject public grpc node, got nil error")
	}
}

func TestLoad_AcceptsValidNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"server_host":"eu.example.com","nodes":[{"name":"eu-1","api_addr":"10.7.0.1:10085","inbound_tag":"vless-reality-in","public_host":"eu1.example.com","public_port":443}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Name != "eu-1" {
		t.Errorf("nodes not parsed: %+v", cfg.Nodes)
	}
}
