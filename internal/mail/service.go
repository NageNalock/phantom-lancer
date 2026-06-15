package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/mail/configapply"
	"phantom-lancer/internal/mail/imapsync"
	"phantom-lancer/internal/mail/moxcli"
	"phantom-lancer/internal/mail/probes"
	"phantom-lancer/internal/storage"
)

// Service is the top-level coordinator for the Mail module.  It wires
// storage, the event hub, the Mox supervisor, probes and background workers
// together and exposes a narrow public surface that the HTTP API layer
// calls into.
//
// The zero value is not usable; construct with NewService.  Construction
// order: main() → NewService → Ensure(ctx) (schema & defaults) →
// StartBackground(ctx) (workers) → Close() on shutdown.
type Service struct {
	store   *storage.Store
	hub     *events.Hub
	dataDir string
	log     *slog.Logger

	// moxRoot is the directory under cfg.DataDir where all Mox-owned state
	// lives: binary, data, configs, markers, logs.
	moxRoot string

	mu              sync.Mutex
	running         bool
	backgroundStop  chan struct{}
	backgroundDone  chan struct{}
	phantomInstance string

	// lastProbeResults holds the most recent 9-layer probe result set
	// (padded to 9 by padProbeResults; layers 4–9 are Unknown until wired).
	lastProbeResults []probes.Result
	lastProbeOverall probes.Severity
	lastProbeAt      string
	// lastChangeAt is updated on every lifecycle / binary / settings mutation
	// so the UI dashboard can show "state changed N seconds ago".
	lastChangeAt string

	// --- Phase 3 wiring (filled in Ensure / StartBackground) ----------------
	drift *configapply.DriftDetector
	cli   *moxcli.Runner

	// --- Phase 7 wiring (IMAP sync) -----------------------------------------
	imapSyncManager *imapsync.Manager
}

// NewService builds a Service.  Call Ensure before StartBackground.
func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	root := filepath.Join(dataDir, "mail", "mox")
	svc := &Service{
		store:   store,
		hub:     hub,
		dataDir: dataDir,
		log:     logger.With("module", "mail"),
		moxRoot: root,
	}
	// Phase 7: IMAP sync manager.  Constructed eagerly (before Ensure) so
	// tests / Pause/Stop can be called even on a Service that never fully
	// initialised the Mox sidecar.
	svc.imapSyncManager = imapsync.NewManager(store, logger)
	return svc
}

// PhantomInstanceID returns the stable instance identifier used to mark
// the owner of any running Mox process (moxsupervisor marker file).  It
// is generated once during Ensure and never rotated – if the row is lost
// the operator must resolve any orphan manually.
func (s *Service) PhantomInstanceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phantomInstance
}

// Ensure runs idempotent on-boot bootstrap:
//
//  1. Creates the default mail_mox_settings row (id = 1) if absent.
//  2. Generates + persists a phantom_instance_id UUID-ish token if absent.
//  3. Creates the on-disk directories Mox will need (<data>/mail/mox/{bin,data,config,logs,run}).
//
// These are intentionally safe to re-run: no existing data is ever
// overwritten or removed.
func (s *Service) Ensure(ctx context.Context) error {
	if s.store == nil {
		return errors.New("mail.Service: store is nil")
	}
	if err := s.ensureDirs(ctx); err != nil {
		return fmt.Errorf("mail: ensure dirs: %w", err)
	}
	settings, err := s.store.MailEnsureSettings(ctx)
	if err != nil {
		return fmt.Errorf("mail: ensure settings row: %w", err)
	}
	// Phase 8B: register mail-module-specific redaction regexes into the
	// safelog tail pass.  Runs after settings so the operator-level
	// "danger_hard_delete_enabled" setting is never logged raw; runs before
	// mutex lock / phantom-instance setup so those early slog entries (which
	// may contain account passwords in their debug payloads) are already
	// filtered.
	registerMailRedactions(ctx, s.log)
	s.mu.Lock()
	defer s.mu.Unlock()

	// --- Phase 3: CLI runner + drift detector -----------------------------
	// MUST run before any early return (phantom-instance fast path, race-win
	// fast path) so s.cli / s.drift are always non-nil on success. Idempotent.
	if s.cli == nil {
		s.cli = &moxcli.Runner{
			BinaryPath: settings.MoxBinaryPath,
			ConfigPath: settings.MoxConfigPath,
			DataDir:    settings.MoxDataDir,
		}
		// If binary/config paths still empty at this point, pre-fill the defaults
		// from s.moxRoot so a stale settings row doesn't leave CLI runner invalid.
		if s.cli.BinaryPath == "" {
			s.cli.BinaryPath = filepath.Join(s.moxRoot, "bin", "mox")
		}
		if s.cli.ConfigPath == "" {
			s.cli.ConfigPath = filepath.Join(s.moxRoot, "config", "mox.conf")
		}
		if s.cli.DataDir == "" {
			s.cli.DataDir = filepath.Join(s.moxRoot, "data")
		}
		// Hash currently on disk (if any) so drift detector can warn when on-disk
		// config diverges from what we last-synced.
		initialHash, _ := configapply.HashFile(s.cli.ConfigPath)
		s.drift = configapply.NewDriftDetector(s.cli.ConfigPath, initialHash)
	}

	s.phantomInstance = settings.PhantomInstanceID
	if s.phantomInstance != "" {
		return nil
	}
	// Generate a 16-byte random token, hex-encoded, so it's stable,
	// URL-safe, and has enough entropy to disambiguate co-hosted
	// Phantom instances in a pinch.
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("mail: generate phantom_instance_id: %w", err)
	}
	tok := "phantom-" + hex.EncodeToString(buf)
	updated, err := s.store.MailUpdatePhantomInstanceID(ctx, tok)
	if err != nil {
		return fmt.Errorf("mail: persist phantom_instance_id: %w", err)
	}
	if updated != nil {
		s.phantomInstance = updated.PhantomInstanceID
		s.log.InfoContext(ctx, "mail: ensure complete",
			"phantom_instance_id", s.phantomInstance,
			"mox_root", s.moxRoot,
		)
		return nil
	}
	// Racing writer won; read back.
	row, err := s.store.MailEnsureSettings(ctx)
	if err != nil {
		return err
	}
	s.phantomInstance = row.PhantomInstanceID
	s.log.InfoContext(ctx, "mail: ensure complete",
		"phantom_instance_id", s.phantomInstance,
		"mox_root", s.moxRoot,
	)
	// If we've already been initialised (managed config_mode), consider the
	// disk hash "authoritative" until next Run().  On boot + every 10 minutes
	// a worker will Refresh() and flip the drifted flag on mismatch.
	return nil
}

// StartBackground launches the long-lived workers (drift detector, probe
// ticker, IMAP sync dispatcher, ACME renewal worker…).  Phase 2 wires:
//
//   - probe ticker (5s): L1/L2/L3
//   - adoption ticker (30s): re-attempt orphan Adopt() on boot
//   - supervisor-exit observer: publish runtime stopped/crashed events
//
// Each worker is implemented in runtime.go; startWorkers fans them out.
//
// StartBackground is idempotent: repeated calls are no-ops.
func (s *Service) StartBackground(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	stop := make(chan struct{})
	done := make(chan struct{})
	s.backgroundStop = stop
	s.backgroundDone = done
	s.mu.Unlock()

	s.startWorkers(ctx, stop, done)
	s.log.InfoContext(ctx, "mail: background workers started (phase 2)",
		"probe_fast", probeTickerFast, "probe_slow", probeTickerSlow,
		"adopt", adoptionTickerInterval)
}

// Close shuts the service down.  Idempotent; safe to call on a Service
// that was never started.
func (s *Service) Close() error {
	s.mu.Lock()
	running := s.running
	stop := s.backgroundStop
	done := s.backgroundDone
	s.running = false
	s.backgroundStop = nil
	s.backgroundDone = nil
	s.mu.Unlock()
	if running && stop != nil {
		close(stop)
	}
	if s.imapSyncManager != nil {
		s.imapSyncManager.StopAll()
	}
	if running && done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			return errors.New("mail: background workers did not stop within 3s")
		}
	}
	return nil
}

// MoxRoot returns the directory under cfg.DataDir where all Mox-owned
// state lives.  Exposed so sub-modules (supervisor, probes, certmanager)
// do not need to re-derive the path.
func (s *Service) MoxRoot() string { return s.moxRoot }

// IsRunning reports whether StartBackground has completed successfully.
// Used by HTTP handlers to distinguish "not yet initialised" from real
// errors during setup.
func (s *Service) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// touchLastChange records the wall time at which the last binary / lifecycle
// / settings mutation happened.  Exposed for the UI "last change" pill.
func (s *Service) touchLastChange() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastChangeAt = time.Now().UTC().Format(time.RFC3339)
}

// --- Phase 3 accessors (HTTP handlers read these) -------------------------

// Drifted reports whether the on-disk mox.conf diverged from the last
// Phantom-synced version.  Drifted=true triggers HTTP 409 on writes.
func (s *Service) Drifted() bool {
	if s.drift == nil {
		return false
	}
	return s.drift.Drifted()
}

// DriftDetector exposes the detector for the SSE worker / UI summary.
func (s *Service) DriftDetector() *configapply.DriftDetector { return s.drift }

// CLIRunner exposes the mox CLI runner for direct subcommand invocations.
func (s *Service) CLIRunner() *moxcli.Runner { return s.cli }
