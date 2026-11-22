package health

import (
	"crypto/rand"
	"encoding/hex"
)

// newCheckID generates a random, URL-safe identifier for a new Check.
// Mirrors eventbus's newEventID in approach (crypto/rand, hex-encoded)
// with a short prefix so a check ID is visually distinguishable from an
// event ID or a process PID at a glance in logs/URLs.
func newCheckID() string {
	var buf [12]byte
	_, _ = rand.Read(buf[:])
	return "chk_" + hex.EncodeToString(buf[:])
}
