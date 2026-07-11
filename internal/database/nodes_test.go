package database

import (
	"testing"

	"github.com/alchemylink/raven-subscribe/internal/models"
)

func TestUpsertNode_InsertThenUpdate(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	id, err := db.UpsertNode(models.Node{
		Name: "local", APIAddr: "127.0.0.1:10085", InboundTag: "vless-reality-in",
		PublicHost: "eu.example.com", PublicPort: 443, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertNode insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// Update in place by name; id must be stable.
	id2, err := db.UpsertNode(models.Node{
		Name: "local", APIAddr: "10.7.0.1:10085", InboundTag: "vless-reality-in",
		PublicHost: "eu-new.example.com", PublicPort: 8443, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpsertNode update: %v", err)
	}
	if id2 != id {
		t.Errorf("upsert changed id: got %d, want %d", id2, id)
	}

	got, err := db.GetNodeByName("local")
	if err != nil {
		t.Fatalf("GetNodeByName: %v", err)
	}
	if got == nil {
		t.Fatal("node not found after upsert")
	}
	if got.APIAddr != "10.7.0.1:10085" || got.PublicHost != "eu-new.example.com" || got.PublicPort != 8443 {
		t.Errorf("update did not apply: %+v", got)
	}
	if got.Enabled {
		t.Error("node should be disabled after update")
	}
}

func TestGetNodeByName_Absent(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	got, err := db.GetNodeByName("nope")
	if err != nil {
		t.Fatalf("GetNodeByName: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for absent node, got %+v", got)
	}
}

func TestReconcileNodes_DisablesVanished(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	// Initial topology: two enabled nodes.
	if err := db.ReconcileNodes([]models.Node{
		{Name: "eu-1", APIAddr: "10.7.0.1:10085", Enabled: true},
		{Name: "eu-2", APIAddr: "10.7.0.2:10085", Enabled: true},
	}); err != nil {
		t.Fatalf("ReconcileNodes initial: %v", err)
	}

	// eu-2 drops out of config → should be disabled, not deleted.
	if err := db.ReconcileNodes([]models.Node{
		{Name: "eu-1", APIAddr: "10.7.0.1:10085", Enabled: true},
	}); err != nil {
		t.Fatalf("ReconcileNodes reduced: %v", err)
	}

	all, err := db.ListNodes()
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 nodes (vanished kept, disabled), got %d", len(all))
	}
	byName := map[string]models.Node{}
	for _, n := range all {
		byName[n.Name] = n
	}
	if !byName["eu-1"].Enabled {
		t.Error("eu-1 should remain enabled")
	}
	if byName["eu-2"].Enabled {
		t.Error("eu-2 should be disabled after vanishing from config")
	}
}

func TestBackfillUserNodesToEnabled(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	// Two users, one enabled + one disabled node.
	u1, err := db.CreateUser("alice", "", "tok-alice", "")
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	u2, err := db.CreateUser("bob", "", "tok-bob", "")
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	localID, err := db.UpsertNode(models.Node{Name: "local", APIAddr: "127.0.0.1:10085", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertNode local: %v", err)
	}
	if _, err := db.UpsertNode(models.Node{Name: "eu-2", APIAddr: "10.7.0.2:10085", Enabled: false}); err != nil {
		t.Fatalf("UpsertNode eu-2: %v", err)
	}

	if err := db.BackfillUserNodesToEnabled(); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	// Both users placed on the enabled node only.
	for _, u := range []int64{u1.ID, u2.ID} {
		ids, err := db.ListNodeIDsForUser(u)
		if err != nil {
			t.Fatalf("ListNodeIDsForUser(%d): %v", u, err)
		}
		if len(ids) != 1 || ids[0] != localID {
			t.Errorf("user %d: got node ids %v, want [%d]", u, ids, localID)
		}
	}
}

func TestBackfill_Idempotent_SkipsAlreadyPlaced(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.CreateUser("carol", "", "tok-carol", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	aID, err := db.UpsertNode(models.Node{Name: "a", APIAddr: "10.7.0.1:10085", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertNode a: %v", err)
	}
	// Explicitly place carol on node a only.
	if err := db.AssignUserToNodes(u.ID, []int64{aID}); err != nil {
		t.Fatalf("AssignUserToNodes: %v", err)
	}

	// A new enabled node appears. Backfill must NOT touch carol (already placed),
	// preserving explicit per-user placement.
	if _, err := db.UpsertNode(models.Node{Name: "b", APIAddr: "10.7.0.2:10085", Enabled: true}); err != nil {
		t.Fatalf("UpsertNode b: %v", err)
	}
	if err := db.BackfillUserNodesToEnabled(); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	ids, err := db.ListNodeIDsForUser(u.ID)
	if err != nil {
		t.Fatalf("ListNodeIDsForUser: %v", err)
	}
	if len(ids) != 1 || ids[0] != aID {
		t.Errorf("backfill should not have added node b to an already-placed user: got %v, want [%d]", ids, aID)
	}

	// Running backfill again is a no-op.
	if err := db.BackfillUserNodesToEnabled(); err != nil {
		t.Fatalf("Backfill 2: %v", err)
	}
	ids2, _ := db.ListNodeIDsForUser(u.ID)
	if len(ids2) != 1 {
		t.Errorf("second backfill changed placement: got %v", ids2)
	}
}

func TestUserNodes_CascadeOnUserDelete(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.CreateUser("dave", "", "tok-dave", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	nID, err := db.UpsertNode(models.Node{Name: "local", APIAddr: "127.0.0.1:10085", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if err := db.AssignUserToNodes(u.ID, []int64{nID}); err != nil {
		t.Fatalf("AssignUserToNodes: %v", err)
	}
	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	ids, err := db.ListNodeIDsForUser(u.ID)
	if err != nil {
		t.Fatalf("ListNodeIDsForUser: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("user_nodes should cascade-delete with user, got %v", ids)
	}
}

func TestSetUserNodes_ReplacesSet(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, err := db.CreateUser("alice", "", "tok", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	a, _ := db.UpsertNode(models.Node{Name: "a", APIAddr: "10.7.0.1:10085", Enabled: true})
	b, _ := db.UpsertNode(models.Node{Name: "b", APIAddr: "10.7.0.2:10085", Enabled: true})
	c, _ := db.UpsertNode(models.Node{Name: "c", APIAddr: "10.7.0.3:10085", Enabled: true})

	if err := db.SetUserNodes(u.ID, []int64{a, b}); err != nil {
		t.Fatalf("SetUserNodes: %v", err)
	}
	ids, _ := db.ListNodeIDsForUser(u.ID)
	if len(ids) != 2 {
		t.Fatalf("after first set: got %v, want 2 ids", ids)
	}

	// Replace with a different set — old placements must be dropped, not merged.
	if err := db.SetUserNodes(u.ID, []int64{c}); err != nil {
		t.Fatalf("SetUserNodes 2: %v", err)
	}
	ids, _ = db.ListNodeIDsForUser(u.ID)
	if len(ids) != 1 || ids[0] != c {
		t.Errorf("replace failed: got %v, want [%d]", ids, c)
	}

	// Empty set clears all placements.
	if err := db.SetUserNodes(u.ID, nil); err != nil {
		t.Fatalf("SetUserNodes clear: %v", err)
	}
	ids, _ = db.ListNodeIDsForUser(u.ID)
	if len(ids) != 0 {
		t.Errorf("clear failed: got %v", ids)
	}
}

func TestRemoveUserFromNode(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, _ := db.CreateUser("bob", "", "tok2", "")
	a, _ := db.UpsertNode(models.Node{Name: "a", APIAddr: "10.7.0.1:10085", Enabled: true})
	b, _ := db.UpsertNode(models.Node{Name: "b", APIAddr: "10.7.0.2:10085", Enabled: true})
	if err := db.SetUserNodes(u.ID, []int64{a, b}); err != nil {
		t.Fatalf("SetUserNodes: %v", err)
	}

	if err := db.RemoveUserFromNode(u.ID, a); err != nil {
		t.Fatalf("RemoveUserFromNode: %v", err)
	}
	ids, _ := db.ListNodeIDsForUser(u.ID)
	if len(ids) != 1 || ids[0] != b {
		t.Errorf("got %v, want [%d]", ids, b)
	}
	// Removing a non-placement is a no-op.
	if err := db.RemoveUserFromNode(u.ID, a); err != nil {
		t.Errorf("removing absent placement should be no-op: %v", err)
	}
}

func TestListNodesForUser(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	u, _ := db.CreateUser("carol", "", "tok3", "")
	a, _ := db.UpsertNode(models.Node{Name: "eu-1", APIAddr: "10.7.0.1:10085", InboundTag: "vless-in", PublicHost: "eu1.example.com", PublicPort: 443, Enabled: true})
	_, _ = db.UpsertNode(models.Node{Name: "eu-2", APIAddr: "10.7.0.2:10085", Enabled: true})
	if err := db.SetUserNodes(u.ID, []int64{a}); err != nil {
		t.Fatalf("SetUserNodes: %v", err)
	}

	nodes, err := db.ListNodesForUser(u.ID)
	if err != nil {
		t.Fatalf("ListNodesForUser: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "eu-1" || nodes[0].PublicHost != "eu1.example.com" {
		t.Errorf("got %+v, want just eu-1 with full fields", nodes)
	}
}

func TestEnabledNodeIDs(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	a, _ := db.UpsertNode(models.Node{Name: "a", APIAddr: "10.7.0.1:10085", Enabled: true})
	_, _ = db.UpsertNode(models.Node{Name: "b", APIAddr: "10.7.0.2:10085", Enabled: false})
	c, _ := db.UpsertNode(models.Node{Name: "c", APIAddr: "10.7.0.3:10085", Enabled: true})

	ids, err := db.EnabledNodeIDs()
	if err != nil {
		t.Fatalf("EnabledNodeIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %v, want 2 enabled ids", ids)
	}
	set := map[int64]bool{ids[0]: true, ids[1]: true}
	if !set[a] || !set[c] {
		t.Errorf("expected enabled a,c; got %v", ids)
	}
}
