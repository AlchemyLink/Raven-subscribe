package xray

import (
	"encoding/json"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/models"
)

// vnextEndpoint extracts the address and port from a vless/vmess-style outbound.
func vnextEndpoint(t *testing.T, ob Outbound) (string, int) {
	t.Helper()
	var s struct {
		Vnext []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"vnext"`
	}
	if err := json.Unmarshal(ob.Settings, &s); err != nil {
		t.Fatalf("unmarshal outbound settings: %v", err)
	}
	if len(s.Vnext) == 0 {
		t.Fatalf("outbound %s has no vnext server", ob.Tag)
	}
	return s.Vnext[0].Address, s.Vnext[0].Port
}

func vlessClient(tag string) models.UserClientFull {
	return models.UserClientFull{
		UserClient:      models.UserClient{ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`},
		InboundTag:      tag,
		InboundProtocol: "vless",
		InboundPort:     443,
		InboundRaw:      `{"tag":"` + tag + `","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"u@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
	}
}

// Golden N=1: a client with no node hints must resolve to server_host:inbound_port,
// exactly as before multi-node existed.
func TestGenerate_NoNodeHint_UsesServerHost(t *testing.T) {
	clients := []models.UserClientFull{vlessClient("vless-in")}
	cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"}, clients, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig: %v", err)
	}
	addr, port := vnextEndpoint(t, cfg.Outbounds[0])
	if addr != "example.com" {
		t.Errorf("address: got %q, want example.com", addr)
	}
	if port != 443 {
		t.Errorf("port: got %d, want 443", port)
	}
}

// A node-expanded client points its outbound at the node's public endpoint,
// overriding server_host.
func TestGenerate_NodeOverride_UsesNodeEndpoint(t *testing.T) {
	c := vlessClient("vless-in")
	c.NodeName = "eu-2"
	c.NodeHost = "eu2.example.com"
	c.NodePort = 8443
	cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"}, []models.UserClientFull{c}, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig: %v", err)
	}
	addr, port := vnextEndpoint(t, cfg.Outbounds[0])
	if addr != "eu2.example.com" || port != 8443 {
		t.Errorf("node endpoint: got %s:%d, want eu2.example.com:8443", addr, port)
	}
}

// Two nodes serving the same inbound produce two distinct proxy outbounds
// gathered under one balancer.
func TestGenerate_TwoNodes_BalancedOutbounds(t *testing.T) {
	a := vlessClient("vless-in")
	a.NodeName, a.NodeHost, a.NodePort = "eu-1", "eu1.example.com", 443
	b := vlessClient("vless-in")
	b.NodeName, b.NodeHost, b.NodePort = "eu-2", "eu2.example.com", 443

	cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"}, []models.UserClientFull{a, b}, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig: %v", err)
	}

	hosts := map[string]bool{}
	proxyCount := 0
	for _, ob := range cfg.Outbounds {
		if ob.Protocol != "vless" {
			continue
		}
		proxyCount++
		addr, _ := vnextEndpoint(t, ob)
		hosts[addr] = true
	}
	if proxyCount != 2 {
		t.Fatalf("expected 2 proxy outbounds, got %d", proxyCount)
	}
	if !hosts["eu1.example.com"] || !hosts["eu2.example.com"] {
		t.Errorf("expected both node hosts, got %v", hosts)
	}
	if len(cfg.Routing.Balancers) != 1 {
		t.Fatalf("expected 1 balancer over the node outbounds, got %d", len(cfg.Routing.Balancers))
	}
	if cfg.Routing.Balancers[0].Tag != "proxy-balance" {
		t.Errorf("balancer tag: got %q", cfg.Routing.Balancers[0].Tag)
	}
}
