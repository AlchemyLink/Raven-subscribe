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
	api.HandleFunc("/sync", s.triggerSync).Methods(http.MethodPost)

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
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
	if p := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol"))); p != "" {
		filtered := make([]models.UserClientFull, 0, len(clients))
		for _, c := range clients {
			if strings.EqualFold(c.InboundProtocol, p) {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
	}
	if t := strings.TrimSpace(r.URL.Query().Get("inbound_tag")); t != "" {
		filtered := make([]models.UserClientFull, 0, len(clients))
		for _, c := range clients {
			if c.InboundTag == t {
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

	cfg, err := xray.GenerateClientConfig(s.cfg.ServerHost, *user, clients, globalRoutesJSON)
	if err != nil {
		log.Printf("ERROR generate config for %s: %v", user.Username, err)
		jsonError(w, "could not generate config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="xray-config.json"`)
	w.Header().Set("Profile-Title", user.Username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(cfg)
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

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) getGlobalRoutes(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) deleteGlobalRoutes(w http.ResponseWriter, r *http.Request) {
	if err := s.db.UpdateGlobalClientRoutes("[]"); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"scope": "global",
		"rules": []models.UserRouteRule{},
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

func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	inbounds, err := s.db.ListInbounds()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, inbounds)
}

// ─── Sync handler ─────────────────────────────────────────────────────────────

func (s *Server) triggerSync(w http.ResponseWriter, r *http.Request) {
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
	enc.Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// If crypto/rand fails, the process is in a broken state; crash loudly.
		panic(err)
	}
	return hex.EncodeToString(b)
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
