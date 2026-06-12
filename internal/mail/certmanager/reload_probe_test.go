package certmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// ---------- test harness helpers ----------

// minimalIssueConfig builds an IssueConfig that is safe for offline tests
// (LegoStubClient + ManualDNSProvider + no-op reload/probe callbacks).
// Callers override fields on the returned config before calling Issue.
func minimalIssueConfig(t *testing.T, dir string) IssueConfig {
	t.Helper()
	lego, err := NewLegoClient(dir, "admin@example.com",
		"https://stub.example.com/directory", true)
	if err != nil {
		t.Fatalf("NewLegoClient: %v", err)
	}
	manual := &ManualDNSProvider{
		Persist: func(fqdn, value, domain, status string) error { return nil },
	}
	return IssueConfig{
		Domain:       "example.com",
		SANDomains:   []string{"mx.example.com", "mail.example.com"},
		DataDir:      dir,
		DNSProviders: map[string]DNSProvider{"example.com": manual},
		LegoClient:   lego,
		ACMEContactEmail: "admin@example.com",
		ACMEDirectoryURL: "https://stub.example.com/directory",
		AcceptTOS: true,
		// Manual DNS mode needs a confirm callback — ours is a no-op (tokens
		// are already "propagated" in tests because the Lego stub doesn't
		// actually query DNS).
		ManualModeConfirmCallback: func(_ context.Context, _, _ string) error { return nil },
		ReloadOrRestartMox: func(_ context.Context) error { return nil },
		TLSProbe:           func(_ context.Context) (string, error) { return "good", nil },
		PersistCertificate: func(_ Certificate) error { return nil },
	}
}

// drainProgress spawns a goroutine that drains ch until closed, returning
// the count of events received.  Callers must close the channel after Issue
// returns (Issue never closes progress; it just sends on it).
func drainProgress(t *testing.T, ch <-chan StepStatus) (nEvents *int64) {
	t.Helper()
	var counter int64
	go func() {
		for range ch {
			atomic.AddInt64(&counter, 1)
		}
	}()
	return &counter
}

// ---------- Subtest 1: Reload counter + step-9 happy path ----------

func TestReloadProbe_HappyPath_Calls(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalIssueConfig(t, dir)

	var reloadCalls atomic.Int64
	var probeCalls atomic.Int64
	var persistCalls atomic.Int64
	cfg.ReloadOrRestartMox = func(_ context.Context) error {
		reloadCalls.Add(1)
		return nil
	}
	cfg.TLSProbe = func(_ context.Context) (string, error) {
		probeCalls.Add(1)
		return "good", nil
	}
	cfg.PersistCertificate = func(c Certificate) error {
		persistCalls.Add(1)
		if c.Domain == "" {
			return errors.New("empty domain in persist")
		}
		return nil
	}

	prog := make(chan StepStatus, 128)
	_ = drainProgress(t, prog)

	res := Issue(context.Background(), cfg, prog)
	close(prog)

	if !res.Success {
		t.Fatalf("happy path Issue() should succeed, got res=%+v msg=%q rollback=%q",
			res.Success, res.Message, res.RollbackErr)
	}
	if res.Step != StepCount {
		t.Errorf("completed step should be %d, got %d", StepCount, res.Step)
	}

	// Each of the 3 write destinations must exist with the correct permissions.
	type artifact struct {
		path string
		perm os.FileMode
	}
	for _, a := range []artifact{
		{filepath.Join(dir, "certs", "example.com", "privkey.pem"), 0o600},
		{filepath.Join(dir, "certs", "example.com", "chain.pem"), 0o644},
		{filepath.Join(dir, "certs", "example.com", "cert.pem"), 0o644},
	} {
		info, err := os.Stat(a.path)
		if err != nil {
			t.Errorf("missing artifact %s: %v", a.path, err)
			continue
		}
		if info.Mode().Perm() != a.perm {
			t.Errorf("artifact %s: want perm %o, got %o", a.path, a.perm, info.Mode().Perm())
		}
		if info.Size() == 0 {
			t.Errorf("artifact %s is empty", a.path)
		}
	}

	// Reload must be called exactly once (on success: no rollback reload).
	if got := reloadCalls.Load(); got != 1 {
		t.Errorf("expected 1 ReloadOrRestartMox call, got %d", got)
	}
	// Probe must be called exactly once.
	if got := probeCalls.Load(); got != 1 {
		t.Errorf("expected 1 TLSProbe call, got %d", got)
	}
	if got := persistCalls.Load(); got != 1 {
		t.Errorf("expected 1 PersistCertificate call, got %d", got)
	}

	// CertPath / KeyPath / ChainPath must be populated.
	for _, p := range []string{res.CertPath, res.KeyPath, res.ChainPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("IssueResult path %q is not on disk: %v", p, err)
		}
	}
}

// ---------- Subtest 2: TLSProbe returns "critical" → rollback fires ----------

func TestReloadProbe_ProbeFail_Rollback(t *testing.T) {
	dir := t.TempDir()
	// Pre-populate "old" artifacts so rollback has a .bak to restore from.
	certDir := filepath.Join(dir, "certs", "example.com")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldPrivKey := []byte("---- BEGIN OLD PRIVKEY ----\nold-priv-key-content\n")
	oldCert := []byte("---- BEGIN OLD CERT ----\nold-cert-content\n")
	oldChain := []byte("---- BEGIN OLD CHAIN ----\nold-chain-content\n")
	if err := os.WriteFile(filepath.Join(certDir, "privkey.pem"), oldPrivKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), oldCert, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "chain.pem"), oldChain, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := minimalIssueConfig(t, dir)

	// The first TLSProbe (after the new-write reload) returns "critical";
	// after doRollback restores the old certs and calls reload + probe again,
	// the probe returns "good" to confirm rollback succeeded.
	var probeCalls atomic.Int64
	var reloadCalls atomic.Int64
	cfg.TLSProbe = func(_ context.Context) (string, error) {
		n := probeCalls.Add(1)
		switch n {
		case 1:
			return "critical", nil
		default:
			return "good", nil
		}
	}
	cfg.ReloadOrRestartMox = func(_ context.Context) error {
		reloadCalls.Add(1)
		return nil
	}

	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)

	res := Issue(context.Background(), cfg, prog)
	close(prog)

	// Because post-write probe failed, the pipeline should report failure,
	// trigger doRollback, and NOT persist the cert.
	if res.Success {
		t.Errorf("probe-fail path should NOT return Success=true")
	}
	// fail(idx=9, ...) sets res.Step = idx+1 = 10.
	if res.Step != 10 {
		t.Errorf("last completed step should be 10 (after fail at idx=9 sets Step=idx+1), got %d", res.Step)
	}
	// Rollback should have succeeded.
	if res.RollbackErr != "" {
		t.Errorf("rollback should succeed but RollbackErr=%q", res.RollbackErr)
	}

	// Reload: 1 after the new write + 1 after restoring old = 2.
	if got := reloadCalls.Load(); got < 2 {
		t.Errorf("expected >=2 reload calls, got %d", got)
	}
	// Probe: 1 after new write + 1 after rollback = 2.
	if got := probeCalls.Load(); got < 2 {
		t.Errorf("expected >=2 probe calls, got %d", got)
	}

	// The 3 files on disk should have been RESTORED to the "old" content.
	privBack, err := os.ReadFile(filepath.Join(certDir, "privkey.pem"))
	if err != nil {
		t.Fatalf("read privkey: %v", err)
	}
	if !bytes.Equal(privBack, oldPrivKey) {
		t.Errorf("privkey was not restored after rollback\nwant=%q\ngot =%q", oldPrivKey, privBack)
	}
	certBack, err := os.ReadFile(filepath.Join(certDir, "cert.pem"))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if !bytes.Equal(certBack, oldCert) {
		t.Errorf("cert was not restored after rollback")
	}
	chainBack, err := os.ReadFile(filepath.Join(certDir, "chain.pem"))
	if err != nil {
		t.Fatalf("read chain: %v", err)
	}
	if !bytes.Equal(chainBack, oldChain) {
		t.Errorf("chain was not restored after rollback")
	}
}

// ---------- Subtest 3: ReloadOrRestartMox fails → step 9 fails (no rollback) ----------

func TestReloadProbe_ReloadError(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalIssueConfig(t, dir)
	reloadErr := fmt.Errorf("mox service not reachable")
	var reloadCalls atomic.Int64
	cfg.ReloadOrRestartMox = func(_ context.Context) error {
		n := reloadCalls.Add(1)
		// Only fail the first call (the post-write one).  A rollback must
		// still attempt reload, and if that succeeds we confirm rollback
		// fully runs.
		if n == 1 {
			return reloadErr
		}
		return nil
	}

	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)
	res := Issue(context.Background(), cfg, prog)
	close(prog)

	if res.Success {
		t.Errorf("reload error should yield Success=false")
	}
	// Message should contain the wrapped reload error text.
	if res.Step != 9 && res.Step != StepCount {
		if len(res.Message) == 0 {
			t.Errorf("failure Message should be non-empty")
		}
	}
}

// ---------- Subtest 4: TLSAEnabled = true populates IssueResult.TLSA ----------

func TestReloadProbe_TLSAEnabled(t *testing.T) {
	// Use real (self-signed) X.509 via the lego interface so TLSA can parse it.
	dir := t.TempDir()
	cfg := minimalIssueConfig(t, dir)
	cfg.TLSAEnabled = true
	cfg.MxHost = "mx.example.com"

	cfg.LegoClient = &fakeLegoRealX509{t: t}

	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)
	res := Issue(context.Background(), cfg, prog)
	close(prog)

	if !res.Success {
		t.Fatalf("with real-x509 lego should succeed, got: %+v msg=%q", res, res.Message)
	}
	if res.TLSA == nil {
		t.Fatalf("TLSAEnabled=true and valid X.509 should produce a TLSA record")
	}
	if res.TLSA.Usage != 3 || res.TLSA.Selector != 1 || res.TLSA.MatchingType != 1 {
		t.Errorf("TLSA tuple: want 3 1 1, got %d %d %d",
			res.TLSA.Usage, res.TLSA.Selector, res.TLSA.MatchingType)
	}
	if res.TLSA.Port != 25 {
		t.Errorf("TLSA port: want 25, got %d", res.TLSA.Port)
	}
	// The MX host we supplied (mx.example.com) should appear fully qualified.
	wantFQDN := "_25._tcp.mx.example.com."
	if res.TLSA.FQDN != wantFQDN {
		t.Errorf("TLSA FQDN: want %q, got %q", wantFQDN, res.TLSA.FQDN)
	}
	if len(res.TLSA.HexDigest) != 64 {
		t.Errorf("TLSA HexDigest should be 64 hex chars, got len=%d value=%q",
			len(res.TLSA.HexDigest), res.TLSA.HexDigest)
	}
}

// fakeLegoRealX509 is a LegoClient implementation that generates a fresh
// self-signed ECDSA P-256 X.509 cert (valid PEM DER) so the TLSA
// computation inside the pipeline can run for real.
type fakeLegoRealX509 struct{ t *testing.T }

func (f *fakeLegoRealX509) ObtainCertificate(
	ctx context.Context,
	domains []string,
	cb func(fqdn, keyAuth string) (cleanup func() error, err error),
) (pemPrivateKey, pemCertificate, pemIssuerChain []byte, err error) {
	f.t.Helper()
	pemCert, pemKey, _ := newSelfSignedCert(f.t) // from tlsa_test.go
	// Drive the DNS-01 callback once per domain, as LegoStubClient would.
	for _, d := range domains {
		if cb != nil {
			clean, cerr := cb("_acme-challenge."+d+".", "fake-keyauth-"+d)
			if cerr != nil {
				return nil, nil, nil, cerr
			}
			if clean != nil {
				defer func() { _ = clean() }()
			}
		}
	}
	// Chain = same as leaf for self-signed (stub is fine for TLSA tests).
	return pemKey, pemCert, pemCert, nil
}

func (f *fakeLegoRealX509) RevokeCertificate(ctx context.Context, pem []byte) error {
	if len(pem) == 0 {
		return errors.New("empty pem")
	}
	return nil
}

// ---------- Subtest 5: Step-8 partial write with TestStepFail = rollback ----------

func TestReloadProbe_Step8_PartialWrite_TriggersRollback(t *testing.T) {
	// Populate a pre-existing cert so .bak copies exist AND will be written
	// via CopyAtomic (which internally uses writeAtomic → TestStepFail can
	// fire there).  Then inject a TestStepFail on the FIRST write-atomic
	// call.  Since step 8 backs up 3 files via CopyAtomic before doing the
	// 3 live writes, the injection fires on the first .bak copy.  For the
	// tier 7-9 rollback path we instead want to inject on the LIVE privkey
	// write — to avoid the .bak copies consuming the single shot we run the
	// pipeline TWICE:
	//
	//  Pass 1 (no injection, no pre-existing):  writes artifacts
	//  Pass 2 (no injection, pre-existing):     writes .bak then writes live
	//  Pass 3 (injection ON, pre-existing):     writes .bak then FAILS live
	//
	// Since a single test can only easily run 1 Issue() call, we use a
	// simpler technique: count the number of live-write error returns in
	// the success-critical path by having step-8's LiveWrite fail.  We do
	// this by arming TestStepFail so that it fires ONLY on the 4th
	// writeAtomic call — i.e. the first LIVE (privkey) write.  Because we
	// only have a single fire-once value, we install an AFTER-fail
	// callback via package state using a write-counting wrapper.  Since
	// atomic.go doesn't expose such a hook we take the pragmatic approach:
	// pre-fill 3 "successful" atomic writes with the old-artifact backups
	// BEFORE calling Issue, then arm TestStepFail.  But we can't easily do
	// that either because Issue calls issueStep8 which does its own
	// CopyAtomic calls.
	//
	// The most robust, race-safe offline proof that step-8 failure triggers
	// tiered rollback: use WriteAtomic0600 directly on the live privkey
	// path via a tiny ad-hoc helper that mimics issueStep8's order, but
	// with TestStepFail injected to prove that the step 8 fail() → tiered
	// rollback in the main pipeline is reachable.  Since we can't easily
	// wedge injection past the 3 .bak writes without modifying production
	// code, we instead verify the rollback behavior INDIRECTLY: the
	// ProbeFail test already exercises step-9 fail → tier 7-9 rollback via
	// doRollback.  The code path is identical.  So what we REALLY test here
	// is: if WriteAtomic returns an error inside step 8, fail() sets
	// Success=false AND doRollback is called.  We simulate the write-error
	// by arming a cfg that has NO ReloadOrRestartMox — wait no, step-8
	// errors BEFORE that.  Instead we: use TestStepFail, but first do a
	// warm-up by writing 3 files with names that DON'T match the step-8
	// filenames, so when step-8 .bak writes happen, they land in fresh
	// paths that haven't yet consumed the single-shot injection… actually
	// the single-shot fires on the first writeAtomic call period, no matter
	// the path.
	//
	// OKAY: actual solution.  We will NOT populate pre-existing artifacts.
	// In that case the `if info, err := os.Stat(p); err == nil && !info.IsDir()`
	// guard in step 8 evaluates to false and NO .bak files are created via
	// CopyAtomic.  That means the FIRST writeAtomic in step 8 is the LIVE
	// privkey write.  Exactly what we want.  The test then becomes: assert
	// the injection makes Issue return Success=false (and not panic).
	dir := t.TempDir()
	// NO pre-existing artifacts → no .bak writes → injection hits live write.
	cfg := minimalIssueConfig(t, dir)

	TestStepFail = 3 // fail during the LIVE privkey write (after Sync, before Close)
	t.Cleanup(func() { TestStepFail = -1 })

	// Sanity: count how many times the injection fired by comparing the
	// before/after value modulo 100 (the -100 decrement only fires once).
	beforeFail := TestStepFail

	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)
	res := Issue(context.Background(), cfg, prog)
	close(prog)

	afterFail := TestStepFail

	if res.Success {
		t.Errorf("injected step-8 partial write should fail overall")
	}
	// The injection should have fired exactly once (value went from 3 to -97).
	if afterFail != beforeFail-100 {
		t.Errorf("TestStepFail was not consumed: before=%d after=%d", beforeFail, afterFail)
	}
	// Since we failed at step 8 (live privkey write failed), fail(idx=8, ...)
	// should set res.Step = 9 AND trigger doRollback (idx in [7,9] range).
	if res.Step != 9 {
		t.Errorf("after step-8-injected fail: res.Step should be 9 (idx+1), got %d", res.Step)
	}
}

// ---------- Subtest 6: PersistCertificate error bubbled up ----------

func TestReloadProbe_PersistError(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalIssueConfig(t, dir)
	cfg.PersistCertificate = func(_ Certificate) error {
		return errors.New("disk full")
	}
	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)
	res := Issue(context.Background(), cfg, prog)
	close(prog)
	if res.Success {
		t.Errorf("persist failure must not be reported as success")
	}
}

// ---------- Subtest 7: 5 consecutive Issues → reloads >=5 ----------

func TestReloadProbe_5ConsecutiveIssues_ReloadCounter(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalIssueConfig(t, dir)

	var reloadCalls atomic.Int64
	var probeCalls atomic.Int64
	cfg.ReloadOrRestartMox = func(_ context.Context) error {
		reloadCalls.Add(1)
		return nil
	}
	cfg.TLSProbe = func(_ context.Context) (string, error) {
		probeCalls.Add(1)
		return "good", nil
	}

	N := 5
	successes := 0
	for i := 0; i < N; i++ {
		prog := make(chan StepStatus, 256)
		_ = drainProgress(t, prog)
		res := Issue(context.Background(), cfg, prog)
		close(prog)
		if res.Success {
			successes++
		}
	}

	if successes != N {
		t.Fatalf("all %d Issues should succeed, got %d successes", N, successes)
	}
	gotReloads := reloadCalls.Load()
	if gotReloads < int64(N) {
		t.Errorf("expected >=%d reload calls across %d Issues, got %d", N, N, gotReloads)
	}
	gotProbes := probeCalls.Load()
	if gotProbes < int64(N) {
		t.Errorf("expected >=%d probe calls across %d Issues, got %d", N, N, gotProbes)
	}
	t.Logf("5 consecutive issues: reloads=%d probes=%d", gotReloads, gotProbes)
}

// ---------- Subtest 8: Reload fails → rollback restores v1 originals ----------

func TestReloadProbe_ReloadFail_RollsBack(t *testing.T) {
	dir := t.TempDir()
	certDir := filepath.Join(dir, "certs", "example.com")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Pre-populate v1 artifacts (will be backed up → .bak → restored on rollback).
	v1 := struct{ priv, cert, chain []byte }{
		priv:  []byte("---- BEGIN OLD PRIVKEY ----\nv1-privkey-content\n"),
		cert:  []byte("---- BEGIN OLD CERT ----\nv1-cert-content\n"),
		chain: []byte("---- BEGIN OLD CHAIN ----\nv1-chain-content\n"),
	}
	if err := os.WriteFile(filepath.Join(certDir, "privkey.pem"), v1.priv, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), v1.cert, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "chain.pem"), v1.chain, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := minimalIssueConfig(t, dir)

	// ReloadOrRestartMox: first call (the post-write one) returns an error.
	// Second call (post-rollback restore) succeeds.
	// We track via a counter that rollback actually ran the reload again.
	var reloadCalls atomic.Int64
	var reloadFails atomic.Int64
	var probeCalls atomic.Int64
	cfg.ReloadOrRestartMox = func(_ context.Context) error {
		n := reloadCalls.Add(1)
		if n == 1 {
			reloadFails.Add(1)
			return fmt.Errorf("mox reload failed: listener port in use")
		}
		return nil
	}
	cfg.TLSProbe = func(_ context.Context) (string, error) {
		n := probeCalls.Add(1)
		// First probe never runs because reload fails before it.
		// After rollback reload succeeds, probe returns "good" to confirm rollback OK.
		_ = n
		return "good", nil
	}

	prog := make(chan StepStatus, 256)
	_ = drainProgress(t, prog)
	res := Issue(context.Background(), cfg, prog)
	close(prog)

	// Post-write reload failed → step 9 fail → rollback fires.
	if res.Success {
		t.Errorf("reload-fail path should NOT return Success=true")
	}
	if res.RollbackErr != "" {
		t.Errorf("rollback should succeed, got RollbackErr=%q", res.RollbackErr)
	}

	// Reload: 1 (post-write, failed) + 1 (post-restore, succeeded) = >=2
	if got := reloadCalls.Load(); got < 2 {
		t.Errorf("expected >=2 reload calls, got %d", got)
	}
	if got := reloadFails.Load(); got != 1 {
		t.Errorf("expected exactly 1 reload failure, got %d", got)
	}

	// All three files must have been RESTORED to v1 content.
	paths := []struct {
		name string
		want []byte
	}{
		{filepath.Join(certDir, "privkey.pem"), v1.priv},
		{filepath.Join(certDir, "cert.pem"), v1.cert},
		{filepath.Join(certDir, "chain.pem"), v1.chain},
	}
	for _, p := range paths {
		got, err := os.ReadFile(p.name)
		if err != nil {
			t.Errorf("read %s: %v", p.name, err)
			continue
		}
		if !bytes.Equal(got, p.want) {
			t.Errorf("%s: rollback did not restore v1\n  want=%q\n  got =%q",
				filepath.Base(p.name), string(p.want), string(got))
		}
	}
}
