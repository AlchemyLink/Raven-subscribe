// Package xray provides config parsing and client config generation.
// This file adds API-created users to Xray inbound config files.
package xray

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AddClientToInbound adds a new client to the Xray inbound with the given tag.
// configDir is the Xray config directory (e.g. /etc/xray/config.d).
// inboundTag is the tag of the inbound to add the client to.
// username is used as the client's email/identity.
// Returns the client credentials JSON for UpsertUserClient, or error.
func AddClientToInbound(configDir, inboundTag, username string) (clientConfigJSON string, err error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf("username required")
	}

	// Find file containing the inbound
	file, protocol, settingsRaw, err := findInboundSettings(configDir, inboundTag)
	if err != nil {
		return "", err
	}

	// Build new client based on protocol
	var newClient map[string]interface{}
	switch strings.ToLower(protocol) {
	case "vless":
		newClient = buildVLESSClient(username, settingsRaw)
	case "vmess":
		newClient = buildVMessClient(username, settingsRaw)
	case "trojan":
		newClient = buildTrojanClient(username)
	case "shadowsocks":
		newClient = buildShadowsocksClient(username, settingsRaw)
	case "socks":
		return "", fmt.Errorf("socks inbound does not support API user creation")
	default:
		return "", fmt.Errorf("unsupported protocol %s for inbound %s", protocol, inboundTag)
	}

	if newClient == nil {
		return "", fmt.Errorf("failed to build client for protocol %s", protocol)
	}

	// Marshal client config for DB storage (StoredClientConfig format)
	clientConfigJSON, err = clientToStoredConfig(protocol, newClient)
	if err != nil {
		return "", fmt.Errorf("client config: %w", err)
	}

	// Add client to settings and write back
	if err := addClientToFile(file, inboundTag, newClient); err != nil {
		return "", err
	}

	return clientConfigJSON, nil
}

// AddExistingClientToInbound adds a client with existing credentials (from DB) to the inbound.
// Used when syncing DB users back to config files.
func AddExistingClientToInbound(configDir, inboundTag, username, storedConfigJSON string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username required")
	}

	file, _, _, err := findInboundSettings(configDir, inboundTag)
	if err != nil {
		return err
	}

	clientMap, err := storedConfigToClientMap(storedConfigJSON, username)
	if err != nil {
		return err
	}

	return addClientToFile(file, inboundTag, clientMap)
}

// RemoveUserFromInbound removes a user (by email/username) from the inbound config file.
func RemoveUserFromInbound(configDir, inboundTag, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email required")
	}

	file, _, _, err := findInboundSettings(configDir, inboundTag)
	if err != nil {
		return err
	}

	return removeClientFromFile(file, inboundTag, email)
}

// writeConfigFile writes config with 0o600. Run Raven as the same user as Xray (e.g. User=xray in systemd).
func writeConfigFile(filePath string, out []byte) error {
	tmpPath := filePath + ".raven.tmp"
	if err := os.WriteFile(tmpPath, out, 0o600); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func removeClientFromFile(filePath, inboundTag, email string) error {
	// #nosec G304 -- filePath comes from controlled config discovery within configDir.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var inbounds []interface{}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err == nil {
		if ib, ok := root["inbounds"]; ok {
			if arr, ok := ib.([]interface{}); ok {
				inbounds = arr
			}
		}
	}
	if len(inbounds) == 0 {
		if err := json.Unmarshal(data, &inbounds); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
		root = map[string]interface{}{"inbounds": inbounds}
	}

	modified := false
	for _, raw := range inbounds {
		ib, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := ib["tag"].(string)
		if strings.TrimSpace(tag) != inboundTag {
			continue
		}

		settings, _ := ib["settings"].(map[string]interface{})
		if settings == nil {
			return fmt.Errorf("inbound %s has no settings", inboundTag)
		}
		clients, _ := settings["clients"].([]interface{})
		if clients == nil {
			return nil
		}

		var filtered []interface{}
		for _, c := range clients {
			cm, ok := c.(map[string]interface{})
			if !ok {
				filtered = append(filtered, c)
				continue
			}
			clientEmail, _ := cm["email"].(string)
			if strings.TrimSpace(clientEmail) == email {
				modified = true
				continue
			}
			filtered = append(filtered, c)
		}
		if modified {
			settings["clients"] = filtered
		}
		break
	}

	if !modified {
		return nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	return writeConfigFile(filePath, out)
}

// storedConfigToClientMap converts StoredClientConfig JSON to the client map format for config files.
func storedConfigToClientMap(storedJSON, email string) (map[string]interface{}, error) {
	var stored StoredClientConfig
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
		return nil, fmt.Errorf("parse stored config: %w", err)
	}

	proto := strings.ToLower(stored.Protocol)
	switch proto {
	case "vless":
		return map[string]interface{}{
			"id":    stored.ID,
			"flow":  firstNonEmpty(stored.Flow, ""),
			"email": email,
		}, nil
	case "vmess":
		return map[string]interface{}{
			"id":       stored.ID,
			"alterId": stored.AlterId,
			"email":    email,
		}, nil
	case "trojan":
		return map[string]interface{}{
			"password": stored.Password,
			"email":    email,
		}, nil
	case "shadowsocks":
		return map[string]interface{}{
			"password": stored.Password,
			"method":   firstNonEmpty(stored.Method, "2022-blake3-aes-256-gcm"),
			"email":    email,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported protocol %s", stored.Protocol)
	}
}

func findInboundSettings(configDir, tag string) (filePath, protocol string, settings json.RawMessage, err error) {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", "", nil, fmt.Errorf("read config dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(configDir, e.Name())
		// #nosec G304 -- fullPath is built from files listed by os.ReadDir(configDir).
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		var inbounds []json.RawMessage
		var cfg struct {
			Inbounds []json.RawMessage `json:"inbounds"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			if err2 := json.Unmarshal(data, &inbounds); err2 != nil {
				continue
			}
		} else {
			inbounds = cfg.Inbounds
		}

		for _, raw := range inbounds {
			var ib struct {
				Tag      string          `json:"tag"`
				Protocol string          `json:"protocol"`
				Settings json.RawMessage `json:"settings"`
			}
			if err := json.Unmarshal(raw, &ib); err != nil {
				continue
			}
			if strings.TrimSpace(ib.Tag) == tag {
				return fullPath, ib.Protocol, ib.Settings, nil
			}
		}
	}
	return "", "", nil, fmt.Errorf("inbound %s not found in %s", tag, configDir)
}

func buildVLESSClient(username string, settingsRaw json.RawMessage) map[string]interface{} {
	flow := ""
	if len(settingsRaw) > 0 {
		var s struct {
			Clients []struct { Flow string `json:"flow"` } `json:"clients"`
		}
		_ = json.Unmarshal(settingsRaw, &s)
		if len(s.Clients) > 0 && s.Clients[0].Flow != "" {
			flow = s.Clients[0].Flow
		}
	}
	return map[string]interface{}{
		"id":    generateUUID(),
		"flow":  flow,
		"email": username,
	}
}

func buildVMessClient(username string, settingsRaw json.RawMessage) map[string]interface{} {
	alterID := 0
	if len(settingsRaw) > 0 {
		var s struct {
			Clients []struct { AlterID int `json:"alterId"` } `json:"clients"`
		}
		_ = json.Unmarshal(settingsRaw, &s)
		if len(s.Clients) > 0 {
			alterID = s.Clients[0].AlterID
		}
	}
	return map[string]interface{}{
		"id":       generateUUID(),
		"alterId":  alterID,
		"email":    username,
	}
}

func buildTrojanClient(username string) map[string]interface{} {
	return map[string]interface{}{
		"password": generatePassword(16),
		"email":    username,
	}
}

func buildShadowsocksClient(username string, settingsRaw json.RawMessage) map[string]interface{} {
	method := "2022-blake3-aes-256-gcm"
	if len(settingsRaw) > 0 {
		var s struct {
			Method  string `json:"method"`
			Clients []struct { Method string `json:"method"` } `json:"clients"`
		}
		_ = json.Unmarshal(settingsRaw, &s)
		if s.Method != "" {
			method = s.Method
		} else if len(s.Clients) > 0 && s.Clients[0].Method != "" {
			method = s.Clients[0].Method
		}
	}
	// 2022 methods need base64-encoded 32-byte key; others use plain password
	password := generatePassword(16)
	if strings.HasPrefix(method, "2022") {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		password = base64.StdEncoding.EncodeToString(b)
	}
	return map[string]interface{}{
		"password": password,
		"method":   method,
		"email":    username,
	}
}

func clientToStoredConfig(protocol string, client map[string]interface{}) (string, error) {
	proto := strings.ToLower(protocol)
	switch proto {
	case "vless":
		id, _ := client["id"].(string)
		flow, _ := client["flow"].(string)
		enc := "none"
		dec, ok := client["decryption"].(string)
		if ok && dec != "" {
			enc = dec
		}
		// #nosec G117 -- password-like fields are expected in stored protocol credentials.
		b, _ := json.Marshal(StoredClientConfig{
			Protocol:   "vless",
			ID:         id,
			Flow:       flow,
			Encryption: enc,
		})
		return string(b), nil
	case "vmess":
		id, _ := client["id"].(string)
		aid := 0
		if a, ok := client["alterId"].(float64); ok {
			aid = int(a)
		}
		// #nosec G117 -- password-like fields are expected in stored protocol credentials.
		b, _ := json.Marshal(StoredClientConfig{
			Protocol: "vmess",
			ID:       id,
			AlterId:  aid,
		})
		return string(b), nil
	case "trojan":
		pwd, _ := client["password"].(string)
		// #nosec G117 -- password-like fields are expected in stored protocol credentials.
		b, _ := json.Marshal(StoredClientConfig{
			Protocol:   "trojan",
			Password:   pwd,
		})
		return string(b), nil
	case "shadowsocks":
		pwd, _ := client["password"].(string)
		method, _ := client["method"].(string)
		// #nosec G117 -- password-like fields are expected in stored protocol credentials.
		b, _ := json.Marshal(StoredClientConfig{
			Protocol: "shadowsocks",
			Password: pwd,
			Method:   method,
		})
		return string(b), nil
	default:
		return "", fmt.Errorf("unsupported protocol %s", protocol)
	}
}

func addClientToFile(filePath, inboundTag string, newClient map[string]interface{}) error {
	// #nosec G304 -- filePath comes from controlled config discovery within configDir.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var inbounds []interface{}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err == nil {
		if ib, ok := root["inbounds"]; ok {
			if arr, ok := ib.([]interface{}); ok {
				inbounds = arr
			}
		}
	}
	if len(inbounds) == 0 {
		if err := json.Unmarshal(data, &inbounds); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
		root = map[string]interface{}{"inbounds": inbounds}
	}

	modified := false
	for _, raw := range inbounds {
		ib, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := ib["tag"].(string)
		if strings.TrimSpace(tag) != inboundTag {
			continue
		}

		settings, _ := ib["settings"].(map[string]interface{})
		if settings == nil {
			return fmt.Errorf("inbound %s has no settings", inboundTag)
		}
		clients, _ := settings["clients"].([]interface{})
		if clients == nil {
			clients = []interface{}{}
		}
		settings["clients"] = append(clients, newClient)
		modified = true
		break
	}

	if !modified {
		return fmt.Errorf("inbound %s not found in %s", inboundTag, filePath)
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	return writeConfigFile(filePath, out)
}

func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generatePassword(byteLen int) string {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:byteLen*2]
}
