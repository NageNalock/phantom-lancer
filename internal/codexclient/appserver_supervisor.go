package codexclient

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// App-server runtime states surfaced to the UI.
const (
	RuntimeStopped  = "stopped"
	RuntimeStarting = "starting"
	RuntimeRunning  = "running"
	RuntimeFailed   = "failed"
	RuntimeDegraded = "degraded"
)

// AppServerStatus is the snapshot returned to the API / UI.
type AppServerStatus struct {
	State         string `json:"state"`
	PID           int    `json:"pid,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	LastProbeAt   string `json:"lastProbeAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	Enabled       bool   `json:"enabled"`
}

// NotificationHandler consumes app-server notifications. Returning is expected to
// be cheap; heavy work should be dispatched asynchronously by the handler.
type NotificationHandler func(Notification)

// RequestHandler consumes server-initiated app-server requests (for example
// approval requests). Implementations must reply via AppServerClient.Respond.
type RequestHandler func(ServerRequest)

// AppServerSupervisor manages a single managed `codex app-server` runtime: one
// owner-triggered start, a periodic liveness probe and a clean stop. It never
// auto-restarts in the MVP.
type AppServerSupervisor struct {
	detector  *Detector
	settings  func() Settings
	log       *slog.Logger
	onNotify  NotificationHandler
	onRequest RequestHandler
	onFailure func(string)

	mu          sync.Mutex
	client      *AppServerClient
	state       string
	startedAt   time.Time
	lastProbeAt time.Time
	lastError   string
	stopProbe   chan struct{}
}

func (s *AppServerSupervisor) SetFailureHandler(handler func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onFailure = handler
}

func NewAppServerSupervisor(detector *Detector, settings func() Settings, onNotify NotificationHandler, onRequest RequestHandler, logger *slog.Logger) *AppServerSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &AppServerSupervisor{
		detector:  detector,
		settings:  settings,
		log:       logger,
		onNotify:  onNotify,
		onRequest: onRequest,
		state:     RuntimeStopped,
	}
}

// StartProbeLoop starts the periodic liveness probe. The loop only updates
// runtime state; it does not write a log or audit entry for healthy probes.
func (s *AppServerSupervisor) StartProbeLoop(ctx context.Context) {
	go func() {
		for {
			interval := time.Duration(s.settings().AppServerProbeSeconds) * time.Second
			if interval < 5*time.Second {
				interval = 20 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				s.probe(ctx)
			}
		}
	}()
}

func (s *AppServerSupervisor) probe(ctx context.Context) {
	s.mu.Lock()
	client := s.client
	state := s.state
	s.mu.Unlock()
	if client == nil || state != RuntimeRunning {
		return
	}
	select {
	case <-client.Done():
		s.setFailed("app-server process exited")
		return
	default:
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// thread/loaded/list is a cheap real method that validates the transport and
	// JSON-RPC plumbing without side effects.
	_, err := client.Call(cctx, "thread/loaded/list", map[string]any{})
	s.mu.Lock()
	s.lastProbeAt = time.Now()
	s.mu.Unlock()
	if err != nil {
		var rpcErr jsonRPCError
		if errors.As(err, &rpcErr) {
			// A protocol-level error still means the transport is alive.
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		s.setFailed(Redact("probe failed: "+err.Error(), 160))
	}
}

// Start performs the one-click start sequence: verify the CLI is installed and
// app-server capable, launch the child process and complete the handshake.
func (s *AppServerSupervisor) Start(ctx context.Context) (AppServerStatus, error) {
	s.mu.Lock()
	if s.state == RuntimeRunning || s.state == RuntimeStarting {
		status := s.statusLocked()
		s.mu.Unlock()
		return status, nil
	}
	s.state = RuntimeStarting
	s.lastError = ""
	s.mu.Unlock()

	settings := s.settings()
	if !settings.AppServerEnabled {
		s.setFailed("app-server disabled in settings")
		return s.Status(), errors.New("app-server disabled in settings")
	}
	detection := s.detector.Detect(ctx)
	if detection.BinaryPath == "" {
		s.setFailed("codex binary not found")
		return s.Status(), errors.New("codex binary not found")
	}
	if !detection.Capabilities.AppServer {
		s.setFailed("codex app-server not supported by this CLI")
		return s.Status(), errors.New("codex app-server not supported")
	}
	if detection.Capabilities.AuthState == "logged_out" {
		s.setFailed("codex CLI is not authenticated")
		return s.Status(), errors.New("codex CLI is not authenticated")
	}
	if detection.Capabilities.SandboxState == "unavailable" {
		s.setFailed("codex CLI sandbox is unavailable")
		return s.Status(), errors.New("codex CLI sandbox is unavailable")
	}

	client, err := StartAppServer(ctx, detection.BinaryPath, settings.CodexHome)
	if err != nil {
		s.setFailed(Redact("start failed: "+err.Error(), 160))
		return s.Status(), err
	}
	s.attachClientPumps(client)
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		s.setFailed(Redact("initialize failed: "+err.Error(), 160))
		return s.Status(), err
	}

	s.mu.Lock()
	s.client = client
	s.state = RuntimeRunning
	s.startedAt = time.Now()
	s.lastProbeAt = time.Now()
	s.lastError = ""
	s.mu.Unlock()

	return s.Status(), nil
}

func (s *AppServerSupervisor) attachClientPumps(client *AppServerClient) {
	go func() {
		for notif := range client.Notifications() {
			if s.onNotify != nil {
				s.onNotify(notif)
			}
		}
	}()
	go func() {
		for req := range client.Requests() {
			if s.onRequest != nil {
				s.onRequest(req)
			}
		}
	}()
	go func() {
		<-client.Done()
		s.setFailed("app-server process exited")
	}()
}

// Stop terminates the managed runtime.
func (s *AppServerSupervisor) Stop(ctx context.Context) AppServerStatus {
	s.mu.Lock()
	client := s.client
	s.client = nil
	s.state = RuntimeStopped
	s.startedAt = time.Time{}
	s.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	return s.Status()
}

// Restart stops then starts the runtime.
func (s *AppServerSupervisor) Restart(ctx context.Context) (AppServerStatus, error) {
	s.Stop(ctx)
	return s.Start(ctx)
}

// Client returns the active client, or nil when not running.
func (s *AppServerSupervisor) Client() *AppServerClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != RuntimeRunning {
		return nil
	}
	return s.client
}

func (s *AppServerSupervisor) Status() AppServerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *AppServerSupervisor) statusLocked() AppServerStatus {
	status := AppServerStatus{
		State:     s.state,
		LastError: s.lastError,
		Enabled:   s.settings().AppServerEnabled,
	}
	if s.client != nil {
		status.PID = s.client.PID()
	}
	if !s.startedAt.IsZero() {
		status.StartedAt = s.startedAt.UTC().Format(time.RFC3339Nano)
		if s.state == RuntimeRunning {
			status.UptimeSeconds = int64(time.Since(s.startedAt).Seconds())
		}
	}
	if !s.lastProbeAt.IsZero() {
		status.LastProbeAt = s.lastProbeAt.UTC().Format(time.RFC3339Nano)
	}
	return status
}

func (s *AppServerSupervisor) setFailed(message string) {
	s.mu.Lock()
	notify := s.state != RuntimeFailed || s.lastError != message
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
	if s.state != RuntimeStopped {
		s.state = RuntimeFailed
	}
	s.lastError = message
	s.startedAt = time.Time{}
	if s.log != nil {
		s.log.Warn("codex app-server failure", "summary", message)
	}
	handler := s.onFailure
	s.mu.Unlock()
	if notify && handler != nil {
		handler(message)
	}
}
