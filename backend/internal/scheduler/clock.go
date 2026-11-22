package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// clock exists purely so tests can control time deterministically
// without sleeping - mirrors internal/health's identical clock
// interface. Defaults to time.Now via realClock in production.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// newTaskID generates a random, URL-safe identifier for a new Task.
// Mirrors internal/health's newCheckID.
func newTaskID() string {
	var buf [12]byte
	_, _ = rand.Read(buf[:])
	return "task_" + hex.EncodeToString(buf[:])
}
