package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/gorilla/mux"
)

// ─── Emergency status ─────────────────────────────────────────────────────────

// getEmergencyStatus returns the current emergency mode state.
// GET /api/emergency/status
func (s *Server) getEmergencyStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.db.GetEmergencyStatus()
	if err != nil {
		log.Printf("ERROR get emergency status: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, status)
}

// activateEmergency switches all subscriptions to the specified emergency profile.
// POST /api/emergency/activate   body: {"profile_id": 1}
func (s *Server) activateEmergency(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProfileID int64 `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ProfileID <= 0 {
		jsonError(w, "profile_id is required", http.StatusBadRequest)
		return
	}

	profile, err := s.db.GetEmergencyProfile(req.ProfileID)
	if err != nil {
		log.Printf("ERROR get emergency profile %d: %v", req.ProfileID, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		jsonError(w, "profile not found", http.StatusNotFound)
		return
	}

	if err := s.db.ActivateEmergency(req.ProfileID); err != nil {
		log.Printf("ERROR activate emergency (profile %d): %v", req.ProfileID, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("EMERGENCY MODE ACTIVATED: profile %q (id=%d, inbounds=%v)",
		profile.Name, profile.ID, profile.InboundTags)

	status, _ := s.db.GetEmergencyStatus()
	jsonOK(w, status)
}

// deactivateEmergency restores normal subscription serving.
// POST /api/emergency/deactivate
func (s *Server) deactivateEmergency(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.DeactivateEmergency(); err != nil {
		log.Printf("ERROR deactivate emergency: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("EMERGENCY MODE DEACTIVATED")
	jsonOK(w, map[string]bool{"active": false})
}

// ─── Emergency profiles ───────────────────────────────────────────────────────

// listEmergencyProfiles returns all emergency profiles.
// GET /api/emergency/profiles
func (s *Server) listEmergencyProfiles(w http.ResponseWriter, _ *http.Request) {
	profiles, err := s.db.ListEmergencyProfiles()
	if err != nil {
		log.Printf("ERROR list emergency profiles: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if profiles == nil {
		profiles = []models.EmergencyProfile{}
	}
	jsonOK(w, profiles)
}

// createEmergencyProfile creates a new emergency profile.
// POST /api/emergency/profiles   body: {"name": "CDN", "description": "...", "inbound_tags": ["vless-cdn-in"]}
func (s *Server) createEmergencyProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		InboundTags []string `json:"inbound_tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.InboundTags == nil {
		req.InboundTags = []string{}
	}

	profile, err := s.db.CreateEmergencyProfile(req.Name, req.Description, req.InboundTags)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			jsonError(w, "profile name already exists", http.StatusConflict)
			return
		}
		log.Printf("ERROR create emergency profile %q: %v", sanitizeLogField(req.Name), err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, profile)
}

// getEmergencyProfileByID returns a single emergency profile.
// GET /api/emergency/profiles/{id}
func (s *Server) getEmergencyProfileByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(w, r)
	if !ok {
		return
	}
	profile, err := s.db.GetEmergencyProfile(id)
	if err != nil {
		log.Printf("ERROR get emergency profile %d: %v", id, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		jsonError(w, "profile not found", http.StatusNotFound)
		return
	}
	jsonOK(w, profile)
}

// updateEmergencyProfile replaces the fields of an existing emergency profile.
// PUT /api/emergency/profiles/{id}   body: {"name": "CDN", "description": "...", "inbound_tags": [...]}
func (s *Server) updateEmergencyProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(w, r)
	if !ok {
		return
	}

	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		InboundTags []string `json:"inbound_tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.InboundTags == nil {
		req.InboundTags = []string{}
	}

	profile, err := s.db.UpdateEmergencyProfile(id, req.Name, req.Description, req.InboundTags)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			jsonError(w, "profile name already exists", http.StatusConflict)
			return
		}
		log.Printf("ERROR update emergency profile %d: %v", id, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		jsonError(w, "profile not found", http.StatusNotFound)
		return
	}
	jsonOK(w, profile)
}

// deleteEmergencyProfile removes an emergency profile.
// DELETE /api/emergency/profiles/{id}
func (s *Server) deleteEmergencyProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProfileID(w, r)
	if !ok {
		return
	}

	// Refuse to delete the active profile.
	if em, err := s.db.GetEmergencyStatus(); err == nil && em.Active && em.ProfileID != nil && *em.ProfileID == id {
		jsonError(w, "cannot delete the currently active emergency profile", http.StatusConflict)
		return
	}

	if err := s.db.DeleteEmergencyProfile(id); err != nil {
		log.Printf("ERROR delete emergency profile %d: %v", id, err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func parseProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := mux.Vars(r)["id"]
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "invalid profile id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
