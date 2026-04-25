// Package api implements the HTTP API server for xray-subscription management.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/alchemylink/raven-subscribe/internal/xray"
)

// Syncer interface so we don't import the syncer package (avoid circular deps)
type Syncer interface {
	Sync() error
}

// Server is the HTTP API server holding shared dependencies.
type Server struct {
	cfg    *config.Config
	db     *database.DB
	syncer Syncer
}

// NewServer creates a new Server with the given config, database, and syncer.
func NewServer(cfg *config.Config, db *database.DB, syncer Syncer) *Server {
	return &Server{cfg: cfg, db: db, syncer: syncer}
}

// Router builds and returns the configured HTTP router.
func (s *Server) Router() http.Handler {
	r := mux.NewRouter()

	// ── Fallback subscription endpoints (stable token, never rotated) ────────
	// Mirrors the primary /sub/* and /c/* route set, keyed on fallback_token.
	fb := s.withFallbackAuth
	r.HandleFunc("/sub/fallback/{token}", fb(s.handleSubscription)).Methods(http.MethodGet)
	r.HandleFunc("/sub/fallback/{token}/links.txt", fb(s.handleSubscriptionLinksText)).Methods(http.MethodGet)
	r.HandleFunc("/sub/fallback/{token}/links.b64", fb(s.handleSubscriptionLinksB64)).Methods(http.MethodGet)
	r.HandleFunc("/c/fallback/{token}", fb(s.handleCompactSubscription)).Methods(http.MethodGet)
	r.HandleFunc("/c/fallback/{token}/links.txt", fb(s.handleCompactSubscriptionLinksText)).Methods(http.MethodGet)
	r.HandleFunc("/c/fallback/{token}/links.b64", fb(s.handleCompactSubscriptionLinksB64)).Methods(http.MethodGet)

	// ── Subscription endpoint (public, authenticated by token) ──────────────
	r.HandleFunc("/sub/{token}", s.handleSubscription).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links", s.handleSubscriptionLinks).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links.txt", s.handleSubscriptionLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links.b64", s.handleSubscriptionLinksB64).Methods(http.MethodGet)

	// ── Compact subscription endpoint — always serves lightweight config ──────
	// /c/{token}        → full Xray JSON, geo-selectors stripped
	// /c/{token}/links.txt → plain share links, geo-selectors stripped
	// /c/{token}/links.b64 → base64 share links, geo-selectors stripped
	r.HandleFunc("/c/{token}", s.handleCompactSubscription).Methods(http.MethodGet)
	r.HandleFunc("/c/{token}/links.txt", s.handleCompactSubscriptionLinksText).Methods(http.MethodGet)
	r.HandleFunc("/c/{token}/links.b64", s.handleCompactSubscriptionLinksB64).Methods(http.MethodGet)
	// ── sing-box / Hysteria2 subscription endpoints ───────────────────────────
	// /sub/{token}/singbox      → sing-box JSON with Hysteria2 outbounds only
	// /sub/{token}/hysteria2    → hysteria2:// share links (plain text)
	// /sub/{token}/hysteria2.b64 → hysteria2:// share links (base64)
	r.HandleFunc("/sub/{token}/singbox", s.handleSingboxSubscription).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/hysteria2", s.handleHysteria2LinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/hysteria2.b64", s.handleHysteria2LinksB64).Methods(http.MethodGet)

	r.HandleFunc("/sub/{token}/vless", s.handleVLESSLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vless.b64", s.handleVLESSLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vless/list", s.handleVLESSList).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vless/{vlessTag}", s.handleVLESSLinkByTagText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vless/{vlessTag}/b64", s.handleVLESSLinkByTagB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vmess", s.handleVMessLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/vmess.b64", s.handleVMessLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/trojan", s.handleTrojanLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/trojan.b64", s.handleTrojanLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/ss", s.handleShadowsocksLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/ss.b64", s.handleShadowsocksLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/shadowsocks", s.handleShadowsocksLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/shadowsocks.b64", s.handleShadowsocksLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/protocol/{protocol}", s.handleSubscription).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/protocol/{protocol}/links.txt", s.handleSubscriptionLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/protocol/{protocol}/links.b64", s.handleSubscriptionLinksB64).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/inbound/{inboundTag}", s.handleSubscription).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/inbound/{inboundTag}/links.txt", s.handleSubscriptionLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/inbound/{inboundTag}/links.b64", s.handleSubscriptionLinksB64).Methods(http.MethodGet)

	// ── Admin API (protected by admin token header) ───────────────────────
	api := r.PathPrefix("/api").Subrouter()
	api.Use(s.adminAuth)

	api.HandleFunc("/users", s.listUsers).Methods(http.MethodGet)
	api.HandleFunc("/users", s.createUser).Methods(http.MethodPost)
	api.HandleFunc("/users/{id}", s.getUser).Methods(http.MethodGet)
	api.HandleFunc("/users/{id}", s.deleteUser).Methods(http.MethodDelete)
	api.HandleFunc("/users/{id}/enable", s.enableUser).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/disable", s.disableUser).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/token", s.regenerateToken).Methods(http.MethodPost)
	api.HandleFunc("/users/{id}/routes", s.getUserRoutes).Methods(http.MethodGet)
	api.HandleFunc("/users/{id}/routes", s.addUserRoute).Methods(http.MethodPost)
	api.HandleFunc("/users/{id}/routes", s.setUserRoutes).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/routes/{index}", s.updateUserRoute).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/routes/{index}", s.deleteUserRoute).Methods(http.MethodDelete)
	api.HandleFunc("/users/{id}/routes/id/{routeId}", s.updateUserRouteByID).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/routes/id/{routeId}", s.deleteUserRouteByID).Methods(http.MethodDelete)
	api.HandleFunc("/users/{id}/clients", s.getUserClients).Methods(http.MethodGet)
	api.HandleFunc("/users/{id}/clients", s.addUserClient).Methods(http.MethodPost)
	api.HandleFunc("/users/{id}/clients/{inboundId}/enable", s.enableUserClient).Methods(http.MethodPut)
	api.HandleFunc("/users/{id}/clients/{inboundId}/disable", s.disableUserClient).Methods(http.MethodPut)

	api.HandleFunc("/inbounds", s.listInbounds).Methods(http.MethodGet)
	api.HandleFunc("/routes/global", s.getGlobalRoutes).Methods(http.MethodGet)
	api.HandleFunc("/routes/global", s.setGlobalRoutes).Methods(http.MethodPut)
	api.HandleFunc("/routes/global", s.addGlobalRoute).Methods(http.MethodPost)
	api.HandleFunc("/routes/global", s.deleteGlobalRoutes).Methods(http.MethodDelete)
	api.HandleFunc("/config/balancer", s.getBalancerConfig).Methods(http.MethodGet)
	api.HandleFunc("/config/balancer", s.setBalancerConfig).Methods(http.MethodPut)
	api.HandleFunc("/sync", s.triggerSync).Methods(http.MethodPost)

	// ── Fallback management ───────────────────────────────────────────────────
	api.HandleFunc("/users/{id}/fallback/token", s.regenerateFallbackToken).Methods(http.MethodPost)
	api.HandleFunc("/fallback/status", s.getFallbackStatus).Methods(http.MethodGet)
	api.HandleFunc("/fallback/enable", s.enableFallback).Methods(http.MethodPost)
	api.HandleFunc("/fallback/disable", s.disableFallback).Methods(http.MethodPost)
	api.HandleFunc("/fallback/accessed", s.listFallbackAccessedUsers).Methods(http.MethodGet)

	// ── Emergency config rotation ─────────────────────────────────────────────
	api.HandleFunc("/emergency/status", s.getEmergencyStatus).Methods(http.MethodGet)
	api.HandleFunc("/emergency/activate", s.activateEmergency).Methods(http.MethodPost)
	api.HandleFunc("/emergency/deactivate", s.deactivateEmergency).Methods(http.MethodPost)
	api.HandleFunc("/emergency/profiles", s.listEmergencyProfiles).Methods(http.MethodGet)
	api.HandleFunc("/emergency/profiles", s.createEmergencyProfile).Methods(http.MethodPost)
	api.HandleFunc("/emergency/profiles/{id:[0-9]+}", s.getEmergencyProfileByID).Methods(http.MethodGet)
	api.HandleFunc("/emergency/profiles/{id:[0-9]+}", s.updateEmergencyProfile).Methods(http.MethodPut)
	api.HandleFunc("/emergency/profiles/{id:[0-9]+}", s.deleteEmergencyProfile).Methods(http.MethodDelete)

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]string{"status": "ok"})
	}).Methods(http.MethodGet)

	return s.rateLimitWrap(r)
}

func (s *Server) rateLimitWrap(next http.Handler) http.Handler {
	subRL := rateLimitMiddleware(s.cfg.RateLimitSubPerMin)
	adminRL := rateLimitMiddleware(s.cfg.RateLimitAdminPerMin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/api") {
			adminRL(next).ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/sub") || strings.HasPrefix(path, "/c") {
			subRL(next).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Middleware ───────────────────────────────────────────────────────────────

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken == "" {
			// No token configured — lock the API entirely rather than opening it.
			// Set admin_token in config.json to enable the admin API.
			jsonError(w, "admin API disabled: admin_token not configured", http.StatusServiceUnavailable)
			return
		}
		token := r.Header.Get("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Subscription endpoint ────────────────────────────────────────────────────

// handleSubscription returns a complete xray client config JSON for the user
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	if token == "" {
		jsonError(w, "missing token", http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil {
		jsonError(w, "invalid token", http.StatusNotFound)
		return
	}
	if !user.Enabled {
		jsonError(w, "user disabled", http.StatusForbidden)
		return
	}

	clients, err := s.db.GetUserClients(user.ID)
	if err != nil {
		// #nosec G706 -- username is sanitized before logging.
		log.Printf("ERROR get user clients for %s: %v", sanitizeLogField(user.Username), err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Emergency mode: when active, restrict clients to the emergency profile's inbound tags.
	// Falls back to normal clients if the user has no emergency inbounds enrolled.
	if em, err := s.db.GetEmergencyStatus(); err == nil && em.Active {
		w.Header().Set("X-Emergency-Mode", "active")
		if em.Profile != nil && len(em.Profile.InboundTags) > 0 {
			tagSet := make(map[string]bool, len(em.Profile.InboundTags))
			for _, tag := range em.Profile.InboundTags {
				tagSet[tag] = true
			}
			var emergency []models.UserClientFull
			for _, c := range clients {
				if tagSet[c.InboundTag] {
					emergency = append(emergency, c)
				}
			}
			if len(emergency) > 0 {
				clients = emergency
			}
		}
	}

	// Optional filters allow exposing one protocol / inbound tag per subscription URL.
	// Examples:
	//   /sub/<token>?protocol=vless
	//   /sub/<token>?inbound_tag=vless-xhttp-in-1
	if p := extractProtocolFilter(r); p != "" {
		filtered := make([]models.UserClientFull, 0, len(clients))
		for _, c := range clients {
			if strings.EqualFold(c.InboundProtocol, p) {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
	}
	if t := extractInboundTagFilter(r); t != "" {
		filtered := make([]models.UserClientFull, 0, len(clients))
		for i, c := range clients {
			if matchesInboundTagFilter(c.InboundTag, t, i) {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
	}
	if len(clients) == 0 {
		jsonError(w, "no enabled clients matched filters", http.StatusNotFound)
		return
	}

	globalRoutesJSON, err := s.db.GetGlobalClientRoutes()
	if err != nil {
		log.Printf("ERROR get global routes: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	balancerStrategy, balancerProbeURL, balancerProbeInterval, err := s.getEffectiveBalancerConfig()
	if err != nil {
		log.Printf("ERROR get balancer config: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	cfg, err := xray.GenerateClientConfig(
		s.cfg.ServerHost,
		s.cfg.InboundHosts,
		s.cfg.InboundPorts,
		*user,
		clients,
		globalRoutesJSON,
		balancerStrategy,
		balancerProbeURL,
		balancerProbeInterval,
		s.cfg.SocksInboundPort,
		s.cfg.HTTPInboundPort,
		s.cfg.ClientDNSServers,
		s.cfg.ClientBlackholeResponse,
	)
	if err != nil {
		// #nosec G706 -- username is sanitized before logging.
		log.Printf("ERROR generate config for %s: %v", sanitizeLogField(user.Username), err)
		jsonError(w, "could not generate config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if isMobileProfileRequest(r) {
		applyMobileRoutingProfile(cfg)
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "v2box" || format == "links" || format == "links.txt" {
		writeProxyLinksText(w, user.Username, cfg)
		return
	}
	if format == "b64" || format == "links.b64" {
		writeProxyLinksB64(w, user.Username, cfg)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="xray-config.json"`)
	w.Header().Set("Profile-Title", user.Username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		// #nosec G706 -- username is sanitized before logging.
		log.Printf("ERROR encode subscription response for %s: %v", sanitizeLogField(user.Username), err)
	}
}

// handleCompactSubscription serves /c/{token} — same as /sub/{token} but always applies
// the lightweight routing profile (geo-selectors stripped). Intended as the primary
// subscription URL for all clients.
func (s *Server) handleCompactSubscription(w http.ResponseWriter, r *http.Request) {
	r2 := r.WithContext(r.Context())
	q := r2.URL.Query()
	q.Set("profile", "mobile")
	r2.URL.RawQuery = q.Encode()
	s.handleSubscription(w, r2)
}

// handleCompactSubscriptionLinksText serves /c/{token}/links.txt — lightweight plain-text links.
func (s *Server) handleCompactSubscriptionLinksText(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("profile", "mobile")
	r2 := r.WithContext(r.Context())
	r2.URL.RawQuery = q.Encode()
	s.handleSubscriptionLinksText(w, r2)
}

// handleCompactSubscriptionLinksB64 serves /c/{token}/links.b64 — lightweight base64 links.
func (s *Server) handleCompactSubscriptionLinksB64(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("profile", "mobile")
	r2 := r.WithContext(r.Context())
	r2.URL.RawQuery = q.Encode()
	s.handleSubscriptionLinksB64(w, r2)
}

// handleSubscriptionLinks returns helper URLs to subscribe by protocol or inbound tag.
func (s *Server) handleSubscriptionLinks(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	if token == "" {
		jsonError(w, "missing token", http.StatusBadRequest)
		return
	}

	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil {
		jsonError(w, "invalid token", http.StatusNotFound)
		return
	}
	if !user.Enabled {
		jsonError(w, "user disabled", http.StatusForbidden)
		return
	}

	clients, err := s.db.GetUserClients(user.ID)
	if err != nil {
		// #nosec G706 -- username is sanitized before logging.
		log.Printf("ERROR get user clients for %s: %v", sanitizeLogField(user.Username), err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(clients) == 0 {
		jsonError(w, "no enabled clients", http.StatusNotFound)
		return
	}

	baseSubURL := s.cfg.SubURL(token)

	protocolSet := map[string]struct{}{}
	inboundTagSet := map[string]struct{}{}
	for _, c := range clients {
		p := strings.ToLower(strings.TrimSpace(c.InboundProtocol))
		if p != "" {
			protocolSet[p] = struct{}{}
		}
		t := strings.TrimSpace(c.InboundTag)
		if t != "" {
			inboundTagSet[t] = struct{}{}
		}
	}

	protocols := make([]string, 0, len(protocolSet))
	for p := range protocolSet {
		protocols = append(protocols, p)
	}
	sort.Strings(protocols)

	inboundTags := make([]string, 0, len(inboundTagSet))
	for t := range inboundTagSet {
		inboundTags = append(inboundTags, t)
	}
	sort.Strings(inboundTags)

	type namedLink struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	resp := struct {
		Username     string      `json:"username"`
		All          string      `json:"all"`
		ByProtocol   []namedLink `json:"by_protocol"`
		ByInboundTag []namedLink `json:"by_inbound_tag"`
	}{
		Username: user.Username,
		All:      baseSubURL,
	}

	for _, p := range protocols {
		resp.ByProtocol = append(resp.ByProtocol, namedLink{
			Name: p,
			URL:  withSubQuery(baseSubURL, "protocol", p),
		})
	}
	for _, t := range inboundTags {
		resp.ByInboundTag = append(resp.ByInboundTag, namedLink{
			Name: t,
			URL:  withSubQuery(baseSubURL, "inbound_tag", t),
		})
	}

	jsonOK(w, resp)
}

func withSubQuery(base, key, value string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

// ─── User handlers ────────────────────────────────────────────────────────────

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, usePagination := parsePagination(r)

	var users []models.User
	var err error
	if usePagination {
		users, err = s.db.ListUsersPaginated(limit, offset)
	} else {
		users, err = s.db.ListUsers()
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var resp []models.UserResponse
	for _, u := range users {
		resp = append(resp, models.UserResponse{
			User:    u,
			SubURL:  s.cfg.SubURL(u.Token),
			SubURLs: s.cfg.SubURLsWithFallback(u.Token, u.FallbackToken),
		})
	}

	if usePagination {
		total, err := s.db.CountUsers()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"items":  resp,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	} else {
		jsonOK(w, resp)
	}
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req models.CreateUserRequest
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&req); err != nil {
		jsonError(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if err := validateUsername(req.Username); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(req.Username)

	// Check duplicate
	existing, _ := s.db.GetUserByUsername(username)
	if existing != nil {
		jsonError(w, "username already exists", http.StatusConflict)
		return
	}

	token := generateToken()
	fallbackToken := generateToken()
	// Email in DB mirrors username for Xray; not accepted on create and not exposed in API JSON.
	user, err := s.db.CreateUser(username, "", token, fallbackToken)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build list of inbounds to add user to
	var inboundsToAdd []models.InboundSpec
	if len(req.Inbounds) > 0 {
		inboundsToAdd = req.Inbounds
	} else if tag := strings.TrimSpace(s.cfg.APIUserInboundTag); tag != "" {
		protocol := strings.TrimSpace(s.cfg.APIUserInboundProtocol)
		inboundsToAdd = []models.InboundSpec{{Tag: tag, Protocol: protocol}}
	}

	inbounds, _ := s.db.ListInbounds()
	for _, spec := range inboundsToAdd {
		tag := strings.TrimSpace(spec.Tag)
		if tag == "" {
			continue
		}
		protocol := strings.TrimSpace(spec.Protocol)
		if protocol == "" {
			for _, ib := range inbounds {
				if ib.Tag == tag {
					protocol = ib.Protocol
					break
				}
			}
		}
		if protocol == "" {
			protocol = strings.TrimSpace(s.cfg.APIUserInboundProtocol)
		}
		s.addUserToInbound(user, tag, protocol)
	}

	if len(inboundsToAdd) > 0 && s.cfg.XrayAPIAddr == "" {
		go func() { _ = s.syncer.Sync() }()
	}

	jsonOK(w, models.UserResponse{User: *user, SubURL: s.cfg.SubURL(user.Token), SubURLs: s.cfg.SubURLsWithFallback(user.Token, user.FallbackToken)})
}

// addUserToInbound adds a user to one inbound (Xray + DB). Used when creating users.
func (s *Server) addUserToInbound(user *models.User, tag, protocolFallback string) {
	identity := user.ClientIdentity()
	clientEncStr := s.cfg.VLESSClientEncryption[tag]
	var clientConfig string
	var err error
	if apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr); apiAddr != "" {
		clientConfig, err = xray.AddClientToInboundViaAPI(apiAddr, s.cfg.ConfigDir, tag, identity, protocolFallback, clientEncStr)
	} else {
		clientConfig, err = xray.AddClientToInbound(s.cfg.ConfigDir, tag, identity, s.cfg.XrayConfigFilePerm(), clientEncStr)
	}
	if err != nil {
		logXrayUserInboundError("WARN: add user %s to Xray inbound %s: %s", identity, tag, err)
		return
	}

	inbounds, _ := s.db.ListInbounds()
	var found bool
	for _, ib := range inbounds {
		if ib.Tag == tag {
			_ = s.db.UpsertUserClient(user.ID, ib.ID, clientConfig)
			found = true
			break
		}
	}
	if !found {
		_ = s.syncer.Sync()
		inbounds, _ = s.db.ListInbounds()
		for _, ib := range inbounds {
			if ib.Tag == tag {
				_ = s.db.UpsertUserClient(user.ID, ib.ID, clientConfig)
				found = true
				break
			}
		}
	}
	if !found {
		if parsed, configFile, err := xray.GetInboundByTag(s.cfg.ConfigDir, tag); err == nil && parsed != nil {
			ibID, err := s.db.UpsertInbound(parsed.Tag, parsed.Protocol, parsed.Port, configFile, parsed.RawJSON)
			if err == nil {
				_ = s.db.UpsertUserClient(user.ID, ibID, clientConfig)
				found = true
			}
		}
	}
	if !found && protocolFallback != "" {
		protocol := strings.ToLower(strings.TrimSpace(protocolFallback))
		port := s.cfg.APIUserInboundPort
		if port <= 0 {
			port = 443
		}
		if protocol == "vless" || protocol == "vmess" || protocol == "trojan" || protocol == "shadowsocks" {
			rawJSON := fmt.Sprintf(`{"tag":"%s","protocol":"%s","port":%d}`, tag, protocol, port)
			ibID, err := s.db.UpsertInbound(tag, protocol, port, "api-managed", rawJSON)
			if err == nil {
				_ = s.db.UpsertUserClient(user.ID, ibID, clientConfig)
				found = true
				log.Printf("Created inbound %s in DB (api_user_inbound_protocol fallback)", sanitizeLogField(tag))
			}
		}
	}
	if !found {
		logXrayInboundUserMissing(tag, identity)
	}
}

// removeUserFromXray removes the user from all inbounds they are enrolled in (file or API).
func (s *Server) removeUserFromXray(userID int64, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}
	clients, err := s.db.GetUserClients(userID)
	if err != nil {
		return
	}
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	for _, c := range clients {
		tag := c.InboundTag
		if apiAddr != "" {
			if err := xray.RemoveUserFromInboundViaAPI(apiAddr, tag, username); err != nil {
				logXrayUserInboundError("WARN: remove user %s from Xray inbound %s: %s", username, tag, err)
			}
			// Also remove from config file so the user does not re-appear after Xray restart.
			if err := xray.RemoveUserFromInbound(s.cfg.ConfigDir, tag, username, s.cfg.XrayConfigFilePerm()); err != nil {
				logXrayUserInboundError("WARN: remove user %s from Xray config %s: %s", username, tag, err)
			}
		} else {
			if err := xray.RemoveUserFromInbound(s.cfg.ConfigDir, tag, username, s.cfg.XrayConfigFilePerm()); err != nil {
				logXrayUserInboundError("WARN: remove user %s from Xray config %s: %s", username, tag, err)
			}
		}
	}
	if len(clients) > 0 {
		go func() { _ = s.syncer.Sync() }()
	}
}

// addClientToXray adds a single user/client to one inbound in Xray (config or API).
func (s *Server) addClientToXray(username, tag, clientConfig string) {
	username = strings.TrimSpace(username)
	if username == "" || tag == "" {
		return
	}
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	if apiAddr != "" {
		if err := xray.AddExistingClientToInboundViaAPI(apiAddr, tag, username, clientConfig); err != nil {
			logXrayUserInboundError("WARN: add user %s to Xray inbound %s: %s", username, tag, err)
		}
	} else {
		if err := xray.AddExistingClientToInbound(s.cfg.ConfigDir, tag, username, clientConfig, s.cfg.XrayConfigFilePerm()); err != nil {
			logXrayUserInboundError("WARN: add user %s to Xray config %s: %s", username, tag, err)
		}
		go func() { _ = s.syncer.Sync() }()
	}
}

// removeClientFromXray removes a single user from one inbound in Xray (config or API).
func (s *Server) removeClientFromXray(username, tag string) {
	username = strings.TrimSpace(username)
	if username == "" || tag == "" {
		return
	}
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	if apiAddr != "" {
		if err := xray.RemoveUserFromInboundViaAPI(apiAddr, tag, username); err != nil {
			logXrayUserInboundError("WARN: remove user %s from Xray inbound %s: %s", username, tag, err)
		}
		// Also remove from config file so the user does not re-appear after Xray restart.
		if err := xray.RemoveUserFromInbound(s.cfg.ConfigDir, tag, username, s.cfg.XrayConfigFilePerm()); err != nil {
			logXrayUserInboundError("WARN: remove user %s from Xray config %s: %s", username, tag, err)
		}
	} else {
		if err := xray.RemoveUserFromInbound(s.cfg.ConfigDir, tag, username, s.cfg.XrayConfigFilePerm()); err != nil {
			logXrayUserInboundError("WARN: remove user %s from Xray config %s: %s", username, tag, err)
		}
	}
	go func() { _ = s.syncer.Sync() }()
}

// addUserToXray adds the user to all inbounds they have user_client for, using existing credentials from DB.
func (s *Server) addUserToXray(userID int64, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}
	clients, err := s.db.GetUserClients(userID)
	if err != nil {
		return
	}
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	for _, c := range clients {
		tag := c.InboundTag
		if apiAddr != "" {
			if err := xray.AddExistingClientToInboundViaAPI(apiAddr, tag, username, c.ClientConfig); err != nil {
				logXrayUserInboundError("WARN: add user %s to Xray inbound %s: %s", username, tag, err)
			}
		} else {
			if err := xray.AddExistingClientToInbound(s.cfg.ConfigDir, tag, username, c.ClientConfig, s.cfg.XrayConfigFilePerm()); err != nil {
				logXrayUserInboundError("WARN: add user %s to Xray config %s: %s", username, tag, err)
			}
		}
	}
	if apiAddr == "" && len(clients) > 0 {
		go func() { _ = s.syncer.Sync() }()
	}
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	jsonOK(w, models.UserResponse{User: *user, SubURL: s.cfg.SubURL(user.Token), SubURLs: s.cfg.SubURLsWithFallback(user.Token, user.FallbackToken)})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	// Remove from Xray before deleting from DB (need username for removal)
	s.removeUserFromXray(user.ID, user.ClientIdentity())

	if err := s.db.DeleteUser(user.ID); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) enableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, true)
}

func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, false)
}

func (s *Server) setUserEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	if err := s.db.SetUserEnabled(user.ID, enabled); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Sync to Xray: remove when disabled, add when enabled
	if enabled {
		s.addUserToXray(user.ID, user.ClientIdentity())
	} else {
		s.removeUserFromXray(user.ID, user.ClientIdentity())
	}
	jsonOK(w, map[string]bool{"enabled": enabled})
}

func (s *Server) regenerateToken(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	newToken := generateToken()
	if err := s.db.UpdateUserToken(user.ID, newToken); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{
		"token":   newToken,
		"sub_url": s.cfg.SubURL(newToken),
	})
}

func (s *Server) getUserRoutes(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	jsonOK(w, map[string]interface{}{
		"user_id":  user.ID,
		"username": user.Username,
		"rules":    rules,
	})
}

func (s *Server) setUserRoutes(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	body, err := io.ReadAll(limitRequestBody(r))
	if err != nil {
		jsonError(w, "failed to read body", http.StatusBadRequest)
		return
	}
	var payload struct {
		Rules []models.UserRouteRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Rules == nil {
		// Support bare array payload for convenience.
		var raw []models.UserRouteRule
		if err2 := json.Unmarshal(body, &raw); err2 == nil {
			payload.Rules = raw
		} else {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
	}
	if err := validateUserRoutes(payload.Rules); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	assignRouteIDs(payload.Rules)
	if err := s.saveUserRoutes(user.ID, payload.Rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"rules":   payload.Rules,
	})
}

func (s *Server) addUserRoute(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	rule, err := decodeUserRouteFromBody(limitRequestBody(r))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateUserRoutes([]models.UserRouteRule{rule}); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(rule.ID) == "" {
		rule.ID = generateRouteID()
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	rules = append(rules, rule)
	if err := s.saveUserRoutes(user.ID, rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"index":   len(rules) - 1,
		"rule":    rule,
		"rules":   rules,
	})
}

func (s *Server) updateUserRoute(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	index, err := parseRouteIndex(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	rule, err := decodeUserRouteFromBody(limitRequestBody(r))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateUserRoutes([]models.UserRouteRule{rule}); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if index < 0 || index >= len(rules) {
		jsonError(w, "route index out of range", http.StatusNotFound)
		return
	}
	if strings.TrimSpace(rule.ID) == "" {
		rule.ID = rules[index].ID
	}
	if strings.TrimSpace(rule.ID) == "" {
		rule.ID = generateRouteID()
	}
	rules[index] = rule
	if err := s.saveUserRoutes(user.ID, rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"index":   index,
		"rule":    rule,
		"rules":   rules,
	})
}

func (s *Server) updateUserRouteByID(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	routeID := strings.TrimSpace(mux.Vars(r)["routeId"])
	if routeID == "" {
		jsonError(w, "missing route id", http.StatusBadRequest)
		return
	}
	rule, err := decodeUserRouteFromBody(limitRequestBody(r))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(rule.ID) == "" {
		rule.ID = routeID
	}
	if strings.TrimSpace(rule.ID) != routeID {
		jsonError(w, "route id mismatch", http.StatusBadRequest)
		return
	}
	if err := validateUserRoutes([]models.UserRouteRule{rule}); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	idx := findRouteIndexByID(rules, routeID)
	if idx < 0 {
		jsonError(w, "route id not found", http.StatusNotFound)
		return
	}
	rules[idx] = rule
	if err := s.saveUserRoutes(user.ID, rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"index":   idx,
		"rule":    rule,
		"rules":   rules,
	})
}

func (s *Server) deleteUserRoute(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	index, err := parseRouteIndex(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if index < 0 || index >= len(rules) {
		jsonError(w, "route index out of range", http.StatusNotFound)
		return
	}
	removed := rules[index]
	rules = append(rules[:index], rules[index+1:]...)
	if err := s.saveUserRoutes(user.ID, rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"index":   index,
		"removed": removed,
		"rules":   rules,
	})
}

func (s *Server) deleteUserRouteByID(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	routeID := strings.TrimSpace(mux.Vars(r)["routeId"])
	if routeID == "" {
		jsonError(w, "missing route id", http.StatusBadRequest)
		return
	}
	rules, err := parseUserRoutes(user.ClientRoutes)
	if err != nil {
		jsonError(w, "invalid stored routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		if err := s.saveUserRoutes(user.ID, rules); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	idx := findRouteIndexByID(rules, routeID)
	if idx < 0 {
		jsonError(w, "route id not found", http.StatusNotFound)
		return
	}
	removed := rules[idx]
	rules = append(rules[:idx], rules[idx+1:]...)
	if err := s.saveUserRoutes(user.ID, rules); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"user_id": user.ID,
		"index":   idx,
		"removed": removed,
		"rules":   rules,
	})
}

func (s *Server) getGlobalRoutes(w http.ResponseWriter, _ *http.Request) {
	raw, err := s.db.GetGlobalClientRoutes()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rules, err := parseUserRoutes(raw)
	if err != nil {
		jsonError(w, "invalid stored global routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		data, err := json.Marshal(rules)
		if err != nil {
			jsonError(w, "failed to encode routes", http.StatusInternalServerError)
			return
		}
		if err := s.db.UpdateGlobalClientRoutes(string(data)); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	jsonOK(w, map[string]interface{}{
		"scope": "global",
		"rules": rules,
	})
}

func (s *Server) setGlobalRoutes(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(limitRequestBody(r))
	if err != nil {
		jsonError(w, "failed to read body", http.StatusBadRequest)
		return
	}
	var payload struct {
		Rules []models.UserRouteRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Rules == nil {
		var raw []models.UserRouteRule
		if err2 := json.Unmarshal(body, &raw); err2 == nil {
			payload.Rules = raw
		} else {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
	}
	if err := validateUserRoutes(payload.Rules); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	assignRouteIDs(payload.Rules)
	data, err := json.Marshal(payload.Rules)
	if err != nil {
		jsonError(w, "failed to encode routes", http.StatusInternalServerError)
		return
	}
	if err := s.db.UpdateGlobalClientRoutes(string(data)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"scope": "global",
		"rules": payload.Rules,
	})
}

func (s *Server) addGlobalRoute(w http.ResponseWriter, r *http.Request) {
	rule, err := decodeUserRouteFromBody(limitRequestBody(r))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(rule.ID) == "" {
		rule.ID = generateRouteID()
	}
	if err := validateUserRoutes([]models.UserRouteRule{rule}); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := s.db.GetGlobalClientRoutes()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rules, err := parseUserRoutes(raw)
	if err != nil {
		jsonError(w, "invalid stored global routes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if assignRouteIDs(rules) {
		data, err := json.Marshal(rules)
		if err != nil {
			jsonError(w, "failed to encode routes", http.StatusInternalServerError)
			return
		}
		if err := s.db.UpdateGlobalClientRoutes(string(data)); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	rules = append(rules, rule)
	if err := validateUserRoutes(rules); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := json.Marshal(rules)
	if err != nil {
		jsonError(w, "failed to encode routes", http.StatusInternalServerError)
		return
	}
	if err := s.db.UpdateGlobalClientRoutes(string(data)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"scope": "global",
		"index": len(rules) - 1,
		"rule":  rule,
		"rules": rules,
	})
}

func (s *Server) deleteGlobalRoutes(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.UpdateGlobalClientRoutes("[]"); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"scope": "global",
		"rules": []models.UserRouteRule{},
	})
}

func (s *Server) getBalancerConfig(w http.ResponseWriter, _ *http.Request) {
	effectiveStrategy, effectiveProbeURL, effectiveProbeInterval, err := s.getEffectiveBalancerConfig()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	override, err := s.db.GetBalancerRuntimeConfig()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	source := "config"
	if override != nil {
		source = "runtime"
	}
	jsonOK(w, map[string]interface{}{
		"source": source,
		"effective": map[string]string{
			"strategy":      effectiveStrategy,
			"probe_url":     effectiveProbeURL,
			"probe_interval": effectiveProbeInterval,
		},
		"config_file_defaults": map[string]string{
			"strategy":      s.cfg.BalancerStrategy,
			"probe_url":     s.cfg.BalancerProbeURL,
			"probe_interval": s.cfg.BalancerProbeFreq,
		},
		"runtime_override": override,
	})
}

func (s *Server) setBalancerConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Strategy      string `json:"strategy"`
		ProbeURL      string `json:"probe_url"`
		ProbeInterval string `json:"probe_interval"`
		Reset         bool   `json:"reset"`
	}
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&req); err != nil {
		jsonError(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.Reset {
		if err := s.db.DeleteBalancerRuntimeConfig(); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"status": "reset_to_config_defaults",
		})
		return
	}

	strategy := normalizeBalancerStrategyInput(req.Strategy)
	if strategy == "" {
		jsonError(w, "strategy must be one of: random, roundRobin, leastPing, leastLoad", http.StatusBadRequest)
		return
	}
	probeURL := strings.TrimSpace(req.ProbeURL)
	if probeURL == "" {
		probeURL = s.cfg.BalancerProbeURL
	}
	probeInterval := strings.TrimSpace(req.ProbeInterval)
	if probeInterval == "" {
		probeInterval = s.cfg.BalancerProbeFreq
	}

	if err := s.db.SetBalancerRuntimeConfig(database.BalancerRuntimeConfig{
		Strategy:      strategy,
		ProbeURL:      probeURL,
		ProbeInterval: probeInterval,
	}); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"status": "ok",
		"runtime_override": map[string]string{
			"strategy":      strategy,
			"probe_url":     probeURL,
			"probe_interval": probeInterval,
		},
	})
}

func (s *Server) getUserClients(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	clients, err := s.db.GetUserClients(user.ID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, clients)
}

// addUserClient adds one inbound client mapping for an existing user.
// Request body: {"tag":"<inbound-tag>","protocol":"<optional>"}.
func (s *Server) addUserClient(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}

	var req models.InboundSpec
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&req); err != nil {
		jsonError(w, "invalid json body", http.StatusBadRequest)
		return
	}
	tag := strings.TrimSpace(req.Tag)
	if tag == "" {
		jsonError(w, "tag is required", http.StatusBadRequest)
		return
	}

	// Idempotent behavior: if enabled client for this tag already exists, return it.
	existingClients, err := s.db.GetUserClients(user.ID)
	if err == nil {
		for _, c := range existingClients {
			if c.InboundTag == tag {
				jsonOK(w, c)
				return
			}
		}
	}

	protocol := strings.TrimSpace(req.Protocol)
	if protocol == "" {
		inbounds, _ := s.db.ListInbounds()
		for _, ib := range inbounds {
			if ib.Tag == tag {
				protocol = ib.Protocol
				break
			}
		}
	}
	if protocol == "" {
		protocol = strings.TrimSpace(s.cfg.APIUserInboundProtocol)
	}

	s.addUserToInbound(user, tag, protocol)

	clients, err := s.db.GetUserClients(user.ID)
	if err != nil {
		jsonError(w, "failed to load created client", http.StatusInternalServerError)
		return
	}
	for _, c := range clients {
		if c.InboundTag == tag {
			jsonOK(w, c)
			return
		}
	}
	jsonError(w, "failed to add user to inbound", http.StatusInternalServerError)
}

func (s *Server) enableUserClient(w http.ResponseWriter, r *http.Request) {
	s.setClientEnabled(w, r, true)
}

func (s *Server) disableUserClient(w http.ResponseWriter, r *http.Request) {
	s.setClientEnabled(w, r, false)
}

func (s *Server) setClientEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	vars := mux.Vars(r)
	inboundID, err := strconv.ParseInt(strings.TrimSpace(vars["inboundId"]), 10, 64)
	if err != nil {
		jsonError(w, "invalid inbound id", http.StatusBadRequest)
		return
	}
	tag, clientConfig, err := s.db.GetUserClientByUserAndInbound(user.ID, inboundID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tag == "" {
		jsonError(w, "client not found", http.StatusNotFound)
		return
	}
	if err := s.db.SetUserClientEnabled(user.ID, inboundID, enabled); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Sync to Xray (config or API), same as enable/disable user
	if enabled {
		s.addClientToXray(user.ClientIdentity(), tag, clientConfig)
	} else {
		s.removeClientFromXray(user.ClientIdentity(), tag)
	}
	jsonOK(w, map[string]bool{"enabled": enabled})
}

// ─── Inbound handlers ─────────────────────────────────────────────────────────

func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	limit, offset, usePagination := parsePagination(r)

	var inbounds []models.Inbound
	var err error
	if usePagination {
		inbounds, err = s.db.ListInboundsPaginated(limit, offset)
	} else {
		inbounds, err = s.db.ListInbounds()
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := make([]map[string]interface{}, 0, len(inbounds))
	for _, ib := range inbounds {
		item := map[string]interface{}{
			"id":          ib.ID,
			"tag":         ib.Tag,
			"protocol":    ib.Protocol,
			"port":        ib.Port,
			"config_file": ib.ConfigFile,
			"updated_at":  ib.UpdatedAt,
		}
		var raw interface{}
		if err := json.Unmarshal([]byte(ib.RawConfig), &raw); err != nil {
			// Keep backward-compatible behavior if raw JSON is malformed.
			item["raw_config"] = ib.RawConfig
		} else {
			item["raw_config"] = raw
		}
		resp = append(resp, item)
	}

	if usePagination {
		total, err := s.db.CountInbounds()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]interface{}{
			"items":  resp,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
	} else {
		jsonOK(w, resp)
	}
}

// ─── Sync handler ─────────────────────────────────────────────────────────────

func (s *Server) triggerSync(w http.ResponseWriter, _ *http.Request) {
	go func() {
		if err := s.syncer.Sync(); err != nil {
			log.Printf("Manual sync error: %v", err)
		}
	}()
	jsonOK(w, map[string]string{"status": "sync started"})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

const maxRequestBodyBytes = 512 * 1024 // 512KB

// usernameValid matches alphanumeric, underscore, hyphen, at, dot (for emails). Length 1–64.
var usernameValid = regexp.MustCompile(`^[a-zA-Z0-9_.@-]{1,64}$`)

func validateUsername(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("username required")
	}
	if !usernameValid.MatchString(s) {
		return fmt.Errorf("username must be 1–64 chars: letters, digits, underscore, hyphen, at, dot")
	}
	return nil
}

func limitRequestBody(r *http.Request) io.Reader {
	return io.LimitReader(r.Body, maxRequestBodyBytes)
}

// parsePagination reads limit and offset from query params.
// If both are absent, returns usePagination=false (backward compatible).
// If either is present, returns limit (default 50, max 100), offset, usePagination=true.
func parsePagination(r *http.Request) (limit, offset int, usePagination bool) {
	q := r.URL.Query()
	limitStr := q.Get("limit")
	offsetStr := q.Get("offset")
	if limitStr == "" && offsetStr == "" {
		return 0, 0, false
	}
	limit = 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
			if limit > 100 {
				limit = 100
			}
		}
	}
	offset = 0
	if offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset, true
}

func (s *Server) getByID(w http.ResponseWriter, r *http.Request) (*models.User, error) {
	idStr := strings.TrimSpace(mux.Vars(r)["id"])
	if idStr == "" {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return nil, fmt.Errorf("empty id")
	}
	var user *models.User
	var err error
	if id, parseErr := strconv.ParseInt(idStr, 10, 64); parseErr == nil {
		user, err = s.db.GetUserByID(id)
	} else {
		user, err = s.db.GetUserByUsername(idStr)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return nil, err
	}
	if user == nil {
		jsonError(w, "user not found", http.StatusNotFound)
		return nil, nil
	}
	return user, nil
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("ERROR encode JSON response: %v", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("ERROR encode JSON error response: %v", err)
	}
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// If crypto/rand fails, the process is in a broken state; crash loudly.
		panic(err)
	}
	return hex.EncodeToString(b)
}

func sanitizeLogField(v string) string {
	// Prevent control characters from forging multiline log entries.
	return strings.NewReplacer("\r", "", "\n", "", "\t", " ").Replace(v)
}

// logXrayUserInboundError logs a Xray sync warning. tmpl must be a constant format string with three %s (user, inbound, error text).
// Operands are sanitized to mitigate log forging (gosec G706).
func logXrayUserInboundError(tmpl string, user, inbound string, err error) {
	if err == nil {
		return
	}
	// #nosec G706 -- tmpl is fixed at each call site; user, inbound, err text sanitized via sanitizeLogField (standalone gosec ignores //nolint:gosec)
	log.Printf(tmpl, sanitizeLogField(user), sanitizeLogField(inbound), sanitizeLogField(err.Error()))
}

func logXrayInboundUserMissing(tag, identity string) {
	// #nosec G706 -- tag and identity sanitized via sanitizeLogField
	log.Printf("WARN: inbound %s not found in DB; user %s has no subscription for this inbound",
		sanitizeLogField(tag), sanitizeLogField(identity))
}

func isMobileProfileRequest(r *http.Request) bool {
	profile := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("profile")))
	if profile == "mobile" {
		return true
	}
	mobile := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mobile")))
	if mobile == "1" || mobile == "true" || mobile == "yes" {
		return true
	}
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "android") ||
		strings.Contains(ua, "iphone") ||
		strings.Contains(ua, "ipad") ||
		strings.Contains(ua, "v2box") ||
		strings.Contains(ua, "v2rayng") ||
		strings.Contains(ua, "nekobox")
}

func applyMobileRoutingProfile(cfg *xray.ClientConfig) {
	if cfg == nil || cfg.Routing == nil {
		return
	}
	rules := make([]xray.RoutingRule, 0, len(cfg.Routing.Rules))
	for _, rule := range cfg.Routing.Rules {
		rule.Domain = stripGeoSelectors(rule.Domain)
		rule.IP = stripGeoSelectors(rule.IP)
		// Keep only effective rules after stripping geo selectors.
		if len(rule.Domain) == 0 &&
			len(rule.IP) == 0 &&
			strings.TrimSpace(rule.Network) == "" &&
			strings.TrimSpace(rule.Port) == "" &&
			len(rule.Protocol) == 0 &&
			len(rule.InboundTag) == 0 {
			continue
		}
		rules = append(rules, rule)
	}
	cfg.Routing.Rules = rules
}

func stripGeoSelectors(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		s := strings.ToLower(strings.TrimSpace(v))
		if strings.HasPrefix(s, "geosite:") || strings.HasPrefix(s, "geoip:") {
			continue
		}
		out = append(out, v)
	}
	return out
}

func parseUserRoutes(raw string) ([]models.UserRouteRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []models.UserRouteRule{}, nil
	}
	var rules []models.UserRouteRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func validateUserRoutes(rules []models.UserRouteRule) error {
	seen := map[string]struct{}{}
	for i, rule := range rules {
		if strings.TrimSpace(rule.ID) != "" {
			if _, ok := seen[rule.ID]; ok {
				return fmt.Errorf("rules[%d].id must be unique", i)
			}
			seen[rule.ID] = struct{}{}
		}
		tag := strings.ToLower(strings.TrimSpace(rule.OutboundTag))
		if tag != "direct" && tag != "proxy" && tag != "block" {
			return fmt.Errorf("rules[%d].outboundTag must be one of: direct, proxy, block", i)
		}
		// Xray requires at least one effective field for 'field' rules.
		if len(rule.Domain) == 0 &&
			len(rule.IP) == 0 &&
			strings.TrimSpace(rule.Network) == "" &&
			strings.TrimSpace(rule.Port) == "" &&
			len(rule.Protocol) == 0 &&
			len(rule.InboundTag) == 0 {
			return fmt.Errorf("rules[%d] has no effective fields", i)
		}
		if rule.Type != "" && strings.ToLower(strings.TrimSpace(rule.Type)) != "field" {
			return fmt.Errorf("rules[%d].type must be 'field' or empty", i)
		}
		if err := validateRouteSelectors(rule); err != nil {
			return fmt.Errorf("rules[%d] %w", i, err)
		}
	}
	return nil
}

func validateRouteSelectors(rule models.UserRouteRule) error {
	for _, d := range rule.Domain {
		v := strings.TrimSpace(strings.ToLower(d))
		if strings.HasPrefix(v, "geoip:") {
			return fmt.Errorf("domain contains geoip selector %q; use ip:[\"geoip:...\"]", d)
		}
		if strings.HasPrefix(v, "geositeip:") {
			return fmt.Errorf("invalid selector %q; use geosite:... in domain or geoip:... in ip", d)
		}
	}
	for _, ip := range rule.IP {
		v := strings.TrimSpace(strings.ToLower(ip))
		if strings.HasPrefix(v, "geosite:") {
			return fmt.Errorf("ip contains geosite selector %q; use domain:[\"geosite:...\"]", ip)
		}
		if strings.HasPrefix(v, "geositeip:") {
			return fmt.Errorf("invalid selector %q; use geosite:... in domain or geoip:... in ip", ip)
		}
	}
	return nil
}

func (s *Server) saveUserRoutes(userID int64, rules []models.UserRouteRule) error {
	data, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("failed to encode routes: %w", err)
	}
	if err := s.db.UpdateUserClientRoutes(userID, string(data)); err != nil {
		return err
	}
	return nil
}

func parseRouteIndex(r *http.Request) (int, error) {
	raw := mux.Vars(r)["index"]
	idx, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid route index")
	}
	return idx, nil
}

func decodeUserRouteFromBody(body io.Reader) (models.UserRouteRule, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return models.UserRouteRule{}, fmt.Errorf("failed to read body")
	}
	var rule models.UserRouteRule
	if err := json.Unmarshal(data, &rule); err == nil && (rule.OutboundTag != "" || rule.Type != "" || len(rule.Domain)+len(rule.IP)+len(rule.Protocol)+len(rule.InboundTag) > 0 || rule.Network != "" || rule.Port != "") {
		return rule, nil
	}
	var wrapped struct {
		Rule models.UserRouteRule `json:"rule"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Rule.OutboundTag != "" {
		return wrapped.Rule, nil
	}
	return models.UserRouteRule{}, fmt.Errorf("invalid json body")
}

func (s *Server) getEffectiveBalancerConfig() (strategy string, probeURL string, probeInterval string, err error) {
	override, err := s.db.GetBalancerRuntimeConfig()
	if err != nil {
		return "", "", "", err
	}
	if override != nil {
		return firstNonEmptyString(strings.TrimSpace(override.Strategy), s.cfg.BalancerStrategy),
			firstNonEmptyString(strings.TrimSpace(override.ProbeURL), s.cfg.BalancerProbeURL),
			firstNonEmptyString(strings.TrimSpace(override.ProbeInterval), s.cfg.BalancerProbeFreq),
			nil
	}
	return s.cfg.BalancerStrategy, s.cfg.BalancerProbeURL, s.cfg.BalancerProbeFreq, nil
}

func normalizeBalancerStrategyInput(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "random":
		return "random"
	case "roundrobin":
		return "roundRobin"
	case "leastping":
		return "leastPing"
	case "leastload":
		return "leastLoad"
	default:
		return ""
	}
}

func assignRouteIDs(rules []models.UserRouteRule) bool {
	changed := false
	for i := range rules {
		if strings.TrimSpace(rules[i].ID) == "" {
			rules[i].ID = generateRouteID()
			changed = true
		}
	}
	return changed
}

func findRouteIndexByID(rules []models.UserRouteRule, routeID string) int {
	for i, r := range rules {
		if strings.TrimSpace(r.ID) == routeID {
			return i
		}
	}
	return -1
}

func generateRouteID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
