package app

import (
	"context"
	"encoding/json"
	"time"

	"weloury/internal/eventbus"
)

// heartbeatInterval is how often the demo heartbeat event fires. It exists
// in Phase 1 purely to give the desktop shell something real to subscribe
// to end-to-end (event bus -> IPC event stream -> frontend) before any
// actual monitoring subsystem exists. Later phases replace/supplement it
// with real metric and lifecycle events at their own natural cadences -
// see eventbus.TypeHeartbeat's doc comment.
const heartbeatInterval = 5 * time.Second

func (a *App) runHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	seq := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			payload, _ := json.Marshal(map[string]int{"sequence": seq})
			a.bus.Publish(eventbus.Event{
				Type:     eventbus.TypeHeartbeat,
				Source:   "app.heartbeat",
				Severity: eventbus.SeverityInfo,
				Payload:  payload,
			})
		}
	}
}
