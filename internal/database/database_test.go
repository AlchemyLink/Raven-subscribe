package database

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDB_CreateUser_GetUserByID_GetUserByToken(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.CreateUser("alice", "token-alice-123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID <= 0 {
		t.Errorf("expected positive ID, got %d", u.ID)
	}
	if u.Username != "alice" {
		t.Errorf("Username: got %q", u.Username)
	}
	if u.Token != "token-alice-123" {
		t.Errorf("Token: got %q", u.Token)
	}
	if !u.Enabled {
		t.Error("expected Enabled=true")
	}

	byID, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if byID == nil || byID.Username != "alice" {
		t.Errorf("GetUserByID: got %+v", byID)
	}

	byToken, err := db.GetUserByToken("token-alice-123")
	if err != nil {
		t.Fatalf("GetUserByToken: %v", err)
	}
	if byToken == nil || byToken.ID != u.ID {
		t.Errorf("GetUserByToken: got %+v", byToken)
	}
}

func TestDB_GetUserByID_NotFound(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.GetUserByID(99999)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u != nil {
		t.Errorf("expected nil for non-existent user, got %+v", u)
	}
}

func TestDB_GetUserByToken_NotFound(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.GetUserByToken("nonexistent")
	if err != nil {
		t.Fatalf("GetUserByToken: %v", err)
	}
	if u != nil {
		t.Errorf("expected nil for non-existent token, got %+v", u)
	}
}

func TestDB_DeleteUser(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.CreateUser("bob", "token-bob")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestDB_ListUsers_CountUsers(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	n, err := db.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("initial count: got %d, want 0", n)
	}

	_, _ = db.CreateUser("u1", "t1")
	_, _ = db.CreateUser("u2", "t2")

	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("ListUsers: got %d, want 2", len(users))
	}

	n, err = db.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 2 {
		t.Errorf("CountUsers: got %d, want 2", n)
	}
}

func TestDB_UpsertInbound_ListInbounds(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	id, err := db.UpsertInbound("vless-in", "vless", 443, "01.json", `{"tag":"vless-in"}`)
	if err != nil {
		t.Fatalf("UpsertInbound: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive inbound ID, got %d", id)
	}

	inbounds, err := db.ListInbounds()
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("ListInbounds: got %d, want 1", len(inbounds))
	}
	if inbounds[0].Tag != "vless-in" {
		t.Errorf("Tag: got %q", inbounds[0].Tag)
	}

	// Upsert same tag again
	id2, err := db.UpsertInbound("vless-in", "vless", 8443, "01.json", `{"tag":"vless-in"}`)
	if err != nil {
		t.Fatalf("UpsertInbound: %v", err)
	}
	if id2 != id {
		t.Errorf("UpsertInbound id mismatch: got %d, want %d", id2, id)
	}
}

func TestDB_UpsertUserClient_GetUserClients(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, _ := db.CreateUser("alice", "t-alice")
	ibID, _ := db.UpsertInbound("vless-1", "vless", 443, "01.json", `{}`)

	if err := db.UpsertUserClient(u.ID, ibID, `{"protocol":"vless","id":"uuid1","flow":"xtls-rprx-vision"}`); err != nil {
		t.Fatalf("UpsertUserClient: %v", err)
	}

	clients, err := db.GetUserClients(u.ID)
	if err != nil {
		t.Fatalf("GetUserClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("GetUserClients: got %d, want 1", len(clients))
	}
	if clients[0].InboundTag != "vless-1" {
		t.Errorf("InboundTag: got %q", clients[0].InboundTag)
	}
}

func TestDB_GetUserClientByUserAndInbound(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, _ := db.CreateUser("alice", "t-alice")
	ibID, _ := db.UpsertInbound("vless-1", "vless", 443, "01.json", `{}`)
	_ = db.UpsertUserClient(u.ID, ibID, `{"protocol":"vless","id":"uuid1"}`)

	tag, cfg, err := db.GetUserClientByUserAndInbound(u.ID, ibID)
	if err != nil {
		t.Fatalf("GetUserClientByUserAndInbound: %v", err)
	}
	if tag != "vless-1" {
		t.Errorf("tag: got %q, want vless-1", tag)
	}
	if !strings.Contains(cfg, "uuid1") {
		t.Errorf("clientConfig: expected uuid1, got %q", cfg)
	}

	// Not found
	_, _, err = db.GetUserClientByUserAndInbound(999, 999)
	if err != nil {
		t.Fatalf("GetUserClientByUserAndInbound not found: %v", err)
	}
	tag2, cfg2, _ := db.GetUserClientByUserAndInbound(999, 999)
	if tag2 != "" || cfg2 != "" {
		t.Errorf("expected empty for not found, got tag=%q cfg=%q", tag2, cfg2)
	}
}

func testDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return db, func() { _ = db.Close() }
}
