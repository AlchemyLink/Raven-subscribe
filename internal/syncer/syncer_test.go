package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
)

func TestSyncer_New(t *testing.T) {
	cfg := &config.Config{ConfigDir: "/tmp/xray"}
	db, cleanup := testDB(t)
	defer cleanup()
	s := New(cfg, db)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.cfg != cfg {
		t.Error("config not set")
	}
	_ = s
}

func TestSyncer_Sync_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}
	db, cleanup := testDB(t)
	defer cleanup()
	s := New(cfg, db)

	if err := s.Sync(); err != nil {
		t.Fatalf("Sync empty dir: %v", err)
	}
}

func TestSyncer_Sync_WithConfigFiles(t *testing.T) {
	// go test runs from module root
	configDir := filepath.Join("testdata", "xray", "config.d")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Skip("testdata/xray/config.d not found")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := &config.Config{ConfigDir: configDir}
	s := New(cfg, db)

	if err := s.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	inbounds, err := db.ListInbounds()
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) == 0 {
		t.Error("expected at least one inbound after sync")
	}
}

func TestSyncer_Sync_APIFallbackCreatesInbound(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "empty_config")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	defer func() { _ = db.Close() }()

	cfg := &config.Config{
		ConfigDir:             configDir,
		APIUserInboundTag:     "vless-api",
		APIUserInboundProtocol: "vless",
		APIUserInboundPort:    443,
	}
	s := New(cfg, db)

	if err := s.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	inbounds, err := db.ListInbounds()
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound (api fallback), got %d", len(inbounds))
	}
	if inbounds[0].Tag != "vless-api" {
		t.Errorf("Tag: got %q, want vless-api", inbounds[0].Tag)
	}
}

func TestSyncer_Start_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir, SyncInterval: 1}
	db, cleanup := testDB(t)
	defer cleanup()
	s := New(cfg, db)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not stop after context cancel")
	}
}

func testDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatalf("database.New: %v", err)
	}
	return db, func() { _ = db.Close() }
}
