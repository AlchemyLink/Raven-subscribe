// Package singbox provides parsing of sing-box server configuration files.
package singbox

import (
	"encoding/json"
	"fmt"
	"os"
)

// serverConfig is a minimal representation of a sing-box server config file.
type serverConfig struct {
	Inbounds []inbound `json:"inbounds"`
}

type inbound struct {
	Type      string    `json:"type"`
	Tag       string    `json:"tag"`
	Listen    string    `json:"listen,omitempty"`
	ListenPort int      `json:"listen_port,omitempty"`
	Users     []user    `json:"users,omitempty"`
	Obfs      *obfs     `json:"obfs,omitempty"`
	TLS       *tls      `json:"tls,omitempty"`
	UpMbps    int       `json:"up_mbps,omitempty"`
	DownMbps  int       `json:"down_mbps,omitempty"`
}

type user struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type obfs struct {
	Type     string `json:"type,omitempty"`
	Password string `json:"password,omitempty"`
}

type tls struct {
	Enabled    bool   `json:"enabled,omitempty"`
	ServerName string `json:"server_name,omitempty"`
}

// ParsedInbound is the result of parsing a single inbound from a sing-box config.
type ParsedInbound struct {
	Tag      string
	Protocol string
	Port     int
	RawJSON  string
	Clients  []ParsedClient
}

// ParsedClient is one user entry found in an inbound.
type ParsedClient struct {
	// Identity is the user name — used to match with existing DB users.
	Identity   string
	ConfigJSON string
}

// storedHysteria2Config holds per-user Hysteria2 credentials for the DB.
type storedHysteria2Config struct {
	Protocol string `json:"protocol"`
	Password string `json:"password"`
	// Transport settings shared across all users of the inbound.
	ServerName  string `json:"server_name,omitempty"`
	ObfsType    string `json:"obfs_type,omitempty"`
	ObfsPassword string `json:"obfs_password,omitempty"`
	UpMbps      int    `json:"up_mbps,omitempty"`
	DownMbps    int    `json:"down_mbps,omitempty"`
}

// ParseConfig reads a sing-box config file and returns all supported inbounds.
// Currently only hysteria2 inbounds are parsed.
func ParseConfig(path string) ([]ParsedInbound, error) {
	// #nosec G304 -- path comes from application config, not user input.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sing-box config %s: %w", path, err)
	}

	var cfg serverConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse sing-box config %s: %w", path, err)
	}

	var result []ParsedInbound
	for _, ib := range cfg.Inbounds {
		switch ib.Type {
		case "hysteria2":
			parsed, err := parseHysteria2(ib, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARN: singbox parse hysteria2 inbound %q: %v\n", ib.Tag, err)
				continue
			}
			result = append(result, *parsed)
		}
	}
	return result, nil
}

func parseHysteria2(ib inbound, sourceFile string) (*ParsedInbound, error) {
	tag := ib.Tag
	if tag == "" {
		tag = "hysteria2-in"
	}

	// Build shared transport metadata (same for all users of this inbound).
	shared := storedHysteria2Config{
		Protocol: "hysteria2",
		UpMbps:   ib.UpMbps,
		DownMbps: ib.DownMbps,
	}
	if ib.TLS != nil {
		shared.ServerName = ib.TLS.ServerName
	}
	if ib.Obfs != nil {
		shared.ObfsType = ib.Obfs.Type
		shared.ObfsPassword = ib.Obfs.Password
	}

	// Raw JSON for DB storage — strip users to keep size small and avoid
	// storing cleartext passwords in the inbound record.
	rawIb := ib
	rawIb.Users = nil
	rawBytes, err := json.Marshal(rawIb)
	if err != nil {
		return nil, fmt.Errorf("marshal inbound raw: %w", err)
	}

	parsed := &ParsedInbound{
		Tag:      tag,
		Protocol: "hysteria2",
		Port:     ib.ListenPort,
		RawJSON:  string(rawBytes),
	}

	for _, u := range ib.Users {
		if u.Name == "" {
			continue
		}
		cred := shared
		cred.Password = u.Password
		credBytes, err := json.Marshal(cred) // #nosec G117 -- marshaling user credential struct for storage, password field is intentional.
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: singbox marshal cred for user %q: %v\n", u.Name, err)
			continue
		}
		parsed.Clients = append(parsed.Clients, ParsedClient{
			Identity:   u.Name,
			ConfigJSON: string(credBytes),
		})
	}

	_ = sourceFile
	return parsed, nil
}
