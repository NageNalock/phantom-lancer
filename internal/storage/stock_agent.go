package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
)

const (
	stockAgentFieldLimit                     = 64 << 10
	stockAgentGraphLimit                     = 256 << 10
	StockAgentEstimatedCostPerThousandTokens = 0.002
)

var (
	stockAgentBearerSecretPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{10,}`)
	stockAgentKVSecretPattern     = regexp.MustCompile(`(?i)((?:api[_-]?key|token|session|secret|password)=)[^&\s]+`)
	stockAgentJWTSecretPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
)

type StockAgentModelProfile struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	TaskType         string  `json:"taskType"`
	DecisionProtocol string  `json:"decisionProtocol"`
	AuthMode         string  `json:"authMode"`
	Enabled          bool    `json:"enabled"`
	Temperature      float64 `json:"temperature"`
	DailyTokenBudget int     `json:"dailyTokenBudget"`
	DailyCostBudget  float64 `json:"dailyCostBudget"`
	Status           string  `json:"status"`
	LastUsedAt       string  `json:"lastUsedAt,omitempty"`
	FailureSummary   string  `json:"failureSummary,omitempty"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type StockAgentRun struct {
	ID                string `json:"id"`
	TriggerSource     string `json:"triggerSource"`
	TriggerObjectType string `json:"triggerObjectType"`
	TriggerObjectID   string `json:"triggerObjectId"`
	StrategyID        string `json:"strategyId,omitempty"`
	PortfolioID       string `json:"portfolioId,omitempty"`
	WatchID           string `json:"watchId,omitempty"`
	AlertID           string `json:"alertId,omitempty"`
	ReviewID          string `json:"reviewId,omitempty"`
	Symbol            string `json:"symbol,omitempty"`
	DecisionProtocol  string `json:"decisionProtocol"`
	Status            string `json:"status"`
	Result            string `json:"result"`
	Confidence        string `json:"confidence"`
	ModelProfileID    string `json:"modelProfileId,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Model             string `json:"model,omitempty"`
	PromptSnapshot    string `json:"promptSnapshot,omitempty"`
	InputSnapshot     string `json:"inputSnapshot,omitempty"`
	OutputSnapshot    string `json:"outputSnapshot,omitempty"`
	RunGraphJSON      string `json:"runGraphJson"`
	SkillSnapshotJSON string `json:"skillSnapshotJson"`
	ToolSnapshotJSON  string `json:"toolSnapshotJson"`
	CostSummaryJSON   string `json:"costSummaryJson"`
	Summary           string `json:"summary"`
	RedactionSummary  string `json:"redactionSummary,omitempty"`
	StartedAt         string `json:"startedAt,omitempty"`
	CompletedAt       string `json:"completedAt,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type StockAgentAuthorization struct {
	ID               string `json:"id"`
	RunID            string `json:"runId"`
	ReviewID         string `json:"reviewId,omitempty"`
	ProfileID        string `json:"profileId"`
	TaskType         string `json:"taskType"`
	DecisionProtocol string `json:"decisionProtocol"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Symbol           string `json:"symbol,omitempty"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	PromptSnapshot   string `json:"promptSnapshot,omitempty"`
	InputSnapshot    string `json:"inputSnapshot,omitempty"`
	OutputSnapshot   string `json:"outputSnapshot,omitempty"`
	RequestedBy      string `json:"requestedBy"`
	Decision         string `json:"decision,omitempty"`
	ErrorSummary     string `json:"errorSummary,omitempty"`
	CreatedAt        string `json:"createdAt"`
	DecidedAt        string `json:"decidedAt,omitempty"`
	CompletedAt      string `json:"completedAt,omitempty"`
	UpdatedAt        string `json:"updatedAt"`
}

type StockAgentRunStep struct {
	ID            string `json:"id"`
	RunID         string `json:"runId"`
	StepKey       string `json:"stepKey"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	InputJSON     string `json:"inputJson"`
	OutputJSON    string `json:"outputJson"`
	ToolCallsJSON string `json:"toolCallsJson"`
	LatencyMs     int    `json:"latencyMs"`
	TokenEstimate int    `json:"tokenEstimate"`
	Summary       string `json:"summary"`
	StartedAt     string `json:"startedAt,omitempty"`
	CompletedAt   string `json:"completedAt,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

type StockAgentClaim struct {
	ID                 string `json:"id"`
	RunID              string `json:"runId"`
	StepID             string `json:"stepId,omitempty"`
	ClaimType          string `json:"claimType"`
	Text               string `json:"text"`
	EvidenceJSON       string `json:"evidenceJson"`
	VerificationStatus string `json:"verificationStatus"`
	Confidence         string `json:"confidence"`
	SourceRef          string `json:"sourceRef,omitempty"`
	CreatedAt          string `json:"createdAt"`
}

type StockStrategyPatch struct {
	ID         string `json:"id"`
	RunID      string `json:"runId"`
	ReviewID   string `json:"reviewId,omitempty"`
	StrategyID string `json:"strategyId"`
	PatchJSON  string `json:"patchJson"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	AcceptedAt string `json:"acceptedAt,omitempty"`
}

type StockAgentTraceSummary struct {
	RunCount          int    `json:"runCount"`
	CompletedRunCount int    `json:"completedRunCount"`
	FailedRunCount    int    `json:"failedRunCount"`
	PendingPatchCount int    `json:"pendingPatchCount"`
	ClaimCount        int    `json:"claimCount"`
	LastRunAt         string `json:"lastRunAt,omitempty"`
}

type StockAgentLedgerCleanupResult struct {
	RetentionDays         int    `json:"retentionDays"`
	KeepRuns              int    `json:"keepRuns"`
	Cutoff                string `json:"cutoff"`
	RunsDeleted           int64  `json:"runsDeleted"`
	StepsDeleted          int64  `json:"stepsDeleted"`
	ClaimsDeleted         int64  `json:"claimsDeleted"`
	AuthorizationsDeleted int64  `json:"authorizationsDeleted"`
}

func (s *Store) migrateStockAgent(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS stock_agent_model_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'codex_cli',
  model TEXT NOT NULL DEFAULT 'default',
  task_type TEXT NOT NULL DEFAULT 'review',
  decision_protocol TEXT NOT NULL DEFAULT 'single_review',
  auth_mode TEXT NOT NULL DEFAULT 'user_config',
  enabled INTEGER NOT NULL DEFAULT 1,
  temperature REAL NOT NULL DEFAULT 0.2,
  daily_token_budget INTEGER NOT NULL DEFAULT 0,
  daily_cost_budget REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'available',
  last_used_at TEXT NOT NULL DEFAULT '',
  failure_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(provider, model, task_type, decision_protocol)
);
CREATE INDEX IF NOT EXISTS idx_stock_agent_profiles_task ON stock_agent_model_profiles(task_type, enabled);
CREATE TABLE IF NOT EXISTS stock_agent_runs (
  id TEXT PRIMARY KEY,
  trigger_source TEXT NOT NULL DEFAULT '',
  trigger_object_type TEXT NOT NULL DEFAULT '',
  trigger_object_id TEXT NOT NULL DEFAULT '',
  strategy_id TEXT NOT NULL DEFAULT '',
  portfolio_id TEXT NOT NULL DEFAULT '',
  watch_id TEXT NOT NULL DEFAULT '',
  alert_id TEXT NOT NULL DEFAULT '',
  review_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  decision_protocol TEXT NOT NULL DEFAULT 'single_review',
  status TEXT NOT NULL DEFAULT 'completed',
  result TEXT NOT NULL DEFAULT '',
  confidence TEXT NOT NULL DEFAULT '',
  model_profile_id TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  prompt_snapshot TEXT NOT NULL DEFAULT '',
  input_snapshot TEXT NOT NULL DEFAULT '',
  output_snapshot TEXT NOT NULL DEFAULT '',
  run_graph_json TEXT NOT NULL DEFAULT '{}',
  skill_snapshot_json TEXT NOT NULL DEFAULT '{}',
  tool_snapshot_json TEXT NOT NULL DEFAULT '{}',
  cost_summary_json TEXT NOT NULL DEFAULT '{}',
  summary TEXT NOT NULL DEFAULT '',
  redaction_summary TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_agent_runs_created ON stock_agent_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_agent_runs_review ON stock_agent_runs(review_id);
CREATE INDEX IF NOT EXISTS idx_stock_agent_runs_strategy ON stock_agent_runs(strategy_id, created_at DESC);
CREATE TABLE IF NOT EXISTS stock_agent_authorizations (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  review_id TEXT NOT NULL DEFAULT '',
  profile_id TEXT NOT NULL DEFAULT '',
  task_type TEXT NOT NULL DEFAULT 'review',
  decision_protocol TEXT NOT NULL DEFAULT 'single_review',
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  reason TEXT NOT NULL DEFAULT '',
  prompt_snapshot TEXT NOT NULL DEFAULT '',
  input_snapshot TEXT NOT NULL DEFAULT '',
  output_snapshot TEXT NOT NULL DEFAULT '',
  requested_by TEXT NOT NULL DEFAULT 'system',
  decision TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  decided_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_agent_authorizations_status ON stock_agent_authorizations(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_agent_authorizations_run ON stock_agent_authorizations(run_id);
CREATE TABLE IF NOT EXISTS stock_agent_run_steps (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  step_key TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'completed',
  input_json TEXT NOT NULL DEFAULT '{}',
  output_json TEXT NOT NULL DEFAULT '{}',
  tool_calls_json TEXT NOT NULL DEFAULT '[]',
  latency_ms INTEGER NOT NULL DEFAULT 0,
  token_estimate INTEGER NOT NULL DEFAULT 0,
  summary TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_agent_steps_run ON stock_agent_run_steps(run_id, created_at);
CREATE TABLE IF NOT EXISTS stock_agent_claims (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  step_id TEXT NOT NULL DEFAULT '',
  claim_type TEXT NOT NULL DEFAULT 'observation',
  text TEXT NOT NULL,
  evidence_json TEXT NOT NULL DEFAULT '[]',
  verification_status TEXT NOT NULL DEFAULT 'unverified',
  confidence TEXT NOT NULL DEFAULT 'medium',
  source_ref TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_agent_claims_run ON stock_agent_claims(run_id, created_at);
CREATE TABLE IF NOT EXISTS stock_strategy_patches (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL DEFAULT '',
  review_id TEXT NOT NULL DEFAULT '',
  strategy_id TEXT NOT NULL,
  patch_json TEXT NOT NULL DEFAULT '{}',
  summary TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending_acceptance',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  accepted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_stock_strategy_patches_status ON stock_strategy_patches(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_strategy_patches_strategy ON stock_strategy_patches(strategy_id, created_at DESC);
`)
	if err != nil {
		return err
	}
	return s.seedStockAgentProfiles(ctx)
}

func (s *Store) seedStockAgentProfiles(ctx context.Context) error {
	defaults := []StockAgentModelProfile{
		{
			Name:             "System Rule Trace",
			Provider:         "system",
			Model:            "rule-engine",
			TaskType:         "review",
			DecisionProtocol: "single_review",
			AuthMode:         "none",
			Enabled:          true,
			Temperature:      0,
			Status:           "available",
		},
		{
			Name:             "System Challenge Trace",
			Provider:         "system",
			Model:            "rule-engine",
			TaskType:         "review",
			DecisionProtocol: "analysis_with_challenge",
			AuthMode:         "none",
			Enabled:          true,
			Temperature:      0,
			Status:           "available",
		},
		{
			Name:             "System Portfolio Debate Trace",
			Provider:         "system",
			Model:            "rule-engine",
			TaskType:         "debate",
			DecisionProtocol: "portfolio_constrained_debate",
			AuthMode:         "none",
			Enabled:          true,
			Temperature:      0,
			Status:           "available",
		},
		{
			Name:             "Codex CLI Review",
			Provider:         "codex_cli",
			Model:            "default",
			TaskType:         "review",
			DecisionProtocol: "analysis_with_challenge",
			AuthMode:         "user_config",
			Enabled:          false,
			Temperature:      0.2,
			Status:           "disabled",
		},
		{
			Name:             "Codex CLI Debate",
			Provider:         "codex_cli",
			Model:            "default",
			TaskType:         "debate",
			DecisionProtocol: "portfolio_constrained_debate",
			AuthMode:         "confirm_required",
			Enabled:          false,
			Temperature:      0.2,
			Status:           "disabled",
		},
	}
	for _, profile := range defaults {
		existing, err := s.getStockAgentModelProfileByKey(ctx, profile.Provider, profile.Model, profile.TaskType, profile.DecisionProtocol)
		if err == nil && existing.ID != "" {
			continue
		}
		if err != nil && err != ErrNotFound {
			return err
		}
		if _, err := s.UpsertStockAgentModelProfile(ctx, profile); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertStockAgentModelProfile(ctx context.Context, profile StockAgentModelProfile) (StockAgentModelProfile, error) {
	profile.Provider = normalizeStockSource(profile.Provider)
	if profile.Provider == "" {
		profile.Provider = "codex_cli"
	}
	profile.Model = strings.TrimSpace(profile.Model)
	if profile.Model == "" {
		profile.Model = "default"
	}
	profile.TaskType = defaultString(profile.TaskType, "review")
	profile.DecisionProtocol = defaultString(profile.DecisionProtocol, "single_review")
	profile.AuthMode = defaultString(profile.AuthMode, "user_config")
	if profile.Provider == "system" && profile.AuthMode != "none" {
		return StockAgentModelProfile{}, errors.New("system profile auth mode must be none")
	}
	if profile.Provider != "system" && profile.Provider != "codex_cli" && profile.Enabled {
		return StockAgentModelProfile{}, errors.New("unsupported stock agent executor provider")
	}
	if profile.AuthMode == "disabled" {
		profile.Enabled = false
	}
	profile.Status = defaultString(profile.Status, "available")
	if !profile.Enabled && profile.Status == "available" {
		profile.Status = "disabled"
	}
	profile.Name = defaultString(profile.Name, fmt.Sprintf("%s %s", profile.Provider, profile.Model))
	if profile.Temperature < 0 {
		profile.Temperature = 0
	}
	ts := now()
	existing, err := s.getStockAgentModelProfileByKey(ctx, profile.Provider, profile.Model, profile.TaskType, profile.DecisionProtocol)
	if err == nil {
		profile.ID = existing.ID
		profile.CreatedAt = existing.CreatedAt
		profile.UpdatedAt = ts
		if profile.LastUsedAt == "" {
			profile.LastUsedAt = existing.LastUsedAt
		}
		if profile.FailureSummary == "" {
			profile.FailureSummary = existing.FailureSummary
		}
	} else if err == ErrNotFound {
		id, err := ids.New("stmp")
		if err != nil {
			return StockAgentModelProfile{}, err
		}
		profile.ID = id
		profile.CreatedAt = ts
		profile.UpdatedAt = ts
	} else {
		return StockAgentModelProfile{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_agent_model_profiles (id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, model, task_type, decision_protocol) DO UPDATE SET name = excluded.name, auth_mode = excluded.auth_mode, enabled = excluded.enabled, temperature = excluded.temperature, daily_token_budget = excluded.daily_token_budget, daily_cost_budget = excluded.daily_cost_budget, status = excluded.status, last_used_at = excluded.last_used_at, failure_summary = excluded.failure_summary, updated_at = excluded.updated_at`,
		profile.ID, profile.Name, profile.Provider, profile.Model, profile.TaskType, profile.DecisionProtocol, profile.AuthMode, boolInt(profile.Enabled), profile.Temperature, profile.DailyTokenBudget, profile.DailyCostBudget, profile.Status, profile.LastUsedAt, profile.FailureSummary, profile.CreatedAt, profile.UpdatedAt)
	if err != nil {
		return StockAgentModelProfile{}, err
	}
	return profile, nil
}

func (s *Store) getStockAgentModelProfileByKey(ctx context.Context, provider, model, taskType, protocol string) (StockAgentModelProfile, error) {
	item, err := scanStockAgentModelProfile(s.db.QueryRowContext(ctx, `SELECT id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at FROM stock_agent_model_profiles WHERE provider = ? AND model = ? AND task_type = ? AND decision_protocol = ?`, normalizeStockSource(provider), strings.TrimSpace(model), taskType, protocol))
	if err == sql.ErrNoRows {
		return StockAgentModelProfile{}, ErrNotFound
	}
	return item, err
}

func (s *Store) SelectStockAgentModelProfile(ctx context.Context, taskType, protocol string) (StockAgentModelProfile, error) {
	if item, err := s.selectStockAgentModelProfile(ctx, `task_type = ? AND decision_protocol = ?`, taskType, protocol); err == nil {
		return item, nil
	} else if err != ErrNotFound {
		return StockAgentModelProfile{}, err
	}
	return s.selectStockAgentModelProfile(ctx, `task_type = ?`, taskType)
}

func (s *Store) selectStockAgentModelProfile(ctx context.Context, condition string, args ...any) (StockAgentModelProfile, error) {
	query := `SELECT id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at FROM stock_agent_model_profiles WHERE enabled = 1 AND status = 'available' AND auth_mode != 'disabled' AND (` + condition + `) ORDER BY CASE WHEN provider = 'system' THEN 1 ELSE 0 END, updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return StockAgentModelProfile{}, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanStockAgentModelProfile(rows)
		if err != nil {
			return StockAgentModelProfile{}, err
		}
		if item.DailyTokenBudget > 0 || item.DailyCostBudget > 0 {
			if err := s.stockAgentBudgetAvailable(ctx, item); err != nil {
				continue
			}
		}
		return item, nil
	}
	if err := rows.Err(); err != nil {
		return StockAgentModelProfile{}, err
	}
	return StockAgentModelProfile{}, ErrNotFound
}

func (s *Store) UpdateStockAgentModelProfileRuntime(ctx context.Context, id, status, failureSummary string) (StockAgentModelProfile, error) {
	ts := now()
	_, err := s.db.ExecContext(ctx, `UPDATE stock_agent_model_profiles SET status = COALESCE(NULLIF(?, ''), status), last_used_at = ?, failure_summary = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(status), ts, strings.TrimSpace(failureSummary), ts, id)
	if err != nil {
		return StockAgentModelProfile{}, err
	}
	item, err := scanStockAgentModelProfile(s.db.QueryRowContext(ctx, `SELECT id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at FROM stock_agent_model_profiles WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockAgentModelProfile{}, ErrNotFound
	}
	return item, err
}

func (s *Store) GetStockAgentModelProfile(ctx context.Context, id string) (StockAgentModelProfile, error) {
	item, err := scanStockAgentModelProfile(s.db.QueryRowContext(ctx, `SELECT id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at FROM stock_agent_model_profiles WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockAgentModelProfile{}, ErrNotFound
	}
	return item, err
}

func (s *Store) stockAgentBudgetAvailable(ctx context.Context, profile StockAgentModelProfile) error {
	var tokens int
	since := now()[:10] + "T00:00:00Z"
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(s.token_estimate), 0) FROM stock_agent_run_steps s JOIN stock_agent_runs r ON r.id = s.run_id WHERE r.model_profile_id = ? AND s.created_at >= ?`, profile.ID, since).Scan(&tokens); err != nil {
		return err
	}
	cost := float64(tokens) / 1000 * StockAgentEstimatedCostPerThousandTokens
	if profile.DailyTokenBudget > 0 && tokens >= profile.DailyTokenBudget {
		return errors.New("stock agent daily token budget exhausted")
	}
	if profile.DailyCostBudget > 0 && cost >= profile.DailyCostBudget {
		return errors.New("stock agent daily cost budget exhausted")
	}
	return nil
}

func (s *Store) ListStockAgentModelProfiles(ctx context.Context) ([]StockAgentModelProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, provider, model, task_type, decision_protocol, auth_mode, enabled, temperature, daily_token_budget, daily_cost_budget, status, last_used_at, failure_summary, created_at, updated_at FROM stock_agent_model_profiles ORDER BY task_type, decision_protocol, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockAgentModelProfile
	for rows.Next() {
		item, err := scanStockAgentModelProfile(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockAgentRun(ctx context.Context, run StockAgentRun) (StockAgentRun, error) {
	id, err := ids.New("star")
	if err != nil {
		return StockAgentRun{}, err
	}
	ts := now()
	run.ID = id
	run.Symbol = normalizeStockSymbol(run.Symbol)
	run.DecisionProtocol = defaultString(run.DecisionProtocol, "single_review")
	run.Status = defaultString(run.Status, "completed")
	run.PromptSnapshot = limitStockAgentText(redactStockAgentText(run.PromptSnapshot), stockAgentFieldLimit)
	run.InputSnapshot = limitStockAgentText(redactStockAgentText(run.InputSnapshot), stockAgentFieldLimit)
	run.OutputSnapshot = limitStockAgentText(redactStockAgentText(run.OutputSnapshot), stockAgentFieldLimit)
	run.RunGraphJSON = limitStockAgentText(defaultString(run.RunGraphJSON, "{}"), stockAgentGraphLimit)
	run.SkillSnapshotJSON = limitStockAgentText(defaultString(run.SkillSnapshotJSON, "{}"), stockAgentFieldLimit)
	run.ToolSnapshotJSON = limitStockAgentText(defaultString(run.ToolSnapshotJSON, "{}"), stockAgentFieldLimit)
	run.CostSummaryJSON = limitStockAgentText(defaultString(run.CostSummaryJSON, "{}"), stockAgentFieldLimit)
	run.Summary = limitStockAgentText(run.Summary, 4000)
	if run.StartedAt == "" {
		run.StartedAt = ts
	}
	if run.CompletedAt == "" && (run.Status == "completed" || run.Status == "failed" || run.Status == "degraded") {
		run.CompletedAt = ts
	}
	run.CreatedAt = ts
	run.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_agent_runs (id, trigger_source, trigger_object_type, trigger_object_id, strategy_id, portfolio_id, watch_id, alert_id, review_id, symbol, decision_protocol, status, result, confidence, model_profile_id, provider, model, prompt_snapshot, input_snapshot, output_snapshot, run_graph_json, skill_snapshot_json, tool_snapshot_json, cost_summary_json, summary, redaction_summary, started_at, completed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TriggerSource, run.TriggerObjectType, run.TriggerObjectID, run.StrategyID, run.PortfolioID, run.WatchID, run.AlertID, run.ReviewID, run.Symbol, run.DecisionProtocol, run.Status, run.Result, run.Confidence, run.ModelProfileID, run.Provider, run.Model, run.PromptSnapshot, run.InputSnapshot, run.OutputSnapshot, run.RunGraphJSON, run.SkillSnapshotJSON, run.ToolSnapshotJSON, run.CostSummaryJSON, run.Summary, run.RedactionSummary, run.StartedAt, run.CompletedAt, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return StockAgentRun{}, err
	}
	if run.ModelProfileID != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE stock_agent_model_profiles SET last_used_at = ?, updated_at = ? WHERE id = ?`, ts, ts, run.ModelProfileID)
	}
	return run, nil
}

func (s *Store) UpdateStockAgentRunGraph(ctx context.Context, id, graphJSON string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE stock_agent_runs SET run_graph_json = ?, updated_at = ? WHERE id = ?`, limitStockAgentText(defaultString(graphJSON, "{}"), stockAgentGraphLimit), now(), id)
	return err
}

func (s *Store) UpdateStockAgentRunExecution(ctx context.Context, id, status, outputSnapshot, costSummaryJSON, summary string) (StockAgentRun, error) {
	ts := now()
	if strings.TrimSpace(outputSnapshot) != "" {
		outputSnapshot = limitStockAgentText(redactStockAgentText(outputSnapshot), stockAgentFieldLimit)
	}
	if strings.TrimSpace(costSummaryJSON) != "" {
		costSummaryJSON = limitStockAgentText(defaultString(costSummaryJSON, "{}"), stockAgentFieldLimit)
	}
	if strings.TrimSpace(summary) != "" {
		summary = limitStockAgentText(summary, 4000)
	}
	completedAt := ""
	switch status {
	case "completed", "failed", "degraded", "authorization_denied":
		completedAt = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_agent_runs SET status = COALESCE(NULLIF(?, ''), status), output_snapshot = COALESCE(NULLIF(?, ''), output_snapshot), cost_summary_json = COALESCE(NULLIF(?, ''), cost_summary_json), summary = COALESCE(NULLIF(?, ''), summary), completed_at = COALESCE(NULLIF(?, ''), completed_at), updated_at = ? WHERE id = ?`,
		strings.TrimSpace(status), outputSnapshot, costSummaryJSON, summary, completedAt, ts, id)
	if err != nil {
		return StockAgentRun{}, err
	}
	return s.GetStockAgentRun(ctx, id)
}

func (s *Store) GetStockAgentRun(ctx context.Context, id string) (StockAgentRun, error) {
	item, err := scanStockAgentRun(s.db.QueryRowContext(ctx, `SELECT id, trigger_source, trigger_object_type, trigger_object_id, strategy_id, portfolio_id, watch_id, alert_id, review_id, symbol, decision_protocol, status, result, confidence, model_profile_id, provider, model, prompt_snapshot, input_snapshot, output_snapshot, run_graph_json, skill_snapshot_json, tool_snapshot_json, cost_summary_json, summary, redaction_summary, started_at, completed_at, created_at, updated_at FROM stock_agent_runs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockAgentRun{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockAgentRuns(ctx context.Context, limit int) ([]StockAgentRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, trigger_source, trigger_object_type, trigger_object_id, strategy_id, portfolio_id, watch_id, alert_id, review_id, symbol, decision_protocol, status, result, confidence, model_profile_id, provider, model, prompt_snapshot, input_snapshot, output_snapshot, run_graph_json, skill_snapshot_json, tool_snapshot_json, cost_summary_json, summary, redaction_summary, started_at, completed_at, created_at, updated_at FROM stock_agent_runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockAgentRun
	for rows.Next() {
		item, err := scanStockAgentRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) StockAgentRunForReview(ctx context.Context, reviewID string) (StockAgentRun, error) {
	item, err := scanStockAgentRun(s.db.QueryRowContext(ctx, `SELECT id, trigger_source, trigger_object_type, trigger_object_id, strategy_id, portfolio_id, watch_id, alert_id, review_id, symbol, decision_protocol, status, result, confidence, model_profile_id, provider, model, prompt_snapshot, input_snapshot, output_snapshot, run_graph_json, skill_snapshot_json, tool_snapshot_json, cost_summary_json, summary, redaction_summary, started_at, completed_at, created_at, updated_at FROM stock_agent_runs WHERE review_id = ? ORDER BY created_at DESC LIMIT 1`, reviewID))
	if err == sql.ErrNoRows {
		return StockAgentRun{}, ErrNotFound
	}
	return item, err
}

func (s *Store) CreateStockAgentAuthorization(ctx context.Context, auth StockAgentAuthorization) (StockAgentAuthorization, error) {
	if auth.RunID == "" {
		return StockAgentAuthorization{}, errors.New("run id is required")
	}
	if auth.ProfileID == "" {
		return StockAgentAuthorization{}, errors.New("profile id is required")
	}
	id, err := ids.New("staa")
	if err != nil {
		return StockAgentAuthorization{}, err
	}
	ts := now()
	auth.ID = id
	auth.TaskType = defaultString(auth.TaskType, "review")
	auth.DecisionProtocol = defaultString(auth.DecisionProtocol, "single_review")
	auth.Symbol = normalizeStockSymbol(auth.Symbol)
	auth.Status = defaultString(auth.Status, "pending")
	auth.Reason = limitStockAgentText(auth.Reason, 2000)
	auth.PromptSnapshot = limitStockAgentText(redactStockAgentText(auth.PromptSnapshot), stockAgentFieldLimit)
	auth.InputSnapshot = limitStockAgentText(redactStockAgentText(auth.InputSnapshot), stockAgentFieldLimit)
	auth.OutputSnapshot = limitStockAgentText(redactStockAgentText(auth.OutputSnapshot), stockAgentFieldLimit)
	auth.RequestedBy = defaultString(auth.RequestedBy, "system")
	auth.CreatedAt = ts
	auth.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_agent_authorizations (id, run_id, review_id, profile_id, task_type, decision_protocol, provider, model, symbol, status, reason, prompt_snapshot, input_snapshot, output_snapshot, requested_by, decision, error_summary, created_at, decided_at, completed_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		auth.ID, auth.RunID, auth.ReviewID, auth.ProfileID, auth.TaskType, auth.DecisionProtocol, auth.Provider, auth.Model, auth.Symbol, auth.Status, auth.Reason, auth.PromptSnapshot, auth.InputSnapshot, auth.OutputSnapshot, auth.RequestedBy, auth.Decision, auth.ErrorSummary, auth.CreatedAt, auth.DecidedAt, auth.CompletedAt, auth.UpdatedAt)
	if err != nil {
		return StockAgentAuthorization{}, err
	}
	return auth, nil
}

func (s *Store) ListStockAgentAuthorizations(ctx context.Context, status string, limit int) ([]StockAgentAuthorization, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, run_id, review_id, profile_id, task_type, decision_protocol, provider, model, symbol, status, reason, prompt_snapshot, input_snapshot, output_snapshot, requested_by, decision, error_summary, created_at, decided_at, completed_at, updated_at FROM stock_agent_authorizations`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockAgentAuthorization
	for rows.Next() {
		item, err := scanStockAgentAuthorization(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockAgentAuthorization(ctx context.Context, id string) (StockAgentAuthorization, error) {
	item, err := scanStockAgentAuthorization(s.db.QueryRowContext(ctx, `SELECT id, run_id, review_id, profile_id, task_type, decision_protocol, provider, model, symbol, status, reason, prompt_snapshot, input_snapshot, output_snapshot, requested_by, decision, error_summary, created_at, decided_at, completed_at, updated_at FROM stock_agent_authorizations WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockAgentAuthorization{}, ErrNotFound
	}
	return item, err
}

func (s *Store) UpdateStockAgentAuthorizationStatus(ctx context.Context, id, status, decision, errorSummary string) (StockAgentAuthorization, error) {
	status = strings.TrimSpace(status)
	switch status {
	case "pending", "approved", "denied", "completed", "failed":
	default:
		return StockAgentAuthorization{}, errors.New("unsupported authorization status")
	}
	ts := now()
	decidedAt := ""
	if decision != "" || status == "approved" || status == "denied" {
		decidedAt = ts
	}
	completedAt := ""
	if status == "completed" || status == "failed" || status == "denied" {
		completedAt = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_agent_authorizations SET status = ?, decision = COALESCE(NULLIF(?, ''), decision), error_summary = ?, decided_at = COALESCE(NULLIF(?, ''), decided_at), completed_at = COALESCE(NULLIF(?, ''), completed_at), updated_at = ? WHERE id = ?`,
		status, strings.TrimSpace(decision), limitStockAgentText(redactStockAgentText(errorSummary), 2000), decidedAt, completedAt, ts, id)
	if err != nil {
		return StockAgentAuthorization{}, err
	}
	return s.GetStockAgentAuthorization(ctx, id)
}

func (s *Store) CreateStockAgentRunStep(ctx context.Context, step StockAgentRunStep) (StockAgentRunStep, error) {
	if step.RunID == "" {
		return StockAgentRunStep{}, errors.New("run id is required")
	}
	id, err := ids.New("stas")
	if err != nil {
		return StockAgentRunStep{}, err
	}
	ts := now()
	step.ID = id
	step.Status = defaultString(step.Status, "completed")
	step.InputJSON = limitStockAgentText(redactStockAgentText(defaultString(step.InputJSON, "{}")), stockAgentFieldLimit)
	step.OutputJSON = limitStockAgentText(redactStockAgentText(defaultString(step.OutputJSON, "{}")), stockAgentFieldLimit)
	step.ToolCallsJSON = limitStockAgentText(redactStockAgentText(defaultString(step.ToolCallsJSON, "[]")), stockAgentFieldLimit)
	step.Summary = limitStockAgentText(step.Summary, 2000)
	if step.StartedAt == "" {
		step.StartedAt = ts
	}
	if step.CompletedAt == "" && (step.Status == "completed" || step.Status == "failed" || step.Status == "degraded") {
		step.CompletedAt = ts
	}
	step.CreatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_agent_run_steps (id, run_id, step_key, role, status, input_json, output_json, tool_calls_json, latency_ms, token_estimate, summary, started_at, completed_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		step.ID, step.RunID, step.StepKey, step.Role, step.Status, step.InputJSON, step.OutputJSON, step.ToolCallsJSON, step.LatencyMs, step.TokenEstimate, step.Summary, step.StartedAt, step.CompletedAt, step.CreatedAt)
	return step, err
}

func (s *Store) UpdateStockAgentRunStepStatus(ctx context.Context, runID, stepKey, status, summary, outputJSON string) (StockAgentRunStep, error) {
	if runID == "" || stepKey == "" {
		return StockAgentRunStep{}, errors.New("run id and step key are required")
	}
	ts := now()
	completedAt := ""
	if status == "completed" || status == "failed" || status == "degraded" || status == "denied" {
		completedAt = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_agent_run_steps SET status = COALESCE(NULLIF(?, ''), status), summary = COALESCE(NULLIF(?, ''), summary), output_json = COALESCE(NULLIF(?, ''), output_json), completed_at = COALESCE(NULLIF(?, ''), completed_at) WHERE run_id = ? AND step_key = ?`,
		status, limitStockAgentText(summary, 2000), limitStockAgentText(redactStockAgentText(outputJSON), stockAgentFieldLimit), completedAt, runID, stepKey)
	if err != nil {
		return StockAgentRunStep{}, err
	}
	item, err := scanStockAgentRunStep(s.db.QueryRowContext(ctx, `SELECT id, run_id, step_key, role, status, input_json, output_json, tool_calls_json, latency_ms, token_estimate, summary, started_at, completed_at, created_at FROM stock_agent_run_steps WHERE run_id = ? AND step_key = ? ORDER BY created_at DESC LIMIT 1`, runID, stepKey))
	if err == sql.ErrNoRows {
		return StockAgentRunStep{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ListStockAgentRunSteps(ctx context.Context, runID string, limit int) ([]StockAgentRunStep, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := `SELECT id, run_id, step_key, role, status, input_json, output_json, tool_calls_json, latency_ms, token_estimate, summary, started_at, completed_at, created_at FROM stock_agent_run_steps`
	args := []any{}
	if runID != "" {
		query += ` WHERE run_id = ?`
		args = append(args, runID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockAgentRunStep
	for rows.Next() {
		item, err := scanStockAgentRunStep(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockAgentClaim(ctx context.Context, claim StockAgentClaim) (StockAgentClaim, error) {
	if claim.RunID == "" {
		return StockAgentClaim{}, errors.New("run id is required")
	}
	id, err := ids.New("stcl")
	if err != nil {
		return StockAgentClaim{}, err
	}
	claim.ID = id
	claim.ClaimType = defaultString(claim.ClaimType, "observation")
	claim.Text = limitStockAgentText(redactStockAgentText(claim.Text), 2000)
	claim.EvidenceJSON = limitStockAgentText(redactStockAgentText(defaultString(claim.EvidenceJSON, "[]")), stockAgentFieldLimit)
	claim.VerificationStatus = defaultString(claim.VerificationStatus, "unverified")
	claim.Confidence = defaultString(claim.Confidence, "medium")
	claim.CreatedAt = now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_agent_claims (id, run_id, step_id, claim_type, text, evidence_json, verification_status, confidence, source_ref, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		claim.ID, claim.RunID, claim.StepID, claim.ClaimType, claim.Text, claim.EvidenceJSON, claim.VerificationStatus, claim.Confidence, claim.SourceRef, claim.CreatedAt)
	return claim, err
}

func (s *Store) ListStockAgentClaims(ctx context.Context, runID string, limit int) ([]StockAgentClaim, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := `SELECT id, run_id, step_id, claim_type, text, evidence_json, verification_status, confidence, source_ref, created_at FROM stock_agent_claims`
	args := []any{}
	if runID != "" {
		query += ` WHERE run_id = ?`
		args = append(args, runID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockAgentClaim
	for rows.Next() {
		item, err := scanStockAgentClaim(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockStrategyPatch(ctx context.Context, patch StockStrategyPatch) (StockStrategyPatch, error) {
	if patch.StrategyID == "" {
		return StockStrategyPatch{}, errors.New("strategy id is required")
	}
	if _, err := s.GetStockStrategy(ctx, patch.StrategyID); err != nil {
		return StockStrategyPatch{}, err
	}
	id, err := ids.New("stsp")
	if err != nil {
		return StockStrategyPatch{}, err
	}
	ts := now()
	patch.ID = id
	patch.PatchJSON = limitStockAgentText(redactStockAgentText(defaultString(patch.PatchJSON, "{}")), stockAgentFieldLimit)
	patch.Summary = limitStockAgentText(patch.Summary, 2000)
	patch.Status = defaultString(patch.Status, "pending_acceptance")
	patch.CreatedAt = ts
	patch.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_strategy_patches (id, run_id, review_id, strategy_id, patch_json, summary, status, created_at, updated_at, accepted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		patch.ID, patch.RunID, patch.ReviewID, patch.StrategyID, patch.PatchJSON, patch.Summary, patch.Status, patch.CreatedAt, patch.UpdatedAt, patch.AcceptedAt)
	return patch, err
}

func (s *Store) ListStockStrategyPatches(ctx context.Context, status string, limit int) ([]StockStrategyPatch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, run_id, review_id, strategy_id, patch_json, summary, status, created_at, updated_at, accepted_at FROM stock_strategy_patches`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockStrategyPatch
	for rows.Next() {
		item, err := scanStockStrategyPatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) StockStrategyPatchForReview(ctx context.Context, reviewID string) (StockStrategyPatch, error) {
	item, err := scanStockStrategyPatch(s.db.QueryRowContext(ctx, `SELECT id, run_id, review_id, strategy_id, patch_json, summary, status, created_at, updated_at, accepted_at FROM stock_strategy_patches WHERE review_id = ? ORDER BY created_at DESC LIMIT 1`, reviewID))
	if err == sql.ErrNoRows {
		return StockStrategyPatch{}, ErrNotFound
	}
	return item, err
}

func (s *Store) UpdateStockStrategyPatchStatus(ctx context.Context, id, status string) (StockStrategyPatch, error) {
	if status != "rejected" && status != "pending_acceptance" {
		return StockStrategyPatch{}, errors.New("unsupported patch status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_strategy_patches SET status = ?, updated_at = ? WHERE id = ?`, status, now(), id)
	if err != nil {
		return StockStrategyPatch{}, err
	}
	return s.GetStockStrategyPatch(ctx, id)
}

func (s *Store) GetStockStrategyPatch(ctx context.Context, id string) (StockStrategyPatch, error) {
	item, err := scanStockStrategyPatch(s.db.QueryRowContext(ctx, `SELECT id, run_id, review_id, strategy_id, patch_json, summary, status, created_at, updated_at, accepted_at FROM stock_strategy_patches WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockStrategyPatch{}, ErrNotFound
	}
	return item, err
}

func (s *Store) ApplyStockStrategyPatch(ctx context.Context, id string) (StockStrategy, StockStrategyPatch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	defer tx.Rollback()
	patch, err := scanStockStrategyPatch(tx.QueryRowContext(ctx, `SELECT id, run_id, review_id, strategy_id, patch_json, summary, status, created_at, updated_at, accepted_at FROM stock_strategy_patches WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockStrategy{}, StockStrategyPatch{}, ErrNotFound
	}
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if patch.Status != "pending_acceptance" {
		return StockStrategy{}, StockStrategyPatch{}, errors.New("strategy patch is not pending acceptance")
	}
	strategy, err := scanStockStrategy(tx.QueryRowContext(ctx, `SELECT id, title, strategy_type, portfolio_id, symbol, market, name, direction, entry_price_low, entry_price_high, trigger_price_above, trigger_price_below, take_profit, stop_loss, target_position_pct, status, source, thesis, risk_notes, current_version, created_at, updated_at FROM stock_strategies WHERE id = ?`, patch.StrategyID))
	if err == sql.ErrNoRows {
		return StockStrategy{}, StockStrategyPatch{}, ErrNotFound
	}
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	var payload struct {
		Direction         string  `json:"direction"`
		TriggerPriceAbove float64 `json:"triggerPriceAbove"`
		TriggerPriceBelow float64 `json:"triggerPriceBelow"`
		TakeProfit        float64 `json:"takeProfit"`
		StopLoss          float64 `json:"stopLoss"`
		TargetPositionPct float64 `json:"targetPositionPct"`
		Status            string  `json:"status"`
		ThesisAppend      string  `json:"thesisAppend"`
		RiskNotesAppend   string  `json:"riskNotesAppend"`
	}
	if err := json.Unmarshal([]byte(patch.PatchJSON), &payload); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if payload.Direction != "" {
		strategy.Direction = payload.Direction
	}
	if payload.TriggerPriceAbove > 0 {
		strategy.TriggerPriceAbove = payload.TriggerPriceAbove
	}
	if payload.TriggerPriceBelow > 0 {
		strategy.TriggerPriceBelow = payload.TriggerPriceBelow
	}
	if payload.TakeProfit > 0 {
		strategy.TakeProfit = payload.TakeProfit
	}
	if payload.StopLoss > 0 {
		strategy.StopLoss = payload.StopLoss
	}
	if payload.TargetPositionPct > 0 {
		strategy.TargetPositionPct = payload.TargetPositionPct
	}
	if payload.Status != "" {
		strategy.Status = payload.Status
	}
	if payload.ThesisAppend != "" {
		strategy.Thesis = appendStockNote(strategy.Thesis, payload.ThesisAppend)
	}
	if payload.RiskNotesAppend != "" {
		strategy.RiskNotes = appendStockNote(strategy.RiskNotes, payload.RiskNotesAppend)
	}
	strategy.CurrentVersion++
	ts := now()
	strategy.UpdatedAt = ts
	if _, err := tx.ExecContext(ctx, `UPDATE stock_strategies SET direction = ?, trigger_price_above = ?, trigger_price_below = ?, take_profit = ?, stop_loss = ?, target_position_pct = ?, status = ?, thesis = ?, risk_notes = ?, current_version = ?, updated_at = ? WHERE id = ?`,
		strategy.Direction, strategy.TriggerPriceAbove, strategy.TriggerPriceBelow, strategy.TakeProfit, strategy.StopLoss, strategy.TargetPositionPct, strategy.Status, strategy.Thesis, strategy.RiskNotes, strategy.CurrentVersion, strategy.UpdatedAt, strategy.ID); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	snapshot, err := json.Marshal(strategy)
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	versionID, err := ids.New("stsv")
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_strategy_versions (id, strategy_id, version_number, snapshot_json, status, created_at, accepted_at) VALUES (?, ?, ?, ?, 'accepted', ?, ?)`,
		versionID, strategy.ID, strategy.CurrentVersion, string(snapshot), ts, ts); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_strategy_patches SET status = 'accepted', updated_at = ?, accepted_at = ? WHERE id = ?`, ts, ts, patch.ID); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	memID, err := ids.New("stmm")
	if err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_memories (id, portfolio_id, symbol, object_type, object_id, summary, created_at) VALUES (?, ?, ?, 'strategy_patch', ?, ?, ?)`,
		memID, strategy.PortfolioID, strategy.Symbol, patch.ID, "接受策略补丁，生成策略新版本", ts); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return StockStrategy{}, StockStrategyPatch{}, err
	}
	accepted, err := s.GetStockStrategyPatch(ctx, patch.ID)
	return strategy, accepted, err
}

func (s *Store) StockAgentTraceSummary(ctx context.Context) (StockAgentTraceSummary, error) {
	var summary StockAgentTraceSummary
	err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(1) FROM stock_agent_runs),
  (SELECT COUNT(1) FROM stock_agent_runs WHERE status = 'completed'),
  (SELECT COUNT(1) FROM stock_agent_runs WHERE status = 'failed'),
  (SELECT COUNT(1) FROM stock_strategy_patches WHERE status = 'pending_acceptance'),
  (SELECT COUNT(1) FROM stock_agent_claims),
  COALESCE((SELECT created_at FROM stock_agent_runs ORDER BY created_at DESC LIMIT 1), '')
`).Scan(&summary.RunCount, &summary.CompletedRunCount, &summary.FailedRunCount, &summary.PendingPatchCount, &summary.ClaimCount, &summary.LastRunAt)
	return summary, err
}

func (s *Store) CleanupStockAgentLedger(ctx context.Context, retentionDays, keepRuns int) (StockAgentLedgerCleanupResult, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	if keepRuns <= 0 {
		keepRuns = 500
	}
	if keepRuns > 5000 {
		keepRuns = 5000
	}
	cutoff := formatTime(time.Now().UTC().AddDate(0, 0, -retentionDays))
	rows, err := s.db.QueryContext(ctx, `
SELECT id FROM stock_agent_runs
WHERE created_at < ?
  AND id NOT IN (SELECT id FROM stock_agent_runs ORDER BY created_at DESC LIMIT ?)
  AND id NOT IN (SELECT run_id FROM stock_strategy_patches WHERE status = 'pending_acceptance' AND run_id != '')
ORDER BY created_at ASC
LIMIT 1000`, cutoff, keepRuns)
	if err != nil {
		return StockAgentLedgerCleanupResult{}, err
	}
	defer rows.Close()
	var idsToDelete []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return StockAgentLedgerCleanupResult{}, err
		}
		idsToDelete = append(idsToDelete, id)
	}
	if err := rows.Err(); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	}
	result := StockAgentLedgerCleanupResult{RetentionDays: retentionDays, KeepRuns: keepRuns, Cutoff: cutoff}
	if len(idsToDelete) == 0 {
		return result, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(idsToDelete)), ",")
	args := make([]any, 0, len(idsToDelete))
	for _, id := range idsToDelete {
		args = append(args, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockAgentLedgerCleanupResult{}, err
	}
	defer tx.Rollback()
	if res, err := tx.ExecContext(ctx, `DELETE FROM stock_agent_authorizations WHERE run_id IN (`+placeholders+`)`, args...); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	} else {
		result.AuthorizationsDeleted, _ = res.RowsAffected()
	}
	if res, err := tx.ExecContext(ctx, `DELETE FROM stock_agent_claims WHERE run_id IN (`+placeholders+`)`, args...); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	} else {
		result.ClaimsDeleted, _ = res.RowsAffected()
	}
	if res, err := tx.ExecContext(ctx, `DELETE FROM stock_agent_run_steps WHERE run_id IN (`+placeholders+`)`, args...); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	} else {
		result.StepsDeleted, _ = res.RowsAffected()
	}
	if res, err := tx.ExecContext(ctx, `DELETE FROM stock_agent_runs WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	} else {
		result.RunsDeleted, _ = res.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return StockAgentLedgerCleanupResult{}, err
	}
	return result, nil
}

func (s *Store) CreateStockMemory(ctx context.Context, memory StockMemory) (StockMemory, error) {
	id, err := ids.New("stmm")
	if err != nil {
		return StockMemory{}, err
	}
	memory.ID = id
	memory.Symbol = normalizeStockSymbol(memory.Symbol)
	memory.CreatedAt = now()
	if memory.ObjectType == "" || memory.ObjectID == "" {
		return StockMemory{}, errors.New("memory object is required")
	}
	memory.Summary = limitStockAgentText(redactStockAgentText(memory.Summary), 2000)
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_memories (id, portfolio_id, symbol, object_type, object_id, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		memory.ID, memory.PortfolioID, memory.Symbol, memory.ObjectType, memory.ObjectID, memory.Summary, memory.CreatedAt)
	return memory, err
}

func scanStockAgentModelProfile(row interface{ Scan(...any) error }) (StockAgentModelProfile, error) {
	var item StockAgentModelProfile
	var enabled int
	err := row.Scan(&item.ID, &item.Name, &item.Provider, &item.Model, &item.TaskType, &item.DecisionProtocol, &item.AuthMode, &enabled, &item.Temperature, &item.DailyTokenBudget, &item.DailyCostBudget, &item.Status, &item.LastUsedAt, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt)
	item.Enabled = enabled == 1
	return item, err
}

func scanStockAgentRun(row interface{ Scan(...any) error }) (StockAgentRun, error) {
	var item StockAgentRun
	err := row.Scan(&item.ID, &item.TriggerSource, &item.TriggerObjectType, &item.TriggerObjectID, &item.StrategyID, &item.PortfolioID, &item.WatchID, &item.AlertID, &item.ReviewID, &item.Symbol, &item.DecisionProtocol, &item.Status, &item.Result, &item.Confidence, &item.ModelProfileID, &item.Provider, &item.Model, &item.PromptSnapshot, &item.InputSnapshot, &item.OutputSnapshot, &item.RunGraphJSON, &item.SkillSnapshotJSON, &item.ToolSnapshotJSON, &item.CostSummaryJSON, &item.Summary, &item.RedactionSummary, &item.StartedAt, &item.CompletedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanStockAgentAuthorization(row interface{ Scan(...any) error }) (StockAgentAuthorization, error) {
	var item StockAgentAuthorization
	err := row.Scan(&item.ID, &item.RunID, &item.ReviewID, &item.ProfileID, &item.TaskType, &item.DecisionProtocol, &item.Provider, &item.Model, &item.Symbol, &item.Status, &item.Reason, &item.PromptSnapshot, &item.InputSnapshot, &item.OutputSnapshot, &item.RequestedBy, &item.Decision, &item.ErrorSummary, &item.CreatedAt, &item.DecidedAt, &item.CompletedAt, &item.UpdatedAt)
	return item, err
}

func scanStockAgentRunStep(row interface{ Scan(...any) error }) (StockAgentRunStep, error) {
	var item StockAgentRunStep
	err := row.Scan(&item.ID, &item.RunID, &item.StepKey, &item.Role, &item.Status, &item.InputJSON, &item.OutputJSON, &item.ToolCallsJSON, &item.LatencyMs, &item.TokenEstimate, &item.Summary, &item.StartedAt, &item.CompletedAt, &item.CreatedAt)
	return item, err
}

func scanStockAgentClaim(row interface{ Scan(...any) error }) (StockAgentClaim, error) {
	var item StockAgentClaim
	err := row.Scan(&item.ID, &item.RunID, &item.StepID, &item.ClaimType, &item.Text, &item.EvidenceJSON, &item.VerificationStatus, &item.Confidence, &item.SourceRef, &item.CreatedAt)
	return item, err
}

func scanStockStrategyPatch(row interface{ Scan(...any) error }) (StockStrategyPatch, error) {
	var item StockStrategyPatch
	err := row.Scan(&item.ID, &item.RunID, &item.ReviewID, &item.StrategyID, &item.PatchJSON, &item.Summary, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.AcceptedAt)
	return item, err
}

func appendStockNote(existing, add string) string {
	existing = strings.TrimSpace(existing)
	add = strings.TrimSpace(add)
	if existing == "" {
		return add
	}
	if add == "" {
		return existing
	}
	return existing + "\n" + add
}

func limitStockAgentText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func redactStockAgentText(value string) string {
	if value == "" {
		return ""
	}
	value = stockAgentBearerSecretPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = stockAgentKVSecretPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = stockAgentJWTSecretPattern.ReplaceAllString(value, "[REDACTED]")
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, marker := range []string{"authorization", "api_key", "apikey", "cookie", "session", "token", "password", "secret", "private_key"} {
			if strings.Contains(lower, marker) {
				lines[i] = "[REDACTED]"
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
