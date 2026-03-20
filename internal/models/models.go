// Package models defines the shared data structures used across the application.
package models

import (
	"strings"
	"time"
)

// User represents a subscription user stored in the database.
type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"-"` // Xray client email / monitoring; not exposed in API JSON (use username)
	Token     string    `json:"token"`
	Enabled   bool      `json:"enabled"`
	ClientRoutes string `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
}

// InboundSpec specifies an inbound to add the user to. Protocol is optional — derived from tag via DB/config when omitted.
type InboundSpec struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol,omitempty"`
}

// CreateUserRequest is the API request body for creating a user
type CreateUserRequest struct {
	Username string         `json:"username"`
	Inbounds []InboundSpec  `json:"inbounds,omitempty"` // Optional: inbounds to add user to. If empty, uses api_user_inbound_tag from config.
}

// SubURLs holds all subscription URL variants for a user.
type SubURLs struct {
	Full        string `json:"full"`
	LinksText   string `json:"links_txt"`
	LinksB64    string `json:"links_b64"`
	Compact     string `json:"compact"`
	CompactText string `json:"compact_txt"`
	CompactB64  string `json:"compact_b64"`
}

// UserResponse is the API response body returned when a user is created or fetched.
type UserResponse struct {
	User    User    `json:"user"`
	SubURL  string  `json:"sub_url"`
	SubURLs SubURLs `json:"sub_urls"`
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
