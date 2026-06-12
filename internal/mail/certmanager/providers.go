package certmanager

import (
	"context"
	"fmt"
	"strings"
)

// ---- Provider config structs ------------------------------------------------
//
// Each struct holds PLAINTEXT tokens when passed across the API boundary.
// The storage layer (storage_mail.go) encrypts these via wrapMailSecret
// before writing SQLite, and decrypts on read.  The certmanager package
// never logs or persists them — it only hands them off to the real lego
// adapters when those are vendored in a later PR.

// CloudflareConfig is the connection payload for Cloudflare's v4 API.
type CloudflareConfig struct {
	APIToken string // scoped to "Zone:DNS:Edit"
	ZoneID   string // optional; empty = auto-lookup via FQDN
}

// DNSPodConfig is the connection payload for Tencent Cloud DNSPod.
type DNSPodConfig struct {
	SecretID  string
	SecretKey string
	ZoneID    string // optional; empty = auto-lookup via FQDN
}

// Route53Config is the connection payload for AWS Route 53.
type Route53Config struct {
	AccessKey     string
	SecretKey     string
	Region        string // usually us-east-1 for global Route 53
	HostedZoneID  string // optional; empty = auto-lookup via FQDN
}

// ---- Concrete provider stubs ------------------------------------------------
//
// The methods below return descriptive errors that include the FQDN and
// the *length* (never the contents) of the token so operators can tell
// whether the secret was actually populated without the error payload
// itself leaking credential material.  A future PR will swap these out
// for github.com/go-acme/lego/v4/providers/dns/* adapters without
// changing any call site.

// CloudflareProvider is the stub implementation.
type CloudflareProvider struct {
	ID       string
	Label    string
	Config   CloudflareConfig
}

// ProviderID implements DNSProvider.
func (p *CloudflareProvider) ProviderID() string { return p.ID }

// DisplayName implements DNSProvider.
func (p *CloudflareProvider) DisplayName() string { return p.Label }

// TestConnection implements DNSProvider.
func (p *CloudflareProvider) TestConnection(ctx context.Context) error {
	_ = ctx
	if p.Config.APIToken == "" {
		return fmt.Errorf("Cloudflare TestConnection: empty APIToken")
	}
	return fmt.Errorf("Cloudflare TestConnection is not wired: token_len=%d zone_len=%d",
		len(p.Config.APIToken), len(p.Config.ZoneID))
}

// SetTXT implements DNSProvider.
func (p *CloudflareProvider) SetTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("Cloudflare SetTXT is not wired: fqdn=%s token_len=%d value_len=%d",
		fqdn, len(p.Config.APIToken), len(value))
}

// RemoveTXT implements DNSProvider.
func (p *CloudflareProvider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("Cloudflare RemoveTXT is not wired: fqdn=%s token_len=%d value_len=%d",
		fqdn, len(p.Config.APIToken), len(value))
}

// DNSPodProvider is the stub implementation.
type DNSPodProvider struct {
	ID     string
	Label  string
	Config DNSPodConfig
}

func (p *DNSPodProvider) ProviderID() string  { return p.ID }
func (p *DNSPodProvider) DisplayName() string { return p.Label }
func (p *DNSPodProvider) TestConnection(ctx context.Context) error {
	_ = ctx
	if p.Config.SecretID == "" || p.Config.SecretKey == "" {
		return fmt.Errorf("DNSPod TestConnection: empty SecretID/SecretKey")
	}
	return fmt.Errorf("DNSPod TestConnection is not wired: secret_id_len=%d zone_len=%d",
		len(p.Config.SecretID), len(p.Config.ZoneID))
}
func (p *DNSPodProvider) SetTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("DNSPod SetTXT is not wired: fqdn=%s secret_id_len=%d value_len=%d",
		fqdn, len(p.Config.SecretID), len(value))
}
func (p *DNSPodProvider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("DNSPod RemoveTXT is not wired: fqdn=%s secret_id_len=%d value_len=%d",
		fqdn, len(p.Config.SecretID), len(value))
}

// Route53Provider is the stub implementation.
type Route53Provider struct {
	ID     string
	Label  string
	Config Route53Config
}

func (p *Route53Provider) ProviderID() string  { return p.ID }
func (p *Route53Provider) DisplayName() string { return p.Label }
func (p *Route53Provider) TestConnection(ctx context.Context) error {
	_ = ctx
	if p.Config.AccessKey == "" || p.Config.SecretKey == "" {
		return fmt.Errorf("Route53 TestConnection: empty AccessKey/SecretKey")
	}
	return fmt.Errorf("Route53 TestConnection is not wired: ak_len=%d region=%s zone_len=%d",
		len(p.Config.AccessKey), p.Config.Region, len(p.Config.HostedZoneID))
}
func (p *Route53Provider) SetTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("Route53 SetTXT is not wired: fqdn=%s ak_len=%d value_len=%d",
		fqdn, len(p.Config.AccessKey), len(value))
}
func (p *Route53Provider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	return fmt.Errorf("Route53 RemoveTXT is not wired: fqdn=%s ak_len=%d value_len=%d",
		fqdn, len(p.Config.AccessKey), len(value))
}

// ---- Manual-mode provider ---------------------------------------------------
//
// When a domain has no real DNS token (the common case for hobby users
// with Cloudflare free plans that don't want to issue API tokens), the
// pipeline falls back to this provider.  SetTXT / RemoveTXT forward the
// pending challenge to the UI through a caller-supplied persistence
// callback; the operator creates the TXT record by hand and clicks
// "Confirm" in the UI, which unblocks ManualModeWaitOrProbePropagation.

// ManualDNSProvider is a sentinel provider that never performs real DNS
// operations.  Instead it writes pending challenges to SQLite via the
// injected callback so the UI can surface them to the operator.
type ManualDNSProvider struct {
	// Persist is called by SetTXT/RemoveTXT to save/clear the challenge.
	// May be nil — in that case the methods return descriptive errors
	// telling the caller to configure a persistence callback.
	Persist func(fqdn, value, domain, status string) error
}

func (*ManualDNSProvider) ProviderID() string  { return "manual" }
func (*ManualDNSProvider) DisplayName() string { return "Manual (operator creates TXT by hand)" }
func (*ManualDNSProvider) TestConnection(ctx context.Context) error {
	_ = ctx
	return nil // nothing to test; user will do the work manually
}
func (m *ManualDNSProvider) SetTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	if m.Persist == nil {
		return fmt.Errorf("ManualDNSProvider: SetTXT: Persist callback not configured for fqdn=%s", fqdn)
	}
	// Derive the bare domain (strip _acme-challenge. prefix) so the UI can
	// group challenges by managed domain.
	domain := strings.TrimPrefix(fqdn, "_acme-challenge.")
	domain = strings.TrimSuffix(domain, ".")
	if err := m.Persist(fqdn, value, domain, "pending"); err != nil {
		return fmt.Errorf("ManualDNSProvider: persist pending challenge: %w", err)
	}
	return nil
}
func (m *ManualDNSProvider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	_ = ctx
	if m.Persist == nil {
		return fmt.Errorf("ManualDNSProvider: RemoveTXT: Persist callback not configured for fqdn=%s", fqdn)
	}
	domain := strings.TrimPrefix(fqdn, "_acme-challenge.")
	domain = strings.TrimSuffix(domain, ".")
	if err := m.Persist(fqdn, value, domain, "cleared"); err != nil {
		return fmt.Errorf("ManualDNSProvider: persist cleared challenge: %w", err)
	}
	return nil
}

// ---- Provider factory -------------------------------------------------------

// NewDNSProviderFromConfig constructs a concrete DNSProvider from a
// provider kind string and a flat string map.  The map keys exactly match
// the exported field names of each *Config struct (case-insensitive for
// operator convenience at the HTTP API layer).
func NewDNSProviderFromConfig(kind string, id, displayName string, cfg map[string]string) (DNSProvider, error) {
	lk := strings.ToLower(strings.TrimSpace(kind))
	switch lk {
	case "cloudflare":
		c := CloudflareConfig{
			APIToken: mapGetCI(cfg, "APIToken"),
			ZoneID:   mapGetCI(cfg, "ZoneID"),
		}
		return &CloudflareProvider{ID: id, Label: displayName, Config: c}, nil
	case "dnspod":
		c := DNSPodConfig{
			SecretID:  mapGetCI(cfg, "SecretID"),
			SecretKey: mapGetCI(cfg, "SecretKey"),
			ZoneID:    mapGetCI(cfg, "ZoneID"),
		}
		return &DNSPodProvider{ID: id, Label: displayName, Config: c}, nil
	case "route53":
		c := Route53Config{
			AccessKey:    mapGetCI(cfg, "AccessKey"),
			SecretKey:    mapGetCI(cfg, "SecretKey"),
			Region:       mapGetCI(cfg, "Region"),
			HostedZoneID: mapGetCI(cfg, "HostedZoneID"),
		}
		return &Route53Provider{ID: id, Label: displayName, Config: c}, nil
	case "manual", "":
		return &ManualDNSProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown DNS provider kind %q", kind)
	}
}

// mapGetCI performs a case-insensitive key lookup in cfg.
func mapGetCI(cfg map[string]string, want string) string {
	if v, ok := cfg[want]; ok {
		return v
	}
	lwant := strings.ToLower(want)
	for k, v := range cfg {
		if strings.ToLower(k) == lwant {
			return v
		}
	}
	return ""
}
