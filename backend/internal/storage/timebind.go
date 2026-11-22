package storage

import "time"

// sqliteTimeLayout is a fixed-width RFC 3339-style layout in UTC. Every
// time.Time Weloury binds into SQLite goes through sqliteTime (and
// nullableTime, for nullable columns) so the stored text:
//
//   - is parseable by the modernc.org/sqlite driver's read path
//     (parseTimeFormats) back into a real time.Time, no matter which
//     platform Weloury runs on;
//   - sorts correctly as bare text, because every stored string is
//     fixed-width and zero-padded in UTC ("2026-09-29 21:04:05.123" <
//     "2026-09-29 21:04:05.456" lexicographically), which keeps the raw
//     `ORDER BY timestamp` / `timestamp < ?` comparisons in events.go and
//     metrics.go meaningful;
//   - carries no local-zone or wall-clock ambiguity: the zone offset is
//     dropped (everything is UTC) rather than round-tripped.
//
// Why this exists: binding a time.Time directly makes the driver store
// t.String() ("2006-01-02 15:04:05.999999999 -0700 MST"). On Linux in UTC
// that string happens to round-trip through the driver's parseTimeFormats.
// On Windows it does not: the MST component expands to the full
// Windows-tz zone name (e.g. "W. Europe Standard Time"), which none of the
// driver's parse layouts accept, so the driver hands the raw string to
// database/sql and every Scan into *time.Time / sql.NullTime fails with
// "unsupported Scan, storing driver.Value type string into type
// *time.Time". Time formats are also a data-compatibility concern in
// their own right: a database written on one machine must be readable by
// the same binary everywhere, so the stored text format must not depend
// on the writer's platform or local zone at all.
//
// The driver's own `_time_format=sqlite` DSN parameter (format 7 of
// https://www.sqlite.org/lang_datefunc.html) would fix the round-trip, but
// its output is not fixed-width (fractional seconds are variable-length)
// and it keeps the local offset, which leaves the lexicographic-ordering
// property above unreliable. Normalizing at the binding layer keeps that
// property, keeps every repository call site dead simple (wrap the value,
// don't think about it again), and keeps the format entirely under Weloury's
// control - one more reason this codebase prefers owning a small, explicit
// dependency over wiring a library knob whose exact output shape can drift
// with driver upgrades.
//
// The layout uses a space rather than "T" as the date/time separator, in
// line with SQLite's own time-string conventions (format 7); the driver's
// parseTimeFormats accept it, and so does anything reading the database
// directly (e.g. a diagnostics tool inspecting weloury.db).
const sqliteTimeLayout = "2006-01-02 15:04:05.999999999"

// sqliteTime converts t to the canonical text form Weloury stores in SQLite.
// The value is converted to UTC first so the stored wall clock is
// unambiguous and comparable across machines and DST transitions. A zero
// time.Time is converted like any other (yielding "0001-01-01 ...") -
// nullable columns must use nullableTime instead, which maps zero to SQL
// NULL; this function's only job is the text format.
func sqliteTime(t time.Time) string {
	return t.UTC().Format(sqliteTimeLayout)
}
