// Package safelog provides a single, canonical redaction pipeline used by
// every service boundary that may emit secrets to service logs, audit logs,
// request logs, SSE events or error messages rendered to clients.
//
// All regexes live here and nowhere else. Downstream packages (logs,
// codexgateway, httpapi, …) import this package instead of compiling their
// own private copies.
package safelog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Unified redaction patterns. Order matters: bearer → key-value → structured
// data → opaque identifiers → URL query-string stripping.
var (
	// bearerToken matches `Bearer <token>` (RFC 6750 style) with a
	// generous but not unlimited token alphabet and a lower length bound.
	bearerToken = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`)

	// keyValuePair captures common secret knobs in JSON, query-string,
	// cookie, header and log output. Group layout: $1=key, $2=separator.
	// Covers: api[_-]?key / token / secret / password / passwd / authorization
	//         / cookie / csrf / session / access[_-]?token / refresh[_-]?token
	//         / id[_-]?token.
	keyValuePair = regexp.MustCompile(
		`(?i)\b(access[_-]?token|refresh[_-]?token|id[_-]?token|` +
			`api[_-]?key|apikey|token|secret|password|passwd|` +
			`authorization|cookie|csrf|session)\b(\s*["']?\s*[=:]\s*["']?)` +
			`("[^"]+"|'[^']+'|[^\s,;&}\)\[\]]+)`,
	)

	// pemPrivate matches the start of a PEM private key block and its
	// base64 body up to the closing footer. It greedily swallows the
	// entire block so individual base64 lines are not partially leaked.
	pemPrivate = regexp.MustCompile(
		`-----BEGIN (?:[A-Z0-9 ]* )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z0-9 ]* )?PRIVATE KEY-----`,
	)

	// dataURL swallows inlined image/audio data URIs.
	dataURL = regexp.MustCompile(`data:[A-Za-z0-9.+-]+/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=_-]+`)

	// uuid4 masks UUID v4 while preserving 4-char prefix/suffix for
	// correlation (e.g. log trace ids).
	uuid4 = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)

	// awsSignature matches the AWS SigV4 trio that frequently leaks in
	// presigned URLs and SDK debug output. The prefix forms also cover
	// the "Credential=AKIA…" / "Signature=…" spellings used in the
	// canonical AWS4-HMAC-SHA256 Authorization header. Go's regexp does
	// not support lookbehind, so we require a non-identifier character
	// (or start-of-string) before the bare keyword variants.
	awsSignature = regexp.MustCompile(
		`(?i)((?:X-Amz-Signature|X-Amz-Credential|X-Amz-Security-Token)=|` +
			`(?:^|[^A-Za-z0-9_-])(?:Credential|Signature)\s*=\s*)[^&\s,"']+`,
	)

	// httpURL is used to strip query strings in-place for any naked URL
	// that survived earlier phases.
	httpURL = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

// registeredRegex holds a user-supplied regex + replacement pair for the
// optional tail pass of Redact().  Stored compiled so repeated Redact calls
// don't recompile the pattern.
type registeredRegex struct {
	re          *regexp.Regexp
	replacement string
}

var (
	registeredRedactions []registeredRegex
	registeredMu         sync.Mutex
)

// RegisterRegex compiles pattern and appends it to the tail redaction pass.
// The replacement string uses the same syntax as regexp.ReplaceAllString
// ($1, $2, … refer to capture groups).  Returns a non-nil error if the
// pattern fails to compile.  The global registry is mutex-protected; safe
// to call from any goroutine.
func RegisterRegex(pattern, replacement string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	registeredMu.Lock()
	defer registeredMu.Unlock()
	registeredRedactions = append(registeredRedactions, registeredRegex{
		re:          re,
		replacement: replacement,
	})
	return nil
}

// ResetRegistered clears every pattern previously added via RegisterRegex.
// Used by tests and by callers that want to replace the whole tail set.
func ResetRegistered() {
	registeredMu.Lock()
	defer registeredMu.Unlock()
	registeredRedactions = registeredRedactions[:0]
}

// Redact applies every redaction rule to value and returns the sanitised
// string. It is safe to call on empty input. Redact is the single entry
// point used by every other helper in this package and by callers that
// want the full pipeline without length truncation.
func Redact(value string) string {
	if value == "" {
		return value
	}
	v := bearerToken.ReplaceAllString(value, "Bearer [redacted]")
	v = keyValuePair.ReplaceAllString(v, "${1}${2}[redacted]")
	v = pemPrivate.ReplaceAllString(v, "[redacted-private-key]")
	v = dataURL.ReplaceAllString(v, "[redacted-image-data]")
	v = uuid4.ReplaceAllStringFunc(v, func(m string) string {
		if len(m) <= 8 {
			return "****"
		}
		return m[:4] + "..." + m[len(m)-4:]
	})
	v = awsSignature.ReplaceAllString(v, "${1}[redacted]")
	v = httpURL.ReplaceAllStringFunc(v, func(m string) string {
		parsed, err := url.Parse(m)
		if err != nil || parsed.RawQuery == "" {
			return m
		}
		parsed.RawQuery = "redacted"
		return parsed.String()
	})
	// Tail pass: user-registered redactions (applied in registration
	// order).  Snapshot the slice under the mutex so the hot path of
	// Redact() doesn't hold the lock while running regex substitutions.
	registeredMu.Lock()
	tail := make([]registeredRegex, len(registeredRedactions))
	copy(tail, registeredRedactions)
	registeredMu.Unlock()
	for _, rr := range tail {
		v = rr.re.ReplaceAllString(v, rr.replacement)
	}
	return v
}

// Error returns a redacted error string clipped to max runes. max<=0
// defaults to 240.
func Error(err error, max int) string {
	if err == nil {
		return ""
	}
	return Text(err.Error(), max)
}

// Text redacts value and clips it to max runes (default 240 when max<=0).
func Text(value string, max int) string {
	value = Redact(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if max <= 0 {
		max = 240
	}
	runes := []rune(value)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return value
}

// URLLabel returns scheme+host+path for raw (with query redacted).
func URLLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Text(raw, 120)
	}
	p := strings.TrimRight(parsed.EscapedPath(), "/")
	if p == "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return Text(fmt.Sprintf("%s://%s%s", parsed.Scheme, parsed.Host, p), 160)
}

// RequestPathLabel returns just the path portion of u (no query, no fragment)
// for use in request telemetry logs. The result is always safe to log: it
// contains only the URL path with query/fragment unconditionally stripped, and
// is passed through the global redaction pipeline with a length cap as a
// defence-in-depth measure.
//
// Unlike URLLabel, this function works directly on *url.URL and never falls
// back to the raw RequestURI, so relative request paths (the common case for
// incoming HTTP requests) are guaranteed to have their query dropped.
func RequestPathLabel(u *url.URL) string {
	if u == nil {
		return ""
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	if p == "" {
		p = "/"
	}
	return Text(p, 160)
}

// HostLabel returns the host portion of raw, falling back to a clipped
// redacted raw string if it cannot be parsed.
func HostLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return Text(raw, 80)
	}
	return parsed.Host
}
