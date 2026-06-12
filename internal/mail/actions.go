package mail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"phantom-lancer/internal/mail/moxbinary"
	"phantom-lancer/internal/mail/moxsupervisor"
	"phantom-lancer/internal/mail/probes"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Runtime status aggregate.
// -----------------------------------------------------------------------------

// RuntimeStatus returns the "everything we know" snapshot served by
// /api/mail/runtime/status.  The call is cheap: no subprocesses, no HTTP –
// it reads cached probe results + supervisor.State() + settings row.
func (s *Service) RuntimeStatus(ctx context.Context) (*RuntimeStatus, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	state, pid, bootID, cls, fails, backoff := svc.Status()

	// Best-effort detect; errors are logged but never hard-fail the status
	// page (operator wants to see the UI even when disk is weird).
	var det *moxbinary.DetectedResult
	det, derr := moxbinary.Detect(s.controlledBinDir(), moxbinary.DetectOptions{VersionTimeout: 3 * time.Second, SkipPATH: false})
	if derr != nil && !errors.Is(derr, moxbinary.ErrNoBinary) {
		s.log.WarnContext(ctx, "mail: RuntimeStatus detect failed", "error", derr)
	}
	if det == nil {
		det = &moxbinary.DetectedResult{}
	}

	s.mu.Lock()
	probesResult := s.lastProbeResults
	overall := s.lastProbeOverall
	lastProbe := s.lastProbeAt
	lastChange := s.lastChangeAt
	s.mu.Unlock()

	if len(probesResult) == 0 {
		probesResult = padProbeResults(nil)
	}

	uptime := time.Duration(0)
	if pid > 0 && state == moxsupervisor.StateRunning {
		// Best-effort uptime: starttime from /proc on linux, else 0.
		uptime = procUptime(pid)
	}

	return &RuntimeStatus{
		ConfigMode:       settings.ConfigMode,
		DesiredState:     settings.DesiredState,
		ImportMode:       settings.ImportMode,
		Observed:         string(state),
		PID:              pid,
		BootID:           bootID,
		CrashLoopState:   string(cls),
		ConsecutiveFails: fails,
		BackoffRemaining: backoff,
		Uptime:           uptime,
		BinaryControlled: det.Controlled,
		BinaryPATH:       det.Path,
		BinarySelected:   det.Selected,
		Probes:           probesResult,
		Overall:          overall,
		DomainCount:      0, // Phase 3
		AccountCount:     0, // Phase 3
		LastProbeAt:      lastProbe,
		LastChangeAt:     lastChange,
	}, nil
}

// procUptime reads /proc/<pid>/stat starttime on Linux and converts it to a
// wall-time Duration since boot.  Returns 0 on other platforms (the uptime
// field is a best-effort nicety; absence degrades to "show 0" in UI).
func procUptime(pid int) time.Duration {
	if runtime.GOOS != "linux" {
		return 0
	}
	startTicks, _, ok := readProcStartTimeTicksFallback(pid)
	if !ok {
		return 0
	}
	bt, clk, ok := bootTimeAndClockTicks()
	if !ok {
		return 0
	}
	startNano := int64(startTicks) * int64(time.Second) / int64(clk)
	bootNano := bt.UnixNano()
	pidStart := bootNano + startNano
	elapsed := time.Since(time.Unix(0, pidStart))
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

// --- Fallback: these helpers exist so probes/readProcStartTimeTicks can be
// re-used without pulling moxbinary / probes into a circular import.  They
// mirror the same implementation from Phase 2.3 / 2.1 (identical pattern).
//
// We keep small local copies rather than depend on internal imports because
// (1) readProcStartTimeTicks lives in moxsupervisor (which mail already
// imports) but the boot-time helpers are genuinely independent, and (2) we
// want to avoid an import from the "mail" package into "probes" (which would
// be circular since probes also re-exports L1/L2/L3).

func readProcStartTimeTicksFallback(pid int) (uint64, string, bool) {
	// Thin wrapper around readProcStartTimeTicksFallbackImpl so the function
	// body stays readable.
	return readProcStartTimeTicksFallbackImpl(pid)
}

func readProcStartTimeTicksFallbackImpl(pid int) (uint64, string, bool) {
	if runtime.GOOS != "linux" {
		return 0, "non-linux", false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Sprintf("read /proc/%d/stat: %v", pid, err), false
	}
	idx := strings.LastIndexByte(string(data), ')')
	if idx < 0 {
		return 0, "malformed /proc/<pid>/stat: no closing paren", false
	}
	tail := strings.TrimLeft(string(data[idx+1:]), " ")
	fields := strings.Fields(tail)
	if len(fields) < 20 {
		return 0, fmt.Sprintf("too few tail tokens (%d)", len(fields)), false
	}
	// Manual int parse to avoid strconv import.
	v := uint64(0)
	for _, c := range []byte(fields[19]) {
		if c < '0' || c > '9' {
			return 0, "non-numeric starttime", false
		}
		v = v*10 + uint64(c-'0')
	}
	return v, "linux /proc/<pid>/stat field[19]", true
}

// bootTimeAndClockTicks reads btime from /proc/stat and CLK_TCK from
// sysconf(SC_CLK_TCK).  Returns (boottime, clk_tck, ok).
func bootTimeAndClockTicks() (time.Time, int, bool) {
	if runtime.GOOS != "linux" {
		return time.Time{}, 0, false
	}
	clk := 100
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, clk, false
	}
	var btime int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			// parse int
			n := int64(0)
			ok := true
			for _, c := range []byte(f[1]) {
				if c < '0' || c > '9' {
					ok = false
					break
				}
				n = n*10 + int64(c-'0')
			}
			if ok {
				btime = n
			}
		}
	}
	if btime == 0 {
		return time.Time{}, clk, false
	}
	return time.Unix(btime, 0), clk, true
}

// -----------------------------------------------------------------------------
// Binary actions: Detect / Download / Install / Uninstall.
// -----------------------------------------------------------------------------

// BinaryDetect runs moxbinary.Detect against the configured directories.
func (s *Service) BinaryDetect(ctx context.Context, req BinaryDetectRequest) (*BinaryDetectResponse, error) {
	_ = ctx
	opts := moxbinary.DetectOptions{
		HintPath:       strings.TrimSpace(req.HintPath),
		ExtraPATH:      req.ExtraPATH,
		VersionTimeout: req.VersionTimeout,
		SkipPATH:       req.SkipPATH,
	}
	if opts.VersionTimeout <= 0 {
		opts.VersionTimeout = 5 * time.Second
	}
	dr, err := moxbinary.Detect(s.controlledBinDir(), opts)
	if err != nil {
		return nil, fmt.Errorf("moxbinary.Detect: %w", err)
	}
	s.touchLastChange()
	sourceVal := ""
	versionVal := ""
	inWhiteVal := false
	if dr.Selected != nil {
		sourceVal = dr.Selected.Source
		versionVal = dr.Selected.Version
		inWhiteVal = dr.Selected.InWhitelist
	}
	s.addAudit(ctx, EventTypeBinaryDetected,
		fmt.Sprintf("检测 Mox 二进制（controlled=%v path=%v）", dr.Controlled != nil, dr.Path != nil),
		map[string]any{
			"hint":         dr.Hint != nil,
			"selected":     dr.Selected != nil,
			"source":       sourceVal,
			"version":      versionVal,
			"in_whitelist": inWhiteVal,
		}, "low")
	return &BinaryDetectResponse{
		Controlled: dr.Controlled,
		PATH:       dr.Path,
		Hint:       dr.Hint,
		Selected:   dr.Selected,
	}, nil
}

// BinaryDownload fetches the requested pinned version from the approved
// release-prefix whitelist and verifies its SHA256.  Returns a path to a
// tempfile in DestDir with 0700 permissions.  The caller is responsible for
// moving it into place via Install() or removing it if Install is never
// called (leaked tempfiles older than 1h are cleaned on boot).
func (s *Service) BinaryDownload(ctx context.Context, req BinaryDownloadRequest) (*BinaryDownloadResponse, error) {
	version := strings.TrimSpace(req.Version)
	if version == "" {
		return nil, fmt.Errorf("%w: empty version", moxbinary.ErrUnknownVersion)
	}
	destDir := req.DestDir
	if destDir == "" {
		destDir = s.controlledBinDir()
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir dest: %w", err)
	}

	sizeMax := req.SizeMaxBytes
	if sizeMax <= 0 {
		sizeMax = 200 * 1024 * 1024 // 200 MiB
	}

	var prog func(rx, total int64)
	if req.Progress != nil || req.ReportPercent {
		progress := req.Progress
		prog = func(rx, total int64) {
			if progress == nil || total <= 0 {
				return
			}
			pct := int(rx * 100 / total)
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			select {
			case progress <- pct:
			default:
			}
		}
	}

	dr, err := moxbinary.Download(ctx, version, moxbinary.DownloadOptions{
		HTTPClient:   &http.Client{Timeout: 10 * time.Minute},
		OverrideURL:  strings.TrimSpace(req.OverrideURL),
		DestDir:      destDir,
		SizeMaxBytes: sizeMax,
		Progress:     prog,
	})
	if err != nil {
		s.addAudit(ctx, EventTypeBinaryDetected, "下载 Mox 失败",
			map[string]any{"version": version, "error": err.Error()}, "medium")
		return nil, err
	}
	s.touchLastChange()
	s.addAudit(ctx, EventTypeBinaryDetected, fmt.Sprintf("下载 Mox %s 成功（%d 字节）", version, dr.SizeBytes),
		map[string]any{
			"version":  version,
			"size":     dr.SizeBytes,
			"checksum": dr.ChecksumSHA256,
			"temp":     filepath.Base(dr.TempPath),
		}, "medium")
	return &BinaryDownloadResponse{
		TempPath:       dr.TempPath,
		SizeBytes:      dr.SizeBytes,
		ChecksumSHA256: dr.ChecksumSHA256,
		ExpectedSHA256: dr.ExpectedSHA256,
		Version:        dr.Version,
	}, nil
}

// BinaryInstall places a (previously downloaded / caller-provided) binary
// into the controlled install directory.  The install creates a backup
// copy + writes a version sidecar (used by Uninstall as defence-in-depth).
func (s *Service) BinaryInstall(ctx context.Context, req BinaryInstallRequest) (*moxbinary.InstallResult, error) {
	src := strings.TrimSpace(req.Src)
	if src == "" {
		return nil, fmt.Errorf("install: missing src path")
	}
	destDir := s.controlledBinDir()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir controlled dir: %w", err)
	}
	res, err := moxbinary.Install(ctx, src, destDir, moxbinary.InstallOptions{
		Version:        strings.TrimSpace(req.Version),
		ChecksumSHA256: strings.TrimSpace(req.ChecksumSHA256),
		Force:          req.Force,
	})
	if err != nil {
		s.addAudit(ctx, EventTypeBinaryInstalled, "安装 Mox 失败",
			map[string]any{"error": err.Error(), "src": filepath.Base(src)}, "high")
		return nil, err
	}

	// Persist binary path to settings so supervisor picks it up next time.
	if _, serr := s.store.MailUpsertBinaryPath(ctx, res.InstalledPath); serr != nil {
		s.log.WarnContext(ctx, "mail: install succeeded but DB update of mox_binary_path failed",
			"error", serr, "installed_path", res.InstalledPath)
	}

	s.touchLastChange()
	s.publish(ctx, EventTypeBinaryInstalled, map[string]any{
		"version":  res.InstalledVersion,
		"checksum": res.InstalledChecksumSHA256,
		"path":     res.InstalledPath,
		"replaced": res.ReplacedVersion,
	})
	s.addAudit(ctx, EventTypeBinaryInstalled, fmt.Sprintf("安装 Mox %s 到受控目录", res.InstalledVersion),
		map[string]any{
			"installed_version": res.InstalledVersion,
			"previous_backup":   res.PreviousBackupPath,
			"replaced":          res.ReplacedVersion,
		}, "high")
	return res, nil
}

// BinaryUninstall removes ONLY the Phantom-controlled mox copy; defence:
// sidecar must exist (see moxbinary/uninstall.go).  Refuses when binary is
// in use (running Mox) unless Force=true.
func (s *Service) BinaryUninstall(ctx context.Context, req BinaryUninstallRequest) (*BinaryUninstallResponse, error) {
	res, err := moxbinary.UninstallWithResult(s.controlledBinDir(), req.Force)
	if err != nil {
		s.addAudit(ctx, EventTypeBinaryUninstalled, "卸载 Mox 失败",
			map[string]any{"error": err.Error(), "force": req.Force}, "high")
		return nil, err
	}

	// Clear binary path from settings so supervisor doesn't keep pointing
	// at a deleted binary.
	if _, serr := s.store.MailUpsertBinaryPath(ctx, ""); serr != nil {
		s.log.WarnContext(ctx, "mail: uninstall succeeded but clearing mox_binary_path failed", "error", serr)
	}

	s.touchLastChange()
	s.publish(ctx, EventTypeBinaryUninstalled, map[string]any{
		"version": res.UninstalledVersion,
		"removed": res.RemovedBinary,
		"force":   req.Force,
	})
	s.addAudit(ctx, EventTypeBinaryUninstalled,
		fmt.Sprintf("卸载受控 Mox %s（备份移除 %d 个）", res.UninstalledVersion, res.BackupsRemoved),
		map[string]any{
			"uninstalled_version": res.UninstalledVersion,
			"backups_removed":     res.BackupsRemoved,
			"removed_sidecar":     res.RemovedSidecar,
			"controlled_dir":      res.ControlledDir,
		}, "high")
	return &BinaryUninstallResponse{
		RemovedBinary:  res.RemovedBinary,
		RemovedSidecar: res.RemovedSidecar,
		BackupsRemoved: res.BackupsRemoved,
		UninstalledVer: res.UninstalledVersion,
		ControlledDir:  res.ControlledDir,
	}, nil
}

// -----------------------------------------------------------------------------
// Lifecycle: Start / Stop / Restart / Probe.
// -----------------------------------------------------------------------------

// Start attempts to launch Mox via moxsupervisor.  Blocks through preflight
// and cmd.Start(); once Start() returns, Mox is running (or we've failed).
// On success, publishes an event and refreshes desired_state in settings.
func (s *Service) Start(ctx context.Context, req LifecycleRequest) (*LifecycleResponse, error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.EnsurePaths(); err != nil {
		return nil, err
	}
	importRO, _, serr := s.importMode(ctx)
	if serr != nil {
		return nil, serr
	}
	if importRO {
		return &LifecycleResponse{
			Requested:   "start",
			Accepted:    false,
			ObservedNow: string(moxsupervisor.StateImportRO),
			Message:     "import read-only mode; start disabled",
		}, fmt.Errorf("%w: import read-only", moxsupervisor.ErrNotStarted)
	}

	s.addAudit(ctx, EventTypeRuntimeStartRequested, "请求启动 Mox",
		map[string]any{"reason": req.Reason, "block_ms": req.BlockMS}, "high")

	serr = svc.Start(ctx)
	obs, _, _, _, _, _ := svc.Status()
	if serr != nil {
		s.publish(ctx, EventTypeRuntimeStartFailed, map[string]any{
			"error": serr.Error(), "observed": string(obs),
		})
		return &LifecycleResponse{
			Requested:   "start",
			Accepted:    false,
			ObservedNow: string(obs),
			Message:     serr.Error(),
		}, serr
	}

	s.touchLastChange()
	_, _ = s.store.MailUpsertDesiredState(ctx, "running")
	s.publish(ctx, EventTypeRuntimeStarted, map[string]any{
		"observed": string(obs),
	})
	s.addAudit(ctx, EventTypeRuntimeStarted, "已启动 Mox",
		map[string]any{"reason": req.Reason, "observed": string(obs)}, "high")
	return &LifecycleResponse{
		Requested:   "start",
		Accepted:    true,
		ObservedNow: string(obs),
	}, nil
}

// Stop asks the supervisor to stop Mox (3-tier signal escalation).  Blocks
// until Stop() returns (which can take up to 30+10+5 = 45s in the worst
// case).  The HTTP layer should use BlockMS to avoid blocking on a slow
// client; the call itself never fails partially.
func (s *Service) Stop(ctx context.Context, req LifecycleRequest) (*LifecycleResponse, error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	s.addAudit(ctx, EventTypeRuntimeStopRequested, "请求停止 Mox",
		map[string]any{"reason": req.Reason}, "high")
	sr, err := svc.Stop()
	obs, _, _, _, _, _ := svc.Status()
	if err != nil {
		return &LifecycleResponse{
			Requested:   "stop",
			Accepted:    false,
			ObservedNow: string(obs),
			Message:     err.Error(),
		}, err
	}
	s.touchLastChange()
	_, _ = s.store.MailUpsertDesiredState(ctx, "stopped")
	s.publish(ctx, EventTypeRuntimeStopped, map[string]any{
		"observed":   string(obs),
		"exit_code":  sr.ExitCode,
		"signal":     int(sr.SignalUsed),
		"killed":     sr.Killed,
	})
	s.addAudit(ctx, EventTypeRuntimeStopped, "已停止 Mox",
		map[string]any{
			"reason":    req.Reason,
			"exit_code": sr.ExitCode,
			"signal":    int(sr.SignalUsed),
			"killed":    sr.Killed,
			"duration":  sr.Duration.String(),
		}, "high")
	return &LifecycleResponse{
		Requested:   "stop",
		Accepted:    true,
		ObservedNow: string(obs),
	}, nil
}

// Restart is equivalent to Stop → Start.  Preflight is re-run as part of
// Start, so a config change between calls will be picked up.
func (s *Service) Restart(ctx context.Context, req LifecycleRequest) (*LifecycleResponse, error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	s.addAudit(ctx, EventTypeRuntimeStopRequested, "请求重启 Mox",
		map[string]any{"reason": req.Reason}, "high")
	sr, err := svc.Restart(ctx)
	obs, _, _, _, _, _ := svc.Status()
	if err != nil {
		s.publish(ctx, EventTypeRuntimeStartFailed, map[string]any{
			"action":   "restart",
			"error":    err.Error(),
			"observed": string(obs),
		})
		return &LifecycleResponse{
			Requested:   "restart",
			Accepted:    false,
			ObservedNow: string(obs),
			Message:     err.Error(),
		}, err
	}
	s.touchLastChange()
	_, _ = s.store.MailUpsertDesiredState(ctx, "running")
	s.publish(ctx, EventTypeRuntimeRestarted, map[string]any{
		"observed":   string(obs),
		"exit_code":  sr.ExitCode,
		"signal":     int(sr.SignalUsed),
		"killed":     sr.Killed,
	})
	s.addAudit(ctx, EventTypeRuntimeRestarted, "已重启 Mox",
		map[string]any{
			"reason":    req.Reason,
			"exit_code": sr.ExitCode,
			"signal":    int(sr.SignalUsed),
			"killed":    sr.Killed,
			"duration":  sr.Duration.String(),
		}, "high")
	return &LifecycleResponse{
		Requested:   "restart",
		Accepted:    true,
		ObservedNow: string(obs),
	}, nil
}

// RuntimeProbe runs (a subset of) probe layers right now.  Normally the
// background ticker handles this; exposing the action lets operators
// trigger a fresh probe set from the UI immediately after Start or config
// changes.
func (s *Service) RuntimeProbe(ctx context.Context, req RuntimeProbeRequest) (*RuntimeProbeResponse, error) {
	results, err := s.runAllProbes(ctx, req.Layers)
	if err != nil {
		return nil, err
	}
	overall := probes.Summary(results)
	out := padProbeResults(results)
	return &RuntimeProbeResponse{
		Results: out,
		Overall: overall,
		At:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// importMode reads import_mode from the settings row.
func (s *Service) importMode(ctx context.Context) (bool, string, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return false, "", err
	}
	return settings.ImportMode, settings.ImportLabel, nil
}

// -----------------------------------------------------------------------------
// Setup: Initialize / Import / Preflight Ports.
// -----------------------------------------------------------------------------

// SetupInitialize writes the placeholder config + data dir + records paths
// in the settings row, readying a fresh managed install for Start.
func (s *Service) SetupInitialize(ctx context.Context, req SetupInitializeRequest) (*SetupInitializeResponse, error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.EnsurePaths(); err != nil {
		return nil, err
	}
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Guard against accidental overwrite – unless operator opts in.
	exists := false
	if fi, ferr := os.Stat(svc.ConfigPath); ferr == nil && !fi.IsDir() && fi.Size() > 0 {
		exists = true
	}
	if exists && !req.OverwriteExistingConf {
		return nil, fmt.Errorf("refusing to overwrite existing %s (set overwrite_existing_conf to force)", svc.ConfigPath)
	}

	// Pick binary path (controlled or user-chosen).
	binPath := svc.BinaryPath
	if req.UseControlledBinary {
		binPath = filepath.Join(s.controlledBinDir(), "mox")
	}
	// Detect best available binary for suggestions.
	var hint string
	dr, derr := moxbinary.Detect(s.controlledBinDir(), moxbinary.DetectOptions{VersionTimeout: 3 * time.Second})
	if derr == nil && dr.Selected != nil {
		hint = dr.Selected.Path
	}
	if binPath == filepath.Join(s.controlledBinDir(), "mox") {
		if hint != "" {
			binPath = hint
		}
	}

	// Write placeholder config.  Mox config format is documented in
	// https://github.com/mjl-/mox/blob/master/mox.conf.example; we write a
	// minimal skeleton with the requested admin/hostname + listen addresses
	// so Phase 3 configapply can diff and patch it.
	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = settings.Hostname
	}
	adminEmail := strings.TrimSpace(req.AdminEmail)
	if adminEmail == "" {
		adminEmail = settings.AdminEmail
	}
	webmailAddr := strings.TrimSpace(req.WebmailAddr)
	if webmailAddr == "" {
		webmailAddr = settings.WebmailAddr
	}
	webapiAddr := strings.TrimSpace(req.WebAPIAddr)
	if webapiAddr == "" {
		webapiAddr = settings.WebAPIAddr
	}
	skeleton := fmt.Sprintf(defaultConfSkeleton, hostname, adminEmail, adminEmail, webmailAddr, webapiAddr)
	if err := atomicWrite0600(svc.ConfigPath, []byte(skeleton)); err != nil {
		return nil, fmt.Errorf("write mox.conf: %w", err)
	}

	// Persist resolved paths + hostnames to settings.
	if _, uerr := s.store.MailUpsertSetup(ctx, storage.MailSetupUpdate{
		AdminEmail: adminEmail,
		Hostname:   hostname,
		WebmailAddr: webmailAddr,
		WebAPIAddr:  webapiAddr,
		BinaryPath:  binPath,
		ConfigPath:  svc.ConfigPath,
		DataDir:     svc.DataDir,
	}); uerr != nil {
		return nil, fmt.Errorf("persist setup settings: %w", uerr)
	}

	s.touchLastChange()
	s.publish(ctx, EventTypeSetupInitialized, map[string]any{
		"config_path": svc.ConfigPath,
		"data_dir":    svc.DataDir,
		"binary_path": binPath,
	})
	s.addAudit(ctx, EventTypeSetupInitialized,
		fmt.Sprintf("初始化 Mox（hostname=%s admin=%s）", hostname, adminEmail),
		map[string]any{
			"hostname":    hostname,
			"admin_email": adminEmail,
			"binary_path": binPath,
			"config_path": svc.ConfigPath,
			"data_dir":    svc.DataDir,
			"overwrite":   req.OverwriteExistingConf,
		}, "high")

	next := []string{
		"添加域名 (Domains tab)",
		"验证端口空闲 (Preflight ports)",
		"启动 Mox (Start)",
		"运行 L1/L2/L3 probes 确认全绿",
	}
	return &SetupInitializeResponse{
		ConfigPath:      svc.ConfigPath,
		DataDir:         svc.DataDir,
		BinaryPath:      binPath,
		PlaceholderNote: "已写入占位 mox.conf（含 hostname/admin/监听地址）",
		NextSteps:       next,
	}, nil
}

// SetupImport marks the service as "import mode" (read-only) and wires the
// supervisor at the provided external paths.  After this call:
//   - Probes and RuntimeStatus work.
//   - Start / Stop / Install / ConfigApply all refuse.
//   - L4..L9 probes still fire (operator wants delivery dashboards).
func (s *Service) SetupImport(ctx context.Context, req SetupImportRequest) (*SetupImportResponse, error) {
	req.BinaryPath = strings.TrimSpace(req.BinaryPath)
	req.ConfigPath = strings.TrimSpace(req.ConfigPath)
	req.DataDir = strings.TrimSpace(req.DataDir)
	req.Label = strings.TrimSpace(req.Label)
	switch {
	case req.BinaryPath == "":
		return nil, fmt.Errorf("import: missing binary_path")
	case req.ConfigPath == "":
		return nil, fmt.Errorf("import: missing config_path")
	case req.DataDir == "":
		return nil, fmt.Errorf("import: missing data_dir")
	}
	// Sanity: external binary must exist and be executable.
	info, err := os.Stat(req.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("import: binary %s: %w", req.BinaryPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("import: binary path is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("import: binary %s not executable", req.BinaryPath)
	}
	label := req.Label
	if label == "" {
		label = filepath.Base(filepath.Dir(req.DataDir))
	}

	_, err = s.store.MailUpsertImport(ctx, storage.MailImportUpdate{
		ImportMode: true,
		Label:      label,
		BinaryPath: req.BinaryPath,
		ConfigPath: req.ConfigPath,
		DataDir:    req.DataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("persist import settings: %w", err)
	}

	s.touchLastChange()
	s.publish(ctx, EventTypeSetupImported, map[string]any{
		"label":       label,
		"binary_path": req.BinaryPath,
		"config_path": req.ConfigPath,
		"data_dir":    req.DataDir,
	})
	s.addAudit(ctx, EventTypeSetupImported,
		fmt.Sprintf("导入外部 Mox（label=%s）— 进入只读模式", label),
		map[string]any{
			"label":       label,
			"binary_path": req.BinaryPath,
			"config_path": req.ConfigPath,
			"data_dir":    req.DataDir,
		}, "high")

	// Preflight notes: helpful hints for "imported" setup dash.
	notes := []string{}
	if _, serr := os.Stat(req.ConfigPath); serr != nil {
		notes = append(notes, "config 文件 "+req.ConfigPath+" 不存在")
	}
	if _, serr := os.Stat(req.DataDir); serr != nil {
		notes = append(notes, "data 目录 "+req.DataDir+" 不存在")
	}
	return &SetupImportResponse{
		Imported:       true,
		Label:          label,
		PreflightNotes: notes,
	}, nil
}

// PreflightPorts runs the supervisor preflight (subset: ports only) and
// returns the per-port breakdown used by the Setup tab "port checklist"
// panel.  We don't run the full config test because that requires a binary
// to be installed, which may not have happened yet during setup.
func (s *Service) PreflightPorts(ctx context.Context, _ PreflightPortsRequest) (*PreflightPortsResponse, error) {
	svc, err := s.supervisor(ctx)
	if err != nil {
		return nil, err
	}
	pf := svc.Preflight(ctx)
	out := make([]PortCheck, 0, len(pf.Ports))
	allOK := true
	for _, p := range pf.Ports {
		out = append(out, PortCheck{
			Name:     p.Name,
			Port:     p.Port,
			Host:     p.Host,
			Free:     p.Free,
			Conflict: p.Conflict,
		})
		if !p.Free {
			allOK = false
		}
	}
	return &PreflightPortsResponse{Ports: out, AllOK: allOK}, nil
}

// -----------------------------------------------------------------------------
// Config skeleton + atomic write helpers (shared, no deps).
// -----------------------------------------------------------------------------

// defaultConfSkeleton is the minimal Mox configuration skeleton written by
// SetupInitialize.  Values are substituted with operator input.  The format
// is mox.conf (Go flag-like, documented at mox source).
const defaultConfSkeleton = `# Managed by Phantom Lancer — do not edit by hand.
# Edits made outside Phantom are detected at boot + every 10 min as
# "config drift"; system-level writes are blocked until resolved.

Hostname: %s
Postmaster: %s
AdminAddress: %s

# Listen addresses.  All are loopback / unix-socket by default; operator
# can change later via Settings tab (or external reverse proxy).
#   — Webmail/Admin UI —
WebmailAddr: %s
#   — WebAPI (unix socket: /path/to/sock or loopback:port) —
WebAPIAddr: %s

# SMTP + submission ports (Internet-facing).  Phase 3 DNS / TLS wiring
# populates the per-domain blocks below this header.

# Domains block (placeholder — added via Domains tab):

# TLS block (placeholder — populated by certmanager after Phase 4):

# DANE / TLSA records (placeholder — Phase 4):
`

// atomicWrite0600 writes data to path with 0600 permissions atomically:
// tempfile in the same directory → chmod 0600 → fsync → rename.  This is
// used both for the placeholder mox.conf (written during setup) and for
// Phase 3 configapply atomic updates (which reuse the helper).
func atomicWrite0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, path)
}
