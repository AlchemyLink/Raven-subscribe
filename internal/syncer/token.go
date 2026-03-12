package syncer

import (
	"crypto/rand"
	"encoding/hex"
)

// generateToken creates a cryptographically random 32-byte hex token
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
