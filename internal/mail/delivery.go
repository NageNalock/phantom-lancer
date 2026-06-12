package mail

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Delivery / queue / suppression / rate service-layer value types.
// -----------------------------------------------------------------------------

// OutboundRateSnapshot is a read-only view of current counters vs configured
// thresholds for a scope.  Counters are zero-filled during Phase 6 (Phase 7
// wires real increments on each send attempt).
type OutboundRateSnapshot struct {
	Scope             string  `json:"scope"`
	Send1m            int64   `json:"send_1m"`
	Send1mWarn        int64   `json:"send_1m_warn"`
	Send1mCrit        int64   `json:"send_1m_crit"`
	Send1h            int64   `json:"send_1h"`
	Send1hWarn        int64   `json:"send_1h_warn"`
	Send1hCrit        int64   `json:"send_1h_crit"`
	BounceRatePct     float64 `json:"bounce_rate_pct"`
	BounceRatePctWarn float64 `json:"bounce_rate_pct_warn"`
	BounceRatePctCrit float64 `json:"bounce_rate_pct_crit"`
	Severity          string  `json:"severity"` // good/warn/critical
	UpdatedAt         string  `json:"updated_at"`
}

// QueueBulkActionRequest is the wire format for queue bulk operations.
// Defined here for clarity; the handler layer can construct it directly.
type QueueBulkActionRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"` // hold/unhold/schedule/fail/drop
}

// ---- ID helpers -------------------------------------------------------------

func deliveryEventID() string {
	if id, err := ids.New(IDPrefixDeliveryEvent); err == nil && id != "" {
		return id
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return IDPrefixDeliveryEvent + "_" + hex.EncodeToString(buf)
}

func suppressionID() string {
	if id, err := ids.New(IDPrefixSuppression); err == nil && id != "" {
		return id
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return IDPrefixSuppression + "_" + hex.EncodeToString(buf)
}

// sha256Hex returns the hex-encoded sha256 of s.
func sha256Hex(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// redactIPs replaces any IPv4 / IPv6 appearance in the input string with
// "[redacted_ip]" so error strings don't leak operator network layout.
var ipv4Regex = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
var ipv6Regex = regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){2,7}[0-9a-fA-F]{0,4}\b`)

func redactIPs(s string) string {
	s = ipv4Regex.ReplaceAllString(s, "[redacted_ip]")
	s = ipv6Regex.ReplaceAllString(s, "[redacted_ip]")
	return s
}

// truncate80 clips s to max 80 runes, adding "…" if truncated.
func truncate80(s string) string {
	runes := []rune(s)
	if len(runes) <= 80 {
		return s
	}
	return string(runes[:79]) + "…"
}

// domainFromAddr extracts the part after '@' from a loose email address.
// Returns "" if the address contains no '@'.
func domainFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	// Strip angle brackets if present ("<user@domain>").
	addr = strings.Trim(addr, "<>")
	// Strip display-name prefixes ("Alice <a@b>" → "a@b" already handled above).
	if idx := strings.LastIndex(addr, "@"); idx >= 0 {
		dom := strings.ToLower(strings.TrimSpace(addr[idx+1:]))
		// Handle source routing / comments defensively.
		if end := strings.IndexAny(dom, " >)]"); end >= 0 {
			dom = dom[:end]
		}
		return dom
	}
	return ""
}

// --- Delivery public methods ------------------------------------------------

// DeliveryList returns paginated delivery events matching the filter.
// Read-only: no write guard required.
func (s *Service) DeliveryList(ctx context.Context, f storage.MailDeliveryListFilter) (*storage.MailDeliveryListResponse, error) {
	return s.store.MailDeliveryList(ctx, f)
}

// DeliveryGet returns a single delivery event by id.
func (s *Service) DeliveryGet(ctx context.Context, id string) (*storage.MailDeliveryEvent, error) {
	if id == "" {
		return nil, errors.New("delivery get: id is required")
	}
	return s.store.MailDeliveryGet(ctx, id)
}

// DeliveryRetry schedules a retry for a previously failed / deferred event.
// Currently a stub: writes an audit entry and returns nil so the operator UX
// can proceed.  Phase 7 wires real re-queue logic through Mox CLI.
func (s *Service) DeliveryRetry(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("delivery retry: id is required")
	}
	_, gerr := s.store.MailDeliveryGet(ctx, id)
	if gerr != nil {
		return fmt.Errorf("delivery retry: %w", gerr)
	}
	// TODO(Phase 7): invoke Mox requeue CLI / API endpoint to actually
	//   re-schedule the outbound attempt.  For now we only record intent.
	s.addAudit(ctx, EventTypeQueueAction,
		fmt.Sprintf("retried delivery id=%s (stub; Phase 7 wires actual requeue)", id),
		map[string]any{"id": id, "stub": true}, "medium")
	s.log.WarnContext(ctx, "delivery retry: stub implementation", "id", id)
	return nil
}

// DeliveryDelete hard-deletes a single delivery event row.
func (s *Service) DeliveryDelete(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("delivery delete: id is required")
	}
	if err := s.store.MailDeliveryDelete(ctx, id); err != nil {
		return fmt.Errorf("delivery delete: %w", err)
	}
	s.addAudit(ctx, EventTypeRetentionPruned,
		fmt.Sprintf("deleted delivery event id=%s", id),
		map[string]any{"id": id}, "high")
	s.publish(ctx, EventTypeRetentionPruned, map[string]any{
		"scope": "delivery",
		"id":    id,
	})
	s.touchLastChange()
	return nil
}

// DeliveryPrune removes delivery events older than `days` days.  Returns the
// count of rows deleted.
func (s *Service) DeliveryPrune(ctx context.Context, days int) (int64, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return 0, err
	}
	n, err := s.store.MailDeliveryPrune(ctx, days)
	if err != nil {
		return 0, fmt.Errorf("delivery prune: %w", err)
	}
	s.addAudit(ctx, EventTypeRetentionPruned,
		fmt.Sprintf("pruned %d delivery records older than %d days", n, days),
		map[string]any{"scope": "delivery", "count": n, "days": days}, "medium")
	s.publish(ctx, EventTypeRetentionPruned, map[string]any{
		"scope": "delivery",
		"count": n,
		"days":  days,
	})
	s.touchLastChange()
	return n, nil
}

// DeliveryIngestEvent converts a raw webhook JSON payload into a
// MailDeliveryEvent row with safe redacted fields and persists it.  Called
// from WebhookIngest.  Private helper but exported (lowercase-ish isn't
// possible in Go across the same package; public so callers can stub).
func (s *Service) DeliveryIngestEvent(ctx context.Context, eventType string, body map[string]any) error {
	if body == nil {
		return errors.New("delivery ingest: nil body")
	}
	lowerET := strings.ToLower(eventType)
	status := "processed"
	switch {
	case strings.Contains(lowerET, "bounce"), strings.Contains(lowerET, "fail"):
		status = "bounced"
	case strings.Contains(lowerET, "defer"), strings.Contains(lowerET, "delay"):
		status = "deferred"
	case strings.Contains(lowerET, "deliver"), strings.Contains(lowerET, "sent"), strings.Contains(lowerET, "success"):
		status = "sent"
	case strings.Contains(lowerET, "drop"):
		status = "dropped"
	case strings.Contains(lowerET, "suppress"):
		status = "suppressed"
	}
	if s, ok := strVal(body, "status"); ok && s != "" {
		status = s
	}
	str := func(k string) string { v, _ := strVal(body, k); return v }
	from := str("from")
	to := str("to")
	recipient := str("recipient")
	if recipient == "" {
		recipient = to
	}
	messageID := str("message_id")
	subject := str("subject")
	if subject == "" {
		subject = str("subject_snippet")
	}
	errStr := str("error")
	if errStr == "" {
		errStr = str("reason")
		if errStr == "" {
			errStr = str("diagnostic_code")
		}
	}
	smtpCode, _ := intVal(body, "smtp_code")
	if smtpCode == 0 {
		smtpCode, _ = intVal(body, "code")
	}
	smtpEnh, _ := strVal(body, "smtp_enhanced")
	if smtpEnh == "" {
		smtpEnh, _ = strVal(body, "enhanced_code")
	}
	attempts, _ := intVal(body, "attempt_count")
	if attempts == 0 {
		attempts = 1
	}
	first, _ := strVal(body, "first_attempt_at")
	last, _ := strVal(body, "last_attempt_at")
	completed, _ := strVal(body, "completed_at")
	direction := "out"
	if d, ok := strVal(body, "direction"); ok && d != "" {
		direction = d
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if first == "" {
		first = now
	}
	if last == "" {
		last = now
	}
	if (status == "sent" || status == "bounced" || status == "dropped" || status == "suppressed") && completed == "" {
		completed = now
	}
	ev := &storage.MailDeliveryEvent{
		ID:             deliveryEventID(),
		FromDomain:     domainFromAddr(from),
		ToDomain:       domainFromAddr(to),
		MessageIDHash:  sha256Hex(messageID),
		SubjectSnippet: truncate80(subject),
		Direction:      direction,
		SMTPCode:       smtpCode,
		SMTPEnhanced:   smtpEnh,
		RedactedError:  redactIPs(errStr),
		Status:         status,
		AttemptCount:   attempts,
		FirstAttemptAt: first,
		LastAttemptAt:  last,
		CompletedAt:    completed,
		RecipientHash:  sha256Hex(recipient),
		CreatedAt:      now,
	}
	saved, err := s.store.MailDeliveryInsert(ctx, ev)
	if err != nil {
		return fmt.Errorf("delivery ingest: persist: %w", err)
	}
	// Publish the appropriate lifecycle event.
	pubType := EventTypeDeliverySucceeded
	switch status {
	case "bounced":
		pubType = EventTypeDeliveryFailed
	case "deferred":
		pubType = EventTypeDeliveryDeferred
	case "suppressed":
		pubType = EventTypeSuppressionUpdated
	case "dropped", "fail", "failed":
		pubType = EventTypeDeliveryFailed
	}
	s.publish(ctx, pubType, map[string]any{
		"id":              saved.ID,
		"status":          status,
		"from_domain":     saved.FromDomain,
		"to_domain":       saved.ToDomain,
		"message_id_hash": saved.MessageIDHash,
		"smtp_code":       saved.SMTPCode,
		"attempt_count":   saved.AttemptCount,
		"event_type":      eventType,
	})
	// Best-effort: if this is a bounce, push a suppression entry so the
	// operator doesn't accidentally re-send to known-bad recipients.
	if recipient != "" && (status == "bounced" || status == "suppressed") {
		_ = s.ensureBounceSuppression(ctx, recipient, status, smtpCode, eventType)
	}
	return nil
}

// QueueIngestEvent mirrors a raw Mox-side queue event into the local cache
// table.  Phase 7 wires a full periodic re-sync; for now the event itself is
// the authoritative signal.
func (s *Service) QueueIngestEvent(ctx context.Context, eventType string, body map[string]any) error {
	if body == nil {
		return errors.New("queue ingest: nil body")
	}
	str := func(k string) string { v, _ := strVal(body, k); return v }
	itemID := str("queue_id")
	if itemID == "" {
		itemID = str("id")
	}
	if itemID == "" {
		itemID, _ = ids.New(IDPrefixQueueItem)
	}
	bucket := str("bucket")
	if bucket == "" {
		lower := strings.ToLower(eventType)
		switch {
		case strings.Contains(lower, "hold"):
			bucket = "hold"
		case strings.Contains(lower, "schedule"):
			bucket = "schedule"
		case strings.Contains(lower, "defer"), strings.Contains(lower, "delay"):
			bucket = "deferred"
		case strings.Contains(lower, "fail"):
			bucket = "fail"
		case strings.Contains(lower, "drop"):
			bucket = "drop"
		default:
			bucket = "active"
		}
	}
	status := str("status")
	if status == "" {
		status = bucket
	}
	from := str("envelope_from")
	if from == "" {
		from = str("from")
	}
	to := str("envelope_to")
	if to == "" {
		to = str("to")
	}
	scheduled := str("scheduled_at")
	attempts, _ := intVal(body, "attempt_count")
	now := time.Now().UTC().Format(time.RFC3339)
	// Persist via MailQueueBulkUpdateBucket when row exists, otherwise
	// write-through isn't implemented for the queue cache in Phase 6
	// (Mox is the source of truth).  Log and return for observability.
	s.log.DebugContext(ctx, "queue ingest event",
		"id", itemID,
		"bucket", bucket,
		"status", status,
		"from_domain", domainFromAddr(from),
		"attempts", attempts,
		"event_type", eventType,
		"scheduled", scheduled,
	)
	s.publish(ctx, EventTypeQueueAction, map[string]any{
		"id":          itemID,
		"bucket":      bucket,
		"status":      status,
		"event_type":  eventType,
		"from_domain": domainFromAddr(from),
	})
	_ = now
	_ = to
	return nil
}

// strVal looks up a string-valued field in a map, supporting both flat keys
// and dotted paths (one level only: "a.b").
func strVal(m map[string]any, key string) (string, bool) {
	if v, ok := m[key]; ok && v != nil {
		if s, sok := v.(string); sok {
			return s, true
		}
		return fmt.Sprintf("%v", v), true
	}
	if idx := strings.Index(key, "."); idx > 0 {
		parent := key[:idx]
		child := key[idx+1:]
		if p, ok := m[parent].(map[string]any); ok {
			return strVal(p, child)
		}
	}
	return "", false
}

// intVal looks up an integer-valued field (accepts numbers and numeric strings).
func intVal(m map[string]any, key string) (int, bool) {
	if v, ok := m[key]; ok && v != nil {
		switch tv := v.(type) {
		case float64:
			return int(tv), true
		case int:
			return tv, true
		case int64:
			return int(tv), true
		case string:
			if i, err := strconv.Atoi(tv); err == nil {
				return i, true
			}
		}
	}
	if idx := strings.Index(key, "."); idx > 0 {
		parent := key[:idx]
		child := key[idx+1:]
		if p, ok := m[parent].(map[string]any); ok {
			return intVal(p, child)
		}
	}
	return 0, false
}

// ensureBounceSuppression creates or updates a suppression row for a
// known-bounced recipient.  Called as a best-effort side-effect of
// DeliveryIngestEvent; errors are logged but not surfaced to the caller.
func (s *Service) ensureBounceSuppression(ctx context.Context, recipient, reason string, smtpCode int, source string) error {
	if recipient == "" {
		return nil
	}
	rh := sha256Hex(recipient)
	// Defer write-guard until we know we need to write.
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	sup := &storage.MailSuppression{
		ID:            suppressionID(),
		RecipientHash: rh,
		Reason:        "bounce",
		SMTPCode:      smtpCode,
		Source:        source,
		AddedAt:       time.Now().UTC().Format(time.RFC3339),
		Active:        true,
	}
	if _, err := s.store.MailSuppressionUpsert(ctx, sup); err != nil {
		s.log.WarnContext(ctx, "bounce suppression upsert failed",
			"recipient_hash", rh, "error", err)
		return err
	}
	return nil
}

// --- Queue public methods ---------------------------------------------------

// QueueGetSummary returns counts per queue bucket as a flat map.
func (s *Service) QueueGetSummary(ctx context.Context) (map[string]int64, error) {
	sum, err := s.store.MailQueueSummaryRead(ctx)
	if err != nil {
		return nil, fmt.Errorf("queue summary: %w", err)
	}
	return map[string]int64{
		"hold":     sum.Hold,
		"active":   sum.Active,
		"schedule": sum.Schedule,
		"deferred": sum.Deferred,
		"fail":     sum.Fail,
		"drop":     sum.Drop,
	}, nil
}

// QueueList returns queue items filtered by bucket (or all if bucket is empty).
func (s *Service) QueueList(ctx context.Context, bucket, cursor string, limit int) ([]*storage.MailQueueItem, error) {
	return s.store.MailQueueList(ctx, bucket, limit, cursor)
}

// QueueBulkAction moves a set of queue items to a new bucket.
// Supported actions: hold / unhold (= active) / schedule / fail / drop.
func (s *Service) QueueBulkAction(ctx context.Context, ids []string, action string) (int64, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, errors.New("queue bulk action: ids required")
	}
	newBucket, ok := mapQueueAction(action)
	if !ok {
		return 0, fmt.Errorf("queue bulk action: unknown action %q", action)
	}
	n, err := s.store.MailQueueBulkUpdateBucket(ctx, ids, newBucket)
	if err != nil {
		return 0, fmt.Errorf("queue bulk action: %w", err)
	}
	risk := "medium"
	switch action {
	case "fail", "drop":
		risk = "high"
	}
	s.addAudit(ctx, EventTypeQueueAction,
		fmt.Sprintf("queue bulk %s: %d items moved to bucket %q", action, n, newBucket),
		map[string]any{
			"action":      action,
			"new_bucket":  newBucket,
			"count":       n,
			"ids_sample":  sampleIDs(ids, 10),
		}, risk)
	s.publish(ctx, EventTypeQueueAction, map[string]any{
		"action":     action,
		"new_bucket": newBucket,
		"count":      n,
	})
	s.touchLastChange()
	return n, nil
}

func mapQueueAction(a string) (string, bool) {
	switch strings.ToLower(a) {
	case "hold":
		return "hold", true
	case "unhold":
		return "active", true
	case "schedule":
		return "schedule", true
	case "fail":
		return "fail", true
	case "drop":
		return "drop", true
	default:
		return "", false
	}
}

func sampleIDs(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

// --- Suppression public methods ---------------------------------------------

// SuppressionList returns paginated suppression rows.
func (s *Service) SuppressionList(ctx context.Context, f storage.MailSuppressionFilter) ([]*storage.MailSuppression, error) {
	return s.store.MailSuppressionList(ctx, f)
}

// SuppressionUpsert creates or updates a suppression row (by recipient_hash).
func (s *Service) SuppressionUpsert(ctx context.Context, sup *storage.MailSuppression) (*storage.MailSuppression, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if sup == nil {
		return nil, errors.New("suppression upsert: nil entry")
	}
	if strings.TrimSpace(sup.RecipientHash) == "" {
		return nil, errors.New("suppression upsert: recipient_hash is required")
	}
	if sup.ID == "" {
		sup.ID = suppressionID()
	}
	saved, err := s.store.MailSuppressionUpsert(ctx, sup)
	if err != nil {
		return nil, fmt.Errorf("suppression upsert: %w", err)
	}
	s.addAudit(ctx, EventTypeSuppressionUpdated,
		fmt.Sprintf("upserted suppression id=%s active=%t reason=%s",
			saved.ID, saved.Active, saved.Reason),
		map[string]any{
			"id":              saved.ID,
			"recipient_hash":  saved.RecipientHash,
			"active":          saved.Active,
			"reason":          saved.Reason,
			"smtp_code":       saved.SMTPCode,
		}, "medium")
	s.publish(ctx, EventTypeSuppressionUpdated, map[string]any{
		"id":             saved.ID,
		"active":         saved.Active,
		"reason":         saved.Reason,
		"recipient_hash": saved.RecipientHash,
	})
	s.touchLastChange()
	return saved, nil
}

// SuppressionDelete removes a suppression row by id.
func (s *Service) SuppressionDelete(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("suppression delete: id is required")
	}
	if err := s.store.MailSuppressionDelete(ctx, id); err != nil {
		return fmt.Errorf("suppression delete: %w", err)
	}
	s.addAudit(ctx, EventTypeSuppressionUpdated,
		fmt.Sprintf("deleted suppression id=%s", id),
		map[string]any{"id": id}, "medium")
	s.publish(ctx, EventTypeSuppressionUpdated, map[string]any{
		"id":     id,
		"action": "deleted",
	})
	s.touchLastChange()
	return nil
}

// SuppressionBulkImport imports a batch of suppression entries (upsert per
// recipient_hash).  Returns count of rows affected.
func (s *Service) SuppressionBulkImport(ctx context.Context, entries []storage.MailSuppression) (int64, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	// Assign ids / added_at where missing so storage doesn't need to know
	// about service-layer id helpers.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range entries {
		if entries[i].ID == "" {
			entries[i].ID = suppressionID()
		}
		if entries[i].AddedAt == "" {
			entries[i].AddedAt = now
		}
	}
	n, err := s.store.MailSuppressionBulkImport(ctx, entries)
	if err != nil {
		return 0, fmt.Errorf("suppression bulk import: %w", err)
	}
	s.addAudit(ctx, EventTypeSuppressionUpdated,
		fmt.Sprintf("imported %d suppressions", n),
		map[string]any{"count": n, "input_count": len(entries)}, "medium")
	s.publish(ctx, EventTypeSuppressionUpdated, map[string]any{
		"action": "bulk_import",
		"count":  n,
	})
	s.touchLastChange()
	return n, nil
}

// SuppressionPruneExpired removes rows whose expires_at has elapsed.
func (s *Service) SuppressionPruneExpired(ctx context.Context) (int64, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return 0, err
	}
	n, err := s.store.MailSuppressionPruneExpired(ctx)
	if err != nil {
		return 0, fmt.Errorf("suppression prune expired: %w", err)
	}
	s.addAudit(ctx, EventTypeRetentionPruned,
		fmt.Sprintf("pruned %d expired suppression rows", n),
		map[string]any{"scope": "suppression", "count": n}, "medium")
	s.publish(ctx, EventTypeRetentionPruned, map[string]any{
		"scope": "suppression",
		"count": n,
	})
	s.touchLastChange()
	return n, nil
}

// --- Rate / threshold public methods ---------------------------------------

// OutboundRateGetSnapshot returns a zero-filled snapshot for the given scope
// with thresholds populated from the DB.  Real counter bumps are wired in
// Phase 7.
func (s *Service) OutboundRateGetSnapshot(ctx context.Context, scope string) (OutboundRateSnapshot, error) {
	if scope == "" {
		scope = "global"
	}
	all, err := s.store.MailOutboundThresholdList(ctx)
	if err != nil {
		return OutboundRateSnapshot{}, fmt.Errorf("rate snapshot: %w", err)
	}
	// Pick most-specific matching threshold.  Fallback chain: exact scope →
	// parent (domain → global) → global → defaults.
	var match *storage.MailOutboundThreshold
	fallback := &storage.MailOutboundThreshold{
		Scope:              "global",
		Send1mWarn:         600,
		Send1mCrit:         1200,
		Send1hWarn:         10_000,
		Send1hCrit:         20_000,
		BounceRatePctWarn:  5.0,
		BounceRatePctCrit:  10.0,
	}
	// Iterate to find exact match first, then a broader scope.
	var globalMatch *storage.MailOutboundThreshold
	for i := range all {
		t := all[i]
		if t.Scope == scope {
			match = t
		}
		if t.Scope == "global" {
			globalMatch = t
		}
	}
	if match == nil {
		// Derive parent scope: "domain:x" → "global". "account:a@b" → "global".
		if globalMatch != nil {
			match = globalMatch
		} else {
			match = fallback
		}
	}
	snap := OutboundRateSnapshot{
		Scope:             scope,
		Send1m:            0,
		Send1mWarn:        match.Send1mWarn,
		Send1mCrit:        match.Send1mCrit,
		Send1h:            0,
		Send1hWarn:        match.Send1hWarn,
		Send1hCrit:        match.Send1hCrit,
		BounceRatePct:     0,
		BounceRatePctWarn: match.BounceRatePctWarn,
		BounceRatePctCrit: match.BounceRatePctCrit,
		Severity:          "good",
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	_ = net.ParseIP // keep for potential future use in delivery.go
	return snap, nil
}

// OutboundThresholdList returns all configured outbound rate thresholds.
func (s *Service) OutboundThresholdList(ctx context.Context) ([]*storage.MailOutboundThreshold, error) {
	return s.store.MailOutboundThresholdList(ctx)
}

// OutboundThresholdUpsert creates or updates an outbound rate threshold.
func (s *Service) OutboundThresholdUpsert(ctx context.Context, t *storage.MailOutboundThreshold) (*storage.MailOutboundThreshold, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("threshold upsert: nil")
	}
	if strings.TrimSpace(t.Scope) == "" {
		return nil, errors.New("threshold upsert: scope required")
	}
	saved, err := s.store.MailOutboundThresholdUpsert(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("threshold upsert: %w", err)
	}
	s.addAudit(ctx, EventTypeSettingsUpdated,
		fmt.Sprintf("updated outbound threshold scope=%s", saved.Scope),
		map[string]any{
			"scope":             saved.Scope,
			"send_1m_warn":      saved.Send1mWarn,
			"send_1m_crit":      saved.Send1mCrit,
			"send_1h_warn":      saved.Send1hWarn,
			"send_1h_crit":      saved.Send1hCrit,
			"bounce_rate_warn":  saved.BounceRatePctWarn,
			"bounce_rate_crit":  saved.BounceRatePctCrit,
		}, "medium")
	s.publish(ctx, EventTypeSettingsUpdated, map[string]any{
		"scope": saved.Scope,
	})
	s.touchLastChange()
	return saved, nil
}
