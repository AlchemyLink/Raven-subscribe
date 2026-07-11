package api

import (
	"log"

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
