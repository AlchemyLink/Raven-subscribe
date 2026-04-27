package syncer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/config"
)

func TestProbe_FreshDirSucceeds(t *testing.T) {
	dir := t.TempDir()
	s := New(&config.Config{ConfigDir: dir}, nil)
	s.Probe()
	st := s.Status()
	if !st.ProbeOK {
		t.Fatalf("ProbeOK = false, error = %q", st.ProbeError)
	}
	// Probe file must be cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Base(e.Name()) != "" && len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("leftover dotfile in dir: %s", e.Name())
		}
	}
}

func TestProbe_ReadOnlyDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — perms checks are bypassed")
	}
	dir := t.TempDir()
	// Deliberately drop write so the probe hits EACCES; cleanup restores
	// owner-rwx so t.TempDir() can rm the path.
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // intentionally restrictive to exercise EACCES path
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore rwx-owner only, still tighter than the surrounding test infra

	s := New(&config.Config{ConfigDir: dir}, nil)
	s.Probe()
	st := s.Status()
	if st.ProbeOK {
		t.Fatal("ProbeOK = true on read-only dir, expected false")
	}
	if st.ProbeError == "" {
		t.Error("ProbeError empty — should carry the OS error")
	}
}

func TestStatus_RecordTracksDriftAndErrors(t *testing.T) {
	s := New(&config.Config{ConfigDir: t.TempDir()}, nil)

	// Successful sync — no drift, no errors.
	s.status.record(time.Now(), true, "", 5, 5, nil)
	st := s.Status()
	if !st.LastSyncOK || st.LastError != "" || st.ErrorsLastHour != 0 {
		t.Fatalf("clean record: got %+v", st)
	}

	// Drifted sync — counts a failure and lists the entry.
	drift := []DriftEntry{{Username: "alice", InboundTag: "vless-reality-v2-in"}}
	s.status.record(time.Now(), false, "permission denied", 6, 5, drift)
	st = s.Status()
	if st.LastSyncOK {
		t.Error("LastSyncOK should be false after error record")
	}
	if st.ErrorsLastHour != 1 {
		t.Errorf("ErrorsLastHour = %d, want 1", st.ErrorsLastHour)
	}
	if len(st.Drift) != 1 || st.Drift[0].Username != "alice" {
		t.Errorf("Drift = %+v", st.Drift)
	}
}

func TestErrorRing_DropsOlderThanHour(t *testing.T) {
	r := errorRing{}
	now := time.Now()
	r.push(now.Add(-90 * time.Minute)) // outside window
	r.push(now.Add(-30 * time.Minute)) // inside
	r.push(now.Add(-10 * time.Minute)) // inside
	got := r.countSince(now.Add(-time.Hour))
	if got != 2 {
		t.Errorf("count within last hour = %d, want 2", got)
	}
}
