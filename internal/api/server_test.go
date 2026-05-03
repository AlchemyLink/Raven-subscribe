package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/syncer"
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

func TestAPI_GetUserByToken_Lookup(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	// Create user to obtain a real token
	createBody := `{"username":"portaluser"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader([]byte(createBody)))
	req.Header.Set("X-Admin-Token", "admin-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Token    string `json:"token"`
		} `json:"user"`
		SubURL string `json:"sub_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.User.Token == "" {
		t.Fatal("token empty")
	}

	// Lookup by token returns the same user record + sub_url
	req = httptest.NewRequest(http.MethodGet, "/api/users/by-token/"+created.User.Token, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("by-token lookup: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Token    string `json:"token"`
		} `json:"user"`
		SubURL string `json:"sub_url"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode lookup: %v", err)
	}
	if resp.User.ID != created.User.ID {
		t.Errorf("id: got %d, want %d", resp.User.ID, created.User.ID)
	}
	if resp.User.Username != "portaluser" {
		t.Errorf("username: got %q, want portaluser", resp.User.Username)
	}
	if resp.SubURL == "" {
		t.Error("sub_url should be populated")
	}

	// Unknown token → 404
	req = httptest.NewRequest(http.MethodGet, "/api/users/by-token/nonexistent-token-xyz", nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: %d body=%s", rec.Code, rec.Body.String())
	}

	// No admin token → 401
	req = httptest.NewRequest(http.MethodGet, "/api/users/by-token/"+created.User.Token, nil)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no admin token: %d", rec.Code)
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

// TestAPI_AddUserClient_ReEnablesDisabledRow covers the regression where a
// disabled user_clients row caused POST /api/users/{id}/clients to return 500
// "failed to add user to inbound". With the fix, a disabled row for the same
// tag is re-enabled in DB (and pushed to Xray) instead of triggering a fresh
// addUserToInbound that would crash on Xray's "User already exists".
func TestAPI_AddUserClient_ReEnablesDisabledRow(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	// Create user
	createBody := `{"username":"reenable_user"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader([]byte(createBody)))
	req.Header.Set("X-Admin-Token", "admin-secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// Pre-seed: inbound + disabled user_clients row, simulating the production drift.
	const tag = "vless-reality-v2-in"
	inboundID, err := srv.db.UpsertInbound(tag, "vless", 4444, "201-test.json", `{}`)
	if err != nil {
		t.Fatalf("upsert inbound: %v", err)
	}
	if err := srv.db.UpsertUserClient(created.User.ID, inboundID, `{"id":"test-uuid"}`); err != nil {
		t.Fatalf("upsert user_client: %v", err)
	}
	if err := srv.db.SetUserClientEnabled(created.User.ID, inboundID, false); err != nil {
		t.Fatalf("disable client: %v", err)
	}

	// Confirm the precondition — GetUserClients (enabled-only) returns nothing,
	// GetAllUserClients returns the disabled row.
	if cs, _ := srv.db.GetUserClients(created.User.ID); len(cs) != 0 {
		t.Fatalf("precondition: GetUserClients should be empty, got %+v", cs)
	}
	if cs, _ := srv.db.GetAllUserClients(created.User.ID); len(cs) != 1 || cs[0].Enabled {
		t.Fatalf("precondition: GetAllUserClients should return 1 disabled row, got %+v", cs)
	}

	// POST /api/users/{id}/clients should re-enable, not 500.
	addBody := `{"tag":"` + tag + `"}`
	req = httptest.NewRequest(http.MethodPost,
		"/api/users/"+strconv.FormatInt(created.User.ID, 10)+"/clients",
		bytes.NewReader([]byte(addBody)))
	req.Header.Set("X-Admin-Token", "admin-secret")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable add client: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		InboundTag string `json:"inbound_tag"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode add: %v", err)
	}
	if resp.InboundTag != tag {
		t.Errorf("inbound_tag: got %q, want %q", resp.InboundTag, tag)
	}
	if !resp.Enabled {
		t.Errorf("response enabled: got false, want true")
	}

	// DB row should now be enabled.
	cs, _ := srv.db.GetUserClients(created.User.ID)
	if len(cs) != 1 || !cs[0].Enabled || cs[0].InboundTag != tag {
		t.Errorf("post-condition: GetUserClients should return 1 enabled row for %q, got %+v", tag, cs)
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

func (n *noopSyncer) Sync() error              { return nil }
func (n *noopSyncer) Status() syncer.SyncStatus { return syncer.SyncStatus{ProbeOK: true} }

// ── helpers ──────────────────────────────────────────────────────────────────

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
