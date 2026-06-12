package certmanager

import (
	"time"
)

// DefaultDaysBeforeRenewal is the default "renew N days before expiry"
// threshold.  It mirrors Let's Encrypt's own recommendation (90-day
// certs, renew at 30 days before expiry → 2/3 through the lifetime).
const DefaultDaysBeforeRenewal = 30

// DefaultRetriesPerDay is the default cap on how many renewal attempts
// a single certificate may schedule per wall-clock day.  6 attempts/day
// = once every 4h in the <7-day window, which is aggressive enough for
// DNS-01 propagation quirks without hammering the ACME directory.
const DefaultRetriesPerDay = 6

// NextRenewal returns the UTC wall-clock time at which a certificate
// whose NotAfter is `notAfter` should be scheduled for renewal.  When
// daysBefore is <= 0 the DefaultDaysBeforeRenewal (30) is used.
//
// If `notAfter - daysBefore` lands in the past, the returned time is
// clamped to "now rounded down to the next hour" so callers don't spawn
// 10,000 overdue renewals simultaneously.
func NextRenewal(notAfter time.Time, daysBefore int) time.Time {
	if daysBefore <= 0 {
		daysBefore = DefaultDaysBeforeRenewal
	}
	na := notAfter.UTC()
	due := na.AddDate(0, 0, -daysBefore)
	now := time.Now().UTC()
	if due.Before(now) {
		// Clamp to the top of the next hour to avoid thundering herds.
		return now.Truncate(time.Hour).Add(time.Hour)
	}
	return due
}

// ShouldRenew reports whether a certificate with NotAfter `notAfter`
// should be renewed at wall-clock time `now`.  When daysBefore is <= 0
// the DefaultDaysBeforeRenewal is used.
//
// Semantic: return true iff now >= (notAfter - daysBefore).
func ShouldRenew(notAfter time.Time, now time.Time, daysBefore int) bool {
	if daysBefore <= 0 {
		daysBefore = DefaultDaysBeforeRenewal
	}
	threshold := notAfter.UTC().AddDate(0, 0, -daysBefore)
	return !now.UTC().Before(threshold)
}

// RetryIntervalDaysLeft maps the remaining-days-until-expiry into a
// backoff interval used between consecutive failed renewal attempts.
//
// Policy (tighter than the 6h spec for <7d to avoid missing a cert):
//
//	daysLeft >= 30  →  1 hour  (plenty of runway; very lazy retry)
//	7 <= d < 30     →  6 hours (approaching; ramp up)
//	daysLeft < 7    →  4 hours (pushing deadline; aggressive but not
//	                             so aggressive that we trip ACME rate
//	                             limits for Let's Encrypt)
func RetryIntervalDaysLeft(daysLeft int) time.Duration {
	switch {
	case daysLeft >= 30:
		return time.Hour
	case daysLeft >= 7:
		return 6 * time.Hour
	default:
		return 4 * time.Hour
	}
}

// RetriesExhausted reports how many of the timestamps in lastAttempts
// fall within the most recent 24h window and whether that count
// exceeds perDayCap.  When perDayCap <= 0, DefaultRetriesPerDay is
// used.  Returns true iff len(recent-24h) > perDayCap.
//
// Callers typically invoke this before scheduling a retry so a single
// misconfigured certificate can't drive the ACME rate-limit system to
// ban the account.  Inputs may contain zero-valued times; they are
// skipped (never count against the cap).
func RetriesExhausted(lastAttempts []time.Time, perDayCap int) bool {
	if perDayCap <= 0 {
		perDayCap = DefaultRetriesPerDay
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	count := 0
	for _, t := range lastAttempts {
		if t.IsZero() {
			continue
		}
		if t.UTC().After(cutoff) {
			count++
			if count > perDayCap {
				return true
			}
		}
	}
	return false
}

// DaysBetween returns the whole number of 24-hour spans between t0 and
// t1.  It's used by the renewal scheduler for pretty-printing.  The
// result is positive when t1 is after t0.
func DaysBetween(t0, t1 time.Time) int {
	d := t1.UTC().Sub(t0.UTC())
	return int(d.Hours() / 24)
}
