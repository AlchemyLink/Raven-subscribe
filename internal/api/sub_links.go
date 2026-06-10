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

	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/alchemylink/raven-subscribe/internal/xray"
	"github.com/gorilla/mux"
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

func (s *Server) handleVLESSList(w http.ResponseWriter, r *http.Request) {
	cfg, username, err := s.generateConfigForSubscriptionRequest(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	entries := buildProxyLinkEntries(cfg, clientSupportsXHTTPExtra(r))
	list := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		if e.Protocol != "vless" {
			continue
		}
		list = append(list, map[string]string{
			"tag":        e.Tag,
			"url":        e.URL,
			"url_b64":    base64.StdEncoding.EncodeToString([]byte(e.URL)),
			"by_tag":     fmt.Sprintf("%s/vless/%s", strings.TrimSuffix(extractSubBaseURL(r), "/"), url.PathEscape(e.Tag)),
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
	entries := buildProxyLinkEntries(cfg, clientSupportsXHTTPExtra(r))
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

	// Fold the per-user hy2 reserve into the GENERAL link list only (not protocol-forced lists).
	var extra []string
	if forcedProtocol == "" {
		extra = s.hysteriaMainSubExtra(mux.Vars(r)["token"])
	}
	emitXHTTPExtra := clientSupportsXHTTPExtra(r)
	if format == "b64" {
		writeProxyLinksB64(w, username, cfg, emitXHTTPExtra, extra)
		return
	}
	writeProxyLinksText(w, username, cfg, emitXHTTPExtra, extra)
}

// filterExcludedInbounds drops clients whose inbound tag is in cfg.ExcludeInboundTags.
// Called by every subscription-serving path (JSON, links, links-index, fallback) so an
// excluded transport never appears in what clients download, while the inbound stays
// alive on the server. Must run before the FallbackInboundTags split.
func (s *Server) filterExcludedInbounds(clients []models.UserClientFull) []models.UserClientFull {
	if len(s.cfg.ExcludeInboundTags) == 0 {
		return clients
	}
	ex := make(map[string]bool, len(s.cfg.ExcludeInboundTags))
	for _, tag := range s.cfg.ExcludeInboundTags {
		ex[tag] = true
	}
	kept := make([]models.UserClientFull, 0, len(clients))
	for _, c := range clients {
		if !ex[c.InboundTag] {
			kept = append(kept, c)
		}
	}
	return kept
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

func writeProxyLinksText(w http.ResponseWriter, username string, cfg *xray.ClientConfig, emitXHTTPExtra bool, extra ...[]string) {
	links := append(buildProxyLinks(cfg, emitXHTTPExtra), flattenExtra(extra)...)
	payload := strings.Join(links, "\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Profile-Title", username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	_, _ = w.Write([]byte(payload))
}

// flattenExtra merges optional variadic extra-link slices into one slice.
func flattenExtra(extra [][]string) []string {
	var out []string
	for _, e := range extra {
		out = append(out, e...)
	}
	return out
}

func writeProxyLinksB64(w http.ResponseWriter, username string, cfg *xray.ClientConfig, emitXHTTPExtra bool, extra ...[]string) {
	links := append(buildProxyLinks(cfg, emitXHTTPExtra), flattenExtra(extra)...)
	plain := strings.Join(links, "\n")
	payload := base64.StdEncoding.EncodeToString([]byte(plain))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Profile-Title", username)
	w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	_, _ = w.Write([]byte(payload))
}

func buildProxyLinks(cfg *xray.ClientConfig, emitXHTTPExtra bool) []string {
	entries := buildProxyLinkEntries(cfg, emitXHTTPExtra)
	links := make([]string, 0, len(entries))
	for _, e := range entries {
		links = append(links, e.URL)
	}
	return links
}

func buildProxyLinkEntries(cfg *xray.ClientConfig, emitXHTTPExtra bool) []proxyLink {
	if cfg == nil {
		return nil
	}
	links := make([]proxyLink, 0, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		switch ob.Protocol {
		case "vless":
			if l := buildVLESSLink(ob, emitXHTTPExtra); l != "" {
				links = append(links, proxyLink{Protocol: "vless", Tag: ob.Tag, URL: l})
			}
		case "vmess":
			if l := buildVMessLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "vmess", Tag: ob.Tag, URL: l})
			}
		case "trojan":
			if l := buildTrojanLink(ob, emitXHTTPExtra); l != "" {
				links = append(links, proxyLink{Protocol: "trojan", Tag: ob.Tag, URL: l})
			}
		case "shadowsocks":
			if l := buildSSLink(ob); l != "" {
				links = append(links, proxyLink{Protocol: "shadowsocks", Tag: ob.Tag, URL: l})
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
	// ExcludeInboundTags removes tags from EVERY subscription (primary + fallback, all
	// formats), before the FallbackInboundTags split. See filterExcludedInbounds.
	clients = s.filterExcludedInbounds(clients)
	// FallbackInboundTags is symmetric (whitelist on /sub/fallback/*, blacklist on primary).
	if len(s.cfg.FallbackInboundTags) > 0 {
		tagSet := make(map[string]bool, len(s.cfg.FallbackInboundTags))
		for _, tag := range s.cfg.FallbackInboundTags {
			tagSet[tag] = true
		}
		isFallback := r.Context().Value(ctxFallbackKey{}) == true
		filtered := make([]models.UserClientFull, 0, len(clients))
		for _, c := range clients {
			inFallback := tagSet[c.InboundTag]
			if isFallback && inFallback {
				filtered = append(filtered, c)
			} else if !isFallback && !inFallback {
				filtered = append(filtered, c)
			}
		}
		clients = filtered
	}
	clients = applySubscriptionFiltersWithProtocol(clients, r, forcedProtocol)
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
		s.effectiveHost(r),
		s.effectiveInboundHosts(r),
		s.cfg.InboundPorts,
		*user,
		clients,
		globalRoutesJSON,
		balancerStrategy,
		balancerProbeURL,
		balancerProbeInterval,
		s.cfg.SocksInboundPort,
		s.cfg.HTTPInboundPort,
		s.cfg.ClientDNSServers,
		s.cfg.ClientBlackholeResponse,
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

func buildVLESSLink(ob xray.Outbound, emitXHTTPExtra bool) string {
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
		if ob.StreamSettings.Security == "tls" && ob.StreamSettings.TLSSettings != nil {
			applyTLSCertParams(params, ob.StreamSettings.TLSSettings)
		}
		applyTransportParams(params, ob.StreamSettings, emitXHTTPExtra)
	}
	if fm := extractFinalmask(ob.Settings); fm != "" {
		params.Set("fm", fm)
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
		"v":    "2",
		"ps":   ob.Tag,
		"add":  vn.Address,
		"port": strconv.Itoa(vn.Port),
		"id":   u.ID,
		"aid":  strconv.Itoa(u.AlterId),
		"scy":  firstNonEmptyString(u.Security, "auto"),
		"net":  "tcp",
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

func buildTrojanLink(ob xray.Outbound, emitXHTTPExtra bool) string {
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
			applyTLSCertParams(params, ob.StreamSettings.TLSSettings)
		}
		applyTransportParams(params, ob.StreamSettings, emitXHTTPExtra)
	}
	if fm := extractFinalmask(ob.Settings); fm != "" {
		params.Set("fm", fm)
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

// clientSupportsXHTTPExtra reports whether the requesting client can be trusted with
// the vless:// `extra` query param (xmux + xPaddingBytes) WITHOUT breaking the
// connection. Only v2rayN/v2rayNG have mature XHTTP+xmux support and parse `extra`
// correctly. Xray-core-embedded mobile clients (Happ, Streisand, INCY) accept and even
// display the param but then fail to connect on XHTTP+xmux — the handshake completes
// but no HTTP data flows (XTLS/Xray-core#5918, #6048; open / can't-reproduce upstream,
// version-coupling sensitive). sing-box/libXray clients don't parse `extra` at all
// (XTLS/libXray#62). So we emit `extra` ONLY for v2rayN-family User-Agents; everyone
// else gets the clean URI (connects fine, just without the anti-volumetric xmux lever).
// "v2rayn" is a substring of both "v2rayn/x.y" and "v2rayng/x.y" (case-insensitive).
func clientSupportsXHTTPExtra(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.Contains(strings.ToLower(r.UserAgent()), "v2rayn")
}

func applyTransportParams(params url.Values, ss *xray.StreamSettings, emitXHTTPExtra bool) {
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
			// "extra" carries the client-side advanced fields (xmux, xPaddingBytes,
			// scMaxEachPostBytes, ...). vless:// URIs historically dropped it, so the
			// mux + anti-volumetric levers never reached URI-import clients
			// (v2rayN/v2rayNG) — the prod root cause of the M1 download freeze on
			// mobile DPI (proven 2026-06-10: no-xmux stalls 4/5, with-xmux 5/5 clean).
			// v2rayN parses a URL-encoded JSON `extra` query param; emit it so URI subs
			// carry xmux too — but ONLY for clients that handle it (emitXHTTPExtra,
			// gated on User-Agent via clientSupportsXHTTPExtra). Emitting it to Happ/
			// libXray/sing-box clients breaks the connection (XTLS/Xray-core#5918,#6048).
			if emitXHTTPExtra {
				if ex, ok := xh["extra"].(map[string]interface{}); ok && len(ex) > 0 {
					if b, err := json.Marshal(ex); err == nil {
						params.Set("extra", string(b))
					}
				}
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

// extractFinalmask returns the JSON of outbound.settings.finalmask for the URI
// `fm` parameter (Xray share-link standard, v26.2.6+). When the field is absent
// or settings cannot be parsed, returns an empty string and no `fm` is emitted.
func extractFinalmask(settings json.RawMessage) string {
	if len(settings) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(settings, &m); err != nil {
		return ""
	}
	fm, ok := m["finalmask"]
	if !ok {
		return ""
	}
	trimmed := strings.TrimSpace(string(fm))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		return ""
	}
	return trimmed
}

// applyTLSCertParams emits the `pcs` (pinnedPeerCertSha256) and `vcn`
// (verifyPeerCertByName) URI parameters introduced by Xray v26.1.18+ as the
// safe replacement for `allowInsecure` (which auto-disables 2026-06-01 UTC).
// Multiple SHA-256 fingerprints are joined by comma.
func applyTLSCertParams(params url.Values, tls *xray.TLSSettings) {
	if tls == nil {
		return
	}
	if len(tls.PinnedPeerCertSha256) > 0 {
		params.Set("pcs", strings.Join(tls.PinnedPeerCertSha256, ","))
	}
	if tls.VerifyPeerCertByName != "" {
		params.Set("vcn", tls.VerifyPeerCertByName)
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
