package mail

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"phantom-lancer/internal/mail/probes"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// DNSBL probe: MX → IP → DNSBL source lookups.
// -----------------------------------------------------------------------------

// DefaultDNSBLSources is the default RFC 5782 DNSBL zone list consulted by
// DNSBLProbeAll.  These are widely accepted free-to-query public lists; the
// operator can override the list in future via a settings row.
var DefaultDNSBLSources = []string{
	"zen.spamhaus.org",
	"bl.spamcop.net",
	"dbl.spamhaus.org",
	"combined.mail-abuse.org",
	"uribl.swinog.ch",
}

// dnsblAllowedDomains is the SSRF allow-list for DNSBL probe targets.  All
// DNSBL sources must match exactly or as a subdomain of an entry here.
// The probes.IsTargetAllowed helper rejects private / loopback / unlisted
// hostnames by default.
var dnsblAllowedDomains = []string{
	"spamhaus.org",
	"spamcop.net",
	"mail-abuse.org",
	"swinog.ch",
}

// DNSBLProbeAll resolves the MX records of every registered domain,
// flattens them to a unique set of IPs, and queries each (IP × DNSBL
// source) via reverse-zone DNS lookups (RFC 5782).  It returns the full
// per-IP-per-source result set plus aggregated summary counts.
func (s *Service) DNSBLProbeAll(ctx context.Context) (*storage.DNSBLProbeResponse, error) {
	// Step 1: fetch registered domains.  MailListDomains is currently a
	// stub in Phase 3 so we degrade gracefully if it returns not
	// implemented (returns empty results rather than a hard error).
	domains, derr := s.store.MailListDomains(ctx)
	if derr != nil {
		if isNotImplemented(derr) {
			// No real CRUD yet — warn the operator but return an empty
			// probe response so the UI renders something useful.
			s.log.WarnContext(ctx, "dnsbl: MailListDomains not implemented; returning empty result", "error", derr)
			return emptyDNSBLResponse(), nil
		}
		return nil, fmt.Errorf("dnsbl: list domains: %w", derr)
	}
	// Step 2: for each domain, resolve MX → MX host → A/AAAA → unique IPs.
	ipSet := map[string]struct{}{}
	for _, d := range domains {
		if d == nil || d.Domain == "" || !d.Enabled {
			continue
		}
		hosts, merr := resolveMX(ctx, d.Domain)
		if merr != nil {
			s.log.DebugContext(ctx, "dnsbl: mx resolve failed", "domain", d.Domain, "error", merr)
			// Fallback: also try the bare domain as an A record, some
			// small installs don't set MX and deliver directly.
			hosts = append(hosts, d.Domain)
		}
		for _, h := range hosts {
			ips, ierr := resolveIPs(ctx, h)
			if ierr != nil {
				s.log.DebugContext(ctx, "dnsbl: ip resolve failed", "host", h, "error", ierr)
				continue
			}
			for _, ip := range ips {
				ipSet[ip] = struct{}{}
			}
		}
	}
	// Step 3: build (IP × source) probe list and run lookups.
	resp := &storage.DNSBLProbeResponse{
		LastRunAt: time.Now().UTC().Format(time.RFC3339),
		Results:   []storage.DNSBLResult{},
	}
	// Track per-IP listing counts (for severity summary).
	ipListedCount := map[string]int{}
	totalIPs := len(ipSet)
	resolver := &net.Resolver{PreferGo: true}
	for ip := range ipSet {
		for _, src := range DefaultDNSBLSources {
			// SSRF guard: ensure the DNSBL source is on the allow-list.
			if !probes.IsTargetAllowed(src, dnsblAllowedDomains, false) {
				s.log.WarnContext(ctx, "dnsbl: skipping SSRF-disallowed source", "source", src)
				continue
			}
			listed, code, lerr := dnsblQuery(ctx, resolver, ip, src)
			r := storage.DNSBLResult{
				IP:     ip,
				Source: src,
				Listed: listed,
				Code:   code,
			}
			switch {
			case !listed:
				r.Severity = "good"
			default:
				r.Severity = "warn"
				ipListedCount[ip]++
			}
			resp.Results = append(resp.Results, r)
			if lerr != nil {
				s.log.DebugContext(ctx, "dnsbl: query result",
					"ip", ip, "source", src, "listed", listed, "code", code, "error", lerr)
			}
		}
	}
	// Step 4: summary counts.
	resp.Summary.TotalIPs = totalIPs
	for ip, count := range ipListedCount {
		_ = ip
		resp.Summary.ListedCount++
		if count >= 3 {
			resp.Summary.CriticalCount++
			// Upgrade severity to "critical" for any listing row on that IP
			// so the UI highlights them.
			for i := range resp.Results {
				if resp.Results[i].IP == ip && resp.Results[i].Listed {
					resp.Results[i].Severity = "critical"
				}
			}
		} else if count >= 1 {
			resp.Summary.WarnCount++
		}
	}
	// Step 5: audit + publish.  Low-severity diagnostic; never blocks.
	s.addAudit(ctx, "mail.reputation.dnsbl_probe",
		fmt.Sprintf("ran dnsbl probe: %d ips, %d listed, %d critical, %d warn",
			resp.Summary.TotalIPs, resp.Summary.ListedCount, resp.Summary.CriticalCount, resp.Summary.WarnCount),
		map[string]any{
			"total_ips":     resp.Summary.TotalIPs,
			"listed_count":  resp.Summary.ListedCount,
			"critical_count": resp.Summary.CriticalCount,
			"warn_count":    resp.Summary.WarnCount,
		}, "low")
	if resp.Summary.ListedCount > 0 {
		s.publish(ctx, EventTypeReputationDNSBLHit, map[string]any{
			"total_ips":     resp.Summary.TotalIPs,
			"listed_count":  resp.Summary.ListedCount,
			"critical_count": resp.Summary.CriticalCount,
			"warn_count":    resp.Summary.WarnCount,
		})
	} else {
		s.publish(ctx, EventTypeReputationDNSBLClear, map[string]any{
			"total_ips": resp.Summary.TotalIPs,
		})
	}
	return resp, nil
}

// emptyDNSBLResponse returns a zero-filled response for cases where no
// domains are registered yet (or the storage layer is stubbed out).
func emptyDNSBLResponse() *storage.DNSBLProbeResponse {
	return &storage.DNSBLProbeResponse{
		LastRunAt: time.Now().UTC().Format(time.RFC3339),
		Results:   []storage.DNSBLResult{},
	}
}

// isNotImplemented reports whether an error carries the storage-level
// "not implemented in phase 1" marker so we can degrade gracefully.
func isNotImplemented(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errNotImplementedMarker) ||
		strings.Contains(err.Error(), "not implemented")
}

// errNotImplementedMarker is compared via errors.Is using sentinel semantics;
// we construct an ad-hoc one to match against strings above.
var errNotImplementedMarker = errors.New("not implemented")

// --- DNS helpers ------------------------------------------------------------

// resolveMX returns the MX target hostnames for a domain, sorted by
// preference (lowest first).  If no MX records exist, returns an error so
// the caller can fall back to the bare domain.
func resolveMX(ctx context.Context, domain string) ([]string, error) {
	r := &net.Resolver{PreferGo: true}
	mxs, err := r.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		if mx == nil {
			continue
		}
		h := strings.TrimSuffix(mx.Host, ".")
		if h != "" {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no mx records")
	}
	return out, nil
}

// resolveIPs resolves a hostname to a list of unique IPs (both v4 and v6).
func resolveIPs(ctx context.Context, host string) ([]string, error) {
	r := &net.Resolver{PreferGo: true}
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var out []string
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		// Skip loopback / link-local / private addresses to avoid leaking
		// internal topology via DNSBL queries.
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out, nil
}

// dnsblQuery performs a single RFC 5782 reverse-zone lookup.
// Returns (listed bool, responseCode string, err).
//
// For IPv4: reverse octets (4.3.2.1 for 1.2.3.4) + "." + source
// For IPv6: reverse nibbles (hex) + "." + source
func dnsblQuery(ctx context.Context, resolver *net.Resolver, ipStr, source string) (bool, string, error) {
	if resolver == nil {
		resolver = &net.Resolver{PreferGo: true}
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false, "", fmt.Errorf("invalid ip %q", ipStr)
	}
	var rev string
	if v4 := ip.To4(); v4 != nil {
		rev = fmt.Sprintf("%d.%d.%d.%d", v4[3], v4[2], v4[1], v4[0])
	} else {
		// IPv6: reverse the hex encoding of the full 16 bytes, nibble by nibble.
		full := make([]byte, hex.EncodedLen(len(ip)))
		hex.Encode(full, ip)
		// Expand every nibble: already hex-encoded, just reverse the order
		// and join with dots.
		parts := make([]string, 0, len(full))
		for i := len(full) - 1; i >= 0; i-- {
			parts = append(parts, string(full[i]))
		}
		rev = strings.Join(parts, ".")
	}
	query := rev + "." + source
	ips, err := resolver.LookupIPAddr(ctx, query)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, "", nil
		}
		return false, "", err
	}
	listed := false
	var lastOctet string
	for _, a := range ips {
		v4 := a.IP.To4()
		if v4 == nil {
			continue
		}
		// Only 127.0.0.x responses count as listed per the DNSBL convention.
		if v4[0] == 127 && v4[1] == 0 && v4[2] == 0 && v4[3] > 0 {
			listed = true
			lastOctet = fmt.Sprintf("%d", v4[3])
		}
	}
	return listed, lastOctet, nil
}
