package certmanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/mail/probes"
)

// L8Probe is the L8 certificate-expiry probe for the certmanager module.
// It is registered alongside the mail module's L1–L7 probes so the
// overall probes.Summary() sweep reports cert-expiry risk.
//
// State logic (mapped to probes.Severity):
//
//   - StateRed    : any cert's NotAfter is within ThresholdCritDays (7) of now,
//                   or NotAfter is already in the past.
//   - StateYellow : any cert's NotAfter is within ThresholdWarnDays (30) but
//                   not within the red window.
//   - StateGreen  : all certs have >= ThresholdWarnDays of runway.
//   - StateUnknown: Certs slice is empty or all NotAfter are zero-valued.
//
// The per-cert detail is packed into Result.Message as a structured,
// parseable line so downstream health dashboards can extract per-domain
// runway without modifying the probes.Result interface itself.  The
// format is: "{domain}:{days_left}:{not_after_rfc3339}" separated by
// semicolons.
type L8Probe struct {
	Certs              []Certificate
	ThresholdWarnDays  int
	ThresholdCritDays  int
}

// NewL8 constructs an L8 probe with the default thresholds (30d warn,
// 7d crit).  Pass 0 to fall back to the defaults.
func NewL8(certs []Certificate) *L8Probe {
	return &L8Probe{
		Certs:             certs,
		ThresholdWarnDays: 30,
		ThresholdCritDays: 7,
	}
}

// Name implements the probes.Probe interface-compatible convention used
// by the mail control plane.  (certmanager.L8Probe is not registered
// via probes.RunAll directly — the service layer calls Run separately
// and appends the result to the summary.)
func (p *L8Probe) Name() string { return "l8_cert_expiry" }

// Layer returns the probe layer (L8 = TLS certificates).
func (p *L8Probe) Layer() int { return 8 }

// Run executes the expiry sweep.  The returned Result pointer can be
// appended to a []probes.Result slice for Summary()/RunAll-equivalent
// aggregation.
func (p *L8Probe) Run(ctx context.Context) *probes.Result {
	start := time.Now()
	r := &probes.Result{
		Name:      p.Name(),
		Layer:     p.Layer(),
		State:     probes.StateUnknown,
		StartedAt: start,
	}
	defer func() { r.Duration = time.Since(start) }()

	warn := p.ThresholdWarnDays
	if warn <= 0 {
		warn = 30
	}
	crit := p.ThresholdCritDays
	if crit <= 0 || crit >= warn {
		crit = 7
	}
	if len(p.Certs) == 0 {
		r.Message = "no certificates registered"
		return r
	}

	// Respect ctx cancellation (cheap check; the loop below is pure CPU
	// but this keeps us consistent with the probes.Probe contract).
	select {
	case <-ctx.Done():
		r.Err = ctx.Err()
		r.Message = "canceled"
		return r
	default:
	}

	var worst probes.Severity
	var detailParts []string
	critCount := 0
	warnCount := 0
	okCount := 0

	for _, c := range p.Certs {
		na := c.NotAfter
		var daysLeft int
		if na.IsZero() {
			daysLeft = 0
		} else {
			daysLeft = int(time.Until(na).Hours() / 24)
		}
		naStr := ""
		if !na.IsZero() {
			naStr = na.Format(time.RFC3339)
		}
		detailParts = append(detailParts, fmt.Sprintf("%s:%d:%s",
			c.Domain, daysLeft, naStr))

		switch {
		case na.IsZero() || daysLeft <= 0:
			// Zero-valued NotAfter or already expired → Red.
			worst = probes.StateRed
			critCount++
		case daysLeft < crit:
			worst = probes.StateRed
			critCount++
		case daysLeft < warn:
			if worst < probes.StateYellow {
				worst = probes.StateYellow
			}
			warnCount++
		default:
			if worst == probes.StateUnknown {
				worst = probes.StateGreen
			}
			okCount++
		}
	}

	r.State = worst
	r.Message = fmt.Sprintf(
		"%d certs — crit:%d warn:%d ok:%d — %s",
		len(p.Certs), critCount, warnCount, okCount,
		strings.Join(detailParts, "; "),
	)
	return r
}
