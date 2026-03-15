package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"xray-subscription/internal/config"
	"xray-subscription/internal/database"
	"xray-subscription/internal/models"
	"xray-subscription/internal/xray"
)

// Syncer interface so we don't import the syncer package (avoid circular deps)
type Syncer interface {
	Sync() error
}

type Server struct {
	cfg    *config.Config
	db     *database.DB
	syncer Syncer
}

func NewServer(cfg *config.Config, db *database.DB, syncer Syncer) *Server {
	return &Server{cfg: cfg, db: db, syncer: syncer}
}

func (s *Server) Router() http.Handler {
	r := mux.NewRouter()

	// ── Subscription endpoint (public, authenticated by token) ──────────────
	r.HandleFunc("/sub/{token}", s.handleSubscription).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links", s.handleSubscriptionLinks).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links.txt", s.handleSubscriptionLinksText).Methods(http.MethodGet)
	r.HandleFunc("/sub/{token}/links.b64", s.handleSubscriptionLinksB64).Methods(http.MethodGet)
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
	api.HandleFunc("/users/{userId}/clients/{inboundId}/enable", s.enableUserClient).Methods(http.MethodPut)
	api.HandleFunc("/users/{userId}/clients/{inboundId}/disable", s.disableUserClient).Methods(http.MethodPut)

	api.HandleFunc("/inbounds", s.listInbounds).Methods(http.MethodGet)
	api.HandleFunc("/routes/global", s.getGlobalRoutes).Methods(http.MethodGet)
	api.HandleFunc("/routes/global", s.setGlobalRoutes).Methods(http.MethodPut)
	api.HandleFunc("/routes/global", s.addGlobalRoute).Methods(http.MethodPost)
	api.HandleFunc("/routes/global", s.deleteGlobalRoutes).Methods(http.MethodDelete)
	api.HandleFunc("/config/balancer", s.getBalancerConfig).Methods(http.MethodGet)
	api.HandleFunc("/config/balancer", s.setBalancerConfig).Methods(http.MethodPut)
	api.HandleFunc("/sync", s.triggerSync).Methods(http.MethodPost)

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]string{"status": "ok"})
	}).Methods(http.MethodGet)

	return r
}

// ─── Middleware ───────────────────────────────────────────────────────────────

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Admin-Token")
		if token == "" {
			token = r.URL.Query().Get("admin_token")
		}
		if token != s.cfg.AdminToken {
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
		log.Printf("ERROR get user clients for %s: %v", user.Username, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
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
		*user,
		clients,
		globalRoutesJSON,
		balancerStrategy,
		balancerProbeURL,
		balancerProbeInterval,
	)
	if err != nil {
		log.Printf("ERROR generate config for %s: %v", user.Username, err)
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
		log.Printf("ERROR encode subscription response for %s: %v", user.Username, err)
	}
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
		log.Printf("ERROR get user clients for %s: %v", user.Username, err)
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

func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.db.ListUsers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var resp []models.UserResponse
	for _, u := range users {
		resp = append(resp, models.UserResponse{
			User:   u,
			SubURL: s.cfg.SubURL(u.Token),
		})
	}
	jsonOK(w, resp)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req models.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
		jsonError(w, "username required", http.StatusBadRequest)
		return
	}

	// Check duplicate
	existing, _ := s.db.GetUserByUsername(req.Username)
	if existing != nil {
		jsonError(w, "username already exists", http.StatusConflict)
		return
	}

	token := generateToken()
	user, err := s.db.CreateUser(req.Username, token)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, models.UserResponse{User: *user, SubURL: s.cfg.SubURL(user.Token)})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	jsonOK(w, models.UserResponse{User: *user, SubURL: s.cfg.SubURL(user.Token)})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
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
		"user_id":   user.ID,
		"username":  user.Username,
		"rules":     rules,
	})
}

func (s *Server) setUserRoutes(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	body, err := io.ReadAll(r.Body)
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
	rule, err := decodeUserRouteFromBody(r.Body)
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
	rule, err := decodeUserRouteFromBody(r.Body)
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
	rule, err := decodeUserRouteFromBody(r.Body)
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
	body, err := io.ReadAll(r.Body)
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
	rule, err := decodeUserRouteFromBody(r.Body)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		jsonError(w, "strategy must be one of: random, leastPing, leastLoad", http.StatusBadRequest)
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

func (s *Server) enableUserClient(w http.ResponseWriter, r *http.Request) {
	s.setClientEnabled(w, r, true)
}

func (s *Server) disableUserClient(w http.ResponseWriter, r *http.Request) {
	s.setClientEnabled(w, r, false)
}

func (s *Server) setClientEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	vars := mux.Vars(r)
	userID, err1 := strconv.ParseInt(vars["userId"], 10, 64)
	inboundID, err2 := strconv.ParseInt(vars["inboundId"], 10, 64)
	if err1 != nil || err2 != nil {
		jsonError(w, "invalid ids", http.StatusBadRequest)
		return
	}
	if err := s.db.SetUserClientEnabled(userID, inboundID, enabled); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"enabled": enabled})
}

// ─── Inbound handlers ─────────────────────────────────────────────────────────

func (s *Server) listInbounds(w http.ResponseWriter, _ *http.Request) {
	inbounds, err := s.db.ListInbounds()
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
	jsonOK(w, resp)
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

func (s *Server) getByID(w http.ResponseWriter, r *http.Request) (*models.User, error) {
	idStr := mux.Vars(r)["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return nil, err
	}
	user, err := s.db.GetUserByID(id)
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
