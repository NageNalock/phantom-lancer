package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Manager owns the lifecycle of the HTTP server and listener so the listen
// address can be hot-swapped at runtime without a process restart.
//
// All state mutations are guarded by a mutex.  The pre-bind step of a swap is
// intentionally performed OUTSIDE the lock so that a slow net.Listen on the
// new address (e.g. while the kernel negotiates a port) never blocks callers
// from reading the current Addr(), and never races with process-wide
// Shutdown().
type Manager struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	addr     string
	handler  http.Handler
	log      *slog.Logger
	serveWG  sync.WaitGroup
}

// New constructs a Manager for the given initial address and handler.
// The server is NOT started – call Start() to bind the initial listener.
func New(initialAddr string, handler http.Handler, log *slog.Logger) *Manager {
	return &Manager{
		addr:    initialAddr,
		handler: handler,
		log:     log,
	}
}

// Start binds the initial listener and launches the serve goroutine.
// Returns an error if the configured address cannot be bound.
func (m *Manager) Start() error {
	ln, err := net.Listen("tcp", m.addr)
	if err != nil {
		return fmt.Errorf("bind address %s: %w", m.addr, err)
	}
	m.mu.Lock()
	m.listener = ln
	m.server = m.buildServer(m.addr)
	srv := m.server
	m.mu.Unlock()
	m.serveWG.Add(1)
	go m.launchServe(srv, ln)
	return nil
}

// Addr returns the currently-effective listen address (host:port).
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}

// SwapAddr performs an atomic hot-swap to a new listen address.
//
// Guarantees:
//   - The old listener stays active until the new one is PROVEN to bind
//     successfully.  If any step fails, the old listener is untouched.
//   - The response for in-flight requests that arrived on the old listener
//     is delivered before Shutdown() returns (standard-library behaviour).
//   - Long-lived streams (SSE etc.) are force-dropped after a short timeout.
func (m *Manager) SwapAddr(newAddr string) error {
	// ---- Step 1: validate -------------------------------------------------
	host, portStr, err := net.SplitHostPort(newAddr)
	if err != nil {
		return fmt.Errorf("invalid address format, expected host:port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %q: must be 1-65535", portStr)
	}
	_ = host // host already validated by SplitHostPort

	// ---- Step 2: pre-bind OUTSIDE the lock (the slow/blocking step) ------
	newLn, err := net.Listen("tcp", newAddr)
	if err != nil {
		return fmt.Errorf("cannot bind %s: %w", newAddr, err)
	}

	// ---- Step 3: atomically swap state under the lock --------------------
	m.mu.Lock()
	oldServer := m.server
	oldListener := m.listener
	oldAddr := m.addr

	newServer := m.buildServer(newAddr)
	m.server = newServer
	m.listener = newLn
	m.addr = newAddr
	m.mu.Unlock()

	m.log.Info("http server swapping address", "old_addr", oldAddr, "new_addr", newAddr)

	// Start the new serve goroutine immediately so new connections are
	// accepted while we are still draining the old server.
	m.serveWG.Add(1)
	go m.launchServe(newServer, newLn)

	// ---- Step 4: gracefully drain the OLD server -------------------------
	// Use a tight 2 s timeout (matching the self-update restart path) because
	// SSE streams are long-lived and will always run to deadline; the browser
	// client will auto-reconnect.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if oldServer != nil {
		if sErr := oldServer.Shutdown(shutdownCtx); sErr != nil {
			if errors.Is(sErr, context.DeadlineExceeded) {
				m.log.Warn("http server graceful shutdown timed out during swap; forcing close", "timeout", "2s")
			} else {
				m.log.Warn("http server shutdown during swap returned error", "error", sErr)
			}
			if cErr := oldServer.Close(); cErr != nil {
				m.log.Warn("http server force close during swap failed", "error", cErr)
			}
			if oldListener != nil {
				_ = oldListener.Close()
			}
		}
	}
	m.log.Info("http server address swap complete", "addr", newAddr)
	return nil
}

// Shutdown gracefully terminates the current server on process exit.
// Intended to be called from the main goroutine's signal/exit path.  It waits
// for the serve goroutine(s) to return so the caller can be sure no goroutine
// is touching resources that are about to be closed (e.g. during syscall.Exec
// self-upgrade).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	srv := m.server
	ln := m.listener
	m.server = nil
	m.listener = nil
	m.mu.Unlock()

	var err error
	if srv != nil {
		err = srv.Shutdown(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if cErr := srv.Close(); cErr != nil {
					m.log.Warn("http server force close on exit failed", "error", cErr)
				}
				if ln != nil {
					_ = ln.Close()
				}
			}
		}
	} else if ln != nil {
		// Safeguard: Close listener even if server was already nil.
		_ = ln.Close()
	}
	m.serveWG.Wait()
	return err
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func (m *Manager) buildServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           m.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// launchServe starts srv.Serve in the background.  It MUST NOT be called
// while Manager.mu is held – Serve() blocks until the listener is closed.
func (m *Manager) launchServe(srv *http.Server, ln net.Listener) {
	defer m.serveWG.Done()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		m.log.Error("http server failed", "error", err, "addr", srv.Addr)
	}
}
