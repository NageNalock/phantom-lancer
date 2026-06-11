package supervisor

import "time"

// BackoffStep returns the delay for a given restart attempt (1-based).
// It uses the fixed table [1s, 2s, 5s, 10s, 30s] and clamps both to
// [minDelay, maxDelay]. Attempt numbers beyond 5 saturate at the last entry.
//
// Attempt values <= 0 return 0.
func BackoffStep(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	if attempt <= 0 {
		return 0
	}
	table := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		30 * time.Second,
	}
	idx := attempt - 1
	if idx >= len(table) {
		idx = len(table) - 1
	}
	d := table[idx]
	if minDelay > 0 && d < minDelay {
		d = minDelay
	}
	if maxDelay > 0 && d > maxDelay {
		d = maxDelay
	}
	return d
}

// BackoffTracker tracks consecutive restart attempts and resets the counter
// once a child has been observed running for longer than StableAfter.
type BackoffTracker struct {
	MinDelay    time.Duration
	MaxDelay    time.Duration
	StableAfter time.Duration
	attempts    int
}

// NextDelay returns the delay for the next restart attempt and increments
// the internal attempt counter.
func (b *BackoffTracker) NextDelay() time.Duration {
	b.attempts++
	return BackoffStep(b.attempts, b.MinDelay, b.MaxDelay)
}

// ObserveUptime resets the attempt counter when the child ran longer than
// the stable-after threshold. Call this with the child's run duration after
// each child exit.
func (b *BackoffTracker) ObserveUptime(uptime time.Duration) {
	threshold := b.StableAfter
	if threshold <= 0 {
		threshold = 60 * time.Second
	}
	if uptime > threshold {
		b.attempts = 0
	}
}

// CurrentAttempts returns the current consecutive failure count (for logging).
func (b *BackoffTracker) CurrentAttempts() int { return b.attempts }
