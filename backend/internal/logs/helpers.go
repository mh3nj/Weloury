package logs

import (
	"encoding/json"
	"strings"
)

func marshalEntry(e Entry) json.RawMessage {
	data, err := json.Marshal(e)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// levelRank gives slog's four standard level names a comparable integer
// ordering, matching log/slog's own numeric levels (Debug=-4, Info=0,
// Warn=4, Error=8) in relative order without importing slog just for
// this - Entry.Level is already a plain string (see its doc comment) so
// the query layer stays decoupled from log/slog's types.
var levelRank = map[string]int{
	"DEBUG": -4,
	"INFO":  0,
	"WARN":  4,
	"ERROR": 8,
}

// parseLevel resolves a level name to its rank. ok is false for an
// empty or unrecognized name, in which case callers should treat the
// filter as absent rather than matching nothing.
func parseLevel(s string) (rank int, ok bool) {
	r, found := levelRank[strings.ToUpper(strings.TrimSpace(s))]
	return r, found
}

func normalizeSearch(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func containsFold(haystack, lowerNeedle string) bool {
	return strings.Contains(strings.ToLower(haystack), lowerNeedle)
}
