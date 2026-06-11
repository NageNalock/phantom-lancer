// Package logsampler provides a tiny keyed rate limiter for slog calls
// that fall on hot paths — streaming scan loops, HTTP telemetry
// middleware, repeated upstream health probes, and so on. Each Sampler
// maps distinct string keys to a "last emitted" timestamp and returns
// false for events arriving faster than the configured interval.
//
// The package is intentionally small, dependency-free, and safe for
// concurrent use. It is not a general-purpose request rate limiter —
// see internal/authlimiter for that.
package logsampler

import (
	"sync"
	"time"
)

// Sampler is a keyed "allow at most once per Interval" gate. The zero
// value is usable (behaves as Allow-always); construct via New for
// meaningful sampling.
type Sampler struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time

	// Always, when true, causes Allow to unconditionally return true.
	// Useful as a test hook and for callers that want a consistent API
	// regardless of whether sampling is enabled.
	Always bool
}

// New returns a Sampler that allows at most one emission per key per
// interval. A non-positive interval disables sampling entirely.
func New(interval time.Duration) *Sampler {
	if interval < 0 {
		interval = 0
	}
	return &Sampler{interval: interval, last: make(map[string]time.Time)}
}

// Allow reports whether an event tagged `key` may be emitted right
// now. As a side-effect it opportunistically prunes stale entries when
// the internal map grows past a soft cap; there is no background
// goroutine.
func (s *Sampler) Allow(key string) bool {
	if s == nil || s.Always || s.interval == 0 {
		return true
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.last) > 512 {
		cutoff := now.Add(-s.interval * 2)
		for k, t := range s.last {
			if t.Before(cutoff) {
				delete(s.last, k)
			}
		}
	}
	if prev, ok := s.last[key]; ok && now.Sub(prev) < s.interval {
		return false
	}
	s.last[key] = now
	return true
}
