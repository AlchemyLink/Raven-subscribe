package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/gorilla/mux"
)

// Hysteria2 reserve integration (see hysteria_raven_integration_plan):
//   - /hysteria/auth  : auth-backend the native hysteria daemon calls (auth.type:http).
//                       Validates a connection's auth string (= the user's sub token)
//                       against the DB; per-user revocation. obfs is shared (set in the
//                       hysteria server config + emitted in the client URI), NOT here.
//   - /sub/{token}/hy2: returns the per-user hysteria2:// client URI (txt / .b64).

// hysteriaAuthRequest is the JSON the hysteria daemon POSTs to the http auth backend.
type hysteriaAuthRequest struct {
	Addr string `json:"addr"`
	Auth string `json:"auth"`
	Tx   int64  `json:"tx"`
}

// hysteriaAuthResponse: ok=true admits the connection; id labels it for stats.
type hysteriaAuthResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id,omitempty"`
}

// handleHysteriaAuth validates a hysteria connection by treating its auth string as a
// user sub token. Loopback-only (the daemon runs beside Raven on the EU host).
func (s *Server) handleHysteriaAuth(w http.ResponseWriter, r *http.Request) {
	if !requestFromLoopback(r) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	var req hysteriaAuthRequest
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&req); err != nil || req.Auth == "" {
		writeJSONOK(w, hysteriaAuthResponse{OK: false})
		return
	}
	user, err := s.db.GetUserByToken(req.Auth)
	if err != nil || user == nil || !user.Enabled {
		writeJSONOK(w, hysteriaAuthResponse{OK: false})
		return
	}
	writeJSONOK(w, hysteriaAuthResponse{OK: true, ID: user.Username})
}

func writeJSONOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// buildHysteria2URI builds the per-user client URI. auth = user sub token; obfs/sni are
// shared. With a real cert on the server the URI needs no pinSHA256 (mainstream clients).
func buildHysteria2URI(auth, name string, h *HysteriaConfigView) string {
	q := url.Values{}
	if h.ObfsType != "" && h.ObfsPassword != "" {
		q.Set("obfs", h.ObfsType)
		q.Set("obfs-password", h.ObfsPassword)
	}
	if h.SNI != "" {
		q.Set("sni", h.SNI)
	}
	if h.CertPin != "" {
		// Self-signed cert: pinSHA256 alone is NOT enough — native hysteria still runs CA
		// chain verification (fails "unknown authority"). insecure=1 skips CA/hostname checks;
		// the pin then verifies the exact cert fingerprint, so MITM is still rejected. This
		// keeps the cert off CT logs while staying secure (trust anchored on the pin).
		q.Set("pinSHA256", h.CertPin)
		q.Set("insecure", "1")
	}
	return fmt.Sprintf("hysteria2://%s@%s:%d/?%s#%s",
		url.QueryEscape(auth), h.Host, h.Port, q.Encode(), url.QueryEscape(name))
}

// HysteriaConfigView is the subset of config.HysteriaConfig the builder needs (kept local
// so the api package doesn't import a concrete config struct for one helper).
type HysteriaConfigView struct {
	Host         string
	Port         int
	ObfsType     string
	ObfsPassword string
	SNI          string
	CertPin      string
}

// handleHysteriaSub returns the user's hysteria2:// URI (txt, or base64 when b64=true).
func (s *Server) handleHysteriaSub(w http.ResponseWriter, r *http.Request, b64 bool) {
	if s.cfg.Hysteria == nil || !s.cfg.Hysteria.Enabled {
		jsonError(w, "hysteria reserve not enabled", http.StatusNotFound)
		return
	}
	token := mux.Vars(r)["token"]
	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil {
		jsonError(w, "invalid token", http.StatusNotFound)
		return
	}
	if !user.Enabled {
		jsonError(w, "user disabled", http.StatusForbidden)
		return
	}
	if !user.Hy2Enabled {
		jsonError(w, "hysteria2 reserve not enabled for this user", http.StatusForbidden)
		return
	}
	h := s.cfg.Hysteria
	uri := buildHysteria2URI(user.Token, "hy2-reserve", &HysteriaConfigView{
		Host: h.Host, Port: h.Port, ObfsType: h.ObfsType, ObfsPassword: h.ObfsPassword, SNI: h.SNI, CertPin: h.CertPin,
	})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if b64 {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(uri))))
		return
	}
	_, _ = w.Write([]byte(uri))
}

// hysteriaMainSubExtra returns the per-user hysteria2:// URI as a one-element slice when the
// reserve is enabled AND configured to ride the main link-list subscription; else nil.
func (s *Server) hysteriaMainSubExtra(token string) []string {
	h := s.cfg.Hysteria
	if h == nil || !h.Enabled || !h.InMainSub || token == "" {
		return nil
	}
	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil || !user.Enabled || !user.Hy2Enabled {
		return nil
	}
	return []string{buildHysteria2URI(user.Token, "hy2-reserve", &HysteriaConfigView{
		Host: h.Host, Port: h.Port, ObfsType: h.ObfsType, ObfsPassword: h.ObfsPassword, SNI: h.SNI, CertPin: h.CertPin,
	})}
}

// getUserHy2 (admin) returns whether the per-user Hysteria2 reserve is enabled.
func (s *Server) getUserHy2(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	jsonOK(w, map[string]bool{"hy2_enabled": user.Hy2Enabled})
}

// setUserHy2 (admin) toggles the per-user Hysteria2 reserve gate. Body: {"enabled": bool}.
func (s *Server) setUserHy2(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&body); err != nil {
		jsonError(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if err := s.db.SetUserHy2Enabled(user.ID, body.Enabled); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"hy2_enabled": body.Enabled})
}

// hysteriaPseudoInbound renders the hysteria reserve as a synthetic inbound entry for the
// /api/inbounds listing. It is NOT a real Xray inbound (id 0 + config_file "native-daemon"
// + raw_config.pseudo signal that); the dashboard segregates it and gates it per-user via
// the hy2_enabled flag, not via user_clients.
func hysteriaPseudoInbound(h *config.HysteriaConfig) map[string]interface{} {
	return map[string]interface{}{
		"id":          0,
		"tag":         "hysteria2-reserve",
		"protocol":    "hysteria2",
		"port":        h.Port,
		"config_file": "native-daemon",
		"updated_at":  time.Time{},
		"raw_config": map[string]interface{}{
			"tag": "hysteria2-reserve", "protocol": "hysteria2", "port": h.Port, "pseudo": true,
		},
	}
}

func requestFromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
