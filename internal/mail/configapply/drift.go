package configapply

import (
	"sync"
	"time"
)

// DriftDetector keeps the known-good hash last written by a successful
// configapply Run() and exposes a Drifted() flag that flips true whenever
// the on-disk config diverges from it.  Reads happen on boot + periodically
// via a 10-minute ticker.
type DriftDetector struct {
	mu         sync.Mutex
	configPath string
	sqliteHash string // last hash we consider "authoritative"
	diskHash   string // last read from disk
	drifted    bool
	lastCheck  string // RFC3339
}

func NewDriftDetector(configPath, lastKnownHash string) *DriftDetector {
	return &DriftDetector{
		configPath: configPath,
		sqliteHash: lastKnownHash,
		diskHash:   "",
		drifted:    false,
		lastCheck:  time.Now().Format(time.RFC3339),
	}
}

// Refresh reads the on-disk config, compares to sqliteHash, updates flag,
// returns (drifted, diskHash, err).
func (d *DriftDetector) Refresh() (bool, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	hash, err := HashFile(d.configPath)
	d.lastCheck = time.Now().Format(time.RFC3339)
	if err != nil {
		// No file → no drift yet (nothing to diverge from).
		return d.drifted, d.diskHash, err
	}
	d.diskHash = hash
	// Empty sqliteHash means we've never run a successful apply yet; treat
	// the current disk as baseline.
	if d.sqliteHash == "" {
		d.sqliteHash = hash
		d.drifted = false
		return false, hash, nil
	}
	d.drifted = d.sqliteHash != hash
	return d.drifted, hash, nil
}

// Drifted is the threadsafe accessor consumed by HTTP write guards.
func (d *DriftDetector) Drifted() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.drifted
}

// ConfigPath returns the watched path (used by status endpoints).
func (d *DriftDetector) ConfigPath() string { return d.configPath }

// SQLiteHash returns the last known-good hash.
func (d *DriftDetector) SQLiteHash() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sqliteHash
}

// ExpectedHash is an alias for SQLiteHash (used by HTTP layer for drift summary).
func (d *DriftDetector) ExpectedHash() string { return d.SQLiteHash() }

// DiskHash returns the hash last read from disk.
func (d *DriftDetector) DiskHash() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.diskHash
}

// LastCheck returns RFC3339 timestamp of last Refresh() call.
func (d *DriftDetector) LastCheck() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastCheck
}

// SetSynced records a new canonical hash after a completed Run().
// Used for both "overwrite" action (Phantom → disk) and "reimport"
// action (disk → SQLite, call SetSynced(diskHash)).
func (d *DriftDetector) SetSynced(newHash string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sqliteHash = newHash
	d.diskHash = newHash
	d.drifted = false
	d.lastCheck = time.Now().Format(time.RFC3339)
}
