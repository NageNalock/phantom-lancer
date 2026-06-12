package mail

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/storage"
)

// isLoopbackSource reports whether addr (form "host:port", "@" (unix socket),
// or bare host) refers to the local loopback interface.  Empty or unix-socket
// style addresses are treated as loopback (same-machine traffic).
func isLoopbackSource(addr string) bool {
	if addr == "" || strings.HasPrefix(addr, "@") {
		return true
	}
	// Split brackets first (IPv6 with port).
	var host string
	switch {
	case strings.HasPrefix(addr, "["):
		if end := strings.Index(addr, "]"); end > 1 {
			host = addr[1:end]
		} else {
			host = addr
		}
	case strings.Contains(addr, ":"):
		// Could be IPv4:port OR bare IPv6 (multiple colons).
		if strings.Count(addr, ":") == 1 {
			// IPv4:port form.
			idx := strings.Index(addr, ":")
			host = addr[:idx]
		} else {
			// Bare IPv6 (no brackets, no port). Use whole string.
			host = addr
		}
	default:
		host = addr
	}
	// Fast-path literals before resolving net.ParseIP.
	if host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// -----------------------------------------------------------------------------
// Webhook service-layer value types.
// -----------------------------------------------------------------------------

// WebhookRegisterRequest is the UI/service payload for registering a new
// inbound or outbound webhook.  The signing secret is generated server-side
// and returned exactly once as the second return value of WebhookRegister.
type WebhookRegisterRequest struct {
	Name         string   `json:"name"`
	Direction    string   `json:"direction"`     // "in" (ingress) or "out" (egress)
	URL          string   `json:"url,omitempty"` // required for out; empty for in
	SourceCIDR   string   `json:"source_cidr"`   // advisory; ingress validation is currently by match order
	MaxBodyBytes int64    `json:"max_body_bytes"`
	SigningAlg   string   `json:"signing_alg"` // "hmac-sha256"
	EventMask    []string `json:"event_mask"`  // event type names to subscribe to
	Enabled      bool     `json:"enabled"`
}

// webhookID returns a short id for a webhook registration, falling back to a
// hex token if ids.New fails.
func webhookID(prefix string) string {
	if id, err := ids.New(prefix); err == nil && id != "" {
		return id
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return prefix + "_" + hex.EncodeToString(buf)
}

// webhookEventID returns a short id for a webhook event row.
func webhookEventID() string {
	if id, err := ids.New(IDPrefixWebhookEvent); err == nil && id != "" {
		return id
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return IDPrefixWebhookEvent + "_" + hex.EncodeToString(buf)
}

// generateWebhookSecret produces a 32-byte cryptographically random secret
// hex-encoded so the caller can hand it to the upstream provider exactly
// once.  The same plaintext is wrapped by the mail keeper before persisting.
func generateWebhookSecret() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// --- Public service methods -------------------------------------------------

// WebhookRegister creates a new webhook registration, generates a 32-byte
// shared secret, wraps it under the mail keeper, persists the wrapped
// version, and returns (registration, plaintextSecret, err).  The
// plaintextSecret is shown exactly once to the caller; it is never stored
// unwrapped in SQLite, audit, or logs.
func (s *Service) WebhookRegister(ctx context.Context, req *WebhookRegisterRequest) (*storage.MailWebhookRegistration, string, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, "", err
	}
	if req == nil {
		return nil, "", errors.New("webhook register: request is nil")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, "", errors.New("webhook register: name is required")
	}
	if req.Direction != "in" && req.Direction != "out" {
		return nil, "", fmt.Errorf("webhook register: direction must be in or out, got %q", req.Direction)
	}
	if req.Direction == "out" && strings.TrimSpace(req.URL) == "" {
		return nil, "", errors.New("webhook register: outbound webhook requires url")
	}
	if req.SigningAlg == "" {
		req.SigningAlg = "hmac-sha256"
	}
	if req.MaxBodyBytes <= 0 {
		req.MaxBodyBytes = 1 << 20 // 1 MiB default
	}
	maskJSON, err := json.Marshal(req.EventMask)
	if err != nil {
		return nil, "", fmt.Errorf("webhook register: marshal event_mask: %w", err)
	}
	plain := generateWebhookSecret()
	wrapped, err := s.store.WrapMailSecret(plain)
	if err != nil {
		return nil, "", fmt.Errorf("webhook register: wrap secret: %w", err)
	}
	row := &storage.MailWebhookRegistration{
		ID:            webhookID(IDPrefixWebhook),
		Name:          strings.TrimSpace(req.Name),
		Direction:     req.Direction,
		URL:           strings.TrimSpace(req.URL),
		SigningAlg:    req.SigningAlg,
		SourceCIDR:    strings.TrimSpace(req.SourceCIDR),
		MaxBodyBytes:  req.MaxBodyBytes,
		EventMask:     string(maskJSON),
		WrappedSecret: wrapped,
		Enabled:       req.Enabled,
	}
	saved, err := s.store.MailWebhookUpsert(ctx, row)
	if err != nil {
		return nil, "", fmt.Errorf("webhook register: persist: %w", err)
	}
	s.addAudit(ctx, "mail.webhook.created",
		fmt.Sprintf("created webhook %q direction=%s", saved.Name, saved.Direction),
		map[string]any{
			"id":         saved.ID,
			"name":       saved.Name,
			"direction":  saved.Direction,
			"signing_alg": saved.SigningAlg,
			"enabled":    saved.Enabled,
		}, "high")
	s.publish(ctx, "mail.webhook.created", map[string]any{
		"id":        saved.ID,
		"name":      saved.Name,
		"direction": saved.Direction,
		"enabled":   saved.Enabled,
	})
	s.touchLastChange()
	return saved, plain, nil
}

// WebhookList returns all registered webhooks (without secrets).
func (s *Service) WebhookList(ctx context.Context) ([]*storage.MailWebhookRegistration, error) {
	return s.store.MailWebhookList(ctx)
}

// WebhookDelete hard-deletes a webhook registration by id.
func (s *Service) WebhookDelete(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("webhook delete: id is required")
	}
	// Try to capture the name for audit before delete (best-effort).
	list, _ := s.store.MailWebhookList(ctx)
	var name string
	for _, r := range list {
		if r.ID == id {
			name = r.Name
			break
		}
	}
	if err := s.store.MailWebhookDelete(ctx, id); err != nil {
		return fmt.Errorf("webhook delete: %w", err)
	}
	s.addAudit(ctx, "mail.webhook.deleted",
		fmt.Sprintf("deleted webhook id=%s name=%q", id, name),
		map[string]any{"id": id, "name": name}, "high")
	s.publish(ctx, "mail.webhook.deleted", map[string]any{"id": id, "name": name})
	s.touchLastChange()
	return nil
}

// WebhookRotateSecret generates a new 32-byte shared secret for an existing
// registration, wraps it, and returns the plaintext exactly once.
func (s *Service) WebhookRotateSecret(ctx context.Context, id string) (string, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("webhook rotate: id is required")
	}
	plain := generateWebhookSecret()
	wrapped, err := s.store.WrapMailSecret(plain)
	if err != nil {
		return "", fmt.Errorf("webhook rotate: wrap secret: %w", err)
	}
	if err := s.store.MailWebhookRotateSecret(ctx, id, wrapped); err != nil {
		return "", fmt.Errorf("webhook rotate: persist: %w", err)
	}
	s.addAudit(ctx, "mail.webhook.secret_rotated",
		fmt.Sprintf("rotated webhook secret id=%s", id),
		map[string]any{"id": id}, "high")
	s.publish(ctx, "mail.webhook.secret_rotated", map[string]any{"id": id})
	s.touchLastChange()
	return plain, nil
}

// WebhookIngest validates and dispatches an inbound webhook payload.
// It returns (statusString, eventType, error).  statusString is one of
// "accepted", "processed", or "rejected" and is suitable for the HTTP
// response body; error is non-nil only when the caller should surface a
// non-2xx response (e.g. HMAC failure, missing registration).
//
// Validation follows section 6.1:
//  1. timestamp within 900s of wall time
//  2. signature header matches "sha256=<hex>"
//  3. find matching registration by source (or default inbound)
//  4. unwrap shared secret
//  5. constant-time HMAC comparison over (ts.body)
//  6. append webhook event row
//  7. dispatch to DeliveryIngestEvent / QueueIngestEvent on success
func (s *Service) WebhookIngest(ctx context.Context, sourceAddr string, timestampStr string, signatureHeader string, body []byte) (string, string, error) {
	// Always compute these for later audit event writing.
	payloadHash := sha256.Sum256(body)
	payloadSize := int64(len(body))
	nowTs := time.Now().Unix()
	eventType := ""

	// --- Build a best-effort event row regardless of outcome. --------------
	makeEvent := func(reg *storage.MailWebhookRegistration, regID, status, reason string, valid bool) *storage.MailWebhookEvent {
		direction := "in"
		if reg != nil {
			direction = reg.Direction
		}
		skewMs := int64(0)
		if tsUnix, err := strconv.ParseInt(strings.TrimSpace(timestampStr), 10, 64); err == nil {
			delta := nowTs - tsUnix
			if delta < 0 {
				delta = -delta
			}
			skewMs = delta * 1000
		}
		return &storage.MailWebhookEvent{
			ID:              webhookEventID(),
			RegistrationID:  regID,
			Direction:       direction,
			EventType:       eventType,
			PayloadHash:     hex.EncodeToString(payloadHash[:]),
			PayloadSize:     payloadSize,
			SourceAddr:      sourceAddr,
			HMACValid:       valid,
			TimestampSkewMs: skewMs,
			Status:          status,
			ErrorReason:     reason,
		}
	}

	// Step 0a: source address must be loopback (unix socket or 127.0.0.1 / ::1).
	// Webhook ingress is only reachable via local sidecar; rejecting external
	// source IPs closes a trivial DoS / brute-force surface.
	if !isLoopbackSource(sourceAddr) {
		ev := makeEvent(nil, "", "rejected", fmt.Sprintf("source %q is not loopback", sourceAddr), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source": sourceAddr,
			"reason": "source_not_loopback",
			"status": "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: source address %q is not loopback", sourceAddr)
	}

	// Step 0b: enforce per-registration body size cap (default 1 MiB).
	// We don't yet know the registration, so fall back to the global 1 MiB default;
	// a per-registration override is applied later after lookup (best-effort).
	const defaultMaxBody = 1 << 20 // 1 MiB
	if payloadSize > defaultMaxBody {
		ev := makeEvent(nil, "", "rejected", fmt.Sprintf("payload %d bytes exceeds max %d", payloadSize, defaultMaxBody), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":      sourceAddr,
			"reason":      "body_too_large",
			"size":        payloadSize,
			"max_allowed": defaultMaxBody,
			"status":      "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: payload %d bytes exceeds maximum allowed %d", payloadSize, defaultMaxBody)
	}

	// Parse timestamp (unix seconds).
	tsUnix, tsErr := strconv.ParseInt(strings.TrimSpace(timestampStr), 10, 64)
	skewMs := int64(0)
	if tsErr == nil {
		delta := nowTs - tsUnix
		if delta < 0 {
			delta = -delta
		}
		skewMs = delta * 1000
	}

	// Parse signature: "sha256=<hex>".  Multiple algorithms separated by
	// commas are allowed; we pick the sha256 one.  Case-sensitive prefix.
	signaturePresent := signatureHeader != ""
	providedHex := ""
	for _, part := range strings.Split(signatureHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "sha256=") {
			providedHex = strings.TrimPrefix(part, "sha256=")
			break
		}
	}

	// After lookup (step 3) we will validate this hex.

	// Step 1: timestamp skew check (allow 900s window, config-wide default).
	const maxSkew = int64(900)
	if tsErr != nil {
		ev := makeEvent(nil, "", "rejected", fmt.Sprintf("bad timestamp: %v", tsErr), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":       sourceAddr,
			"reason":       "bad_timestamp",
			"status":       "rejected",
			"hmac_valid":   false,
			"event_type":   eventType,
			"payload_hash": hex.EncodeToString(payloadHash[:]),
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: invalid timestamp: %v", tsErr)
	}
	delta := nowTs - tsUnix
	if delta < 0 {
		delta = -delta
	}
	if delta > maxSkew {
		reason := "timestamp_skew"
		if nowTs-tsUnix > maxSkew {
			reason = "timestamp_expired"
		}
		ev := makeEvent(nil, "", "rejected", fmt.Sprintf("timestamp skew %ds exceeds %ds", delta, maxSkew), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":       sourceAddr,
			"reason":       reason,
			"skew_seconds": delta,
			"status":       "rejected",
			"hmac_valid":   false,
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: timestamp skew %d exceeds window: %s", delta, reason)
	}

	// Step 3: find inbound registration (by source_addr CIDR if ever
	// implemented, otherwise first enabled inbound registration).
	reg, findErr := s.store.MailWebhookFindForIngress(ctx, sourceAddr)
	if findErr != nil {
		ev := makeEvent(nil, "", "rejected", fmt.Sprintf("no inbound webhook registration: %v", findErr), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source": sourceAddr,
			"reason": "no_registration",
			"status": "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: no registration: %w", findErr)
	}
	if !reg.Enabled {
		ev := makeEvent(reg, reg.ID, "rejected", "registration disabled", false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":   sourceAddr,
			"reason":   "disabled",
			"webhook_id": reg.ID,
			"status":   "rejected",
		})
		return "rejected", eventType, errors.New("webhook ingest: registration disabled")
	}

	// Step 4: unwrap shared secret.
	secret, unwrapErr := s.store.MailWebhookReadSecret(ctx, reg.ID)
	if unwrapErr != nil {
		ev := makeEvent(reg, reg.ID, "rejected", fmt.Sprintf("read secret: %v", unwrapErr), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":   sourceAddr,
			"reason":   "secret_unwrap_failed",
			"webhook_id": reg.ID,
			"status":   "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: unwrap secret: %w", unwrapErr)
	}
	secretPlain, uerr := s.store.UnwrapMailSecret(secret)
	if uerr != nil {
		ev := makeEvent(reg, reg.ID, "rejected", fmt.Sprintf("unwrap secret: %v", uerr), false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":   sourceAddr,
			"reason":   "secret_unwrap_failed",
			"webhook_id": reg.ID,
			"status":   "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: %w", uerr)
	}
	if secretPlain == "" {
		ev := makeEvent(reg, reg.ID, "rejected", "empty shared secret", false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		return "rejected", eventType, errors.New("webhook ingest: empty shared secret")
	}

	// Step 5: constant-time HMAC comparison.
	// First: hex syntax + length check (malformed vs mismatch).
	signingInput := fmt.Sprintf("%s.%s", timestampStr, string(body))
	mac := hmac.New(sha256.New, []byte(secretPlain))
	mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	expectedHex := hex.EncodeToString(expected)

	// Malformed signature detection:
	//  - no "sha256=" prefix (case sensitive, e.g. SHA256= rejected)
	//  - provided hex is empty (no sha256 component found)
	//  - provided string contains non-hex characters
	//  - length wrong (not 64 hex chars / 32 bytes)
	malformedReason := ""
	if !signaturePresent {
		malformedReason = "signature_missing"
	} else if providedHex == "" {
		malformedReason = "malformed_signature"
	}
	providedRaw, perr := hex.DecodeString(providedHex)
	if malformedReason == "" && perr != nil {
		malformedReason = "malformed_signature"
	}
	if malformedReason == "" && len(providedRaw) != len(expected) {
		// Wrong length: treat as malformed so caller can distinguish from
		// a bit-flipped valid-length attack.
		malformedReason = "malformed_signature"
	}

	if malformedReason != "" {
		ev := makeEvent(reg, reg.ID, "rejected", malformedReason, false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":     sourceAddr,
			"reason":     malformedReason,
			"webhook_id": reg.ID,
			"status":     "rejected",
		})
		return "rejected", eventType, fmt.Errorf("webhook ingest: %s", malformedReason)
	}

	// Constant-time comparison over decoded raw bytes.
	hmacValid := hmac.Equal(expected, providedRaw)
	// Defence-in-depth: also compare hex forms (timing neutral given lengths match).
	if !hmacValid && len(providedHex) == len(expectedHex) {
		hmacValid = hmac.Equal([]byte(expectedHex), []byte(providedHex))
	}

	if !hmacValid {
		ev := makeEvent(reg, reg.ID, "rejected", "hmac signature mismatch", false)
		_ = s.store.MailWebhookEventAppend(ctx, ev)
		s.publish(ctx, EventTypeWebhookInRejected, map[string]any{
			"source":     sourceAddr,
			"reason":     "signature_mismatch",
			"webhook_id": reg.ID,
			"status":     "rejected",
		})
		return "rejected", eventType, errors.New("webhook ingest: signature_mismatch")
	}

	// --- HMAC valid: parse payload for dispatch and event writing. ---------
	status := "processed"
	var parsed map[string]any
	if jerr := json.Unmarshal(body, &parsed); jerr == nil && parsed != nil {
		if et, ok := parsed["event_type"].(string); ok {
			eventType = et
		}
	}

	// Write the webhook event row (valid).
	ev := makeEvent(reg, reg.ID, status, "", true)
	_ = s.store.MailWebhookEventAppend(ctx, ev)

	s.publish(ctx, EventTypeWebhookInReceived, map[string]any{
		"source":       sourceAddr,
		"webhook_id":   reg.ID,
		"event_type":   eventType,
		"status":       status,
		"hmac_valid":   true,
		"skew_seconds": (skewMs / 1000),
		"payload_hash": hex.EncodeToString(payloadHash[:]),
	})

	// Step 8: dispatch by event type prefix.
	lowerET := strings.ToLower(eventType)
	switch {
	case strings.HasPrefix(lowerET, "delivery"), strings.HasPrefix(lowerET, "bounce"):
		if parsed != nil {
			if derr := s.DeliveryIngestEvent(ctx, eventType, parsed); derr != nil {
				s.log.WarnContext(ctx, "webhook: dispatch to delivery failed", "event_type", eventType, "error", derr)
			}
		}
	case strings.HasPrefix(lowerET, "queue"):
		if parsed != nil {
			if derr := s.QueueIngestEvent(ctx, eventType, parsed); derr != nil {
				s.log.WarnContext(ctx, "webhook: dispatch to queue failed", "event_type", eventType, "error", derr)
			}
		}
	}

	return status, eventType, nil
}

// WebhookEventList returns the most recent webhook event rows.
func (s *Service) WebhookEventList(ctx context.Context, limit int) ([]*storage.MailWebhookEvent, error) {
	return s.store.MailWebhookEventList(ctx, limit)
}
