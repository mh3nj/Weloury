module weloury

go 1.22

// modernc.org/sqlite is a CGO-free, pure-Go SQLite driver. It is
// noticeably slower under heavy concurrent write load than mattn/go-sqlite3
// (which wraps the real C library), but Weloury's write volume is modest
// (config, events, and — starting Phase 4 — batched metric aggregates,
// not raw per-sample writes). In exchange we get trivial cross-compilation
// to Windows/Linux/macOS without a C toolchain or CGO on every build
// machine, which matters a great deal for a cross-platform desktop tool.
// See ARCHITECTURE.md "Persistence" for the full rationale.
require modernc.org/sqlite v1.34.4

require golang.org/x/sys v0.22.0

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/gc/v3 v3.0.0-20240107210532-573471604cb6 // indirect
	modernc.org/libc v1.55.3 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.8.0 // indirect
	modernc.org/strutil v1.2.0 // indirect
	modernc.org/token v1.1.0 // indirect
)
