// Package authlimiter provides a small in-memory login/failure backoff used
// to rate-limit repeated credential challenges. It intentionally has no
// external dependencies so it can be shared across packages (httpapi,
// dockercontrol, …) without pulling in HTTP storage, configuration, or
// audit subsystems.
//
// The limiter tracks failures on two independent dimensions — "account"
// (username-keyed) and "ip" — and enforces an exponential backoff once a
// configured failure threshold is crossed. The standard configuration
// constants (DefaultThreshold, BackoffBase, BackoffMax, FailureReset) are
// tuned for admin-style password prompts; callers that need different
// trade-offs can pass their own threshold to New and override durations
// via their own wrapper.
package authlimiter

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultFailureThreshold is the default number of consecutive failures
	// required before exponential backoff kicks in.
	DefaultFailureThreshold = 5
	// FailureReset is the idle window after which a stale failure record is
	// cleared and the failure counter is reset.
	FailureReset = 30 * time.Minute
	// BackoffBase is the initial exponential backoff duration for the first
	// threshold crossing. Each further crossing doubles the duration until
	// BackoffMax is reached.
	BackoffBase = time.Minute
	// BackoffMax is the maximum duration a single backoff can impose.
	BackoffMax = 15 * time.Minute
)

// Backoff tracks per-account and per-ip failure windows and produces
// allow/deny decisions and backoff events. The zero value is not usable;
// construct via NewBackoff.
type Backoff struct {
	mu        sync.Mutex
	threshold int
	accounts  map[string]*backoffState
	ips       map[string]*backoffState
}

type backoffState struct {
	Failures     int
	Level        int
	LastFailure  time.Time
	BackoffUntil time.Time
}

// Decision describes whether a request should be allowed to proceed, and,
// if not, which dimension imposed the block and until when.
type Decision struct {
	Limited      bool
	Dimension    string // "account" or "ip"
	BackoffUntil time.Time
}

// Event is produced the first time a backoff level is reached for a
// dimension. It is surfaced to callers so they can emit audit events.
type Event struct {
	Dimension    string
	BackoffUntil time.Time
	Duration     time.Duration
}

// NewBackoff returns a ready-to-use Backoff with the given failure
// threshold. If threshold is <= 0, DefaultFailureThreshold is used.
func NewBackoff(threshold int) *Backoff {
	if threshold <= 0 {
		threshold = DefaultFailureThreshold
	}
	return &Backoff{
		threshold: threshold,
		accounts:  make(map[string]*backoffState),
		ips:       make(map[string]*backoffState),
	}
}

// Check returns the current allow/deny decision for (username, ip). It
// performs side-effect-free lookups and garbage-collects entries older
// than FailureReset.
func (b *Backoff) Check(username, ip string, now time.Time) Decision {
	b.mu.Lock()
	defer b.mu.Unlock()

	var decision Decision
	b.checkState(b.accounts, accountBackoffKey(username), "account", now, &decision)
	if ip != "" {
		b.checkState(b.ips, ip, "ip", now, &decision)
	}
	return decision
}

// RecordFailure records one authentication failure and returns any events
// that cross the configured backoff threshold. Returned events are
// intended to drive audit logging.
func (b *Backoff) RecordFailure(username, ip string, now time.Time) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	events := []Event{}
	if event, ok := b.recordFailure(b.accounts, accountBackoffKey(username), "account", now); ok {
		events = append(events, event)
	}
	if ip != "" {
		if event, ok := b.recordFailure(b.ips, ip, "ip", now); ok {
			events = append(events, event)
		}
	}
	return events
}

// RecordSuccess clears any per-account failure state. It intentionally
// does not touch per-IP state — a success from one account should not
// wash out repeated failures from other accounts sharing the same IP.
func (b *Backoff) RecordSuccess(username string) {
	b.mu.Lock()
	delete(b.accounts, accountBackoffKey(username))
	b.mu.Unlock()
}

func (b *Backoff) checkState(states map[string]*backoffState, key, dimension string, now time.Time, decision *Decision) {
	state := states[key]
	if state == nil {
		return
	}
	if !state.BackoffUntil.IsZero() && now.Before(state.BackoffUntil) {
		if !decision.Limited || state.BackoffUntil.After(decision.BackoffUntil) {
			*decision = Decision{Limited: true, Dimension: dimension, BackoffUntil: state.BackoffUntil}
		}
		return
	}
	if !state.LastFailure.IsZero() && now.Sub(state.LastFailure) > FailureReset {
		delete(states, key)
	}
}

func (b *Backoff) recordFailure(states map[string]*backoffState, key, dimension string, now time.Time) (Event, bool) {
	state := states[key]
	if state == nil {
		state = &backoffState{}
		states[key] = state
	}
	if !state.LastFailure.IsZero() && now.Sub(state.LastFailure) > FailureReset {
		state.Failures = 0
		state.Level = 0
		state.BackoffUntil = time.Time{}
	}
	state.Failures++
	state.LastFailure = now
	if state.Failures < b.threshold {
		return Event{}, false
	}

	state.Level++
	duration := backoffDuration(state.Level)
	state.BackoffUntil = now.Add(duration)
	state.Failures = 0
	return Event{Dimension: dimension, BackoffUntil: state.BackoffUntil, Duration: duration}, true
}

func backoffDuration(level int) time.Duration {
	if level < 1 {
		level = 1
	}
	duration := BackoffBase
	for i := 1; i < level; i++ {
		duration *= 2
		if duration >= BackoffMax {
			return BackoffMax
		}
	}
	return duration
}

func accountBackoffKey(username string) string {
	username = strings.TrimSpace(strings.ToLower(username))
	if username == "" {
		return "<blank>"
	}
	return username
}

// ClientIP extracts the bare (host, no port) remote address from a request
// for per-IP rate limiting. It does not honour X-Forwarded-For on purpose:
// rate-limit keys must come from a trusted source, and, unlike the request
// telemetry middleware, this helper is used in contexts where the upstream
// proxy header is not guaranteed to be trustworthy.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
