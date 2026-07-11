package api

import (
	"path/filepath"
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/models"
)

func nodeTestServer(t *testing.T, nodes []config.NodeConfig) (*Server, *database.DB) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{ServerHost: "eu.example.com", BaseURL: "http://eu.example.com", Nodes: nodes}
	return NewServer(cfg, db, &noopSyncer{}), db
}

func clientOn(tag string) models.UserClientFull {
	return models.UserClientFull{
		UserClient:      models.UserClient{ClientConfig: `{"protocol":"vless","id":"u"}`},
		InboundTag:      tag,
		InboundProtocol: "vless",
		InboundPort:     443,
	}
}

func TestExpandClientsForNodes_SingleNodeNoOp(t *testing.T) {
	srv, _ := nodeTestServer(t, nil) // no nodes configured
	clients := []models.UserClientFull{clientOn("vless-in")}
	got := srv.expandClientsForNodes(1, clients)
	if len(got) != 1 || got[0].NodeHost != "" {
		t.Fatalf("single-node must be a no-op, got %+v", got)
	}
}

func TestExpandClientsForNodes_ExpandsPerPlacedNode(t *testing.T) {
	nodes := []config.NodeConfig{
		{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443},
		{Name: "eu-2", APIAddr: "10.7.0.2:10085", InboundTag: "vless-in", PublicHost: "eu2.example.com", PublicPort: 8443},
	}
	srv, db := nodeTestServer(t, nodes)

	u, err := db.CreateUser("alice", "", "tok", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	id1, _ := db.UpsertNode(models.Node{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443, Enabled: true})
	id2, _ := db.UpsertNode(models.Node{Name: "eu-2", APIAddr: "10.7.0.2:10085", InboundTag: "vless-in", PublicHost: "eu2.example.com", PublicPort: 8443, Enabled: true})
	if err := db.AssignUserToNodes(u.ID, []int64{id1, id2}); err != nil {
		t.Fatalf("AssignUserToNodes: %v", err)
	}

	got := srv.expandClientsForNodes(u.ID, []models.UserClientFull{clientOn("vless-in")})
	if len(got) != 2 {
		t.Fatalf("expected 2 expanded clients, got %d", len(got))
	}
	hosts := map[string]int{}
	for _, c := range got {
		hosts[c.NodeHost] = c.NodePort
	}
	if hosts["eu1.example.com"] != 443 || hosts["eu2.example.com"] != 8443 {
		t.Errorf("unexpected node endpoints: %v", hosts)
	}
}

func TestExpandClientsForNodes_SkipsClientWithNoPlacedNode(t *testing.T) {
	nodes := []config.NodeConfig{
		{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443},
	}
	srv, db := nodeTestServer(t, nodes)

	u, err := db.CreateUser("bob", "", "tok2", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	id1, _ := db.UpsertNode(models.Node{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443, Enabled: true})
	if err := db.AssignUserToNodes(u.ID, []int64{id1}); err != nil {
		t.Fatalf("AssignUserToNodes: %v", err)
	}

	// Client on an inbound no placed node serves → dropped; client on vless-in → kept.
	clients := []models.UserClientFull{clientOn("vless-xhttp-in"), clientOn("vless-in")}
	got := srv.expandClientsForNodes(u.ID, clients)
	if len(got) != 1 {
		t.Fatalf("expected only the served inbound to survive, got %d", len(got))
	}
	if got[0].InboundTag != "vless-in" || got[0].NodeHost != "eu1.example.com" {
		t.Errorf("wrong surviving client: %+v", got[0])
	}
}

func TestExpandClientsForNodes_NoPlacementFallsBack(t *testing.T) {
	// Nodes configured but the user has no placements yet: fall back to the
	// unexpanded clients rather than dropping everything.
	nodes := []config.NodeConfig{{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443}}
	srv, db := nodeTestServer(t, nodes)
	u, err := db.CreateUser("carol", "", "tok3", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got := srv.expandClientsForNodes(u.ID, []models.UserClientFull{clientOn("vless-in")})
	if len(got) != 1 || got[0].NodeHost != "" {
		t.Fatalf("no placement should fall back to unexpanded clients, got %+v", got)
	}
}
