// Package config loads and validates the application configuration from a JSON file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alchemylink/raven-subscribe/internal/models"
)

// Config holds all runtime configuration for the xray-subscription service.
type Config struct {
	ListenAddr        string `json:"listen_addr"`
	ServerHost        string `json:"server_host"`
	ConfigDir         string `json:"config_dir"`
	DBPath            string `json:"db_path"`
	SyncInterval      int    `json:"sync_interval_seconds"`
	BaseURL           string `json:"base_url"`
	// FallbackBaseURL overrides BaseURL for fallback subscription URLs (/sub/fallback/*).
	// When set, fallback tokens point to this domain (e.g. "https://sub.example.com" — EU direct,
	// bypassing RU relay). Allows fallback URLs to remain reachable even when RU VPS is down.
	// When empty, falls back to BaseURL.
	FallbackBaseURL string `json:"fallback_base_url,omitempty"`
	// FallbackServerHost overrides ServerHost in VPN links served via /sub/fallback/*.
	// Set to EU VPS IP or EU-direct domain so fallback configs bypass RU relay.
	// When empty, ServerHost is used (same server address as primary subscription).
	FallbackServerHost   string            `json:"fallback_server_host,omitempty"`
	// FallbackInboundHosts overrides InboundHosts for fallback subscription requests.
	// When nil, InboundHosts is used.
	FallbackInboundHosts map[string]string `json:"fallback_inbound_hosts,omitempty"`
	// FallbackInboundTags restricts /sub/fallback/* responses to outbounds whose
	// inbound tag is in this list. Empty/nil means no filtering (all user inbounds returned).
	// Use to expose only an isolated fallback inbound (e.g. ["vless-fallback-in"])
	// while primary subscription continues to serve all primary inbounds.
	FallbackInboundTags []string `json:"fallback_inbound_tags,omitempty"`
	AdminToken        string `json:"admin_token"`
	BalancerStrategy  string `json:"balancer_strategy"`
	BalancerProbeURL  string `json:"balancer_probe_url"`
	BalancerProbeFreq string `json:"balancer_probe_interval"`
	// Ports for client inbounds in generated subscription configs. Zero = use default.
	SocksInboundPort int `json:"socks_inbound_port"` // default 2080
	HTTPInboundPort  int `json:"http_inbound_port"`  // default 1081
	// Rate limiting: requests per minute per IP. Zero = disabled.
	RateLimitSubPerMin   int `json:"rate_limit_sub_per_min"`   // default 60
	RateLimitAdminPerMin int `json:"rate_limit_admin_per_min"` // default 30
	// When set, API-created users are added to this Xray inbound (tag). Enables write-back to config_dir or Xray API.
	APIUserInboundTag string `json:"api_user_inbound_tag,omitempty"`
	// When set, users are added via Xray gRPC API instead of writing to config files. E.g. "127.0.0.1:8080".
	// Requires api_user_inbound_tag. Xray must have API enabled with HandlerService in services.
	XrayAPIAddr string `json:"xray_api_addr,omitempty"`
	// Fallback when inbound is not in config_dir: protocol (vless, vmess, trojan, shadowsocks) for creating inbound in DB.
	// Use when config_dir is empty or Xray configs are elsewhere.
	APIUserInboundProtocol string `json:"api_user_inbound_protocol,omitempty"`
	// Fallback port for the inbound when creating from api_user_inbound_protocol. Default 443.
	APIUserInboundPort int `json:"api_user_inbound_port,omitempty"`
	// Octal permission bits for Xray JSON files Raven writes under config_dir (e.g. "0644", "0755"). Empty = 0600.
	XrayConfigFileMode string `json:"xray_config_file_mode,omitempty"`
	// InboundHosts overrides server_host for specific inbound tags.
	// Key: inbound tag, value: host/IP to use in generated client configs.
	// Falls back to ServerHost when a tag is not listed.
	// Example: {"hysteria-in": "203.0.113.5", "vless-reality-in": "example.com"}
	InboundHosts map[string]string `json:"inbound_hosts,omitempty"`

	// InboundPorts overrides the port for specific inbound tags in generated client configs.
	// Key: inbound tag, value: port number. Falls back to inbound's own port when tag is not listed.
	// Example: {"vless-reality-in": 8444} — clients connect to relay:8444 instead of EU:443
	InboundPorts map[string]int `json:"inbound_ports,omitempty"`

	// ClientBlackholeResponse sets the blackhole outbound response type in generated client configs.
	// Accepted values: "http" (default, returns an HTTP error immediately so clients don't stall),
	// "none" (drops the connection silently, no response sent).
	// When empty, "http" is used.
	ClientBlackholeResponse string `json:"client_blackhole_response,omitempty"`

	// ClientDNSServers overrides the DNS server list in generated client configs.
	// Each entry is either a plain IP string or an object with Xray DNS server fields:
	//   address      — IP or hostname (required)
	//   domains      — resolve only these domains via this server (geosite:/domain: syntax)
	//   skipFallback — when true, server is excluded from the fallback list (List 2);
	//                  use with domain-specific servers to prevent them handling unmatched domains
	//   expectIPs    — accept only responses whose IPs match these ranges (geoip: syntax);
	//                  mismatches are discarded and the next server is tried (anti-spoofing)
	// When empty or omitted, a default list (1.1.1.1, 8.8.8.8, 8.8.4.4) is used.
	// Example:
	//   [{"address":"77.88.8.8","domains":["geosite:ru-blocked"],"skipFallback":true,"expectIPs":["geoip:ru"]},"1.1.1.1","9.9.9.9"]
	ClientDNSServers []interface{} `json:"client_dns_servers,omitempty"`

	// VLESSClientEncryption maps VLESS inbound tag to its client-side VLESS Encryption string.
	// Required when the inbound uses VLESS Encryption (decryption != "none").
	// Generate both strings with: xray vlessenc
	// Example: {"vless-reality-in": "mlkem768x25519plus.native.0rtt.(X25519 Password).(ML-KEM-768 Client)"}
	VLESSClientEncryption map[string]string `json:"vless_client_encryption,omitempty"`
	// XrayEnabled controls whether Xray config_dir sync is active. Default true.
	// Set to false when Xray is not installed — suppresses "directory not found" warnings.
	XrayEnabled *bool `json:"xray_enabled,omitempty"`

	xrayFilePerm os.FileMode `json:"-"`
}

// Load reads and parses a JSON config file from path. An empty path returns defaults.
func Load(path string) (*Config, error) {
	cfg := &Config{
		ListenAddr:   ":8080",
		ConfigDir:    "/etc/xray/config.d",
		DBPath:       "/var/lib/xray-subscription/db.sqlite",
		SyncInterval: 60,
		BaseURL:      "http://localhost:8080",
		// Supported values: random, roundRobin, leastPing, leastLoad
		BalancerStrategy:  "leastPing",
		BalancerProbeURL:  "https://www.gstatic.com/generate_204",
		BalancerProbeFreq: "30s",
	}

	if path == "" {
		if err := applyXrayFilePerm(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// #nosec G304 -- path is explicitly provided by CLI/runtime configuration.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	normalizeVLESSClientEncryption(cfg)

	if cfg.ServerHost == "" {
		return nil, fmt.Errorf("server_host is required in config")
	}
	cfg.BalancerStrategy = normalizeBalancerStrategy(cfg.BalancerStrategy)
	if cfg.BalancerStrategy == "" {
		return nil, fmt.Errorf("invalid balancer_strategy: must be one of random, roundRobin, leastPing, leastLoad")
	}
	if err := applyXrayFilePerm(cfg); err != nil {
		return nil, fmt.Errorf("xray_config_file_mode: %w", err)
	}
	return cfg, nil
}

// normalizeVLESSClientEncryption trims map keys and values so lookups match Xray inbound tags
// (avoids misses from accidental spaces in config.json).
func normalizeVLESSClientEncryption(cfg *Config) {
	if cfg == nil || len(cfg.VLESSClientEncryption) == 0 {
		return
	}
	out := make(map[string]string, len(cfg.VLESSClientEncryption))
	for k, v := range cfg.VLESSClientEncryption {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		cfg.VLESSClientEncryption = nil
	} else {
		cfg.VLESSClientEncryption = out
	}
}

// XrayConfigFilePerm returns permission bits used when writing Xray JSON configs under config_dir.
// Default is 0o600 (owner read/write only). Safe for nil receiver.
func (c *Config) XrayConfigFilePerm() os.FileMode {
	if c == nil {
		return 0o600
	}
	if c.xrayFilePerm == 0 {
		return 0o600
	}
	return c.xrayFilePerm
}

func applyXrayFilePerm(cfg *Config) error {
	perm, err := parseXrayConfigFileMode(cfg.XrayConfigFileMode)
	if err != nil {
		return err
	}
	cfg.xrayFilePerm = perm
	return nil
}

// parseXrayConfigFileMode parses "0644", "644", "0o644", etc. Empty string → 0o600.
func parseXrayConfigFileMode(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0o600, nil
	}
	s = strings.TrimPrefix(s, "0o")
	s = strings.TrimPrefix(s, "0O")
	if s == "" {
		return 0, fmt.Errorf("empty after stripping 0o prefix")
	}
	for _, r := range s {
		if r < '0' || r > '7' {
			return 0, fmt.Errorf("invalid octal digit in %q", s)
		}
	}
	u, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("parse octal: %w", err)
	}
	if u > 0o777 {
		return 0, fmt.Errorf("mode must be <= 0777, got %#o", u)
	}
	return os.FileMode(u) & 0o777, nil
}

// IsXrayEnabled returns true if Xray sync is enabled (default true).
func (c *Config) IsXrayEnabled() bool {
	if c.XrayEnabled == nil {
		return true
	}
	return *c.XrayEnabled
}

// HostForInbound returns the server host for a given inbound tag.
// Falls back to ServerHost if the tag is not in InboundHosts.
func (c *Config) HostForInbound(tag string) string {
	if h, ok := c.InboundHosts[tag]; ok && strings.TrimSpace(h) != "" {
		return h
	}
	return c.ServerHost
}

// SubURL returns the full subscription URL for the given user token.
func (c *Config) SubURL(token string) string {
	return fmt.Sprintf("%s/sub/%s", c.BaseURL, token)
}

// FallbackURL returns the base fallback subscription URL for the given fallback token.
func (c *Config) FallbackURL(fallbackToken string) string {
	if fallbackToken == "" {
		return ""
	}
	base := c.BaseURL
	if c.FallbackBaseURL != "" {
		base = c.FallbackBaseURL
	}
	return fmt.Sprintf("%s/sub/fallback/%s", base, fallbackToken)
}

// SubURLs returns all subscription URL variants for the given user token.
func (c *Config) SubURLs(token string) models.SubURLs {
	sub := fmt.Sprintf("%s/sub/%s", c.BaseURL, token)
	compact := fmt.Sprintf("%s/c/%s", c.BaseURL, token)
	return models.SubURLs{
		Full:        sub,
		LinksText:   sub + "/links.txt",
		LinksB64:    sub + "/links.b64",
		Compact:     compact,
		CompactText: compact + "/links.txt",
		CompactB64:  compact + "/links.b64",
	}
}

// SubURLsWithFallback returns all subscription URL variants including all fallback format variants.
func (c *Config) SubURLsWithFallback(token, fallbackToken string) models.SubURLs {
	urls := c.SubURLs(token)
	if fallbackToken == "" {
		return urls
	}
	fbase := c.BaseURL
	if c.FallbackBaseURL != "" {
		fbase = c.FallbackBaseURL
	}
	fsub := fmt.Sprintf("%s/sub/fallback/%s", fbase, fallbackToken)
	fcp := fmt.Sprintf("%s/c/fallback/%s", fbase, fallbackToken)
	urls.Fallback = fsub
	urls.FallbackText = fsub + "/links.txt"
	urls.FallbackB64 = fsub + "/links.b64"
	urls.FallbackCompact = fcp
	urls.FallbackCompactText = fcp + "/links.txt"
	urls.FallbackCompactB64 = fcp + "/links.b64"
	return urls
}

func normalizeBalancerStrategy(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	switch s {
	case "", "leastping":
		return "leastPing"
	case "random":
		return "random"
	case "roundrobin":
		return "roundRobin"
	case "leastload":
		return "leastLoad"
	default:
		return ""
	}
}
