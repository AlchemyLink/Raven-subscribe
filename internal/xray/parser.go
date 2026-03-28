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

// GetInboundByTag finds the first inbound with the given tag in config_dir.
// Returns nil if not found. Used to ensure inbound exists in DB when creating users.
func GetInboundByTag(dir, tag string) (*ParsedInbound, string, error) {
	parsed, err := ParseConfigDir(dir)
	if err != nil {
		return nil, "", err
	}
	tag = strings.TrimSpace(tag)
	for file, inbounds := range parsed {
		for i := range inbounds {
			if strings.TrimSpace(inbounds[i].Tag) == tag {
				return &inbounds[i], file, nil
			}
		}
	}
	return nil, "", nil
}

// ParseConfigDir reads all JSON files from dir and returns parsed inbounds per file.
// Client VLESS Encryption strings are not resolved; use ParseConfigDirWith for that.
// Does not WARN when VLESS Encryption is enabled but no client map is supplied (internal use).
func ParseConfigDir(dir string) (map[string][]ParsedInbound, error) {
	return parseConfigDirWith(dir, nil, false)
}

// ParseConfigDirWith is like ParseConfigDir but resolves VLESS Encryption client strings.
// clientEncMap maps inbound tag to the client-side VLESS Encryption string from config.
// When a tag needs a client string but it is missing from the map (or map is nil), logs WARN.
func ParseConfigDirWith(dir string, clientEncMap map[string]string) (map[string][]ParsedInbound, error) {
	return parseConfigDirWith(dir, clientEncMap, true)
}

func parseConfigDirWith(dir string, clientEncMap map[string]string, warnVLESSClientEnc bool) (map[string][]ParsedInbound, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// config_dir absent means Xray is not installed or not yet configured — not an error.
			return make(map[string][]ParsedInbound), nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	result := make(map[string][]ParsedInbound)
	// One WARN per inbound tag per directory scan (same tag may appear in multiple JSON files).
	vlessEncWarned := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		inbounds, err := parseConfigFileWith(fullPath, clientEncMap, vlessEncWarned, warnVLESSClientEnc)
		if err != nil {
			// Log and continue; don't fail all due to one bad file
			fmt.Fprintf(os.Stderr, "WARN: parse %s: %v\n", fullPath, err)
			continue
		}
		result[fullPath] = inbounds
	}
	return result, nil
}

// ParseConfigFile parses a single xray server config JSON file.
func ParseConfigFile(path string) ([]ParsedInbound, error) {
	return parseConfigFileWith(path, nil, nil, false)
}

// vlessEncWarned, when non-nil, suppresses duplicate VLESS Encryption WARN lines for the same inbound tag
// across multiple files in one directory scan. nil = warn every time (single-file parse).
func parseConfigFileWith(path string, clientEncMap map[string]string, vlessEncWarned map[string]struct{}, warnVLESSClientEnc bool) ([]ParsedInbound, error) {
	// #nosec G304 -- path comes from configured xray config directory traversal.
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

		clients, err := extractClients(si, clientEncMap, vlessEncWarned, warnVLESSClientEnc)
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

// extractClients pulls all client credentials from an inbound.
// clientEncMap maps inbound tag to the client-side VLESS Encryption string (may be nil).
// vlessEncWarned deduplicates VLESS Encryption warnings when scanning a directory; nil = no dedup.
// warnVLESSClientEnc: when false, missing vless_client_encryption is not logged (ParseConfigDir / single-file tools).
func extractClients(si ServerInbound, clientEncMap map[string]string, vlessEncWarned map[string]struct{}, warnVLESSClientEnc bool) ([]ParsedClient, error) {
	proto := strings.ToLower(si.Protocol)
	switch proto {
	case "vmess":
		return extractVMess(si)
	case "vless":
		return extractVLESS(si, clientEncMap, vlessEncWarned, warnVLESSClientEnc)
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
		// #nosec G117 -- credentials are marshaled for internal DB storage.
		b, _ := json.Marshal(cred)
		identity := firstNonEmpty(c.Email, c.ID)
		clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
	}
	return clients, nil
}

func extractVLESS(si ServerInbound, clientEncMap map[string]string, vlessEncWarned map[string]struct{}, warnVLESSClientEnc bool) ([]ParsedClient, error) {
	var s VLESSInboundSettings
	if err := json.Unmarshal(si.Settings, &s); err != nil {
		return nil, err
	}

	inboundTag := strings.TrimSpace(si.Tag)

	// Determine client-side encryption string.
	// The server's settings.decryption contains private keys and is NOT sent to clients.
	// The client encryption string (public keys only) comes from vless_client_encryption config map.
	clientEnc := "none"
	if s.Decryption != "" && s.Decryption != "none" {
		if enc, ok := clientEncMap[inboundTag]; ok && enc != "" {
			clientEnc = enc
		} else if warnVLESSClientEnc {
			doWarn := vlessEncWarned == nil
			if !doWarn {
				if _, dup := vlessEncWarned[inboundTag]; !dup {
					vlessEncWarned[inboundTag] = struct{}{}
					doWarn = true
				}
			}
			if doWarn {
				fmt.Fprintf(os.Stderr,
					"WARN: inbound %q uses VLESS Encryption but vless_client_encryption[%q] is not set; "+
						"client outbound encryption will be \"none\" — clients will fail to connect\n",
					inboundTag, inboundTag)
			}
		}
	}

	var clients []ParsedClient
	for _, c := range s.Clients {
		cred := StoredClientConfig{
			Protocol:   "vless",
			ID:         c.ID,
			Flow:       c.Flow,
			Email:      c.Email,
			Encryption: clientEnc,
		}
		// #nosec G117 -- credentials are marshaled for internal DB storage.
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
		// #nosec G117 -- credentials are marshaled for internal DB storage.
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
			// #nosec G117 -- credentials are marshaled for internal DB storage.
			b, _ := json.Marshal(cred)
			identity := firstNonEmpty(c.Email, c.Password)
			clients = append(clients, ParsedClient{Identity: identity, ConfigJSON: string(b)})
		}
	} else if s.Password != "" {
		// Single-user shadowsocks
		method := firstNonEmpty(s.Method, "aes-256-gcm")
		cred := StoredClientConfig{Protocol: "shadowsocks", Password: s.Password, Method: method}
		// #nosec G117 -- credentials are marshaled for internal DB storage.
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
		// #nosec G117 -- credentials are marshaled for internal DB storage.
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
