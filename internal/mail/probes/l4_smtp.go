package probes

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

// L4Config configures the L4 SMTP banner probe.
type L4Config struct {
	SMTPAddr       string        // host:port; may be "" to skip port 25
	SubmissionAddr string        // host:port; may be "" to skip port 587
	SMTPSAddr      string        // host:port; may be "" to skip port 465 (TLS)
	Timeout        time.Duration // per-port dial+read timeout
	AllowedDomains []string      // passed to SSRF guard
	// DialFunc, if non-nil, replaces the default net.Dialer used for
	// plain-text connections.  Used by unit tests to supply mock
	// connections without real network.
	DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSConnFunc, if non-nil, replaces tls.DialWithDialer for TLS
	// connections (SMTPS / IMAPS ports).
	TLSConnFunc func(addr string, cfg *tls.Config) (net.Conn, error)
}

type l4SMTP struct {
	cfg L4Config
}

// NewL4SMTP constructs an L4 SMTP-banner probe.
func NewL4SMTP(cfg L4Config) Probe {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &l4SMTP{cfg: cfg}
}

func (p *l4SMTP) Name() string  { return "l4_smtp_banner" }
func (p *l4SMTP) Layer() int    { return 4 }

func (p *l4SMTP) Run(ctx context.Context) Result {
	start := time.Now()
	r := Result{Name: p.Name(), Layer: p.Layer(), StartedAt: start, State: StateUnknown}

	addrs := []struct {
		name string
		addr string
		tls  bool
	}{
		{"smtp", p.cfg.SMTPAddr, false},
		{"submission", p.cfg.SubmissionAddr, false},
		{"smtps", p.cfg.SMTPSAddr, true},
	}

	checked := 0
	passed := 0
	var msgs []string

	for _, a := range addrs {
		if a.addr == "" {
			continue
		}
		checked++
		// SSRF guard: extract host and validate.
		host, _, err := net.SplitHostPort(a.addr)
		if err != nil {
			// If no port, the whole thing is host — this is actually a format
			// error, so skip.
			msgs = append(msgs, fmt.Sprintf("%s: bad addr %q", a.name, a.addr))
			continue
		}
		if !IsTargetAllowed(host, p.cfg.AllowedDomains, len(p.cfg.AllowedDomains) == 0) {
			msgs = append(msgs, fmt.Sprintf("%s: %s blocked by SSRF guard", a.name, host))
			r.State = StateRed
			continue
		}
		banner, perr := p.readBanner(ctx, a.addr, a.tls)
		if perr != nil {
			msgs = append(msgs, fmt.Sprintf("%s: %v", a.name, perr))
			continue
		}
		if strings.HasPrefix(banner, "220") {
			passed++
			msgs = append(msgs, fmt.Sprintf("%s: 220 banner OK", a.name))
		} else {
			msgs = append(msgs, fmt.Sprintf("%s: unexpected banner %q", a.name, banner))
		}
	}

	r.Duration = time.Since(start)
	if checked == 0 {
		r.Message = "no addresses configured"
		r.State = StateUnknown
		return r
	}
	switch {
	case passed == checked:
		r.State = StateGreen
	case passed > 0:
		r.State = StateYellow
	default:
		r.State = StateRed
	}
	r.Message = fmt.Sprintf("%d/%d ports passed — %s", passed, checked, strings.Join(msgs, "; "))
	return r
}

func (p *l4SMTP) readBanner(ctx context.Context, addr string, useTLS bool) (string, error) {
	deadline := time.Now().Add(p.cfg.Timeout)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var conn net.Conn
	var err error
	if useTLS {
		tconf := &tls.Config{InsecureSkipVerify: true} // banner probe only
		if p.cfg.TLSConnFunc != nil {
			conn, err = p.cfg.TLSConnFunc(addr, tconf)
		} else {
			dialer := &net.Dialer{Deadline: deadline}
			conn, err = tls.DialWithDialer(dialer, "tcp", addr, tconf)
		}
	} else {
		if p.cfg.DialFunc != nil {
			conn, err = p.cfg.DialFunc(dctx, "tcp", addr)
		} else {
			dialer := &net.Dialer{Deadline: deadline}
			conn, err = dialer.DialContext(dctx, "tcp", addr)
		}
	}
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(deadline)
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read banner: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
