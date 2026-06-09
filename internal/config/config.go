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

// HysteriaConfig configures the per-user Hysteria2 reserve subscription channel.
type HysteriaConfig struct {
	Enabled      bool   `json:"enabled"`
	Host         string `json:"host"`          // domain clients connect to (relay apex, e.g. zirgate.com)
	Port         int    `json:"port"`          // relay UDP port forwarded to the EU hysteria server (e.g. 47014)
	ObfsType     string `json:"obfs_type"`     // "salamander" (mainstream-client default) or "gecko"
	ObfsPassword string `json:"obfs_password"` // shared obfs key (server-wide, not per-user)
	SNI          string `json:"sni"`           // TLS SNI presented by the client (matches the server cert CN)
	// CertPin is the server cert SHA256 fingerprint (hex). Emitted as pinSHA256 in the
	// hysteria2:// URI so a self-signed cert verifies without a public CA — avoids leaking
	// the SNI domain into Certificate Transparency logs (relay-OPSEC parity). The URI carries
	// it automatically, so there is no manual-pin burden. Empty = rely on CA trust (real cert).
	CertPin string `json:"cert_pin,omitempty"`
	// InMainSub, when true, also appends the per-user hysteria2:// URI to the link-list
	// subscription (/sub/{token}/links, /c/{token}/links, etc.) so clients get the reserve
	// as an extra server without importing /sub/{token}/hy2 separately. Off by default.
	InMainSub bool `json:"in_main_sub"`
}

// Config holds all runtime configuration for the xray-subscription service.
type Config struct {
	ListenAddr string `json:"listen_addr"`
	// MetricsListen, when set (e.g. "127.0.0.1:9091"), serves Prometheus metrics
	// at /metrics on a dedicated listener. Deliberately separate from ListenAddr:
	// the main router is publicly reachable through the subscription vhost, and
	// metrics must never be. Empty = metrics disabled.
	MetricsListen     string `json:"metrics_listen,omitempty"`
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

	// KillSwitchInboundTags is the set of inbound tags the fallback killswitch
	// (enable/disable + reconcile loop) actually adds/removes from the running Xray.
	// This is DELIBERATELY separate from FallbackInboundTags: a tag can be served on
	// the /sub/fallback/* route (subscription routing) WITHOUT being torn down when
	// the killswitch is OFF. Empty/nil → falls back to FallbackInboundTags (legacy
	// behaviour, where the two sets are identical). Use KillSwitchTags() to resolve.
	// Example: fallback_inbound_tags=[vless-fallback-in, vless-experimental-in] but
	// killswitch_inbound_tags=[vless-fallback-in] → experimental stays up regardless
	// of killswitch state (it is just a config.d inbound, like the primaries).
	KillSwitchInboundTags []string `json:"killswitch_inbound_tags,omitempty"`

	// ExcludeInboundTags drops these inbound tags from ALL subscription responses
	// (primary /sub/* AND fallback /sub/fallback/*, every format). Unlike
	// FallbackInboundTags (which MOVES a tag from primary to fallback), this removes
	// it everywhere. Use to retire a dead transport from what clients download while
	// keeping the inbound alive on the server (e.g. ["vless-reality-v2-in"] once
	// Reality stops passing the RU first hop). Applied before the FallbackInboundTags
	// split, so an excluded tag never appears regardless of route.
	ExcludeInboundTags []string `json:"exclude_inbound_tags,omitempty"`

	// Hysteria, when enabled, exposes a per-user Hysteria2 UDP reserve: /sub/{token}/hy2
	// returns a hysteria2:// URI, and /hysteria/auth is the auth-backend the native
	// hysteria daemon calls (auth.type:http) to validate a connection's auth (= the
	// user's sub token) against the DB. obfs is shared (anti-DPI key, not access control);
	// per-user control is via the auth-backend. See hysteria_raven_integration_plan.
	Hysteria *HysteriaConfig `json:"hysteria,omitempty"`

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
	//
	// Example RU split — resolves allowed RU services via Yandex DNS (defeats VPN
	// detection by geo-mismatch) while blocked domains stay on foreign DNS through
	// the proxy:
	//   [{"address":"77.88.8.8","domains":["geosite:category-ru","domain:ru","domain:su","domain:рф"],"skipFallback":true,"expectIPs":["geoip:ru"]},"1.1.1.1","9.9.9.9"]
	//
	// SECURITY: never list `geosite:ru-blocked` (or any Roskomnadzor blocklist
	// selector) under a Russian resolver — Yandex DNS is RU-jurisdiction (374-ФЗ
	// "Yarovaya" + СОРМ-3) and logs query metadata available to FSB, which would
	// tie each subscriber's residential IP to every blocked site they query.
	ClientDNSServers []interface{} `json:"client_dns_servers,omitempty"`

	// VLESSClientEncryption maps VLESS inbound tag to its client-side VLESS Encryption string.
	// Required when the inbound uses VLESS Encryption (decryption != "none").
	// Generate both strings with: xray vlessenc
	// Example: {"vless-reality-in": "mlkem768x25519plus.native.0rtt.(X25519 Password).(ML-KEM-768 Client)"}
	VLESSClientEncryption map[string]string `json:"vless_client_encryption,omitempty"`
	// XrayEnabled controls whether Xray config_dir sync is active. Default true.
	// Set to false when Xray is not installed — suppresses "directory not found" warnings.
	XrayEnabled *bool `json:"xray_enabled,omitempty"`

	// KillSwitchReconcileInterval is how often (seconds) raven-subscribe re-applies
	// the persisted killswitch state to the Xray runtime via gRPC. Catches drift
	// caused by xray restarts that reload fallback inbounds from /etc/xray/config.d/
	// while the killswitch is OFF in the DB.
	// Default 30. Set to 0 to disable the periodic reconcile (startup-only).
	// No-op when xray_api_addr or fallback_inbound_tags are unset.
	KillSwitchReconcileInterval int `json:"killswitch_reconcile_interval_seconds,omitempty"`

	xrayFilePerm os.FileMode `json:"-"`
}

// KillSwitchTags returns the inbound tags the fallback killswitch controls.
// When KillSwitchInboundTags is set it is authoritative; otherwise it falls back
// to FallbackInboundTags so existing deployments keep their current behaviour.
func (c *Config) KillSwitchTags() []string {
	if len(c.KillSwitchInboundTags) > 0 {
		return c.KillSwitchInboundTags
	}
	return c.FallbackInboundTags
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
		KillSwitchReconcileInterval: 30,
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
	urls := models.SubURLs{
		Full:        sub,
		LinksText:   sub + "/links.txt",
		LinksB64:    sub + "/links.b64",
		Compact:     compact,
		CompactText: compact + "/links.txt",
		CompactB64:  compact + "/links.b64",
	}
	if c.Hysteria != nil && c.Hysteria.Enabled {
		urls.Hy2 = sub + "/hy2"
	}
	return urls
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
