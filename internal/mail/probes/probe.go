// Package probes implements the 9-layer health probe stack used by the
// Mail control plane.  The layers, in order of increasing integration:
//
//	L1 Process   — pid alive + /proc/<pid>/stat starttime match (marker-based)
//	L2 Control   — `mox config list` subprocess succeeds → mox can read conf
//	L3 WebAPI    — HTTP(S) reachability of the Mox admin webapi
//	L4 SMTP      — 220 banner on the SMTP listener
//	L5 IMAP(S)   — IMAP "* OK" banner (or "PREAUTH" for preauth sockets)
//	L6 DNS       — MX / SPF / DKIM / DMARC / PTR / TLS-RPT / TLSA / autoconfig
//	L7 Delivery  — queue depth + bounce rate + suppression + webhook counters
//	L8 TLS       — cert expiry <30d / <7d + chain validity + TLSA match
//	L9 Reputation— DNSBL hits against domain MX IPs
//
// This file defines the shared types + the registry pattern.  Individual
// probes live in separate files (l1_process.go, l2_control.go, etc.).
//
// Concurrency notes:
//   - Probe.Run(ctx) MUST honour ctx cancellation (in particular, probes
//     that call subprocesses or HTTP must propagate it).
//   - Individual probes are internally synchronised; no two Run() calls
//     to the same probe instance overlap.  Callers can invoke Run() from
//     different goroutines safely.
//   - A single probe instance may be reused across calls (cached results
//     are intentionally NOT kept — callers decide on caching policy).
package probes

import (
	"context"
	"fmt"
	"time"
)

// Severity is the traffic-light result of a single probe run.
type Severity int

const (
	// StateUnknown means the probe could not be evaluated (e.g. mox is not
	// running yet, config path is empty).
	StateUnknown Severity = iota
	// StateGreen means the probe's pass condition is satisfied.
	StateGreen
	// StateYellow means the probe is degraded but functional (e.g. SMTP
	// banner arrives but takes >2s, or cert expires in <30d).
	StateYellow
	// StateRed means the probe definitively failed.
	StateRed
)

// String renders severity for logs / JSON.
func (s Severity) String() string {
	switch s {
	case StateUnknown:
		return "unknown"
	case StateGreen:
		return "green"
	case StateYellow:
		return "yellow"
	case StateRed:
		return "red"
	default:
		return fmt.Sprintf("severity(%d)", int(s))
	}
}

// MarshalJSON implements json.Marshaler so severity serialises as a string.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// Result is the outcome of a single Probe.Run() invocation.
type Result struct {
	// Name is the unique identifier (matches Probe.Name()).
	Name string `json:"name"`
	// Layer is the 1–9 probe layer.
	Layer int `json:"layer"`
	// State is the severity of this run.
	State Severity `json:"state"`
	// Message is a one-line human-readable summary.  For StateRed/StateYellow
	// it describes what's wrong; for StateGreen it confirms what was checked.
	Message string `json:"message"`
	// Duration is wall-clock time spent running the probe.
	Duration time.Duration `json:"duration_ns"`
	// StartedAt is the wall-clock start of the probe run.
	StartedAt time.Time `json:"started_at"`
	// Err, if non-nil, is the underlying low-level error for logs.  It is
	// intentionally NOT serialized into JSON (too noisy for the UI).
	Err error `json:"-"`
}

// Probe is the interface implemented by every layer.
type Probe interface {
	// Name returns a stable unique identifier for this probe layer.
	Name() string
	// Layer returns the 1–9 layer number.
	Layer() int
	// Run executes the probe.  It MUST honour ctx cancellation, and never
	// panic — all error paths are captured in the returned Result.Err.
	Run(ctx context.Context) Result
}

// RunAll executes every probe in `probes` concurrently (respecting ctx) and
// returns the results in probe order.  Concurrency is capped at 3 probes at
// a time so a 9-layer sweep doesn't spawn 9 subprocesses simultaneously.
func RunAll(ctx context.Context, probes []Probe) []Result {
	if len(probes) == 0 {
		return nil
	}
	out := make([]Result, len(probes))
	// Concurrency cap.
	const maxConcurrent = 3
	sem := make(chan struct{}, maxConcurrent)
	done := make(chan struct{}, len(probes))
	for i, p := range probes {
		i, p := i, p
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- struct{}{} }()
			out[i] = p.Run(ctx)
		}()
	}
	// Wait for all launches.
	for i := 0; i < len(probes); i++ {
		<-done
	}
	return out
}

// Summary returns the worst severity across all results (Green → Yellow →
// Red).  Useful for the UI pill showing "overall mail subsystem status".
func Summary(results []Result) Severity {
	worst := StateGreen
	anyUnknown := false
	for _, r := range results {
		switch r.State {
		case StateRed:
			return StateRed
		case StateYellow:
			worst = StateYellow
		case StateUnknown:
			anyUnknown = true
		}
	}
	if worst == StateGreen && anyUnknown {
		// If there's any Unknown (no data yet) and no worse state, surface
		// Yellow so the UI shows "degraded" rather than "all green" when
		// probes are still coming online.
		return StateYellow
	}
	return worst
}
