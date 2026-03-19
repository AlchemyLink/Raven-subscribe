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

	// Token in path is invalid (not numeric id)
	req = httptest.NewRequest(http.MethodGet, "/api/users/"+createResp.User.Token, nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("get user by token path: got %d, want 400, body=%s", rec.Code, rec.Body.String())
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

	req := httptest.NewRequest(http.MethodGet, "/api/users/not-a-number", nil)
	req.Header.Set("X-Admin-Token", "admin-secret")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id: got %d, body=%s", rec.Code, rec.Body.String())
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

