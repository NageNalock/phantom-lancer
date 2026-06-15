package configapply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// -----------------------------------------------------------------------------
// DriftDetector baseline tests (single-file, in-memory hash — no real DB).
// -----------------------------------------------------------------------------

func TestDriftDetect_NoDrift(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")
	seed := []byte("Hostname: mx.test\nAdminAddress: a@mx.test\n")
	if err := WriteAtomic(cfgPath, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedHash := HashBytes(seed)

	d := NewDriftDetector(cfgPath, seedHash)
	drifted, diskHash, err := d.Refresh()
	if err != nil {
		t.Fatalf("Refresh err: %v", err)
	}
	if drifted {
		t.Fatal("seed == sqliteHash but drifted=true")
	}
	if diskHash != seedHash {
		t.Fatalf("diskHash=%s != %s", diskHash, seedHash)
	}
	if d.Drifted() {
		t.Fatal("Drifted() accessor returned true after no-drift Refresh")
	}
	if d.ExpectedHash() != seedHash {
		t.Fatalf("ExpectedHash=%q != seedHash=%q", d.ExpectedHash(), seedHash)
	}
	if d.SQLiteHash() != seedHash {
		t.Fatalf("SQLiteHash mismatch: %q vs %q", d.SQLiteHash(), seedHash)
	}
}

// TestDriftDetect_NoBaseline verifies that when sqliteHash is empty, the
// current disk content becomes the authoritative baseline on first Refresh.
func TestDriftDetect_NoBaseline(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")
	seed := []byte("brand new install seed\n")
	if err := WriteAtomic(cfgPath, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	d := NewDriftDetector(cfgPath, "") // empty sqliteHash: first boot
	drifted, _, err := d.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if drifted {
		t.Fatal("first-boot empty hash should not flag drift")
	}
	if d.SQLiteHash() != HashBytes(seed) {
		t.Fatalf("SQLiteHash should have been set to disk hash")
	}
	if d.DiskHash() != HashBytes(seed) {
		t.Fatalf("DiskHash wrong")
	}
}

func TestDriftDetect_Drifted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")
	seed := []byte("Hostname: mx.test\nAdminAddress: a@mx.test\n")
	if err := WriteAtomic(cfgPath, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedHash := HashBytes(seed)

	d := NewDriftDetector(cfgPath, seedHash)

	// Simulate an operator hand-editing the file on disk.
	changed := append([]byte(nil), seed...)
	changed = append(changed, []byte("# manual edit line\n")...)
	if err := WriteAtomic(cfgPath, changed); err != nil {
		t.Fatalf("write changed: %v", err)
	}

	drifted, diskHash, err := d.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !drifted {
		t.Fatal("file changed but drifted=false")
	}
	if diskHash == seedHash {
		t.Fatal("disk hash still equal to seed after mutation")
	}
	if diskHash != HashBytes(changed) {
		t.Fatalf("diskHash doesn't reflect actual content")
	}
	if !d.Drifted() {
		t.Fatal("Drifted() accessor should reflect the last Refresh")
	}
	// ConfigPath accessor for status-UI rendering.
	if d.ConfigPath() != cfgPath {
		t.Fatalf("ConfigPath()=%q want %q", d.ConfigPath(), cfgPath)
	}
	// LastCheck set to a recent RFC3339 string.
	if d.LastCheck() == "" {
		t.Fatal("LastCheck empty after Refresh")
	}
}

// TestDriftDetect_MissingFile verifies the detector does not crash on a
// missing config file (e.g. fresh install, or file was deleted).
func TestDriftDetect_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "no-such-file.conf")

	d := NewDriftDetector(cfgPath, "deadbeef")
	drifted, diskHash, err := d.Refresh()
	if err == nil {
		t.Fatal("expected error reading missing file")
	}
	_ = drifted
	_ = diskHash
}

// -----------------------------------------------------------------------------
// Resolve-flow tests.
//
// The DriftDetector itself only exposes SetSynced(); the heavy lifting for
// "overwrite from DB" vs "sync from disk" is handled by the service layer
// (re-running the 10-step pipeline, or re-importing disk content).  These
// tests model both resolve modes at the detector + disk layer to lock in the
// correct semantics for each action.
// -----------------------------------------------------------------------------

// buildCanonicalSnapshot returns the bytes the DB would produce for the
// "authoritative" snapshot plus its hash.
func buildCanonicalSnapshot(_ *testing.T, seedID string) ([]byte, string) {
	b := []byte("# canonical v" + seedID + "\nHostname: mx.test\nAdminAddress: a@mx.test\n")
	return b, HashBytes(b)
}

func TestDriftResolve_OverwriteFromDB(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")

	// Phase A: baseline install.
	dbContent, dbHash := buildCanonicalSnapshot(t, "1")
	if err := WriteAtomic(cfgPath, dbContent); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := NewDriftDetector(cfgPath, dbHash)
	if drifted, _, err := d.Refresh(); err != nil || drifted {
		t.Fatalf("baseline drift=%v err=%v", drifted, err)
	}

	// Phase B: operator hand-edits the file → drift.
	tampered := append([]byte(nil), dbContent...)
	tampered = append(tampered, []byte("ManualOverride: true\n")...)
	if err := WriteAtomic(cfgPath, tampered); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	drifted, _, err := d.Refresh()
	if err != nil || !drifted {
		t.Fatalf("expected drift, got drifted=%v err=%v", drifted, err)
	}
	if !d.Drifted() {
		t.Fatal("precondition: Drifted() must be true before resolve")
	}

	// Phase C: resolve(action="overwrite_from_db").
	// Semantics: re-apply DB records → disk, then SetSynced(dbHash).
	newDBContent, newDBHash := buildCanonicalSnapshot(t, "2")
	if err := WriteAtomic(cfgPath, newDBContent); err != nil {
		t.Fatalf("re-apply DB: %v", err)
	}
	d.SetSynced(newDBHash)

	// Verify invariants after overwrite resolve.
	if d.Drifted() {
		t.Fatal("drift should be cleared after SetSynced")
	}
	if d.ExpectedHash() != newDBHash {
		t.Fatalf("ExpectedHash=%q want %q", d.ExpectedHash(), newDBHash)
	}
	// Disk content actually matches DB content.
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newDBContent) {
		t.Fatalf("on-disk differs from DB canonical\ngot=%q\nwant=%q", got, newDBContent)
	}
	// A subsequent Refresh should confirm no drift.
	if drifted2, _, err2 := d.Refresh(); err2 != nil || drifted2 {
		t.Fatalf("post-resolve Refresh drifted=%v err=%v", drifted2, err2)
	}
}

func TestDriftResolve_SyncFromDisk(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")

	// Baseline.
	dbContent, dbHash := buildCanonicalSnapshot(t, "1")
	if err := WriteAtomic(cfgPath, dbContent); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := NewDriftDetector(cfgPath, dbHash)

	// Operator makes an intentional change (e.g. mox upgrade tweaked fields).
	operatorEdits := append([]byte(nil), dbContent...)
	operatorEdits = append(operatorEdits, []byte("# Upgrade 2026-06 added fields\nMaxMsgSize: 50M\n")...)
	if err := WriteAtomic(cfgPath, operatorEdits); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if drifted, _, err := d.Refresh(); err != nil || !drifted {
		t.Fatalf("drift expected after operator edit, got drifted=%v err=%v", drifted, err)
	}
	newDiskHash := HashBytes(operatorEdits)

	// Resolve(action="sync_from_disk"): record the NEW disk hash as
	// authoritative (caller also updates SQLite rows — out of scope for this
	// unit; we only lock in the detector side-contract).
	d.SetSynced(newDiskHash)

	if d.Drifted() {
		t.Fatal("drift not cleared by sync_from_disk resolve")
	}
	if d.SQLiteHash() != newDiskHash {
		t.Fatalf("SQLiteHash=%q != new disk hash %q", d.SQLiteHash(), newDiskHash)
	}
	// Refresh should still report no drift.
	if drifted, _, err := d.Refresh(); err != nil || drifted {
		t.Fatalf("post-sync Refresh drifted=%v err=%v", drifted, err)
	}
}

// -----------------------------------------------------------------------------
// Multi-file scan aggregation.
//
// The production detector watches one path (mox.conf).  For installations
// where the operator split their config into includes, the service layer
// composes a tree of DriftDetector instances and reports an aggregate
// summary.  MultiFileScan encapsulates that pattern here so we can test it.
// -----------------------------------------------------------------------------

type DriftSummary struct {
	Modified int
	Deleted  int
	Added    int
	Details  map[string]string // path → "modified"/"deleted"/"added"
}

// MultiFileScan runs Refresh on each path using the stored baseline map and
// returns an aggregate summary.  baselines map is mutated inline: paths that
// appear on disk but not in baselines are treated as "added" and get a
// detector created on the fly (caller must persist baselines afterwards).
func MultiFileScan(baselines map[string]string, paths []string) DriftSummary {
	s := DriftSummary{Details: map[string]string{}}
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
		baseline, ok := baselines[p]
		// First, handle path existence (surfaces os.IsNotExist reliably
		// regardless of how HashFile wraps errors internally).
		_, statErr := os.Stat(p)
		if !ok {
			if statErr == nil {
				s.Added++
				s.Details[p] = "added"
			}
			continue
		}
		if statErr != nil && os.IsNotExist(statErr) {
			s.Deleted++
			s.Details[p] = "deleted"
			continue
		}
		d := NewDriftDetector(p, baseline)
		drifted, _, err := d.Refresh()
		if err != nil {
			// Unreadable → treat as missing.
			s.Deleted++
			s.Details[p] = "deleted"
			continue
		}
		if drifted {
			s.Modified++
			s.Details[p] = "modified"
		}
	}
	for p := range baselines {
		if seen[p] {
			continue
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			s.Deleted++
			s.Details[p] = "deleted"
		}
	}
	return s
}

// seedFile writes `content` to `p` and returns its hash for the baseline map.
func seedFile(t *testing.T, p, content string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b := []byte(content)
	if err := WriteAtomic(p, b); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p, HashBytes(b)
}

func TestDrift_MultipleFiles_Changed_Deleted_Added(t *testing.T) {
	dir := t.TempDir()
	// 1. Seed 3 files.
	moxConf, moxHash := seedFile(t, filepath.Join(dir, "mox.conf"), "Hostname: mx.test\n")
	dkimConf, dkimHash := seedFile(t, filepath.Join(dir, "dkim.pub"), "v=DKIM1; k=rsa; ...\n")
	spfConf, spfHash := seedFile(t, filepath.Join(dir, "spf.txt"), "v=spf1 include:...\n")

	baselines := map[string]string{
		moxConf: moxHash,
		dkimConf: dkimHash,
		spfConf:  spfHash,
	}
	paths := []string{moxConf, dkimConf, spfConf}

	// Precondition: no drift.
	s0 := MultiFileScan(baselines, paths)
	if s0.Modified != 0 || s0.Deleted != 0 || s0.Added != 0 {
		t.Fatalf("precondition scan non-zero: %+v", s0)
	}

	// 2. Change mox.conf (modify).
	newContent := "Hostname: mail2.test\n"
	if err := WriteAtomic(moxConf, []byte(newContent)); err != nil {
		t.Fatal(err)
	}
	// 3. Delete dkim.pub.
	if err := os.Remove(dkimConf); err != nil {
		t.Fatal(err)
	}
	// 4. Add a brand new dmarc.txt file (not in baselines, but in paths list
	//    so the scanner picks it up).
	dmarcPath, _ := seedFile(t, filepath.Join(dir, "dmarc.txt"), "v=DMARC1; p=quarantine;\n")
	paths = append(paths, dmarcPath)

	summary := MultiFileScan(baselines, paths)

	if summary.Modified != 1 {
		t.Errorf("Modified=%d want 1", summary.Modified)
	}
	if summary.Deleted != 1 {
		t.Errorf("Deleted=%d want 1", summary.Deleted)
	}
	if summary.Added != 1 {
		t.Errorf("Added=%d want 1", summary.Added)
	}
	// Category correctness.
	if summary.Details[moxConf] != "modified" {
		t.Errorf("moxConf category=%q want modified", summary.Details[moxConf])
	}
	if summary.Details[dkimConf] != "deleted" {
		t.Errorf("dkimConf category=%q want deleted", summary.Details[dkimConf])
	}
	if summary.Details[dmarcPath] != "added" {
		t.Errorf("dmarc category=%q want added", summary.Details[dmarcPath])
	}

	// Report string rendering matches categories.
	var sb strings.Builder
	for p, c := range summary.Details {
		sb.WriteString(filepath.Base(p) + ":" + c + " ")
	}
	report := sb.String()
	for _, want := range []string{"mox.conf:modified", "dkim.pub:deleted", "dmarc.txt:added"} {
		if !strings.Contains(report, want) {
			t.Errorf("report %q missing %q", report, want)
		}
	}
}

// -----------------------------------------------------------------------------
// Concurrency smoke test — DriftDetector must be safe to Refresh from N
// goroutines at once while readers call Drifted/ExpectedHash.
// -----------------------------------------------------------------------------

func TestDriftDetector_ConcurrentRefreshAndRead(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mox.conf")
	seed := []byte("initial\n")
	if err := WriteAtomic(cfgPath, seed); err != nil {
		t.Fatal(err)
	}
	d := NewDriftDetector(cfgPath, HashBytes(seed))

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Writers: mutate disk + Refresh (every 2ms).
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = WriteAtomic(cfgPath, []byte("iteration "+string(rune('0'+n%10))+"\n"))
				_, _, _ = d.Refresh()
			}
			_ = id
		}(i)
	}
	// Readers: call Drifted() + accessors in a tight loop.
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			for n := 0; n < 2000; n++ {
				_ = d.Drifted()
				_ = d.SQLiteHash()
				_ = d.DiskHash()
				_ = d.ConfigPath()
				_ = d.LastCheck()
			}
		}()
	}
	// Periodic sync calls (simulates resolve).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 40; n++ {
			d.SetSynced("sync-" + string(rune('a'+n%26)))
		}
	}()

	_ = ctx
	wg.Wait()
	// If we got here without race-detector complaints, we're good.
}
