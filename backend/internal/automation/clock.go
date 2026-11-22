package automation

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// clock exists purely so tests can control time deterministically
// without sleeping - mirrors internal/health's and internal/scheduler's
// identical interface. Defaults to time.Now via realClock in production.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// newRuleID generates a random, URL-safe identifier for a new Rule.
// Mirrors internal/health's newCheckID and internal/scheduler's
// newTaskID.
func newRuleID() string {
	var buf [12]byte
	_, _ = rand.Read(buf[:])
	return "rule_" + hex.EncodeToString(buf[:])
}
