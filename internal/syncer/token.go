package syncer

import (
	"crypto/rand"
	"encoding/hex"
)

// generateToken creates a cryptographically random 32-byte hex token
func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
