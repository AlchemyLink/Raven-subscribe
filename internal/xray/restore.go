// Package xray provides restore and DB-to-config sync for API-created users.
package xray

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// RestoreUsersToXray restores all users from DB to Xray via gRPC API.
// Call on startup when xray_api_addr and api_user_inbound_tag are set.
func RestoreUsersToXray(apiAddr, inboundTag string, users []struct {
	Username     string
	ClientConfig string
	Protocol     string
}) {
	if apiAddr == "" || inboundTag == "" || len(users) == 0 {
		return
	}

	log.Printf("Restoring %d users to Xray inbound %s via API", len(users), inboundTag)
	restored := 0
	for _, u := range users {
		username := strings.TrimSpace(u.Username)
		if username == "" {
			continue
		}
		if err := AddExistingClientToInboundViaAPI(apiAddr, inboundTag, username, u.ClientConfig); err != nil {
			log.Printf("WARN: restore user %s to Xray: %v", username, err)
			continue
		}
		restored++
	}
	if restored > 0 {
		log.Printf("Restored %d/%d users to Xray", restored, len(users))
	}
}

// SyncResult is the outcome of a single SyncDBToConfig call.
type SyncResult struct {
	// Added is the number of users successfully appended to the inbound's
	// on-disk config in this pass.
	Added int
	// FailedUsers is the list of usernames whose config write failed. The
	// caller (typically the Syncer) records these for the /api/sync/status
	// drift list so admins can see who's broken without grepping logs.
	FailedUsers []string
	// FirstError is the first underlying error message encountered. Useful
	// for surfacing a representative reason in the health endpoint without
	// overwhelming the response.
	FirstError string
}

// SyncDBToConfig writes users from DB to config file if they are not already present.
// Call periodically when xray_api_addr is set, to persist API-created users to disk.
//
// Returns a SyncResult so the caller can surface per-user failures (e.g. a
// permission error on config.d that silently dropped 11 users on prod 2026-04-27).
func SyncDBToConfig(configDir, inboundTag string, users []struct {
	Username     string
	ClientConfig string
	Protocol     string
}, existingIdentities map[string]bool, filePerm os.FileMode) SyncResult {
	var res SyncResult
	if configDir == "" || inboundTag == "" {
		return res
	}

	for _, u := range users {
		username := strings.TrimSpace(u.Username)
		if username == "" {
			continue
		}
		if existingIdentities[username] {
			continue
		}
		if err := AddExistingClientToInbound(configDir, inboundTag, username, u.ClientConfig, filePerm); err != nil {
			log.Printf("WARN: sync user %s to config: %v", username, err)
			res.FailedUsers = append(res.FailedUsers, username)
			if res.FirstError == "" {
				res.FirstError = err.Error()
			}
			continue
		}
		res.Added++
		existingIdentities[username] = true
	}
	if res.Added > 0 {
		log.Printf("Synced %d users from DB to config", res.Added)
	}
	return res
}

// GetExistingIdentitiesInInbound returns the set of client identities (email/id) in the config for the given inbound.
func GetExistingIdentitiesInInbound(configDir, tag string) (map[string]bool, error) {
	parsed, err := ParseConfigDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	for _, inbounds := range parsed {
		for _, ib := range inbounds {
			if strings.TrimSpace(ib.Tag) == tag {
				idents := make(map[string]bool)
				for _, c := range ib.Clients {
					if c.Identity != "" {
						idents[c.Identity] = true
					}
				}
				return idents, nil
			}
		}
	}
	return nil, fmt.Errorf("inbound %s not found", tag)
}
