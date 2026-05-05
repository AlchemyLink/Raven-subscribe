package core

import "github.com/alchemylink/raven-subscribe/internal/models"

// BuildRequest is the full input the API layer hands the engine to render a
// per-user client config. Every field is engine-agnostic in name; engines may
// ignore fields they don't support (e.g. an engine without balancer support
// silently drops BalancerSpec).
//
// The fields mirror the current xray.GenerateClientConfig signature so Phase 2
// can adapt without renaming any data on the API side.
type BuildRequest struct {
	ServerHost     string
	InboundHosts   map[string]string
	InboundPorts   map[string]int
	User           models.User
	Clients        []models.UserClientFull
	GlobalRoutes   string // JSON array of route rules; engine-agnostic shape
	Balancer       BalancerSpec
	LocalProxy     LocalProxySpec
	DNS            []any // generic; engines accept Xray-shaped DNS objects today
	BlackholeReply string
}

// BalancerSpec describes outbound load-balancing policy if the engine supports
// it. An empty Strategy disables the balancer.
type BalancerSpec struct {
	Strategy      string // "random" | "roundRobin" | "leastPing" | "leastLoad" | ""
	ProbeURL      string
	ProbeInterval string
}

// LocalProxySpec describes the local SOCKS/HTTP listeners the client app
// exposes after connecting. Zero ports disable the corresponding listener.
type LocalProxySpec struct {
	SOCKSPort int
	HTTPPort  int
}

// EngineConfig is the opaque per-user output of ClientConfigBuilder.Build.
// Callers obtain wire bytes via Render and per-inbound URIs via Outbounds;
// they do not type-assert into the concrete engine struct.
//
// Render must be deterministic: identical BuildRequest + identical engine
// version produce byte-identical output for ViewFullJSON.
type EngineConfig interface {
	Render(view View) ([]byte, error)
	Outbounds() []OutboundLink
}

// ClientConfigBuilder turns a BuildRequest into an EngineConfig. The
// implementation is responsible for engine-specific concerns: REALITY key
// derivation, transport conversions, routing-rule resolution, balancer
// wiring, and per-protocol URI emission.
type ClientConfigBuilder interface {
	Build(req BuildRequest) (EngineConfig, error)
}
