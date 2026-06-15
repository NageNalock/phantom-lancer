package certmanager

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
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
	AccessKey    string
	SecretKey    string
	Region       string // usually us-east-1 for global Route 53
	HostedZoneID string // optional; empty = auto-lookup via FQDN
}

// ---- Concrete provider implementations -------------------------------------

// CloudflareProvider talks to Cloudflare's public DNS API.
type CloudflareProvider struct {
	ID     string
	Label  string
	Config CloudflareConfig
}

// ProviderID implements DNSProvider.
func (p *CloudflareProvider) ProviderID() string { return p.ID }

// DisplayName implements DNSProvider.
func (p *CloudflareProvider) DisplayName() string { return p.Label }

// TestConnection implements DNSProvider.
func (p *CloudflareProvider) TestConnection(ctx context.Context) error {
	if p.Config.APIToken == "" {
		return fmt.Errorf("Cloudflare TestConnection: empty APIToken")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Config.APIToken)
	req.Header.Set("Accept", "application/json")
	return expect2xx(req, "Cloudflare token verify")
}

// SetTXT implements DNSProvider.
func (p *CloudflareProvider) SetTXT(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.cloudflareZoneID(ctx, fqdn)
	if err != nil {
		return err
	}
	body := map[string]any{"type": "TXT", "name": strings.TrimSuffix(fqdn, "."), "content": value, "ttl": 120}
	return cloudflareRequest(ctx, p.Config.APIToken, http.MethodPost, "https://api.cloudflare.com/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records", body)
}

// RemoveTXT implements DNSProvider.
func (p *CloudflareProvider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	zoneID, err := p.cloudflareZoneID(ctx, fqdn)
	if err != nil {
		return err
	}
	records, err := cloudflareListTXT(ctx, p.Config.APIToken, zoneID, fqdn, value)
	if err != nil {
		return err
	}
	for _, id := range records {
		if err := cloudflareRequest(ctx, p.Config.APIToken, http.MethodDelete, "https://api.cloudflare.com/client/v4/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(id), nil); err != nil {
			return err
		}
	}
	return nil
}

// DNSPodProvider talks to Tencent Cloud DNSPod's public API.
type DNSPodProvider struct {
	ID     string
	Label  string
	Config DNSPodConfig
}

func (p *DNSPodProvider) ProviderID() string  { return p.ID }
func (p *DNSPodProvider) DisplayName() string { return p.Label }
func (p *DNSPodProvider) TestConnection(ctx context.Context) error {
	if p.Config.SecretID == "" || p.Config.SecretKey == "" {
		return fmt.Errorf("DNSPod TestConnection: empty SecretID/SecretKey")
	}
	return dnspodCall(ctx, p.Config, "DescribeDomainList", map[string]any{"Limit": 1}, nil)
}
func (p *DNSPodProvider) SetTXT(ctx context.Context, fqdn, value string) error {
	domain, subdomain := splitDNSPodName(fqdn)
	params := map[string]any{"Domain": domain, "SubDomain": subdomain, "RecordType": "TXT", "RecordLine": "默认", "Value": value, "TTL": 120}
	if p.Config.ZoneID != "" {
		params["DomainId"] = p.Config.ZoneID
		delete(params, "Domain")
	}
	return dnspodCall(ctx, p.Config, "CreateRecord", params, nil)
}
func (p *DNSPodProvider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	domain, subdomain := splitDNSPodName(fqdn)
	var out struct {
		Response struct {
			RecordList []struct {
				RecordID int64  `json:"RecordId"`
				Value    string `json:"Value"`
			} `json:"RecordList"`
		} `json:"Response"`
	}
	params := map[string]any{"Domain": domain, "Subdomain": subdomain, "RecordType": "TXT"}
	if p.Config.ZoneID != "" {
		params["DomainId"] = p.Config.ZoneID
		delete(params, "Domain")
	}
	if err := dnspodCall(ctx, p.Config, "DescribeRecordList", params, &out); err != nil {
		return err
	}
	for _, rec := range out.Response.RecordList {
		if rec.Value != value {
			continue
		}
		del := map[string]any{"Domain": domain, "RecordId": rec.RecordID}
		if p.Config.ZoneID != "" {
			del["DomainId"] = p.Config.ZoneID
			delete(del, "Domain")
		}
		if err := dnspodCall(ctx, p.Config, "DeleteRecord", del, nil); err != nil {
			return err
		}
	}
	return nil
}

// Route53Provider talks to AWS Route 53's public API.
type Route53Provider struct {
	ID     string
	Label  string
	Config Route53Config
}

func (p *Route53Provider) ProviderID() string  { return p.ID }
func (p *Route53Provider) DisplayName() string { return p.Label }
func (p *Route53Provider) TestConnection(ctx context.Context) error {
	if p.Config.AccessKey == "" || p.Config.SecretKey == "" {
		return fmt.Errorf("Route53 TestConnection: empty AccessKey/SecretKey")
	}
	return route53Request(ctx, p.Config, http.MethodGet, "https://route53.amazonaws.com/2013-04-01/hostedzone?maxitems=1", nil)
}
func (p *Route53Provider) SetTXT(ctx context.Context, fqdn, value string) error {
	zoneID := strings.TrimPrefix(p.Config.HostedZoneID, "/hostedzone/")
	if zoneID == "" {
		return fmt.Errorf("Route53 SetTXT: HostedZoneID is required")
	}
	body := route53ChangeXML("UPSERT", fqdn, value)
	return route53Request(ctx, p.Config, http.MethodPost, "https://route53.amazonaws.com/2013-04-01/hostedzone/"+url.PathEscape(zoneID)+"/rrset", []byte(body))
}
func (p *Route53Provider) RemoveTXT(ctx context.Context, fqdn, value string) error {
	zoneID := strings.TrimPrefix(p.Config.HostedZoneID, "/hostedzone/")
	if zoneID == "" {
		return fmt.Errorf("Route53 RemoveTXT: HostedZoneID is required")
	}
	body := route53ChangeXML("DELETE", fqdn, value)
	return route53Request(ctx, p.Config, http.MethodPost, "https://route53.amazonaws.com/2013-04-01/hostedzone/"+url.PathEscape(zoneID)+"/rrset", []byte(body))
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

func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func expect2xx(req *http.Request, label string) error {
	resp, err := httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: HTTP %d: %s", label, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func cloudflareRequest(ctx context.Context, token, method, endpoint string, body any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return expect2xx(req, "Cloudflare DNS API")
}

func (p *CloudflareProvider) cloudflareZoneID(ctx context.Context, fqdn string) (string, error) {
	if p.Config.ZoneID != "" {
		return p.Config.ZoneID, nil
	}
	name := strings.TrimSuffix(strings.TrimPrefix(fqdn, "_acme-challenge."), ".")
	parts := strings.Split(name, ".")
	for i := 0; i < len(parts)-1; i++ {
		zone := strings.Join(parts[i:], ".")
		endpoint := "https://api.cloudflare.com/client/v4/zones?name=" + url.QueryEscape(zone) + "&per_page=1"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+p.Config.APIToken)
		resp, err := httpClient().Do(req)
		if err != nil {
			return "", err
		}
		var out struct {
			Result []struct {
				ID string `json:"id"`
			} `json:"result"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
		resp.Body.Close()
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode <= 299 && len(out.Result) > 0 && out.Result[0].ID != "" {
			return out.Result[0].ID, nil
		}
	}
	return "", fmt.Errorf("Cloudflare: could not resolve zone for fqdn=%s", fqdn)
}

func cloudflareListTXT(ctx context.Context, token, zoneID, fqdn, value string) ([]string, error) {
	endpoint := "https://api.cloudflare.com/client/v4/zones/" + url.PathEscape(zoneID) + "/dns_records?type=TXT&name=" + url.QueryEscape(strings.TrimSuffix(fqdn, "."))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("Cloudflare list TXT: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Result []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, rec := range out.Result {
		if rec.Content == value {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}

func dnspodCall(ctx context.Context, cfg DNSPodConfig, action string, params map[string]any, out any) error {
	if cfg.SecretID == "" || cfg.SecretKey == "" {
		return fmt.Errorf("DNSPod %s: empty SecretID/SecretKey", action)
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://dnspod.tencentcloudapi.com", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	ts := time.Now().Unix()
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")
	hashedPayload := sha256Hex(payload)
	canonicalHeaders := "content-type:application/json\nhost:dnspod.tencentcloudapi.com\n"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\ncontent-type;host\n" + hashedPayload
	credentialScope := date + "/dnspod/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + fmt.Sprint(ts) + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	signature := hex.EncodeToString(hmacSHA256(hmacSHA256(hmacSHA256([]byte("TC3"+cfg.SecretKey), date), "dnspod"), "tc3_request", stringToSign))
	auth := "TC3-HMAC-SHA256 Credential=" + cfg.SecretID + "/" + credentialScope + ", SignedHeaders=content-type;host, Signature=" + signature
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", "dnspod.tencentcloudapi.com")
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Timestamp", fmt.Sprint(ts))
	req.Header.Set("X-TC-Version", "2021-03-23")
	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("DNSPod %s: HTTP %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var probe struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	_ = json.Unmarshal(body, &probe)
	if probe.Response.Error != nil {
		return fmt.Errorf("DNSPod %s: %s: %s", action, probe.Response.Error.Code, probe.Response.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return err
		}
	}
	return nil
}

func hmacSHA256(key []byte, vals ...string) []byte {
	h := hmac.New(sha256.New, key)
	for _, v := range vals {
		h.Write([]byte(v))
	}
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func splitDNSPodName(fqdn string) (domain, subdomain string) {
	name := strings.TrimSuffix(fqdn, ".")
	parts := strings.Split(name, ".")
	if len(parts) <= 2 {
		return name, "@"
	}
	return strings.Join(parts[len(parts)-2:], "."), strings.Join(parts[:len(parts)-2], ".")
}

func route53Request(ctx context.Context, cfg Route53Config, method, endpoint string, body []byte) error {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
	}
	creds := aws.Credentials{AccessKeyID: cfg.AccessKey, SecretAccessKey: cfg.SecretKey}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, sha256Hex(body), "route53", region, time.Now()); err != nil {
		return err
	}
	return expect2xx(req, "Route53 DNS API")
}

func route53ChangeXML(action, fqdn, value string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>
      <Change>
        <Action>` + action + `</Action>
        <ResourceRecordSet>
          <Name>` + xmlEscape(strings.TrimSuffix(fqdn, ".")+".") + `</Name>
          <Type>TXT</Type>
          <TTL>120</TTL>
          <ResourceRecords><ResourceRecord><Value>"` + xmlEscape(value) + `"</Value></ResourceRecord></ResourceRecords>
        </ResourceRecordSet>
      </Change>
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>`
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
