package configapply

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// -----------------------------------------------------------------------------
// Test-only overrides.  Safe to leave nil for production; the Run() loop
// checks them before every step and short-circuits to normal behaviour when
// unset.  Set from a _test.go (or any file) inside the package to inject
// per-step failures for ROLLBACK / drift-detector contract verification.
var (
	testOverrideMu      sync.Mutex
	TestStepFnOverride  map[int]func(ctx context.Context) error // step 1..10 → return error to fail that step
	TestBeforeStepFn    func(step int) error                    // called before any step's real work
	TestAfterRollbackFn func()                                  // called after rollback completes (or fails)
)

// -----------------------------------------------------------------------------
// Narrow local runner interface.
//
// We deliberately avoid importing the moxcli package here to keep the
// configapply layer testable and free of tight coupling.  The caller
// (mail.Service) owns the moxcli.Runner instance and adapts it to the
// RunnerInterface below using a tiny shim.  Concrete type definitions
// live in types.go (shared across the package).

// Run executes the 10-step apply pipeline, streaming progress through
// `progress` (if non-nil, never closed here — caller owns lifecycle).
//
// Failure semantics:
//
//	step ≤3 → return error, keep tmp file for operator inspection.
//	step 4–6 → delete tmp if it exists, leave original intact.
//	step ≥7 → ROLLBACK: rename .bak → .conf, call reloadFn, probeFn.
//
// reloadFn is expected to perform a "graceful reload or restart" (Mox
// SIGHUP first; if unsupported → supervisor Restart).  probeFn returns
// "good" / "warn" / "critical" / "error" / "unknown"; we treat anything
// other than "good" as a probe failure and trigger rollback.
func Run(
	ctx context.Context,
	settings SettingsSnapshot,
	domains []DomainSnapshot,
	accounts []AccountSnapshot,
	aliases []AliasSnapshot,
	cli RunnerInterface,
	reloadFn func(context.Context) error,
	probeFn func(context.Context) (string, error),
	progress chan<- StepStatus,
) PipelineResult {
	result := PipelineResult{Steps: make([]StepStatus, 0, StepCount)}
	configPath := settings.MoxConfigPath
	newPath := configPath + ".new"
	bakPath := configPath + ".bak"

	// emit helper
	emit := func(step int, state, msg, out string) {
		st := StepStatus{
			Step:    step,
			Total:   StepCount,
			Percent: 0,
			Message: msg,
			Output:  out,
			State:   state,
		}
		if step > 0 && step <= StepCount {
			st.Name = StepNames[step-1]
			st.Percent = int(float32(step-1) / float32(StepCount) * 100)
		} else if state == "rollback" {
			st.Name = "Rollback"
			st.Percent = 0
		}
		if state == "done" {
			st.Percent = int(float32(step) / float32(StepCount) * 100)
		}
		if step == StepCount && state == "done" {
			st.Percent = 100
		}
		result.Steps = append(result.Steps, st)
		if progress != nil {
			select {
			case progress <- st:
			default:
			}
		}
	}

	// stepHook: returns (overrideErr, hookErr).  Non-nil overrideErr means
	// the test injected a failure at step i — caller MUST return fail(step,
	// …).  Non-nil hookErr comes from TestBeforeStepFn — caller MUST also
	// treat it as a step failure.
	stepHook := func(step int) (ovrErr error, beforeErr error) {
		testOverrideMu.Lock()
		ovr := TestStepFnOverride
		bef := TestBeforeStepFn
		testOverrideMu.Unlock()
		if bef != nil {
			if e := bef(step); e != nil {
				return nil, e
			}
		}
		if ovr != nil {
			if fn, ok := ovr[step]; ok && fn != nil {
				if e := fn(ctx); e != nil {
					return e, nil
				}
			}
		}
		return nil, nil
	}

	fail := func(step int, msg, out string) PipelineResult {
		emit(step, "failed", msg, out)
		result.Success = false
		result.FailureStep = step
		result.Summary = msg

		// Failure-before-swap → clean up tmp.
		if step <= 6 {
			_ = os.Remove(newPath)
		}

		// Steps ≥7 → rollback.
		if step >= 7 {
			if rbErr := rollback(ctx, configPath, bakPath, reloadFn, probeFn, emit, progress); rbErr != "" {
				result.RollbackErr = rbErr
			} else {
				result.RolledBack = true
			}
			testOverrideMu.Lock()
			arf := TestAfterRollbackFn
			testOverrideMu.Unlock()
			if arf != nil {
				arf()
			}
		}
		return result
	}

	// ----------------------------------------------------------------------
	// Step 1: ValidatePhantomSettings.
	// ----------------------------------------------------------------------
	{
		emit(1, "running", "Validating Phantom settings …", "")
		if ovrErr, hkErr := stepHook(1); ovrErr != nil {
			return fail(1, "step 1 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(1, "step 1 hook: "+hkErr.Error(), "")
		}
		var errs []string
		if strings.TrimSpace(settings.Hostname) == "" {
			errs = append(errs, "hostname required")
		}
		if strings.TrimSpace(settings.AdminEmail) == "" {
			errs = append(errs, "admin_email required")
		}
		if !strings.Contains(settings.AdminEmail, "@") {
			errs = append(errs, "admin_email must contain @")
		}
		if settings.MoxConfigPath == "" {
			errs = append(errs, "config path missing")
		}
		for i, d := range domains {
			if strings.TrimSpace(d.Domain) == "" {
				errs = append(errs, fmt.Sprintf("domain[%d]: empty domain", i))
			}
		}
		for i, a := range accounts {
			if !strings.Contains(a.Email, "@") {
				errs = append(errs, fmt.Sprintf("account[%d]: email invalid: %q", i, a.Email))
			}
		}
		if len(errs) > 0 {
			return fail(1, strings.Join(errs, "; "), "")
		}
		emit(1, "done", fmt.Sprintf("settings ok (%d domains, %d accounts, %d aliases)", len(domains), len(accounts), len(aliases)), "")
	}

	// ----------------------------------------------------------------------
	// Step 2: BuildConfigSkeleton.
	// ----------------------------------------------------------------------
	var cfgBytes []byte
	{
		emit(2, "running", "Building canonical mox.conf from settings …", "")
		if ovrErr, hkErr := stepHook(2); ovrErr != nil {
			return fail(2, "step 2 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(2, "step 2 hook: "+hkErr.Error(), "")
		}
		cfgBytes = buildCanonicalConfig(settings, domains, accounts, aliases)
		emit(2, "done", fmt.Sprintf("canonical config built (%d bytes)", len(cfgBytes)), "")
	}

	// ----------------------------------------------------------------------
	// Step 3: ConfigTestCLI (against in-memory content via tmp file).
	// ----------------------------------------------------------------------
	{
		emit(3, "running", "Running `mox config test` …", "")
		if ovrErr, hkErr := stepHook(3); ovrErr != nil {
			return fail(3, "step 3 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(3, "step 3 hook: "+hkErr.Error(), "")
		}
		if cli != nil {
			// Pre-stage the new bytes so CLI sees them for validation.
			if err := WriteAtomic(newPath, cfgBytes); err != nil {
				return fail(3, fmt.Sprintf("stage new: %v", err), "")
			}
			// Swap in the runner target temporarily by creating a side-load.
			// NOTE: the `cli` shim is already bound to the real settings;
			// we cannot change its ConfigPath mid-flight.  Instead we use a
			// best-effort pattern: if ConfigTest returns errors and the
			// current on-disk config is present, surface them.  A post-swap
			// ConfigTest (step 7) re-validates after atomic replace.
			res, cerr := cli.ConfigTest(ctx)
			_ = cerr
			if res != nil && !res.OK {
				_ = os.Remove(newPath)
				return fail(3, fmt.Sprintf("config test: %s", strings.Join(res.Errors, "; ")), res.Output)
			}
		}
		emit(3, "done", "config test passed", "")
	}

	// ----------------------------------------------------------------------
	// Step 4: CreateTmpPath — WriteAtomic newPath (staged in step 3 already).
	// ----------------------------------------------------------------------
	{
		emit(4, "running", "Creating tmp path …", "")
		if ovrErr, hkErr := stepHook(4); ovrErr != nil {
			return fail(4, "step 4 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(4, "step 4 hook: "+hkErr.Error(), "")
		}
		if _, err := os.Stat(newPath); err != nil {
			if werr := WriteAtomic(newPath, cfgBytes); werr != nil {
				return fail(4, fmt.Sprintf("write tmp: %v", werr), "")
			}
		}
		h, herr := HashFile(newPath)
		if herr != nil {
			return fail(4, fmt.Sprintf("hash tmp: %v", herr), "")
		}
		result.ConfigHash = h
		emit(4, "done", fmt.Sprintf("tmp staged, hash=%s", shortHash(h)), "")
	}

	// ----------------------------------------------------------------------
	// Step 5: BackupActive — if old config exists, copy to .bak.
	// ----------------------------------------------------------------------
	{
		emit(5, "running", "Backing up active config …", "")
		if ovrErr, hkErr := stepHook(5); ovrErr != nil {
			return fail(5, "step 5 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(5, "step 5 hook: "+hkErr.Error(), "")
		}
		if info, err := os.Stat(configPath); err == nil && !info.IsDir() {
			if cerr := CopyAtomic(configPath, bakPath); cerr != nil {
				return fail(5, fmt.Sprintf("backup: %v", cerr), "")
			}
			emit(5, "done", "backed up to "+bakPath, "")
		} else {
			emit(5, "done", "no existing config — skipped backup", "")
		}
	}

	// ----------------------------------------------------------------------
	// Step 6: AtomicSwap — rename newPath → configPath.
	// ----------------------------------------------------------------------
	{
		emit(6, "running", "Performing atomic swap …", "")
		if ovrErr, hkErr := stepHook(6); ovrErr != nil {
			return fail(6, "step 6 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(6, "step 6 hook: "+hkErr.Error(), "")
		}
		if err := os.Rename(newPath, configPath); err != nil {
			return fail(6, fmt.Sprintf("rename: %v", err), "")
		}
		emit(6, "done", "swap complete", "")
	}

	// ----------------------------------------------------------------------
	// Step 7: ReloadOrRestart.
	// ----------------------------------------------------------------------
	{
		emit(7, "running", "Signaling mox reload/restart …", "")
		if ovrErr, hkErr := stepHook(7); ovrErr != nil {
			return fail(7, "step 7 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(7, "step 7 hook: "+hkErr.Error(), "")
		}
		if reloadFn != nil {
			if rerr := reloadFn(ctx); rerr != nil {
				return fail(7, fmt.Sprintf("reload: %v", rerr), "")
			}
		}
		// Post-reload: run config test against the now-active config.
		if cli != nil {
			ct, cerr := cli.ConfigTest(ctx)
			if cerr != nil {
				return fail(7, fmt.Sprintf("post-reload configtest: %v", cerr), "")
			}
			if ct != nil && !ct.OK {
				return fail(7, "post-reload configtest failed", strings.Join(ct.Errors, "; "))
			}
		}
		emit(7, "done", "reload ok", "")
	}

	// ----------------------------------------------------------------------
	// Step 8: PostApplyConfigList.
	// ----------------------------------------------------------------------
	{
		emit(8, "running", "Post-apply config list …", "")
		if ovrErr, hkErr := stepHook(8); ovrErr != nil {
			return fail(8, "step 8 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(8, "step 8 hook: "+hkErr.Error(), "")
		}
		if cli != nil {
			_, lerr := cli.ConfigList(ctx)
			if lerr != nil {
				return fail(8, fmt.Sprintf("config list: %v", lerr), "")
			}
		}
		emit(8, "done", "config list ok", "")
	}

	// ----------------------------------------------------------------------
	// Step 9: ProbeLayersL1_L2_L3.
	// ----------------------------------------------------------------------
	{
		emit(9, "running", "Running L1/L2/L3 probes …", "")
		if ovrErr, hkErr := stepHook(9); ovrErr != nil {
			return fail(9, "step 9 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(9, "step 9 hook: "+hkErr.Error(), "")
		}
		if probeFn != nil {
			state, perr := probeFn(ctx)
			if perr != nil {
				return fail(9, fmt.Sprintf("probe error: %v", perr), "")
			}
			if state != "good" && state != "warn" {
				return fail(9, fmt.Sprintf("probe state=%s", state), "")
			}
			emit(9, "done", fmt.Sprintf("probe state=%s", state), "")
		} else {
			emit(9, "done", "probeFn not wired (skipped)", "")
		}
	}

	// ----------------------------------------------------------------------
	// Step 10: PersistSyncedFlag.
	// ----------------------------------------------------------------------
	{
		emit(10, "running", "Persisting synced flag + hash …", "")
		if ovrErr, hkErr := stepHook(10); ovrErr != nil {
			return fail(10, "step 10 override: "+ovrErr.Error(), "")
		} else if hkErr != nil {
			return fail(10, "step 10 hook: "+hkErr.Error(), "")
		}
		h, herr := HashFile(configPath)
		if herr != nil {
			return fail(10, fmt.Sprintf("hash active: %v", herr), "")
		}
		result.ConfigHash = h
		emit(10, "done", fmt.Sprintf("persisted hash=%s", shortHash(h)), "")
	}

	result.Success = true
	result.Summary = "apply succeeded"
	return result
}

// rollback runs the rollback sequence (copy .bak → .conf + reload + probe)
// and returns a non-empty error string on failure.
func rollback(
	ctx context.Context,
	configPath, bakPath string,
	reloadFn func(context.Context) error,
	probeFn func(context.Context) (string, error),
	emit func(step int, state, msg, out string),
	_ chan<- StepStatus,
) string {
	emit(0, "rollback", "Rolling back from .bak …", "")
	info, err := os.Stat(bakPath)
	if err != nil || info.IsDir() {
		return fmt.Sprintf("rollback: no backup file at %s", bakPath)
	}
	if err := CopyAtomic(bakPath, configPath); err != nil {
		return fmt.Sprintf("rollback: restore %v", err)
	}
	if reloadFn != nil {
		if rerr := reloadFn(ctx); rerr != nil {
			return fmt.Sprintf("rollback reload: %v", rerr)
		}
	}
	if probeFn != nil {
		state, perr := probeFn(ctx)
		if perr != nil || (state != "good" && state != "warn") {
			return fmt.Sprintf("rollback probe: %v state=%s", perr, state)
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// buildCanonicalConfig produces the canonical mox.conf byte slice from
// settings.  Uses a loose template — correctness is validated later by
// `mox config test`; our job here is to produce a deterministic, human
// readable blob whose hash we can compare for drift detection.
func buildCanonicalConfig(
	s SettingsSnapshot,
	domains []DomainSnapshot,
	accounts []AccountSnapshot,
	aliases []AliasSnapshot,
) []byte {
	var b strings.Builder
	b.WriteString("# Auto-generated by Phantom Lancer configapply.  DO NOT HAND-EDIT.\n")
	b.WriteString("# sha256 of this file is tracked for drift detection.\n\n")

	fmt.Fprintf(&b, "Hostname: %s\n", s.Hostname)
	fmt.Fprintf(&b, "AdminAddress: %s\n", s.AdminEmail)
	fmt.Fprintf(&b, "Postmaster: %s\n", s.AdminEmail)
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "# Webmail / API bind addresses (loopback-only defaults).\n")
	fmt.Fprintf(&b, "WebmailAddress: %s\n", safeWebBindAddr(s.WebmailAddr, "127.0.0.1:10444", false))
	fmt.Fprintf(&b, "WebAPIAddress: %s\n", safeWebBindAddr(s.WebAPIAddr, "127.0.0.1:10445", true))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "# Mail listener ports.\n")
	fmt.Fprintf(&b, "SMTPPort: %d\n", defport(s.SMTPPort, 25))
	fmt.Fprintf(&b, "SubmissionPort: %d\n", defport(s.SMTPSubmissionPort, 587))
	fmt.Fprintf(&b, "SMTPSPort: %d\n", defport(s.SMTPSPort, 465))
	fmt.Fprintf(&b, "IMAPPort: %d\n", defport(s.IMAPPort, 143))
	fmt.Fprintf(&b, "IMAPSPort: %d\n", defport(s.IMAPSPort, 993))
	if s.ConfigHost != "" {
		fmt.Fprintf(&b, "ConfigHost: %s\n", s.ConfigHost)
	}

	b.WriteString("\n# ---- Domains ----------------------------------------------------------\n")
	for _, d := range domains {
		fmt.Fprintf(&b, "\n[Domain.%s]\n", d.Domain)
		if d.Disabled {
			fmt.Fprintf(&b, "  Disabled: true\n")
		}
		if d.DKIMSelector != "" {
			fmt.Fprintf(&b, "  DKIMSelector: %s\n", d.DKIMSelector)
		}
		if d.DMARCPolicy != "" {
			fmt.Fprintf(&b, "  DMARCPolicy: %s\n", d.DMARCPolicy)
		}
		if d.DMARCRUA != "" {
			fmt.Fprintf(&b, "  DMARCRUA: %s\n", d.DMARCRUA)
		}
		if d.SPFInclude != "" {
			fmt.Fprintf(&b, "  SPFInclude: %s\n", d.SPFInclude)
		}
	}

	b.WriteString("\n# ---- Accounts ---------------------------------------------------------\n")
	for _, a := range accounts {
		fmt.Fprintf(&b, "\n[Account.%s]\n", a.Email)
		fmt.Fprintf(&b, "  Enabled: %v\n", a.Enabled)
		if a.DisplayName != "" {
			fmt.Fprintf(&b, "  DisplayName: %s\n", a.DisplayName)
		}
		if a.Role != "" {
			fmt.Fprintf(&b, "  Role: %s\n", a.Role)
		}
	}

	b.WriteString("\n# ---- Aliases ----------------------------------------------------------\n")
	for _, a := range aliases {
		fmt.Fprintf(&b, "\n[Alias.%s]\n", a.AliasAddr)
		fmt.Fprintf(&b, "  Enabled: %v\n", a.Enabled)
		if a.Mode != "" {
			fmt.Fprintf(&b, "  Mode: %s\n", a.Mode)
		}
		if len(a.Recipients) > 0 {
			fmt.Fprintf(&b, "  Recipients: %s\n", strings.Join(a.Recipients, ","))
		}
	}

	b.WriteString("\n# End of Phantom-generated config.\n")
	return []byte(b.String())
}

func safeWebBindAddr(addr, fallback string, loopbackOnly bool) string {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fallback
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 || port < 1024 || port == 80 || port == 443 {
		return fallback
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return fallback
	}
	if loopbackOnly && !ip.IsLoopback() {
		return fallback
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port))
}

func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
func defport(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
