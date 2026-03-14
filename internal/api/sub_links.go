package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"xray-subscription/internal/models"
	"xray-subscription/internal/xray"
)

func (s *Server) handleSubscriptionLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormat(w, r, "txt")
}

func (s *Server) handleSubscriptionLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormat(w, r, "b64")
}

func (s *Server) handleSubscriptionLinksByFormat(w http.ResponseWriter, r *http.Request, format string) {
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
	clients = applySubscriptionFilters(clients, r)
	if len(clients) == 0 {
		jsonError(w, "no enabled clients matched filters", http.StatusNotFound)
		return
	}

	globalRoutesJSON, err := s.db.GetGlobalClientRoutes()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	balancerStrategy, balancerProbeURL, balancerProbeInterval, err := s.getEffectiveBalancerConfig()
	if err != nil {
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
		jsonError(w, "could not generate config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if format == "b64" {
		writeProxyLinksB64(w, user.Username, cfg)
		return
	}
	writeProxyLinksText(w, user.Username, cfg)
}

func applySubscriptionFilters(clients []models.UserClientFull, r *http.Request) []models.UserClientFull {
	result := clients
	if p := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol"))); p != "" {
		filtered := make([]models.UserClientFull, 0, len(result))
		for _, c := range result {
			if strings.EqualFold(c.InboundProtocol, p) {
				filtered = append(filtered, c)
			}
		}
		result = filtered
	}
	if t := strings.TrimSpace(r.URL.Query().Get("inbound_tag")); t != "" {
		filtered := make([]models.UserClientFull, 0, len(result))
		for _, c := range result {
			if c.InboundTag == t {
				filtered = append(filtered, c)
			}
		}
		result = filtered
	}
	return result
}

func writeProxyLinksText(w http.ResponseWriter, username string, cfg *xray.ClientConfig) {
	links := buildProxyLinks(cfg)
	payload := strings.Join(links, "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscription.txt"`)
	w.Header().Set("Profile-Title", username)
	_, _ = w.Write([]byte(payload))
}

func writeProxyLinksB64(w http.ResponseWriter, username string, cfg *xray.ClientConfig) {
	links := buildProxyLinks(cfg)
	plain := strings.Join(links, "\n")
	payload := base64.StdEncoding.EncodeToString([]byte(plain))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscription.b64.txt"`)
	w.Header().Set("Profile-Title", username)
	_, _ = w.Write([]byte(payload))
}

func buildProxyLinks(cfg *xray.ClientConfig) []string {
	if cfg == nil {
		return nil
	}
	links := make([]string, 0, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		switch ob.Protocol {
		case "vless":
			if l := buildVLESSLink(ob); l != "" {
				links = append(links, l)
			}
		case "vmess":
			if l := buildVMessLink(ob); l != "" {
				links = append(links, l)
			}
		case "trojan":
			if l := buildTrojanLink(ob); l != "" {
				links = append(links, l)
			}
		case "shadowsocks":
			if l := buildSSLink(ob); l != "" {
				links = append(links, l)
			}
		}
	}
	return links
}

func buildVLESSLink(ob xray.Outbound) string {
	var s xray.VLESSOutboundSettings
	if err := json.Unmarshal(ob.Settings, &s); err != nil || len(s.Vnext) == 0 || len(s.Vnext[0].Users) == 0 {
		return ""
	}
	vn := s.Vnext[0]
	u := vn.Users[0]
	params := url.Values{}
	params.Set("encryption", firstNonEmptyString(u.Encryption, "none"))
	if u.Flow != "" {
		params.Set("flow", u.Flow)
	}
	if ob.StreamSettings != nil {
		params.Set("type", firstNonEmptyString(ob.StreamSettings.Network, "tcp"))
		if ob.StreamSettings.Security != "" {
			params.Set("security", ob.StreamSettings.Security)
		}
		if ob.StreamSettings.Security == "reality" && ob.StreamSettings.RealitySettings != nil {
			rs := ob.StreamSettings.RealitySettings
			if rs.ServerName != "" {
				params.Set("sni", rs.ServerName)
			}
			if rs.Fingerprint != "" {
				params.Set("fp", rs.Fingerprint)
			}
			if rs.PublicKey != "" {
				params.Set("pbk", rs.PublicKey)
			}
			if rs.ShortId != "" {
				params.Set("sid", rs.ShortId)
			}
			if rs.SpiderX != "" {
				params.Set("spx", rs.SpiderX)
			}
		}
		applyTransportParams(params, ob.StreamSettings)
	}
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
		url.QueryEscape(u.ID), vn.Address, vn.Port, params.Encode(), url.QueryEscape(ob.Tag))
}

func buildVMessLink(ob xray.Outbound) string {
	var s xray.VMessOutboundSettings
	if err := json.Unmarshal(ob.Settings, &s); err != nil || len(s.Vnext) == 0 || len(s.Vnext[0].Users) == 0 {
		return ""
	}
	vn := s.Vnext[0]
	u := s.Vnext[0].Users[0]
	item := map[string]string{
		"v":   "2",
		"ps":  ob.Tag,
		"add": vn.Address,
		"port": strconv.Itoa(vn.Port),
		"id":  u.ID,
		"aid": strconv.Itoa(u.AlterId),
		"scy": firstNonEmptyString(u.Security, "auto"),
		"net": "tcp",
		"type": "none",
		"host": "",
		"path": "",
		"tls":  "",
		"sni":  "",
		"alpn": "",
		"fp":   "",
	}
	if ob.StreamSettings != nil {
		if ob.StreamSettings.Network != "" {
			item["net"] = ob.StreamSettings.Network
		}
		if ob.StreamSettings.Security == "tls" || ob.StreamSettings.Security == "reality" {
			item["tls"] = ob.StreamSettings.Security
		}
		if ob.StreamSettings.TLSSettings != nil {
			item["sni"] = ob.StreamSettings.TLSSettings.ServerName
			item["fp"] = ob.StreamSettings.TLSSettings.Fingerprint
		}
		if ob.StreamSettings.RealitySettings != nil {
			item["sni"] = ob.StreamSettings.RealitySettings.ServerName
			item["fp"] = ob.StreamSettings.RealitySettings.Fingerprint
		}
		applyVMessTransport(item, ob.StreamSettings)
	}
	raw, _ := json.Marshal(item)
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

func buildTrojanLink(ob xray.Outbound) string {
	var s xray.TrojanOutboundSettings
	if err := json.Unmarshal(ob.Settings, &s); err != nil || len(s.Servers) == 0 {
		return ""
	}
	sv := s.Servers[0]
	params := url.Values{}
	if ob.StreamSettings != nil {
		params.Set("type", firstNonEmptyString(ob.StreamSettings.Network, "tcp"))
		if ob.StreamSettings.Security != "" {
			params.Set("security", ob.StreamSettings.Security)
		}
		if ob.StreamSettings.TLSSettings != nil {
			if ob.StreamSettings.TLSSettings.ServerName != "" {
				params.Set("sni", ob.StreamSettings.TLSSettings.ServerName)
			}
			if ob.StreamSettings.TLSSettings.Fingerprint != "" {
				params.Set("fp", ob.StreamSettings.TLSSettings.Fingerprint)
			}
		}
		applyTransportParams(params, ob.StreamSettings)
	}
	return fmt.Sprintf("trojan://%s@%s:%d?%s#%s",
		url.QueryEscape(sv.Password), sv.Address, sv.Port, params.Encode(), url.QueryEscape(ob.Tag))
}

func buildSSLink(ob xray.Outbound) string {
	var s xray.ShadowsocksOutboundSettings
	if err := json.Unmarshal(ob.Settings, &s); err != nil || len(s.Servers) == 0 {
		return ""
	}
	sv := s.Servers[0]
	cred := base64.StdEncoding.EncodeToString([]byte(sv.Method + ":" + sv.Password))
	return fmt.Sprintf("ss://%s@%s:%d#%s", cred, sv.Address, sv.Port, url.QueryEscape(ob.Tag))
}

func applyTransportParams(params url.Values, ss *xray.StreamSettings) {
	if ss == nil {
		return
	}
	switch ss.Network {
	case "ws":
		var ws map[string]interface{}
		if err := json.Unmarshal(ss.WSSettings, &ws); err == nil {
			if v, ok := ws["path"].(string); ok && v != "" {
				params.Set("path", v)
			}
			if v, ok := ws["host"].(string); ok && v != "" {
				params.Set("host", v)
			}
		}
	case "grpc":
		var g map[string]interface{}
		if err := json.Unmarshal(ss.GRPCSettings, &g); err == nil {
			if v, ok := g["serviceName"].(string); ok && v != "" {
				params.Set("serviceName", v)
			}
		}
	case "xhttp":
		var xh map[string]interface{}
		if err := json.Unmarshal(ss.XHTTPSettings, &xh); err == nil {
			if v, ok := xh["path"].(string); ok && v != "" {
				params.Set("path", v)
			}
			if v, ok := xh["host"].(string); ok && v != "" {
				params.Set("host", v)
			}
			if v, ok := xh["mode"].(string); ok && v != "" {
				params.Set("mode", v)
			}
		}
	}
}

func applyVMessTransport(item map[string]string, ss *xray.StreamSettings) {
	if ss == nil {
		return
	}
	switch ss.Network {
	case "ws":
		var ws map[string]interface{}
		if err := json.Unmarshal(ss.WSSettings, &ws); err == nil {
			if v, ok := ws["path"].(string); ok {
				item["path"] = v
			}
			if v, ok := ws["host"].(string); ok {
				item["host"] = v
			}
		}
	case "grpc":
		var g map[string]interface{}
		if err := json.Unmarshal(ss.GRPCSettings, &g); err == nil {
			if v, ok := g["serviceName"].(string); ok {
				item["path"] = v
			}
		}
	}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

