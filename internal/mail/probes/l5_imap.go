package probes

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

// L5Config holds inputs for the L5 IMAP banner probe.
type L5Config struct {
	IMAPAddr       string   // host:port for plain/STARTTLS IMAP; usually ":143"
	IMAPSAddr      string   // host:port for implicit TLS IMAPS; usually ":993"
	Timeout        time.Duration
	AllowedDomains []string
	// DialFunc overrides plain TCP connects for tests.
	DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSConnFunc overrides tls.DialWithDialer for tests.
	TLSConnFunc func(addr string, cfg *tls.Config) (net.Conn, error)
}

// L5IMAPProbe reports whether at least one configured IMAP endpoint
// responds with an "* OK" greeting.  Aggregation rules mirror L4:
// no endpoints → Unknown; all fail → Red; partial → Yellow; all OK → Green.
type L5IMAPProbe struct {
	cfg L5Config
}

func NewL5IMAP(cfg L5Config) *L5IMAPProbe { return &L5IMAPProbe{cfg: cfg} }

func (p *L5IMAPProbe) Name() string { return "l5_imap" }
func (p *L5IMAPProbe) Layer() int    { return 5 }

func (p *L5IMAPProbe) Run(ctx context.Context) (r Result) {
	r = Result{Name: p.Name(), Layer: p.Layer(), State: StateUnknown, StartedAt: time.Now()}
	defer func() { r.Duration = time.Since(r.StartedAt) }()

	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	targets := []struct {
		label, addr string
		tlsMode     int // 0=plain, 2=implicit tls
	}{
		{"imap", p.cfg.IMAPAddr, 0},
		{"imaps", p.cfg.IMAPSAddr, 2},
	}

	allowLoopback := len(p.cfg.AllowedDomains) == 0
	configured := 0
	ok := 0
	var lastErr string

	for _, t := range targets {
		if t.addr == "" {
			continue
		}
		configured++
		host, _, err := net.SplitHostPort(t.addr)
		if err != nil {
			lastErr = fmt.Sprintf("split %s %s: %v", t.label, t.addr, err)
			continue
		}
		if !IsTargetAllowed(host, p.cfg.AllowedDomains, allowLoopback) {
			r.Err = fmt.Errorf("l5: target %q not in allowed domains or loopback", host)
			r.Message = fmt.Sprintf("blocked by SSRF guard: %s", host)
			r.State = StateRed
			return r
		}
		greeting, perr := probeIMAPBanner(ctx, t.addr, timeout, t.tlsMode, p.cfg)
		if perr != nil {
			lastErr = fmt.Sprintf("%s: %v", t.label, perr)
			continue
		}
		// "* OK [CAPABILITY …]" or "* PREAUTH [ … ]" both treated as success.
		if len(greeting) >= 5 && (greeting[0] == '*' && (containsPrefix(greeting[1:], " OK") || containsPrefix(greeting[1:], " PREAUTH"))) {
			ok++
		} else {
			lastErr = fmt.Sprintf("%s: unexpected greeting %q", t.label, greeting)
		}
	}

	switch {
	case configured == 0:
		r.Message = "no IMAP endpoints configured"
	case ok == configured:
		r.State = StateGreen
		r.Message = fmt.Sprintf("all %d endpoint(s) reply * OK", ok)
	case ok > 0:
		r.State = StateYellow
		r.Message = fmt.Sprintf("%d/%d endpoint(s) OK; last issue: %s", ok, configured, lastErr)
	default:
		r.State = StateRed
		r.Message = fmt.Sprintf("no endpoints respond * OK (last: %s)", lastErr)
	}
	return r
}

func probeIMAPBanner(ctx context.Context, addr string, timeout time.Duration, tlsMode int, cfg L5Config) (string, error) {
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: timeout}
	switch tlsMode {
	case 2:
		tc := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
		if cfg.TLSConnFunc != nil {
			conn, err = cfg.TLSConnFunc(addr, tc)
		} else {
			conn, err = tls.DialWithDialer(dialer, "tcp", addr, tc)
		}
	default:
		if cfg.DialFunc != nil {
			conn, err = cfg.DialFunc(ctx, "tcp", addr)
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", addr)
		}
	}
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	rd := bufio.NewReader(conn)
	line, rerr := rd.ReadString('\n')
	if rerr != nil && line == "" {
		return "", rerr
	}
	return line, nil
}

func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
