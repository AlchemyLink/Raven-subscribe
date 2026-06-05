package xray

import (
	"encoding/json"
	"strings"
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 31080, 31081, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	_, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	_, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	_, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, globalRoutes, "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
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

// TestGenerateClientConfigVLESSTestpre verifies that testpre from StoredClientConfig
// is propagated into the VLESS outbound user entry (for v2 inbounds).
func TestGenerateClientConfigVLESSTestpre(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}

	// StoredClientConfig with testpre=2 (as stored after parsing a v2 inbound)
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid-v2","flow":"xtls-rprx-vision","encryption":"none","testpre":2}`,
			},
			InboundTag:      "vless-reality-v2",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-reality-v2","protocol":"vless","port":443,"settings":{"testpre":2,"decryption":"none","clients":[{"id":"uuid-v2","email":"user@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"destination.com"}}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	if len(cfg.Outbounds) == 0 {
		t.Fatal("expected at least one outbound")
	}

	var vlessSettings VLESSOutboundSettings
	if err := json.Unmarshal(cfg.Outbounds[0].Settings, &vlessSettings); err != nil {
		t.Fatalf("unmarshal VLESS settings: %v", err)
	}
	if len(vlessSettings.Vnext) != 1 || len(vlessSettings.Vnext[0].Users) != 1 {
		t.Fatal("expected 1 vnext with 1 user")
	}

	u := vlessSettings.Vnext[0].Users[0]
	if u.Testpre != 2 {
		t.Fatalf("expected Testpre=2 in outbound user, got %d", u.Testpre)
	}
}

// TestGenerateClientConfigVLESSTestpreZeroOmitted verifies that testpre=0 (legacy inbound)
// does not appear in the outbound JSON.
func TestGenerateClientConfigVLESSTestpreZeroOmitted(t *testing.T) {
	serverHost := "example.com"
	user := models.User{Username: "testuser"}

	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid-legacy","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-reality",
			InboundProtocol: "vless",
			InboundPort:     443,
			InboundRaw:      `{"tag":"vless-reality","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid-legacy","email":"user@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"destination.com"}}}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}

	raw, err := json.Marshal(cfg.Outbounds[0].Settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if strings.Contains(string(raw), "testpre") {
		t.Fatalf("expected testpre omitted for legacy inbound, got: %s", raw)
	}
}

// TestGenerateClientConfigXHTTPHostFromServerNames verifies that when the server-side
// xhttpSettings has no "host" field and realitySettings uses serverNames[] (array),
// the generated client xhttp config still gets the correct host derived from serverNames[0].
func TestGenerateClientConfigXHTTPHostFromServerNames(t *testing.T) {
	serverHost := "1.2.3.4"
	user := models.User{Username: "testuser"}
	clients := []models.UserClientFull{
		{
			UserClient: models.UserClient{
				ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
			},
			InboundTag:      "vless-xhttp-in",
			InboundProtocol: "vless",
			InboundPort:     2053,
			InboundRaw: `{
				"tag":"vless-xhttp-in","protocol":"vless","port":2053,
				"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"u@t.com","flow":"xtls-rprx-vision"}]},
				"streamSettings":{
					"network":"xhttp","security":"reality",
					"realitySettings":{
						"dest":"www.adobe.com:443",
						"serverNames":["www.adobe.com"],
						"publicKey":"testpublickey12345678901234567890123456789012",
						"shortIds":["abc123"]
					},
					"xhttpSettings":{
						"mode":"auto",
						"path":"/api/v3/data-sync",
						"scMaxPacketSize":50000
					}
				}
			}`,
		},
	}

	cfg, err := GenerateClientConfig(serverHost, nil, nil, user, clients, "", "", "", "", 0, 0, nil, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig error: %v", err)
	}
	if len(cfg.Outbounds) == 0 {
		t.Fatal("expected at least one outbound")
	}

	ob := cfg.Outbounds[0]
	if ob.StreamSettings == nil {
		t.Fatal("expected stream settings")
	}
	if ob.StreamSettings.XHTTPSettings == nil {
		t.Fatal("expected xhttp settings in outbound")
	}

	var xhttp map[string]interface{}
	if err := json.Unmarshal(ob.StreamSettings.XHTTPSettings, &xhttp); err != nil {
		t.Fatalf("unmarshal xhttp settings: %v", err)
	}

	host, ok := xhttp["host"]
	if !ok {
		t.Fatal("xhttpSettings missing 'host' — client cannot establish XHTTP+Reality connection")
	}
	if host != "www.adobe.com" {
		t.Fatalf("expected host 'www.adobe.com', got %q", host)
	}
}

// TestConvertXHTTPSettingsInjectsXMux verifies the client config gets a tuned
// xmux block by default (anti-DPI connection rotation), and that an explicit
// server-provided xmux overrides the default.
func TestConvertXHTTPSettingsInjectsXMux(t *testing.T) {
	t.Run("default injected when absent", func(t *testing.T) {
		raw := json.RawMessage(`{"mode":"packet-up","path":"/p"}`)
		out, err := convertXHTTPSettings(raw, &RealitySettings{ServerName: "addons.mozilla.org"})
		if err != nil {
			t.Fatalf("convertXHTTPSettings error: %v", err)
		}
		var cs map[string]interface{}
		if err := json.Unmarshal(out, &cs); err != nil {
			t.Fatalf("unmarshal client settings: %v", err)
		}
		xmux, ok := cs["xmux"].(map[string]interface{})
		if !ok {
			t.Fatal("client xhttpSettings missing default xmux block")
		}
		// A complete set must be emitted — once any xmux field is set, Xray drops
		// defaults for the rest, so all keys must be present.
		for _, k := range []string{"maxConcurrency", "maxConnections", "cMaxReuseTimes", "hMaxRequestTimes", "hMaxReusableSecs", "hKeepAlivePeriod"} {
			if _, ok := xmux[k]; !ok {
				t.Errorf("default xmux missing key %q", k)
			}
		}
		if xmux["maxConcurrency"] != "16-32" {
			t.Errorf("maxConcurrency: got %v, want \"16-32\"", xmux["maxConcurrency"])
		}
	})

	t.Run("server override honored", func(t *testing.T) {
		raw := json.RawMessage(`{"mode":"packet-up","path":"/p","xmux":{"maxConcurrency":"4-8"}}`)
		out, err := convertXHTTPSettings(raw, nil)
		if err != nil {
			t.Fatalf("convertXHTTPSettings error: %v", err)
		}
		var cs map[string]interface{}
		if err := json.Unmarshal(out, &cs); err != nil {
			t.Fatalf("unmarshal client settings: %v", err)
		}
		xmux, ok := cs["xmux"].(map[string]interface{})
		if !ok {
			t.Fatal("xmux missing")
		}
		if xmux["maxConcurrency"] != "4-8" {
			t.Errorf("override not honored: got %v, want \"4-8\"", xmux["maxConcurrency"])
		}
		// override replaces the default wholesale; no default keys leak in
		if _, leaked := xmux["hMaxReusableSecs"]; leaked {
			t.Error("server override should replace default xmux wholesale, default key leaked")
		}
	})
}

func TestGenerateClientConfigDNSServerFields(t *testing.T) {
	dnsServers := []interface{}{
		DNSServer{
			Address:      "77.88.8.8",
			Domains:      []string{"geosite:ru-blocked"},
			SkipFallback: true,
			ExpectIPs:    []string{"geoip:ru"},
		},
		"1.1.1.1",
		"9.9.9.9",
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

	cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"}, clients, "", "", "", "", 0, 0, dnsServers, "")
	if err != nil {
		t.Fatalf("GenerateClientConfig: %v", err)
	}

	raw, err := json.Marshal(cfg.DNS)
	if err != nil {
		t.Fatalf("marshal DNS: %v", err)
	}
	out := string(raw)

	for _, want := range []string{"skipFallback", "expectIPs", "geoip:ru", "geosite:ru-blocked", "77.88.8.8", "9.9.9.9"} {
		if !strings.Contains(out, want) {
			t.Errorf("DNS JSON missing %q, got: %s", want, out)
		}
	}
}

func TestGenerateClientConfigBlackholeResponse(t *testing.T) {
	client := models.UserClientFull{
		UserClient: models.UserClient{
			ClientConfig: `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`,
		},
		InboundTag:      "vless-1",
		InboundProtocol: "vless",
		InboundPort:     443,
		InboundRaw:      `{"tag":"vless-1","protocol":"vless","port":443,"settings":{"decryption":"none","clients":[{"id":"uuid1","email":"u@test.com","flow":"xtls-rprx-vision"}]},"streamSettings":{"network":"tcp"}}`,
	}
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"", "http"},
		{"http", "http"},
		{"HTTP", "http"},
		{"none", "none"},
		{"NONE", "none"},
		{"bogus", "http"},
	} {
		cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"}, []models.UserClientFull{client}, "", "", "", "", 0, 0, nil, tc.input)
		if err != nil {
			t.Fatalf("input=%q: %v", tc.input, err)
		}
		var found bool
		for _, ob := range cfg.Outbounds {
			if ob.Tag != "block" {
				continue
			}
			found = true
			raw, _ := json.Marshal(ob.Settings)
			got := string(raw)
			if !strings.Contains(got, `"`+tc.want+`"`) {
				t.Errorf("input=%q: want blackhole type %q, got settings: %s", tc.input, tc.want, got)
			}
		}
		if !found {
			t.Errorf("input=%q: no block outbound in config", tc.input)
		}
	}
}

func TestSortClientsXHTTPFirst(t *testing.T) {
	tcp := models.UserClientFull{
		InboundTag: "vless-reality-v2-in",
		InboundRaw: `{"streamSettings":{"network":"tcp"}}`,
	}
	xhttp := models.UserClientFull{
		InboundTag: "vless-xhttp-v2-in",
		InboundRaw: `{"streamSettings":{"network":"xhttp"}}`,
	}
	splithttp := models.UserClientFull{
		InboundTag: "legacy-splithttp-in",
		InboundRaw: `{"streamSettings":{"network":"splithttp"}}`,
	}
	noRaw := models.UserClientFull{
		InboundTag: "vless-xhttp-from-tag",
	}
	noRawTCP := models.UserClientFull{
		InboundTag: "vless-reality-tag-only",
	}

	t.Run("xhttp_promoted_above_tcp", func(t *testing.T) {
		got := sortClientsXHTTPFirst([]models.UserClientFull{tcp, xhttp})
		if got[0].InboundTag != xhttp.InboundTag {
			t.Errorf("expected xhttp first, got %q", got[0].InboundTag)
		}
	})

	t.Run("splithttp_alias_treated_as_xhttp", func(t *testing.T) {
		got := sortClientsXHTTPFirst([]models.UserClientFull{tcp, splithttp})
		if got[0].InboundTag != splithttp.InboundTag {
			t.Errorf("expected splithttp first, got %q", got[0].InboundTag)
		}
	})

	t.Run("stable_order_within_same_priority", func(t *testing.T) {
		a := models.UserClientFull{InboundTag: "vless-xhttp-a", InboundRaw: `{"streamSettings":{"network":"xhttp"}}`}
		b := models.UserClientFull{InboundTag: "vless-xhttp-b", InboundRaw: `{"streamSettings":{"network":"xhttp"}}`}
		got := sortClientsXHTTPFirst([]models.UserClientFull{a, b, tcp})
		if got[0].InboundTag != "vless-xhttp-a" || got[1].InboundTag != "vless-xhttp-b" {
			t.Errorf("expected a,b,tcp; got %q,%q,%q", got[0].InboundTag, got[1].InboundTag, got[2].InboundTag)
		}
	})

	t.Run("tag_fallback_when_inbound_raw_missing", func(t *testing.T) {
		got := sortClientsXHTTPFirst([]models.UserClientFull{noRawTCP, noRaw})
		if got[0].InboundTag != noRaw.InboundTag {
			t.Errorf("expected xhttp tag-fallback first, got %q", got[0].InboundTag)
		}
	})

	t.Run("does_not_mutate_caller_slice", func(t *testing.T) {
		input := []models.UserClientFull{tcp, xhttp}
		_ = sortClientsXHTTPFirst(input)
		if input[0].InboundTag != tcp.InboundTag {
			t.Errorf("caller slice mutated: input[0]=%q, want %q", input[0].InboundTag, tcp.InboundTag)
		}
	})
}

func TestGenerateClientConfig_XHTTPOutboundFirst(t *testing.T) {
	tcpClient := models.UserClientFull{
		InboundTag:      "vless-reality-v2-in",
		InboundProtocol: "vless",
		InboundPort:     4444,
		InboundRaw:      `{"tag":"vless-reality-v2-in","protocol":"vless","port":4444,"settings":{"clients":[{"id":"uuid-tcp","email":"u@t.com","flow":"xtls-rprx-vision"}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"destination.com"}}}`,
	}
	tcpClient.ClientConfig = `{"protocol":"vless","id":"uuid-tcp","flow":"xtls-rprx-vision","encryption":"none"}`

	xhttpClient := models.UserClientFull{
		InboundTag:      "vless-xhttp-v2-in",
		InboundProtocol: "vless",
		InboundPort:     2054,
		InboundRaw:      `{"tag":"vless-xhttp-v2-in","protocol":"vless","port":2054,"settings":{"clients":[{"id":"uuid-xhttp","email":"u@t.com"}],"decryption":"none"},"streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"serverName":"addons.mozilla.org"},"xhttpSettings":{"path":"/p"}}}`,
	}
	xhttpClient.ClientConfig = `{"protocol":"vless","id":"uuid-xhttp","encryption":"none"}`

	// DB returns tcp first, xhttp second — order before sorting
	cfg, err := GenerateClientConfig("example.com", nil, nil, models.User{Username: "u"},
		[]models.UserClientFull{tcpClient, xhttpClient}, "", "", "", "", 0, 0, nil, "http")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Find first proxy outbound (skip socks/http inbounds, pick the first
	// vless outbound in cfg.Outbounds order).
	var firstProxyTag string
	for _, ob := range cfg.Outbounds {
		if ob.Protocol == "vless" {
			firstProxyTag = ob.Tag
			break
		}
	}
	if firstProxyTag == "" {
		t.Fatal("no vless outbound in generated config")
	}
	if !strings.Contains(strings.ToLower(firstProxyTag), "xhttp") {
		t.Errorf("expected XHTTP proxy first, got tag %q. Outbounds: %v", firstProxyTag,
			outboundTags(cfg.Outbounds))
	}

	// XHTTP-primary balancer: the Selector balances only XHTTP outbounds, and
	// the non-XHTTP (Reality/TCP) outbound is demoted to FallbackTag — engaged
	// only when every XHTTP outbound is observed down (2026-Q2 TSPU regime,
	// where Reality sessions are killed within seconds on the first hop).
	if len(cfg.Routing.Balancers) != 1 {
		t.Fatalf("expected 1 balancer, got %d", len(cfg.Routing.Balancers))
	}
	b := cfg.Routing.Balancers[0]
	if len(b.Selector) != 1 {
		t.Errorf("expected exactly 1 XHTTP selector, got %v", b.Selector)
	}
	for _, sel := range b.Selector {
		if !strings.Contains(strings.ToLower(sel), "xhttp") {
			t.Errorf("balancer Selector must contain only XHTTP tags, got %v", b.Selector)
		}
	}
	if strings.Contains(strings.ToLower(b.FallbackTag), "xhttp") {
		t.Errorf("FallbackTag should be the non-XHTTP (Reality) outbound, got %q", b.FallbackTag)
	}
	if !strings.Contains(strings.ToLower(b.FallbackTag), "reality") {
		t.Errorf("FallbackTag should be the Reality outbound, got %q", b.FallbackTag)
	}
	// Observatory must probe only the XHTTP outbound(s).
	if cfg.Observatory == nil || len(cfg.Observatory.SubjectSelector) != 1 ||
		!strings.Contains(strings.ToLower(cfg.Observatory.SubjectSelector[0]), "xhttp") {
		t.Errorf("Observatory should probe only the XHTTP outbound, got %+v", cfg.Observatory)
	}
}

func outboundTags(out []Outbound) []string {
	tags := make([]string, 0, len(out))
	for _, o := range out {
		tags = append(tags, o.Tag)
	}
	return tags
}
