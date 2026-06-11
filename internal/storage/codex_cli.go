package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"phantom-lancer/internal/ids"
)

// CodexCliInstallation captures the redacted detection summary for the local
// codex CLI binary. Only capability bits and summaries are persisted, never raw
// doctor output, tokens or environment.
type CodexCliInstallation struct {
	ID             string         `json:"id"`
	BinaryPath     string         `json:"binaryPath"`
	Version        string         `json:"version"`
	Status         string         `json:"status"`
	Capabilities   map[string]any `json:"capabilities"`
	DoctorSummary  map[string]any `json:"doctorSummary"`
	LastProbeError string         `json:"lastProbeError,omitempty"`
	DetectedAt     string         `json:"detectedAt,omitempty"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

type CodexCliWorkspace struct {
	ID                    string         `json:"id"`
	Label                 string         `json:"label"`
	Path                  string         `json:"-"`
	PathSummary           string         `json:"pathSummary"`
	TrustState            string         `json:"trustState"`
	DefaultModel          string         `json:"defaultModel,omitempty"`
	DefaultSandbox        string         `json:"defaultSandbox"`
	DefaultApprovalPolicy string         `json:"defaultApprovalPolicy"`
	NetworkPolicy         map[string]any `json:"networkPolicy"`
	Pinned                bool           `json:"pinned"`
	LastOpenedAt          string         `json:"lastOpenedAt,omitempty"`
	GitBranch             string         `json:"gitBranch,omitempty"`
	GitState              string         `json:"gitState,omitempty"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt"`
}

type CodexCliThread struct {
	ID               string `json:"id"`
	CodexThreadID    string `json:"codexThreadId,omitempty"`
	WorkspaceID      string `json:"workspaceId"`
	Title            string `json:"title"`
	Status           string `json:"status"`
	SourceMode       string `json:"sourceMode"`
	Kind             string `json:"kind,omitempty"`
	Background       bool   `json:"background"`
	BackgroundSource string `json:"backgroundSource,omitempty"`
	Model            string `json:"model,omitempty"`
	SandboxMode      string `json:"sandboxMode"`
	ApprovalPolicy   string `json:"approvalPolicy"`
	Pinned           bool   `json:"pinned"`
	ArchivedAt       string `json:"archivedAt,omitempty"`
	LastTurnID       string `json:"lastTurnId,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

type CodexCliThreadFilters struct {
	IncludeArchived bool
	Query           string
	WorkspaceID     string
	Status          string
	Kind            string
}

type CodexCliTurn struct {
	ID             string         `json:"id"`
	ThreadID       string         `json:"threadId"`
	CodexTurnID    string         `json:"codexTurnId,omitempty"`
	Status         string         `json:"status"`
	PromptSummary  string         `json:"promptSummary,omitempty"`
	Model          string         `json:"model,omitempty"`
	SandboxMode    string         `json:"sandboxMode,omitempty"`
	ApprovalPolicy string         `json:"approvalPolicy,omitempty"`
	StartedAt      string         `json:"startedAt,omitempty"`
	CompletedAt    string         `json:"completedAt,omitempty"`
	ErrorSummary   string         `json:"errorSummary,omitempty"`
	Usage          map[string]any `json:"usage,omitempty"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

type CodexCliEvent struct {
	ID          string         `json:"id"`
	ThreadID    string         `json:"threadId"`
	TurnID      string         `json:"turnId,omitempty"`
	Sequence    int64          `json:"sequence"`
	EventType   string         `json:"eventType"`
	CodexMethod string         `json:"codexMethod,omitempty"`
	ItemType    string         `json:"itemType,omitempty"`
	Payload     map[string]any `json:"payload"`
	TextPreview string         `json:"textPreview,omitempty"`
	CreatedAt   string         `json:"createdAt"`
}

type CodexCliApproval struct {
	ID             string         `json:"id"`
	ThreadID       string         `json:"threadId"`
	TurnID         string         `json:"turnId,omitempty"`
	CodexRequestID string         `json:"codexRequestId,omitempty"`
	Status         string         `json:"status"`
	ActionKind     string         `json:"actionKind"`
	CommandPreview string         `json:"commandPreview,omitempty"`
	CwdSummary     string         `json:"cwdSummary,omitempty"`
	RiskLevel      string         `json:"riskLevel"`
	RequestPayload map[string]any `json:"requestPayload,omitempty"`
	Decision       string         `json:"decision,omitempty"`
	DecidedAt      string         `json:"decidedAt,omitempty"`
	ExpiresAt      string         `json:"expiresAt,omitempty"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
	// JSONRPCRequestID stores the raw JSON-RPC request id that must be echoed
	// back when replying to the codex app-server. It is persisted so the
	// broker map can be re-hydrated after service restart. Empty for
	// approvals created before the recovery column was added.
	JSONRPCRequestID string `json:"-"`
	// RecoveryStatus is "live" when the approval is attached to a live codex
	// app-server; "orphaned" after a service restart where the owning
	// subprocess died and no reply can be delivered. Orphaned approvals are still
	// decidable by the owner for audit, but resolve as decision="stale".
	RecoveryStatus string `json:"recoveryStatus"`
}

type CodexCliRun struct {
	ID              string `json:"id"`
	ThreadID        string `json:"threadId,omitempty"`
	TurnID          string `json:"turnId,omitempty"`
	Mode            string `json:"mode"`
	PID             int    `json:"pid,omitempty"`
	Status          string `json:"status"`
	StartedAt       string `json:"startedAt,omitempty"`
	LastHeartbeatAt string `json:"lastHeartbeatAt,omitempty"`
	ExitedAt        string `json:"exitedAt,omitempty"`
	ExitCode        int    `json:"exitCode"`
	ErrorSummary    string `json:"errorSummary,omitempty"`
}

type CodexCliAttachment struct {
	ID          string `json:"id"`
	ThreadID    string `json:"threadId,omitempty"`
	TurnID      string `json:"turnId,omitempty"`
	Kind        string `json:"kind"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes"`
	StoragePath string `json:"-"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type CodexCliReviewComment struct {
	ID          string `json:"id"`
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	FilePath    string `json:"filePath"`
	OldLine     int    `json:"oldLine,omitempty"`
	NewLine     int    `json:"newLine,omitempty"`
	HunkHeader  string `json:"hunkHeader,omitempty"`
	Body        string `json:"body"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	ResolvedAt  string `json:"resolvedAt,omitempty"`
}

type CodexCliCommand struct {
	ID             string `json:"id"`
	ThreadID       string `json:"threadId"`
	WorkspaceID    string `json:"workspaceId,omitempty"`
	CommandPreview string `json:"commandPreview"`
	CwdSummary     string `json:"cwdSummary,omitempty"`
	Status         string `json:"status"`
	ExitCode       int    `json:"exitCode"`
	OutputPreview  string `json:"outputPreview,omitempty"`
	ErrorSummary   string `json:"errorSummary,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	CompletedAt    string `json:"completedAt,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type CodexCliBrowserSession struct {
	ID          string `json:"id"`
	ThreadID    string `json:"threadId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	URL         string `json:"url"`
	Status      string `json:"status"`
	LastError   string `json:"lastError,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type CodexCliAutomation struct {
	ID                    string         `json:"id"`
	Kind                  string         `json:"kind"`
	ThreadID              string         `json:"threadId,omitempty"`
	WorkspaceID           string         `json:"workspaceId,omitempty"`
	Title                 string         `json:"title"`
	PromptSummary         string         `json:"promptSummary"`
	Schedule              map[string]any `json:"schedule"`
	Enabled               bool           `json:"enabled"`
	DefaultSandbox        string         `json:"defaultSandbox"`
	DefaultApprovalPolicy string         `json:"defaultApprovalPolicy"`
	LastRunAt             string         `json:"lastRunAt,omitempty"`
	NextRunAt             string         `json:"nextRunAt,omitempty"`
	RetryCount            int            `json:"retryCount"`
	FailureBackoffUntil   string         `json:"failureBackoffUntil,omitempty"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt"`
}

type CodexCliAutomationRun struct {
	ID              string `json:"id"`
	AutomationID    string `json:"automationId"`
	ThreadID        string `json:"threadId,omitempty"`
	TurnID          string `json:"turnId,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	Status          string `json:"status"`
	StartedAt       string `json:"startedAt,omitempty"`
	LastHeartbeatAt string `json:"lastHeartbeatAt,omitempty"`
	CompletedAt     string `json:"completedAt,omitempty"`
	FindingSummary  string `json:"findingSummary,omitempty"`
	ErrorSummary    string `json:"errorSummary,omitempty"`
	TriageState     string `json:"triageState"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type CodexCliCapabilityCache struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Payload   map[string]any `json:"payload"`
	LastError string         `json:"lastError,omitempty"`
	ProbedAt  string         `json:"probedAt,omitempty"`
	UpdatedAt string         `json:"updatedAt"`
}

type CodexCliNotification struct {
	ID        string         `json:"id"`
	Scope     string         `json:"scope"`
	ScopeID   string         `json:"scopeId,omitempty"`
	EventType string         `json:"eventType"`
	Title     string         `json:"title"`
	Summary   string         `json:"summary"`
	Status    string         `json:"status"`
	Severity  string         `json:"severity"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
}

// ---- generic settings (codex_cli.* keys) ----

// GetSettingsByPrefix returns all settings rows whose key starts with prefix.
func (s *Store) GetSettingsByPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// PutSettings upserts the provided key/value settings rows in a single tx.
func (s *Store) PutSettings(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	now := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- installations ----

func (s *Store) UpsertCodexCliInstallation(ctx context.Context, install CodexCliInstallation) (CodexCliInstallation, error) {
	now := now()
	if install.Capabilities == nil {
		install.Capabilities = map[string]any{}
	}
	if install.DoctorSummary == nil {
		install.DoctorSummary = map[string]any{}
	}
	caps, _ := json.Marshal(install.Capabilities)
	doctor, _ := json.Marshal(install.DoctorSummary)
	existing, err := s.GetCodexCliInstallation(ctx)
	createdAt := now
	id := "default"
	if err == nil {
		createdAt = existing.CreatedAt
		id = existing.ID
	} else if !errors.Is(err, ErrNotFound) {
		return CodexCliInstallation{}, err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_installations (id, binary_path, version, status, capabilities_json, doctor_summary_json, last_probe_error, detected_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  binary_path = excluded.binary_path,
  version = excluded.version,
  status = excluded.status,
  capabilities_json = excluded.capabilities_json,
  doctor_summary_json = excluded.doctor_summary_json,
  last_probe_error = excluded.last_probe_error,
  detected_at = excluded.detected_at,
  updated_at = excluded.updated_at`,
		id, install.BinaryPath, install.Version, install.Status, string(caps), string(doctor), install.LastProbeError, now, createdAt, now)
	if err != nil {
		return CodexCliInstallation{}, err
	}
	return s.GetCodexCliInstallation(ctx)
}

func (s *Store) GetCodexCliInstallation(ctx context.Context) (CodexCliInstallation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, binary_path, version, status, capabilities_json, doctor_summary_json, last_probe_error, detected_at, created_at, updated_at FROM codex_cli_installations ORDER BY updated_at DESC LIMIT 1`)
	var install CodexCliInstallation
	var caps, doctor string
	err := row.Scan(&install.ID, &install.BinaryPath, &install.Version, &install.Status, &caps, &doctor, &install.LastProbeError, &install.DetectedAt, &install.CreatedAt, &install.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliInstallation{}, ErrNotFound
	}
	if err != nil {
		return CodexCliInstallation{}, err
	}
	_ = json.Unmarshal([]byte(caps), &install.Capabilities)
	_ = json.Unmarshal([]byte(doctor), &install.DoctorSummary)
	if install.Capabilities == nil {
		install.Capabilities = map[string]any{}
	}
	if install.DoctorSummary == nil {
		install.DoctorSummary = map[string]any{}
	}
	return install, nil
}

// ---- workspaces ----

func NormalizeCodexCliWorkspace(ws CodexCliWorkspace) CodexCliWorkspace {
	ws.Label = strings.TrimSpace(ws.Label)
	ws.Path = strings.TrimSpace(ws.Path)
	ws.TrustState = strings.TrimSpace(strings.ToLower(ws.TrustState))
	switch ws.TrustState {
	case "trusted", "restricted":
	default:
		ws.TrustState = "untrusted"
	}
	ws.DefaultSandbox = strings.TrimSpace(ws.DefaultSandbox)
	if ws.DefaultSandbox == "" {
		ws.DefaultSandbox = "read-only"
	}
	ws.DefaultApprovalPolicy = strings.TrimSpace(ws.DefaultApprovalPolicy)
	if ws.DefaultApprovalPolicy != "on-request" {
		ws.DefaultApprovalPolicy = "on-request"
	}
	ws.DefaultModel = strings.TrimSpace(ws.DefaultModel)
	if ws.NetworkPolicy == nil {
		ws.NetworkPolicy = map[string]any{"enabled": false}
	}
	if ws.TrustState != "trusted" {
		ws.NetworkPolicy["enabled"] = false
	}
	if strings.TrimSpace(ws.Label) == "" {
		ws.Label = workspaceLabelFromPath(ws.Path)
	}
	if strings.TrimSpace(ws.PathSummary) == "" {
		ws.PathSummary = summarizePath(ws.Path)
	}
	return ws
}

func (s *Store) CreateCodexCliWorkspace(ctx context.Context, ws CodexCliWorkspace) (CodexCliWorkspace, error) {
	ws = NormalizeCodexCliWorkspace(ws)
	id, err := ids.New("cxws")
	if err != nil {
		return CodexCliWorkspace{}, err
	}
	ws.ID = id
	now := now()
	ws.CreatedAt = now
	ws.UpdatedAt = now
	net, _ := json.Marshal(ws.NetworkPolicy)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_workspaces (id, label, path, path_summary, trust_state, default_model, default_sandbox, default_approval_policy, network_policy_json, pinned, last_opened_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.ID, ws.Label, ws.Path, ws.PathSummary, ws.TrustState, ws.DefaultModel, ws.DefaultSandbox, ws.DefaultApprovalPolicy, string(net), boolToInt(ws.Pinned), ws.LastOpenedAt, ws.CreatedAt, ws.UpdatedAt)
	if err != nil {
		return CodexCliWorkspace{}, err
	}
	return s.GetCodexCliWorkspace(ctx, ws.ID)
}

func (s *Store) GetCodexCliWorkspace(ctx context.Context, id string) (CodexCliWorkspace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, label, path, path_summary, trust_state, default_model, default_sandbox, default_approval_policy, network_policy_json, pinned, last_opened_at, created_at, updated_at FROM codex_cli_workspaces WHERE id = ?`, id)
	ws, err := scanCodexCliWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliWorkspace{}, ErrNotFound
	}
	return ws, err
}

func (s *Store) ListCodexCliWorkspaces(ctx context.Context) ([]CodexCliWorkspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, label, path, path_summary, trust_state, default_model, default_sandbox, default_approval_policy, network_policy_json, pinned, last_opened_at, created_at, updated_at FROM codex_cli_workspaces ORDER BY pinned DESC, CASE WHEN last_opened_at = '' THEN 1 ELSE 0 END, last_opened_at DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliWorkspace{}
	for rows.Next() {
		ws, err := scanCodexCliWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCodexCliWorkspace(ctx context.Context, ws CodexCliWorkspace) (CodexCliWorkspace, error) {
	existing, err := s.GetCodexCliWorkspace(ctx, ws.ID)
	if err != nil {
		return CodexCliWorkspace{}, err
	}
	ws.Path = existing.Path
	ws = NormalizeCodexCliWorkspace(ws)
	net, _ := json.Marshal(ws.NetworkPolicy)
	_, err = s.db.ExecContext(ctx, `
UPDATE codex_cli_workspaces SET label = ?, path_summary = ?, trust_state = ?, default_model = ?, default_sandbox = ?, default_approval_policy = ?, network_policy_json = ?, pinned = ?, updated_at = ? WHERE id = ?`,
		ws.Label, ws.PathSummary, ws.TrustState, ws.DefaultModel, ws.DefaultSandbox, ws.DefaultApprovalPolicy, string(net), boolToInt(ws.Pinned), now(), ws.ID)
	if err != nil {
		return CodexCliWorkspace{}, err
	}
	return s.GetCodexCliWorkspace(ctx, ws.ID)
}

func (s *Store) TouchCodexCliWorkspace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_workspaces SET last_opened_at = ?, updated_at = ? WHERE id = ?`, now(), now(), id)
	return err
}

func (s *Store) DeleteCodexCliWorkspace(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_workspaces WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- threads ----

const codexCliThreadColumns = `id, codex_thread_id, workspace_id, title, status, source_mode, kind, background, background_source, model, sandbox_mode, approval_policy, pinned, archived_at, last_turn_id, last_error, created_at, updated_at`

func (s *Store) CreateCodexCliThread(ctx context.Context, thread CodexCliThread) (CodexCliThread, error) {
	id, err := ids.New("cxth")
	if err != nil {
		return CodexCliThread{}, err
	}
	thread.ID = id
	now := now()
	thread.CreatedAt = now
	thread.UpdatedAt = now
	if thread.Status == "" {
		thread.Status = "idle"
	}
	if thread.SourceMode == "" {
		thread.SourceMode = "app_server"
	}
	if strings.TrimSpace(thread.Title) == "" {
		thread.Title = "新对话"
	}
	if strings.TrimSpace(thread.Kind) == "" {
		thread.Kind = "code"
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_threads (`+codexCliThreadColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		thread.ID, thread.CodexThreadID, thread.WorkspaceID, thread.Title, thread.Status, thread.SourceMode, thread.Kind, boolInt(thread.Background), thread.BackgroundSource, thread.Model, thread.SandboxMode, thread.ApprovalPolicy, boolInt(thread.Pinned), thread.ArchivedAt, thread.LastTurnID, thread.LastError, thread.CreatedAt, thread.UpdatedAt)
	if err != nil {
		return CodexCliThread{}, err
	}
	return s.GetCodexCliThread(ctx, thread.ID)
}

func (s *Store) GetCodexCliThread(ctx context.Context, id string) (CodexCliThread, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+codexCliThreadColumns+` FROM codex_cli_threads WHERE id = ?`, id)
	thread, err := scanCodexCliThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliThread{}, ErrNotFound
	}
	return thread, err
}

func (s *Store) ListCodexCliThreads(ctx context.Context, includeArchived bool, q string) ([]CodexCliThread, error) {
	return s.ListCodexCliThreadsFiltered(ctx, CodexCliThreadFilters{IncludeArchived: includeArchived, Query: q})
}

func (s *Store) ListCodexCliThreadsFiltered(ctx context.Context, filters CodexCliThreadFilters) ([]CodexCliThread, error) {
	query := `SELECT ` + codexCliThreadColumns + ` FROM codex_cli_threads`
	clauses := []string{}
	args := []any{}
	if !filters.IncludeArchived {
		clauses = append(clauses, "archived_at = ''")
	}
	if workspaceID := strings.TrimSpace(filters.WorkspaceID); workspaceID != "" {
		clauses = append(clauses, "workspace_id = ?")
		args = append(args, workspaceID)
	}
	if status := strings.TrimSpace(filters.Status); status != "" && status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if kind := strings.TrimSpace(filters.Kind); kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, kind)
	}
	if q := strings.TrimSpace(filters.Query); q != "" {
		like := "%" + q + "%"
		clauses = append(clauses, `(
			title LIKE ? OR last_error LIKE ? OR model LIKE ?
			OR workspace_id IN (SELECT id FROM codex_cli_workspaces WHERE label LIKE ? OR path_summary LIKE ?)
			OR id IN (SELECT thread_id FROM codex_cli_turns WHERE prompt_summary LIKE ? OR error_summary LIKE ?)
			OR id IN (SELECT thread_id FROM codex_cli_events WHERE text_preview LIKE ?)
		)`)
		args = append(args, like, like, like, like, like, like, like, like)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY pinned DESC, updated_at DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliThread{}
	for rows.Next() {
		thread, err := scanCodexCliThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

func (s *Store) ListBackgroundCodexCliThreads(ctx context.Context, limit int) ([]CodexCliThread, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+codexCliThreadColumns+` FROM codex_cli_threads WHERE background = 1 AND archived_at = '' ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliThread{}
	for rows.Next() {
		thread, err := scanCodexCliThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

func (s *Store) HasRunningCodexCliTurn(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM codex_cli_turns WHERE status IN ('running', 'waiting_approval')`).Scan(&count)
	return count > 0, err
}

func (s *Store) CountRunningCodexCliTurns(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM codex_cli_turns WHERE status IN ('running', 'waiting_approval')`).Scan(&count)
	return count, err
}

func (s *Store) CountCodexCliTurnsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(1) FROM codex_cli_turns GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}

func (s *Store) ListQueuedCodexCliTurns(ctx context.Context, limit int) ([]CodexCliTurn, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+codexCliTurnColumns+` FROM codex_cli_turns WHERE status = 'queued' ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliTurn{}
	for rows.Next() {
		turn, err := scanCodexCliTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, turn)
	}
	return out, rows.Err()
}

// ---- P1 review comments ----

func (s *Store) CreateCodexCliReviewComment(ctx context.Context, comment CodexCliReviewComment) (CodexCliReviewComment, error) {
	if strings.TrimSpace(comment.ID) == "" {
		id, err := ids.New("cxrev")
		if err != nil {
			return CodexCliReviewComment{}, err
		}
		comment.ID = id
	}
	if strings.TrimSpace(comment.Status) == "" {
		comment.Status = "open"
	}
	comment.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_review_comments (id, thread_id, turn_id, workspace_id, file_path, old_line, new_line, hunk_header, body, status, created_at, resolved_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		comment.ID, comment.ThreadID, comment.TurnID, comment.WorkspaceID, comment.FilePath, comment.OldLine, comment.NewLine, comment.HunkHeader, comment.Body, comment.Status, comment.CreatedAt, comment.ResolvedAt)
	if err != nil {
		return CodexCliReviewComment{}, err
	}
	return s.GetCodexCliReviewComment(ctx, comment.ID)
}

func (s *Store) GetCodexCliReviewComment(ctx context.Context, id string) (CodexCliReviewComment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, thread_id, turn_id, workspace_id, file_path, old_line, new_line, hunk_header, body, status, created_at, resolved_at FROM codex_cli_review_comments WHERE id = ?`, id)
	var comment CodexCliReviewComment
	err := row.Scan(&comment.ID, &comment.ThreadID, &comment.TurnID, &comment.WorkspaceID, &comment.FilePath, &comment.OldLine, &comment.NewLine, &comment.HunkHeader, &comment.Body, &comment.Status, &comment.CreatedAt, &comment.ResolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliReviewComment{}, ErrNotFound
	}
	return comment, err
}

func (s *Store) ListCodexCliReviewComments(ctx context.Context, threadID string) ([]CodexCliReviewComment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, workspace_id, file_path, old_line, new_line, hunk_header, body, status, created_at, resolved_at FROM codex_cli_review_comments WHERE thread_id = ? ORDER BY created_at DESC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliReviewComment{}
	for rows.Next() {
		var comment CodexCliReviewComment
		if err := rows.Scan(&comment.ID, &comment.ThreadID, &comment.TurnID, &comment.WorkspaceID, &comment.FilePath, &comment.OldLine, &comment.NewLine, &comment.HunkHeader, &comment.Body, &comment.Status, &comment.CreatedAt, &comment.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, comment)
	}
	return out, rows.Err()
}

func (s *Store) ResolveCodexCliReviewComment(ctx context.Context, id string) (CodexCliReviewComment, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_review_comments SET status = 'resolved', resolved_at = ? WHERE id = ?`, now(), id)
	if err != nil {
		return CodexCliReviewComment{}, err
	}
	return s.GetCodexCliReviewComment(ctx, id)
}

// ---- P1 command runner ----

func (s *Store) CreateCodexCliCommand(ctx context.Context, command CodexCliCommand) (CodexCliCommand, error) {
	if strings.TrimSpace(command.ID) == "" {
		id, err := ids.New("cxcmd")
		if err != nil {
			return CodexCliCommand{}, err
		}
		command.ID = id
	}
	if strings.TrimSpace(command.Status) == "" {
		command.Status = "queued"
	}
	now := now()
	command.CreatedAt = now
	command.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_commands (id, thread_id, workspace_id, command_preview, cwd_summary, status, exit_code, output_preview, error_summary, started_at, completed_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		command.ID, command.ThreadID, command.WorkspaceID, command.CommandPreview, command.CwdSummary, command.Status, command.ExitCode, command.OutputPreview, command.ErrorSummary, command.StartedAt, command.CompletedAt, command.CreatedAt, command.UpdatedAt)
	if err != nil {
		return CodexCliCommand{}, err
	}
	return s.GetCodexCliCommand(ctx, command.ID)
}

func (s *Store) GetCodexCliCommand(ctx context.Context, id string) (CodexCliCommand, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, thread_id, workspace_id, command_preview, cwd_summary, status, exit_code, output_preview, error_summary, started_at, completed_at, created_at, updated_at FROM codex_cli_commands WHERE id = ?`, id)
	return scanCodexCliCommand(row)
}

func (s *Store) ListCodexCliCommands(ctx context.Context, threadID string) ([]CodexCliCommand, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, workspace_id, command_preview, cwd_summary, status, exit_code, output_preview, error_summary, started_at, completed_at, created_at, updated_at FROM codex_cli_commands WHERE thread_id = ? ORDER BY created_at DESC LIMIT 50`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliCommand{}
	for rows.Next() {
		command, err := scanCodexCliCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, command)
	}
	return out, rows.Err()
}

// StartCodexCliCommand transitions a command from queued to running atomically.
// The returned bool reports whether the row actually transitioned; if it is
// false the command was already cancelled or finished (for example by an
// interrupt that landed before the runner started) and the caller must not run
// it. This closes the race where a queued command is cancelled but the runner
// would otherwise force it back to running.
func (s *Store) StartCodexCliCommand(ctx context.Context, id string) (CodexCliCommand, bool, error) {
	now := now()
	res, err := s.db.ExecContext(ctx, `UPDATE codex_cli_commands SET status = 'running', started_at = ?, updated_at = ? WHERE id = ? AND status = 'queued'`, now, now, id)
	if err != nil {
		return CodexCliCommand{}, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CodexCliCommand{}, false, err
	}
	command, err := s.GetCodexCliCommand(ctx, id)
	if err != nil {
		return CodexCliCommand{}, false, err
	}
	return command, affected > 0, nil
}

func (s *Store) FinishCodexCliCommand(ctx context.Context, id, status string, exitCode int, outputPreview, errorSummary string) (CodexCliCommand, error) {
	now := now()
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_commands SET status = ?, exit_code = ?, output_preview = ?, error_summary = ?, completed_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued', 'running')`, status, exitCode, outputPreview, errorSummary, now, now, id)
	if err != nil {
		return CodexCliCommand{}, err
	}
	return s.GetCodexCliCommand(ctx, id)
}

func scanCodexCliCommand(row workspaceScanner) (CodexCliCommand, error) {
	var command CodexCliCommand
	err := row.Scan(&command.ID, &command.ThreadID, &command.WorkspaceID, &command.CommandPreview, &command.CwdSummary, &command.Status, &command.ExitCode, &command.OutputPreview, &command.ErrorSummary, &command.StartedAt, &command.CompletedAt, &command.CreatedAt, &command.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliCommand{}, ErrNotFound
	}
	return command, err
}

// ---- P1 preview browser ----

func (s *Store) CreateCodexCliBrowserSession(ctx context.Context, session CodexCliBrowserSession) (CodexCliBrowserSession, error) {
	if strings.TrimSpace(session.ID) == "" {
		id, err := ids.New("cxbws")
		if err != nil {
			return CodexCliBrowserSession{}, err
		}
		session.ID = id
	}
	if strings.TrimSpace(session.Status) == "" {
		session.Status = "open"
	}
	now := now()
	session.CreatedAt = now
	session.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_browser_sessions (id, thread_id, workspace_id, url, status, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.ThreadID, session.WorkspaceID, session.URL, session.Status, session.LastError, session.CreatedAt, session.UpdatedAt)
	if err != nil {
		return CodexCliBrowserSession{}, err
	}
	return s.GetCodexCliBrowserSession(ctx, session.ID)
}

func (s *Store) GetCodexCliBrowserSession(ctx context.Context, id string) (CodexCliBrowserSession, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, thread_id, workspace_id, url, status, last_error, created_at, updated_at FROM codex_cli_browser_sessions WHERE id = ?`, id)
	var session CodexCliBrowserSession
	err := row.Scan(&session.ID, &session.ThreadID, &session.WorkspaceID, &session.URL, &session.Status, &session.LastError, &session.CreatedAt, &session.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliBrowserSession{}, ErrNotFound
	}
	return session, err
}

func (s *Store) ListCodexCliBrowserSessions(ctx context.Context, threadID string) ([]CodexCliBrowserSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, workspace_id, url, status, last_error, created_at, updated_at FROM codex_cli_browser_sessions WHERE thread_id = ? ORDER BY created_at DESC LIMIT 20`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliBrowserSession{}
	for rows.Next() {
		var session CodexCliBrowserSession
		if err := rows.Scan(&session.ID, &session.ThreadID, &session.WorkspaceID, &session.URL, &session.Status, &session.LastError, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCodexCliBrowserSession(ctx context.Context, session CodexCliBrowserSession) (CodexCliBrowserSession, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_browser_sessions SET url = ?, status = ?, last_error = ?, updated_at = ? WHERE id = ?`, session.URL, session.Status, session.LastError, now(), session.ID)
	if err != nil {
		return CodexCliBrowserSession{}, err
	}
	return s.GetCodexCliBrowserSession(ctx, session.ID)
}

func (s *Store) DeleteCodexCliBrowserSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_browser_sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- P2 automations ----

func (s *Store) CreateCodexCliAutomation(ctx context.Context, automation CodexCliAutomation) (CodexCliAutomation, error) {
	if strings.TrimSpace(automation.ID) == "" {
		id, err := ids.New("cxauto")
		if err != nil {
			return CodexCliAutomation{}, err
		}
		automation.ID = id
	}
	automation = normalizeCodexCliAutomation(automation)
	now := now()
	automation.CreatedAt = now
	automation.UpdatedAt = now
	schedule, _ := json.Marshal(automation.Schedule)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_automations (id, kind, thread_id, workspace_id, title, prompt_summary, schedule_json, enabled, default_sandbox, default_approval_policy, last_run_at, next_run_at, retry_count, failure_backoff_until, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		automation.ID, automation.Kind, automation.ThreadID, automation.WorkspaceID, automation.Title, automation.PromptSummary, string(schedule), boolInt(automation.Enabled), automation.DefaultSandbox, automation.DefaultApprovalPolicy, automation.LastRunAt, automation.NextRunAt, automation.RetryCount, automation.FailureBackoffUntil, automation.CreatedAt, automation.UpdatedAt)
	if err != nil {
		return CodexCliAutomation{}, err
	}
	return s.GetCodexCliAutomation(ctx, automation.ID)
}

func (s *Store) GetCodexCliAutomation(ctx context.Context, id string) (CodexCliAutomation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, thread_id, workspace_id, title, prompt_summary, schedule_json, enabled, default_sandbox, default_approval_policy, last_run_at, next_run_at, retry_count, failure_backoff_until, created_at, updated_at FROM codex_cli_automations WHERE id = ?`, id)
	return scanCodexCliAutomation(row)
}

func (s *Store) ListCodexCliAutomations(ctx context.Context) ([]CodexCliAutomation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, thread_id, workspace_id, title, prompt_summary, schedule_json, enabled, default_sandbox, default_approval_policy, last_run_at, next_run_at, retry_count, failure_backoff_until, created_at, updated_at FROM codex_cli_automations ORDER BY enabled DESC, next_run_at ASC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAutomation{}
	for rows.Next() {
		item, err := scanCodexCliAutomation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListDueCodexCliAutomations(ctx context.Context, nowTime string, limit int) ([]CodexCliAutomation, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, thread_id, workspace_id, title, prompt_summary, schedule_json, enabled, default_sandbox, default_approval_policy, last_run_at, next_run_at, retry_count, failure_backoff_until, created_at, updated_at FROM codex_cli_automations WHERE enabled = 1 AND next_run_at != '' AND next_run_at <= ? AND (failure_backoff_until = '' OR failure_backoff_until <= ?) ORDER BY next_run_at ASC LIMIT ?`, nowTime, nowTime, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAutomation{}
	for rows.Next() {
		item, err := scanCodexCliAutomation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCodexCliAutomation(ctx context.Context, automation CodexCliAutomation) (CodexCliAutomation, error) {
	automation = normalizeCodexCliAutomation(automation)
	schedule, _ := json.Marshal(automation.Schedule)
	_, err := s.db.ExecContext(ctx, `
UPDATE codex_cli_automations
SET kind = ?, thread_id = ?, workspace_id = ?, title = ?, prompt_summary = ?, schedule_json = ?, enabled = ?, default_sandbox = ?, default_approval_policy = ?, last_run_at = ?, next_run_at = ?, retry_count = ?, failure_backoff_until = ?, updated_at = ?
WHERE id = ?`,
		automation.Kind, automation.ThreadID, automation.WorkspaceID, automation.Title, automation.PromptSummary, string(schedule), boolInt(automation.Enabled), automation.DefaultSandbox, automation.DefaultApprovalPolicy, automation.LastRunAt, automation.NextRunAt, automation.RetryCount, automation.FailureBackoffUntil, now(), automation.ID)
	if err != nil {
		return CodexCliAutomation{}, err
	}
	return s.GetCodexCliAutomation(ctx, automation.ID)
}

func (s *Store) DeleteCodexCliAutomation(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_automation_runs WHERE automation_id = ?`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_automations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateCodexCliAutomationRun(ctx context.Context, run CodexCliAutomationRun) (CodexCliAutomationRun, error) {
	if strings.TrimSpace(run.ID) == "" {
		id, err := ids.New("cxarun")
		if err != nil {
			return CodexCliAutomationRun{}, err
		}
		run.ID = id
	}
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "queued"
	}
	if strings.TrimSpace(run.TriageState) == "" {
		run.TriageState = "open"
	}
	now := now()
	run.CreatedAt = now
	run.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_automation_runs (id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(automation_id, client_request_id) DO UPDATE SET updated_at = updated_at`,
		run.ID, run.AutomationID, run.ThreadID, run.TurnID, run.ClientRequestID, run.Status, run.StartedAt, run.LastHeartbeatAt, run.CompletedAt, run.FindingSummary, run.ErrorSummary, run.TriageState, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return CodexCliAutomationRun{}, err
	}
	if run.ClientRequestID != "" {
		return s.GetCodexCliAutomationRunByClientRequest(ctx, run.AutomationID, run.ClientRequestID)
	}
	return s.GetCodexCliAutomationRun(ctx, run.ID)
}

func (s *Store) GetCodexCliAutomationRun(ctx context.Context, id string) (CodexCliAutomationRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at FROM codex_cli_automation_runs WHERE id = ?`, id)
	return scanCodexCliAutomationRun(row)
}

func (s *Store) GetCodexCliAutomationRunByClientRequest(ctx context.Context, automationID, requestID string) (CodexCliAutomationRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at FROM codex_cli_automation_runs WHERE automation_id = ? AND client_request_id = ?`, automationID, requestID)
	return scanCodexCliAutomationRun(row)
}

func (s *Store) GetCodexCliAutomationRunByTurn(ctx context.Context, turnID string) (CodexCliAutomationRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at FROM codex_cli_automation_runs WHERE turn_id = ? ORDER BY created_at DESC LIMIT 1`, turnID)
	return scanCodexCliAutomationRun(row)
}

func (s *Store) ListCodexCliAutomationRuns(ctx context.Context, triage string) ([]CodexCliAutomationRun, error) {
	query := `SELECT id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at FROM codex_cli_automation_runs`
	args := []any{}
	if triage = strings.TrimSpace(triage); triage != "" && triage != "all" {
		query += ` WHERE triage_state = ?`
		args = append(args, triage)
	}
	query += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAutomationRun{}
	for rows.Next() {
		run, err := scanCodexCliAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) ListActiveCodexCliAutomationRuns(ctx context.Context) ([]CodexCliAutomationRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, automation_id, thread_id, turn_id, client_request_id, status, started_at, last_heartbeat_at, completed_at, finding_summary, error_summary, triage_state, created_at, updated_at FROM codex_cli_automation_runs WHERE status IN ('queued', 'running') ORDER BY created_at ASC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAutomationRun{}
	for rows.Next() {
		run, err := scanCodexCliAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCodexCliAutomationRun(ctx context.Context, run CodexCliAutomationRun) (CodexCliAutomationRun, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_automation_runs SET thread_id = ?, turn_id = ?, status = ?, started_at = ?, last_heartbeat_at = ?, completed_at = ?, finding_summary = ?, error_summary = ?, triage_state = ?, updated_at = ? WHERE id = ?`, run.ThreadID, run.TurnID, run.Status, run.StartedAt, run.LastHeartbeatAt, run.CompletedAt, run.FindingSummary, run.ErrorSummary, run.TriageState, now(), run.ID)
	if err != nil {
		return CodexCliAutomationRun{}, err
	}
	return s.GetCodexCliAutomationRun(ctx, run.ID)
}

func (s *Store) ArchiveCodexCliAutomationRun(ctx context.Context, id string) (CodexCliAutomationRun, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_automation_runs SET triage_state = 'archived', updated_at = ? WHERE id = ?`, now(), id)
	if err != nil {
		return CodexCliAutomationRun{}, err
	}
	return s.GetCodexCliAutomationRun(ctx, id)
}

func scanCodexCliAutomation(row workspaceScanner) (CodexCliAutomation, error) {
	var item CodexCliAutomation
	var schedule string
	var enabled int
	err := row.Scan(&item.ID, &item.Kind, &item.ThreadID, &item.WorkspaceID, &item.Title, &item.PromptSummary, &schedule, &enabled, &item.DefaultSandbox, &item.DefaultApprovalPolicy, &item.LastRunAt, &item.NextRunAt, &item.RetryCount, &item.FailureBackoffUntil, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliAutomation{}, ErrNotFound
	}
	if err != nil {
		return CodexCliAutomation{}, err
	}
	_ = json.Unmarshal([]byte(schedule), &item.Schedule)
	if item.Schedule == nil {
		item.Schedule = map[string]any{}
	}
	item.Enabled = enabled != 0
	return item, nil
}

func scanCodexCliAutomationRun(row workspaceScanner) (CodexCliAutomationRun, error) {
	var run CodexCliAutomationRun
	err := row.Scan(&run.ID, &run.AutomationID, &run.ThreadID, &run.TurnID, &run.ClientRequestID, &run.Status, &run.StartedAt, &run.LastHeartbeatAt, &run.CompletedAt, &run.FindingSummary, &run.ErrorSummary, &run.TriageState, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliAutomationRun{}, ErrNotFound
	}
	return run, err
}

func normalizeCodexCliAutomation(item CodexCliAutomation) CodexCliAutomation {
	item.Kind = strings.TrimSpace(item.Kind)
	if item.Kind != "project" {
		item.Kind = "thread_wakeup"
	}
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		item.Title = "Codex automation"
	}
	item.PromptSummary = strings.TrimSpace(item.PromptSummary)
	item.DefaultSandbox = strings.TrimSpace(item.DefaultSandbox)
	if item.DefaultSandbox != "read-only" {
		item.DefaultSandbox = "read-only"
	}
	item.DefaultApprovalPolicy = strings.TrimSpace(item.DefaultApprovalPolicy)
	if item.DefaultApprovalPolicy != "on-request" {
		item.DefaultApprovalPolicy = "on-request"
	}
	if item.Schedule == nil {
		item.Schedule = map[string]any{}
	}
	return item
}

// ---- P2 capability cache and notifications ----

func (s *Store) UpsertCodexCliCapabilityCache(ctx context.Context, item CodexCliCapabilityCache) (CodexCliCapabilityCache, error) {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = strings.TrimSpace(item.Kind)
	}
	if strings.TrimSpace(item.Status) == "" {
		item.Status = "unknown"
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	data, _ := json.Marshal(item.Payload)
	now := now()
	if item.ProbedAt == "" {
		item.ProbedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_capability_cache (id, kind, status, payload_json, last_error, probed_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, status = excluded.status, payload_json = excluded.payload_json, last_error = excluded.last_error, probed_at = excluded.probed_at, updated_at = excluded.updated_at`,
		item.ID, item.Kind, item.Status, string(data), item.LastError, item.ProbedAt, now)
	if err != nil {
		return CodexCliCapabilityCache{}, err
	}
	return s.GetCodexCliCapabilityCache(ctx, item.ID)
}

func (s *Store) GetCodexCliCapabilityCache(ctx context.Context, id string) (CodexCliCapabilityCache, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, status, payload_json, last_error, probed_at, updated_at FROM codex_cli_capability_cache WHERE id = ?`, id)
	var item CodexCliCapabilityCache
	var payload string
	err := row.Scan(&item.ID, &item.Kind, &item.Status, &payload, &item.LastError, &item.ProbedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliCapabilityCache{}, ErrNotFound
	}
	if err != nil {
		return CodexCliCapabilityCache{}, err
	}
	_ = json.Unmarshal([]byte(payload), &item.Payload)
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	return item, nil
}

func (s *Store) CreateCodexCliNotification(ctx context.Context, item CodexCliNotification) (CodexCliNotification, error) {
	if strings.TrimSpace(item.ID) == "" {
		id, err := ids.New("cxnot")
		if err != nil {
			return CodexCliNotification{}, err
		}
		item.ID = id
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	if strings.TrimSpace(item.Scope) == "" {
		item.Scope = "codex"
	}
	if strings.TrimSpace(item.Status) == "" {
		item.Status = "unread"
	}
	if strings.TrimSpace(item.Severity) == "" {
		item.Severity = "neutral"
	}
	data, _ := json.Marshal(item.Payload)
	now := now()
	item.CreatedAt = now
	item.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO codex_cli_notifications (id, scope, scope_id, event_type, title, summary, status, severity, payload_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Scope, item.ScopeID, item.EventType, item.Title, item.Summary, item.Status, item.Severity, string(data), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return CodexCliNotification{}, err
	}
	return s.GetCodexCliNotification(ctx, item.ID)
}

func (s *Store) GetCodexCliNotification(ctx context.Context, id string) (CodexCliNotification, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, scope, scope_id, event_type, title, summary, status, severity, payload_json, created_at, updated_at FROM codex_cli_notifications WHERE id = ?`, id)
	return scanCodexCliNotification(row)
}

func (s *Store) ListCodexCliNotifications(ctx context.Context, scope, status string) ([]CodexCliNotification, error) {
	query := `SELECT id, scope, scope_id, event_type, title, summary, status, severity, payload_json, created_at, updated_at FROM codex_cli_notifications`
	clauses := []string{}
	args := []any{}
	if scope = strings.TrimSpace(scope); scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, scope)
	}
	if status = strings.TrimSpace(status); status != "" && status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliNotification{}
	for rows.Next() {
		item, err := scanCodexCliNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCodexCliNotificationStatus(ctx context.Context, id, status string) (CodexCliNotification, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_notifications SET status = ?, updated_at = ? WHERE id = ?`, status, now(), id)
	if err != nil {
		return CodexCliNotification{}, err
	}
	return s.GetCodexCliNotification(ctx, id)
}

func (s *Store) ArchiveReadCodexCliNotifications(ctx context.Context, scope string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE codex_cli_notifications SET status = 'archived', updated_at = ? WHERE scope = ? AND status = 'read'`, now(), scope)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

func scanCodexCliNotification(row workspaceScanner) (CodexCliNotification, error) {
	var item CodexCliNotification
	var payload string
	err := row.Scan(&item.ID, &item.Scope, &item.ScopeID, &item.EventType, &item.Title, &item.Summary, &item.Status, &item.Severity, &payload, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliNotification{}, ErrNotFound
	}
	if err != nil {
		return CodexCliNotification{}, err
	}
	_ = json.Unmarshal([]byte(payload), &item.Payload)
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	return item, nil
}

func (s *Store) SaveCodexCliThread(ctx context.Context, thread CodexCliThread) (CodexCliThread, error) {
	_, err := s.db.ExecContext(ctx, `
UPDATE codex_cli_threads SET codex_thread_id = ?, title = ?, status = ?, source_mode = ?, kind = ?, background = ?, background_source = ?, model = ?, sandbox_mode = ?, approval_policy = ?, pinned = ?, archived_at = ?, last_turn_id = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		thread.CodexThreadID, thread.Title, thread.Status, thread.SourceMode, thread.Kind, boolInt(thread.Background), thread.BackgroundSource, thread.Model, thread.SandboxMode, thread.ApprovalPolicy, boolInt(thread.Pinned), thread.ArchivedAt, thread.LastTurnID, thread.LastError, now(), thread.ID)
	if err != nil {
		return CodexCliThread{}, err
	}
	return s.GetCodexCliThread(ctx, thread.ID)
}

// MarkCodexCliThreadsUnknown is invoked after a server restart so queued,
// running and approval-blocked turns are not shown as live. Pending approvals
// are preserved but marked "orphaned": owner decisions after restart still
// land in audit, but no reply is sent to the (now-dead) codex app-server.
func (s *Store) MarkCodexCliRunningThreadsInterrupted(ctx context.Context, message string) error {
	now := now()
	if _, err := s.db.ExecContext(ctx, `UPDATE codex_cli_turns SET status = 'failed', error_summary = ?, completed_at = ?, updated_at = ? WHERE status IN ('queued', 'running', 'waiting_approval')`, message, now, now); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE codex_cli_threads SET status = 'failed', last_error = ?, updated_at = ? WHERE status IN ('queued', 'running', 'needs_approval')`, message, now); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE codex_cli_approvals SET recovery_status = 'orphaned', updated_at = ? WHERE status = 'pending' AND recovery_status != 'orphaned'`, now); err != nil {
		return err
	}
	return nil
}

// ---- turns ----

const codexCliTurnColumns = `id, thread_id, codex_turn_id, status, prompt_summary, model, sandbox_mode, approval_policy, started_at, completed_at, error_summary, usage_json, created_at, updated_at`

func (s *Store) CreateCodexCliTurn(ctx context.Context, turn CodexCliTurn) (CodexCliTurn, error) {
	id, err := ids.New("cxtn")
	if err != nil {
		return CodexCliTurn{}, err
	}
	turn.ID = id
	now := now()
	turn.CreatedAt = now
	turn.UpdatedAt = now
	if turn.Status == "" {
		turn.Status = "running"
	}
	if turn.StartedAt == "" {
		turn.StartedAt = now
	}
	usage := "{}"
	if len(turn.Usage) > 0 {
		data, _ := json.Marshal(turn.Usage)
		usage = string(data)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_turns (`+codexCliTurnColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		turn.ID, turn.ThreadID, turn.CodexTurnID, turn.Status, turn.PromptSummary, turn.Model, turn.SandboxMode, turn.ApprovalPolicy, turn.StartedAt, turn.CompletedAt, turn.ErrorSummary, usage, turn.CreatedAt, turn.UpdatedAt)
	if err != nil {
		return CodexCliTurn{}, err
	}
	return s.GetCodexCliTurn(ctx, turn.ID)
}

func (s *Store) GetCodexCliTurn(ctx context.Context, id string) (CodexCliTurn, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+codexCliTurnColumns+` FROM codex_cli_turns WHERE id = ?`, id)
	turn, err := scanCodexCliTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliTurn{}, ErrNotFound
	}
	return turn, err
}

func (s *Store) ListCodexCliTurns(ctx context.Context, threadID string) ([]CodexCliTurn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+codexCliTurnColumns+` FROM codex_cli_turns WHERE thread_id = ? ORDER BY created_at ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliTurn{}
	for rows.Next() {
		turn, err := scanCodexCliTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, turn)
	}
	return out, rows.Err()
}

// ListRecentFailedCodexCliTurns returns the most recent failed turns across all
// threads, newest first, for the triage inbox.
func (s *Store) ListRecentFailedCodexCliTurns(ctx context.Context, limit int) ([]CodexCliTurn, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.`+strings.ReplaceAll(codexCliTurnColumns, ", ", ", t.")+` FROM codex_cli_turns t JOIN codex_cli_threads th ON th.id = t.thread_id WHERE t.status = 'failed' AND th.archived_at = '' ORDER BY t.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliTurn{}
	for rows.Next() {
		turn, err := scanCodexCliTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, turn)
	}
	return out, rows.Err()
}

// ListOpenCodexCliReviewComments returns unresolved review comments across all
// threads, newest first, for the triage inbox.
func (s *Store) ListOpenCodexCliReviewComments(ctx context.Context, limit int) ([]CodexCliReviewComment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.thread_id, c.turn_id, c.workspace_id, c.file_path, c.old_line, c.new_line, c.hunk_header, c.body, c.status, c.created_at, c.resolved_at FROM codex_cli_review_comments c JOIN codex_cli_threads th ON th.id = c.thread_id WHERE c.status != 'resolved' AND th.archived_at = '' ORDER BY c.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliReviewComment{}
	for rows.Next() {
		var comment CodexCliReviewComment
		if err := rows.Scan(&comment.ID, &comment.ThreadID, &comment.TurnID, &comment.WorkspaceID, &comment.FilePath, &comment.OldLine, &comment.NewLine, &comment.HunkHeader, &comment.Body, &comment.Status, &comment.CreatedAt, &comment.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, comment)
	}
	return out, rows.Err()
}

func (s *Store) SaveCodexCliTurn(ctx context.Context, turn CodexCliTurn) (CodexCliTurn, error) {
	usage := "{}"
	if len(turn.Usage) > 0 {
		data, _ := json.Marshal(turn.Usage)
		usage = string(data)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE codex_cli_turns SET codex_turn_id = ?, status = ?, prompt_summary = ?, model = ?, sandbox_mode = ?, approval_policy = ?, started_at = ?, completed_at = ?, error_summary = ?, usage_json = ?, updated_at = ? WHERE id = ?`,
		turn.CodexTurnID, turn.Status, turn.PromptSummary, turn.Model, turn.SandboxMode, turn.ApprovalPolicy, turn.StartedAt, turn.CompletedAt, turn.ErrorSummary, usage, now(), turn.ID)
	if err != nil {
		return CodexCliTurn{}, err
	}
	return s.GetCodexCliTurn(ctx, turn.ID)
}

// ---- events ----

func (s *Store) AppendCodexCliEvent(ctx context.Context, event CodexCliEvent) (CodexCliEvent, error) {
	id, err := ids.New("cxev")
	if err != nil {
		return CodexCliEvent{}, err
	}
	event.ID = id
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM codex_cli_events WHERE thread_id = ?`, event.ThreadID).Scan(&seq); err != nil {
		return CodexCliEvent{}, err
	}
	event.Sequence = seq
	event.CreatedAt = now()
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	payload, _ := json.Marshal(event.Payload)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_events (id, thread_id, turn_id, sequence, event_type, codex_method, item_type, payload_json, text_preview, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.ThreadID, event.TurnID, event.Sequence, event.EventType, event.CodexMethod, event.ItemType, string(payload), event.TextPreview, event.CreatedAt)
	if err != nil {
		return CodexCliEvent{}, err
	}
	return event, nil
}

func (s *Store) ListCodexCliEvents(ctx context.Context, threadID string, after int64, limit int) ([]CodexCliEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, sequence, event_type, codex_method, item_type, payload_json, text_preview, created_at FROM codex_cli_events WHERE thread_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, threadID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliEvent{}
	for rows.Next() {
		var event CodexCliEvent
		var payload string
		if err := rows.Scan(&event.ID, &event.ThreadID, &event.TurnID, &event.Sequence, &event.EventType, &event.CodexMethod, &event.ItemType, &payload, &event.TextPreview, &event.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &event.Payload)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) ListRecentCodexCliEventsForTurn(ctx context.Context, threadID, turnID string, limit int) ([]CodexCliEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, sequence, event_type, codex_method, item_type, payload_json, text_preview, created_at FROM codex_cli_events WHERE thread_id = ? AND turn_id = ? ORDER BY sequence DESC LIMIT ?`, threadID, turnID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliEvent{}
	for rows.Next() {
		var event CodexCliEvent
		var payload string
		if err := rows.Scan(&event.ID, &event.ThreadID, &event.TurnID, &event.Sequence, &event.EventType, &event.CodexMethod, &event.ItemType, &payload, &event.TextPreview, &event.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &event.Payload)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) PruneCodexCliEvents(ctx context.Context, threadID string, maxEvents int) error {
	if maxEvents <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM codex_cli_events
WHERE thread_id = ? AND id IN (
  SELECT id FROM codex_cli_events WHERE thread_id = ?
  ORDER BY sequence DESC LIMIT -1 OFFSET ?
)`, threadID, threadID, maxEvents)
	return err
}

// DeleteCodexCliEventsOlderThan removes events created strictly before the given
// RFC3339 cutoff across all threads. Returns the number of rows removed.
func (s *Store) DeleteCodexCliEventsOlderThan(ctx context.Context, cutoff string) (int64, error) {
	if strings.TrimSpace(cutoff) == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_events WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

// ---- approvals ----

const codexCliApprovalColumns = `id, thread_id, turn_id, codex_request_id, status, action_kind, command_preview, cwd_summary, risk_level, request_payload_json, decision, decided_at, expires_at, created_at, updated_at, jsonrpc_request_id_json, recovery_status`

func (s *Store) CreateCodexCliApproval(ctx context.Context, approval CodexCliApproval) (CodexCliApproval, error) {
	id, err := ids.New("cxap")
	if err != nil {
		return CodexCliApproval{}, err
	}
	approval.ID = id
	now := now()
	approval.CreatedAt = now
	approval.UpdatedAt = now
	if approval.Status == "" {
		approval.Status = "pending"
	}
	if approval.RiskLevel == "" {
		approval.RiskLevel = "medium"
	}
	if approval.RequestPayload == nil {
		approval.RequestPayload = map[string]any{}
	}
	if approval.RecoveryStatus == "" {
		approval.RecoveryStatus = "live"
	}
	payload, _ := json.Marshal(approval.RequestPayload)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_approvals (`+codexCliApprovalColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approval.ID, approval.ThreadID, approval.TurnID, approval.CodexRequestID, approval.Status, approval.ActionKind, approval.CommandPreview, approval.CwdSummary, approval.RiskLevel, string(payload), approval.Decision, approval.DecidedAt, approval.ExpiresAt, approval.CreatedAt, approval.UpdatedAt, approval.JSONRPCRequestID, approval.RecoveryStatus)
	if err != nil {
		return CodexCliApproval{}, err
	}
	return s.GetCodexCliApproval(ctx, approval.ID)
}

func (s *Store) GetCodexCliApproval(ctx context.Context, id string) (CodexCliApproval, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+codexCliApprovalColumns+` FROM codex_cli_approvals WHERE id = ?`, id)
	approval, err := scanCodexCliApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliApproval{}, ErrNotFound
	}
	return approval, err
}

func (s *Store) ListCodexCliApprovals(ctx context.Context, status, threadID string) ([]CodexCliApproval, error) {
	query := `SELECT ` + codexCliApprovalColumns + ` FROM codex_cli_approvals`
	clauses := []string{}
	args := []any{}
	if status = strings.TrimSpace(status); status != "" && status != "all" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		clauses = append(clauses, "thread_id = ?")
		args = append(args, threadID)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliApproval{}
	for rows.Next() {
		approval, err := scanCodexCliApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, approval)
	}
	return out, rows.Err()
}

func (s *Store) ResolveCodexCliApproval(ctx context.Context, id, status, decision string) (CodexCliApproval, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_approvals SET status = ?, decision = ?, decided_at = ?, updated_at = ? WHERE id = ? AND status = 'pending'`, status, decision, now(), now(), id)
	if err != nil {
		return CodexCliApproval{}, err
	}
	return s.GetCodexCliApproval(ctx, id)
}

func (s *Store) ExpireCodexCliApprovals(ctx context.Context, nowTime string) ([]CodexCliApproval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+codexCliApprovalColumns+` FROM codex_cli_approvals WHERE status = 'pending' AND expires_at != '' AND expires_at < ?`, nowTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	expired := []CodexCliApproval{}
	for rows.Next() {
		approval, err := scanCodexCliApproval(rows)
		if err != nil {
			return nil, err
		}
		expired = append(expired, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(expired) == 0 {
		return expired, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE codex_cli_approvals SET status = 'failed', decision = 'expired', decided_at = ?, updated_at = ? WHERE status = 'pending' AND expires_at != '' AND expires_at < ?`, nowTime, nowTime, nowTime)
	return expired, err
}

// ---- runs ----

func (s *Store) CreateCodexCliRun(ctx context.Context, run CodexCliRun) (CodexCliRun, error) {
	id, err := ids.New("cxrun")
	if err != nil {
		return CodexCliRun{}, err
	}
	run.ID = id
	now := now()
	if run.StartedAt == "" {
		run.StartedAt = now
	}
	if run.Status == "" {
		run.Status = "running"
	}
	if run.Mode == "" {
		run.Mode = "exec"
	}
	run.LastHeartbeatAt = now
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_runs (id, thread_id, turn_id, mode, pid, status, started_at, last_heartbeat_at, exited_at, exit_code, error_summary)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ThreadID, run.TurnID, run.Mode, run.PID, run.Status, run.StartedAt, run.LastHeartbeatAt, run.ExitedAt, run.ExitCode, run.ErrorSummary)
	if err != nil {
		return CodexCliRun{}, err
	}
	return run, nil
}

func (s *Store) FinishCodexCliRun(ctx context.Context, id, status string, exitCode int, errorSummary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE codex_cli_runs SET status = ?, exit_code = ?, error_summary = ?, exited_at = ? WHERE id = ?`, status, exitCode, errorSummary, now(), id)
	return err
}

func (s *Store) MarkCodexCliOrphanRuns(ctx context.Context, message string) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE codex_cli_runs SET status = 'exited', error_summary = ?, exited_at = ? WHERE status = 'running'`, message, now())
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// ---- attachments ----

func (s *Store) CreateCodexCliAttachment(ctx context.Context, att CodexCliAttachment) (CodexCliAttachment, error) {
	id, err := ids.New("cxatt")
	if err != nil {
		return CodexCliAttachment{}, err
	}
	att.ID = id
	att.CreatedAt = now()
	if att.Kind == "" {
		att.Kind = "image"
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO codex_cli_attachments (id, thread_id, turn_id, kind, filename, content_type, size_bytes, storage_path, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		att.ID, att.ThreadID, att.TurnID, att.Kind, att.Filename, att.ContentType, att.SizeBytes, att.StoragePath, att.ExpiresAt, att.CreatedAt)
	if err != nil {
		return CodexCliAttachment{}, err
	}
	return att, nil
}

func (s *Store) GetCodexCliAttachment(ctx context.Context, id string) (CodexCliAttachment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, thread_id, turn_id, kind, filename, content_type, size_bytes, storage_path, expires_at, created_at FROM codex_cli_attachments WHERE id = ?`, id)
	var att CodexCliAttachment
	err := row.Scan(&att.ID, &att.ThreadID, &att.TurnID, &att.Kind, &att.Filename, &att.ContentType, &att.SizeBytes, &att.StoragePath, &att.ExpiresAt, &att.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexCliAttachment{}, ErrNotFound
	}
	return att, err
}

func (s *Store) DeleteCodexCliAttachment(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM codex_cli_attachments WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AssignCodexCliAttachmentsToTurn(ctx context.Context, threadID, turnID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `UPDATE codex_cli_attachments SET turn_id = ? WHERE id = ? AND thread_id = ?`, turnID, id, threadID)
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}

func (s *Store) ListCodexCliAttachmentsForTurn(ctx context.Context, turnID string) ([]CodexCliAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, kind, filename, content_type, size_bytes, storage_path, expires_at, created_at FROM codex_cli_attachments WHERE turn_id = ?`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAttachment{}
	for rows.Next() {
		var att CodexCliAttachment
		if err := rows.Scan(&att.ID, &att.ThreadID, &att.TurnID, &att.Kind, &att.Filename, &att.ContentType, &att.SizeBytes, &att.StoragePath, &att.ExpiresAt, &att.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveCodexCliAttachments(ctx context.Context, threadID string, nowTime string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM codex_cli_attachments WHERE thread_id = ? AND (expires_at = '' OR expires_at >= ?)`, threadID, nowTime).Scan(&count)
	return count, err
}

func (s *Store) ListExpiredCodexCliAttachments(ctx context.Context, nowTime string) ([]CodexCliAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, thread_id, turn_id, kind, filename, content_type, size_bytes, storage_path, expires_at, created_at FROM codex_cli_attachments WHERE expires_at != '' AND expires_at < ?`, nowTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CodexCliAttachment{}
	for rows.Next() {
		var att CodexCliAttachment
		if err := rows.Scan(&att.ID, &att.ThreadID, &att.TurnID, &att.Kind, &att.Filename, &att.ContentType, &att.SizeBytes, &att.StoragePath, &att.ExpiresAt, &att.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, rows.Err()
}

// ---- scanners and helpers ----

func scanCodexCliWorkspace(row workspaceScanner) (CodexCliWorkspace, error) {
	var ws CodexCliWorkspace
	var net string
	var pinned int
	err := row.Scan(&ws.ID, &ws.Label, &ws.Path, &ws.PathSummary, &ws.TrustState, &ws.DefaultModel, &ws.DefaultSandbox, &ws.DefaultApprovalPolicy, &net, &pinned, &ws.LastOpenedAt, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		return CodexCliWorkspace{}, err
	}
	ws.Pinned = pinned == 1
	_ = json.Unmarshal([]byte(net), &ws.NetworkPolicy)
	if ws.NetworkPolicy == nil {
		ws.NetworkPolicy = map[string]any{"enabled": false}
	}
	ws = NormalizeCodexCliWorkspace(ws)
	return ws, nil
}

func scanCodexCliThread(row workspaceScanner) (CodexCliThread, error) {
	var thread CodexCliThread
	var pinned int
	var background int
	err := row.Scan(&thread.ID, &thread.CodexThreadID, &thread.WorkspaceID, &thread.Title, &thread.Status, &thread.SourceMode, &thread.Kind, &background, &thread.BackgroundSource, &thread.Model, &thread.SandboxMode, &thread.ApprovalPolicy, &pinned, &thread.ArchivedAt, &thread.LastTurnID, &thread.LastError, &thread.CreatedAt, &thread.UpdatedAt)
	if err != nil {
		return CodexCliThread{}, err
	}
	thread.Background = background == 1
	thread.Pinned = pinned == 1
	return thread, nil
}

func scanCodexCliTurn(row workspaceScanner) (CodexCliTurn, error) {
	var turn CodexCliTurn
	var usage string
	err := row.Scan(&turn.ID, &turn.ThreadID, &turn.CodexTurnID, &turn.Status, &turn.PromptSummary, &turn.Model, &turn.SandboxMode, &turn.ApprovalPolicy, &turn.StartedAt, &turn.CompletedAt, &turn.ErrorSummary, &usage, &turn.CreatedAt, &turn.UpdatedAt)
	if err != nil {
		return CodexCliTurn{}, err
	}
	_ = json.Unmarshal([]byte(usage), &turn.Usage)
	return turn, nil
}

func scanCodexCliApproval(row workspaceScanner) (CodexCliApproval, error) {
	var approval CodexCliApproval
	var payload string
	var recoveryStatus sql.NullString
	err := row.Scan(&approval.ID, &approval.ThreadID, &approval.TurnID, &approval.CodexRequestID, &approval.Status, &approval.ActionKind, &approval.CommandPreview, &approval.CwdSummary, &approval.RiskLevel, &payload, &approval.Decision, &approval.DecidedAt, &approval.ExpiresAt, &approval.CreatedAt, &approval.UpdatedAt, &approval.JSONRPCRequestID, &recoveryStatus)
	if err != nil {
		return CodexCliApproval{}, err
	}
	if recoveryStatus.Valid && recoveryStatus.String != "" {
		approval.RecoveryStatus = recoveryStatus.String
	} else {
		approval.RecoveryStatus = "live"
	}
	_ = json.Unmarshal([]byte(payload), &approval.RequestPayload)
	return approval, nil
}

func workspaceLabelFromPath(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
	if path == "" {
		return "workspace"
	}
	if index := strings.LastIndex(path, "/"); index >= 0 && index < len(path)-1 {
		return path[index+1:]
	}
	return path
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func summarizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	const maxRunes = 60
	runes := []rune(path)
	if len(runes) <= maxRunes {
		return path
	}
	base := workspaceLabelFromPath(path)
	return ".../" + base
}
