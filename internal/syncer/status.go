package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SyncStatus is the diagnostic snapshot served by /api/sync/status.
//
// It exists because raven-subscribe's persistence path (writing user JSON to
// /etc/xray/config.d/*) can fail silently — most notably when the directory
// has wrong ownership and *.raven.tmp opens fail with EACCES every minute.
// Before this struct, those errors only surfaced in journalctl WARN lines and
// users got broken VPN with no signal to the admin.
//
// The fields are populated by Syncer.Sync() (via recordSync) and by the
// startup probe (via setProbeOK). All access goes through statusMu.
type SyncStatus struct {
	// LastSyncAt is when the most recent Sync() iteration completed,
	// regardless of outcome. Zero value means "never ran".
	LastSyncAt time.Time `json:"last_sync_at"`

	// LastSyncOK reflects whether the most recent iteration completed with
	// zero per-user persistence failures (and no top-level error).
	LastSyncOK bool `json:"last_sync_ok"`

	// LastError is a short human-readable reason for the most recent
	// failure. Empty when LastSyncOK.
	LastError string `json:"last_error,omitempty"`

	// ErrorsLastHour is a rolling count of failed iterations within the
	// trailing 60 minutes. Useful for "is this transient or chronic?".
	ErrorsLastHour int `json:"errors_last_hour"`

	// DBUsers / ConfigUsers are aggregate counts across all inbounds —
	// big drift between them is a strong signal something's broken.
	DBUsers     int `json:"db_users"`
	ConfigUsers int `json:"config_users"`

	// Drift lists usernames present in the DB but missing from xray's
	// on-disk config for at least one inbound. These users hold valid
	// subscription URLs but xray will reject their UUIDs.
	Drift []DriftEntry `json:"drift,omitempty"`

	// ProbeOK is set on startup by Syncer.Probe(). False means the daemon
	// cannot write to ConfigDir at all — every periodic sync will fail
	// the same way until a human fixes ownership/permissions.
	ProbeOK bool `json:"probe_ok"`

	// ProbeError carries the underlying OS error when ProbeOK is false.
	ProbeError string `json:"probe_error,omitempty"`
}

// DriftEntry pinpoints one user-inbound pair that is in the DB but not in
// the on-disk config. A single user can appear multiple times when they're
// missing from several inbounds.
type DriftEntry struct {
	Username   string `json:"username"`
	InboundTag string `json:"inbound_tag"`
}

// errorRingSize bounds the ErrorsLastHour ring. 12 buckets at 5min each
// gives a 1-hour window without storing every individual timestamp.
const errorRingSize = 12

// errorRing tracks failure timestamps in a fixed-size ring; rolling-hour
// counts come from len() of entries newer than now-1h.
type errorRing struct {
	timestamps []time.Time
}

func (r *errorRing) push(t time.Time) {
	r.timestamps = append(r.timestamps, t)
	cutoff := t.Add(-time.Hour)
	// Drop anything older than 1h to keep the slice bounded.
	idx := 0
	for i, ts := range r.timestamps {
		if ts.After(cutoff) {
			idx = i
			break
		}
	}
	if idx > 0 {
		r.timestamps = append(r.timestamps[:0], r.timestamps[idx:]...)
	}
	if len(r.timestamps) > errorRingSize*4 {
		// Hard cap so a flapping daemon can't grow this unbounded.
		r.timestamps = r.timestamps[len(r.timestamps)-errorRingSize*4:]
	}
}

func (r *errorRing) countSince(cutoff time.Time) int {
	n := 0
	for _, t := range r.timestamps {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}

// statusState is everything Syncer needs to keep about its own health.
// Callers go through Syncer.recordSync() / Syncer.Status() — never directly.
type statusState struct {
	mu        sync.Mutex
	probeOK   bool
	probeErr  string
	lastAt    time.Time
	lastOK    bool
	lastErr   string
	errors    errorRing
	dbUsers   int
	confUsers int
	drift     []DriftEntry
}

func (s *statusState) snapshot() SyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := SyncStatus{
		LastSyncAt:     s.lastAt,
		LastSyncOK:     s.lastOK,
		LastError:      s.lastErr,
		ErrorsLastHour: s.errors.countSince(time.Now().Add(-time.Hour)),
		DBUsers:        s.dbUsers,
		ConfigUsers:    s.confUsers,
		ProbeOK:        s.probeOK,
		ProbeError:     s.probeErr,
	}
	if len(s.drift) > 0 {
		out.Drift = make([]DriftEntry, len(s.drift))
		copy(out.Drift, s.drift)
	}
	return out
}

func (s *statusState) record(at time.Time, ok bool, errMsg string,
	dbUsers, confUsers int, drift []DriftEntry,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAt = at
	s.lastOK = ok
	s.lastErr = errMsg
	s.dbUsers = dbUsers
	s.confUsers = confUsers
	s.drift = drift
	if !ok {
		s.errors.push(at)
	}
}

func (s *statusState) setProbe(ok bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeOK = ok
	s.probeErr = errMsg
}

// Probe verifies the daemon can write into configDir by creating a small
// file and removing it. Failure here means every subsequent SyncDBToConfig
// will fail too — surfacing this at startup turns a silent regression
// (the trener_nazar incident) into a loud one.
//
// The probe runs unconditionally even when xray_api_addr is empty, because
// SyncDBToConfig is the only persistence path for newly-created users.
func (s *Syncer) Probe() {
	dir := s.cfg.ConfigDir
	if dir == "" {
		s.status.setProbe(true, "")
		return
	}
	probe := filepath.Join(dir, fmt.Sprintf(".raven-write-probe-%d", os.Getpid()))
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		s.status.setProbe(false, err.Error())
		return
	}
	// Best-effort cleanup; if removal fails (e.g. file got deleted between
	// write and remove) the probe still succeeded.
	_ = os.Remove(probe)
	s.status.setProbe(true, "")
}

// Status returns the current snapshot. Cheap to call from HTTP handlers.
func (s *Syncer) Status() SyncStatus {
	return s.status.snapshot()
}
