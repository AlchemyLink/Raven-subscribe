package api

import (
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/mux"
)

// handleFallbackSubscription serves /sub/fallback/{fallback_token}.
// Returns the same content as the primary subscription endpoint but keyed on the fallback token.
// Records the access time. Returns 404 when fallback is globally disabled or token is unknown.
func (s *Server) handleFallbackSubscription(w http.ResponseWriter, r *http.Request) {
	fallbackToken := mux.Vars(r)["token"]
	if fallbackToken == "" {
		jsonError(w, "missing token", http.StatusBadRequest)
		return
	}

	enabled, err := s.db.GetFallbackEnabled()
	if err != nil {
		log.Printf("ERROR get fallback enabled: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !enabled {
		jsonError(w, "fallback subscription disabled", http.StatusForbidden)
		return
	}

	user, err := s.db.GetUserByFallbackToken(fallbackToken)
	if err != nil || user == nil {
		jsonError(w, "invalid token", http.StatusNotFound)
		return
	}
	if !user.Enabled {
		jsonError(w, "user disabled", http.StatusForbidden)
		return
	}

	go func() {
		if err := s.db.SetFallbackAccessedAt(user.ID, time.Now().UTC()); err != nil {
			log.Printf("WARN set fallback_accessed_at for user %d: %v", user.ID, err)
		}
	}()

	w.Header().Set("X-Fallback-Token", "true")

	// Delegate to the primary subscription handler using the user's primary token.
	// Swap the {token} path variable so handleSubscription resolves the user correctly.
	r = mux.SetURLVars(r, map[string]string{"token": user.Token})
	s.handleSubscription(w, r)
}

// regenerateFallbackToken generates a new fallback token for the user.
// POST /api/users/{id}/fallback/token
func (s *Server) regenerateFallbackToken(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	newToken := generateToken()
	if err := s.db.UpdateFallbackToken(user.ID, newToken); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{
		"fallback_token": newToken,
		"fallback_url":   s.cfg.FallbackURL(newToken),
	})
}

// getFallbackStatus returns the global fallback enabled/disabled state.
// GET /api/fallback/status
func (s *Server) getFallbackStatus(w http.ResponseWriter, _ *http.Request) {
	enabled, err := s.db.GetFallbackEnabled()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"enabled": enabled})
}

// enableFallback enables the global fallback subscription endpoint.
// POST /api/fallback/enable
func (s *Server) enableFallback(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.SetFallbackEnabled(true); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"enabled": true})
}

// disableFallback disables the global fallback subscription endpoint.
// POST /api/fallback/disable
func (s *Server) disableFallback(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.SetFallbackEnabled(false); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"enabled": false})
}

// listFallbackAccessedUsers returns all users who have accessed their fallback subscription,
// ordered by most recent access. GET /api/fallback/accessed
func (s *Server) listFallbackAccessedUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.db.ListUsers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type entry struct {
		ID                 int64      `json:"id"`
		Username           string     `json:"username"`
		FallbackToken      string     `json:"fallback_token,omitempty"`
		FallbackAccessedAt *time.Time `json:"fallback_accessed_at"`
		FallbackURL        string     `json:"fallback_url,omitempty"`
	}
	var result []entry
	for _, u := range users {
		if u.FallbackAccessedAt == nil {
			continue
		}
		result = append(result, entry{
			ID:                 u.ID,
			Username:           u.Username,
			FallbackToken:      u.FallbackToken,
			FallbackAccessedAt: u.FallbackAccessedAt,
			FallbackURL:        s.cfg.FallbackURL(u.FallbackToken),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].FallbackAccessedAt.After(*result[j].FallbackAccessedAt)
	})
	jsonOK(w, result)
}
