package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

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
	api.HandleFunc("/users/{id}/clients", s.getUserClients).Methods(http.MethodGet)
	api.HandleFunc("/users/{userId}/clients/{inboundId}/enable", s.enableUserClient).Methods(http.MethodPut)
	api.HandleFunc("/users/{userId}/clients/{inboundId}/disable", s.disableUserClient).Methods(http.MethodPut)

	api.HandleFunc("/inbounds", s.listInbounds).Methods(http.MethodGet)
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

	cfg, err := xray.GenerateClientConfig(s.cfg.ServerHost, *user, clients)
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
