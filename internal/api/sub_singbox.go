package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/alchemylink/raven-subscribe/internal/xray"
)

// handleSingboxSubscription serves a sing-box JSON config containing only
// Hysteria2 outbounds for the requesting user.
func (s *Server) handleSingboxSubscription(w http.ResponseWriter, r *http.Request) {
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
		log.Printf("ERROR get user clients for singbox sub %s: %v", sanitizeLogField(user.Username), err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	outbounds := make([]xray.Hysteria2OutboundSettings, 0)
	for i, c := range clients {
		if !strings.EqualFold(c.InboundProtocol, "hysteria2") {
			continue
		}
		var cred xray.StoredClientConfig
		if err := json.Unmarshal([]byte(c.ClientConfig), &cred); err != nil {
			log.Printf("WARN: singbox sub parse cred for inbound %s: %v", c.InboundTag, err)
			continue
		}
		serverName := cred.ServerName
		if serverName == "" {
			serverName = s.cfg.ServerHost
		}
		tag := strings.NewReplacer(" ", "-", "/", "-", "\\", "-").Replace(c.InboundTag)
		ob := xray.Hysteria2OutboundSettings{
			Type:       "hysteria2",
			Tag:        fmt.Sprintf("%s-%d", tag, i),
			Server:     s.cfg.ServerHost,
			ServerPort: c.InboundPort,
			Password:   cred.Password,
			UpMbps:     cred.UpMbps,
			DownMbps:   cred.DownMbps,
			TLS: &xray.Hysteria2TLS{
				Enabled:    true,
				ServerName: serverName,
			},
		}
		if cred.ObfsType != "" {
			ob.Obfs = &xray.Hysteria2Obfs{
				Type:     cred.ObfsType,
				Password: cred.ObfsPassword,
			}
		}
		outbounds = append(outbounds, ob)
	}

	if len(outbounds) == 0 {
		jsonError(w, "no hysteria2 inbounds found", http.StatusNotFound)
		return
	}

	cfg := buildSingboxClientConfig(outbounds)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Profile-Title", user.Username)
	w.Header().Set("Content-Disposition", "attachment; filename=\"singbox.json\"")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(cfg)
}

// buildSingboxClientConfig assembles a minimal sing-box client config.
func buildSingboxClientConfig(outbounds []xray.Hysteria2OutboundSettings) map[string]interface{} {
	obs := make([]interface{}, 0, len(outbounds)+2)
	for _, ob := range outbounds {
		obs = append(obs, ob)
	}
	obs = append(obs,
		map[string]string{"type": "direct", "tag": "direct"},
		map[string]string{"type": "block", "tag": "block"},
	)

	return map[string]interface{}{
		"log": map[string]interface{}{
			"level":     "warn",
			"timestamp": true,
		},
		"inbounds": []map[string]interface{}{
			{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 2080,
			},
		},
		"outbounds": obs,
		"route": map[string]interface{}{
			"auto_detect_interface": true,
			"final":                 outbounds[0].Tag,
		},
	}
}

