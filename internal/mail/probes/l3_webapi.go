package probes

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// L3Config inputs for L3WebAPIProbe.
type L3Config struct {
	// BaseURL is the Mox admin webapi endpoint, including scheme.
	// Examples:
	//   http://127.0.0.1:1080/      (loopback TCP)
	//   http+unix://%2Frun%2Fmox%2Fapi.sock/   (unix socket via custom dial)
	//   https://127.0.0.1:10443/     (TLS loopback)
	//
	// We accept an http(s) URL here; the caller is responsible for building
	// it from the mox config.  If a unix socket is desired, callers should
	// set DialUnixSocket instead and leave BaseURL as an http:// URL with a
	// dummy host (the dialer below will override it).
	BaseURL string
	// DialUnixSocket, if set, is the filesystem path of a unix socket.  The
	// probe will dial the socket for the HTTP request (ignoring the host in
	// BaseURL).  This matches Mox's default deployment pattern on Linux.
	DialUnixSocket string
	// Path is appended to BaseURL.  We use "/" by default but callers can
	// point at a more specific endpoint like "/metrics" or "/api" if their
	// Mox install has a stricter auth.
	Path string
	// Timeout bounds the dial + request + first-byte-read.  Default 5s.
	Timeout time.Duration
	// InsecureSkipTLSVerify, when true, skips TLS certificate validation
	// for https:// endpoints.  This is only appropriate for the loopback
	// case with a self-signed cert – which is the normal Mox state before
	// the operator has configured certmanager.  The default (false) is the
	// safer choice when BaseURL is an external host.
	InsecureSkipTLSVerify bool
	// RequireStatusCodes, if non-empty, is the explicit set of HTTP status
	// codes that count as "green".  Default: 2xx + 3xx + 401 (auth prompt)
	// all count as reachable.
	RequireStatusCodes []int
}

// L3WebAPIProbe checks whether the Mox admin webapi is reachable via HTTP.
//
// It performs a single HTTP GET and classifies the result:
//
//	2xx/3xx/401               → StateGreen (webapi is listening)
//	4xx (non-401)             → StateYellow (webapi is UP but endpoint is wrong)
//	5xx / net.DeadlineExceeded
//	  / connection refused     → StateRed (webapi is DOWN or overloaded)
//	unconfigured BaseURL      → StateUnknown
//
// The probe deliberately does NOT require authentication: the goal is to
// verify the webapi is UP, not that we can access it.  Mox WebAPI unauth
// callers get a 401 which is sufficient proof of liveness.
type L3WebAPIProbe struct {
	cfg L3Config
}

// NewL3WebAPI constructs a new L3WebAPIProbe.
func NewL3WebAPI(cfg L3Config) *L3WebAPIProbe { return &L3WebAPIProbe{cfg: cfg} }

// Name implements Probe.
func (p *L3WebAPIProbe) Name() string { return "l3_webapi" }

// Layer implements Probe.
func (p *L3WebAPIProbe) Layer() int { return 3 }

// Run implements Probe.
func (p *L3WebAPIProbe) Run(ctx context.Context) (r Result) {
	r = Result{Name: p.Name(), Layer: p.Layer(), State: StateUnknown, StartedAt: time.Now()}
	defer func() { r.Duration = time.Since(r.StartedAt) }()

	if p.cfg.BaseURL == "" {
		r.Message = "webapi base URL not configured; Mox has not been initialised"
		return r
	}
	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/" + strings.TrimLeft(p.cfg.Path, "/")

	// Build an http.Client.  If DialUnixSocket is set, use a custom dialer
	// that ignores the host in the URL and always connects to the socket.
	client := &http.Client{Timeout: timeout}
	trans := &http.Transport{
		// Never follow redirects – we only care about the first response.
		DisableKeepAlives:   true,
		MaxConnsPerHost:     1,
		TLSHandshakeTimeout: 2 * time.Second,
	}
	if p.cfg.DialUnixSocket != "" {
		sock := p.cfg.DialUnixSocket
		d := &net.Dialer{Timeout: timeout}
		trans.DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(reqCtx, "unix", sock)
		}
	}
	if p.cfg.InsecureSkipTLSVerify {
		// crypto/tls imported lazily – only need it when the caller actually
		// uses https:// endpoints.  We reference it via `cryptoTLSConfig`.
		trans.TLSClientConfig = cryptoTLSInsecureConfig()
	}
	client.Transport = trans
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		r.State = StateRed
		r.Message = fmt.Sprintf("build request: %v", err)
		r.Err = err
		return r
	}
	req.Header.Set("User-Agent", "phantom-lancer/probes-l3")

	resp, err := client.Do(req)
	if err != nil {
		return classifyL3Error(err, r)
	}
	defer resp.Body.Close()

	// Drain up to 4 KiB of the body so the connection can close cleanly.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return classifyL3Status(resp.StatusCode, resp.Status, p.cfg.RequireStatusCodes, r)
}

// --- helpers ----------------------------------------------------------------

// classifyL3Error converts a transport-level error into a Result.
func classifyL3Error(err error, r Result) Result {
	// ctx cancel/timeout → Red, unless it's our own timeout firing and we
	// have more specific context.
	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		r.State = StateRed
		r.Message = fmt.Sprintf("webapi unreachable: %v (timeout)", err)
		r.Err = err
		return r
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		r.State = StateRed
		r.Message = "webapi connection refused – Mox is not listening on the configured port/socket"
		r.Err = err
	case strings.Contains(msg, "no such file or directory"):
		r.State = StateRed
		r.Message = "unix socket does not exist – Mox is not running"
		r.Err = err
	case strings.Contains(msg, "certificate"):
		// TLS error – webapi IS listening, just has a cert we can't verify.
		r.State = StateYellow
		r.Message = fmt.Sprintf("webapi reachable but TLS error: %v", err)
		r.Err = err
	default:
		r.State = StateRed
		r.Message = fmt.Sprintf("webapi unreachable: %v", err)
		r.Err = err
	}
	return r
}

// classifyL3Status converts an HTTP status code into a Result.
func classifyL3Status(code int, status string, required []int, r Result) Result {
	// Explicit allowlist wins.
	if len(required) > 0 {
		for _, c := range required {
			if c == code {
				r.State = StateGreen
				r.Message = fmt.Sprintf("webapi returned %s", status)
				return r
			}
		}
		r.State = StateYellow
		r.Message = fmt.Sprintf("webapi returned %s (expected one of %v)", status, required)
		return r
	}

	switch {
	case code >= 200 && code < 400:
		r.State = StateGreen
		r.Message = fmt.Sprintf("webapi reachable (%s)", status)
	case code == 401:
		// Mox webapi requires auth – expected for unauth probes.
		r.State = StateGreen
		r.Message = fmt.Sprintf("webapi reachable, requires auth (%s)", status)
	case code >= 400 && code < 500:
		r.State = StateYellow
		r.Message = fmt.Sprintf("webapi returned %s (endpoint misconfigured?)", status)
	case code >= 500:
		r.State = StateRed
		r.Message = fmt.Sprintf("webapi returned %s (server error)", status)
	default:
		r.State = StateYellow
		r.Message = fmt.Sprintf("webapi returned unexpected %s", status)
	}
	return r
}

// --- TLS helper (import isolation) -----------------------------------------
//
// We import crypto/tls lazily through this function to keep the "probes"
// package usable even when InsecureSkipTLSVerify is never set (the common
// case on HTTP loopback / unix sockets).  Go's linker will DCE the
// crypto/tls dependency unless this function is actually reachable.
//
// (In practice crypto/tls is already in the binary because the HTTPS server
// uses it; this is mostly a hygiene measure to keep imports clean.)

// crypto/tls pulled in via dedicated file below to keep probes' top-level
// import list short.  See l3_webapi_tls.go.
