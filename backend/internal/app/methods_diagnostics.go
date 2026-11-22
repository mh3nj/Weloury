package app

import (
	"context"
	"encoding/json"

	"weloury/internal/diagnostics"
	"weloury/internal/ipc"
)

// registerDiagnosticsMethods wires internal/diagnostics onto r. One
// method: generating a report is the entire surface area - see
// internal/diagnostics' package doc comment on why there's no
// persistence or configuration to expose here. Export/download of the
// report is a frontend concern (format and trigger a browser download),
// the same way internal/logs' export works - see LogsPage.tsx.
func registerDiagnosticsMethods(r *ipc.Router, generator *diagnostics.Generator) {
	r.Handle("diagnostics.run", diagnosticsRunHandler(generator))
}

func diagnosticsRunHandler(generator *diagnostics.Generator) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		return generator.Generate(ctx), nil
	}
}
