package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"phantom-lancer/internal/storage"
)

// -----------------------------------------------------------------------------
// Import registration service-layer value types.
// -----------------------------------------------------------------------------

// ImportRegisterRequest registers an external Mox supervisor (or an existing
// on-disk Mox install) as an import-mode peer.  On success settings.ImportMode
// is flipped to true, which gates all other write APIs in the mail module.
type ImportRegisterRequest struct {
	Name           string `json:"name"`             // human-readable label, e.g. "production-mox-01"
	DataDir        string `json:"data_dir"`         // absolute path to Mox data directory (must exist)
	ConfigPath     string `json:"config_path"`      // absolute path to mox.conf (must exist; optional for system-managed)
	SupervisorType string `json:"supervisor_type"`  // external | systemd | supervised | embedded
	ReadOnly       bool   `json:"read_only"`        // default true; only flip to false if remote side supports writes
	ProbeURL       string `json:"probe_url"`        // http(s) URL for remote probe endpoint
	WebAPIEndpoint string `json:"webapi_endpoint"`  // remote Mox webapi base URL; informational
}

// ImportProbeResponse wraps the outcome of a live probe against an
// already-registered import source.  The full JSON body is returned so the
// UI can render any version / capability detail the remote side exposes.
type ImportProbeResponse struct {
	Registration storage.MailImportRegistration `json:"registration"`
	Reachable    bool                           `json:"reachable"`
	HTTPStatus   int                            `json:"http_status,omitempty"`
	Version      string                         `json:"version,omitempty"`
	Body         string                         `json:"body,omitempty"`
	Error        string                         `json:"error,omitempty"`
	ProbedAt     string                         `json:"probed_at"`
	Drifted      bool                           `json:"drifted"`
}

// ImportListResponse wraps the import-registration list plus the current
// settings.ImportMode flag so the UI can show the global write lock state
// without an extra round-trip.
type ImportListResponse struct {
	Items     []storage.MailImportRegistration `json:"items"`
	Count     int                              `json:"count"`
	ImportMode bool                            `json:"import_mode"`
	Drifted   bool                            `json:"drifted"`
}

// --- Helpers ----------------------------------------------------------------

// absExistingPath validates that p is absolute and refers to an existing
// filesystem path (used for DataDir / ConfigPath before persisting).
func absExistingPath(p string) error {
	if p == "" {
		return errors.New("path is required")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute: %q", p)
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("stat %q: %w", p, err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Service methods.
// -----------------------------------------------------------------------------

// MailImportRegister creates an import-registration row and, on success,
// flips settings.ImportMode=true.  Subsequent writes through any other mail
// API are rejected (import_read_only) until the registration is deleted.
//
// Guard note: this method intentionally SKIPS the import_read_only subcheck
// of checkWriteGuard because the whole point of registering an import is
// to transition INTO that mode from managed mode.  It still honours
// config_drifted (there is no legitimate reason to re-register a drifted
// install without resolving the conflict first).
func (s *Service) MailImportRegister(ctx context.Context, req ImportRegisterRequest) (*storage.MailImportRegistration, error) {
	if s.Drifted() {
		return nil, &errCoded{code: "config_drifted", msg: "config drifted; resolve before registering import"}
	}
	if req.Name == "" {
		return nil, errors.New("name is required")
	}
	if err := absExistingPath(req.DataDir); err != nil {
		return nil, fmt.Errorf("data_dir: %w", err)
	}
	// ConfigPath is optional (a bare Mox install might not have one yet).
	if req.ConfigPath != "" {
		if err := absExistingPath(req.ConfigPath); err != nil {
			return nil, fmt.Errorf("config_path: %w", err)
		}
	}
	svType := req.SupervisorType
	switch svType {
	case "external", "systemd", "supervised", "embedded":
	case "":
		svType = "external"
	default:
		return nil, fmt.Errorf("invalid supervisor_type %q", svType)
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	row := storage.MailImportRegistration{
		ID:                 newID(IDPrefixImport),
		Name:               req.Name,
		DataDir:            req.DataDir,
		ConfigPath:         req.ConfigPath,
		SupervisorType:     svType,
		ReadOnly:           true,
		ProbeURL:           req.ProbeURL,
		AccessTokenWrapped: "",
		Status:             "registered",
		LastProbeAt:        "",
		LastError:          "",
		Version:            "",
		CreatedAt:          ts,
		UpdatedAt:          ts,
	}
	if !req.ReadOnly {
		row.ReadOnly = false
	}

	// 1. Persist the registration row first.
	saved, ierr := s.store.MailCreateImportRegistration(ctx, row)
	if ierr != nil {
		return nil, fmt.Errorf("insert import registration: %w", ierr)
	}

	// 2. Flip settings.ImportMode = true + persist paths so the rest of the
	//    module sees the external data_dir/config_path as canonical.
	label := req.Name
	if _, uerr := s.store.MailUpsertImport(ctx, storage.MailImportUpdate{
		ImportMode: true,
		Label:      label,
		BinaryPath: "", // binary path is not part of the registration request
		ConfigPath: req.ConfigPath,
		DataDir:    req.DataDir,
	}); uerr != nil {
		// Rollback the registration row so the DB does not contain an orphan
		// entry that never flipped import_mode.
		_ = s.store.MailDeleteImportRegistration(ctx, saved.ID)
		return nil, fmt.Errorf("upsert import settings: %w", uerr)
	}

	s.addAudit(ctx, EventTypeImportModeLocked,
		fmt.Sprintf("locked into import mode via registration %q", label),
		map[string]any{
			"import_id":       saved.ID,
			"data_dir":        saved.DataDir,
			"supervisor_type": saved.SupervisorType,
			"read_only":       saved.ReadOnly,
		}, "high")
	s.publish(ctx, EventTypeImportModeLocked, map[string]any{
		"id":              saved.ID,
		"name":            saved.Name,
		"supervisor_type": saved.SupervisorType,
		"read_only":       saved.ReadOnly,
		"import_mode":     true,
	})
	s.touchLastChange()
	return &saved, nil
}

// MailImportList returns all registered import sources plus the current
// global import-mode flag and drift status.
func (s *Service) MailImportList(ctx context.Context) (*ImportListResponse, error) {
	rows, err := s.store.MailListImportRegistrations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list import registrations: %w", err)
	}
	importMode := false
	if settings, serr := s.store.MailGetSettings(ctx); serr == nil && settings != nil {
		importMode = settings.ImportMode
	}
	return &ImportListResponse{
		Items:      rows,
		Count:      len(rows),
		ImportMode: importMode,
		Drifted:    s.Drifted(),
	}, nil
}

// MailImportDelete removes an import-registration row and, if it was the
// last one, flips settings.ImportMode = false (writes are re-enabled).
// Always audits HIGH because dropping the last registration transitions the
// module back into managed mode with full write authority.
func (s *Service) MailImportDelete(ctx context.Context, id string) error {
	if s.Drifted() {
		return &errCoded{code: "config_drifted", msg: "config drifted; resolve before deleting registration"}
	}
	if id == "" {
		return errors.New("import registration id is required")
	}
	cur, gerr := s.store.MailGetImportRegistration(ctx, id)
	if gerr != nil {
		return gerr
	}
	if cur.ID == "" {
		return storage.ErrNotFound
	}
	name := cur.Name

	if err := s.store.MailDeleteImportRegistration(ctx, id); err != nil {
		return fmt.Errorf("delete import registration: %w", err)
	}

	// If there are no remaining registrations, exit import mode so the
	// operator can go back to managed-mode writes.
	remaining, _ := s.store.MailListImportRegistrations(ctx)
	if len(remaining) == 0 {
		if _, uerr := s.store.MailUpsertImport(ctx, storage.MailImportUpdate{
			ImportMode: false,
			Label:      "",
			BinaryPath: "",
			ConfigPath: "",
			DataDir:    "",
		}); uerr != nil {
			s.log.WarnContext(ctx, "mail: failed to clear import_mode after last registration removed", "error", uerr)
		} else {
			s.publish(ctx, EventTypeImportModeLocked, map[string]any{
				"id":          id,
				"name":        name,
				"import_mode": false,
				"action":      "cleared",
			})
		}
	}

	s.addAudit(ctx, EventTypeImportModeLocked,
		fmt.Sprintf("removed import registration %q", name),
		map[string]any{
			"import_id":       id,
			"data_dir":        cur.DataDir,
			"supervisor_type": cur.SupervisorType,
			"remaining":       len(remaining),
			"import_mode":     len(remaining) > 0,
		}, "high")
	s.touchLastChange()
	return nil
}

// MailImportProbe performs a live GET against the registration's ProbeURL
// and refreshes the row with status, version, and last-probe timestamps.
// A 5-second timeout is enforced so a stalled remote cannot block the UI.
func (s *Service) MailImportProbe(ctx context.Context, id string) (*ImportProbeResponse, error) {
	if id == "" {
		return nil, errors.New("import registration id is required")
	}
	cur, gerr := s.store.MailGetImportRegistration(ctx, id)
	if gerr != nil {
		return nil, gerr
	}
	if cur.ID == "" {
		return nil, storage.ErrNotFound
	}

	resp := &ImportProbeResponse{
		Registration: cur,
		Drifted:      s.Drifted(),
		ProbedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	if cur.ProbeURL == "" {
		resp.Error = "probe_url not configured"
		return resp, nil
	}

	// Execute the probe against the remote URL.  A dedicated context with a
	// short timeout is used so a stalled remote never hangs the caller.
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	httpReq, herr := http.NewRequestWithContext(pctx, http.MethodGet, cur.ProbeURL, nil)
	if herr != nil {
		resp.Error = fmt.Sprintf("build probe request: %v", herr)
		cur.LastError = resp.Error
		cur.Status = "error"
		cur.LastProbeAt = resp.ProbedAt
		cur.UpdatedAt = resp.ProbedAt
		_, _ = s.store.MailUpdateImportRegistration(ctx, cur)
		return resp, nil
	}
	httpResp, rerr := (&http.Client{}).Do(httpReq)
	if rerr != nil {
		resp.Error = rerr.Error()
		cur.LastError = resp.Error
		cur.Status = "error"
		cur.LastProbeAt = resp.ProbedAt
		cur.UpdatedAt = resp.ProbedAt
		_, _ = s.store.MailUpdateImportRegistration(ctx, cur)
		return resp, nil
	}
	defer httpResp.Body.Close()
	resp.HTTPStatus = httpResp.StatusCode
	resp.Reachable = httpResp.StatusCode >= 200 && httpResp.StatusCode < 300

	// Parse the body for structured version info (the typical Mox
	// /metrics-like or /status endpoint returns JSON).  We don't surface an
	// error on parse failure: the raw body is returned verbatim as a
	// fallback.
	var rawBody []byte
	buf := make([]byte, 8192)
	n, _ := httpResp.Body.Read(buf)
	rawBody = buf[:n]
	resp.Body = string(rawBody)
	// Try to extract a .version field from a JSON body.
	var parsed map[string]any
	if jerr := json.Unmarshal(rawBody, &parsed); jerr == nil {
		if v, ok := parsed["version"].(string); ok {
			resp.Version = v
		} else if v, ok := parsed["mox_version"].(string); ok {
			resp.Version = v
		}
	}

	// Update the row with probe outcomes.
	cur.LastProbeAt = resp.ProbedAt
	cur.UpdatedAt = resp.ProbedAt
	if resp.Reachable {
		cur.Status = "connected"
		cur.LastError = ""
		if resp.Version != "" {
			cur.Version = resp.Version
		}
	} else {
		cur.Status = "error"
		if resp.Error == "" {
			cur.LastError = fmt.Sprintf("HTTP %d", resp.HTTPStatus)
		}
	}
	saved, uerr := s.store.MailUpdateImportRegistration(ctx, cur)
	if uerr != nil {
		s.log.WarnContext(ctx, "mail: update import registration after probe failed", "error", uerr)
	} else {
		resp.Registration = saved
	}

	s.addAudit(ctx, "mail.import.probe",
		fmt.Sprintf("probed import %q: reachable=%v status=%s", cur.Name, resp.Reachable, cur.Status),
		map[string]any{
			"import_id":    id,
			"probe_url":    cur.ProbeURL,
			"reachable":    resp.Reachable,
			"http_status":  resp.HTTPStatus,
			"version":      resp.Version,
		}, "low")
	return resp, nil
}
