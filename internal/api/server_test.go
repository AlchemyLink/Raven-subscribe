package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/models"
	"github.com/alchemylink/raven-subscribe/internal/xray"
)

func TestAPI_Health(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("health: got status %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status: got %q, want ok", body["status"])
	}
}

func TestAPI_ListUsers_Unauthorized(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("listUsers without token: got %d, want 401", rec.Code)
	}
}

func TestAPI_ListUsers_CreateUser_GetUser_DeleteUser_ByID(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	// Create user
	createBody := `{"username":"testuser"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader([]byte(createBody)))
	req.Header.Set("X-Admin-Token", "admin-secret")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var createResp struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Token    string `json:"token"`
		} `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.User.ID <= 0 {
		t.Fatalf("expected positive user ID, got %d", createResp.User.ID)
	}
	if createResp.User.Token == "" {
		t.Fatal("expected non-empty token")
	}
	idPath := strconv.FormatInt(createResp.User.ID, 10)

	// GET by numeric ID
	req = httptest.NewRequest(http.MethodGet, "/api/users/"+idPath, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("get user by id: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Lookup by username should return the user
	req = httptest.NewRequest(http.MethodGet, "/api/users/"+createResp.User.Username, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("get user by username: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// DELETE by numeric id
	req = httptest.NewRequest(http.MethodDelete, "/api/users/"+idPath, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("delete user by id: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Verify deleted
	req = httptest.NewRequest(http.MethodGet, "/api/users/"+idPath, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get deleted user: got %d, want 404", rec.Code)
	}
}

func TestAPI_InvalidID_ReturnsBadRequest(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	// Non-numeric id is treated as username lookup; unknown username → 404.
	req := httptest.NewRequest(http.MethodGet, "/api/users/not-a-number", nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown username: got %d, body=%s", rec.Code, rec.Body.String())
	}
}

func testServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	cfg := &config.Config{
		ServerHost:        "test.example.com",
		BaseURL:           "http://test.example.com",
		AdminToken:         "admin-secret",
		APIUserInboundTag: "", // avoid sync during test
	}
	srv := NewServer(cfg, db, &noopSyncer{})
	return srv, func() { _ = db.Close() }
}

type noopSyncer struct{}

func (n *noopSyncer) Sync() error { return nil }

// ── helpers ──────────────────────────────────────────────────────────────────

// createTestUserWithHysteria2 creates a user and adds a hysteria2 client to DB.
func createTestUserWithHysteria2(t *testing.T, db *database.DB, username string) (token string) {
	t.Helper()
	user, err := db.CreateUser(username, username, "tok-"+username, "fb-"+username)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ibID, err := db.UpsertInbound("hy2-in", "hysteria2", 8443, "singbox.json",
		`{"type":"hysteria2","tag":"hy2-in","listen_port":8443}`)
	if err != nil {
		t.Fatalf("UpsertInbound: %v", err)
	}
	cred := `{"protocol":"hysteria2","password":"secret","server_name":"vpn.example.com","obfs_type":"salamander","obfs_password":"obfssecret"}` // #nosec G101 -- test fixture, not a real credential.
	if err := db.UpsertUserClient(user.ID, ibID, cred); err != nil {
		t.Fatalf("UpsertUserClient: %v", err)
	}
	return user.Token
}

// ── buildHysteria2Link unit tests ────────────────────────────────────────────

func TestBuildHysteria2Link(t *testing.T) {
	settings := xray.Hysteria2OutboundSettings{
		Server:     "vpn.example.com",
		ServerPort: 8443,
		Password:   "secret",
		TLS:        &xray.Hysteria2TLS{Enabled: true, ServerName: "vpn.example.com"},
		Obfs:       &xray.Hysteria2Obfs{Type: "salamander", Password: "obfssecret"},
	}
	settingsJSON, _ := json.Marshal(settings) // #nosec G117 -- marshaling test fixture struct, not a real secret.
	ob := xray.Outbound{
		Tag:      "hy2-tag",
		Protocol: "hysteria2",
		Settings: settingsJSON,
	}

	link := buildHysteria2Link(ob)
	if !strings.HasPrefix(link, "hysteria2://") {
		t.Fatalf("link should start with hysteria2://, got %q", link)
	}
	if !strings.Contains(link, "obfs=salamander") {
		t.Errorf("link should contain obfs=salamander, got %q", link)
	}
	if !strings.Contains(link, "sni=vpn.example.com") {
		t.Errorf("link should contain sni, got %q", link)
	}
	if !strings.Contains(link, "insecure=0") {
		t.Errorf("link should contain insecure=0, got %q", link)
	}
	if !strings.HasSuffix(link, "#hy2-tag") {
		t.Errorf("link should end with #hy2-tag, got %q", link)
	}
}

func TestBuildHysteria2Link_NoObfs(t *testing.T) {
	settings := xray.Hysteria2OutboundSettings{
		Server:     "vpn.example.com",
		ServerPort: 443,
		Password:   "p",
	}
	// #nosec G117 -- marshaling test fixture struct, not a real secret.
	settingsJSON, _ := json.Marshal(settings)
	ob := xray.Outbound{Tag: "t", Protocol: "hysteria2", Settings: settingsJSON}
	link := buildHysteria2Link(ob)
	if strings.Contains(link, "obfs") {
		t.Errorf("link should not contain obfs without obfs config, got %q", link)
	}
}

func TestBuildHysteria2Link_MissingPassword_ReturnsEmpty(t *testing.T) {
	settings := xray.Hysteria2OutboundSettings{Server: "h", ServerPort: 443}
	// #nosec G117 -- marshaling test fixture struct, not a real secret.
	settingsJSON, _ := json.Marshal(settings)
	ob := xray.Outbound{Tag: "t", Protocol: "hysteria2", Settings: settingsJSON}
	if link := buildHysteria2Link(ob); link != "" {
		t.Errorf("expected empty link for missing password, got %q", link)
	}
}

func TestExcludeProtocol(t *testing.T) {
	clients := []models.UserClientFull{
		{InboundProtocol: "vless"},
		{InboundProtocol: "hysteria2"},
		{InboundProtocol: "HYSTERIA2"},
		{InboundProtocol: "vmess"},
	}
	filtered := excludeProtocol(clients, "hysteria2")
	if len(filtered) != 2 {
		t.Errorf("expected 2 non-hysteria2 clients, got %d", len(filtered))
	}
	for _, c := range filtered {
		if strings.EqualFold(c.InboundProtocol, "hysteria2") {
			t.Errorf("hysteria2 client leaked through excludeProtocol")
		}
	}
}

// ── HTTP endpoint tests ───────────────────────────────────────────────────────

func TestAPI_Hysteria2LinksText_ValidToken(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	token := createTestUserWithHysteria2(t, srv.db, "hyuser")

	req := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/hysteria2", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("hysteria2 links: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "hysteria2://") {
		t.Errorf("expected hysteria2:// link, got %q", body)
	}
	if !strings.Contains(body, "obfs=salamander") {
		t.Errorf("expected salamander obfs in link, got %q", body)
	}
}

func TestAPI_Hysteria2LinksB64_ValidToken(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	token := createTestUserWithHysteria2(t, srv.db, "hyuserb64")

	req := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/hysteria2.b64", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", rec.Code, rec.Body.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("response is not valid base64: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "hysteria2://") {
		t.Errorf("decoded payload should start with hysteria2://, got %q", decoded)
	}
}

func TestAPI_Hysteria2Links_InvalidToken(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/sub/badtoken/hysteria2", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad token, got %d", rec.Code)
	}
}

func TestAPI_SingboxSubscription_ValidToken(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	token := createTestUserWithHysteria2(t, srv.db, "singboxuser")

	req := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/singbox", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("singbox sub: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var cfg map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode singbox response: %v", err)
	}
	outbounds, ok := cfg["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		t.Fatal("expected outbounds in singbox config")
	}
	// First outbound should be hysteria2
	first := outbounds[0].(map[string]interface{})
	if first["type"] != "hysteria2" {
		t.Errorf("first outbound type: got %v, want hysteria2", first["type"])
	}
	// Obfs should be present
	obfs, ok := first["obfs"].(map[string]interface{})
	if !ok {
		t.Fatal("expected obfs in hysteria2 outbound")
	}
	if obfs["type"] != "salamander" {
		t.Errorf("obfs type: got %v, want salamander", obfs["type"])
	}
}

func TestAPI_SingboxSubscription_InvalidToken(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/sub/badtoken/singbox", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for bad token, got %d", rec.Code)
	}
}

func TestBuildSingboxClientConfig_Structure(t *testing.T) {
	outbounds := []xray.Hysteria2OutboundSettings{
		{
			Type:       "hysteria2",
			Tag:        "hy2-0",
			Server:     "vpn.example.com",
			ServerPort: 8443,
			Password:   "secret",
			TLS:        &xray.Hysteria2TLS{Enabled: true, ServerName: "vpn.example.com"},
		},
	}
	cfg := buildSingboxClientConfig(outbounds)

	obs, _ := cfg["outbounds"].([]interface{})
	// 1 proxy + direct + block
	if len(obs) != 3 {
		t.Errorf("expected 3 outbounds (hy2 + direct + block), got %d", len(obs))
	}
	inbounds, _ := cfg["inbounds"].([]map[string]interface{})
	if len(inbounds) != 1 {
		t.Errorf("expected 1 inbound, got %d", len(inbounds))
	}
	if inbounds[0]["type"] != "mixed" {
		t.Errorf("inbound type: got %v, want mixed", inbounds[0]["type"])
	}
	route := cfg["route"].(map[string]interface{})
	if route["final"] != "hy2-0" {
		t.Errorf("route.final: got %v, want hy2-0", route["final"])
	}
}


func TestAdminAuth_EmptyToken_Returns503(t *testing.T) {
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	srv := NewServer(&config.Config{
		ServerHost: "test.example.com",
		BaseURL:    "http://test.example.com",
		AdminToken: "", // empty — should lock the API
	}, db, &noopSyncer{})

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodGet, "/api/emergency/status"},
		{http.MethodPost, "/api/emergency/activate"},
		{http.MethodGet, "/api/inbounds"},
	}

	for _, e := range endpoints {
		req := httptest.NewRequest(e.method, e.path, nil)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with empty token: got %d, want 503", e.method, e.path, rec.Code)
		}
	}
}
