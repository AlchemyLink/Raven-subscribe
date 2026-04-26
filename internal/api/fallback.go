package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/alchemylink/raven-subscribe/internal/xray"
)

type ctxFallbackKey struct{}

// withFallbackAuth validates the fallback token and delegates to next using the primary token.
// Shared by all /sub/fallback/* and /c/fallback/* routes.
func (s *Server) withFallbackAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
				log.Printf("WARN set fallback_accessed_at for user %d: %v", user.ID, err) // #nosec G706 -- user.ID is int, err is internal db error
			}
		}()

		w.Header().Set("X-Fallback-Token", "true")
		r = mux.SetURLVars(r, map[string]string{"token": user.Token})
		r = r.WithContext(context.WithValue(r.Context(), ctxFallbackKey{}, true))
		next(w, r)
	}
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

// enableFallback enables the global fallback subscription endpoint AND, when xray_api_addr
// is configured and FallbackInboundTags is non-empty, dynamically re-adds the corresponding
// Xray inbounds via gRPC HandlerService so that VPN traffic resumes immediately.
// POST /api/fallback/enable
func (s *Server) enableFallback(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.SetFallbackEnabled(true); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.applyKillSwitchInbounds(true)
	jsonOK(w, map[string]bool{"enabled": true})
}

// disableFallback disables the global fallback subscription endpoint AND, when xray_api_addr
// is configured and FallbackInboundTags is non-empty, dynamically removes the corresponding
// Xray inbounds via gRPC HandlerService so that the listener is torn down — active VPN
// connections to fallback inbounds get severed on next packet.
// POST /api/fallback/disable
func (s *Server) disableFallback(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.SetFallbackEnabled(false); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.applyKillSwitchInbounds(false)
	jsonOK(w, map[string]bool{"enabled": false})
}

// ReconcileKillSwitchOnStartup applies the current persisted killswitch state to the
// Xray runtime via gRPC. Intended to be called once after server initialization.
// When disabled, removes fallback inbounds from Xray (they may have been loaded from
// /etc/xray/config.d/ on Xray's own startup); when enabled, no-op (Xray already has them).
func (s *Server) ReconcileKillSwitchOnStartup() {
	enabled, err := s.db.GetFallbackEnabled()
	if err != nil {
		log.Printf("WARN killswitch reconcile: read state: %v", err)
		return
	}
	if enabled {
		// Inbounds are expected to be live from Xray config; nothing to add.
		return
	}
	s.applyKillSwitchInbounds(false)
}

// applyKillSwitchInbounds toggles fallback inbounds in the running Xray instance via
// gRPC. Errors are logged but never propagated to the HTTP response — the DB flag is
// already authoritative for subscription gating; gRPC failure means the inbound state
// drifts (will reconcile on next toggle or process restart).
//
// No-op when XrayAPIAddr is empty or FallbackInboundTags is unset.
func (s *Server) applyKillSwitchInbounds(enable bool) {
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	if apiAddr == "" || len(s.cfg.FallbackInboundTags) == 0 {
		return
	}
	for _, tag := range s.cfg.FallbackInboundTags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if enable {
			ib, err := s.db.GetInboundByTag(tag)
			if err != nil {
				log.Printf("WARN killswitch enable: get inbound %s: %v", sanitizeLogField(tag), err)
				continue
			}
			if ib == nil {
				log.Printf("WARN killswitch enable: inbound %s not in DB (synced from Xray config?)", sanitizeLogField(tag))
				continue
			}
			if err := xray.AddInboundFromJSONViaAPI(apiAddr, ib.RawConfig); err != nil {
				// "already exists" is benign — Xray may still hold the inbound from initial config load.
				if strings.Contains(strings.ToLower(err.Error()), "exist") {
					log.Printf("INFO killswitch enable: inbound %s already present (benign)", sanitizeLogField(tag))
					continue
				}
				log.Printf("WARN killswitch enable: AddInbound %s: %v", sanitizeLogField(tag), err)
			}
			continue
		}
		// disable
		if err := xray.RemoveInboundViaAPI(apiAddr, tag); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") ||
				strings.Contains(strings.ToLower(err.Error()), "no such") {
				log.Printf("INFO killswitch disable: inbound %s already absent (benign)", sanitizeLogField(tag))
				continue
			}
			log.Printf("WARN killswitch disable: RemoveInbound %s: %v", sanitizeLogField(tag), err)
		}
	}
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
