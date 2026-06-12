package mail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/mail/moxbinary"
	"phantom-lancer/internal/mail/moxsupervisor"
	"phantom-lancer/internal/mail/probes"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Runtime state model (exported so HTTP handlers can serialise it directly).
// -----------------------------------------------------------------------------

// RuntimeStatus is the aggregate "everything we know about Mox right now"
// blob returned by Service.RuntimeStatus() and served by the HTTP /status
// endpoint.  The struct mirrors the layout rendered by MailOverviewTab:
// desired/observed side-by-side, supervisor detail, and 9 probe dots.
type RuntimeStatus struct {
	// Settings / desired state.
	ConfigMode   string `json:"config_mode"`
	DesiredState string `json:"desired_state"`
	ImportMode   bool   `json:"import_mode"`

	// Supervisor / observed process state.
	Observed          string        `json:"observed_state"`
	PID               int           `json:"pid"`
	BootID            string        `json:"boot_id"`
	CrashLoopState    string        `json:"crash_loop_state"`
	ConsecutiveFails  int           `json:"consecutive_failures"`
	BackoffRemaining  time.Duration `json:"backoff_remaining_ms"`
	Uptime            time.Duration `json:"uptime_ms"`

	// Binary detection summary.
	BinaryControlled *moxbinary.BinaryInfo `json:"binary_controlled,omitempty"`
	BinaryPATH       *moxbinary.BinaryInfo `json:"binary_path,omitempty"`
	BinarySelected   *moxbinary.BinaryInfo `json:"binary_selected,omitempty"`

	// Probes (ordered by layer, 1..9; unfilled layers remain with StateUnknown so
	// the UI can render 9 dots stably).
	Probes  []probes.Result `json:"probes"`
	Overall probes.Severity `json:"overall"`

	// Counts (placeholder in Phase 2 – filled by Phase 3+).
	DomainCount  int `json:"domain_count"`
	AccountCount int `json:"account_count"`

	// Last-touched timestamps.
	LastProbeAt  string `json:"last_probe_at"`
	LastChangeAt string `json:"last_change_at"`
}

// -----------------------------------------------------------------------------
// Binary action request/response types.  These are small, stable value types
// used directly by both the Service layer and the JSON HTTP handlers.
// -----------------------------------------------------------------------------

// BinaryDetectRequest configures a moxbinary.Detect call.  All fields are
// optional; sensible defaults are applied by Service.BinaryDetect.
type BinaryDetectRequest struct {
	HintPath       string        `json:"hint_path"`
	ExtraPATH      []string      `json:"extra_path"`
	VersionTimeout time.Duration `json:"version_timeout_ms"`
	SkipPATH       bool          `json:"skip_path"`
}

// BinaryDetectResponse wraps DetectedResult with a "picked" recommendation
// so the UI knows which card to highlight.
type BinaryDetectResponse struct {
	Controlled *moxbinary.BinaryInfo `json:"controlled,omitempty"`
	PATH       *moxbinary.BinaryInfo `json:"path,omitempty"`
	Hint       *moxbinary.BinaryInfo `json:"hint,omitempty"`
	Selected   *moxbinary.BinaryInfo `json:"selected,omitempty"`
}

// BinaryDownloadRequest requests a specific pinned version from the
// approved-release whitelist.
type BinaryDownloadRequest struct {
	Version       string        `json:"version"`        // e.g. "0.8.11"; leading v is stripped
	OverrideURL   string        `json:"override_url"`   // optional, must still pass whitelist
	DestDir       string        `json:"dest_dir"`       // optional; defaults to <moxRoot>/bin
	SizeMaxBytes  int64         `json:"size_max_bytes"` // optional; default 200 MiB
	ReportPercent bool          `json:"report_percent"`
	Progress      chan<- int    `json:"-"` // 0..100, closed on completion
}

// BinaryDownloadResponse carries the download outcome.
type BinaryDownloadResponse struct {
	TempPath       string `json:"temp_path"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	ExpectedSHA256 string `json:"expected_sha256"`
	Version        string `json:"version"`
}

// BinaryInstallRequest installs a previously-downloaded (or caller-provided)
// tempfile into the controlled directory as the active mox binary.
type BinaryInstallRequest struct {
	// Src is the absolute path of a pre-downloaded mox binary.  If empty, the
	// caller can request an implicit install-from-download by also setting
	// Version.
	Src            string `json:"src"`
	Version        string `json:"version"`
	ChecksumSHA256 string `json:"checksum_sha256"` // optional; if set, verified before copy
	Force          bool   `json:"force"`           // replace even when binaryInUse reports true
}

// BinaryInstallResponse wraps moxbinary.InstallResult.
type BinaryInstallResponse = moxbinary.InstallResult

// BinaryUninstallRequest controls the uninstall flow.  Either Force=true or
// the caller must have already stopped Mox.
type BinaryUninstallRequest struct {
	Force bool `json:"force"`
}

// BinaryUninstallResponse carries the outcome of an uninstall.
type BinaryUninstallResponse struct {
	RemovedBinary  bool   `json:"removed_binary"`
	RemovedSidecar bool   `json:"removed_sidecar"`
	BackupsRemoved int    `json:"backups_removed"`
	UninstalledVer string `json:"uninstalled_version"`
	ControlledDir  string `json:"controlled_dir"`
}

// -----------------------------------------------------------------------------
// Runtime lifecycle request/response types.
// -----------------------------------------------------------------------------

type LifecycleRequest struct {
	// Reason is stored in the audit payload; optional.
	Reason string `json:"reason"`
	// BlockMS, if >0, makes the HTTP handler wait up to this long for the
	// supervisor to reach the target state before returning.  Default 0
	// ("fire and forget" – handler returns immediately).
	BlockMS int `json:"block_ms"`
}

type LifecycleResponse struct {
	Requested   string `json:"requested"`    // "start" / "stop" / "restart"
	Accepted    bool   `json:"accepted"`
	ObservedNow string `json:"observed_now"` // immediate State() snapshot
	Message     string `json:"message,omitempty"`
}

// RuntimeProbeRequest specifies which probe layers to run.  Default (empty)
// runs all currently implemented layers.
type RuntimeProbeRequest struct {
	Layers []int `json:"layers"` // if non-empty, only run these layer numbers
}

type RuntimeProbeResponse struct {
	Results []probes.Result `json:"results"`
	Overall probes.Severity `json:"overall"`
	At      string          `json:"at"`
}

// SetupInitializeRequest initialises a brand-new managed install by writing
// the default mox.conf template and recording the binary path + ports in
// the settings row.  Returns OK once the directories + placeholder config
// are in place (Mox is NOT started automatically).
type SetupInitializeRequest struct {
	AdminEmail            string `json:"admin_email"`
	Hostname              string `json:"hostname"`
	WebAPIAddr            string `json:"webapi_addr"`              // e.g. "127.0.0.1:10445"
	WebmailAddr           string `json:"webmail_addr"`             // e.g. "127.0.0.1:10444"
	UseControlledBinary   bool   `json:"use_controlled_binary"`    // if true, force <moxRoot>/bin/mox
	OverwriteExistingConf bool   `json:"overwrite_existing_conf"`  // dangerous; guarded by DangerConfirm
}

type SetupInitializeResponse struct {
	ConfigPath       string   `json:"config_path"`
	DataDir          string   `json:"data_dir"`
	BinaryPath       string   `json:"binary_path"`
	PlaceholderNote  string   `json:"placeholder_note"`
	NextSteps        []string `json:"next_steps"`
}

// SetupImportRequest records an existing external Mox install as "import
// mode (read only)".  The service points its supervisor at the provided
// paths but disables Start/Stop/Apply.  This is the operator-first path
// for people who already run Mox and just want Phantom dashboards.
type SetupImportRequest struct {
	BinaryPath string `json:"binary_path"`
	ConfigPath string `json:"config_path"`
	DataDir    string `json:"data_dir"`
	Label      string `json:"label"` // e.g. "production-mox-01"
}

type SetupImportResponse struct {
	Imported       bool     `json:"imported"`
	Label          string   `json:"label"`
	PreflightNotes []string `json:"preflight_notes"`
}

// PreflightPortsRequest runs the moxsupervisor port checker against the
// configured ports and returns a per-port breakdown.  Used by the Setup
// tab "port checklist" panel.
type PreflightPortsRequest struct{}
type PreflightPortsResponse struct {
	Ports  []PortCheck `json:"ports"`
	AllOK  bool        `json:"all_ok"`
}
type PortCheck struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Host     string `json:"host"`
	Free     bool   `json:"free"`
	Conflict string `json:"conflict,omitempty"`
}

// -----------------------------------------------------------------------------
// Service wiring: moxsupervisor + probes constructors + lifecycle.
// -----------------------------------------------------------------------------

// supervisor returns the lazily-initialised Supervisor.  On every call the
// supervisor's Binary/Config/Data/Ports are refreshed from the latest
// settings row so callers always see a current picture.
func (s *Service) supervisor(ctx context.Context) (*moxsupervisor.Supervisor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("mail: load settings: %w", err)
	}
	svc := s.buildSupervisorLocked(settings)
	return svc, nil
}

func (s *Service) buildSupervisorLocked(settings *storage.MailMoxSettings) *moxsupervisor.Supervisor {
	binPath := settings.MoxBinaryPath
	if binPath == "" {
		binPath = filepath.Join(s.moxRoot, "bin", "mox")
	}
	dataDir := settings.MoxDataDir
	if dataDir == "" {
		dataDir = filepath.Join(s.moxRoot, "data")
	}
	cfgPath := settings.MoxConfigPath
	if cfgPath == "" {
		cfgPath = filepath.Join(s.moxRoot, "config", "mox.conf")
	}
	ports := moxsupervisor.Ports{
		SMTP:        settings.SMTPPort,
		Submission:  settings.SMTPSubmissionPort,
		SMTPS:       settings.SMTPSPort,
		IMAP:        settings.IMAPPort,
		IMAPS:       settings.IMAPSPort,
		Webmail:     portOrDefault(settings.WebmailAddr, 10444),
		WebAPILocal: portOrDefault(settings.WebAPIAddr, 10445),
	}
	// The supervisor is cheap to build (no OS resources), so we always
	// build a fresh one rather than cache.  This avoids stale-pointer bugs
	// when the operator edits settings without restarting Phantom.
	return moxsupervisor.New(
		s.moxRoot,
		binPath,
		dataDir,
		cfgPath,
		ports,
		s.phantomInstance,
		s.log,
	)
}

// portOrDefault extracts the numeric port from a "host:port" string.
// Used as a tiny helper to build the moxsupervisor.Ports struct from the
// textual hostname:port settings values.
func portOrDefault(hostport string, defaultPort int) int {
	if hostport == "" {
		return defaultPort
	}
	_, sport, err := splitHostPort(hostport)
	if err != nil || sport == "" {
		return defaultPort
	}
	n := 0
	for _, c := range []byte(sport) {
		if c < '0' || c > '9' {
			return defaultPort
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 || n > 65535 {
		return defaultPort
	}
	return n
}

func splitHostPort(s string) (host, port string, err error) {
	// Avoid importing net for this tiny call site; the callers only need
	// the port part and we can fall back on the default if parsing fails.
	lastColon := strings.LastIndexByte(s, ':')
	if lastColon < 0 {
		return s, "", nil
	}
	// IPv6 literal: [::1]:80
	if strings.HasPrefix(s, "[") {
		closeIdx := strings.IndexByte(s, ']')
		if closeIdx < 0 {
			return s, "", fmt.Errorf("malformed hostport %q", s)
		}
		return s[1:closeIdx], s[closeIdx+2:], nil
	}
	return s[:lastColon], s[lastColon+1:], nil
}

// controlledBinDir returns the directory where Phantom installs its own
// copy of mox.  It's never empty.
func (s *Service) controlledBinDir() string { return filepath.Join(s.moxRoot, "bin") }

// -----------------------------------------------------------------------------
// Background workers: probe ticker + adoption ticker + process-exit observer.
// -----------------------------------------------------------------------------

// probeTickerFast is how often the L1/L2/L3 probes run.  These three are
// cheap (<50ms each, capped at 3 concurrent) so a 5-second cadence is fine.
const probeTickerFast = 5 * time.Second

// probeTickerSlow is reserved for future expensive probes (L4 SMTP/L5 IMAP
// TLS handshakes, L6 DNS, L7 delivery stats, L9 DNSBL reputation).  15s
// keeps things observable without hammering real network endpoints.
const probeTickerSlow = 15 * time.Second

// adoptionTickerInterval controls how often we re-check for an orphan Mox
// process from a previous Phantom boot (Adopt() via marker file).  30s is
// a good balance – we don't want to poll /proc too often, but we do want
// the UI to notice "hey, there's an old Mox I can manage" reasonably soon.
const adoptionTickerInterval = 30 * time.Second

// driftTickerInterval controls how often the drift detector re-hashes the
// on-disk mox.conf to detect operator hand-edits.  10 min is a reasonable
// balance — it's far below the usual "I forgot I edited this" window and
// keeps the hash work negligible.
const driftTickerInterval = 10 * time.Minute

// certRenewalInterval controls the background certificate renewal scan.
// A 1-hour cadence is well below the 30-day renewal window; each scan only
// renews certs with DaysLeft < 30 so the per-hour work is nearly always zero.
const certRenewalInterval = 1 * time.Hour

func (s *Service) startWorkers(ctx context.Context) {
	// --- adopt ASAP on boot --------------------------------------------------
	if err := s.bootAdopt(ctx); err != nil {
		s.log.WarnContext(ctx, "mail: boot adopt skipped", "error", err)
	}
	// --- drift refresh ASAP on boot (seed baseline before first user request)
	if s.drift != nil {
		if drifted, _, _ := s.drift.Refresh(); drifted {
			s.log.WarnContext(ctx, "mail: drift detected at boot — writes will be blocked until resolved")
		}
	}

	// --- probe fast ticker ---------------------------------------------------
	fast := time.NewTicker(probeTickerFast)
	// --- slow ticker fires 4x less often (15s) for TCP/TLS banner probes.
	slow := time.NewTicker(probeTickerSlow)
	// --- adopt ticker --------------------------------------------------------
	adoptTicker := time.NewTicker(adoptionTickerInterval)
	// --- drift ticker --------------------------------------------------------
	driftTicker := time.NewTicker(driftTickerInterval)
	// --- cert renewal ticker (Phase 4) ---------------------------------------
	certRenewalTicker := time.NewTicker(certRenewalInterval)
	// --- imap sync manager StartAll ticker (Phase 7) --------------------------
	// Picks up newly-created accounts whose IMAPSyncEnabled was toggled on
	// after boot without requiring a restart.
	imapSyncTicker := time.NewTicker(5 * time.Minute)
	// --- retention ticker (Phase 8) -------------------------------------------
	// Runs applyRetention once per hour so expired logs, webhook events,
	// delivery events, health-checks, and index-message rows are pruned.
	retentionTicker := time.NewTicker(retentionApplyInterval())

	// --- process-exit observer goroutine ------------------------------------
	// Supervisor.Wait() returns a channel that closes once (per Start/Adopt).
	// We launch an observer that re-reads it on each tick and, when the
	// channel fires, publishes a RuntimeStopped/Crashed event + triggers
	// crash-loop backoff handling.
	go s.observeSupervisorExits(ctx)

	done := s.backgroundDone
	go func() {
		defer fast.Stop()
		defer slow.Stop()
		defer adoptTicker.Stop()
		defer driftTicker.Stop()
		defer certRenewalTicker.Stop()
		defer imapSyncTicker.Stop()
		defer retentionTicker.Stop()

		// --- Phase 7: start per-account IMAP sync goroutines -------------------
		if s.imapSyncManager != nil {
			started := s.imapSyncManager.StartAll(ctx)
			if started > 0 {
				s.log.InfoContext(ctx, "mail: started IMAP sync loops", "count", started)
			}
		}

		// Run one probe right after boot so the UI has meaningful dots on
		// page load (no need to wait the full 5s).
		if _, err := s.runAllProbes(ctx, nil); err != nil {
			s.log.DebugContext(ctx, "mail: initial probe run failed (expected if Mox not yet installed)",
				"error", err)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-fast.C:
				if _, err := s.runAllProbes(ctx, nil); err != nil {
					s.log.WarnContext(ctx, "mail: probe tick failed", "error", err)
				}
			case <-slow.C:
				// Phase 3: fire L4 (SMTP banners) and L5 (IMAP banners) every 15s.
				// L6 DNS is per-domain and requires SSRF-guard opt-in per domain
				// (handled by the DomainDNSCheck endpoint on demand).
				if _, err := s.runAllProbes(ctx, []int{4, 5}); err != nil {
					s.log.DebugContext(ctx, "mail: slow probe tick (L4/L5) failed",
						"error", err)
				}
			case <-adoptTicker.C:
				if err := s.bootAdopt(ctx); err != nil {
					s.log.DebugContext(ctx, "mail: adopt tick failed", "error", err)
				}
			case <-driftTicker.C:
				s.refreshDrift(ctx)
			case <-certRenewalTicker.C:
				if renewed := s.runCertificateRenewals(ctx); renewed > 0 {
					s.log.InfoContext(ctx, "mail: background renewal completed", "renewed", renewed)
				}
			case <-imapSyncTicker.C:
				// Phase 7: pick up any accounts that were newly configured
				// with IMAP sync since boot (StartAll is idempotent).
				if s.imapSyncManager != nil {
					if n := s.imapSyncManager.StartAll(ctx); n > 0 {
						s.log.DebugContext(ctx, "mail: picked up new IMAP sync accounts on tick",
							"started", n,
							"running", s.imapSyncManager.CountRunning())
					}
				}
			case <-retentionTicker.C:
				// Phase 8: background retention run.  Silently swallowed on
				// failure (dedicated logs are good; transient errors; next tick
				// will retry).
				if _, rerr := s.MailRetentionApplyNow(ctx); rerr != nil {
					s.log.DebugContext(ctx, "mail: retention tick failed", "error", rerr)
				}
			}
		}
	}()
}

// refreshDrift runs the drift detector's Refresh() method once.  It is safe
// to call on a nil detector (returns immediately) and is safe for concurrent
// use because DriftDetector.Refresh() is internally mutex-guarded.
func (s *Service) refreshDrift(ctx context.Context) {
	if s == nil || s.drift == nil {
		return
	}
	drifted, diskHash, _ := s.drift.Refresh()
	if drifted {
		s.log.WarnContext(ctx, "mail: drift detected",
			"expected_hash", s.drift.SQLiteHash(),
			"disk_hash", diskHash,
			"checked_at", s.drift.LastCheck())
	}
}

// bootAdopt wraps Supervisor.Adopt with idempotency: if the supervisor is
// already managing a live process, it's a no-op.
func (s *Service) bootAdopt(ctx context.Context) error {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return err
	}
	if err := svc.EnsurePaths(); err != nil {
		return err
	}
	state, _, _, _, _, _ := svc.Status()
	if state == moxsupervisor.StateRunning || state == moxsupervisor.StateAdopted {
		return nil
	}
	res, aerr := svc.Adopt()
	if aerr != nil {
		if errors.Is(aerr, moxsupervisor.ErrAdoptionRejected) {
			s.log.WarnContext(ctx, "mail: orphan adoption rejected (operator review required)",
				"issues", formatAdoptionIssues(res.Issues))
			return nil
		}
		return fmt.Errorf("adopt: %w", aerr)
	}
	if res.Success {
		s.log.InfoContext(ctx, "mail: orphan mox adopted",
			"pid", res.ProcessID, "boot_id", res.Marker.BootID,
			"issues", formatAdoptionIssues(res.Issues))
		s.publish(ctx, EventTypeRuntimeAdopted, map[string]any{
			"pid":     res.ProcessID,
			"boot_id": res.Marker.BootID,
			"warnings": formatAdoptionIssues(res.Issues),
		})
	}
	return nil
}

func formatAdoptionIssues(issues []moxsupervisor.AdoptionIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Layer+": "+i.Message)
	}
	sort.Strings(out)
	return out
}

// observeSupervisorExits spins on Supervisor.Wait() and publishes exit events.
// A single observer goroutine is enough because Supervisor serialises lifecycle
// internally.
func (s *Service) observeSupervisorExits(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		svc, err := s.supervisor(ctx)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		// Wait blocks until Start/Adopt has happened (returns a non-closed
		// channel) or the current run exits.
		ch := svc.Wait()
		select {
		case <-ctx.Done():
			return
		case res, ok := <-ch:
			if !ok {
				// No process has ever been started – back off and retry.
				time.Sleep(2 * time.Second)
				continue
			}
			evtType := EventTypeRuntimeStopped
			payload := map[string]any{
				"exit_code":   res.ExitCode,
				"from_signal": res.FromSignal,
				"exited_at":   res.ExitedAt.Format(time.RFC3339),
			}
			if res.ExitCode != 0 && !res.FromSignal {
				evtType = EventTypeRuntimeCrashed
			}
			if res.ExitErr != nil {
				payload["error"] = res.ExitErr.Error()
			}
			s.publish(ctx, evtType, payload)
		}
	}
}

// -----------------------------------------------------------------------------
// Probes construction + execution.
// -----------------------------------------------------------------------------

// newL1L2L3Probes constructs the three currently-implemented probe layers
// based on the current settings + supervisor paths.
//
// Deprecated: use newProbes, which also returns layers 4–6.
func (s *Service) newL1L2L3Probes(ctx context.Context) ([]probes.Probe, error) {
	base, _, err := s.newProbes(ctx)
	return base, err
}

// newProbes constructs all wired probe layers: L1–L5 always, L6 returned in
// the perDomain slice (caller adds per-domain DKIM selectors, then appends to
// the "run all" list).
func (s *Service) newProbes(ctx context.Context) (l1ToL5 []probes.Probe, perDomain []probes.Probe, err error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Supervisor does not expose the current marker path directly; it's
	// always <MoxRoot>/run/mox.marker per Phase 2.1.
	markerPath := filepath.Join(svc.MoxRoot, "run", "mox.marker")
	binaryPath := svc.BinaryPath
	configPath := svc.ConfigPath
	dataDir := svc.DataDir

	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, nil, err
	}

	baseURL, dialUnix := buildWebAPIEndpoint(settings.WebAPIAddr,
		filepath.Join(svc.MoxRoot, "run", "mox.webapi.sock"))

	// L1 + L2 + L3
	base := []probes.Probe{
		probes.NewL1Process(probes.L1Config{
			MarkerPath:         markerPath,
			ExpectedBinaryPath: binaryPath,
			ExpectedInstance:   s.phantomInstance,
		}),
		probes.NewL2Control(probes.L2Config{
			BinaryPath: binaryPath,
			ConfigPath: configPath,
			DataDir:    dataDir,
			Timeout:    15 * time.Second,
		}),
		probes.NewL3WebAPI(probes.L3Config{
			BaseURL:               baseURL,
			DialUnixSocket:        dialUnix,
			Timeout:               3 * time.Second,
			InsecureSkipTLSVerify: true, // loopback / unix socket – self-signed is the normal state pre-ACME
		}),
	}

	// L4: SMTP banners — loopback-only (no external targets).
	_ = dialUnix
	smtpHost := "127.0.0.1"
	if settings.SMTPPort == 0 {
		settings.SMTPPort = 25
	}
	if settings.SMTPSubmissionPort == 0 {
		settings.SMTPSubmissionPort = 587
	}
	if settings.SMTPSPort == 0 {
		settings.SMTPSPort = 465
	}
	l4 := probes.NewL4SMTP(probes.L4Config{
		SMTPAddr:       fmt.Sprintf("%s:%d", smtpHost, settings.SMTPPort),
		SubmissionAddr: fmt.Sprintf("%s:%d", smtpHost, settings.SMTPSubmissionPort),
		SMTPSAddr:      fmt.Sprintf("%s:%d", smtpHost, settings.SMTPSPort),
		Timeout:        3 * time.Second,
	})
	base = append(base, l4)

	// L5: IMAP banners — loopback-only.
	if settings.IMAPPort == 0 {
		settings.IMAPPort = 143
	}
	if settings.IMAPSPort == 0 {
		settings.IMAPSPort = 993
	}
	l5 := probes.NewL5IMAP(probes.L5Config{
		IMAPAddr:  fmt.Sprintf("%s:%d", smtpHost, settings.IMAPPort),
		IMAPSAddr: fmt.Sprintf("%s:%d", smtpHost, settings.IMAPSPort),
		Timeout:   3 * time.Second,
	})
	base = append(base, l5)

	// L6 is per-domain; returned separately so callers can decide whether to
	// wire in actual domains (would require SSRF guard approval).
	perDomain = nil
	return base, perDomain, nil
}

// buildWebAPIEndpoint turns the textual settings.webapi_addr into a probe
// L3 pair (baseURL, unixSocketPath).
//
// Priority:
//  1. If webapi_addr is an absolute filesystem path → unix socket mode (the
//     recommended Mox default; avoids TCP port allocation entirely).
//  2. Otherwise treat as host:port → http://<host>:port/ with loopback-only
//     semantics.  We NEVER expose Mox webapi on 0.0.0.0 automatically;
//     operator has to opt in via settings.
func buildWebAPIEndpoint(addr string, defaultSock string) (baseURL string, unixSocket string) {
	if addr == "" {
		// No explicit address: default to a unix socket inside <moxRoot>/run.
		return "http://mox.local/", defaultSock
	}
	// Absolute path?
	if strings.HasPrefix(addr, "/") {
		return "http://mox.local/", addr
	}
	// host:port → use plain HTTP loopback.
	return "http://" + strings.Trim(addr, "/") + "/", ""
}

// runAllProbes executes every implemented probe layer and persists a
// timestamped summary to the in-memory lastProbe state.  The detailed
// results are returned for the caller's benefit; they are also cached so
// RuntimeStatus can return them synchronously.
func (s *Service) runAllProbes(ctx context.Context, layerFilter []int) ([]probes.Result, error) {
	l1ToL5, perDomain, err := s.newProbes(ctx)
	if err != nil {
		return nil, err
	}
	all := append(l1ToL5, perDomain...)
	_ = perDomain

	var run []probes.Probe
	if len(layerFilter) == 0 {
		run = all
	} else {
		want := map[int]bool{}
		for _, l := range layerFilter {
			want[l] = true
		}
		for _, p := range all {
			if want[p.Layer()] {
				run = append(run, p)
			}
		}
	}

	results := probes.RunAll(ctx, run)
	overall := probes.Summary(results)

	s.mu.Lock()
	// Merge: only pad/overwrite layers that actually ran; keep others.
	existing := s.lastProbeResults
	byLayer := map[int]probes.Result{}
	for _, r := range existing {
		byLayer[r.Layer] = r
	}
	for _, r := range results {
		byLayer[r.Layer] = r
	}
	merged := make([]probes.Result, 0, len(byLayer))
	for _, r := range byLayer {
		merged = append(merged, r)
	}
	s.lastProbeResults = padProbeResults(merged)
	s.lastProbeOverall = overall
	s.lastProbeAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()

	// Publish one aggregate event so the UI SSE stream can update without
	// polling.  Individual probe results are not evented (too noisy).
	s.publish(ctx, EventTypeRuntimeProbeResult, map[string]any{
		"overall": overall.String(),
		"count":   len(results),
		"at":      s.lastProbeAt,
	})
	return results, nil
}

// padProbeResults pads the results slice to 9 entries (one per planned
// layer) with "unimplemented" Yellow-stub entries for layers not yet
// wired.  The UI renders 9 dots – padding here guarantees the order/stable
// index rather than expecting the frontend to do math.
func padProbeResults(rs []probes.Result) []probes.Result {
	const total = 9
	out := make([]probes.Result, 0, total)
	byLayer := map[int]probes.Result{}
	for _, r := range rs {
		byLayer[r.Layer] = r
	}
	layerNames := [total + 1]string{
		"", "l1_process", "l2_control", "l3_webapi",
		"l4_smtp_banner", "l5_imap_tls", "l6_dns",
		"l7_delivery", "l8_certs", "l9_reputation",
	}
	for layer := 1; layer <= total; layer++ {
		if r, ok := byLayer[layer]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, probes.Result{
			Name:    layerNames[layer],
			Layer:   layer,
			State:   probes.StateUnknown,
			Message: "probe not wired yet (future phase)",
		})
	}
	return out
}

// -----------------------------------------------------------------------------
// Audit + event publish helpers.
// -----------------------------------------------------------------------------

// publish emits a hub event if the hub is wired.  Hub-publish is fire-and-
// forget: a nil hub, full channel, or closed consumer must never block the
// service layer.
func (s *Service) publish(ctx context.Context, eventType string, payload map[string]any) {
	if s.hub == nil {
		return
	}
	defer func() {
		// Hub.Publish panics if internally inconsistent; swallow it rather
		// than take down the mail worker.
		_ = recover()
	}()
	s.hub.Publish(events.Event{
		Scope:     EventScope,
		ScopeID:   "",
		Type:      eventType,
		Payload:   payload,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	_ = ctx
}

// addAudit writes a persistent audit row.  The helper exists so every
// call site uses the same low/medium/high risk-level defaults.
func (s *Service) addAudit(ctx context.Context, eventType, summary string, payload map[string]any, risk string) {
	if s.store == nil {
		return
	}
	if risk == "" {
		risk = "low"
	}
	if payload == nil {
		payload = map[string]any{}
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: eventType,
		RiskLevel: risk,
		Summary:   summary,
		Payload:   payload,
	})
}

// keep unused import usages satisfied (these are used in later functions
// added in other files; silence lint for the ones only used above).
var _ = sort.Strings
var _ = os.IsNotExist
var _ = http.MethodGet
var _ sync.Mutex
