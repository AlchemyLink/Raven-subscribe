// Package syncer keeps the database in sync with xray config files via file-watching and periodic polling.
package syncer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/alchemylink/raven-subscribe/internal/config"
	"github.com/alchemylink/raven-subscribe/internal/database"
	"github.com/alchemylink/raven-subscribe/internal/xray"
)

// Syncer watches /etc/xray/config.d and keeps the DB up-to-date
type Syncer struct {
	cfg    *config.Config
	db     *database.DB
	status statusState
}

// New creates a Syncer that watches cfg.ConfigDir and updates db on changes.
func New(cfg *config.Config, db *database.DB) *Syncer {
	return &Syncer{cfg: cfg, db: db}
}

// Start begins periodic sync + file-watch based sync until ctx is done.
func (s *Syncer) Start(ctx context.Context) {
	// File watcher
	go s.watch(ctx)

	// Periodic sync as fallback
	interval := time.Duration(s.cfg.SyncInterval) * time.Second
	if interval < time.Second {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Sync(); err != nil {
				log.Printf("Periodic sync error: %v", err)
			}
		}
	}
}

func (s *Syncer) watch(ctx context.Context) {
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
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return
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

// RestoreOnStartup restores API-created users to Xray via gRPC after a restart.
// Restores all users to all inbounds they are enrolled in.
func (s *Syncer) RestoreOnStartup() {
	apiAddr := strings.TrimSpace(s.cfg.XrayAPIAddr)
	if apiAddr == "" {
		return
	}

	inbounds, err := s.db.ListInbounds()
	if err != nil {
		return
	}
	for _, ib := range inbounds {
		users, err := s.db.ListUserClientsByInboundTag(ib.Tag)
		if err != nil || len(users) == 0 {
			continue
		}
		ucs := make([]struct {
			Username     string
			ClientConfig string
			Protocol     string
		}, len(users))
		for i, u := range users {
			ucs[i] = struct {
				Username     string
				ClientConfig string
				Protocol     string
			}{u.XrayClientEmail(), u.ClientConfig, u.Protocol}
		}
		xray.RestoreUsersToXray(apiAddr, ib.Tag, ucs)
	}
}

// Sync reads config.d and updates the database with inbounds and clients
func (s *Syncer) Sync() error {
	log.Printf("Syncing from %s", s.cfg.ConfigDir)

	// Ensure api_user_inbound_tag exists in DB when config_dir has no inbounds (api_user_inbound_protocol fallback)
	if tag := strings.TrimSpace(s.cfg.APIUserInboundTag); tag != "" && strings.TrimSpace(s.cfg.APIUserInboundProtocol) != "" {
		inbounds, _ := s.db.ListInbounds()
		var exists bool
		for _, ib := range inbounds {
			if ib.Tag == tag {
				exists = true
				break
			}
		}
		if !exists {
			protocol := strings.ToLower(strings.TrimSpace(s.cfg.APIUserInboundProtocol))
			port := s.cfg.APIUserInboundPort
			if port <= 0 {
				port = 443
			}
			if protocol == "vless" || protocol == "vmess" || protocol == "trojan" || protocol == "shadowsocks" {
				rawJSON := fmt.Sprintf(`{"tag":"%s","protocol":"%s","port":%d}`, tag, protocol, port)
				if _, err := s.db.UpsertInbound(tag, protocol, port, "api-managed", rawJSON); err == nil {
					log.Printf("Created inbound %s in DB (api_user_inbound_protocol fallback)", tag)
				}
			}
		}
	}

	if s.cfg.IsXrayEnabled() {
		if err := s.syncXray(); err != nil {
			return err
		}
	} else {
		log.Printf("Xray sync disabled (xray_enabled=false)")
	}

	return nil
}

// syncXray reads config_dir and upserts Xray inbounds/users into DB.
func (s *Syncer) syncXray() error {
	parsed, err := xray.ParseConfigDirWith(s.cfg.ConfigDir, s.cfg.VLESSClientEncryption)
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

			for _, client := range ib.Clients {
				if err := s.syncClient(ibID, client); err != nil {
					log.Printf("WARN: sync client %s in %s: %v", client.Identity, ib.Tag, err)
					continue
				}
				totalClients++
			}
		}
	}

	// Sync DB users to config when using Xray API (persist API-created users to disk)
	var (
		drift          []DriftEntry
		firstSyncErr   string
		dbUserTotal    int
		configUserHits int
	)
	if strings.TrimSpace(s.cfg.XrayAPIAddr) != "" {
		allInbounds, _ := s.db.ListInbounds()
		for _, ib := range allInbounds {
			users, _ := s.db.ListUserClientsByInboundTag(ib.Tag)
			if len(users) == 0 {
				continue
			}
			dbUserTotal += len(users)
			idents, err := xray.GetExistingIdentitiesInInbound(s.cfg.ConfigDir, ib.Tag)
			if err != nil {
				if firstSyncErr == "" {
					firstSyncErr = fmt.Sprintf("inbound %s: %v", ib.Tag, err)
				}
				continue
			}
			configUserHits += len(idents)
			ucs := make([]struct {
				Username     string
				ClientConfig string
				Protocol     string
			}, len(users))
			for i, u := range users {
				ucs[i] = struct {
					Username     string
					ClientConfig string
					Protocol     string
				}{u.XrayClientEmail(), u.ClientConfig, u.Protocol}
			}
			res := xray.SyncDBToConfig(s.cfg.ConfigDir, ib.Tag, ucs, idents, s.cfg.XrayConfigFilePerm())
			for _, u := range res.FailedUsers {
				drift = append(drift, DriftEntry{Username: u, InboundTag: ib.Tag})
			}
			if res.FirstError != "" && firstSyncErr == "" {
				firstSyncErr = fmt.Sprintf("inbound %s: %s", ib.Tag, res.FirstError)
			}
		}
	}

	ok := firstSyncErr == "" && len(drift) == 0
	s.status.record(time.Now(), ok, firstSyncErr, dbUserTotal, configUserHits, drift)

	log.Printf("Xray sync complete: %d inbounds, %d client entries", totalInbounds, totalClients)
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
		fallbackToken := generateToken()
		user, err = s.db.CreateUser(identity, identity, token, fallbackToken)
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
