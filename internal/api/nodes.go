package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/alchemylink/raven-subscribe/internal/models"
)

// expandClientsForNodes turns a user's per-inbound client list into a
// per-node-endpoint list for multi-node subscription generation (Phase 3,
// docs/multi-node-design.md §6.3).
//
// Single-node (no nodes configured): returns clients unchanged, so generated
// configs stay byte-identical to before.
//
// Multi-node: for each enabled node the user is placed on (user_nodes), emit a
// copy of the client whose inbound matches the node's inbound_tag, tagged with
// that node's public endpoint. A user on N nodes serving the same inbound thus
// yields N outbounds that the generator places under one balancer. A client
// whose inbound is served by no placed node is dropped — in multi-node mode
// endpoints come from nodes, so it has nowhere to point.
func (s *Server) expandClientsForNodes(userID int64, clients []models.UserClientFull) []models.UserClientFull {
	if len(s.cfg.Nodes) == 0 {
		return clients
	}

	nodeIDs, err := s.db.ListNodeIDsForUser(userID)
	if err != nil {
		// #nosec G706 -- userID is an int64 (not injectable) and err is an internal DB error.
		log.Printf("WARN multi-node: list node placements for user %d: %v — falling back to single-endpoint", userID, err)
		return clients
	}
	if len(nodeIDs) == 0 {
		return clients
	}
	placed := make(map[int64]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		placed[id] = true
	}

	allNodes, err := s.db.ListNodes()
	if err != nil {
		log.Printf("WARN multi-node: list nodes: %v — falling back to single-endpoint", err)
		return clients
	}
	// inbound tag -> the enabled, placed nodes serving it.
	byTag := make(map[string][]models.Node)
	for _, n := range allNodes {
		if !n.Enabled || !placed[n.ID] {
			continue
		}
		byTag[n.InboundTag] = append(byTag[n.InboundTag], n)
	}

	expanded := make([]models.UserClientFull, 0, len(clients))
	for _, c := range clients {
		nodes := byTag[c.InboundTag]
		if len(nodes) == 0 {
			// No placed node serves this inbound; nothing to point an outbound at.
			continue
		}
		for _, n := range nodes {
			cp := c
			cp.NodeName = n.Name
			cp.NodeHost = n.PublicHost
			cp.NodePort = n.PublicPort
			expanded = append(expanded, cp)
		}
	}
	return expanded
}

// ── Node topology + placement API (admin) ────────────────────────────────────

// listNodes returns the node topology from the DB. Read-only: node definitions
// are managed in config (reconciled at startup, docs/multi-node-design.md §5),
// not created via the API. Works in single-node too (returns the implicit
// "local" node).
func (s *Server) listNodes(w http.ResponseWriter, _ *http.Request) {
	nodes, err := s.db.ListNodes()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if nodes == nil {
		nodes = []models.Node{}
	}
	jsonOK(w, nodes)
}

// getUserNodes lists the nodes a user is placed on.
func (s *Server) getUserNodes(w http.ResponseWriter, r *http.Request) {
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	nodes, err := s.db.ListNodesForUser(user.ID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if nodes == nil {
		nodes = []models.Node{}
	}
	jsonOK(w, nodes)
}

// setUserNodes replaces a user's node placement. Body: {"nodes":["eu-1","eu-2"]}.
// Placement only affects which node endpoints appear in the user's subscription
// (provisioning already fans out to every node), so this is a pure user_nodes
// write. Rejected when multi-node is not configured.
func (s *Server) setUserNodes(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.Nodes) == 0 {
		jsonError(w, "multi-node not configured", http.StatusBadRequest)
		return
	}
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	var body struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.NewDecoder(limitRequestBody(r)).Decode(&body); err != nil {
		jsonError(w, "invalid json body", http.StatusBadRequest)
		return
	}
	ids, bad, err := s.resolveNodeNames(body.Nodes)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(bad) > 0 {
		jsonError(w, "unknown node(s): "+strings.Join(bad, ", "), http.StatusBadRequest)
		return
	}
	if err := s.db.SetUserNodes(user.ID, ids); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nodes, _ := s.db.ListNodesForUser(user.ID)
	if nodes == nil {
		nodes = []models.Node{}
	}
	jsonOK(w, nodes)
}

// deleteUserNode removes a single placement by node name. Rejected when
// multi-node is not configured.
func (s *Server) deleteUserNode(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.Nodes) == 0 {
		jsonError(w, "multi-node not configured", http.StatusBadRequest)
		return
	}
	user, err := s.getByID(w, r)
	if user == nil || err != nil {
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["nodeName"])
	node, err := s.db.GetNodeByName(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if node == nil {
		jsonError(w, "node not found", http.StatusNotFound)
		return
	}
	if err := s.db.RemoveUserFromNode(user.ID, node.ID); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "removed", "node": name})
}

// resolveNodeNames maps node names to their ids. Returns the ids for known
// names and the list of names that don't exist; err is only a real DB error.
func (s *Server) resolveNodeNames(names []string) (ids []int64, bad []string, err error) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		n, err := s.db.GetNodeByName(name)
		if err != nil {
			return nil, nil, err
		}
		if n == nil {
			bad = append(bad, name)
			continue
		}
		ids = append(ids, n.ID)
	}
	return ids, bad, nil
}

// placeUserOnNodes sets a new user's placement in multi-node mode: the given
// node names, or all enabled nodes when none are given (default policy §11).
// No-op in single-node mode. Callers should pre-validate explicit names.
func (s *Server) placeUserOnNodes(userID int64, names []string) error {
	if len(s.cfg.Nodes) == 0 {
		return nil
	}
	var ids []int64
	if len(names) > 0 {
		var bad []string
		var err error
		ids, bad, err = s.resolveNodeNames(names)
		if err != nil {
			return err
		}
		if len(bad) > 0 {
			return fmt.Errorf("unknown node(s): %s", strings.Join(bad, ", "))
		}
	} else {
		var err error
		if ids, err = s.db.EnabledNodeIDs(); err != nil {
			return err
		}
	}
	return s.db.SetUserNodes(userID, ids)
}
