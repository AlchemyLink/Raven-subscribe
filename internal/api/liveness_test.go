package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientFamily(t *testing.T) {
	cases := map[string]string{
		"":                                "none",
		"Happ/3.1.2":                      "happ",
		"v2rayNG/1.10.5":                  "v2rayng",
		"v2rayN/7.0":                      "v2rayn",
		"HiddifyNext/2.5.7 (android)":     "hiddify",
		"V2Box 9.1":                       "v2box",
		"Streisand/1.6":                   "streisand",
		"sing-box 1.14.4":                 "singbox",
		"ClashMetaForAndroid/2.11":        "clash",
		"Mozilla/5.0 (X11; Linux x86_64)": "browser",
		"SomethingElse/1.0":               "other",
	}
	for ua, want := range cases {
		if got := clientFamily(ua); got != want {
			t.Errorf("clientFamily(%q) = %q, want %q", ua, got, want)
		}
	}
}

// obscureEmail must match xray-stats-exporter byte for byte, or per-user
// joins in PromQL silently return nothing.
func TestObscureEmail_MatchesExporter(t *testing.T) {
	// sha256("s3cret:alice@example.com")[:8] hex — the exporter's algorithm.
	if got := obscureEmail("s3cret", "alice@example.com"); got != "c8f88cb3b7368254" {
		t.Fatalf("obscureEmail = %q, want c8f88cb3b7368254", got)
	}
	if obscureEmail("", "alice@example.com") != "alice@example.com" {
		t.Error("empty secret must pass the email through")
	}
}

func TestLiveness_RecordsFetchAndSetsInterval(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	srv.cfg.ProfileUpdateIntervalHours = 2
	srv.cfg.MetricsAnonymizeSecret = "s3cret"

	u, err := srv.db.CreateUser("alice@example.com", "alice@example.com", "tok-alice", "fb-alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	xID, _ := srv.db.UpsertInbound("vless-xhttp-v2-in", "vless", 443, "210.json",
		`{"tag":"vless-xhttp-v2-in","protocol":"vless","port":443,"streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"serverNames":["www.python.org"],"publicKey":"testpublickey12345678901234567890123456789012","shortIds":["ab"]},"xhttpSettings":{"mode":"packet-up","host":"www.python.org","path":"/x"}}}`)
	_ = srv.db.UpsertUserClient(u.ID, xID, `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision","encryption":"none"}`)
	router := srv.Router()

	req := httptest.NewRequest(http.MethodGet, "/sub/tok-alice/links.txt", nil)
	req.Header.Set("User-Agent", "Happ/3.1.2")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Profile-Update-Interval"); got != "2" {
		t.Errorf("Profile-Update-Interval = %q, want 2", got)
	}

	// Unknown token: 404, must not be recorded.
	bad := httptest.NewRecorder()
	router.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/sub/nope/links.txt", nil))
	if bad.Code != http.StatusNotFound {
		t.Fatalf("bad token status = %d", bad.Code)
	}

	mrec := httptest.NewRecorder()
	srv.MetricsHandler()(mrec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := mrec.Body.String()
	want := `raven_sub_last_fetch_timestamp_seconds{email="` + obscureEmail("s3cret", "alice@example.com") + `",path="primary",client="happ"}`
	if !strings.Contains(body, want) {
		t.Errorf("metrics missing %s\n%s", want, body)
	}
	if strings.Contains(body, "alice@example.com") || strings.Contains(body, "tok-alice") {
		t.Error("raw email or token leaked into metrics")
	}
	if strings.Count(body, "raven_sub_last_fetch_timestamp_seconds{") != 1 {
		t.Errorf("expected exactly one fetch series:\n%s", body)
	}
	if !strings.Contains(body, "raven_sub_profile_update_interval_hours 2") {
		t.Error("interval gauge missing")
	}
}

func TestLiveness_NonSubscriptionRoutesUntouched(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	srv.cfg.ProfileUpdateIntervalHours = 2
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Header().Get("Profile-Update-Interval") != "" {
		t.Error("/health must not carry Profile-Update-Interval")
	}
}
