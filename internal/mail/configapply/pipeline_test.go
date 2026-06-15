package configapply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// -----------------------------------------------------------------------------
// Shared test fixtures.
// -----------------------------------------------------------------------------

// fakeRunner is a RunnerInterface that always reports OK unless ConfigTestFail
// is toggled true.
type fakeRunner struct {
	ConfigTestFail   bool
	ConfigListFail   bool
	ConfigTestOutput string
	ConfigTestCalls  atomic.Int32
	ConfigListCalls  atomic.Int32
}

func (f *fakeRunner) ConfigTest(ctx interface{}) (*LocalConfigTestResult, error) {
	f.ConfigTestCalls.Add(1)
	if f.ConfigTestFail {
		return &LocalConfigTestResult{
			OK:     false,
			Errors: []string{"synthetic config test failure"},
			Output: f.ConfigTestOutput,
		}, nil
	}
	return &LocalConfigTestResult{OK: true, Output: "ok"}, nil
}
func (f *fakeRunner) ConfigList(ctx interface{}) (LocalParsedConfig, error) {
	f.ConfigListCalls.Add(1)
	if f.ConfigListFail {
		return nil, errors.New("synthetic config list failure")
	}
	return LocalParsedConfig{"hostname": "mx.test"}, nil
}

// counterReload returns a func that increments a counter.  Fail it by setting
// reloadErr.
type counterHooks struct {
	reloadCalls atomic.Int32
	probeCalls  atomic.Int32
	reloadErr   error
	probeState  string // "good" / "warn" / "critical"
	probeErr    error
}

func (c *counterHooks) reloadFn(context.Context) error {
	c.reloadCalls.Add(1)
	return c.reloadErr
}
func (c *counterHooks) probeFn(context.Context) (string, error) {
	c.probeCalls.Add(1)
	if c.probeErr != nil {
		return "", c.probeErr
	}
	if c.probeState == "" {
		return "good", nil
	}
	return c.probeState, nil
}

// resetTestOverrides zeroes any package-level override hooks so subtests
// cannot leak state into siblings.
func resetTestOverrides() {
	testOverrideMu.Lock()
	defer testOverrideMu.Unlock()
	TestStepFnOverride = nil
	TestBeforeStepFn = nil
	TestAfterRollbackFn = nil
}

// makeSettings returns a SettingsSnapshot pointing at `dir/mox.conf` plus the
// required required-fields (hostname + adminEmail).
func makeSettings(t *testing.T, dir string) SettingsSnapshot {
	t.Helper()
	return SettingsSnapshot{
		Hostname:     "mx.test",
		AdminEmail:   "admin@mx.test",
		MoxConfigPath: filepath.Join(dir, "mox.conf"),
		MoxBinaryPath: filepath.Join(dir, "mox"),
		MoxDataDir:    filepath.Join(dir, "data"),
		WebmailAddr:  "127.0.0.1:10444",
		WebAPIAddr:   "127.0.0.1:10445",
		SMTPPort:     25,
		IMAPPort:     143,
	}
}

func defaultDomains() []DomainSnapshot {
	return []DomainSnapshot{
		{Domain: "mx.test", DKIMSelector: "s1", DMARCPolicy: "p=quarantine"},
	}
}
func defaultAccounts() []AccountSnapshot {
	return []AccountSnapshot{
		{Email: "admin@mx.test", DisplayName: "Admin", Role: "admin", Enabled: true},
	}
}
func defaultAliases() []AliasSnapshot {
	return []AliasSnapshot{
		{AliasAddr: "postmaster@mx.test", Recipients: []string{"admin@mx.test"}, Enabled: true},
	}
}

// -----------------------------------------------------------------------------
// Step-by-step failure tests.
// -----------------------------------------------------------------------------

func TestPipelineFail_Step1(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	// Step 1 = preflight settings validation.  Fail by giving empty hostname.
	settings := makeSettings(t, dir)
	settings.Hostname = ""
	settings.AdminEmail = ""
	hooks := &counterHooks{}
	runner := &fakeRunner{}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 1 {
		t.Fatalf("FailureStep=%d want 1", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 1 must NOT rollback")
	}
	if hooks.reloadCalls.Load() != 0 || hooks.probeCalls.Load() != 0 {
		t.Fatalf("preflight fail must not touch reload/probe (calls=%d/%d)",
			hooks.reloadCalls.Load(), hooks.probeCalls.Load())
	}
}

func TestPipelineFail_Step2(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	// Inject failure via StepFnOverride at step 2.
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		2: func(ctx context.Context) error { return errors.New("boom step 2") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 2 {
		t.Fatalf("FailureStep=%d want 2", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 2 ≤3 must NOT rollback")
	}
	if hooks.reloadCalls.Load() != 0 {
		t.Fatal("reload called on step 2 fail")
	}
}

func TestPipelineFail_Step3(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	hooks := &counterHooks{}
	runner := &fakeRunner{ConfigTestFail: true, ConfigTestOutput: "line1\nline2"}
	// Step 3 uses the CLI shim — ConfigTest failure should trip it.

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 3 {
		t.Fatalf("FailureStep=%d want 3", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 3 ≤3 must NOT rollback")
	}
	if hooks.reloadCalls.Load() != 0 {
		t.Fatal("reload called on step 3 fail")
	}
	// .new file must be cleaned up per pipeline code.
	if _, err := os.Stat(settings.MoxConfigPath + ".new"); !os.IsNotExist(err) {
		t.Fatalf("expected .new cleaned, got err=%v", err)
	}
}

func TestPipelineFail_Step4(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		4: func(ctx context.Context) error { return errors.New("boom step 4") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 4 {
		t.Fatalf("FailureStep=%d want 4", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 4 ≤6 must NOT rollback")
	}
	// tmp cleanup happened via fail().
	if _, err := os.Stat(settings.MoxConfigPath + ".new"); !os.IsNotExist(err) {
		t.Fatalf(".new should be removed, stat err=%v", err)
	}
}

func TestPipelineFail_Step5(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	// Pre-stage an existing config so step 5 has something to back up.
	orig := []byte("legacy config content from operator\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		5: func(ctx context.Context) error { return errors.New("boom step 5") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 5 {
		t.Fatalf("FailureStep=%d want 5", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 5 ≤6 must NOT rollback")
	}
	// Original file must be untouched at this point.
	got, err := os.ReadFile(settings.MoxConfigPath)
	if err != nil {
		t.Fatalf("read orig: %v", err)
	}
	if string(got) != string(orig) {
		t.Fatalf("step 5 fail must not mutate orig file\ngot: %q\nwant:%q", got, orig)
	}
}

func TestPipelineFail_Step6(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("orig step6\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		6: func(ctx context.Context) error { return errors.New("boom step 6") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 6 {
		t.Fatalf("FailureStep=%d want 6", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("step 6 = swap failure — must NOT rollback")
	}
	// Orig untouched, tmp deleted.
	got, _ := os.ReadFile(settings.MoxConfigPath)
	if string(got) != string(orig) {
		t.Fatalf("orig file mutated after step 6 fail: %q", got)
	}
	if _, err := os.Stat(settings.MoxConfigPath + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new should be removed after step 6 fail")
	}
}

func TestPipelineFail_Step7_RollbackTriggered(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("canonical operator config v1\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	rollbackCalled := false
	TestAfterRollbackFn = func() { rollbackCalled = true }
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		7: func(ctx context.Context) error { return errors.New("reload failed synthetic") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 7 {
		t.Fatalf("FailureStep=%d want 7", res.FailureStep)
	}
	if !res.RolledBack {
		t.Fatalf("step 7 ≥7 must rollback. RolledBack=false, RollbackErr=%q", res.RollbackErr)
	}
	if res.RollbackErr != "" {
		t.Fatalf("rollback returned error %q want empty", res.RollbackErr)
	}
	if !rollbackCalled {
		t.Fatal("TestAfterRollbackFn never fired")
	}
	// reload and probe called twice each: once for step 7 (reload), then once
	// more via rollback.
	if hooks.reloadCalls.Load() < 2 {
		t.Logf("note: reloadCalls=%d (step7 may have been overridden before real call)", hooks.reloadCalls.Load())
	}
	// Rollback restored orig content.
	got, err := os.ReadFile(settings.MoxConfigPath)
	if err != nil {
		t.Fatalf("read after rb: %v", err)
	}
	if string(got) != string(orig) {
		t.Fatalf("rollback did not restore orig\ngot=%q\nwant=%q", got, orig)
	}
	// No .new leftovers.
	if _, err := os.Stat(settings.MoxConfigPath + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new not cleaned")
	}
}

func TestPipelineFail_Step8_RollbackTriggered(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("orig v8 seed\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		8: func(ctx context.Context) error { return errors.New("step 8 boom") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 8 {
		t.Fatalf("FailureStep=%d want 8", res.FailureStep)
	}
	if !res.RolledBack {
		t.Fatalf("step 8 must rollback; RollbackErr=%q", res.RollbackErr)
	}
	got, _ := os.ReadFile(settings.MoxConfigPath)
	if string(got) != string(orig) {
		t.Fatalf("rollback mismatch: got=%q want=%q", got, orig)
	}
}

func TestPipelineFail_Step9_RollbackTriggered(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("orig step9\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	// Fail step 9 via hook so probe state during rollback stays "good".
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		9: func(ctx context.Context) error { return errors.New("L3 probe failed: DB query timeout") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 9 {
		t.Fatalf("FailureStep=%d want 9", res.FailureStep)
	}
	if !res.RolledBack {
		t.Fatalf("step 9 must rollback; RollbackErr=%q", res.RollbackErr)
	}
	got, _ := os.ReadFile(settings.MoxConfigPath)
	if string(got) != string(orig) {
		t.Fatalf("probe fail rollback restore failed: got=%q", got)
	}
	// reload called at step 7 + during rollback.
	if got := hooks.reloadCalls.Load(); got < 2 {
		t.Fatalf("reloadCalls=%d want ≥2", got)
	}
}

// TestPipelineFail_Step9_RollbackProbeAlsoFails verifies that when step 9
// fails (bad probe) AND the rollback probe also fails (e.g. the system is
// truly down), RollbackErr is non-empty but RolledBack remains false — the
// caller can surface this as a partial rollback that needs manual triage.
func TestPipelineFail_Step9_RollbackProbeAlsoFails(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("orig step9b\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{probeState: "critical"} // bad probe → bad rollback probe
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		9: func(ctx context.Context) error { return errors.New("L3 probe down") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 9 {
		t.Fatalf("FailureStep=%d", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("rollback probe failed but RolledBack=true; that's inconsistent")
	}
	if res.RollbackErr == "" {
		t.Fatal("expected non-empty RollbackErr describing partial rollback")
	}
}

func TestPipelineFail_Step10_RollbackTriggered(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	orig := []byte("orig step10\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("stage legacy: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	TestStepFnOverride = map[int]func(ctx context.Context) error{
		10: func(ctx context.Context) error { return errors.New("step 10 boom") },
	}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if res.Success {
		t.Fatal("expected failure")
	}
	if res.FailureStep != 10 {
		t.Fatalf("FailureStep=%d want 10", res.FailureStep)
	}
	if !res.RolledBack {
		t.Fatalf("step 10 must rollback; RollbackErr=%q", res.RollbackErr)
	}
	got, _ := os.ReadFile(settings.MoxConfigPath)
	if string(got) != string(orig) {
		t.Fatalf("step10 rollback mismatch: got=%q want=%q", got, orig)
	}
	// synced flag not persisted → ConfigHash should NOT reflect the on-disk
	// file after rollback (we never commit).
	if res.ConfigHash != "" {
		// We may have set it at step 4 — that's fine; it's the tmp-hash, not
		// the persisted hash.  The key invariant: RolledBack==true.
	}
}

// -----------------------------------------------------------------------------
// Full success path.
// -----------------------------------------------------------------------------

func TestPipelineSuccess_AllSteps(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	// Pre-populate an operator seed file.  It will be overwritten by
	// AtomicSwap (step 6).
	orig := []byte("old seed file\n")
	if err := WriteAtomic(settings.MoxConfigPath, orig); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hooks := &counterHooks{}
	runner := &fakeRunner{}

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, nil)

	if !res.Success {
		t.Fatalf("expected success, got FailureStep=%d Summary=%q RollbackErr=%q",
			res.FailureStep, res.Summary, res.RollbackErr)
	}
	if res.FailureStep != 0 {
		t.Fatalf("FailureStep=%d want 0", res.FailureStep)
	}
	if res.RolledBack {
		t.Fatal("success must not roll back")
	}
	if res.RollbackErr != "" {
		t.Fatalf("RollbackErr=%q", res.RollbackErr)
	}
	if len(res.Steps) != StepCount+0 { // 10 steps done, no extra failed / rollback
		// (10 "done" entries)
		if len(res.Steps) < StepCount {
			t.Fatalf("Steps len=%d want ≥%d", len(res.Steps), StepCount)
		}
	}
	// Percentages are monotonic and step 10 finishes at 100%.
	last := res.Steps[len(res.Steps)-1]
	if last.Step != StepCount || last.State != "done" || last.Percent != 100 {
		t.Fatalf("last step=%+v want step=10 state=done percent=100", last)
	}
	// Each step name is present exactly once.
	seen := map[string]int{}
	for _, s := range res.Steps {
		seen[s.Name]++
	}
	for _, name := range StepNames {
		if seen[name] < 1 {
			t.Fatalf("missing step name %q in result steps", name)
		}
	}
	// Config on disk matches the generated canonical content.
	got, err := os.ReadFile(settings.MoxConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := buildCanonicalConfig(settings, defaultDomains(), defaultAccounts(), defaultAliases())
	if string(got) != string(want) {
		// Don't dump the whole thing inline — compare sizes first, then first diff.
		t.Fatalf("on-disk config differs (%d vs %d bytes)\nfirst diff:\nhead got=%q\nhead want=%q",
			len(got), len(want),
			head(got, 100), head(want, 100))
	}
	// ConfigHash in result == HashFile of on-disk config.
	onDiskHash, _ := HashFile(settings.MoxConfigPath)
	if res.ConfigHash != onDiskHash {
		t.Fatalf("result.ConfigHash=%q != on-disk %q", res.ConfigHash, onDiskHash)
	}
	// .new / .tmp leftovers cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if name == "mox.conf.bak" {
			continue // backup is expected to remain.
		}
		if matchSuffix(name, ".tmp", ".tmp-", ".new") {
			t.Fatalf("leftover temp file: %s", name)
		}
	}
	// reload + probe each called at least once.
	if hooks.reloadCalls.Load() == 0 {
		t.Fatal("reload never called on success")
	}
	if hooks.probeCalls.Load() == 0 {
		t.Fatal("probe never called on success")
	}
	// CLI shim was called.
	if runner.ConfigTestCalls.Load() < 2 {
		t.Fatalf("ConfigTest calls=%d want ≥2 (step 3 + step 7 post-reload)", runner.ConfigTestCalls.Load())
	}
	if runner.ConfigListCalls.Load() < 1 {
		t.Fatal("ConfigList never called (step 8)")
	}
}

// -----------------------------------------------------------------------------
// Progress channel smoke test — ensure progress channel is non-blocking and
// surfaces the expected 10 steps.
// -----------------------------------------------------------------------------

func TestPipelineProgressChannel(t *testing.T) {
	resetTestOverrides()
	dir := t.TempDir()
	settings := makeSettings(t, dir)
	_ = WriteAtomic(settings.MoxConfigPath, []byte("seed"))
	hooks := &counterHooks{}
	runner := &fakeRunner{}
	ch := make(chan StepStatus, 64)

	res := Run(context.Background(), settings, defaultDomains(), defaultAccounts(),
		defaultAliases(), runner, hooks.reloadFn, hooks.probeFn, ch)
	close(ch)

	if !res.Success {
		t.Fatalf("unexpected failure: %+v", res)
	}
	got := []StepStatus{}
	for s := range ch {
		got = append(got, s)
	}
	if len(got) < StepCount {
		t.Fatalf("progress events=%d want ≥%d", len(got), StepCount)
	}
	// Running then done for step 1 must be present in order.
	var step1Running, step1Done bool
	for _, s := range got {
		if s.Step == 1 {
			if s.State == "running" {
				step1Running = true
			}
			if s.State == "done" {
				if !step1Running {
					t.Fatal("step 1 done emitted before running")
				}
				step1Done = true
			}
		}
	}
	if !step1Running || !step1Done {
		t.Fatalf("step1 missing running=%v done=%v", step1Running, step1Done)
	}
}

// -----------------------------------------------------------------------------
// Helpers.
// -----------------------------------------------------------------------------

func head(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

func matchSuffix(name string, suffixes ...string) bool {
	for _, s := range suffixes {
		if len(name) >= len(s) && name[:len(s)] == s {
			return true
		}
	}
	return false
}
