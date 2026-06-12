package certmanager

import (
	"context"
	"time"
)

// RenewalRunner is the stateless, caller-driven scheduler used by the
// service layer to sweep all registered certificates once per ticker
// fire.  It does NOT spawn goroutines, does NOT own a clock, and does
// NOT persist state — the ticker, cancellation context, DB access, and
// pipeline construction are all provided from the outside.
//
// Usage pattern in the service layer:
//
//	ticker := time.NewTicker(1 * time.Hour)
//	defer ticker.Stop()
//	for {
//	    select {
//	    case <-ctx.Done(): return
//	    case <-ticker.C:
//	        runner.RunOnce(time.Now())
//	    }
//	}
//
// The three callback fields let callers inject the real Issue()
// pipeline; RenewalRunner itself never depends on storage, mox, or the
// network.
type RenewalRunner struct {
	// Ctx is the cancellation context propagated to LoadAllCerts and
	// (through ForEach) to the Issue pipeline.
	Ctx context.Context
	// LoadAllCerts returns the full set of registered certificates —
	// the runner will filter to ShouldRenew()==true internally.
	LoadAllCerts func(ctx context.Context) ([]Certificate, error)
	// ForEach re-runs the Issue pipeline for the given certificate.
	// Implementations typically build an IssueConfig from the cert's
	// stored metadata (Domain, SANDomains, DNSProviderID, etc.) and
	// call Issue().  If ForEach returns a non-success IssueResult,
	// RenewalRunner will retry up to 2 more times (3 total) for that
	// cert unless RetriesExhausted indicates that the per-day cap has
	// been reached.
	ForEach func(ctx context.Context, cert Certificate) IssueResult
	// OnSuccess is called for every successful renewal.  Implementations
	// typically log an audit event and notify the operator via email
	// or webhook.  May be nil.
	OnSuccess func(cert Certificate, res IssueResult)
	// OnError is called once per failed attempt.  Implementations
	// typically bump the `last_error` DB column and record a low-risk
	// audit event (high-risk only when NotAfter < 7 days).  May be nil.
	OnError func(cert Certificate, res IssueResult, attempt int)
}

const maxAttemptsPerRun = 3

// RunOnce sweeps all certificates, filters to those that
// ShouldRenew(now) is true, and invokes ForEach for each.  Returns the
// number of certificates that were successfully renewed during this
// sweep.  Certificates whose renewal fails are not counted toward the
// return value; their OnError callbacks still fire per attempt.
func (r *RenewalRunner) RunOnce(now time.Time) int {
	if r == nil || r.Ctx == nil || r.LoadAllCerts == nil || r.ForEach == nil {
		// Misconfigured — caller forgot to wire a callback.  Return 0
		// so the service layer can detect "0 swept, 0 succeeded" and
		// surface the misconfiguration as a health probe failure.
		return 0
	}
	certs, err := r.LoadAllCerts(r.Ctx)
	if err != nil {
		// Can't read DB — nothing to do.  Caller probes will see
		// overdue certs and escalate separately.
		return 0
	}

	renewed := 0
	for _, cert := range certs {
		select {
		case <-r.Ctx.Done():
			return renewed
		default:
		}
		if !ShouldRenew(cert.NotAfter, now, DefaultDaysBeforeRenewal) {
			continue
		}
		// Try up to 3 attempts; stop early if RetriesExhausted cap is
		// hit for the 24h window.  Each failure feeds into the
		// (virtual) last-attempt list used to enforce the cap.
		lastAttempts := []time.Time{cert.LastRenewalAttempt}
		var lastRes IssueResult
		attemptsDone := 0
		for a := 0; a < maxAttemptsPerRun; a++ {
			if RetriesExhausted(lastAttempts, DefaultRetriesPerDay) {
				break
			}
			lastRes = r.ForEach(r.Ctx, cert)
			lastAttempts = append(lastAttempts, time.Now().UTC())
			attemptsDone++
			if lastRes.Success {
				break
			}
			// Back off between failed attempts inside a single sweep.
			// The policy here is lighter-weight than
			// RetryIntervalDaysLeft because it's intra-sweep; the real
			// backoff window is enforced by RunOnce being called on
			// the service-level ticker.
			if a < maxAttemptsPerRun-1 {
				select {
				case <-time.After(30 * time.Second):
				case <-r.Ctx.Done():
					return renewed
				}
			}
		}
		if lastRes.Success {
			renewed++
			if r.OnSuccess != nil {
				r.OnSuccess(cert, lastRes)
			}
		} else if r.OnError != nil && attemptsDone > 0 {
			r.OnError(cert, lastRes, attemptsDone)
		}
	}
	return renewed
}
