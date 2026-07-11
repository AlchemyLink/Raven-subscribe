package xray

import (
	"fmt"
	"log"
	"strings"

	"github.com/alchemylink/raven-subscribe/internal/core"
)

// NamedAdmin pairs a node name with the core.AdminAPI that mutates that node.
// The name is used only for diagnostics (per-node warnings on partial failure).
type NamedAdmin struct {
	Name  string
	Admin core.AdminAPI
}

// fanoutAdmin implements core.AdminAPI by applying every mutation to a set of
// nodes (multi-node topology). It hides behind the same interface as a single
// admin, so api.Server can't tell one node from N (docs/multi-node-design.md §6.1).
//
// Failure policy (§6.4): partial failures are NOT rolled back — a user created
// on the reachable node stays created — and are surfaced via per-node warnings.
//   - additive ops (AddClient/AddExistingClient/AddInboundFromJSON) succeed when
//     at least one node applied them; an "already exists" reply counts as applied.
//   - removals (RemoveClient/RemoveInbound) treat "not found" as already-done and
//     return the joined real errors from nodes that could not confirm removal, so
//     a down node stays visible (it gets reconciled once it returns — §6.4/§13).
type fanoutAdmin struct {
	targets []NamedAdmin
}

var _ core.AdminAPI = (*fanoutAdmin)(nil)

// NewFanoutAdmin returns a core.AdminAPI that fans every operation out to all
// targets. Targets are the enabled nodes of the topology; each is typically a
// grpc-only per-node admin (NewGRPCAdmin with an empty configDir).
func NewFanoutAdmin(targets []NamedAdmin) core.AdminAPI {
	return &fanoutAdmin{targets: targets}
}

// isBenignExists reports whether err is Xray's idempotent "already exists"
// reply (AddUser/AddInbound on something already present). Mirrors the idiom in
// api/fallback.go.
func isBenignExists(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "exist")
}

// isBenignAbsent reports whether err is Xray's "already absent" reply
// (RemoveUser/RemoveInbound on something not present). xray-core surfaces this
// via several idioms depending on the dispatch path.
func isBenignAbsent(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such") ||
		strings.Contains(lower, "not enough information")
}

// AddClient fans a new-client add out to every node and returns one stored
// credential JSON to persist. Because nodes are homogeneous the credential is
// identical on every node; the first success wins and any divergent success is
// logged loudly as cross-node config drift. Succeeds if at least one node
// accepted the client; errors only if all nodes failed.
func (f *fanoutAdmin) AddClient(inboundTag, identity string, hint core.AddClientHint) (string, error) {
	var stored string
	var haveStored bool
	var failures []string
	okCount := 0

	for _, t := range f.targets {
		got, err := t.Admin.AddClient(inboundTag, identity, hint)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", t.Name, err))
			continue
		}
		okCount++
		switch {
		case !haveStored:
			stored, haveStored = got, true
		case got != stored:
			log.Printf("WARN fanout AddClient %s@%s: node %q returned a different stored config than %q — cross-node config drift",
				identity, inboundTag, t.Name, f.targets[0].Name)
		}
	}

	logFanoutFailures("AddClient", inboundTag, identity, okCount, failures)
	if okCount == 0 {
		return "", fmt.Errorf("AddClient %s@%s failed on all %d nodes: %s", identity, inboundTag, len(f.targets), strings.Join(failures, "; "))
	}
	return stored, nil
}

// AddExistingClient re-adds a stored client to every node. "Already exists" is
// benign (the client is already present). Succeeds if at least one node has the
// client afterwards.
func (f *fanoutAdmin) AddExistingClient(inboundTag, identity, storedConfigJSON string) error {
	var failures []string
	okCount := 0
	for _, t := range f.targets {
		err := t.Admin.AddExistingClient(inboundTag, identity, storedConfigJSON)
		if err == nil || isBenignExists(err) {
			okCount++
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %v", t.Name, err))
	}
	logFanoutFailures("AddExistingClient", inboundTag, identity, okCount, failures)
	if okCount == 0 {
		return fmt.Errorf("AddExistingClient %s@%s failed on all %d nodes: %s", identity, inboundTag, len(f.targets), strings.Join(failures, "; "))
	}
	return nil
}

// RemoveClient removes a client from every node. "Not found" is benign (already
// gone). Returns the joined real errors from nodes that could not confirm the
// removal so the failure stays visible; a down node is reconciled once it
// returns (§6.4). The caller proceeds with the DB delete regardless.
func (f *fanoutAdmin) RemoveClient(inboundTag, identity string) error {
	var failures []string
	for _, t := range f.targets {
		err := t.Admin.RemoveClient(inboundTag, identity)
		if err == nil || isBenignAbsent(err) {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %v", t.Name, err))
	}
	if len(failures) > 0 {
		log.Printf("WARN fanout RemoveClient %s@%s: %d/%d nodes failed: %s",
			identity, inboundTag, len(failures), len(f.targets), strings.Join(failures, "; "))
		return fmt.Errorf("RemoveClient %s@%s failed on %d node(s): %s", identity, inboundTag, len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// AddInboundFromJSON brings an inbound up on every node (killswitch enable).
// "Already exists" is benign. Succeeds if at least one node has it.
func (f *fanoutAdmin) AddInboundFromJSON(rawJSON string) error {
	var failures []string
	okCount := 0
	for _, t := range f.targets {
		err := t.Admin.AddInboundFromJSON(rawJSON)
		if err == nil || isBenignExists(err) {
			okCount++
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %v", t.Name, err))
	}
	if len(failures) > 0 {
		log.Printf("WARN fanout AddInboundFromJSON: %d/%d nodes failed: %s", len(failures), len(f.targets), strings.Join(failures, "; "))
	}
	if okCount == 0 {
		return fmt.Errorf("AddInboundFromJSON failed on all %d nodes: %s", len(f.targets), strings.Join(failures, "; "))
	}
	return nil
}

// RemoveInbound tears an inbound down on every node (killswitch disable).
// "Not found"/"no such" is benign. Returns the joined real errors from nodes
// that could not confirm removal.
func (f *fanoutAdmin) RemoveInbound(tag string) error {
	var failures []string
	for _, t := range f.targets {
		err := t.Admin.RemoveInbound(tag)
		if err == nil || isBenignAbsent(err) {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %v", t.Name, err))
	}
	if len(failures) > 0 {
		log.Printf("WARN fanout RemoveInbound %s: %d/%d nodes failed: %s", tag, len(failures), len(f.targets), strings.Join(failures, "; "))
		return fmt.Errorf("RemoveInbound %s failed on %d node(s): %s", tag, len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// Engine reports the backing engine. All nodes are homogeneous Xray.
func (f *fanoutAdmin) Engine() string { return "xray" }

// Version returns the version of the first target, or empty when unknown.
func (f *fanoutAdmin) Version() string {
	if len(f.targets) == 0 {
		return ""
	}
	return f.targets[0].Admin.Version()
}

func logFanoutFailures(op, inboundTag, identity string, okCount int, failures []string) {
	if len(failures) > 0 {
		log.Printf("WARN fanout %s %s@%s: %d ok, %d failed: %s",
			op, identity, inboundTag, okCount, len(failures), strings.Join(failures, "; "))
	}
}
