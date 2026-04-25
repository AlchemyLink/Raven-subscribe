// Package xray provides parsing and client config generation for xray-core configurations.
package xray

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/alchemylink/raven-subscribe/internal/models"

	"golang.org/x/crypto/curve25519"
)

// GenerateClientConfig produces a complete xray client JSON config for a user.
// socksPort and httpPort are the local proxy ports in the generated config; 0 = use default (2080, 1081).
// inboundHosts overrides serverHost per inbound tag; falls back to serverHost when tag is not listed.
// inboundPorts overrides the port per inbound tag; falls back to the inbound's own port when tag is not listed.
func GenerateClientConfig(serverHost string, inboundHosts map[string]string, inboundPorts map[string]int, user models.User, clients []models.UserClientFull, globalRoutesJSON string, balancerStrategy string, balancerProbeURL string, balancerProbeInterval string, socksPort, httpPort int, dnsServers []interface{}, blackholeResponse string) (*ClientConfig, error) {
	if socksPort <= 0 {
		socksPort = 2080
	}
	if httpPort <= 0 {
		httpPort = 1081
	}
	dns := defaultDNS()
	if len(dnsServers) > 0 {
		dns = &DNSConfig{Servers: dnsServers}
	}
	cfg := &ClientConfig{
		Log: &LogConfig{LogLevel: "warning"},
		DNS: dns,
		Inbounds: []Inbound{
			localSOCKS(socksPort),
			localHTTP(httpPort),
		},
		Routing: defaultRouting(),
	}
	blackholeResp := strings.ToLower(strings.TrimSpace(blackholeResponse))
	if blackholeResp != "none" {
		blackholeResp = "http"
	}
	// Priority: user rules > global rules > defaults
	applyUserRoutes(cfg, globalRoutesJSON)
	applyUserRoutes(cfg, user.ClientRoutes)

	// Build outbounds from each user client
	var proxyTags []string
	for i, uc := range clients {
		host := serverHost
		if h, ok := inboundHosts[uc.InboundTag]; ok && strings.TrimSpace(h) != "" {
			host = h
		}
		if p, ok := inboundPorts[uc.InboundTag]; ok && p > 0 {
			uc.InboundPort = p
		}
		ob, err := buildOutbound(host, uc, i)
		if err != nil {
			log.Printf("WARN: build outbound for inbound %s: %v", uc.InboundTag, err)
			continue
		}
		cfg.Outbounds = append(cfg.Outbounds, *ob)
		proxyTags = append(proxyTags, ob.Tag)
	}

	if len(cfg.Outbounds) == 0 {
		return nil, fmt.Errorf("no valid outbounds could be generated")
	}

	// Add system outbounds
	cfg.Outbounds = append(cfg.Outbounds,
		freedomOutbound(),
		blackholeOutbound(blackholeResp),
	)

	// Update routing: use balancer if multiple proxies, single proxy otherwise
	if len(proxyTags) > 1 {
		strategy := normalizeBalancerStrategy(balancerStrategy)
		if strategy == "" {
			strategy = "leastPing"
		}
		// Xray load balancing is configured in routing.balancers, not as an outbound protocol.
		cfg.Routing.Balancers = append(cfg.Routing.Balancers, Balancer{
			Tag:      "proxy-balance",
			Selector: proxyTags,
			Strategy: &BalancerStrategy{Type: strategy},
			// If balancer cannot pick a healthy outbound yet, use first proxy as fallback.
			FallbackTag: proxyTags[0],
		})
		if strategy == "leastPing" || strategy == "leastLoad" {
			cfg.Observatory = &ObservatoryConfig{
				SubjectSelector: proxyTags,
				ProbeURL:        firstNonEmpty(strings.TrimSpace(balancerProbeURL), "https://www.gstatic.com/generate_204"),
				ProbeInterval:   firstNonEmpty(strings.TrimSpace(balancerProbeInterval), "30s"),
			}
		}
		resolveProxyRouteTargets(cfg.Routing, true, "proxy-balance")
		cfg.Routing.Rules = append(cfg.Routing.Rules, RoutingRule{
			Type:        "field",
			BalancerTag: "proxy-balance",
			Port:        "0-65535",
		})
	} else if len(proxyTags) == 1 {
		resolveProxyRouteTargets(cfg.Routing, false, proxyTags[0])
		// Single proxy: route directly
		cfg.Routing.Rules = append(cfg.Routing.Rules, RoutingRule{
			Type:        "field",
			OutboundTag: proxyTags[0],
			Port:        "0-65535",
		})
	}

	return cfg, nil
}

func normalizeBalancerStrategy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "random":
		return "random"
	case "roundrobin":
		return "roundRobin"
	case "leastping":
		return "leastPing"
	case "leastload":
		return "leastLoad"
	default:
		return ""
	}
}

func applyUserRoutes(cfg *ClientConfig, routesJSON string) {
	routesJSON = strings.TrimSpace(routesJSON)
	if routesJSON == "" || routesJSON == "null" {
		return
	}
	var userRules []models.UserRouteRule
	if err := json.Unmarshal([]byte(routesJSON), &userRules); err != nil {
		return
	}
	if len(userRules) == 0 {
		return
	}

	custom := make([]RoutingRule, 0, len(userRules))
	for _, r := range userRules {
		if !isAllowedOutboundTag(r.OutboundTag) {
			continue
		}
		rr := RoutingRule{
			Type:        firstNonEmpty(strings.TrimSpace(r.Type), "field"),
			OutboundTag: strings.TrimSpace(r.OutboundTag),
			Domain:      r.Domain,
			IP:          r.IP,
			Network:     r.Network,
			Port:        r.Port,
			Protocol:    r.Protocol,
			InboundTag:  r.InboundTag,
		}
		// Xray rejects rules with no effective fields.
		if len(rr.Domain) == 0 && len(rr.IP) == 0 && rr.Network == "" && rr.Port == "" && len(rr.Protocol) == 0 && len(rr.InboundTag) == 0 {
			continue
		}
		custom = append(custom, rr)
	}
	if len(custom) == 0 {
		return
	}
	cfg.Routing.Rules = append(custom, cfg.Routing.Rules...)
}

func isAllowedOutboundTag(tag string) bool {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "direct", "proxy", "block":
		return true
	default:
		return false
	}
}

func resolveProxyRouteTargets(r *Routing, useBalancer bool, target string) {
	if r == nil || target == "" {
		return
	}
	for i := range r.Rules {
		if strings.ToLower(strings.TrimSpace(r.Rules[i].OutboundTag)) != "proxy" {
			continue
		}
		if useBalancer {
			r.Rules[i].OutboundTag = ""
			r.Rules[i].BalancerTag = target
		} else {
			r.Rules[i].OutboundTag = target
			r.Rules[i].BalancerTag = ""
		}
	}
}

func buildOutbound(serverHost string, uc models.UserClientFull, index int) (*Outbound, error) {
	var cred StoredClientConfig
	if err := json.Unmarshal([]byte(uc.ClientConfig), &cred); err != nil {
		return nil, fmt.Errorf("parse stored config: %w", err)
	}

	tag := fmt.Sprintf("%s-%d", sanitizeTag(uc.InboundTag), index)
	proto := strings.ToLower(uc.InboundProtocol)

	// Parse the server-side inbound raw JSON to get stream settings.
	// Hysteria2 uses sing-box format — no Xray ServerInbound parsing needed.
	var si ServerInbound
	if proto != "hysteria2" {
		if err := json.Unmarshal([]byte(uc.InboundRaw), &si); err != nil {
			return nil, fmt.Errorf("parse inbound raw: %w", err)
		}
	}

	var (
		settings json.RawMessage
		err      error
	)

	switch proto {
	case "vmess":
		settings, err = buildVMessSettings(serverHost, uc.InboundPort, cred)
	case "vless":
		settings, err = buildVLESSSettings(serverHost, uc.InboundPort, cred)
	case "trojan":
		settings, err = buildTrojanSettings(serverHost, uc.InboundPort, cred)
	case "shadowsocks":
		settings, err = buildShadowsocksSettings(serverHost, uc.InboundPort, cred)
	case "socks":
		settings, err = buildSOCKSSettings(serverHost, uc.InboundPort, cred)
	case "hysteria2":
		settings, err = buildHysteria2Settings(serverHost, uc.InboundPort, tag, cred)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", proto)
	}
	if err != nil {
		return nil, err
	}

	// Hysteria2 uses sing-box format — no Xray stream settings.
	var clientStream *StreamSettings
	if proto != "hysteria2" {
		clientStream, err = convertStreamSettings(si.StreamSettings, serverHost)
		if err != nil {
			return nil, fmt.Errorf("convert stream settings: %w", err)
		}
	}

	ob := &Outbound{
		Tag:            tag,
		Protocol:       proto,
		Settings:       settings,
		StreamSettings: clientStream,
	}

	// Enable Mux for compatible protocols (not XTLS/REALITY / VLESS Encryption)
	if shouldUseMux(proto, cred.Flow, cred.Encryption, clientStream) {
		ob.Mux = &MuxConfig{Enabled: true, Concurrency: 8}
	}

	return ob, nil
}

func buildVMessSettings(host string, port int, cred StoredClientConfig) (json.RawMessage, error) {
	s := VMessOutboundSettings{
		Vnext: []VMessServer{{
			Address: host,
			Port:    port,
			Users: []VMessUser{{
				ID:       cred.ID,
				AlterId:  cred.AlterId,
				Security: "auto",
			}},
		}},
	}
	return json.Marshal(s)
}

func buildVLESSSettings(host string, port int, cred StoredClientConfig) (json.RawMessage, error) {
	encryption := strings.TrimSpace(cred.Encryption)
	if encryption == "" {
		// VLESS outbound requires explicit "none" when no encryption is used.
		encryption = "none"
	}
	user := VLESSUser{
		ID:         cred.ID,
		Flow:       cred.Flow,
		Encryption: encryption,
		Testpre:    cred.Testpre,
	}
	if cred.Email != "" {
		user.Email = cred.Email
	}
	s := VLESSOutboundSettings{
		Vnext: []VLESSServer{{
			Address: host,
			Port:    port,
			Users:   []VLESSUser{user},
		}},
	}
	return json.Marshal(s)
}

func buildTrojanSettings(host string, port int, cred StoredClientConfig) (json.RawMessage, error) {
	s := TrojanOutboundSettings{
		Servers: []TrojanServer{{
			Address:  host,
			Port:     port,
			Password: cred.Password,
		}},
	}
	return json.Marshal(s)
}

func buildShadowsocksSettings(host string, port int, cred StoredClientConfig) (json.RawMessage, error) {
	method := cred.Method
	if method == "" {
		method = "aes-256-gcm"
	}
	s := ShadowsocksOutboundSettings{
		Servers: []ShadowsocksServer{{
			Address:  host,
			Port:     port,
			Method:   method,
			Password: cred.Password,
		}},
	}
	return json.Marshal(s)
}

func buildHysteria2Settings(host string, port int, tag string, cred StoredClientConfig) (json.RawMessage, error) {
	serverName := cred.ServerName
	if serverName == "" {
		serverName = host
	}
	s := Hysteria2OutboundSettings{
		Type:       "hysteria2",
		Tag:        tag,
		Server:     host,
		ServerPort: port,
		Password:   cred.Password,
		UpMbps:     cred.UpMbps,
		DownMbps:   cred.DownMbps,
		TLS: &Hysteria2TLS{
			Enabled:    true,
			ServerName: serverName,
		},
	}
	if cred.ObfsType != "" {
		s.Obfs = &Hysteria2Obfs{
			Type:     cred.ObfsType,
			Password: cred.ObfsPassword,
		}
	}
	// #nosec G117 -- password-like fields are expected in stored protocol credentials.
	return json.Marshal(s)
}

func buildSOCKSSettings(host string, port int, cred StoredClientConfig) (json.RawMessage, error) {
	srv := SOCKSServer{Address: host, Port: port}
	if cred.User != "" {
		srv.Users = []SOCKSUser{{User: cred.User, Pass: cred.Password}}
	}
	s := SOCKSOutboundSettings{Servers: []SOCKSServer{srv}}
	return json.Marshal(s)
}

// convertStreamSettings translates server-side stream settings into client-side
func convertStreamSettings(ss *StreamSettings, serverHost string) (*StreamSettings, error) {
	if ss == nil {
		return nil, nil
	}

	client := &StreamSettings{
		Network:  ss.Network,
		Security: ss.Security,
	}

	// Transport-specific settings (mostly pass-through, client-safe)
	client.WSSettings = ss.WSSettings
	client.GRPCSettings = ss.GRPCSettings
	client.HTTPUpgradeSettings = ss.HTTPUpgradeSettings
	// For xhttp, filter out server-only fields and ensure host is present
	if ss.XHTTPSettings != nil {
		xHTTPSettings, err := convertXHTTPSettings(ss.XHTTPSettings, ss.RealitySettings)
		if err != nil {
			return nil, fmt.Errorf("convert xhttp settings: %w", err)
		}
		client.XHTTPSettings = xHTTPSettings
	}
	client.KCPSettings = ss.KCPSettings
	client.QUICSettings = ss.QUICSettings

	if ss.TCPSettings != nil {
		client.TCPSettings = ss.TCPSettings
	}
	if ss.HTTPSettings != nil {
		client.HTTPSettings = ss.HTTPSettings
	}

	// Security layer conversion
	switch strings.ToLower(ss.Security) {
	case "tls":
		client.TLSSettings = convertTLS(ss.TLSSettings, serverHost)
	case "reality":
		rs, err := convertReality(ss.RealitySettings, serverHost)
		if err != nil {
			return nil, err
		}
		client.RealitySettings = rs
	}

	return client, nil
}

func convertTLS(tls *TLSSettings, serverHost string) *TLSSettings {
	if tls == nil {
		return &TLSSettings{ServerName: serverHost}
	}
	return &TLSSettings{
		ServerName:    tls.ServerName,
		Fingerprint:   firstNonEmpty(tls.Fingerprint, "chrome"),
		ALPN:          tls.ALPN,
		AllowInsecure: tls.AllowInsecure,
		// Strip Certificates — client doesn't need server's certs
	}
}

func convertReality(rs *RealitySettings, _ string) (*RealitySettings, error) {
	if rs == nil {
		return nil, fmt.Errorf("REALITY stream settings missing")
	}

	publicKey := rs.PublicKey
	if publicKey == "" && rs.PrivateKey != "" {
		var err error
		publicKey, err = derivePublicKey(rs.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("derive REALITY public key: %w", err)
		}
	}

	serverName := rs.ServerName
	if serverName == "" && len(rs.ServerNames) > 0 {
		serverName = rs.ServerNames[0]
	}

	shortID := rs.ShortId
	if shortID == "" && len(rs.ShortIds) > 0 {
		shortID = rs.ShortIds[0]
	}

	// MLDSA65Verify: client needs the public verification key only. Never pass MLDSA65Seed
	// (server secret) to the client. mldsa65Verify cannot be derived from mldsa65Seed without
	// ML-DSA-65 crypto; server config must include mldsa65Verify explicitly for sharing.
	mldsa65Verify := strings.TrimSpace(rs.MLDSA65Verify)

	return &RealitySettings{
		ServerName:    serverName,
		Fingerprint:   firstNonEmpty(rs.Fingerprint, "chrome"),
		PublicKey:     publicKey,
		ShortId:       shortID,
		SpiderX:       rs.SpiderX,
		MLDSA65Verify: mldsa65Verify,
	}, nil
}

// convertXHTTPSettings filters xhttp settings to include only client-side fields
// If host is missing, it uses serverName from realitySettings
func convertXHTTPSettings(raw json.RawMessage, rs *RealitySettings) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var serverSettings map[string]interface{}
	if err := json.Unmarshal(raw, &serverSettings); err != nil {
		return nil, fmt.Errorf("unmarshal xhttp settings: %w", err)
	}

	// XHTTP client settings whitelist based on https://github.com/XTLS/Xray-core/discussions/4113
	// Core fields: path, host, mode, headers
	// Optional: extra (server-provided advanced settings), xPaddingBytes, scMaxEachPostBytes, scMinPostsIntervalMs
	// Client-only (never from server): xmux, downloadSettings
	clientSettings := make(map[string]interface{})
	
	// Core fields
	if path, ok := serverSettings["path"]; ok {
		clientSettings["path"] = path
	}
	if host, ok := serverSettings["host"]; ok {
		clientSettings["host"] = host
	} else if rs != nil {
		// If host is missing, derive from realitySettings (server uses ServerNames[], client uses ServerName)
		if rs.ServerName != "" {
			clientSettings["host"] = rs.ServerName
		} else if len(rs.ServerNames) > 0 {
			clientSettings["host"] = rs.ServerNames[0]
		}
	}
	if mode, ok := serverSettings["mode"]; ok {
		clientSettings["mode"] = mode
	}
	if headers, ok := serverSettings["headers"]; ok {
		clientSettings["headers"] = headers
	}
	
	// Optional server-sharable fields
	if extra, ok := serverSettings["extra"]; ok {
		clientSettings["extra"] = extra
	}
	if xPaddingBytes, ok := serverSettings["xPaddingBytes"]; ok {
		clientSettings["xPaddingBytes"] = xPaddingBytes
	}
	if scMaxEachPostBytes, ok := serverSettings["scMaxEachPostBytes"]; ok {
		clientSettings["scMaxEachPostBytes"] = scMaxEachPostBytes
	}
	if scMinPostsIntervalMs, ok := serverSettings["scMinPostsIntervalMs"]; ok {
		clientSettings["scMinPostsIntervalMs"] = scMinPostsIntervalMs
	}

	result, err := json.Marshal(clientSettings)
	if err != nil {
		return nil, fmt.Errorf("marshal xhttp settings: %w", err)
	}
	return result, nil
}

// derivePublicKey computes X25519 public key from base64url-encoded private key
func derivePublicKey(privateKeyB64 string) (string, error) {
	privBytes, err := base64.RawURLEncoding.DecodeString(privateKeyB64)
	if err != nil {
		// Try standard base64
		privBytes, err = base64.StdEncoding.DecodeString(privateKeyB64)
		if err != nil {
			return "", fmt.Errorf("decode private key: %w", err)
		}
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("invalid private key length %d (expected 32)", len(privBytes))
	}

	pubBytes, err := curve25519.X25519(privBytes, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("X25519: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(pubBytes), nil
}

func shouldUseMux(proto, flow, encryption string, ss *StreamSettings) bool {
	// xtls-rprx-vision is fundamentally incompatible with Mux regardless of security layer
	if flow == "xtls-rprx-vision" {
		return false
	}
	// VLESS Encryption (outbound encryption != none) must not use Mux
	if strings.TrimSpace(encryption) != "" && strings.TrimSpace(encryption) != "none" {
		return false
	}
	if proto == "vless" || proto == "trojan" {
		if ss != nil && ss.Security == "reality" {
			return false
		}
	}
	// No Mux for QUIC, KCP, xhttp (SplitHTTP)
	if ss != nil && (ss.Network == "quic" || ss.Network == "kcp" || ss.Network == "xhttp") {
		return false
	}
	return proto == "vmess" || proto == "vless"
}

// ─── Default client-side config pieces ───────────────────────────────────────

func localSOCKS(port int) Inbound {
	raw, _ := json.Marshal(map[string]interface{}{
		"auth": "noauth",
		"udp":  true,
	})
	return Inbound{
		Tag:      "socks",
		Port:     port,
		Listen:   "127.0.0.1",
		Protocol: "socks",
		Settings: raw,
		Sniffing: &Sniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls", "quic"},
		},
	}
}

func localHTTP(port int) Inbound {
	raw, _ := json.Marshal(map[string]interface{}{})
	return Inbound{
		Tag:      "http",
		Port:     port,
		Listen:   "127.0.0.1",
		Protocol: "http",
		Settings: raw,
	}
}

func freedomOutbound() Outbound {
	raw, _ := json.Marshal(map[string]interface{}{"domainStrategy": "UseIPv4"})
	return Outbound{Tag: "direct", Protocol: "freedom", Settings: raw}
}

func blackholeOutbound(responseType string) Outbound {
	raw, _ := json.Marshal(map[string]interface{}{"response": map[string]string{"type": responseType}})
	return Outbound{Tag: "block", Protocol: "blackhole", Settings: raw}
}

func defaultDNS() *DNSConfig {
	return &DNSConfig{
		Servers: []interface{}{
			"1.1.1.1",
			"8.8.8.8",
			"8.8.4.4",
		},
	}
}

func defaultRouting() *Routing {
	return &Routing{
		DomainStrategy: "IPOnDemand",
		Rules: []RoutingRule{
			{Type: "field", OutboundTag: "direct", Protocol: []string{"bittorrent"}},
			{
				Type:        "field",
				OutboundTag: "block",
				Domain: []string{
					"geosite:category-ads-all",
					"geosite:category-ads",
					"geosite:category-public-tracker",
				},
			},
			{Type: "field", OutboundTag: "direct", IP: []string{"geoip:private"}},
			{Type: "field", OutboundTag: "direct", Domain: []string{"geosite:private"}},
			// Route blocked Russian domains/IPs through the proxy (runetfreedom geosets,
			// bundled in V2RayNG / Hiddify / NekoBox). Must come before category-ru/geoip:ru.
			{Type: "field", OutboundTag: "proxy", Domain: []string{"geosite:ru-blocked"}},
			{Type: "field", OutboundTag: "proxy", IP: []string{"geoip:ru-blocked"}},
			// Unblocked Russian domains go direct without DNS resolution (faster than IPOnDemand).
			{Type: "field", OutboundTag: "direct", Domain: []string{"geosite:category-ru"}},
			// Fallback: unblocked Russian IPs without a domain match go direct.
			{Type: "field", OutboundTag: "direct", IP: []string{"geoip:ru"}},
		},
	}
}

func sanitizeTag(tag string) string {
	r := strings.NewReplacer(" ", "-", "/", "-", "\\", "-")
	return r.Replace(tag)
}
