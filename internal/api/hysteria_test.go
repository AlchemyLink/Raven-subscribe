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

func TestHysteriaSub(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	hyTestUser(t, srv)
	srv.cfg.Hysteria = &config.HysteriaConfig{
		Enabled: true, Host: "zirgate.com", Port: 47014,
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
		for _, want := range []string{"hysteria2://t-alice@zirgate.com:47014", "obfs=salamander", "obfs-password=obfspw", "sni=www.python.org", "pinSHA256=593c5be4", "insecure=1"} {
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
