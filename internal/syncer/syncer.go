package syncer

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"xray-subscription/internal/config"
	"xray-subscription/internal/database"
	"xray-subscription/internal/xray"
)

// Syncer watches /etc/xray/config.d and keeps the DB up-to-date
type Syncer struct {
	cfg *config.Config
	db  *database.DB
}

func New(cfg *config.Config, db *database.DB) *Syncer {
	return &Syncer{cfg: cfg, db: db}
}

// Start begins periodic sync + file-watch based sync
func (s *Syncer) Start() {
	// File watcher
	go s.watch()

	// Periodic sync as fallback
	interval := time.Duration(s.cfg.SyncInterval) * time.Second
	if interval < time.Second {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if err := s.Sync(); err != nil {
			log.Printf("Periodic sync error: %v", err)
		}
	}
}

func (s *Syncer) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsnotify init error: %v", err)
		return
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			log.Printf("watcher close error: %v", err)
		}
	}()

	if err := watcher.Add(s.cfg.ConfigDir); err != nil {
		log.Printf("Watch %s error: %v", s.cfg.ConfigDir, err)
		return
	}

	log.Printf("Watching %s for changes", s.cfg.ConfigDir)
	debounce := time.NewTimer(0)
	<-debounce.C // drain initial fire

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(event.Name, ".json") {
				continue
			}
			// Debounce rapid writes
			debounce.Reset(500 * time.Millisecond)
		case <-debounce.C:
			log.Printf("Config change detected, syncing...")
			if err := s.Sync(); err != nil {
				log.Printf("Sync after change error: %v", err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// Sync reads config.d and updates the database with inbounds and clients
func (s *Syncer) Sync() error {
	log.Printf("Syncing from %s", s.cfg.ConfigDir)

	parsed, err := xray.ParseConfigDir(s.cfg.ConfigDir)
	if err != nil {
		return fmt.Errorf("parse config dir: %w", err)
	}

	totalInbounds := 0
	totalClients := 0

	for file, inbounds := range parsed {
		presentTags := make([]string, 0, len(inbounds))
		for _, ib := range inbounds {
			presentTags = append(presentTags, ib.Tag)
		}

		// Remove inbounds from this file that are no longer present
		removedTags, err := s.db.GetInboundTagsNotInFile(file, presentTags)
		if err != nil {
			log.Printf("WARN: get removed tags for %s: %v", file, err)
		}
		for _, tag := range removedTags {
			log.Printf("Removing stale inbound: %s", tag)
			if err := s.db.DeleteInboundByTag(tag); err != nil {
				log.Printf("WARN: delete stale inbound %s: %v", tag, err)
			}
		}

		for _, ib := range inbounds {
			ibID, err := s.db.UpsertInbound(ib.Tag, ib.Protocol, ib.Port, file, ib.RawJSON)
			if err != nil {
				log.Printf("WARN: upsert inbound %s: %v", ib.Tag, err)
				continue
			}
			totalInbounds++

			// Sync clients
			for _, client := range ib.Clients {
				if err := s.syncClient(ibID, client); err != nil {
					log.Printf("WARN: sync client %s in %s: %v", client.Identity, ib.Tag, err)
					continue
				}
				totalClients++
			}
		}
	}

	log.Printf("Sync complete: %d inbounds, %d client entries", totalInbounds, totalClients)
	return nil
}

// syncClient finds or creates a user and links them to the inbound
func (s *Syncer) syncClient(inboundID int64, client xray.ParsedClient) error {
	identity := sanitizeUsername(client.Identity)
	if identity == "" {
		return fmt.Errorf("empty client identity")
	}

	// Find or create the user
	user, err := s.db.GetUserByUsername(identity)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}

	if user == nil {
		// Auto-create user with generated token
		token := generateToken()
		user, err = s.db.CreateUser(identity, token)
		if err != nil {
			return fmt.Errorf("create user %s: %w", identity, err)
		}
		log.Printf("Auto-created user: %s", identity)
	}

	// Upsert the user-inbound mapping
	return s.db.UpsertUserClient(user.ID, inboundID, client.ConfigJSON)
}

func sanitizeUsername(s string) string {
	// Remove characters that could cause issues
	r := strings.NewReplacer(
		" ", "_", "\t", "_", "\n", "", "\r", "",
		"'", "", "\"", "", ";", "", "--", "",
	)
	result := r.Replace(strings.TrimSpace(s))
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}
