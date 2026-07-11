// Package models defines the shared data structures used across the application.
package models

import (
	"strings"
	"time"
)

// User represents a subscription user stored in the database.
type User struct {
	ID              int64      `json:"id"`
	Username        string     `json:"username"`
	Email           string     `json:"-"` // Xray client email / monitoring; not exposed in API JSON (use username)
	Token           string     `json:"token"`
	FallbackToken   string     `json:"fallback_token"`
	FallbackAccessedAt *time.Time `json:"fallback_accessed_at,omitempty"`
	Enabled         bool       `json:"enabled"`
	// Hy2Enabled gates the per-user Hysteria2 reserve (/sub/{token}/hy2 and the hy2 URI
	// folded into the main link-list). Defaults to true for all users (DB column DEFAULT 1).
	Hy2Enabled      bool       `json:"hy2_enabled"`
	ClientRoutes    string     `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ClientIdentity returns the Xray client "email" (API + JSON configs). Prefers Email when set.
func (u *User) ClientIdentity() string {
	if u == nil {
		return ""
	}
	e := strings.TrimSpace(u.Email)
	if e != "" {
		return e
	}
	return strings.TrimSpace(u.Username)
}

// Inbound represents a parsed xray server inbound stored in DB
type Inbound struct {
	ID         int64     `json:"id"`
	Tag        string    `json:"tag"`
	Protocol   string    `json:"protocol"`
	Port       int       `json:"port"`
	ConfigFile string    `json:"config_file"`
	RawConfig  string    `json:"raw_config"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Node is a single Xray node in a multi-node topology (the nodes table). It
// mirrors the DB row; deploy/allow_public_grpc are config-only provisioning
// concerns and deliberately not stored here. See docs/multi-node-design.md §5.
type Node struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	APIAddr    string    `json:"api_addr"`
	InboundTag string    `json:"inbound_tag"`
	PublicHost string    `json:"public_host"`
	PublicPort int       `json:"public_port"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// WantedClient is one (identity, credential) the multi-node reconcile expects
// to exist on a node: an enabled user placed on the node who holds an enabled
// credential for the node's inbound_tag. Used as the "want" set against the
// node's runtime "have" set from GetInboundUsers.
type WantedClient struct {
	Email        string
	ClientConfig string
}

// UserClient maps a user to their credentials in a specific inbound
type UserClient struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"user_id"`
	InboundID    int64  `json:"inbound_id"`
	ClientConfig string `json:"client_config"` // protocol-specific JSON credentials
	Enabled      bool   `json:"enabled"`
}

// UserClientFull joins UserClient with Inbound data for config generation
type UserClientFull struct {
	UserClient
	InboundTag      string `json:"inbound_tag"`
	InboundProtocol string `json:"inbound_protocol"`
	InboundPort     int    `json:"inbound_port"`
	InboundRaw      string `json:"inbound_raw"`

	// Multi-node generation hints. Populated only by the node expansion in the
	// API layer (docs/multi-node-design.md §6.3); empty in single-node mode.
	// When NodeHost is set the generator emits this client's outbound pointing
	// at that node's public endpoint instead of server_host, so a user placed
	// on N nodes yields N balanced outbounds. NodeName is diagnostic only.
	NodeName string `json:"-"`
	NodeHost string `json:"-"`
	NodePort int    `json:"-"`
}

// InboundSpec specifies an inbound to add the user to. Protocol is optional — derived from tag via DB/config when omitted.
type InboundSpec struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol,omitempty"`
}

// CreateUserRequest is the API request body for creating a user
type CreateUserRequest struct {
	Username string        `json:"username"`
	Inbounds []InboundSpec `json:"inbounds,omitempty"` // Optional: inbounds to add user to. If empty, uses api_user_inbound_tag from config.
	// Nodes optionally places the user on specific nodes by name (multi-node
	// only). Empty => all enabled nodes (default policy). Ignored single-node.
	Nodes []string `json:"nodes,omitempty"`
}

// SubURLs holds all subscription URL variants for a user.
type SubURLs struct {
	Full        string `json:"full"`
	LinksText   string `json:"links_txt"`
	LinksB64    string `json:"links_b64"`
	Compact     string `json:"compact"`
	CompactText string `json:"compact_txt"`
	CompactB64  string `json:"compact_b64"`
	// Hy2 is the per-user Hysteria2 reserve URL (/sub/{token}/hy2). Present only when the
	// hysteria reserve is enabled in config. Surfaced in the dashboard + Telegram /links.
	Hy2 string `json:"hy2,omitempty"`
	// Fallback variants — keyed on fallback_token, never rotated with primary token.
	Fallback            string `json:"fallback,omitempty"`
	FallbackText        string `json:"fallback_txt,omitempty"`
	FallbackB64         string `json:"fallback_b64,omitempty"`
	FallbackCompact     string `json:"fallback_compact,omitempty"`
	FallbackCompactText string `json:"fallback_compact_txt,omitempty"`
	FallbackCompactB64  string `json:"fallback_compact_b64,omitempty"`
}

// UserResponse is the API response body returned when a user is created or fetched.
type UserResponse struct {
	User    User    `json:"user"`
	SubURL  string  `json:"sub_url"`
	SubURLs SubURLs `json:"sub_urls"`
}

// EmergencyProfile defines a named fallback inbound set used during a blocking event.
// When activated, subscription endpoints serve only the listed inbound tags instead
// of the user's normal inbounds.
type EmergencyProfile struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	InboundTags []string  `json:"inbound_tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// EmergencyStatus is the current state of the emergency bypass mode.
type EmergencyStatus struct {
	Active      bool              `json:"active"`
	ProfileID   *int64            `json:"profile_id,omitempty"`
	Profile     *EmergencyProfile `json:"profile,omitempty"`
	ActivatedAt *time.Time        `json:"activated_at,omitempty"`
}

// UserRouteRule describes a user-defined client routing rule.
// OutboundTag is restricted to: direct, proxy, block.
type UserRouteRule struct {
	ID         string   `json:"id,omitempty"`
	Type       string   `json:"type,omitempty"`
	OutboundTag string   `json:"outboundTag"`
	Domain     []string `json:"domain,omitempty"`
	IP         []string `json:"ip,omitempty"`
	Network    string   `json:"network,omitempty"`
	Port       string   `json:"port,omitempty"`
	Protocol   []string `json:"protocol,omitempty"`
	InboundTag []string `json:"inboundTag,omitempty"`
}
