package xray

import (
	"encoding/json"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/models"
)

func TestGenerateClientConfig(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Outbounds) == 0 {
		t.Fatal("expected at least one outbound")
	}

	if cfg.Outbounds[0].Protocol != "vless" {
		t.Fatalf("expected vless outbound, got %s", cfg.Outbounds[0].Protocol)
	}

	if cfg.DNS == nil {
		t.Fatal("expected DNS config")
	}

	if len(cfg.Routing.Rules) == 0 {
		t.Fatal("expected routing rules")
	}
}

func TestGenerateClientConfigCustomInboundPorts(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 31080, 31081)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Inbounds) < 2 {
		t.Fatalf("expected at least 2 inbounds (socks, http), got %d", len(cfg.Inbounds))
	}
	var socksPort, httpPort int
	for _, ib := range cfg.Inbounds {
		if ib.Protocol == "socks" {
			socksPort = ib.Port
		}
		if ib.Protocol == "http" {
			httpPort = ib.Port
		}
	}
	if socksPort != 31080 {
		t.Fatalf("expected socks port 31080, got %d", socksPort)
	}
	if httpPort != 31081 {
		t.Fatalf("expected http port 31081, got %d", httpPort)
	}
}

func TestGenerateClientConfigMultiProxy(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vmess","id":"uuid2","alterId":0}`,
			},
			InboundTag:      "vmess-1",
			InboundProtocol: "vmess",
			InboundPort:     8443,
			InboundRaw:      `{"tag":"vmess-1","protocol":"vmess","port":8443,"settings":{"clients":[{"id":"uuid2","email":"user2@test.com","alterId":0}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "leastPing", "https://www.gstatic.com/generate_204", "30s", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Outbounds) != 4 {
		t.Fatalf("expected 4 outbounds (2 proxies + direct + block), got %d", len(cfg.Outbounds))
	}

	if len(cfg.Routing.Balancers) != 1 {
		t.Fatalf("expected 1 balancer, got %d", len(cfg.Routing.Balancers))
	}

	balancer := cfg.Routing.Balancers[0]
	if balancer.Tag != "proxy-balance" {
		t.Fatalf("expected balancer tag proxy-balance, got %s", balancer.Tag)
	}

	if len(balancer.Selector) != 2 {
		t.Fatalf("expected 2 proxy selectors, got %d", len(balancer.Selector))
	}

	if cfg.Observatory == nil {
		t.Fatal("expected Observatory config for leastPing strategy")
	}
	if cfg.Observatory.ProbeInterval != "30s" {
		t.Fatalf("expected probe interval 30s, got %s", cfg.Observatory.ProbeInterval)
	}
}

func TestGenerateClientConfigSingleProxy(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Routing.Balancers) != 0 {
		t.Fatalf("expected 0 balancers for single proxy, got %d", len(cfg.Routing.Balancers))
	}

	hasDirect := false
	for _, ob := range cfg.Outbounds {
		if ob.Tag == "direct" {
			hasDirect = true
			break
		}
	}
	if !hasDirect {
		t.Fatal("expected direct outbound")
	}
}

func TestGenerateClientConfigInvalidClientConfig(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"invalid`, // malformed
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none"}}`,
		},
	}

	_, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err == nil {
		t.Fatal("expected error for invalid client config, got nil")
	}
}

func TestGenerateClientConfigInvalidInboundRaw(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"invalid`, // malformed
		},
	}

	_, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err == nil {
		t.Fatal("expected error for invalid inbound raw, got nil")
	}
}

func TestGenerateClientConfigNoValidOutbounds(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"invalid`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"invalid`,
		},
	}

	_, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err == nil {
		t.Fatal("expected error for no valid outbounds, got nil")
	}
	if err.Error() != "no valid outbounds could be generated" {
		t.Fatalf("expected 'no valid outbounds could be generated', got: %v", err)
	}
}

func TestGenerateClientConfigWithGlobalRoutes(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	globalRoutes := `[{"type":"field","outboundTag":"direct","domain":["geosite:ru"]}]`

	cfg, err := GenerateClientConfig(serverHost, user, clients, globalRoutes, "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	hasGlobalRoute := false
	for _, rule := range cfg.Routing.Rules {
		if len(rule.Domain) > 0 && rule.OutboundTag == "direct" {
			hasGlobalRoute = true
			break
		}
	}
	if !hasGlobalRoute {
		t.Fatal("expected global route for geosite:ru")
	}
}

func TestGenerateClientConfigUserRoutes(t *testing.T) {
	serverHost := "example.com"
	user := models.User{
		Username:     "testuser",
		ClientRoutes: `[{"type":"field","outboundTag":"direct","domain":["geosite:private"]}]`,
	}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	hasUserRoute := false
	for _, rule := range cfg.Routing.Rules {
		if len(rule.Domain) > 0 {
			for _, domain := range rule.Domain {
				if domain == "geosite:private" {
					hasUserRoute = true
					break
				}
			}
		}
	}
	if !hasUserRoute {
		t.Fatal("expected user route for geosite:private")
	}
}

func TestGenerateClientConfigMuxEnabled(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vmess","id":"uuid1","alterId":0}`,
			},
			InboundTag:      "vmess-1",
			InboundProtocol: "vmess",
			InboundPort:     8443,
			InboundRaw:      `{"tag":"vmess-1","protocol":"vmess","port":8443,"settings":{"clients":[{"id":"uuid1","email":"user1@test.com","alterId":0}]},"streamSettings":{"network":"tcp","security":"tls"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	muxEnabled := false
	for _, ob := range cfg.Outbounds {
		if ob.Mux != nil && ob.Mux.Enabled {
			muxEnabled = true
			break
		}
	}
	if !muxEnabled {
		t.Fatal("expected Mux enabled for VMess")
	}
}

func TestGenerateClientConfigNoMuxForREALITY(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"example.com"}}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	for _, ob := range cfg.Outbounds {
		if ob.Mux != nil && ob.Mux.Enabled {
			t.Fatal("Mux should be disabled for VLESS-REALITY")
		}
	}
}

func TestGenerateClientConfigShadowsocks(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"shadowsocks","password":"sspass","method":"aes-256-gcm"}`,
			},
			InboundTag:      "ss-1",
			InboundProtocol: "shadowsocks",
			InboundPort:     8388,
			InboundRaw:      `{"tag":"ss-1","protocol":"shadowsocks","port":8388,"settings":{"method":"aes-256-gcm","clients":[{"password":"sspass","email":"user1@test.com"}]}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Outbounds) < 2 {
		t.Fatalf("expected at least 2 outbounds, got %d", len(cfg.Outbounds))
	}

	ssOb := cfg.Outbounds[0]
	if ssOb.Protocol != "shadowsocks" {
		t.Fatalf("expected shadowsocks protocol, got %s", ssOb.Protocol)
	}

	var ssSettings ShadowsocksOutboundSettings
	if err := json.Unmarshal(ssOb.Settings, &ssSettings); err != nil {
		t.Fatalf("unmarshal SS settings: %v", err)
	}

	if len(ssSettings.Servers) != 1 {
		t.Fatalf("expected 1 SS server, got %d", len(ssSettings.Servers))
	}

	if ssSettings.Servers[0].Password != "sspass" {
		t.Fatalf("expected password sspass, got %s", ssSettings.Servers[0].Password)
	}

	if ssSettings.Servers[0].Method != "aes-256-gcm" {
		t.Fatalf("expected method aes-256-gcm, got %s", ssSettings.Servers[0].Method)
	}
}

func TestGenerateClientConfigTrojan(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"trojan","password":"trojanpass"}`,
			},
			InboundTag:      "trojan-1",
			InboundProtocol: "trojan",
			InboundPort:     443,
			InboundRaw:      `{"tag":"trojan-1","protocol":"trojan","port":443,"settings":{"clients":[{"password":"trojanpass","email":"user1@test.com"}]}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	trojanOb := cfg.Outbounds[0]
	if trojanOb.Protocol != "trojan" {
		t.Fatalf("expected trojan protocol, got %s", trojanOb.Protocol)
	}

	var trojanSettings TrojanOutboundSettings
	if err := json.Unmarshal(trojanOb.Settings, &trojanSettings); err != nil {
		t.Fatalf("unmarshal trojan settings: %v", err)
	}

	if len(trojanSettings.Servers) != 1 {
		t.Fatalf("expected 1 trojan server, got %d", len(trojanSettings.Servers))
	}

	if trojanSettings.Servers[0].Password != "trojanpass" {
		t.Fatalf("expected password trojanpass, got %s", trojanSettings.Servers[0].Password)
	}
}

func TestGenerateClientConfigSOCKS(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"socks","user":"socksuser","password":"sockspass"}`,
			},
			InboundTag:      "socks-1",
			InboundProtocol: "socks",
			InboundPort:     1080,
			InboundRaw:      `{"tag":"socks-1","protocol":"socks","port":1080,"settings":{"accounts":[{"user":"socksuser","pass":"sockspass"}]}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	socksOb := cfg.Outbounds[0]
	if socksOb.Protocol != "socks" {
		t.Fatalf("expected socks protocol, got %s", socksOb.Protocol)
	}

	var socksSettings SOCKSOutboundSettings
	if err := json.Unmarshal(socksOb.Settings, &socksSettings); err != nil {
		t.Fatalf("unmarshal socks settings: %v", err)
	}

	if len(socksSettings.Servers) != 1 {
		t.Fatalf("expected 1 socks server, got %d", len(socksSettings.Servers))
	}

	if len(socksSettings.Servers[0].Users) != 1 {
		t.Fatalf("expected 1 socks user, got %d", len(socksSettings.Servers[0].Users))
	}

	if socksSettings.Servers[0].Users[0].User != "socksuser" {
		t.Fatalf("expected socks user socksuser, got %s", socksSettings.Servers[0].Users[0].User)
	}
}

func TestGenerateClientConfigMarshalJSON(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent error: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}

	var cfg2 ClientConfig
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("unmarshal back error: %v", err)
	}

	if len(cfg2.Outbounds) != len(cfg.Outbounds) {
		t.Fatalf("expected %d outbounds after marshal/unmarshal, got %d", len(cfg.Outbounds), len(cfg2.Outbounds))
	}
}

func TestGenerateClientConfigPortParsing(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-1",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"user1@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, user, clients, "", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	var vlessSettings VLESSOutboundSettings
	if err := json.Unmarshal(cfg.Outbounds[0].Settings, &vlessSettings); err != nil {
		t.Fatalf("unmarshal vless settings: %v", err)
	}

	if len(vlessSettings.Vnext) != 1 {
		t.Fatalf("expected 1 vless server, got %d", len(vlessSettings.Vnext))
	}

	if vlessSettings.Vnext[0].Port != 443 {
		t.Fatalf("expected port 443, got %d", vlessSettings.Vnext[0].Port)
	}
}
