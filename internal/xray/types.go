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

// Protocol-specific inbound settings
type VMessInboundSettings struct {
	Clients []VMessClient `json:"clients"`
}

type VMessClient struct {
	ID      string `json:"id"`
	AlterId int    `json:"alterId,omitempty"`
	Email   string `json:"email,omitempty"`
	Level   int    `json:"level,omitempty"`
}

type VLESSInboundSettings struct {
	Clients    []VLESSClient `json:"clients"`
	Decryption string        `json:"decryption,omitempty"`
}

type VLESSClient struct {
	ID    string `json:"id"`
	Flow  string `json:"flow,omitempty"`
	Email string `json:"email,omitempty"`
	Level int    `json:"level,omitempty"`
}

type TrojanInboundSettings struct {
	Clients []TrojanClient `json:"clients"`
}

type TrojanClient struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type ShadowsocksInboundSettings struct {
	Method   string               `json:"method,omitempty"`
	Password string               `json:"password,omitempty"`
	Clients  []ShadowsocksClient  `json:"clients,omitempty"`
	Network  string               `json:"network,omitempty"`
}

type ShadowsocksClient struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Method   string `json:"method,omitempty"`
}

type SOCKSInboundSettings struct {
	Auth     string         `json:"auth,omitempty"`
	Accounts []SOCKSAccount `json:"accounts,omitempty"`
	UDP      bool           `json:"udp,omitempty"`
}

type SOCKSAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// ─── Stream Settings (shared server+client) ────────────────────────────────

type StreamSettings struct {
	Network             string               `json:"network,omitempty"`
	Security            string               `json:"security,omitempty"`
	TLSSettings         *TLSSettings         `json:"tlsSettings,omitempty"`
	RealitySettings     *RealitySettings     `json:"realitySettings,omitempty"`
	WSSettings          *WSSettings          `json:"wsSettings,omitempty"`
	GRPCSettings        *GRPCSettings        `json:"grpcSettings,omitempty"`
	TCPSettings         *TCPSettings         `json:"tcpSettings,omitempty"`
	HTTPSettings        *HTTPSettings        `json:"httpSettings,omitempty"`
	KCPSettings         *KCPSettings         `json:"kcpSettings,omitempty"`
	QUICSettings        *QUICSettings        `json:"quicSettings,omitempty"`
	HTTPUpgradeSettings *HTTPUpgradeSettings `json:"httpupgradeSettings,omitempty"`
	XHTTPSettings       *XHTTPSettings       `json:"xhttpSettings,omitempty"`
}

type TLSSettings struct {
	ServerName    string        `json:"serverName,omitempty"`
	Fingerprint   string        `json:"fingerprint,omitempty"`
	ALPN          []string      `json:"alpn,omitempty"`
	AllowInsecure bool          `json:"allowInsecure,omitempty"`
	Certificates  interface{}   `json:"certificates,omitempty"` // server-side only
}

type RealitySettings struct {
	// Server-side fields
	Show         bool     `json:"show,omitempty"`
	Dest         string   `json:"dest,omitempty"`
	XVer         int      `json:"xver,omitempty"`
	ServerNames  []string `json:"serverNames,omitempty"`
	PrivateKey   string   `json:"privateKey,omitempty"`
	ShortIds     []string `json:"shortIds,omitempty"`
	MinClientVer string   `json:"minClientVer,omitempty"`
	MaxClientVer string   `json:"maxClientVer,omitempty"`
	MaxTimeDiff  int64    `json:"maxTimeDiff,omitempty"`
	// Stored alongside for convenience (or derived)
	PublicKey    string   `json:"publicKey,omitempty"`
	// Client-side fields
	ServerName   string   `json:"serverName,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	ShortId      string   `json:"shortId,omitempty"`
	SpiderX      string   `json:"spiderX,omitempty"`
}

type WSSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type GRPCSettings struct {
	ServiceName    string `json:"serviceName,omitempty"`
	MultiMode      bool   `json:"multiMode,omitempty"`
	IdleTimeout    int    `json:"idle_timeout,omitempty"`
	HealthCheckTimeout int `json:"health_check_timeout,omitempty"`
}

type TCPSettings struct {
	Header interface{} `json:"header,omitempty"`
}

type HTTPSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

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

type QUICSettings struct {
	Security string      `json:"security,omitempty"`
	Key      string      `json:"key,omitempty"`
	Header   interface{} `json:"header,omitempty"`
}

type HTTPUpgradeSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type XHTTPSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ─── Client-side config (subscription output) ────────────────────────────────

// ClientConfig is the full xray client configuration returned in subscription
type ClientConfig struct {
	Log       *LogConfig   `json:"log"`
	DNS       *DNSConfig   `json:"dns"`
	Inbounds  []Inbound    `json:"inbounds"`
	Outbounds []Outbound   `json:"outbounds"`
	Routing   *Routing     `json:"routing"`
}

type LogConfig struct {
	LogLevel string `json:"loglevel"`
}

type DNSConfig struct {
	Servers []interface{} `json:"servers"`
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

type MuxConfig struct {
	Enabled     bool `json:"enabled"`
	Concurrency int  `json:"concurrency,omitempty"`
}

// Protocol-specific outbound settings
type VMessOutboundSettings struct {
	Vnext []VMessServer `json:"vnext"`
}

type VMessServer struct {
	Address string        `json:"address"`
	Port    int           `json:"port"`
	Users   []VMessUser   `json:"users"`
}

type VMessUser struct {
	ID       string `json:"id"`
	AlterId  int    `json:"alterId"`
	Security string `json:"security,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type VLESSOutboundSettings struct {
	Vnext []VLESSServer `json:"vnext"`
}

type VLESSServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []VLESSUser `json:"users"`
}

type VLESSUser struct {
	ID         string `json:"id"`
	Flow       string `json:"flow,omitempty"`
	Encryption string `json:"encryption"`
	Level      int    `json:"level,omitempty"`
}

type TrojanOutboundSettings struct {
	Servers []TrojanServer `json:"servers"`
}

type TrojanServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Level    int    `json:"level,omitempty"`
}

type ShadowsocksOutboundSettings struct {
	Servers []ShadowsocksServer `json:"servers"`
}

type ShadowsocksServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Method   string `json:"method"`
	Password string `json:"password"`
	Level    int    `json:"level,omitempty"`
	UoT      bool   `json:"uot,omitempty"`
}

type SOCKSOutboundSettings struct {
	Servers []SOCKSServer `json:"servers"`
}

type SOCKSServer struct {
	Address string        `json:"address"`
	Port    int           `json:"port"`
	Users   []SOCKSUser   `json:"users,omitempty"`
}

type SOCKSUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// Routing
type Routing struct {
	DomainStrategy string        `json:"domainStrategy,omitempty"`
	Rules          []RoutingRule `json:"rules"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Network     string   `json:"network,omitempty"`
	Port        string   `json:"port,omitempty"`
}

// StoredClientConfig holds per-user per-inbound credential data in DB
type StoredClientConfig struct {
	Protocol string `json:"protocol"`
	// VMess/VLESS
	ID      string `json:"id,omitempty"`
	AlterId int    `json:"alter_id,omitempty"`
	Flow    string `json:"flow,omitempty"`
	// Trojan/SS/SOCKS
	Password string `json:"password,omitempty"`
	Method   string `json:"method,omitempty"`
	// SOCKS
	User string `json:"user,omitempty"`
}
