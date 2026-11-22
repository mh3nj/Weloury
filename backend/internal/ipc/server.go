package ipc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"weloury/internal/eventbus"
)

// Handshake is printed as a single line of JSON to stdout the moment the
// listener is ready, so the parent process (the Tauri shell in normal
// operation; a human during development) can discover how to reach Weloury
// Core without a fixed, hardcoded port or token.
type Handshake struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// Server is Weloury core's local IPC listener.
type Server struct {
	router *Router
	bus    *eventbus.Bus
	logger *slog.Logger
	token  string

	httpServer *http.Server
	listener   net.Listener

	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

// Config configures a Server.
type Config struct {
	// Host must be a loopback address (or "localhost"). NewServer refuses
	// to bind anything else - Weloury never exposes local IPC on a
	// non-loopback interface.
	Host   string
	Port   int // 0 = OS-assigned ephemeral port
	Router *Router
	Bus    *eventbus.Bus
	Logger *slog.Logger
}

// NewServer constructs a Server bound to an ephemeral (or explicit) local
// port. A fresh bearer token is generated every process start and never
// persisted to disk; its only purpose is ensuring that only whoever spawned
// Weloury core (or a developer who read its stdout) can talk to it, since any
// local process can otherwise dial a loopback port.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if !isLoopback(cfg.Host) {
		return nil, fmt.Errorf("ipc: refusing to bind non-loopback host %q", cfg.Host)
	}
	if cfg.Router == nil {
		return nil, fmt.Errorf("ipc: Router is required")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("ipc: Logger is required")
	}
	if cfg.Bus == nil {
		return nil, fmt.Errorf("ipc: Bus is required")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("listen on %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	token, err := randomToken()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	s := &Server{
		router:     cfg.Router,
		bus:        cfg.Bus,
		logger:     cfg.Logger,
		token:      token,
		listener:   ln,
		shutdownCh: make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.withAuth(s.handleRPC))
	mux.HandleFunc("/events", s.withAuth(s.handleEvents))
	mux.HandleFunc("/shutdown", s.withAuth(s.handleShutdown))

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s, nil
}

// Addr returns the actual bound address (useful when Port was 0).
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Token returns the bearer token clients must present.
func (s *Server) Token() string { return s.token }

// ShutdownRequested is closed when a client calls POST /shutdown. The
// application layer selects on this alongside OS signals to trigger
// graceful shutdown uniformly across platforms - see ARCHITECTURE.md.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownCh }

// PrintHandshake writes the Handshake line to w (in practice os.Stdout).
func (s *Server) PrintHandshake(w io.Writer, pid int) error {
	port := 0
	if tcpAddr, ok := s.listener.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	}
	data, err := json.Marshal(Handshake{Port: port, Token: s.token, PID: pid})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// Serve blocks, accepting connections until ctx is canceled, returning
// once the server has finished a graceful shutdown. It mirrors net/http's
// convention: a nil error only after a clean shutdown via ctx or
// /shutdown; any other return is the underlying Serve error.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.httpServer.Serve(s.listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS is required, not optional: the webview serves the UI from its
		// own origin (e.g. http://tauri.localhost on Windows, tauri://localhost
		// on macOS), which is cross-origin to this http://127.0.0.1:<port>
		// listener - so the browser runs the fetch's CORS machinery on every
		// request. Without an Access-Control-Allow-Origin on the response it
		// blocks the body outright ("Failed to fetch" in the UI), and without
		// an OPTIONS preflight path the preflight itself dies here on the 401.
		// The token still does all the auth work (the wildcard is safe: this
		// endpoint requires the Authorization header, so other sites a user
		// visits cannot do anything without it - a browser sends that header
		// only in non-credentialed CORS mode, where the wildcard applies, and
		// only someone who already knows the token can set it).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		presented := extractBearerToken(r)
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func extractBearerToken(r *http.Request) string {
	const prefix = "Bearer "
	if auth := r.Header.Get("Authorization"); len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return r.URL.Query().Get("token")
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
