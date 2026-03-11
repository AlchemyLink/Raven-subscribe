package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	ListenAddr    string `json:"listen_addr"`
	ServerHost    string `json:"server_host"`
	ConfigDir     string `json:"config_dir"`
	DBPath        string `json:"db_path"`
	SyncInterval  int    `json:"sync_interval_seconds"`
	BaseURL       string `json:"base_url"`
	AdminToken    string `json:"admin_token"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		ListenAddr:   ":8080",
		ConfigDir:    "/etc/xray/config.d",
		DBPath:       "/var/lib/xray-subscription/db.sqlite",
		SyncInterval: 60,
		BaseURL:      "http://localhost:8080",
	}

	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	if cfg.ServerHost == "" {
		return nil, fmt.Errorf("server_host is required in config")
	}
	return cfg, nil
}

func (c *Config) SubURL(token string) string {
	return fmt.Sprintf("%s/sub/%s", c.BaseURL, token)
}
