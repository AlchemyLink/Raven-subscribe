package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/models"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// multiNodeServer builds an admin-authed Server with the given node topology
// reconciled into the DB.
func multiNodeServer(t *testing.T, nodes []config.NodeConfig) (*Server, *database.DB) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, n := range nodes {
		if _, err := db.UpsertNode(models.Node{
			Name: n.Name, APIAddr: n.APIAddr, InboundTag: n.InboundTag,
			PublicHost: n.PublicHost, PublicPort: n.PublicPort, Enabled: n.IsEnabled(),
		}); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	cfg := &config.Config{ServerHost: "eu.example.com", BaseURL: "http://eu.example.com", AdminToken: "admin-secret", Nodes: nodes}
	return NewServer(cfg, db, &noopSyncer{}), db
}

func adminReq(method, path, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Admin-Token", "admin-secret")
	return r
}

func twoNodes() []config.NodeConfig {
	return []config.NodeConfig{
		{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443},
		{Name: "eu-2", APIAddr: "10.7.0.2:10085", InboundTag: "vless-in", PublicHost: "eu2.example.com", PublicPort: 443},
	}
}

func TestListNodes(t *testing.T) {
	srv, _ := multiNodeServer(t, twoNodes())
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodGet, "/api/nodes", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/nodes: status %d", rec.Code)
	}
	var nodes []models.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestSetAndGetUserNodes(t *testing.T) {
	srv, db := multiNodeServer(t, twoNodes())
	u, _ := db.CreateUser("alice", "", "tok", "")

	// Place on eu-1 only.
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users/"+itoa(u.ID)+"/nodes", `{"nodes":["eu-1"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST placement: status %d body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodGet, "/api/users/"+itoa(u.ID)+"/nodes", ""))
	var nodes []models.Node
	_ = json.Unmarshal(rec.Body.Bytes(), &nodes)
	if len(nodes) != 1 || nodes[0].Name != "eu-1" {
		t.Fatalf("placement: got %+v, want [eu-1]", nodes)
	}
}

func TestSetUserNodes_UnknownNodeIs400(t *testing.T) {
	srv, db := multiNodeServer(t, twoNodes())
	u, _ := db.CreateUser("bob", "", "tok2", "")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users/"+itoa(u.ID)+"/nodes", `{"nodes":["eu-9"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown node should be 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestPlacementRejectedSingleNode(t *testing.T) {
	// No nodes configured => placement mutation is 400.
	db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer func() { _ = db.Close() }()
	cfg := &config.Config{ServerHost: "eu.example.com", BaseURL: "http://eu.example.com", AdminToken: "admin-secret"}
	srv := NewServer(cfg, db, &noopSyncer{})
	u, _ := db.CreateUser("carol", "", "tok3", "")

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users/"+itoa(u.ID)+"/nodes", `{"nodes":["local"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("placement in single-node should be 400, got %d", rec.Code)
	}
}

func TestDeleteUserNode(t *testing.T) {
	srv, db := multiNodeServer(t, twoNodes())
	u, _ := db.CreateUser("dave", "", "tok4", "")
	id1, _ := db.GetNodeByName("eu-1")
	id2, _ := db.GetNodeByName("eu-2")
	if err := db.SetUserNodes(u.ID, []int64{id1.ID, id2.ID}); err != nil {
		t.Fatalf("SetUserNodes: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodDelete, "/api/users/"+itoa(u.ID)+"/nodes/eu-1", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE placement: status %d", rec.Code)
	}
	nodes, _ := db.ListNodesForUser(u.ID)
	if len(nodes) != 1 || nodes[0].Name != "eu-2" {
		t.Errorf("after delete: got %+v, want [eu-2]", nodes)
	}
}

func TestCreateUser_DefaultPlacesOnAllEnabledNodes(t *testing.T) {
	srv, db := multiNodeServer(t, twoNodes())
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users", `{"username":"erin@z.com"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: status %d body %s", rec.Code, rec.Body.String())
	}
	u, err := db.GetUserByUsername("erin@z.com")
	if err != nil || u == nil {
		t.Fatalf("user not created: %v", err)
	}
	nodes, _ := db.ListNodesForUser(u.ID)
	if len(nodes) != 2 {
		t.Errorf("default placement should be all enabled nodes, got %d", len(nodes))
	}
}

func TestCreateUser_ExplicitNodeSubset(t *testing.T) {
	srv, db := multiNodeServer(t, twoNodes())
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users", `{"username":"frank@z.com","nodes":["eu-2"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: status %d body %s", rec.Code, rec.Body.String())
	}
	u, _ := db.GetUserByUsername("frank@z.com")
	nodes, _ := db.ListNodesForUser(u.ID)
	if len(nodes) != 1 || nodes[0].Name != "eu-2" {
		t.Errorf("explicit subset: got %+v, want [eu-2]", nodes)
	}
}

func TestCreateUser_UnknownNodeIs400(t *testing.T) {
	srv, _ := multiNodeServer(t, twoNodes())
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodPost, "/api/users", `{"username":"grace@z.com","nodes":["nope"]}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown node on create should be 400, got %d", rec.Code)
	}
}

func TestClientsToAdd(t *testing.T) {
	want := []models.WantedClient{
		{Email: "a@z", ClientConfig: "{}"},
		{Email: "b@z", ClientConfig: "{}"},
		{Email: "c@z", ClientConfig: "{}"},
	}
	// b already present on the node.
	add := clientsToAdd(want, []string{"b@z"})
	if len(add) != 2 {
		t.Fatalf("expected 2 to add, got %d", len(add))
	}
	got := map[string]bool{add[0].Email: true, add[1].Email: true}
	if !got["a@z"] || !got["c@z"] || got["b@z"] {
		t.Errorf("wrong add set: %+v", add)
	}
	// All present → nothing to add.
	if n := clientsToAdd(want, []string{"a@z", "b@z", "c@z"}); len(n) != 0 {
		t.Errorf("expected empty add, got %+v", n)
	}
}

func TestSyncStatus_SingleNodeOmitsNodes(t *testing.T) {
	srv, _ := testServer(t) // no cfg.Nodes
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodGet, "/api/sync/status", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := m["nodes"]; ok {
		t.Error("single-node /api/sync/status must omit the nodes field")
	}
	// Flat fields still present (unchanged shape).
	if _, ok := m["probe_ok"]; !ok {
		t.Error("expected flat SyncStatus fields (probe_ok) to remain")
	}
}

func TestSyncStatus_MultiNodeIncludesNodes(t *testing.T) {
	srv, _ := multiNodeServer(t, twoNodes())
	// Simulate a completed reconcile pass.
	srv.storeNodeStatus("eu-1", nodeSyncStatus{Reachable: true, LastApplyOK: true, UsersTarget: 3, UsersPresent: 3})
	srv.storeNodeStatus("eu-2", nodeSyncStatus{Reachable: false, LastError: "connection refused", UsersTarget: 3})

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, adminReq(http.MethodGet, "/api/sync/status", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var resp struct {
		ProbeOK bool                      `json:"probe_ok"`
		Nodes   map[string]nodeSyncStatus `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("expected 2 node statuses, got %d", len(resp.Nodes))
	}
	if !resp.Nodes["eu-1"].Reachable || resp.Nodes["eu-2"].Reachable {
		t.Errorf("node reachability wrong: %+v", resp.Nodes)
	}
	if resp.Nodes["eu-2"].LastError == "" {
		t.Error("expected eu-2 to carry the unreachable error")
	}
}

func TestReconcileNodesLoop_SingleNodeReturnsImmediately(t *testing.T) {
	srv, _ := testServer(t) // no nodes
	done := make(chan struct{})
	go func() { srv.ReconcileNodesLoop(context.Background(), time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReconcileNodesLoop should return immediately in single-node mode")
	}
}
