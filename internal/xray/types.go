package xray

import "encoding/json"

// ─── Server-side config types ─────────────────────────────────────────────────

// ServerConfig is a parsed xray server config file
type ServerConfig struct {
	Inbounds []ServerInbound `json:"inbounds"`
}

// ServerInbound represents one inbound from server config
type ServerInbound struct {
	Tag            string          `json:"tag"`
	Listen         string          `json:"listen,omitempty"`
	Port           json.RawMessage `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
}

// VMessInboundSettings holds VMess inbound configuration from server config.
type VMessInboundSettings struct {
	Clients []VMessClient `json:"clients"`
}

// VMessClient represents a single VMess client credential entry.
type VMessClient struct {
	ID      string `json:"id"`
	//nolint:revive // Keep Xray-compatible JSON field naming.
	AlterId int    `json:"alterId,omitempty"`
	Email   string `json:"email,omitempty"`
	Level   int    `json:"level,omitempty"`
}

// VLESSInboundSettings holds VLESS inbound configuration from server config.
type VLESSInboundSettings struct {
	Clients    []VLESSClient `json:"clients"`
	Decryption string        `json:"decryption,omitempty"`
	Testpre    uint32        `json:"testpre,omitempty"`
}

// VLESSClient represents a single VLESS client credential entry.
type VLESSClient struct {
	ID    string `json:"id"`
	Flow  string `json:"flow,omitempty"`
	Email string `json:"email,omitempty"`
	Level int    `json:"level,omitempty"`
}

// TrojanInboundSettings holds Trojan inbound configuration from server config.
type TrojanInboundSettings struct {
	Clients []TrojanClient `json:"clients"`
}

// TrojanClient represents a single Trojan client credential entry.
type TrojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Level    int    `json:"level,omitempty"`
}

// ShadowsocksInboundSettings holds Shadowsocks inbound configuration from server config.
type ShadowsocksInboundSettings struct {
	Method   string              `json:"method,omitempty"`
	Password string              `json:"password,omitempty"`
	Clients  []ShadowsocksClient `json:"clients,omitempty"`
	Network  string              `json:"network,omitempty"`
}

// ShadowsocksClient represents a single Shadowsocks client credential entry.
type ShadowsocksClient struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Method   string `json:"method,omitempty"`
}

// SOCKSInboundSettings holds SOCKS inbound configuration from server config.
type SOCKSInboundSettings struct {
	Auth     string         `json:"auth,omitempty"`
	Accounts []SOCKSAccount `json:"accounts,omitempty"`
	UDP      bool           `json:"udp,omitempty"`
}

// SOCKSAccount represents a single SOCKS user/password credential entry.
type SOCKSAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// ─── Stream Settings (shared server+client) ────────────────────────────────

// StreamSettings describes the transport layer configuration for an inbound or outbound.
type StreamSettings struct {
	Network         string           `json:"network,omitempty"`
	Security        string           `json:"security,omitempty"`
	TLSSettings     *TLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *RealitySettings `json:"realitySettings,omitempty"`
	// Transport-specific settings are kept as raw JSON to avoid losing fields as
	// Xray evolves.
	//
	// This is especially important for XHTTP (SplitHTTP), where `xhttpSettings`
	// may include `extra`, `xmux`, `downloadSettings` and other nested objects
	// (see upstream discussion: https://github.com/XTLS/Xray-core/discussions/4113).
	WSSettings          json.RawMessage `json:"wsSettings,omitempty"`
	GRPCSettings        json.RawMessage `json:"grpcSettings,omitempty"`
	TCPSettings         *TCPSettings    `json:"tcpSettings,omitempty"`
	HTTPSettings        *HTTPSettings   `json:"httpSettings,omitempty"`
	KCPSettings         *KCPSettings    `json:"kcpSettings,omitempty"`
	QUICSettings        *QUICSettings   `json:"quicSettings,omitempty"`
	HTTPUpgradeSettings json.RawMessage `json:"httpupgradeSettings,omitempty"`
	XHTTPSettings       json.RawMessage `json:"xhttpSettings,omitempty"`
}

// TLSSettings holds TLS security configuration for stream settings.
type TLSSettings struct {
	ServerName    string      `json:"serverName,omitempty"`
	Fingerprint   string      `json:"fingerprint,omitempty"`
	ALPN          []string    `json:"alpn,omitempty"`
	AllowInsecure bool        `json:"allowInsecure,omitempty"`
	// PinnedPeerCertSha256 lists allowed leaf/CA SHA-256 fingerprints. Replaces
	// allowInsecure as the safe alternative after Xray-core v26.2.6 (auto-disable
	// of allowInsecure on UTC 2026-06-01). Shared via URI param `pcs`.
	PinnedPeerCertSha256 []string `json:"pinnedPeerCertSha256,omitempty"`
	// VerifyPeerCertByName accepts a server name to validate the leaf certificate
	// against, decoupled from SNI. Shared via URI param `vcn`.
	VerifyPeerCertByName string      `json:"verifyPeerCertByName,omitempty"`
	Certificates         interface{} `json:"certificates,omitempty"` // server-side only
}

// RealitySettings holds REALITY security configuration (server and client fields).
type RealitySettings struct {
	// Server-side fields
	Show         bool     `json:"show,omitempty"`
	Dest         string   `json:"dest,omitempty"`
	XVer         int      `json:"xver,omitempty"`
	ServerNames  []string `json:"serverNames,omitempty"`
	PrivateKey   string   `json:"privateKey,omitempty"`
	//nolint:revive // Keep Xray-compatible JSON field naming.
	ShortIds     []string `json:"shortIds,omitempty"`
	MLDSA65Seed  string   `json:"mldsa65Seed,omitempty"`
	MinClientVer string   `json:"minClientVer,omitempty"`
	MaxClientVer string   `json:"maxClientVer,omitempty"`
	MaxTimeDiff  int64    `json:"maxTimeDiff,omitempty"`
	// Stored alongside for convenience (or derived)
	PublicKey string `json:"publicKey,omitempty"`
	// Client-side fields
	ServerName    string `json:"serverName,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	//nolint:revive // Keep Xray-compatible JSON field naming.
	ShortId       string `json:"shortId,omitempty"`
	SpiderX       string `json:"spiderX,omitempty"`
	MLDSA65Verify string `json:"mldsa65Verify,omitempty"`
}

// WSSettings holds WebSocket transport configuration.
type WSSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// GRPCSettings holds gRPC transport configuration.
type GRPCSettings struct {
	ServiceName        string `json:"serviceName,omitempty"`
	MultiMode          bool   `json:"multiMode,omitempty"`
	IdleTimeout        int    `json:"idle_timeout,omitempty"`
	HealthCheckTimeout int    `json:"health_check_timeout,omitempty"`
}

// TCPSettings holds TCP transport header configuration.
type TCPSettings struct {
	Header interface{} `json:"header,omitempty"`
}

// HTTPSettings holds HTTP/2 transport host and path configuration.
type HTTPSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

// KCPSettings holds mKCP transport configuration.
type KCPSettings struct {
	MTU              int         `json:"mtu,omitempty"`
	TTI              int         `json:"tti,omitempty"`
	UplinkCapacity   int         `json:"uplinkCapacity,omitempty"`
	DownlinkCapacity int         `json:"downlinkCapacity,omitempty"`
	Congestion       bool        `json:"congestion,omitempty"`
	ReadBufferSize   int         `json:"readBufferSize,omitempty"`
	WriteBufferSize  int         `json:"writeBufferSize,omitempty"`
	Header           interface{} `json:"header,omitempty"`
	Seed             string      `json:"seed,omitempty"`
}

// QUICSettings holds QUIC transport configuration.
type QUICSettings struct {
	Security string      `json:"security,omitempty"`
	Key      string      `json:"key,omitempty"`
	Header   interface{} `json:"header,omitempty"`
}

// HTTPUpgradeSettings holds HTTP Upgrade transport configuration.
type HTTPUpgradeSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// XHTTPSettings holds XHTTP (SplitHTTP) transport configuration.
type XHTTPSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ─── Client-side config (subscription output) ────────────────────────────────

// ClientConfig is the full xray client configuration returned in subscription
type ClientConfig struct {
	Log       *LogConfig `json:"log"`
	DNS       *DNSConfig `json:"dns"`
	Inbounds  []Inbound  `json:"inbounds"`
	Outbounds []Outbound `json:"outbounds"`
	Observatory *ObservatoryConfig `json:"observatory,omitempty"`
	Routing   *Routing   `json:"routing"`
}

// ObservatoryConfig configures the xray observatory for latency-based routing.
type ObservatoryConfig struct {
	SubjectSelector   []string `json:"subjectSelector,omitempty"`
	ProbeURL          string   `json:"probeURL,omitempty"`
	ProbeInterval     string   `json:"probeInterval,omitempty"`
	EnableConcurrency bool     `json:"enableConcurrency,omitempty"`
}

// LogConfig sets the xray client log level.
type LogConfig struct {
	LogLevel string `json:"loglevel"`
}

// DNSConfig holds the DNS server list for the xray client.
type DNSConfig struct {
	Servers []interface{} `json:"servers"`
}

// DNSServer is a structured DNS server entry for use in DNSConfig.Servers.
// Plain string entries (IP addresses) are also valid; use this struct when
// domain-specific routing, IP validation, or fallback control is needed.
type DNSServer struct {
	Address      string   `json:"address"`
	Domains      []string `json:"domains,omitempty"`
	SkipFallback bool     `json:"skipFallback,omitempty"`
	ExpectIPs    []string `json:"expectIPs,omitempty"`
}

// Inbound (client local inbound - socks/http proxy)
type Inbound struct {
	Tag      string          `json:"tag"`
	Port     int             `json:"port"`
	Listen   string          `json:"listen,omitempty"`
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
	Sniffing *Sniffing       `json:"sniffing,omitempty"`
}

// Sniffing enables protocol detection on client local inbounds.
type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
}

// Outbound is a client-side proxy outbound
type Outbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Mux            *MuxConfig      `json:"mux,omitempty"`
}

// MuxConfig controls multiplexing on outbound connections.
type MuxConfig struct {
	Enabled     bool `json:"enabled"`
	Concurrency int  `json:"concurrency,omitempty"`
}

// VMessOutboundSettings holds VMess outbound server list for the client config.
type VMessOutboundSettings struct {
	Vnext []VMessServer `json:"vnext"`
}

// VMessServer is a VMess outbound server entry.
type VMessServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []VMessUser `json:"users"`
}

// VMessUser holds user credentials for a VMess outbound server.
type VMessUser struct {
	ID       string `json:"id"`
	//nolint:revive // Keep Xray-compatible JSON field naming.
	AlterId  int    `json:"alterId"`
	Security string `json:"security,omitempty"`
	Level    int    `json:"level,omitempty"`
}

// VLESSOutboundSettings holds VLESS outbound server list for the client config.
type VLESSOutboundSettings struct {
	Vnext []VLESSServer `json:"vnext"`
}

// VLESSServer is a VLESS outbound server entry.
type VLESSServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []VLESSUser `json:"users"`
}

// VLESSUser holds user credentials for a VLESS outbound server.
type VLESSUser struct {
	ID         string `json:"id"`
	Flow       string `json:"flow,omitempty"`
	Encryption string `json:"encryption"`
	Email      string `json:"email,omitempty"`
	Level      int    `json:"level,omitempty"`
	Testpre    uint32 `json:"testpre,omitempty"`
}

// TrojanOutboundSettings holds Trojan outbound server list for the client config.
type TrojanOutboundSettings struct {
	Servers []TrojanServer `json:"servers"`
}

// TrojanServer is a Trojan outbound server entry.
type TrojanServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Level    int    `json:"level,omitempty"`
}

// ShadowsocksOutboundSettings holds Shadowsocks outbound server list for the client config.
type ShadowsocksOutboundSettings struct {
	Servers []ShadowsocksServer `json:"servers"`
}

// ShadowsocksServer is a Shadowsocks outbound server entry.
type ShadowsocksServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Method   string `json:"method"`
	Password string `json:"password"`
	Level    int    `json:"level,omitempty"`
	UoT      bool   `json:"uot,omitempty"`
}

// SOCKSOutboundSettings holds SOCKS outbound server list for the client config.
type SOCKSOutboundSettings struct {
	Servers []SOCKSServer `json:"servers"`
}

// SOCKSServer is a SOCKS outbound server entry.
type SOCKSServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []SOCKSUser `json:"users,omitempty"`
}

// SOCKSUser holds user credentials for a SOCKS outbound server.
type SOCKSUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// Routing defines the xray routing configuration including rules and balancers.
type Routing struct {
	DomainStrategy string        `json:"domainStrategy,omitempty"`
	Balancers      []Balancer    `json:"balancers,omitempty"`
	Rules          []RoutingRule `json:"rules"`
}

// Balancer describes a named group of outbounds with a selection strategy.
type Balancer struct {
	Tag         string            `json:"tag"`
	Selector    []string          `json:"selector"`
	Strategy    *BalancerStrategy `json:"strategy,omitempty"`
	FallbackTag string            `json:"fallbackTag,omitempty"`
}

// BalancerStrategy specifies the strategy type for a balancer (e.g. leastPing, random).
type BalancerStrategy struct {
	Type string `json:"type"`
}

// RoutingRule defines a single routing rule matching inbound traffic to an outbound or balancer.
type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Network     string   `json:"network,omitempty"`
	Port        string   `json:"port,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
}

// StoredClientConfig holds per-user per-inbound credential data in DB
type StoredClientConfig struct {
	Protocol string `json:"protocol"`
	// VMess/VLESS
	ID      string `json:"id,omitempty"`
	//nolint:revive // Keep backward-compatible stored field naming.
	AlterId int    `json:"alter_id,omitempty"`
	Flow    string `json:"flow,omitempty"`
	Email   string `json:"email,omitempty"`
	// VLESS outbound encryption string. "none" for standard VLESS.
	// For VLESS Encryption (PR #5067): client-side string from vless_client_encryption config map.
	// Never stores the server-side decryption string (which contains private keys).
	Encryption string `json:"encryption,omitempty"`
	// Testpre instructs the client to maintain N pre-established TCP connections (0-RTT pool).
	// Read from inbound settings.testpre; 0 means disabled (field omitted from client config).
	Testpre uint32 `json:"testpre,omitempty"`
	// Trojan/SS/SOCKS
	Password string `json:"password,omitempty"`
	Method   string `json:"method,omitempty"`
	// SOCKS
	User string `json:"user,omitempty"`
}

// UnmarshalJSON keeps backward compatibility for VMess alterId naming.
// Some stored records may contain "alterId" while newer records use "alter_id".
func (s *StoredClientConfig) UnmarshalJSON(data []byte) error {
	type alias StoredClientConfig
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*s = StoredClientConfig(a)

	// Backward compatibility: accept camelCase key if snake_case is absent.
	if s.AlterId == 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil
		}
		if v, ok := raw["alterId"]; ok {
			var alterID int
			if err := json.Unmarshal(v, &alterID); err == nil {
				s.AlterId = alterID
			}
		}
	}
	return nil
}
