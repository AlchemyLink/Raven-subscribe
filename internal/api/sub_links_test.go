package api

import (
	"encoding/json"
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
	link := buildVLESSLink(ob)
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
	link := buildVLESSLink(ob)
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
	link := buildVLESSLink(ob)
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
	link := buildVLESSLink(ob)
	params := parseLinkParams(t, link)
	if got := params.Get("pcs"); got != "" {
		t.Errorf("pcs should be empty under security=reality, got %q", got)
	}
	if got := params.Get("vcn"); got != "" {
		t.Errorf("vcn should be empty under security=reality, got %q", got)
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
	link := buildTrojanLink(ob)
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
