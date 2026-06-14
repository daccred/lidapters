package blend

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
