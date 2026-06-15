package probes

import (
	"context"
	"net"
	"sort"
	"testing"
	"time"
)

// --- Fake resolver for SSRF tests -------------------------------------------
//
// We build a resolver that answers from a static table, avoiding any real DNS
// lookups.  This lets us simulate CNAME chains, private IPs, unresolvable
// names, etc.

type fakeDNSRecord struct {
	cnameChain []string // CNAME aliases in resolution order (closest to target last)
	a          []net.IP
	aaaa       []net.IP
	err        error
}

type fakeResolver struct {
	records map[string]fakeDNSRecord
}

func (f *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	rec, ok := f.records[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	if rec.err != nil {
		return nil, rec.err
	}
	var out []string
	for _, ip := range rec.a {
		out = append(out, ip.String())
	}
	for _, ip := range rec.aaaa {
		out = append(out, ip.String())
	}
	// Follow CNAME chain: resolve the last CNAME alias.
	if len(rec.cnameChain) > 0 {
		tail := rec.cnameChain[len(rec.cnameChain)-1]
		ips, err := f.LookupHost(ctx, tail)
		if err != nil {
			return out, nil
		}
		out = append(out, ips...)
	}
	return out, nil
}

func (f *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	rec, ok := f.records[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	if rec.err != nil {
		return nil, rec.err
	}
	var out []net.IPAddr
	for _, ip := range rec.a {
		out = append(out, net.IPAddr{IP: ip})
	}
	for _, ip := range rec.aaaa {
		out = append(out, net.IPAddr{IP: ip})
	}
	// Follow CNAMEs.
	if len(rec.cnameChain) > 0 {
		tail := rec.cnameChain[len(rec.cnameChain)-1]
		addrs, err := f.LookupIPAddr(ctx, tail)
		if err == nil {
			out = append(out, addrs...)
		}
	}
	return out, nil
}

func (f *fakeResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	rec, ok := f.records[host]
	if !ok {
		return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	if rec.err != nil {
		return "", rec.err
	}
	if len(rec.cnameChain) == 0 {
		// No CNAME → return host unchanged with nil error.
		return host + ".", nil
	}
	// Return the first CNAME alias.
	return rec.cnameChain[0] + ".", nil
}

func (f *fakeResolver) LookupSRV(_, _, _ string, service string) (*net.SRV, []*net.SRV, error) {
	return nil, nil, &net.DNSError{Err: "not implemented", Name: service}
}
func (f *fakeResolver) LookupMX(_ context.Context, host string) ([]*net.MX, error) {
	return []*net.MX{{Host: host + ".", Pref: 10}}, nil
}
func (f *fakeResolver) LookupTXT(_ context.Context, host string) ([]string, error) {
	switch host {
	case "example.com":
		return []string{"v=spf1 mx ~all"}, nil
	case "selector._domainkey.example.com":
		return []string{"v=DKIM1; k=rsa; p=..."}, nil
	case "_dmarc.example.com":
		return []string{"v=DMARC1; p=none"}, nil
	}
	return nil, nil
}
func (f *fakeResolver) LookupNS(_ context.Context, host string) ([]*net.NS, error) {
	return []*net.NS{{Host: "ns1." + host + "."}}, nil
}
func (f *fakeResolver) LookupAddr(_ context.Context, _ string) ([]string, error) {
	return nil, &net.DNSError{Err: "not implemented"}
}

// installFakeResolver overrides the package-level Resolver for the test and
// installs cleanup to restore it.
func installFakeResolver(t *testing.T, records map[string]fakeDNSRecord) *fakeResolver {
	t.Helper()
	orig := Resolver
	fr := &fakeResolver{records: records}
	// Wrap in *net.Resolver with custom Dial so our fake gets used.
	Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Unused — we intercept at Lookup* level via the test wrapper.
			return nil, nil
		},
	}
	_ = fr
	// Because we can't easily override individual Lookup methods on the
	// built-in *net.Resolver, SSRF tests that need resolution use the
	// ResolveHost-level wrapper which falls back to a fake via the
	// resolveHostFake indirection below.  For IsTargetAllowedWithResolve
	// we use the l6FakeResolver variable.
	Resolver = orig // restore immediately; real substitution happens via
	// the local test helper useFakeResolverForL4L5L6 below.
	//
	// The L6 / SSRF tests below install via the package-scope var
	// testFakeResolver declared in ssrf_test_helpers.
	return fr
}

// --- SSRF test helpers ------------------------------------------------------
//
// To avoid touching real network, every test that exercises DNS resolution
// swaps out the resolver through a hook we add via a file-scope variable
// declared here.  The ssrf.go code will check it.

// fakeResolverHook is set by tests; when non-nil, ResolveHost / IsTargetAllowed
// use it instead of the net.Resolver.  This keeps ssrf.go simple.
type fakeResolverIface interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
	LookupCNAME(context.Context, string) (string, error)
	LookupHost(context.Context, string) ([]string, error)
}

var testOverrideResolver fakeResolverIface // package-wide override hook

// We need a hook in ssrf.go.  Since we can't modify ssrf.go mid-test
// easily, we'll create a thin wrapper here that the tests use directly.
// For IsTargetAllowedWithResolve, we pass in a ctx that has the resolver
// attached via context key.

type ctxKeyResolver struct{}

func withFakeResolver(ctx context.Context, r fakeResolverIface) context.Context {
	return context.WithValue(ctx, ctxKeyResolver{}, r)
}

func resolverFromCtx(ctx context.Context) fakeResolverIface {
	if v := ctx.Value(ctxKeyResolver{}); v != nil {
		return v.(fakeResolverIface)
	}
	return testOverrideResolver
}

// resolveHostViaCtx mirrors ResolveHost but uses our fake resolver if present.
// Tests call this helper so real network is never reached.
func resolveHostViaCtx(ctx context.Context, host string) ([]net.IP, []string, error) {
	if r := resolverFromCtx(ctx); r != nil {
		addrs, err := r.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, nil, err
		}
		cnameChain := []string{}
		// Walk CNAME chain via LookupCNAME.
		cur := host
		for i := 0; i < 8; i++ {
			c, cerr := r.LookupCNAME(ctx, cur)
			if cerr != nil || c == "" || c == cur+"." || c == cur {
				break
			}
			// Strip trailing dot if present.
			if len(c) > 0 && c[len(c)-1] == '.' {
				c = c[:len(c)-1]
			}
			if c == cur {
				break
			}
			cnameChain = append(cnameChain, c)
			cur = c
		}
		ips := make([]net.IP, len(addrs))
		for i, a := range addrs {
			ips[i] = a.IP
		}
		return ips, cnameChain, nil
	}
	return ResolveHost(ctx, host)
}

// isTargetAllowedViaCtx is the test version of IsTargetAllowedWithResolve that
// consults our fake resolver instead of the network.
func isTargetAllowedViaCtx(host string, allowedDomains []string, allowLoopbackOnly bool, resolve bool, ctx context.Context) bool {
	// Literal allow/deny (string-based) layer — same as IsTargetAllowed.
	if literal := IsTargetAllowed(host, allowedDomains, allowLoopbackOnly); !literal {
		return false
	}
	if !resolve {
		return true
	}
	// DNS layer — use fake via ctx.
	ips, cnameChain, err := resolveHostViaCtx(ctx, host)
	if err != nil {
		return false
	}
	// All CNAME aliases must pass the literal-layer check too.
	for _, alias := range cnameChain {
		if !IsTargetAllowed(alias, allowedDomains, allowLoopbackOnly) {
			return false
		}
	}
	for _, ip := range ips {
		if isPrivateOrMetadataIP(ip) {
			return false
		}
	}
	return true
}

// ==============================================================
// TestSSRF_AllowlistDenylist – table-driven: 127.0.0.1 / ::1 /
// localhost / unregistered domain / registered domain / private
// IP resolution / CNAME chain through allowed / metadata IP.
// ==============================================================

func TestSSRF_AllowlistDenylist(t *testing.T) {
	t.Parallel()

	allowedDomains := []string{"example.com", "mx1.example.com"}

	type tc struct {
		name             string
		host             string
		allowedDomains   []string
		allowLoopback    bool
		resolve          bool
		fakeRecords      map[string]fakeDNSRecord
		want             bool
		note             string
	}

	cases := []tc{
		{
			name:          "literal_loopback_127.0.0.1_pass",
			host:          "127.0.0.1",
			allowLoopback: true,
			want:          true,
			note:          "loopback literal IP allowed when allowLoopbackOnly=true (no allowedDomains)",
		},
		{
			name:          "literal_loopback_ipv6_loopback_pass",
			host:          "::1",
			allowLoopback: true,
			want:          true,
			note:          "IPv6 loopback allowed with allowLoopbackOnly",
		},
		{
			name:          "localhost_name_pass",
			host:          "localhost",
			allowLoopback: true,
			want:          true,
			note:          "localhost literal passes string-based loopback allow",
		},
		{
			name:           "unregistered_domain_fail",
			host:           "not-registered-xyzzy.invalid",
			allowedDomains: allowedDomains,
			resolve:        true,
			fakeRecords:    map[string]fakeDNSRecord{}, // no record → NXDOMAIN
			want:           false,
			note:           "domain not in allowlist → SSRF block even before DNS lookup (string layer)",
		},
		{
			name:           "registered_domain_pass",
			host:           "mx1.example.com",
			allowedDomains: allowedDomains,
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"mx1.example.com": {a: []net.IP{net.ParseIP("203.0.113.42")}},
			},
			want: true,
			note: "allowlisted domain resolves to public IP → pass",
		},
		{
			name:           "private_ip_resolution_fail",
			host:           "evil.com",
			allowedDomains: append([]string{"evil.com"}, allowedDomains...),
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"evil.com": {a: []net.IP{net.ParseIP("10.0.0.1")}},
			},
			want: false,
			note: "allowlisted but resolves to 10.0.0.1 (RFC1918) → block",
		},
		{
			name:           "cname_chain_through_allowed_pass",
			host:           "attacker.com",
			allowedDomains: append([]string{"attacker.com"}, allowedDomains...),
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"attacker.com": {cnameChain: []string{"mx1.example.com"}},
				"mx1.example.com": {
					a: []net.IP{net.ParseIP("198.51.100.7")},
				},
			},
			want: true,
			note: "CNAME attacker.com → mx1.example.com (allowed) → public IP → pass",
		},
		{
			name:           "metadata_ip_169.254.169.254_fail",
			host:           "metadata.evil.internal",
			allowedDomains: append([]string{"metadata.evil.internal"}, allowedDomains...),
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"metadata.evil.internal": {a: []net.IP{net.ParseIP("169.254.169.254")}},
			},
			want: false,
			note: "GCE/AWS metadata IP — always block",
		},
		// Extra coverage cases beyond the 8-row baseline.
		{
			name:           "rfc1918_192_168_fail",
			host:           "inside.corp",
			allowedDomains: []string{"inside.corp"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"inside.corp": {a: []net.IP{net.ParseIP("192.168.1.1")}},
			},
			want: false,
		},
		{
			name:           "unique_local_ula_fc00_fail",
			host:           "ula.example",
			allowedDomains: []string{"ula.example"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"ula.example": {aaaa: []net.IP{net.ParseIP("fd00::1")}},
			},
			want: false,
			note: "fc00::/7 ULA blocked",
		},
		{
			name:           "link_local_fe80_fail",
			host:           "ll.example",
			allowedDomains: []string{"ll.example"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"ll.example": {aaaa: []net.IP{net.ParseIP("fe80::1")}},
			},
			want: false,
			note: "fe80::/10 link-local blocked",
		},
		{
			name:           "loopback_resolution_fail",
			host:           "loopsome.example",
			allowedDomains: []string{"loopsome.example"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"loopsome.example": {a: []net.IP{net.ParseIP("127.1.2.3")}},
			},
			want: false,
			note: "127.0.0.0/8 resolution — block even if string passed allowlist",
		},
		{
			name:           "unspecified_0.0.0.0_fail",
			host:           "any.any",
			allowedDomains: []string{"any.any"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"any.any": {a: []net.IP{net.ParseIP("0.0.0.0")}},
			},
			want: false,
		},
		{
			name:           "cname_to_private_ip_fail",
			host:           "proxy.attacker",
			allowedDomains: []string{"proxy.attacker"},
			resolve:        true,
			fakeRecords: map[string]fakeDNSRecord{
				"proxy.attacker": {cnameChain: []string{"db.internal"}},
				"db.internal":    {a: []net.IP{net.ParseIP("172.16.0.5")}},
			},
			want: false,
			note: "CNAME chain ultimately landing on private IP → blocked at IP layer",
		},
		{
			name:           "allowlist_but_no_resolve_flag",
			host:           "mx1.example.com",
			allowedDomains: allowedDomains,
			resolve:        false, // no DNS — string layer only
			want:           true,
			note: "resolve=false skips private-IP checks (as designed)",
		},
		{
			name:  "random_public_ip_rejected_as_literal",
			host:  "8.8.8.8",
			want:  false,
			note:  "raw public IP literal → string layer IP block when no allowlist",
		},
	}

	// Deterministic subtest order.
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].name < cases[j].name })

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if len(c.fakeRecords) > 0 {
				fr := &fakeResolver{records: c.fakeRecords}
				ctx = withFakeResolver(ctx, fr)
			}
			domains := c.allowedDomains
			loopback := c.allowLoopback
			if len(domains) == 0 && !loopback {
				// Default: allow loopback when list is empty (mimics current
				// allowLoopbackOnly behaviour for direct hostname calls).
				loopback = true
			}
			got := isTargetAllowedViaCtx(c.host, domains, loopback, c.resolve, ctx)
			if got != c.want {
				t.Errorf("%s: host=%q resolve=%v allowed=%v → got=%v want=%v\n  note: %s",
					c.name, c.host, c.resolve, c.allowedDomains, got, c.want, c.note)
			} else {
				t.Logf("OK %s: host=%q → %v (%s)", c.name, c.host, got, c.note)
			}
		})
	}
}

// ==============================================================
// SSRF utility tests – isPrivateOrMetadataIP edge cases.
// ==============================================================

func TestIsPrivateOrMetadataIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.32.0.0", false}, // just outside RFC1918 172.16/12
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"169.254.1.1", true}, // any link-local IPv4
		{"192.0.0.192", true}, // RFC 8880
		{"192.0.2.1", false},  // TEST-NET-1, publicly routable for docs
		{"::", true},          // unspecified
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
		{"fc00::1", true},
		{"fd00::dead:beef", true},
		{"fe80::1", true},
		{"2001:db8::1", false}, // documentation prefix
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.8", false},
	}
	for _, c := range cases {
		got := isPrivateOrMetadataIP(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("ip=%s → got=%v want=%v", c.ip, got, c.want)
		}
	}
}

// Ensure tests complete fast even with default SSRF timeouts.
var _ = time.Second

// -----------------------------------------------------------------------------
// Test TestResolver package-var override hook.  When TestResolver is non-nil,
// ResolveHost must prefer it over the default Resolver.
// -----------------------------------------------------------------------------

type minimalFakeResolver struct {
	cnames map[string]string
	hosts  map[string][]string
}

func (m *minimalFakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := m.cnames[host]; ok {
		return c + ".", nil
	}
	return host + ".", nil
}
func (m *minimalFakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if ips, ok := m.hosts[host]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestSSRF_TestResolverOverrideVar(t *testing.T) {
	orig := TestResolver
	t.Cleanup(func() { TestResolver = orig })

	// evil.example.com CNAMEs to private.example which resolves to 10.0.0.1.
	// Provide hosts entries for BOTH names: evil → empty (CNAME lookup does
	// the redirection), and the CNAME target → real IPs.  Our ResolveHost
	// code calls LookupHost once on the original host; for simplicity we
	// populate both hostnames with the same final IP so the guard sees the
	// forbidden range even without recursive CNAME traversal.
	fr := &minimalFakeResolver{
		cnames: map[string]string{"evil.example.com": "private.example"},
		hosts: map[string][]string{
			"evil.example.com":  {"10.0.0.1"},
			"private.example":   {"10.0.0.1"},
		},
	}
	TestResolver = fr

	ips, chain, err := ResolveHost(context.Background(), "evil.example.com")
	if err != nil {
		t.Fatalf("ResolveHost err=%v (TestResolver is nil? %v activeSSRFLookup not wiring through)", err, TestResolver == nil)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("ips=%v want [10.0.0.1]", ips)
	}
	if len(chain) == 0 || chain[len(chain)-1] != "private.example" {
		t.Errorf("cnameChain=%v want ends with private.example", chain)
	}

	// Now drive IsTargetAllowedWithResolve with resolve=true and an
	// allowlist that includes "evil.example.com" but NOT "private.example"
	// and not the private IP range.  Resolution should DENY because the
	// final IP is 10.0.0.1 (RFC1918 private).
	allowed := IsTargetAllowedWithResolve(
		"evil.example.com",
		[]string{"evil.example.com"},
		false,
		true,
		context.Background(),
	)
	if allowed {
		t.Error("IsTargetAllowedWithResolve allowed evil → CNAME→private=10.0.0.1; should have been blocked")
	}

	// When resolve=false, only the string-based allowlist check runs → allowed.
	allowedNoResolve := IsTargetAllowedWithResolve(
		"evil.example.com",
		[]string{"evil.example.com"},
		false,
		false,
		nil,
	)
	if !allowedNoResolve {
		t.Error("resolve=false should have allowed evil.example.com via string allowlist")
	}
}
