package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/xray"
)

// parseLinkParams strips the proto://… prefix and returns the URI parameters.
func parseLinkParams(t *testing.T, link string) url.Values {
	t.Helper()
	q := link
	if i := strings.Index(q, "?"); i >= 0 {
		q = q[i+1:]
	}
	if i := strings.Index(q, "#"); i >= 0 {
		q = q[:i]
	}
	v, err := url.ParseQuery(q)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", link, err)
	}
	return v
}

func vlessOutboundSettings(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(xray.VLESSOutboundSettings{
		Vnext: []xray.VLESSServer{{
			Address: "example.com",
			Port:    443,
			Users:   []xray.VLESSUser{{ID: "uuid-x", Encryption: "none"}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal vless settings: %v", err)
	}
	return raw
}

func TestBuildVLESSLink_FinalmaskEmitted(t *testing.T) {
	base := vlessOutboundSettings(t)
	// Inject finalmask alongside the standard VLESS settings.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(base, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	finalmask := json.RawMessage(`{"tcpmasks":[{"fragment":{"packets":"tlshello","length":"100-200"}}]}`)
	generic["finalmask"] = finalmask
	withFM, _ := json.Marshal(generic)

	ob := xray.Outbound{
		Tag:      "test",
		Protocol: "vless",
		Settings: withFM,
		StreamSettings: &xray.StreamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &xray.RealitySettings{
				ServerName: "example.com",
				PublicKey:  "PBK",
				ShortId:    "0011",
			},
		},
	}
	link := buildVLESSLink(ob, false)
	params := parseLinkParams(t, link)
	got := params.Get("fm")
	if got == "" {
		t.Fatalf("expected fm parameter, got empty; link=%s", link)
	}
	// Ensure the JSON survives a round-trip (URL-decoded by ParseQuery).
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("fm is not valid JSON after URL-decode: %v (raw=%s)", err, got)
	}
	if _, ok := decoded["tcpmasks"]; !ok {
		t.Errorf("fm missing tcpmasks; raw=%s", got)
	}
}

func TestBuildVLESSLink_FinalmaskAbsent_NoFM(t *testing.T) {
	ob := xray.Outbound{
		Tag:      "plain",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &xray.RealitySettings{
				ServerName: "example.com",
				PublicKey:  "PBK",
				ShortId:    "0011",
			},
		},
	}
	link := buildVLESSLink(ob, false)
	if got := parseLinkParams(t, link).Get("fm"); got != "" {
		t.Errorf("expected no fm parameter, got %q (link=%s)", got, link)
	}
}

func TestBuildVLESSLink_TLSPCSVCNEmitted(t *testing.T) {
	ob := xray.Outbound{
		Tag:      "tls-test",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:  "tcp",
			Security: "tls",
			TLSSettings: &xray.TLSSettings{
				ServerName:           "example.com",
				Fingerprint:          "chrome",
				PinnedPeerCertSha256: []string{"AAAA1111", "BBBB2222"},
				VerifyPeerCertByName: "leaf.example.com",
			},
		},
	}
	link := buildVLESSLink(ob, false)
	params := parseLinkParams(t, link)
	if got := params.Get("pcs"); got != "AAAA1111,BBBB2222" {
		t.Errorf("pcs: got %q, want %q", got, "AAAA1111,BBBB2222")
	}
	if got := params.Get("vcn"); got != "leaf.example.com" {
		t.Errorf("vcn: got %q, want %q", got, "leaf.example.com")
	}
	if got := params.Get("security"); got != "tls" {
		t.Errorf("security: got %q, want tls", got)
	}
}

func TestBuildVLESSLink_RealityNoPCSVCN(t *testing.T) {
	// pcs/vcn must be absent for security=reality even if TLSSettings is somehow set.
	ob := xray.Outbound{
		Tag:      "reality-only",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:  "tcp",
			Security: "reality",
			RealitySettings: &xray.RealitySettings{
				ServerName: "example.com",
				PublicKey:  "PBK",
				ShortId:    "0011",
			},
			TLSSettings: &xray.TLSSettings{
				PinnedPeerCertSha256: []string{"DEADBEEF"},
				VerifyPeerCertByName: "ignored.example.com",
			},
		},
	}
	link := buildVLESSLink(ob, false)
	params := parseLinkParams(t, link)
	if got := params.Get("pcs"); got != "" {
		t.Errorf("pcs should be empty under security=reality, got %q", got)
	}
	if got := params.Get("vcn"); got != "" {
		t.Errorf("vcn should be empty under security=reality, got %q", got)
	}
}

func xhttpStreamSettings(t *testing.T, withExtra bool) json.RawMessage {
	t.Helper()
	xh := map[string]interface{}{
		"path": "/api/v4/sync",
		"host": "www.python.org",
		"mode": "packet-up",
	}
	if withExtra {
		xh["extra"] = map[string]interface{}{
			"xPaddingBytes": "100-1000",
			"xmux": map[string]interface{}{
				"maxConcurrency": "16-32",
				"cMaxReuseTimes": "64-128",
			},
		}
	}
	raw, err := json.Marshal(xh)
	if err != nil {
		t.Fatalf("marshal xhttp settings: %v", err)
	}
	return raw
}

// The vless:// URI must carry the xhttp "extra" (xmux + xPaddingBytes) so URI-import
// clients (v2rayN) receive the anti-volumetric levers. Dropping it was the prod root
// cause of the M1 download freeze on mobile DPI (2026-06-10).
func TestBuildVLESSLink_XHTTPExtraEmitted(t *testing.T) {
	ob := xray.Outbound{
		Tag:      "xhttp-extra",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:         "xhttp",
			Security:        "reality",
			RealitySettings: &xray.RealitySettings{ServerName: "www.python.org", PublicKey: "PBK", ShortId: "0011"},
			XHTTPSettings:   xhttpStreamSettings(t, true),
		},
	}
	params := parseLinkParams(t, buildVLESSLink(ob, true))
	if got := params.Get("mode"); got != "packet-up" {
		t.Errorf("mode: got %q, want packet-up", got)
	}
	ex := params.Get("extra")
	if ex == "" {
		t.Fatal("extra param missing — xmux would not reach URI clients")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(ex), &parsed); err != nil {
		t.Fatalf("extra is not valid JSON (%q): %v", ex, err)
	}
	if _, ok := parsed["xmux"]; !ok {
		t.Errorf("extra missing xmux: %v", parsed)
	}
	if parsed["xPaddingBytes"] != "100-1000" {
		t.Errorf("extra xPaddingBytes: got %v, want 100-1000", parsed["xPaddingBytes"])
	}
}

func TestBuildVLESSLink_XHTTPNoExtraWhenAbsent(t *testing.T) {
	ob := xray.Outbound{
		Tag:      "xhttp-noextra",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:         "xhttp",
			Security:        "reality",
			RealitySettings: &xray.RealitySettings{ServerName: "www.python.org", PublicKey: "PBK", ShortId: "0011"},
			XHTTPSettings:   xhttpStreamSettings(t, false),
		},
	}
	params := parseLinkParams(t, buildVLESSLink(ob, true))
	if got := params.Get("extra"); got != "" {
		t.Errorf("extra should be absent when xhttp has no extra, got %q", got)
	}
}

// The UA gate: when the client is NOT v2rayN-family (emitXHTTPExtra=false), `extra`
// must be dropped even if present, so Happ/libXray/sing-box clients keep connecting.
// The rest of the URI (mode/path/host/reality) stays intact → clean URI still works.
func TestBuildVLESSLink_XHTTPExtraGatedOff(t *testing.T) {
	ob := xray.Outbound{
		Tag:      "xhttp-gated",
		Protocol: "vless",
		Settings: vlessOutboundSettings(t),
		StreamSettings: &xray.StreamSettings{
			Network:         "xhttp",
			Security:        "reality",
			RealitySettings: &xray.RealitySettings{ServerName: "www.python.org", PublicKey: "PBK", ShortId: "0011"},
			XHTTPSettings:   xhttpStreamSettings(t, true),
		},
	}
	params := parseLinkParams(t, buildVLESSLink(ob, false))
	if got := params.Get("extra"); got != "" {
		t.Errorf("extra must be absent when emitXHTTPExtra=false (Happ/libXray gate), got %q", got)
	}
	if got := params.Get("mode"); got != "packet-up" {
		t.Errorf("mode should still be present on the clean URI, got %q", got)
	}
}

func TestClientSupportsXHTTPExtra(t *testing.T) {
	cases := map[string]bool{
		"v2rayN/7.22.5":  true,
		"v2rayNG/1.10.7": true,
		"V2RayN/7.0":     true, // case-insensitive
		"Happ/3.23.0":    false,
		"Streisand":      false,
		"sing-box 1.10":  false,
		"Hiddify/2.0":    false,
		"":               false,
	}
	for ua, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/sub/x/links.txt", nil)
		if ua != "" {
			r.Header.Set("User-Agent", ua)
		}
		if got := clientSupportsXHTTPExtra(r); got != want {
			t.Errorf("UA %q: got %v, want %v", ua, got, want)
		}
	}
	if clientSupportsXHTTPExtra(nil) {
		t.Error("nil request must not be treated as extra-capable")
	}
}

func TestBuildTrojanLink_TLSPCSVCNEmitted(t *testing.T) {
	settings, _ := json.Marshal(xray.TrojanOutboundSettings{
		Servers: []xray.TrojanServer{{Address: "example.com", Port: 443, Password: "secret"}},
	})
	ob := xray.Outbound{
		Tag:      "trojan-tls",
		Protocol: "trojan",
		Settings: settings,
		StreamSettings: &xray.StreamSettings{
			Network:  "tcp",
			Security: "tls",
			TLSSettings: &xray.TLSSettings{
				ServerName:           "example.com",
				PinnedPeerCertSha256: []string{"FFEE0011"},
				VerifyPeerCertByName: "leaf.example.com",
			},
		},
	}
	link := buildTrojanLink(ob, false)
	params := parseLinkParams(t, link)
	if got := params.Get("pcs"); got != "FFEE0011" {
		t.Errorf("pcs: got %q, want %q", got, "FFEE0011")
	}
	if got := params.Get("vcn"); got != "leaf.example.com" {
		t.Errorf("vcn: got %q, want %q", got, "leaf.example.com")
	}
}

func TestExtractFinalmask_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool // want non-empty result
	}{
		{"nil", "", false},
		{"no key", `{"vnext":[]}`, false},
		{"null finalmask", `{"finalmask":null}`, false},
		{"empty object finalmask", `{"finalmask":{}}`, false},
		{"non-empty finalmask", `{"finalmask":{"tcpmasks":[{"fragment":{"packets":"tlshello"}}]}}`, true},
		{"malformed", `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFinalmask(json.RawMessage(tc.in))
			if (got != "") != tc.want {
				t.Errorf("got %q, want non-empty=%v", got, tc.want)
			}
		})
	}
}
