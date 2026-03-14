package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParsedInbound is the result of parsing a single inbound from a server config file
type ParsedInbound struct {
	Tag      string
	Protocol string
	Port     int
	RawJSON  string   // full inbound JSON for storage
	Clients  []ParsedClient
}

// ParsedClient is one client entry found in an inbound
type ParsedClient struct {
	// Identifier used to match with existing users (email, uuid, or generated)
	Identity string
	// Stored credentials JSON (StoredClientConfig)
	ConfigJSON string
}

// ParseConfigDir reads all JSON files from dir and returns parsed inbounds per file
func ParseConfigDir(dir string) (map[string][]ParsedInbound, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	result := make(map[string][]ParsedInbound)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		inbounds, err := ParseConfigFile(fullPath)
		if err != nil {
			// Log and continue; don't fail all due to one bad file
			fmt.Fprintf(os.Stderr, "WARN: parse %s: %v\n", fullPath, err)
			continue
		}
		result[fullPath] = inbounds
	}
	return result, nil
}

// ParseConfigFile parses a single xray server config JSON file
func ParseConfigFile(path string) ([]ParsedInbound, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// xray config can be just an inbounds array (`[...]`) or a full config
	// (`{"inbounds":[...]}`) — support both.
	var cfg struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		var inboundsOnly []json.RawMessage
		if err2 := json.Unmarshal(data, &inboundsOnly); err2 != nil {
			return nil, fmt.Errorf("json parse: %w", err)
		}
		cfg.Inbounds = inboundsOnly
	}

	var result []ParsedInbound
	for _, raw := range cfg.Inbounds {
		var si ServerInbound
		if err := json.Unmarshal(raw, &si); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: skip inbound: %v\n", err)
			continue
		}

		port, err := parsePort(si.Port)
		if err != nil {
			continue
		}

		clients, err := extractClients(si)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: extract clients for %s: %v\n", si.Tag, err)
		}

		result = append(result, ParsedInbound{
			Tag:      si.Tag,
			Protocol: strings.ToLower(si.Protocol),
			Port:     port,
			RawJSON:  string(raw),
			Clients:  clients,
		})
	}
	return result, nil
}

func parsePort(raw json.RawMessage) (int, error) {
	if raw == nil {
		return 0, fmt.Errorf("missing port")
	}
	// Try as int
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		return i, nil
	}
	// Try as string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		// Could be "443" or "443-450" — take first
		parts := strings.SplitN(s, "-", 2)
		return strconv.Atoi(parts[0])
	}
	return 0, fmt.Errorf("cannot parse port %s", string(raw))
}

// extractClients pulls all client credentials from an inbound
func extractClients(si ServerInbound) ([]ParsedClient, error) {
	proto := strings.ToLower(si.Protocol)
	switch proto {
	case "vmess":
		return extractVMess(si)
	case "vless":
		return extractVLESS(si)
	case "trojan":
		return extractTrojan(si)
	case "shadowsocks":
		return extractShadowsocks(si)
	case "socks":
		return extractSOCKS(si)
	default:
		return nil, nil // unsupported or no clients (freedom, blackhole, etc.)
	}
}

func extractVMess(si ServerInbound) ([]ParsedClient, error) {
	var s VMessInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}
	var clients []ParsedClient
	for _, c := range s.Clients {
		cred := StoredClientConfig{
			Protocol: "vmess",
			ID:       c.ID,
			AlterId:  c.AlterId,
		}
		b, _ := json.Marshal(cred)
		identity := firstNonEmpty(c.Email, c.ID)
		clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
	}
	return clients, nil
}

func extractVLESS(si ServerInbound) ([]ParsedClient, error) {
	var s VLESSInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}
	var clients []ParsedClient
	for _, c := range s.Clients {
		cred := StoredClientConfig{
			Protocol: "vless",
			ID:       c.ID,
			Flow:     c.Flow,
			Email:    c.Email,
		}
		b, _ := json.Marshal(cred)
		identity := firstNonEmpty(c.Email, c.ID)
		clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
	}
	return clients, nil
}

func extractTrojan(si ServerInbound) ([]ParsedClient, error) {
	var s TrojanInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}
	var clients []ParsedClient
	for _, c := range s.Clients {
		cred := StoredClientConfig{
			Protocol: "trojan",
			Password: c.Password,
			Email:    c.Email,
		}
		b, _ := json.Marshal(cred)
		identity := firstNonEmpty(c.Email, c.Password)
		clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
	}
	return clients, nil
}

func extractShadowsocks(si ServerInbound) ([]ParsedClient, error) {
	var s ShadowsocksInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}
	var clients []ParsedClient

	if len(s.Clients) > 0 {
		// Multi-user shadowsocks
		for _, c := range s.Clients {
			method := firstNonEmpty(c.Method, s.Method, "aes-256-gcm")
			cred := StoredClientConfig{
				Protocol: "shadowsocks",
				Password: c.Password,
				Method:   method,
				Email:    c.Email,
			}
			b, _ := json.Marshal(cred)
			identity := firstNonEmpty(c.Email, c.Password)
			clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
		}
	} else if s.Password != "" {
		// Single-user shadowsocks
		method := firstNonEmpty(s.Method, "aes-256-gcm")
		cred := StoredClientConfig{Protocol: "shadowsocks", Password: s.Password, Method: method}
		b, _ := json.Marshal(cred)
		clients = append(clients, ParsedClient{Identity: s.Password, ConfigJSON: string(b)})
	}
	return clients, nil
}

func extractSOCKS(si ServerInbound) ([]ParsedClient, error) {
	var s SOCKSInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}
	var clients []ParsedClient
	for _, acc := range s.Accounts {
		cred := StoredClientConfig{Protocol: "socks", User: acc.User, Password: acc.Pass}
		b, _ := json.Marshal(cred)
		clients = append(clients, ParsedClient{Identity: acc.User, ConfigJSON: string(b)})
	}
	return clients, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
