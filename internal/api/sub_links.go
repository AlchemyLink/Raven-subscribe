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
	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/alchemylink/raven-subscribe/internal/xray"
)

type proxyLink struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
	URL      string `json:"url"`
}

func (s *Server) handleSubscriptionLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "txt", "")
}

func (s *Server) handleSubscriptionLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "b64", "")
}

func (s *Server) handleVLESSLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "txt", "vless")
}

func (s *Server) handleVLESSLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "b64", "vless")
}

func (s *Server) handleVMessLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "txt", "vmess")
}

func (s *Server) handleVMessLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "b64", "vmess")
}

func (s *Server) handleTrojanLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "txt", "trojan")
}

func (s *Server) handleTrojanLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "b64", "trojan")
}

func (s *Server) handleShadowsocksLinksText(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "txt", "shadowsocks")
}

func (s *Server) handleShadowsocksLinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleSubscriptionLinksByFormatAndProtocol(w, r, "b64", "shadowsocks")
}

func (s *Server) handleHysteria2LinksText(w http.ResponseWriter, r *http.Request) {
	s.handleHysteria2LinksByFormat(w, r, "txt")
}

func (s *Server) handleHysteria2LinksB64(w http.ResponseWriter, r *http.Request) {
	s.handleHysteria2LinksByFormat(w, r, "b64")
}

func (s *Server) handleVLESSList(w http.ResponseWriter, r *http.Request) {
	cfg, username, err := s.generateConfigForSubscriptionRequest(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	entries := buildProxyLinkEntries(cfg)
	list := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		if e.Protocol != "vless" {
			continue
		}
		list = append(list, map[string]string{
			"tag":      e.Tag,
			"url":      e.URL,
			"url_b64":  base64.StdEncoding.EncodeToString([]byte(e.URL)),
			"by_tag":   fmt.Sprintf("%s/vless/%s", strings.TrimSuffix(extractSubBaseURL(r), "/"), url.PathEscape(e.Tag)),
			"by_tag_b64": fmt.Sprintf("%s/vless/%s/b64", strings.TrimSuffix(extractSubBaseURL(r), "/"), url.PathEscape(e.Tag)),
		})
	}
	jsonOK(w, map[string]interface{}{
		"profile_title": username,
		"count":         len(list),
		"items":         list,
	})
}

func (s *Server) handleVLESSLinkByTagText(w http.ResponseWriter, r *http.Request) {
	s.handleVLESSLinkByTag(w, r, "txt")
}

func (s *Server) handleVLESSLinkByTagB64(w http.ResponseWriter, r *http.Request) {
	s.handleVLESSLinkByTag(w, r, "b64")
}

func (s *Server) handleVLESSLinkByTag(w http.ResponseWriter, r *http.Request, format string) {
	tag := strings.TrimSpace(mux.Vars(r)["vlessTag"])
	if tag == "" {
		jsonError(w, "missing vless tag", http.StatusBadRequest)
		return
	}
	cfg, username, err := s.generateConfigForSubscriptionRequest(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	entries := buildProxyLinkEntries(cfg)
	for _, e := range entries {
		if e.Protocol != "vless" {
			continue
		}
		if e.Tag != tag {
			continue
		}
		if format == "b64" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Profile-Title", username)
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(e.URL))))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Profile-Title", username)
		_, _ = w.Write([]byte(e.URL))
		return
	}
	jsonError(w, "vless link not found for tag: "+tag, http.StatusNotFound)
}

func (s *Server) handleSubscriptionLinksByFormatAndProtocol(w http.ResponseWriter, r *http.Request, format string, forcedProtocol string) {
	cfg, username, err := s.generateConfigForSubscriptionRequestWithForcedProtocol(r, forcedProtocol)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	if format == "b64" {
		writeProxyLinksB64(w, username, cfg)
		return
	}
	writeProxyLinksText(w, username, cfg)
}

func applySubscriptionFiltersWithProtocol(clients []models.UserClientFull, r *http.Request, forcedProtocol string) []models.UserClientFull {
	result := clients
	p := strings.ToLower(strings.TrimSpace(forcedProtocol))
	if p == "" {
		p = extractProtocolFilter(r)
	}
	if p != "" {
		filtered := make([]models.UserClientFull, 0, len(result))
		for _, c := range result {
			if strings.EqualFold(c.InboundProtocol, p) {
				filtered = append(filtered, c)
			}
		}
		result = filtered
	}
	if t := extractInboundTagFilter(r); t != "" {
		filtered := make([]models.UserClientFull, 0, len(result))
		for i, c := range result {
			if matchesInboundTagFilter(c.InboundTag, t, i) {
				filtered = append(filtered, c)
			}
		}
		result = filtered
	}
	return result
}

func extractProtocolFilter(r *http.Request) string {
	if r == nil {
		return ""
	}
	if p := strings.ToLower(strings.TrimSpace(mux.Vars(r)["protocol"])); p != "" {
		return p
	}
	return strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
}

func extractInboundTagFilter(r *http.Request) string {
	if r == nil {
		return ""
	}
	if t := strings.TrimSpace(mux.Vars(r)["inboundTag"]); t != "" {
		return t
	}
	return strings.TrimSpace(r.URL.Query().Get("inbound_tag"))
}

func matchesInboundTagFilter(inboundTag, filter string, index int) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if inboundTag == filter {
		return true
	}
	sanitized := strings.NewReplacer(" ", "-", "/", "-", "\\", "-").Replace(inboundTag)
	if sanitized == filter {
		return true
	}
	// Also accept generated outbound-like tag (<sanitized inbound tag>-<index>),
	// e.g. filter=vless-xhttp-in-1
	return fmt.Sprintf("%s-%d", sanitized, index) == filter
}

func writeProxyLinksText(w http.ResponseWriter, username string, cfg *xray.ClientConfig) {
	links := buildProxyLinks(cfg)
	payload := strings.Join(links, "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Profile-Title", username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	_, _ = w.Write([]byte(payload))
}

func writeProxyLinksB64(w http.ResponseWriter, username string, cfg *xray.ClientConfig) {
	links := buildProxyLinks(cfg)
	plain := strings.Join(links, "\n")
	payload := base64.StdEncoding.EncodeToString([]byte(plain))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Profile-Title", username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	_, _ = w.Write([]byte(payload))
}

func buildProxyLinks(cfg *xray.ClientConfig) []string {
	entries := buildProxyLinkEntries(cfg)
	links := make([]string, 0, len(entries))
	for _, e := range entries {
		links = append(links, e.URL)
	}
	return links
}

func buildProxyLinkEntries(cfg *xray.ClientConfig) []proxyLink {
	if cfg == nil {
		return nil
	}
	links := make([]proxyLink, 0, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		switch ob.Protocol {
		case "vless":
			if l := buildVLESSLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "vless", Tag: ob.Tag, URL: l})
			}
		case "vmess":
			if l := buildVMessLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "vmess", Tag: ob.Tag, URL: l})
			}
		case "trojan":
			if l := buildTrojanLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "trojan", Tag: ob.Tag, URL: l})
			}
		case "shadowsocks":
			if l := buildSSLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "shadowsocks", Tag: ob.Tag, URL: l})
			}
		case "hysteria2":
			if l := buildHysteria2Link(ob); l != "" {
				links = append(links, proxyLink{Protocol: "hysteria2", Tag: ob.Tag, URL: l})
			}
		}
	}
	return links
}

func (s *Server) generateConfigForSubscriptionRequest(r *http.Request) (*xray.ClientConfig, string, error) {
	return s.generateConfigForSubscriptionRequestWithForcedProtocol(r, "")
}

func (s *Server) generateConfigForSubscriptionRequestWithForcedProtocol(r *http.Request, forcedProtocol string) (*xray.ClientConfig, string, error) {
	token := mux.Vars(r)["token"]
	if token == "" {
		return nil, "", fmt.Errorf("missing token")
	}
	user, err := s.db.GetUserByToken(token)
	if err != nil || user == nil {
		return nil, "", fmt.Errorf("invalid token")
	}
	if !user.Enabled {
		return nil, "", fmt.Errorf("user disabled")
	}
	clients, err := s.db.GetUserClients(user.ID)
	if err != nil {
		// #nosec G706 -- username is sanitized before logging.
		log.Printf("ERROR get user clients for %s: %v", sanitizeLogField(user.Username), err)
		return nil, "", fmt.Errorf("internal error")
	}
	clients = applySubscriptionFiltersWithProtocol(clients, r, forcedProtocol)
	// Hysteria2 uses sing-box format — exclude from Xray JSON subscription.
	// Hysteria2 clients are served via /sub/{token}/singbox and share links.
	clients = excludeProtocol(clients, "hysteria2")
	if len(clients) == 0 {
		return nil, "", fmt.Errorf("no enabled clients matched filters")
	}
	globalRoutesJSON, err := s.db.GetGlobalClientRoutes()
	if err != nil {
		return nil, "", fmt.Errorf("internal error")
	}
	balancerStrategy, balancerProbeURL, balancerProbeInterval, err := s.getEffectiveBalancerConfig()
	if err != nil {
		return nil, "", fmt.Errorf("internal error")
	}
	cfg, err := xray.GenerateClientConfig(
		s.cfg.ServerHost,
		*user,
		clients,
		globalRoutesJSON,
		balancerStrategy,
		balancerProbeURL,
		balancerProbeInterval,
		s.cfg.SocksInboundPort,
		s.cfg.HTTPInboundPort,
	)
	if err != nil {
		return nil, "", fmt.Errorf("could not generate config: %s", err.Error())
	}
	return cfg, user.Username, nil
}

func extractSubBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	token := mux.Vars(r)["token"]
	return fmt.Sprintf("%s://%s/sub/%s", scheme, host, token)
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
			if rs.MLDSA65Verify != "" {
				params.Set("pqv", rs.MLDSA65Verify)
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

func excludeProtocol(clients []models.UserClientFull, protocol string) []models.UserClientFull {
	filtered := clients[:0]
	for _, c := range clients {
		if !strings.EqualFold(c.InboundProtocol, protocol) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func (s *Server) handleHysteria2LinksByFormat(w http.ResponseWriter, r *http.Request, format string) {
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
		log.Printf("ERROR get user clients for hysteria2 sub %s: %v", sanitizeLogField(user.Username), err) // #nosec G706 -- username is sanitized before logging.
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	links := make([]string, 0)
	for i, c := range clients {
		if !strings.EqualFold(c.InboundProtocol, "hysteria2") {
			continue
		}
		var cred xray.StoredClientConfig
		if err := json.Unmarshal([]byte(c.ClientConfig), &cred); err != nil {
			log.Printf("WARN: hysteria2 sub parse cred for inbound %s: %v", c.InboundTag, err)
			continue
		}
		serverName := cred.ServerName
		if serverName == "" {
			serverName = s.cfg.ServerHost
		}
		tag := strings.NewReplacer(" ", "-", "/", "-", "\\", "-").Replace(c.InboundTag)
		tag = fmt.Sprintf("%s-%d", tag, i)

		params := url.Values{}
		if serverName != "" {
			params.Set("sni", serverName)
		}
		if cred.ObfsType != "" {
			params.Set("obfs", cred.ObfsType)
			if cred.ObfsPassword != "" {
				params.Set("obfs-password", cred.ObfsPassword)
			}
		}
		params.Set("insecure", "0")
		link := fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s",
			url.QueryEscape(cred.Password), s.cfg.ServerHost, c.InboundPort, params.Encode(), url.QueryEscape(tag))
		links = append(links, link)
	}

	if len(links) == 0 {
		jsonError(w, "no hysteria2 inbounds found", http.StatusNotFound)
		return
	}

	plain := strings.Join(links, "\n")
	w.Header().Set("Profile-Title", user.Username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if format == "b64" {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(plain))))
		return
	}
	_, _ = w.Write([]byte(plain))
}

func buildHysteria2Link(ob xray.Outbound) string {
	var s xray.Hysteria2OutboundSettings
	if err := json.Unmarshal(ob.Settings, &s); err != nil {
		return ""
	}
	if s.Password == "" || s.Server == "" || s.ServerPort == 0 {
		return ""
	}
	params := url.Values{}
	if s.TLS != nil && s.TLS.ServerName != "" {
		params.Set("sni", s.TLS.ServerName)
	}
	if s.Obfs != nil && s.Obfs.Type != "" {
		params.Set("obfs", s.Obfs.Type)
		if s.Obfs.Password != "" {
			params.Set("obfs-password", s.Obfs.Password)
		}
	}
	params.Set("insecure", "0")
	q := params.Encode()
	link := fmt.Sprintf("hysteria2://%s@%s:%d",
		url.QueryEscape(s.Password), s.Server, s.ServerPort)
	if q != "" {
		link += "?" + q
	}
	link += "#" + url.QueryEscape(ob.Tag)
	return link
}

