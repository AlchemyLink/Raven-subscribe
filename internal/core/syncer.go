package core

// ConfigSyncer reconciles the engine's runtime view of users with the
// authoritative DB list. It is invoked on engine restart (Restore) and on
// drift-detection ticks (SyncDBToConfig). Both paths share AdminAPI under the
// hood; ConfigSyncer is the policy layer that decides what to add and in
// what order.
//
// ConfigSyncer never deletes users: drift fix is additive only. Users that
// are in the engine but not in the DB are surfaced via /api/sync/status and
// resolved out-of-band by the operator. This invariant matches the existing
// xray.SyncDBToConfig behaviour and is preserved through the refactor.
type ConfigSyncer interface {
	// Restore re-adds every DB-known user to the given inbound. Called once
	// on startup after the engine has been (re)launched.
	Restore(inboundTag string, users []ClientToRestore) error

	// SyncDBToConfig diffs the desired user set (want) against the engine's
	// current set (have) and adds the missing entries. Returns a SyncResult
	// describing what was added and which usernames failed.
	SyncDBToConfig(inboundTag string, want []ClientToRestore, have map[string]bool) SyncResult

	// ExistingIdentities returns the set of client identities the engine
	// currently knows for the given inbound. Used by SyncDBToConfig callers
	// to compute the diff.
	ExistingIdentities(inboundTag string) (map[string]bool, error)
}

// ClientToRestore is the per-user input to Restore and SyncDBToConfig. It
// carries the stored credential blob plus an optional protocol hint used
// when the inbound's protocol cannot be looked up from the engine config.
type ClientToRestore struct {
	Identity     string // Xray "email" field — typically the username
	StoredConfig string // engine-native client credential JSON
	Protocol     string // optional hint; "" means "look it up from the inbound"
}

// SyncResult is the outcome of a single SyncDBToConfig call. Field semantics
// match the historical xray.SyncResult so existing /api/sync/status callers
// see the same response shape after Phase 2 wires the engine implementation
// into core.
type SyncResult struct {
	// Added counts users successfully appended to the inbound in this pass.
	Added int

	// FailedUsers lists usernames whose write failed. Recorded so admins can
	// see who's broken in the health endpoint without grepping logs.
	FailedUsers []string

	// FirstError is the first underlying error message encountered. Used to
	// surface a representative reason without overwhelming the response.
	FirstError string
}
