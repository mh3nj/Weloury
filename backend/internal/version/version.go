// Package version holds Weloury core's build-time version information.
package version

import "weloury/pkg/fwp"

// These are overridden via -ldflags at release build time - see
// scripts/build.sh. Their zero values below are what a plain `go build`
// without ldflags produces, which is expected and fine for local
// development.
var (
	Version   = "0.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Details is the JSON-serializable shape returned by the system.version
// FWP method.
type Details struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	BuildDate       string `json:"build_date"`
	ProtocolVersion int    `json:"protocol_version"`
}

// Info returns the current build's version details.
func Info() Details {
	return Details{
		Version:         Version,
		Commit:          Commit,
		BuildDate:       BuildDate,
		ProtocolVersion: fwp.ProtocolVersion,
	}
}
