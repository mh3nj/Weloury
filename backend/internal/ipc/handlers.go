package ipc

import (
	"encoding/json"
	"net/http"
	"strings"

	"weloury/internal/eventbus"
	"weloury/pkg/fwp"
)

// handleRPC implements POST /rpc: decode a single request Envelope, run it
// through the Router, write back the response (or error) Envelope.
//
// Cancellation: the handler's context is r.Context(), which net/http
// cancels automatically if the client disconnects mid-request (see
// http.Request.Context's documentation). A long-running method that
// respects ctx therefore stops promptly if the frontend aborts the
// underlying fetch - this is Phase 1's cancellation mechanism; see the
// ipc package doc comment.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req fwp.Envelope
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, fwp.Envelope{
			V: fwp.ProtocolVersion, Type: fwp.TypeError,
			Error: fwp.NewError(fwp.ErrInvalidParams, "malformed request envelope: "+err.Error()),
		})
		return
	}

	resp := s.router.Dispatch(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}

// handleEvents implements GET /events?channel=a,b,c: a long-lived response
// streaming newline-delimited JSON Envelope(type=event) records for every
// bus event whose type matches one of the requested channels. An empty
// channel list subscribes to everything.
//
// The connection stays open, flushing each event as it is published,
// until the client disconnects (r.Context() is canceled) or the server
// shuts down. This is Weloury's subscription mechanism: subscribing is
// opening the connection, unsubscribing is closing it.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	var filter eventbus.Filter
	if raw := r.URL.Query().Get("channel"); raw != "" {
		types := make([]eventbus.Type, 0)
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				types = append(types, eventbus.Type(part))
			}
		}
		filter = eventbus.MatchTypes(types...)
	}

	ch, unsubscribe := s.bus.Subscribe(r.Context(), filter, eventbus.DefaultSubscriberBuffer)
	defer unsubscribe()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	var seq uint64
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			seq++
			env := fwp.Envelope{
				V:       fwp.ProtocolVersion,
				Type:    fwp.TypeEvent,
				Channel: string(event.Type),
				Seq:     seq,
				Payload: mustMarshal(event),
			}
			if err := enc.Encode(env); err != nil {
				s.logger.Debug("event stream write failed, client likely disconnected", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

// handleShutdown implements POST /shutdown: the desktop shell's signal to
// Weloury core to begin a graceful shutdown, used uniformly across platforms
// instead of OS-specific process signaling - see ARCHITECTURE.md.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
