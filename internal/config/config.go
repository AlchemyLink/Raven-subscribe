package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

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
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		ListenAddr:   ":8080",
		ConfigDir:    "/etc/xray/config.d",
		DBPath:       "/var/lib/xray-subscription/db.sqlite",
		SyncInterval: 60,
		BaseURL:      "http://localhost:8080",
		// Supported values: random, leastPing, leastLoad
		BalancerStrategy:  "leastPing",
		BalancerProbeURL:  "https://www.gstatic.com/generate_204",
		BalancerProbeFreq: "30s",
	}

	if path == "" {
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
		return nil, fmt.Errorf("invalid balancer_strategy: must be one of random, leastPing, leastLoad")
	}
	return cfg, nil
}

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
	case "leastload":
		return "leastLoad"
	default:
		return ""
	}
}
