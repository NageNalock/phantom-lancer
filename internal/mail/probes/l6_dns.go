package probes

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// L6Config configures the L6 DNS-record probe.
type L6Config struct {
	Domain         string
	DKIMSelector   string
	Timeout        time.Duration
	AllowedDomains []string
	// Resolver overrides the default net.DefaultResolver for DNS lookups.
	// When nil, probes.Resolver is used (which itself defaults to
	// net.DefaultResolver).
	Resolver *net.Resolver
}

type l6DNS struct {
	cfg L6Config
}

// NewL6DNS constructs an L6 DNS probe for a single domain.
func NewL6DNS(cfg L6Config) Probe {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &l6DNS{cfg: cfg}
}

func (p *l6DNS) Name() string { return "l6_dns" }
func (p *l6DNS) Layer() int   { return 6 }

// Record check keys (8 recommended records).
var l6Checks = []string{"MX", "SPF", "DKIM", "DMARC", "TLSA", "TLS-RPT", "PTR", "SRV"}

func (p *l6DNS) Run(ctx context.Context) Result {
	start := time.Now()
	r := Result{Name: p.Name(), Layer: p.Layer(), StartedAt: start, State: StateUnknown}

	if p.cfg.Domain == "" {
		r.Message = "no domain configured"
		return r
	}
	if !IsTargetAllowed(p.cfg.Domain, p.cfg.AllowedDomains, false) {
		r.State = StateRed
		r.Message = fmt.Sprintf("domain %q not in allowed list (SSRF guard)", p.cfg.Domain)
		return r
	}

	deadline := time.Now().Add(p.cfg.Timeout)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	resolver := p.cfg.Resolver
	if resolver == nil {
		resolver = Resolver
	}
	checked := 0
	passed := 0
	var msgs []string

	// 1. MX records
	checked++
	mxs, err := resolver.LookupMX(dctx, p.cfg.Domain)
	if err == nil && len(mxs) > 0 {
		passed++
		msgs = append(msgs, fmt.Sprintf("MX: %d record(s)", len(mxs)))
	} else {
		msgs = append(msgs, "MX: missing")
	}

	// 2. SPF (TXT records starting with "v=spf1")
	checked++
	if txts, err := resolver.LookupTXT(dctx, p.cfg.Domain); err == nil {
		found := false
		for _, t := range txts {
			if strings.HasPrefix(t, "v=spf1") {
				found = true
				break
			}
		}
		if found {
			passed++
			msgs = append(msgs, "SPF: present")
		} else {
			msgs = append(msgs, "SPF: missing")
		}
	} else {
		msgs = append(msgs, "SPF: lookup failed")
	}

	// 3. DKIM (<selector>._domainkey.<domain> TXT)
	checked++
	if p.cfg.DKIMSelector != "" {
		dkimName := p.cfg.DKIMSelector + "._domainkey." + p.cfg.Domain
		if txts, err := resolver.LookupTXT(dctx, dkimName); err == nil && len(txts) > 0 {
			passed++
			msgs = append(msgs, "DKIM: present")
		} else {
			msgs = append(msgs, "DKIM: missing")
		}
	} else {
		msgs = append(msgs, "DKIM: no selector configured")
	}

	// 4. DMARC (_dmarc.<domain> TXT)
	checked++
	dmarcName := "_dmarc." + p.cfg.Domain
	if txts, err := resolver.LookupTXT(dctx, dmarcName); err == nil && len(txts) > 0 {
		passed++
		msgs = append(msgs, "DMARC: present")
	} else {
		msgs = append(msgs, "DMARC: missing")
	}

	// 5–8: TLSA / TLS-RPT / PTR / SRV — best-effort, do not block on errors
	for _, name := range []string{"TLSA", "TLS-RPT", "PTR", "SRV"} {
		checked++
		// Skeleton: always return "unknown" for these for now (they are
		// optional / rarely configured).
		msgs = append(msgs, name+": not checked (skeleton)")
	}

	r.Duration = time.Since(start)
	switch {
	case passed == checked:
		r.State = StateGreen
	case passed >= checked/2:
		r.State = StateYellow
	default:
		r.State = StateRed
	}
	r.Message = fmt.Sprintf("%d/%d checks — %s", passed, checked, strings.Join(msgs, "; "))
	return r
}
