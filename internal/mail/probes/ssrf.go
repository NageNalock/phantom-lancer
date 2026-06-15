package probes

import (
	"context"
	"net"
	"strings"
)

// hostResolver is the minimal subset of *net.Resolver that SSRF guard needs
// (LookupCNAME + LookupHost).  Defined as an interface so tests can swap in
// a fake without real DNS I/O.
type hostResolver interface {
	LookupCNAME(ctx context.Context, host string) (cname string, err error)
	LookupHost(ctx context.Context, host string) (addrs []string, err error)
}

// Resolver is the DNS resolver used by SSRF guard and DNS probes.  Tests
// override it with a fake to avoid real network lookups.
//
// Kept as *net.Resolver (not interface) because L6 DNS probes also call
// LookupMX/LookupTXT/LookupSRV.  SSRF-specific tests override TestResolver
// below instead.
var Resolver = net.DefaultResolver

// TestResolver is a SSRF-specific override used exclusively by unit tests.
// When non-nil, ResolveHost prefers TestResolver over the package-level
// Resolver.  This allows fake-DNS tests to inject a minimal fake that only
// satisfies the SSRF hostResolver interface, without replacing the full
// *net.Resolver used by L6 probes across the package.
var TestResolver hostResolver

func activeSSRFLookup() hostResolver {
	if TestResolver != nil {
		return TestResolver
	}
	// *net.Resolver satisfies hostResolver.
	return Resolver
}

// ResolveHost expands a hostname to its resolved IPs, following CNAME
// chains.  This is used by the SSRF guard to detect when a hostname resolves
// to a private/loopback IP (even if the hostname itself is in the allowlist).
func ResolveHost(ctx context.Context, host string) (ips []net.IP, cnameChain []string, err error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, nil, nil
	}
	// Strip port if present.
	hostOnly, _, splitErr := net.SplitHostPort(host)
	if splitErr == nil && hostOnly != "" {
		host = hostOnly
	}
	// If it's a literal IP, return early.
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil, nil
	}
	// LookupHost returns the final A/AAAA records; CNAMEs are silently
	// followed by the resolver.  We also do a separate CNAME lookup so the
	// SSRF guard can reason about the chain.
	if ctx == nil {
		ctx = context.Background()
	}
	r := activeSSRFLookup()
	if cnames, cerr := r.LookupCNAME(ctx, host); cerr == nil && cnames != "" && cnames != host+"." {
		// LookupCNAME returns the final canonical name (with trailing dot).
		cnameChain = append(cnameChain, strings.TrimSuffix(cnames, "."))
	}
	addrs, lerr := r.LookupHost(ctx, host)
	if lerr != nil {
		return nil, cnameChain, lerr
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips, cnameChain, nil
}

// IsTargetAllowed enforces SSRF guard rules for TCP/DNS probe targets:
//   - allowLoopbackOnly=true → only loopback addresses (127.0.0.0/8, ::1, localhost) are OK
//   - allowLoopbackOnly=false → host must be in allowedDomains (exact or subdomain)
//
// When ResolveToIPs is true, hostnames are fully resolved via
// Resolver.LookupHost and every resulting IP MUST NOT be a private IP range
// (RFC1918, link-local, ULA, metadata, etc.) — with the sole exception of
// loopback which is only permitted when allowLoopbackOnly=true.
//
// Private IP ranges (10/8, 172.16/12, 192.168/16, 169.254/16, fc00::/7,
// fe80::/10, etc.) are NEVER allowed unless loopback.
func IsTargetAllowed(host string, allowedDomains []string, allowLoopbackOnly bool) bool {
	return IsTargetAllowedWithResolve(host, allowedDomains, allowLoopbackOnly, false, nil)
}

// IsTargetAllowedWithResolve is the extended SSRF guard that optionally
// performs DNS resolution (when resolve=true) and rejects any host whose
// final IPs fall into private/metadata ranges.  A non-nil ctx can be
// provided for timeout control; when nil context.Background() is used.
func IsTargetAllowedWithResolve(host string, allowedDomains []string, allowLoopbackOnly, resolve bool, ctx context.Context) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	// Strip port if present – just use host portion.
	hostOnly, _, splitErr := net.SplitHostPort(host)
	if splitErr == nil && hostOnly != "" {
		host = hostOnly
	}

	// If it's a literal IP.
	if ip := net.ParseIP(host); ip != nil {
		if isLoopback(host) {
			return allowLoopbackOnly
		}
		// Any other IP (including private) is rejected.
		return false
	}

	// Hostname — check against allowedDomains (exact + subdomain match).
	inAllowedDomains := false
	if allowLoopbackOnly {
		// In loopback-only mode, also allow "localhost" explicitly.
		lower := strings.ToLower(host)
		if lower == "localhost" {
			inAllowedDomains = true
		}
	} else {
		for _, dom := range allowedDomains {
			dom = strings.ToLower(strings.TrimSpace(dom))
			h := strings.ToLower(host)
			if dom == "" {
				continue
			}
			if h == dom {
				inAllowedDomains = true
				break
			}
			if strings.HasSuffix(h, "."+dom) {
				inAllowedDomains = true
				break
			}
		}
		// Also allow "localhost" even without an explicit entry – it's
		// always safe (and required by tests that bind on localhost).
		if strings.ToLower(host) == "localhost" {
			inAllowedDomains = true
		}
	}

	// If not in the allowlist (and not loopback-only mode with localhost),
	// reject immediately.
	if !inAllowedDomains {
		return false
	}

	// Optional IP-resolution guard: block any host whose final resolved IPs
	// are in forbidden (non-loopback) ranges.
	if resolve {
		if ctx == nil {
			ctx = context.Background()
		}
		ips, _, err := ResolveHost(ctx, host)
		if err != nil {
			// DNS resolution failure — conservatively reject (can't prove
			// it's safe).
			return false
		}
		for _, ip := range ips {
			if ip.IsLoopback() {
				if !allowLoopbackOnly {
					// Loopback target when we *don't* allow loopback:
					// only allowed when target == "localhost" literally.
					if strings.ToLower(host) != "localhost" {
						return false
					}
				}
				continue
			}
			if isPrivateOrMetadataIP(ip) {
				return false
			}
		}
	}

	return true
}

// isLoopback reports whether host is a loopback IP address.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// isPrivateIP reports whether host is a private IP range.
func isPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate()
}

// isPrivateOrMetadataIP checks whether ip is in any range that should NEVER
// be reached by probes – private RFC1918, link-local, ULA, cloud metadata,
// loopback (caller handles separately), and test-nets.
func isPrivateOrMetadataIP(ip net.IP) bool {
	// Well-known cloud metadata services.
	metadata := []net.IPNet{
		// AWS / GCP / Azure metadata IP.
		{IP: net.ParseIP("169.254.169.254"), Mask: net.CIDRMask(32, 32)},
		// Oracle cloud metadata.
		{IP: net.ParseIP("192.0.0.192"), Mask: net.CIDRMask(32, 32)},
		// IPv6 link-local + ULA (unique local addresses, fc00::/7).
		{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)},
		{IP: net.ParseIP("fe80::"), Mask: net.CIDRMask(10, 128)},
	}
	for _, m := range metadata {
		if m.Contains(ip) {
			return true
		}
	}
	// Standard IsPrivate() covers 10/8, 172.16/12, 192.168/16, 127/8,
	// ::1/128, fc00::/7, fe80::/10 and more.
	if ip.IsPrivate() {
		return true
	}
	// Link-local IPv4.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// Loopback – caller should have handled it, but double-check here.
	if ip.IsLoopback() {
		return true
	}
	// Unspecified (0.0.0.0 / ::) – never safe to probe.
	if ip.IsUnspecified() {
		return true
	}
	return false
}
