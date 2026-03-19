// Package config loads and validates the application configuration from a JSON file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for the xray-subscription service.
type Config struct {
	ListenAddr        string `json:"listen_addr"`
	ServerHost        string `json:"server_host"`
	ConfigDir         string `json:"config_dir"`
	DBPath            string `json:"db_path"`
	SyncInterval      int    `json:"sync_interval_seconds"`
	BaseURL           string `json:"base_url"`
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

// SubURL returns the full subscription URL for the given user token.
func (c *Config) SubURL(token string) string {
	return fmt.Sprintf("%s/sub/%s", c.BaseURL, token)
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
