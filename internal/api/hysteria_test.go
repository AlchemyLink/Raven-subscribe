package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/config"
)

func hyTestUser(t *testing.T, srv *Server) {
	t.Helper()
	if _, err := srv.db.CreateUser("alice", "alice@example.com", "t-alice", "fb-alice"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
}

func postHyAuth(t *testing.T, srv *Server, body, remoteAddr string) (int, hysteriaAuthResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hysteria/auth", bytes.NewReader([]byte(body)))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var resp hysteriaAuthResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func TestHysteriaAuth(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	hyTestUser(t, srv)

	t.Run("valid token from loopback -> ok", func(t *testing.T) {
		code, resp := postHyAuth(t, srv, `{"addr":"1.2.3.4:5","auth":"t-alice","tx":1}`, "127.0.0.1:40000")
		if code != http.StatusOK || !resp.OK || resp.ID != "alice" {
			t.Fatalf("got code=%d ok=%v id=%q, want 200/true/alice", code, resp.OK, resp.ID)
		}
	})
	t.Run("invalid token -> not ok", func(t *testing.T) {
		_, resp := postHyAuth(t, srv, `{"auth":"nope"}`, "127.0.0.1:40001")
		if resp.OK {
			t.Error("invalid token admitted")
		}
	})
	t.Run("non-loopback -> forbidden", func(t *testing.T) {
		code, _ := postHyAuth(t, srv, `{"auth":"t-alice"}`, "8.8.8.8:40002")
		if code != http.StatusForbidden {
			t.Errorf("non-loopback: got %d, want 403", code)
		}
	})
}

func TestHysteriaInMainSub(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	u, err := srv.db.CreateUser("alice", "alice@example.com", "t-alice", "fb-alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	xID, _ := srv.db.UpsertInbound("vless-xhttp-v2-in", "vless", 443, "210.json",
		`{"tag":"vless-xhttp-v2-in","protocol":"vless","port":443,"streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"serverNames":["www.python.org"],"publicKey":"testpublickey12345678901234567890123456789012","shortIds":["ab"]},"xhttpSettings":{"mode":"packet-up","host":"www.python.org","path":"/x"}}}`)
	_ = srv.db.UpsertUserClient(u.ID, xID, `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`)
	hy := &config.HysteriaConfig{Enabled: true, Host: "example.com", Port: 47014, ObfsType: "salamander", ObfsPassword: "pw", SNI: "www.python.org", CertPin: "abc"}

	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/links.txt", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec.Body.String()
	}
	t.Run("in_main_sub=false -> no hy2 in links", func(t *testing.T) {
		hy.InMainSub = false
		srv.cfg.Hysteria = hy
		if strings.Contains(get(), "hysteria2://") {
			t.Error("hy2 leaked into links when in_main_sub=false")
		}
	})
	t.Run("in_main_sub=true -> hy2 appended to links", func(t *testing.T) {
		hy.InMainSub = true
		srv.cfg.Hysteria = hy
		body := get()
		if !strings.Contains(body, "hysteria2://t-alice@example.com:47014") {
			t.Errorf("hy2 URI missing from links: %s", body)
		}
		if !strings.Contains(body, "vless://") {
			t.Errorf("primary vless link missing (hy2 should be ADDED, not replace): %s", body)
		}
	})
}

func TestHysteriaPerUserGate(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	hyTestUser(t, srv)
	// Give alice a primary inbound+client so links.txt has content for hy2 to append to.
	u, err := srv.db.GetUserByUsername("alice")
	if err != nil || u == nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	xID, _ := srv.db.UpsertInbound("vless-xhttp-v2-in", "vless", 443, "210.json",
		`{"tag":"vless-xhttp-v2-in","protocol":"vless","port":443,"streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"serverNames":["www.python.org"],"publicKey":"testpublickey12345678901234567890123456789012","shortIds":["ab"]},"xhttpSettings":{"mode":"packet-up","host":"www.python.org","path":"/x"}}}`)
	_ = srv.db.UpsertUserClient(u.ID, xID, `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`)
	srv.cfg.Hysteria = &config.HysteriaConfig{
		Enabled: true, Host: "example.com", Port: 47014,
		ObfsType: "salamander", ObfsPassword: "obfspw", SNI: "hy2.example.com",
		InMainSub: true,
	}

	subHy2 := func() int {
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/hy2", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec.Code
	}
	setHy2 := func(enabled bool) int {
		body, _ := json.Marshal(map[string]bool{"enabled": enabled})
		req := httptest.NewRequest(http.MethodPut, "/api/users/alice/hy2", bytes.NewReader(body))
		req.Header.Set("X-Admin-Token", "admin-secret")
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec.Code
	}
	links := func() string {
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/links.txt", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec.Body.String()
	}

	t.Run("default enabled -> 200 + hy2 in links", func(t *testing.T) {
		if code := subHy2(); code != http.StatusOK {
			t.Fatalf("default: got %d, want 200", code)
		}
		if !strings.Contains(links(), "hysteria2://") {
			t.Error("default: hy2 missing from links")
		}
	})
	t.Run("disable -> /hy2 403 + omitted from links", func(t *testing.T) {
		if code := setHy2(false); code != http.StatusOK {
			t.Fatalf("PUT disable: got %d, want 200", code)
		}
		if code := subHy2(); code != http.StatusForbidden {
			t.Errorf("disabled /hy2: got %d, want 403", code)
		}
		if strings.Contains(links(), "hysteria2://") {
			t.Error("disabled: hy2 leaked into links")
		}
	})
	t.Run("re-enable -> 200 again", func(t *testing.T) {
		if code := setHy2(true); code != http.StatusOK {
			t.Fatalf("PUT enable: got %d, want 200", code)
		}
		if code := subHy2(); code != http.StatusOK {
			t.Errorf("re-enabled /hy2: got %d, want 200", code)
		}
	})
}

func TestHysteriaSub(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	hyTestUser(t, srv)
	srv.cfg.Hysteria = &config.HysteriaConfig{
		Enabled: true, Host: "example.com", Port: 47014,
		ObfsType: "salamander", ObfsPassword: "obfspw", SNI: "www.python.org",
		CertPin: "593c5be48dbc7697098beba3d3c326bbd722ca2a58b47a9abd69ab71f0345e3b",
	}

	t.Run("hy2 URI for valid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/hy2", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		uri := rec.Body.String()
		for _, want := range []string{"hysteria2://t-alice@example.com:47014", "obfs=salamander", "obfs-password=obfspw", "sni=www.python.org", "pinSHA256=593c5be4", "insecure=1"} {
			if !strings.Contains(uri, want) {
				t.Errorf("URI missing %q: %s", want, uri)
			}
		}
	})
	t.Run("b64 variant decodes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/hy2.b64", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		dec, err := base64.StdEncoding.DecodeString(rec.Body.String())
		if err != nil || !strings.HasPrefix(string(dec), "hysteria2://") {
			t.Errorf("b64 decode failed: %v / %s", err, rec.Body.String())
		}
	})
	t.Run("disabled when not configured", func(t *testing.T) {
		srv.cfg.Hysteria = nil
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice/hy2", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("not-configured: got %d, want 404", rec.Code)
		}
	})
}
