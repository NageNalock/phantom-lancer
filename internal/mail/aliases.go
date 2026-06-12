package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Alias service-layer value types.
// -----------------------------------------------------------------------------

// AliasUpsertRequest is the UI -> service payload for creating or updating
// an alias / distribution list / catch-all rule.  An empty ID means "create".
type AliasUpsertRequest struct {
	ID          string   `json:"id,omitempty"`
	DomainID    string   `json:"domain_id"`
	Source      string   `json:"source"`            // alias source address (or "@domain" for catch-all)
	Recipients  []string `json:"recipients"`        // list of destination addresses
	Mode        string   `json:"mode"`              // alias | list | catchall
	ListName    string   `json:"list_name,omitempty"`
	ListReplyTo string   `json:"list_reply_to,omitempty"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
}

// AliasResponse wraps a persisted MailAlias row plus the parsed recipients
// slice (de-comma'd from the storage CSV form).
type AliasResponse struct {
	ID          string   `json:"id"`
	DomainID    string   `json:"domain_id"`
	Source      string   `json:"source"`
	Recipients  []string `json:"recipients"`
	Mode        string   `json:"mode"`
	ListName    string   `json:"list_name,omitempty"`
	ListReplyTo string   `json:"list_reply_to,omitempty"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Drifted     bool     `json:"drifted"`
}

// AliasListResponse wraps the list plus summary counts for the UI top card.
type AliasListResponse struct {
	Items    []AliasResponse `json:"items"`
	Count    int             `json:"count"`
	Enabled  int             `json:"enabled_count"`
	Lists    int             `json:"list_count"`
	CatchAll int             `json:"catchall_count"`
	Drifted  bool            `json:"drifted"`
	ImportRO bool            `json:"import_read_only"`
}

// joinRecipients encodes a recipients slice into the CSV storage column.
// Whitespace around recipients is trimmed; empty entries are dropped so the
// stored CSV is always canonical.
func joinRecipients(rs []string) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, r)
	}
	return strings.Join(out, ",")
}

// splitRecipients reverses joinRecipients.  An empty / blank CSV string
// yields a zero-length slice (never nil).
func splitRecipients(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return []string{}
	}
	raw := strings.Split(csv, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// validateRecipients applies loose email-validation to every destination.
// Returns a joined error listing each bad recipient (so the UI can highlight
// them in a single pass rather than one at a time).
func validateRecipients(rs []string) error {
	bad := make([]string, 0)
	for _, r := range rs {
		if !looseEmail.MatchString(r) {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("invalid recipients: %s", strings.Join(bad, ", "))
	}
	return nil
}

// toAliasResponse maps a storage MailAlias row into its wire form.
func toAliasResponse(a storage.MailAlias) AliasResponse {
	return AliasResponse{
		ID:          a.ID,
		DomainID:    a.DomainID,
		Source:      a.Source,
		Recipients:  splitRecipients(a.RecipientsCSV),
		Mode:        a.Mode,
		ListName:    a.ListName,
		ListReplyTo: a.ListReplyTo,
		Description: a.Description,
		Enabled:     a.Enabled,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

// -----------------------------------------------------------------------------
// Service methods.
// -----------------------------------------------------------------------------

// MailAliasUpsert creates or updates an alias.  Same write-guard rules as
// accounts: config_drifted and import_read_only both reject the write.
func (s *Service) MailAliasUpsert(ctx context.Context, req AliasUpsertRequest) (*AliasResponse, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if req.DomainID == "" {
		return nil, errors.New("domain_id is required")
	}
	if strings.TrimSpace(req.Source) == "" {
		return nil, errors.New("source is required")
	}
	if len(req.Recipients) == 0 {
		return nil, errors.New("at least one recipient is required")
	}
	if err := validateRecipients(req.Recipients); err != nil {
		return nil, err
	}

	mode := req.Mode
	switch mode {
	case "alias", "list", "catchall":
	case "":
		mode = "alias"
	default:
		return nil, fmt.Errorf("invalid mode %q (want alias|list|catchall)", mode)
	}

	dom, derr := s.store.MailGetDomain(ctx, req.DomainID)
	if derr != nil || dom == nil {
		return nil, fmt.Errorf("domain not found: %w", derr)
	}

	// Build the full source address for validation (unless this is a catch-all).
	var fullSource string
	if mode == "catchall" {
		fullSource = "@" + strings.ToLower(strings.TrimSpace(dom.Domain))
	} else {
		fullSource = strings.ToLower(strings.TrimSpace(req.Source))
		// If the caller only supplied the local part, attach the domain.
		if !strings.Contains(fullSource, "@") {
			fullSource = fullSource + "@" + strings.ToLower(strings.TrimSpace(dom.Domain))
		}
		if !looseEmail.MatchString(fullSource) {
			return nil, fmt.Errorf("invalid source address %q", fullSource)
		}
	}

	csv := joinRecipients(req.Recipients)
	ts := time.Now().UTC().Format(time.RFC3339)
	eventType := EventTypeAliasUpdated
	var row storage.MailAlias

	if req.ID == "" {
		eventType = EventTypeAliasCreated
		row = storage.MailAlias{
			ID:            newID(IDPrefixAlias),
			DomainID:      req.DomainID,
			Source:        fullSource,
			RecipientsCSV: csv,
			Mode:          mode,
			ListName:      req.ListName,
			ListReplyTo:   req.ListReplyTo,
			Description:   req.Description,
			Enabled:       req.Enabled,
			CreatedAt:     ts,
			UpdatedAt:     ts,
		}
		saved, ierr := s.store.MailCreateAlias(ctx, row)
		if ierr != nil {
			return nil, fmt.Errorf("insert alias: %w", ierr)
		}
		row = saved
	} else {
		cur, gerr := s.store.MailGetAlias(ctx, req.ID)
		if gerr != nil {
			return nil, gerr
		}
		if cur.ID == "" {
			return nil, storage.ErrNotFound
		}
		cur.DomainID = req.DomainID
		cur.Source = fullSource
		cur.RecipientsCSV = csv
		cur.Mode = mode
		cur.ListName = req.ListName
		cur.ListReplyTo = req.ListReplyTo
		cur.Description = req.Description
		cur.Enabled = req.Enabled
		cur.UpdatedAt = ts
		saved, uerr := s.store.MailUpdateAlias(ctx, cur)
		if uerr != nil {
			return nil, fmt.Errorf("update alias: %w", uerr)
		}
		row = saved
	}

	// Best-effort CLI AliasAdd if a runner is wired (the CLI runner is a
	// stub in Phase 5 so errors are non-fatal; the DB is authoritative).
	if s.cli != nil {
		_ = ctx
	}

	risk := "medium"
	if mode == "catchall" {
		risk = "high"
	}
	s.addAudit(ctx, eventType,
		fmt.Sprintf("%s alias %s -> %s (%d recipients)",
			strings.TrimPrefix(strings.TrimPrefix(eventType, "mail."), "alias."),
			fullSource, mode, len(req.Recipients)),
		map[string]any{
			"alias_id":         row.ID,
			"domain_id":        row.DomainID,
			"source":           fullSource,
			"mode":             mode,
			"recipient_count":  len(req.Recipients),
			"enabled":          row.Enabled,
		}, risk)
	s.publish(ctx, eventType, map[string]any{
		"id":               row.ID,
		"source":           fullSource,
		"mode":             mode,
		"recipient_count":  len(req.Recipients),
		"enabled":          row.Enabled,
	})
	s.touchLastChange()

	resp := toAliasResponse(row)
	resp.Drifted = s.Drifted()
	return &resp, nil
}

// MailAliasDelete removes an alias row.  Catchall deletion is high-risk
// because it silently stops matching any previously-matched recipient.
func (s *Service) MailAliasDelete(ctx context.Context, id string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if id == "" {
		return errors.New("alias id is required")
	}
	cur, gerr := s.store.MailGetAlias(ctx, id)
	if gerr != nil || cur.ID == "" {
		return storage.ErrNotFound
	}
	risk := "high"
	if cur.Mode != "catchall" {
		risk = "medium"
	}
	if s.cli != nil {
		_, _, _ = s.cli.AliasDelete(ctx, cur.Source)
	}
	if err := s.store.MailDeleteAlias(ctx, id); err != nil {
		return fmt.Errorf("delete alias: %w", err)
	}
	s.addAudit(ctx, EventTypeAliasDeleted,
		fmt.Sprintf("deleted alias %s (mode=%s)", cur.Source, cur.Mode),
		map[string]any{
			"alias_id": id,
			"source":   cur.Source,
			"mode":     cur.Mode,
		}, risk)
	s.publish(ctx, EventTypeAliasDeleted, map[string]any{
		"id":     id,
		"source": cur.Source,
		"mode":   cur.Mode,
	})
	s.touchLastChange()
	return nil
}

// MailAliasList returns all aliases matching the optional filters.
func (s *Service) MailAliasList(ctx context.Context, domainID, mode string) (*AliasListResponse, error) {
	rows, err := s.store.MailListAliases(ctx, domainID, mode)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	out := make([]AliasResponse, len(rows))
	enabled := 0
	lists := 0
	catchAll := 0
	for i, r := range rows {
		out[i] = toAliasResponse(r)
		if r.Enabled {
			enabled++
		}
		if r.Mode == "list" {
			lists++
		}
		if r.Mode == "catchall" {
			catchAll++
		}
	}
	importRO := false
	if settings, serr := s.store.MailGetSettings(ctx); serr == nil && settings != nil {
		importRO = settings.ImportMode
	}
	return &AliasListResponse{
		Items:    out,
		Count:    len(rows),
		Enabled:  enabled,
		Lists:    lists,
		CatchAll: catchAll,
		Drifted:  s.Drifted(),
		ImportRO: importRO,
	}, nil
}

// MailAliasGet returns a single alias row.
func (s *Service) MailAliasGet(ctx context.Context, id string) (*AliasResponse, error) {
	if id == "" {
		return nil, errors.New("alias id is required")
	}
	cur, err := s.store.MailGetAlias(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.ID == "" {
		return nil, storage.ErrNotFound
	}
	resp := toAliasResponse(cur)
	resp.Drifted = s.Drifted()
	return &resp, nil
}
