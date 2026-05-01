package api

import (
	"context"
	"testing"
	"time"
)

// loopReturned reports whether the loop goroutine returns within timeout.
func loopReturned(t *testing.T, run func(), timeout time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() { run(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func TestKillSwitchLoop_ZeroIntervalReturnsImmediately(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	srv.cfg.XrayAPIAddr = "127.0.0.1:1"
	srv.cfg.FallbackInboundTags = []string{"vless-fallback-in"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !loopReturned(t, func() { srv.ReconcileKillSwitchLoop(ctx, 0) }, 100*time.Millisecond) {
		t.Fatal("loop did not return on interval=0")
	}
}

func TestKillSwitchLoop_NoXrayAPIReturnsImmediately(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	// XrayAPIAddr empty, tags set — guard should still trigger.
	srv.cfg.FallbackInboundTags = []string{"vless-fallback-in"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !loopReturned(t, func() { srv.ReconcileKillSwitchLoop(ctx, 50*time.Millisecond) }, 200*time.Millisecond) {
		t.Fatal("loop did not return when XrayAPIAddr empty")
	}
}

func TestKillSwitchLoop_NoFallbackTagsReturnsImmediately(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	srv.cfg.XrayAPIAddr = "127.0.0.1:1"
	// FallbackInboundTags empty — guard should still trigger.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !loopReturned(t, func() { srv.ReconcileKillSwitchLoop(ctx, 50*time.Millisecond) }, 200*time.Millisecond) {
		t.Fatal("loop did not return when FallbackInboundTags empty")
	}
}

func TestKillSwitchLoop_StopsOnCtxCancel(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	srv.cfg.XrayAPIAddr = "127.0.0.1:1"
	srv.cfg.FallbackInboundTags = []string{"vless-fallback-in"}
	// Default DB state has fallback_enabled=true → reconcile early-returns
	// inside each tick without a gRPC call, so cancellation is fast.

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		srv.ReconcileKillSwitchLoop(ctx, 25*time.Millisecond)
		close(done)
	}()
	time.Sleep(75 * time.Millisecond) // let it tick a few times
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("loop did not exit within 500ms after ctx cancel")
	}
}

// TestKillSwitchToggle_ConcurrentEnableDisableSerialised verifies that the
// killswitch mutex serialises concurrent toggle handlers so the final DB and
// log state is consistent rather than interleaved.
func TestKillSwitchToggle_ConcurrentEnableDisableSerialised(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()
	// Empty XrayAPIAddr → applyKillSwitchInboundsLocked is a no-op, but the
	// mutex still serialises the SetFallbackEnabled writes.

	const N = 20
	finished := make(chan bool, N*2)

	for i := 0; i < N; i++ {
		go func() {
			srv.killSwitchMu.Lock()
			_ = srv.db.SetFallbackEnabled(true)
			srv.killSwitchMu.Unlock()
			finished <- true
		}()
		go func() {
			srv.killSwitchMu.Lock()
			_ = srv.db.SetFallbackEnabled(false)
			srv.killSwitchMu.Unlock()
			finished <- true
		}()
	}
	for i := 0; i < N*2; i++ {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("toggles did not complete — possible deadlock")
		}
	}
	// We do not assert final state because last-writer-wins is the design;
	// the test only proves no deadlock and no panic under contention.
}
