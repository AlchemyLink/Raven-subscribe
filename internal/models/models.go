package models

import "time"

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Token     string    `json:"token"`
	Enabled   bool      `json:"enabled"`
	ClientRoutes string `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// CreateUserRequest is the API request body for creating a user
type CreateUserRequest struct {
	Username string `json:"username"`
}

type UserResponse struct {
	User   User   `json:"user"`
	SubURL string `json:"sub_url"`
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
