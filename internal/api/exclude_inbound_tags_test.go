package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExcludeInboundTags_DropsTagFromSubscription verifies that an inbound tag listed
// in cfg.ExcludeInboundTags is absent from the generated subscription, while other
// inbounds are still served. Guards the "retire a dead transport from what clients
// download while keeping the inbound on the server" path.
func TestExcludeInboundTags_DropsTagFromSubscription(t *testing.T) {
	const xhttpRaw = `{"tag":"vless-xhttp-v2-in","protocol":"vless","port":443,` +
		`"streamSettings":{"network":"xhttp","security":"reality",` +
		`"realitySettings":{"serverNames":["www.python.org"],"publicKey":"testpublickey12345678901234567890123456789012","shortIds":["ab"]},` +
		`"xhttpSettings":{"mode":"packet-up","host":"www.python.org","path":"/x"}}}`
	const realityRaw = `{"tag":"vless-reality-v2-in","protocol":"vless","port":4443,` +
		`"streamSettings":{"network":"tcp","security":"reality",` +
		`"realitySettings":{"serverNames":["www.python.org"],"publicKey":"testpublickey12345678901234567890123456789012","shortIds":["ab"]}}}`
	const clientCfg = `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`

	setup := func(t *testing.T) *Server {
		t.Helper()
		srv, cleanup := testServer(t)
		t.Cleanup(cleanup)
		u, err := srv.db.CreateUser("alice", "alice@example.com", "t-alice", "fb-alice")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		xID, err := srv.db.UpsertInbound("vless-xhttp-v2-in", "vless", 443, "210.json", xhttpRaw)
		if err != nil {
			t.Fatalf("UpsertInbound xhttp: %v", err)
		}
		rID, err := srv.db.UpsertInbound("vless-reality-v2-in", "vless", 4443, "200.json", realityRaw)
		if err != nil {
			t.Fatalf("UpsertInbound reality: %v", err)
		}
		if err := srv.db.UpsertUserClient(u.ID, xID, clientCfg); err != nil {
			t.Fatalf("UpsertUserClient xhttp: %v", err)
		}
		if err := srv.db.UpsertUserClient(u.ID, rID, clientCfg); err != nil {
			t.Fatalf("UpsertUserClient reality: %v", err)
		}
		return srv
	}

	fetchSub := func(t *testing.T, srv *Server) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/sub/t-alice", nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/sub: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	t.Run("control: both inbounds present without exclusion", func(t *testing.T) {
		srv := setup(t)
		body := fetchSub(t, srv)
		if !strings.Contains(body, "vless-xhttp-v2-in") {
			t.Error("xhttp inbound missing from subscription")
		}
		if !strings.Contains(body, "vless-reality-v2-in") {
			t.Error("reality inbound missing from control subscription")
		}
	})

	t.Run("excluded reality tag dropped, xhttp retained", func(t *testing.T) {
		srv := setup(t)
		srv.cfg.ExcludeInboundTags = []string{"vless-reality-v2-in"}
		body := fetchSub(t, srv)
		if strings.Contains(body, "vless-reality-v2-in") {
			t.Error("excluded tag vless-reality-v2-in still present in subscription")
		}
		if !strings.Contains(body, "vless-xhttp-v2-in") {
			t.Error("non-excluded xhttp inbound was wrongly dropped")
		}
	})
}
