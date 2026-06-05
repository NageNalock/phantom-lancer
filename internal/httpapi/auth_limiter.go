package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultLoginFailureThreshold = 5
	loginFailureReset            = 30 * time.Minute
	loginBackoffBase             = time.Minute
	loginBackoffMax              = 15 * time.Minute
)

type loginBackoff struct {
	mu        sync.Mutex
	threshold int
	accounts  map[string]*loginBackoffState
	ips       map[string]*loginBackoffState
}

type loginBackoffState struct {
	Failures     int
	Level        int
	LastFailure  time.Time
	BackoffUntil time.Time
}

type loginBackoffDecision struct {
	Limited      bool
	Dimension    string
	BackoffUntil time.Time
}

type loginBackoffEvent struct {
	Dimension    string
	BackoffUntil time.Time
	Duration     time.Duration
}

func newLoginBackoff(threshold int) *loginBackoff {
	if threshold <= 0 {
		threshold = defaultLoginFailureThreshold
	}
	return &loginBackoff{
		threshold: threshold,
		accounts:  make(map[string]*loginBackoffState),
		ips:       make(map[string]*loginBackoffState),
	}
}

func (b *loginBackoff) Check(username, ip string, now time.Time) loginBackoffDecision {
	b.mu.Lock()
	defer b.mu.Unlock()

	var decision loginBackoffDecision
	b.checkState(b.accounts, accountBackoffKey(username), "account", now, &decision)
	if ip != "" {
		b.checkState(b.ips, ip, "ip", now, &decision)
	}
	return decision
}

func (b *loginBackoff) RecordFailure(username, ip string, now time.Time) []loginBackoffEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	events := []loginBackoffEvent{}
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

func (b *loginBackoff) RecordSuccess(username string) {
	b.mu.Lock()
	delete(b.accounts, accountBackoffKey(username))
	b.mu.Unlock()
}

func (b *loginBackoff) checkState(states map[string]*loginBackoffState, key, dimension string, now time.Time, decision *loginBackoffDecision) {
	state := states[key]
	if state == nil {
		return
	}
	if !state.BackoffUntil.IsZero() && now.Before(state.BackoffUntil) {
		if !decision.Limited || state.BackoffUntil.After(decision.BackoffUntil) {
			*decision = loginBackoffDecision{Limited: true, Dimension: dimension, BackoffUntil: state.BackoffUntil}
		}
		return
	}
	if !state.LastFailure.IsZero() && now.Sub(state.LastFailure) > loginFailureReset {
		delete(states, key)
	}
}

func (b *loginBackoff) recordFailure(states map[string]*loginBackoffState, key, dimension string, now time.Time) (loginBackoffEvent, bool) {
	state := states[key]
	if state == nil {
		state = &loginBackoffState{}
		states[key] = state
	}
	if !state.LastFailure.IsZero() && now.Sub(state.LastFailure) > loginFailureReset {
		state.Failures = 0
		state.Level = 0
		state.BackoffUntil = time.Time{}
	}
	state.Failures++
	state.LastFailure = now
	if state.Failures < b.threshold {
		return loginBackoffEvent{}, false
	}

	state.Level++
	duration := loginBackoffDuration(state.Level)
	state.BackoffUntil = now.Add(duration)
	state.Failures = 0
	return loginBackoffEvent{Dimension: dimension, BackoffUntil: state.BackoffUntil, Duration: duration}, true
}

func loginBackoffDuration(level int) time.Duration {
	if level < 1 {
		level = 1
	}
	duration := loginBackoffBase
	for i := 1; i < level; i++ {
		duration *= 2
		if duration >= loginBackoffMax {
			return loginBackoffMax
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
