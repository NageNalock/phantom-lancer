package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// EndpointConfig 是 Manager 在启动或热切时所需的端点配置。
type EndpointConfig struct {
	Addr              string // host:port
	TLSEnabled        bool
	TLSCertFile       string
	TLSKeyFile        string
	TLSOwnerUIDCheck  bool
	HSTSEnabled       bool
	HSTSMaxAgeSeconds int
}

// Endpoint 是 Manager 当前实际生效端点的只读快照，返回给前端用。
type Endpoint struct {
	Addr              string   `json:"addr"`
	TLSEnabled        bool     `json:"tlsEnabled"`
	Scheme            string   `json:"scheme"`
	CertFile          string   `json:"certFile,omitempty"`
	CertDNSNames      []string `json:"certDnsNames,omitempty"`
	CertNotBefore     string   `json:"certNotBefore,omitempty"`
	CertNotAfter      string   `json:"certNotAfter,omitempty"`
	CertReloadErr     string   `json:"certReloadErr,omitempty"`
	HSTSEnabled       bool     `json:"hstsEnabled"`
	HSTSMaxAgeSeconds int      `json:"hstsMaxAgeSeconds"`
}

// Manager owns the lifecycle of the HTTP/HTTPS server and listener so the
// endpoint can be hot-swapped at runtime without a process restart.
//
// All state mutations are guarded by a mutex.  The pre-bind + TLS pre-load
// step of a swap is intentionally performed OUTSIDE the lock so that a slow
// net.Listen or a slow cert-file I/O never blocks callers reading the current
// Endpoint(), and never races with process-wide Shutdown().
type Manager struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	addr     string

	tlsEnabled bool
	certFile   string
	keyFile    string
	reloader   *CertReloader

	hstsEnabled       bool
	hstsMaxAgeSeconds int

	handler http.Handler
	log     *slog.Logger
	serveWG sync.WaitGroup
}

// New constructs an HTTP-only Manager.  Preserved as a backward-compatible
// constructor – the new code should prefer NewWithEndpoint.
func New(initialAddr string, handler http.Handler, log *slog.Logger) *Manager {
	m, _, _ := newWithEndpointImpl(EndpointConfig{Addr: initialAddr}, handler, log, false)
	return m
}

// NewWithEndpoint is the recommended constructor.
//
//   - bootStrict=true:  if TLS is requested but cannot be initialised the
//     function returns an error and a nil Manager.
//   - bootStrict=false: if TLS is requested but cannot be initialised the
//     service falls back to HTTP on the same address.  The returned Endpoint
//     will reflect the actual effective mode (scheme="http").
func NewWithEndpoint(cfg EndpointConfig, handler http.Handler, log *slog.Logger, bootStrict bool) (*Manager, Endpoint, error) {
	return newWithEndpointImpl(cfg, handler, log, bootStrict)
}

// newWithEndpointImpl is the single internal constructor used by both New()
// and NewWithEndpoint().  It does NOT Start() – callers must Start().
func newWithEndpointImpl(cfg EndpointConfig, handler http.Handler, log *slog.Logger, bootStrict bool) (*Manager, Endpoint, error) {
	m := &Manager{
		handler:           handler,
		log:               log,
		addr:              cfg.Addr,
		hstsEnabled:       cfg.HSTSEnabled,
		hstsMaxAgeSeconds: cfg.HSTSMaxAgeSeconds,
	}
	ep := Endpoint{
		Addr:              cfg.Addr,
		Scheme:            "http",
		HSTSEnabled:       cfg.HSTSEnabled,
		HSTSMaxAgeSeconds: cfg.HSTSMaxAgeSeconds,
	}
	if !cfg.TLSEnabled {
		return m, ep, nil
	}
	cleanCert, cleanKey, _, err := ValidateTLSPaths(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSOwnerUIDCheck)
	if err != nil {
		if !bootStrict {
			log.Log(context.Background(), LevelCritical,
				"TLS_BOOT_FAILURE_HTTP_DOWNGRADE",
				slog.String("addr", cfg.Addr),
				slog.String("reason", truncateErr(err, 220)),
			)
			return m, ep, nil
		}
		return nil, ep, fmt.Errorf("tls boot failed (strict): %w", err)
	}
	reloader, rerr := NewCertReloader(cleanCert, cleanKey, log)
	if rerr != nil {
		if !bootStrict {
			log.Log(context.Background(), LevelCritical,
				"TLS_BOOT_FAILURE_HTTP_DOWNGRADE",
				slog.String("addr", cfg.Addr),
				slog.String("reason", truncateErr(rerr, 220)),
			)
			return m, ep, nil
		}
		return nil, ep, fmt.Errorf("tls boot failed (strict): %w", rerr)
	}
	m.tlsEnabled = true
	m.certFile = cleanCert
	m.keyFile = cleanKey
	m.reloader = reloader
	ep.TLSEnabled = true
	ep.Scheme = "https"
	ep.CertFile = cleanCert
	if ss := reloader.Snapshot(); ss.LastError == "" {
		ep.CertDNSNames = ss.DNSNames
		if !ss.NotBefore.IsZero() {
			ep.CertNotBefore = ss.NotBefore.UTC().Format(time.RFC3339)
		}
		if !ss.NotAfter.IsZero() {
			ep.CertNotAfter = ss.NotAfter.UTC().Format(time.RFC3339)
		}
	}
	return m, ep, nil
}

// LevelCritical is used instead of slog.LevelError for boot failures so that
// they are visible above normal warnings but do not force the process to exit
// (the caller decides on the exit path via bootStrict).
var LevelCritical = slog.Level(12)

// Start binds the initial listener and launches the serve goroutine.
// Returns an error if the configured address cannot be bound.
func (m *Manager) Start() error {
	ln, err := net.Listen("tcp", m.addr)
	if err != nil {
		return fmt.Errorf("bind address %s: %w", m.addr, err)
	}
	m.mu.Lock()
	m.listener = ln
	m.server = m.buildServerForConfig(EndpointConfig{
		Addr:              m.addr,
		TLSEnabled:        m.tlsEnabled,
		HSTSEnabled:       m.hstsEnabled,
		HSTSMaxAgeSeconds: m.hstsMaxAgeSeconds,
	})
	srv := m.server
	actualLn := net.Listener(ln)
	if m.tlsEnabled && m.reloader != nil {
		// 启动 reloader ticker，使用与 Shutdown 解耦的包级内部 context（Manager 自己管理取消）。
		// 这里用 context.Background()，由 Shutdown 显式 Close()。
		m.reloader.Start(context.Background())
		actualLn = tls.NewListener(ln, srv.TLSConfig)
	}
	m.listener = actualLn
	m.mu.Unlock()
	m.serveWG.Add(1)
	go m.launchServe(srv, actualLn)
	return nil
}

// Addr returns the currently-effective listen address (host:port).
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addr
}

// Endpoint returns a snapshot of the currently-effective endpoint suitable
// for serialising in API responses.
func (m *Manager) Endpoint() Endpoint {
	m.mu.Lock()
	addr := m.addr
	tlsOn := m.tlsEnabled
	certFile := m.certFile
	hsts := m.hstsEnabled
	hstsMax := m.hstsMaxAgeSeconds
	reloader := m.reloader
	m.mu.Unlock()

	ep := Endpoint{
		Addr:              addr,
		TLSEnabled:        tlsOn,
		Scheme:            "http",
		HSTSEnabled:       hsts,
		HSTSMaxAgeSeconds: hstsMax,
	}
	if tlsOn {
		ep.Scheme = "https"
		ep.CertFile = certFile
	}
	if reloader != nil {
		ss := reloader.Snapshot()
		ep.CertDNSNames = ss.DNSNames
		if !ss.NotBefore.IsZero() {
			ep.CertNotBefore = ss.NotBefore.UTC().Format(time.RFC3339)
		}
		if !ss.NotAfter.IsZero() {
			ep.CertNotAfter = ss.NotAfter.UTC().Format(time.RFC3339)
		}
		ep.CertReloadErr = ss.LastError
	}
	return ep
}

// SwapAddr is a backward-compatible wrapper around SwapEndpoint that keeps
// the current TLS state.  The new API is SwapEndpoint.
func (m *Manager) SwapAddr(newAddr string) error {
	m.mu.Lock()
	ep := EndpointConfig{
		Addr:              newAddr,
		TLSEnabled:        m.tlsEnabled,
		TLSCertFile:       m.certFile,
		TLSKeyFile:        m.keyFile,
		TLSOwnerUIDCheck:  true,
		HSTSEnabled:       m.hstsEnabled,
		HSTSMaxAgeSeconds: m.hstsMaxAgeSeconds,
	}
	m.mu.Unlock()
	_, err := m.SwapEndpoint(ep)
	return err
}

// SwapEndpoint atomically swaps to a new endpoint configuration.
//
// Guarantees:
//   - Pre-bind and TLS pre-load run OUTSIDE the lock so that slow syscalls
//     never block reads of Endpoint().
//   - Only the atomic state replacement runs under the lock.
//   - Cross-address bind failures leave the old listener untouched.
//   - Same-address TLS/cert changes must briefly close the current listening
//     socket before rebinding the same port; if that rebind fails, state is not
//     advanced and the failure is logged at critical level.
//   - Long-lived streams (SSE etc.) are force-dropped after a 2 s timeout.
func (m *Manager) SwapEndpoint(newCfg EndpointConfig) (Endpoint, error) {
	// ---- Step 1: validate -------------------------------------------------
	if _, _, err := net.SplitHostPort(newCfg.Addr); err != nil {
		return Endpoint{}, fmt.Errorf("invalid address format, expected host:port: %w", err)
	}
	_, portStr, _ := net.SplitHostPort(newCfg.Addr)
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("invalid port %q: must be 1-65535", portStr)
	}
	var cleanCert, cleanKey string
	if newCfg.TLSEnabled {
		var cerr error
		cleanCert, cleanKey, _, cerr = ValidateTLSPaths(newCfg.TLSCertFile, newCfg.TLSKeyFile, newCfg.TLSOwnerUIDCheck)
		if cerr != nil {
			return Endpoint{}, fmt.Errorf("tls validate: %w", cerr)
		}
	}

	// ---- Step 2: fast-path: same addr + same TLS transport → just reload ---
	curEp := m.Endpoint()
	if curEp.Addr == newCfg.Addr && curEp.TLSEnabled == newCfg.TLSEnabled {
		m.mu.Lock()
		sameTransport := m.certFile == cleanCert && m.keyFile == cleanKey
		m.mu.Unlock()
		if sameTransport {
			if m.reloader != nil {
				if rerr := m.reloader.LoadNow(); rerr != nil {
					// 证书文件损坏：保留当前 listener，错误向上返回（不做 eager swap）
					return curEp, fmt.Errorf("tls reload: %w", rerr)
				}
			}
			m.mu.Lock()
			m.hstsEnabled = newCfg.HSTSEnabled
			m.hstsMaxAgeSeconds = newCfg.HSTSMaxAgeSeconds
			m.mu.Unlock()
			return m.Endpoint(), nil
		}
	}
	if curEp.Addr == newCfg.Addr {
		return m.swapEndpointSameAddr(newCfg, cleanCert, cleanKey, curEp)
	}

	// ---- Step 3: pre-bind + optional TLS pre-load OUTSIDE the lock --------
	newLn, err := net.Listen("tcp", newCfg.Addr)
	if err != nil {
		return Endpoint{}, fmt.Errorf("cannot bind %s: %w", newCfg.Addr, err)
	}
	var newReloader *CertReloader
	if newCfg.TLSEnabled {
		r, rerr := NewCertReloader(cleanCert, cleanKey, m.log)
		if rerr != nil {
			_ = newLn.Close()
			return Endpoint{}, fmt.Errorf("tls preload: %w", rerr)
		}
		newReloader = r
	}

	// ---- Step 4: atomically swap state + drain old server -----------------
	m.mu.Lock()
	oldServer := m.server
	oldListener := m.listener
	oldAddr := m.addr
	oldReloader := m.reloader
	oldTLSEnabled := m.tlsEnabled

	// Install the new reloader (if any) BEFORE buildServerForConfig so the
	// helper sees the *new* m.reloader and attaches it as GetCertificate to
	// the new server's TLSConfig.  Without this ordering newServer.TLSConfig
	// ends up nil, causing tls.NewListener(nil config) → handshake crash.
	if newCfg.TLSEnabled {
		m.certFile = cleanCert
		m.keyFile = cleanKey
	} else {
		m.certFile = ""
		m.keyFile = ""
	}
	m.reloader = newReloader
	m.tlsEnabled = newCfg.TLSEnabled

	newCfgInternal := EndpointConfig{
		Addr:              newCfg.Addr,
		TLSEnabled:        newCfg.TLSEnabled,
		HSTSEnabled:       newCfg.HSTSEnabled,
		HSTSMaxAgeSeconds: newCfg.HSTSMaxAgeSeconds,
	}
	newServer := m.buildServerForConfig(newCfgInternal)
	actualLn := net.Listener(newLn)
	if newCfg.TLSEnabled && newReloader != nil {
		actualLn = tls.NewListener(newLn, newServer.TLSConfig)
		newReloader.Start(context.Background())
	}
	m.server = newServer
	m.listener = actualLn
	m.addr = newCfg.Addr
	m.hstsEnabled = newCfg.HSTSEnabled
	m.hstsMaxAgeSeconds = newCfg.HSTSMaxAgeSeconds
	m.mu.Unlock()

	m.log.Info("http server swapping endpoint",
		slog.String("old_addr", oldAddr),
		slog.String("new_addr", newCfg.Addr),
		slog.Bool("old_tls", oldTLSEnabled),
		slog.Bool("new_tls", newCfg.TLSEnabled),
	)

	// Launch new serve goroutine immediately so new connections are accepted
	// while the old server is draining.
	m.serveWG.Add(1)
	go m.launchServe(newServer, actualLn)

	// Drain the OLD server with a tight timeout.
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
	// Close any old reloader now that the old listener is gone.
	if oldReloader != nil && oldReloader != newReloader {
		oldReloader.Close()
	}
	m.log.Info("http server endpoint swap complete", "addr", newCfg.Addr)
	return m.Endpoint(), nil
}

// swapEndpointSameAddr changes TLS/HSTS/cert settings while keeping the same
// host:port. Unlike cross-address swaps, this cannot pre-bind a second
// listener: the current listener already owns the port. The implementation
// therefore preloads TLS first, closes only the listening socket to free the
// port, immediately re-binds it, then drains old accepted connections in the
// background so the request that triggered the change can still return.
func (m *Manager) swapEndpointSameAddr(newCfg EndpointConfig, cleanCert, cleanKey string, curEp Endpoint) (Endpoint, error) {
	var newReloader *CertReloader
	if newCfg.TLSEnabled {
		r, rerr := NewCertReloader(cleanCert, cleanKey, m.log)
		if rerr != nil {
			return curEp, fmt.Errorf("tls preload: %w", rerr)
		}
		newReloader = r
	}

	m.mu.Lock()
	if m.addr != newCfg.Addr {
		m.mu.Unlock()
		if newReloader != nil {
			newReloader.Close()
		}
		return m.SwapEndpoint(newCfg)
	}
	oldServer := m.server
	oldListener := m.listener
	oldReloader := m.reloader
	oldTLSEnabled := m.tlsEnabled

	if oldListener != nil {
		_ = oldListener.Close()
	}
	newLn, err := listenSameAddrWithRetry(newCfg.Addr, 750*time.Millisecond)
	if err != nil {
		m.mu.Unlock()
		if newReloader != nil {
			newReloader.Close()
		}
		m.log.Log(context.Background(), LevelCritical,
			"HTTP_SERVER_SAME_ADDR_REBIND_FAILED",
			slog.String("addr", newCfg.Addr),
			slog.String("error", err.Error()),
		)
		return curEp, fmt.Errorf("cannot rebind %s after closing current listener: %w", newCfg.Addr, err)
	}

	if newCfg.TLSEnabled {
		m.certFile = cleanCert
		m.keyFile = cleanKey
	} else {
		m.certFile = ""
		m.keyFile = ""
	}
	m.reloader = newReloader
	m.tlsEnabled = newCfg.TLSEnabled
	m.hstsEnabled = newCfg.HSTSEnabled
	m.hstsMaxAgeSeconds = newCfg.HSTSMaxAgeSeconds

	newServer := m.buildServerForConfig(EndpointConfig{
		Addr:              newCfg.Addr,
		TLSEnabled:        newCfg.TLSEnabled,
		HSTSEnabled:       newCfg.HSTSEnabled,
		HSTSMaxAgeSeconds: newCfg.HSTSMaxAgeSeconds,
	})
	actualLn := net.Listener(newLn)
	if newCfg.TLSEnabled && newReloader != nil {
		actualLn = tls.NewListener(newLn, newServer.TLSConfig)
		newReloader.Start(context.Background())
	}
	m.server = newServer
	m.listener = actualLn
	m.mu.Unlock()

	m.log.Info("http server swapping endpoint on same address",
		slog.String("addr", newCfg.Addr),
		slog.Bool("old_tls", oldTLSEnabled),
		slog.Bool("new_tls", newCfg.TLSEnabled),
	)

	m.serveWG.Add(1)
	go m.launchServe(newServer, actualLn)
	go m.drainOldServer(oldServer, nil, oldReloader)

	return m.Endpoint(), nil
}

func listenSameAddrWithRetry(addr string, timeout time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (m *Manager) drainOldServer(oldServer *http.Server, oldListener net.Listener, oldReloader *CertReloader) {
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
	if oldReloader != nil {
		oldReloader.Close()
	}
}

// Shutdown gracefully terminates the current server on process exit.
// Intended to be called from the main goroutine's signal/exit path.  It
// waits for serve goroutine(s) to return so the caller can be sure no
// goroutine is touching resources that are about to be closed (e.g. during
// syscall.Exec self-upgrade).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	srv := m.server
	ln := m.listener
	reloader := m.reloader
	m.server = nil
	m.listener = nil
	m.reloader = nil
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
		_ = ln.Close()
	}
	if reloader != nil {
		reloader.Close()
	}
	m.serveWG.Wait()
	return err
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// buildServerForConfig builds a base *http.Server for an endpoint config,
// optionally attaching the Manager's current reloader as GetCertificate.
func (m *Manager) buildServerForConfig(cfg EndpointConfig) *http.Server {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           m.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if cfg.TLSEnabled && m.reloader != nil {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// AEAD-only cipher suites (no RC4, no 3DES, no CBC, no non-PFS).
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
			NextProtos:       []string{"h2", "http/1.1"},
			GetCertificate:   m.reloader.GetCertificate,
		}
	}
	return srv
}

// launchServe starts srv.Serve in the background.  It MUST NOT be called
// while Manager.mu is held – Serve() blocks until the listener is closed.
func (m *Manager) launchServe(srv *http.Server, ln net.Listener) {
	defer m.serveWG.Done()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
		m.log.Error("http server failed", "error", err, "addr", srv.Addr)
	}
}
