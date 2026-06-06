package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAnsibleContract_ParserShape is the cross-repo guardrail between Ansible
// inbound templates (Raven-server-install) and this parser. The fixtures in
// testdata/ansible-rendered/conf.d are produced by Raven-server-install's
// `tests/run.sh` against deterministic test secrets. Any field-shape change
// in either repo without a corresponding update in the other will be caught
// here at CI time instead of at 4 AM after a deploy.
//
// Refresh procedure: scripts/refresh-ansible-fixtures.sh — see
// testdata/ansible-rendered/README.md.
func TestAnsibleContract_ParserShape(t *testing.T) {
	const fixtureDir = "../../testdata/ansible-rendered/conf.d"

	parsed, err := ParseConfigDir(fixtureDir)
	if err != nil {
		t.Fatalf("ParseConfigDir: %v", err)
	}

	// Collect every parsed inbound across files keyed by tag.
	byTag := make(map[string]ParsedInbound)
	for _, inbounds := range parsed {
		for _, ib := range inbounds {
			byTag[ib.Tag] = ib
		}
	}

	// The Ansible test pipeline is configured to render exactly these three
	// inbounds (primary v2 reality + xhttp, isolated fallback). If a fourth
	// shows up, either Ansible added one (update this list) or a stray file
	// is present (regenerate fixtures).
	wantTags := []string{
		"vless-reality-v2-in",
		"vless-xhttp-v2-in",
		"vless-fallback-in",
	}
	if got, want := len(byTag), len(wantTags); got != want {
		gotTags := make([]string, 0, got)
		for t := range byTag {
			gotTags = append(gotTags, t)
		}
		t.Fatalf("inbound count: got %d %v, want %d %v", got, gotTags, want, wantTags)
	}

	// Per-tag expectations. Port comes from the deterministic Ansible test
	// fixtures; if Ansible changes a default port, refresh + update here.
	cases := []struct {
		tag      string
		port     int
		wantFlow string // empty for xhttp (no Vision)
	}{
		{tag: "vless-reality-v2-in", port: 4444, wantFlow: "xtls-rprx-vision"},
		{tag: "vless-xhttp-v2-in", port: 2054, wantFlow: ""},
		// fallback converted to VLESS+XHTTP+Reality (2026-06-06): XHTTP has no Vision flow.
		{tag: "vless-fallback-in", port: 5443, wantFlow: ""},
	}

	const (
		wantClientUUID  = "11111111-2222-3333-4444-555555555555"
		wantClientEmail = "test@raven.local"
	)

	for _, c := range cases {
		t.Run(c.tag, func(t *testing.T) {
			ib, ok := byTag[c.tag]
			if !ok {
				t.Fatalf("inbound %q missing from parsed output", c.tag)
			}
			if ib.Protocol != "vless" {
				t.Errorf("protocol: got %q, want vless", ib.Protocol)
			}
			if ib.Port != c.port {
				t.Errorf("port: got %d, want %d", ib.Port, c.port)
			}
			if strings.TrimSpace(ib.RawJSON) == "" {
				t.Errorf("RawJSON empty — generator path will fail")
			}
			if len(ib.Clients) != 1 {
				t.Fatalf("clients: got %d, want exactly 1 deterministic test client", len(ib.Clients))
			}

			cl := ib.Clients[0]
			if cl.Identity != wantClientEmail {
				t.Errorf("client identity: got %q, want %q", cl.Identity, wantClientEmail)
			}

			var stored StoredClientConfig
			if err := json.Unmarshal([]byte(cl.ConfigJSON), &stored); err != nil {
				t.Fatalf("StoredClientConfig unmarshal: %v\nraw: %s", err, cl.ConfigJSON)
			}
			if stored.Protocol != "vless" {
				t.Errorf("stored.Protocol: got %q, want vless", stored.Protocol)
			}
			if stored.ID != wantClientUUID {
				t.Errorf("stored.ID: got %q, want %q", stored.ID, wantClientUUID)
			}
			if stored.Email != wantClientEmail {
				t.Errorf("stored.Email: got %q, want %q", stored.Email, wantClientEmail)
			}
			if stored.Flow != c.wantFlow {
				t.Errorf("stored.Flow: got %q, want %q", stored.Flow, c.wantFlow)
			}
			// Fixtures use decryption=none, so encryption string must round-trip as "none"
			// regardless of what client_encryption map is supplied.
			if stored.Encryption != "none" {
				t.Errorf("stored.Encryption: got %q, want none (fixture uses decryption=none)", stored.Encryption)
			}
		})
	}
}

// TestAnsibleContract_StreamSettingsRoundTrip locks the streamSettings shape
// the URI generator depends on. Adding/renaming a streamSettings field in
// Ansible without updating the StreamSettings struct will leave it set to the
// Go zero value here and fail the assertion.
func TestAnsibleContract_StreamSettingsRoundTrip(t *testing.T) {
	const fixtureDir = "../../testdata/ansible-rendered/conf.d"

	parsed, err := ParseConfigDir(fixtureDir)
	if err != nil {
		t.Fatalf("ParseConfigDir: %v", err)
	}

	type streamShape struct {
		network         string
		security        string
		realityDest     string
		realitySNICount int
		xhttpPath       string // only for xhttp inbound; "" otherwise
	}
	want := map[string]streamShape{
		"vless-reality-v2-in": {network: "tcp", security: "reality", realityDest: "dl.google.com:443", realitySNICount: 1},
		"vless-xhttp-v2-in":   {network: "xhttp", security: "reality", realityDest: "addons.mozilla.org:443", realitySNICount: 1, xhttpPath: "/api/v4/sync"},
		// fallback converted to VLESS+XHTTP+Reality (2026-06-06): single cover-SNI www.gstatic.com.
		"vless-fallback-in": {network: "xhttp", security: "reality", realityDest: "www.gstatic.com:443", realitySNICount: 1, xhttpPath: "/v2/assets/sync"},
	}

	for _, inbounds := range parsed {
		for _, ib := range inbounds {
			w, ok := want[ib.Tag]
			if !ok {
				continue
			}
			t.Run(ib.Tag, func(t *testing.T) {
				// Re-parse the raw inbound JSON via the same struct the URI
				// generator uses (StreamSettings); the parser only stores
				// RawJSON, leaving extraction to downstream consumers.
				var si ServerInbound
				if err := json.Unmarshal([]byte(ib.RawJSON), &si); err != nil {
					t.Fatalf("ServerInbound unmarshal: %v", err)
				}
				if got := si.StreamSettings.Network; got != w.network {
					t.Errorf("network: got %q, want %q", got, w.network)
				}
				if got := si.StreamSettings.Security; got != w.security {
					t.Errorf("security: got %q, want %q", got, w.security)
				}
				if si.StreamSettings.RealitySettings == nil {
					t.Fatal("realitySettings missing — schema drift")
				}
				if got := si.StreamSettings.RealitySettings.Dest; got != w.realityDest {
					t.Errorf("realitySettings.dest: got %q, want %q", got, w.realityDest)
				}
				if got := len(si.StreamSettings.RealitySettings.ServerNames); got != w.realitySNICount {
					t.Errorf("realitySettings.serverNames count: got %d, want %d", got, w.realitySNICount)
				}
				if w.xhttpPath != "" {
					if len(si.StreamSettings.XHTTPSettings) == 0 {
						t.Fatal("xhttpSettings missing on xhttp inbound — schema drift")
					}
					var xs struct {
						Mode string `json:"mode"`
						Path string `json:"path"`
					}
					if err := json.Unmarshal(si.StreamSettings.XHTTPSettings, &xs); err != nil {
						t.Fatalf("xhttpSettings unmarshal: %v", err)
					}
					if xs.Path != w.xhttpPath {
						t.Errorf("xhttpSettings.path: got %q, want %q", xs.Path, w.xhttpPath)
					}
				}
			})
		}
	}
}
