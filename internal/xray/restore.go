// Package xray provides restore and DB-to-config sync for API-created users.

package xray

import (
	"fmt"
	"log"
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

// SyncDBToConfig writes users from DB to config file if they are not already present.
// Call periodically when xray_api_addr is set, to persist API-created users to disk.
func SyncDBToConfig(configDir, inboundTag string, users []struct {
	Username     string
	ClientConfig string
	Protocol     string
}, existingIdentities map[string]bool) error {
	if configDir == "" || inboundTag == "" {
		return nil
	}

	added := 0
	for _, u := range users {
		username := strings.TrimSpace(u.Username)
		if username == "" {
			continue
		}
		if existingIdentities[username] {
			continue
		}
		if err := AddExistingClientToInbound(configDir, inboundTag, username, u.ClientConfig); err != nil {
			log.Printf("WARN: sync user %s to config: %v", username, err)
			continue
		}
		added++
		existingIdentities[username] = true
	}
	if added > 0 {
		log.Printf("Synced %d users from DB to config", added)
	}
	return nil
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
