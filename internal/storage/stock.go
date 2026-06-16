package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
)

type StockPortfolio struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description,omitempty"`
	Cash                 float64 `json:"cash"`
	RiskLevel            string  `json:"riskLevel"`
	MaxSinglePositionPct float64 `json:"maxSinglePositionPct"`
	MaxDrawdownPct       float64 `json:"maxDrawdownPct"`
	AllowBuy             bool    `json:"allowBuy"`
	AllowAdd             bool    `json:"allowAdd"`
	AllowReduce          bool    `json:"allowReduce"`
	AllowSell            bool    `json:"allowSell"`
	Notes                string  `json:"notes,omitempty"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type StockPortfolioPatch struct {
	Name                 *string  `json:"name,omitempty"`
	Description          *string  `json:"description,omitempty"`
	Cash                 *float64 `json:"cash,omitempty"`
	CashDelta            *float64 `json:"cashDelta,omitempty"`
	RiskLevel            *string  `json:"riskLevel,omitempty"`
	MaxSinglePositionPct *float64 `json:"maxSinglePositionPct,omitempty"`
	MaxDrawdownPct       *float64 `json:"maxDrawdownPct,omitempty"`
	AllowBuy             *bool    `json:"allowBuy,omitempty"`
	AllowAdd             *bool    `json:"allowAdd,omitempty"`
	AllowReduce          *bool    `json:"allowReduce,omitempty"`
	AllowSell            *bool    `json:"allowSell,omitempty"`
	Notes                *string  `json:"notes,omitempty"`
}

type StockPortfolioUpdateResult struct {
	Before    StockPortfolio `json:"before"`
	Portfolio StockPortfolio `json:"portfolio"`
}

type StockHolding struct {
	ID                string  `json:"id"`
	PortfolioID       string  `json:"portfolioId"`
	Symbol            string  `json:"symbol"`
	Market            string  `json:"market,omitempty"`
	Name              string  `json:"name,omitempty"`
	Quantity          float64 `json:"quantity"`
	AvailableQuantity float64 `json:"availableQuantity"`
	CostPrice         float64 `json:"costPrice"`
	LastPrice         float64 `json:"lastPrice"`
	LastPriceAt       string  `json:"lastPriceAt,omitempty"`
	TradableStatus    string  `json:"tradableStatus"`
	MarketValue       float64 `json:"marketValue"`
	PnL               float64 `json:"pnl"`
	PositionPct       float64 `json:"positionPct"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

type StockQuote struct {
	Symbol         string  `json:"symbol"`
	Market         string  `json:"market,omitempty"`
	Name           string  `json:"name,omitempty"`
	LastPrice      float64 `json:"lastPrice"`
	PreviousClose  float64 `json:"previousClose"`
	Volume         float64 `json:"volume"`
	Amount         float64 `json:"amount"`
	DataTimestamp  string  `json:"dataTimestamp,omitempty"`
	DataFreshness  string  `json:"dataFreshness"`
	TradableStatus string  `json:"tradableStatus"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

type StockStrategy struct {
	ID                string  `json:"id"`
	Title             string  `json:"title"`
	StrategyType      string  `json:"strategyType"`
	PortfolioID       string  `json:"portfolioId,omitempty"`
	Symbol            string  `json:"symbol"`
	Market            string  `json:"market,omitempty"`
	Name              string  `json:"name,omitempty"`
	Direction         string  `json:"direction"`
	EntryPriceLow     float64 `json:"entryPriceLow"`
	EntryPriceHigh    float64 `json:"entryPriceHigh"`
	TriggerPriceAbove float64 `json:"triggerPriceAbove"`
	TriggerPriceBelow float64 `json:"triggerPriceBelow"`
	TakeProfit        float64 `json:"takeProfit"`
	StopLoss          float64 `json:"stopLoss"`
	TargetPositionPct float64 `json:"targetPositionPct"`
	Status            string  `json:"status"`
	Source            string  `json:"source"`
	Thesis            string  `json:"thesis,omitempty"`
	RiskNotes         string  `json:"riskNotes,omitempty"`
	CurrentVersion    int     `json:"currentVersion"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

type StockOpportunity struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	SourceType       string `json:"sourceType"`
	SourceRefID      string `json:"sourceRefId,omitempty"`
	Symbol           string `json:"symbol"`
	Market           string `json:"market,omitempty"`
	Name             string `json:"name,omitempty"`
	Theme            string `json:"theme,omitempty"`
	Thesis           string `json:"thesis"`
	EvidenceSummary  string `json:"evidenceSummary,omitempty"`
	Confidence       string `json:"confidence"`
	Status           string `json:"status"`
	LinkedStrategyID string `json:"linkedStrategyId,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

type StockStrategyVersion struct {
	ID            string `json:"id"`
	StrategyID    string `json:"strategyId"`
	VersionNumber int    `json:"versionNumber"`
	SnapshotJSON  string `json:"snapshotJson"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	AcceptedAt    string `json:"acceptedAt,omitempty"`
}

type StockWatch struct {
	ID                   string  `json:"id"`
	StrategyID           string  `json:"strategyId"`
	PortfolioID          string  `json:"portfolioId,omitempty"`
	Symbol               string  `json:"symbol"`
	Market               string  `json:"market,omitempty"`
	Name                 string  `json:"name,omitempty"`
	Status               string  `json:"status"`
	CheckIntervalSeconds int     `json:"checkIntervalSeconds"`
	TriggerPriceAbove    float64 `json:"triggerPriceAbove"`
	TriggerPriceBelow    float64 `json:"triggerPriceBelow"`
	CooldownSeconds      int     `json:"cooldownSeconds"`
	LastCheckedAt        string  `json:"lastCheckedAt,omitempty"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
}

type StockAlert struct {
	ID             string `json:"id"`
	WatchID        string `json:"watchId"`
	StrategyID     string `json:"strategyId"`
	PortfolioID    string `json:"portfolioId,omitempty"`
	Symbol         string `json:"symbol"`
	Market         string `json:"market,omitempty"`
	Name           string `json:"name,omitempty"`
	Level          string `json:"level"`
	Status         string `json:"status"`
	SourceType     string `json:"sourceType"`
	SourceRefID    string `json:"sourceRefId,omitempty"`
	DedupeKey      string `json:"dedupeKey"`
	CooldownUntil  string `json:"cooldownUntil,omitempty"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	TriggerReason  string `json:"triggerReason"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	AcknowledgedAt string `json:"acknowledgedAt,omitempty"`
	ResolvedAt     string `json:"resolvedAt,omitempty"`
}

type StockReview struct {
	ID              string `json:"id"`
	AlertID         string `json:"alertId"`
	WatchID         string `json:"watchId"`
	StrategyID      string `json:"strategyId"`
	PortfolioID     string `json:"portfolioId,omitempty"`
	Symbol          string `json:"symbol"`
	Market          string `json:"market,omitempty"`
	Name            string `json:"name,omitempty"`
	Status          string `json:"status"`
	ReviewResult    string `json:"reviewResult"`
	InputJSON       string `json:"inputJson"`
	OutputJSON      string `json:"outputJson"`
	GuardrailResult string `json:"guardrailResult"`
	Summary         string `json:"summary"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	CompletedAt     string `json:"completedAt,omitempty"`
}

type StockTradeSignal struct {
	ID             string  `json:"id"`
	ReviewID       string  `json:"reviewId"`
	StrategyID     string  `json:"strategyId"`
	Symbol         string  `json:"symbol"`
	Market         string  `json:"market,omitempty"`
	Name           string  `json:"name,omitempty"`
	Direction      string  `json:"direction"`
	PriceRange     string  `json:"priceRange"`
	TriggerSummary string  `json:"triggerSummary"`
	StopLoss       float64 `json:"stopLoss"`
	TakeProfit     float64 `json:"takeProfit"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"createdAt"`
}

type StockProposedOperation struct {
	ID                string  `json:"id"`
	ReviewID          string  `json:"reviewId"`
	StrategyID        string  `json:"strategyId"`
	PortfolioID       string  `json:"portfolioId"`
	Symbol            string  `json:"symbol"`
	Market            string  `json:"market,omitempty"`
	Name              string  `json:"name,omitempty"`
	Action            string  `json:"action"`
	Quantity          float64 `json:"quantity"`
	Price             float64 `json:"price"`
	Amount            float64 `json:"amount"`
	TargetPositionPct float64 `json:"targetPositionPct"`
	GuardrailResult   string  `json:"guardrailResult"`
	GuardrailReason   string  `json:"guardrailReason"`
	Status            string  `json:"status"`
	CreatedAt         string  `json:"createdAt"`
	ConfirmedAt       string  `json:"confirmedAt,omitempty"`
}

type StockOperation struct {
	ID                  string  `json:"id"`
	ProposedOperationID string  `json:"proposedOperationId"`
	PortfolioID         string  `json:"portfolioId"`
	Symbol              string  `json:"symbol"`
	Market              string  `json:"market,omitempty"`
	Name                string  `json:"name,omitempty"`
	Action              string  `json:"action"`
	Quantity            float64 `json:"quantity"`
	Price               float64 `json:"price"`
	Amount              float64 `json:"amount"`
	OccurredAt          string  `json:"occurredAt"`
	Notes               string  `json:"notes,omitempty"`
	CreatedAt           string  `json:"createdAt"`
}

type StockOperationConfirmation struct {
	Price                float64
	Quantity             float64
	Notes                string
	RiskAcknowledged     bool
	ExpectedAction       string
	ExpectedSymbol       string
	ExpectedGuardrail    string
	ExpectedRiskSummary  string
	ConfirmedReferenceAt string
	MaxQuoteAgeSeconds   int
}

type StockWatchUpdate struct {
	Status               *string
	CheckIntervalSeconds *int
	TriggerPriceAbove    *float64
	TriggerPriceBelow    *float64
	CooldownSeconds      *int
}

type StockMemory struct {
	ID          string `json:"id"`
	PortfolioID string `json:"portfolioId,omitempty"`
	Symbol      string `json:"symbol,omitempty"`
	ObjectType  string `json:"objectType"`
	ObjectID    string `json:"objectId"`
	Summary     string `json:"summary"`
	CreatedAt   string `json:"createdAt"`
}

type StockDashboardSummary struct {
	PortfolioCount        int     `json:"portfolioCount"`
	StrategyCount         int     `json:"strategyCount"`
	ActiveWatchCount      int     `json:"activeWatchCount"`
	OpenAlertCount        int     `json:"openAlertCount"`
	PendingReviewCount    int     `json:"pendingReviewCount"`
	PendingOperationCount int     `json:"pendingOperationCount"`
	TotalCash             float64 `json:"totalCash"`
	TotalMarketValue      float64 `json:"totalMarketValue"`
	TotalAssetValue       float64 `json:"totalAssetValue"`
	LastAlertAt           string  `json:"lastAlertAt,omitempty"`
}

var ErrStockPortfolioInUse = errors.New("stock portfolio is referenced by ledger records")

type StockPortfolioDeleteImpact struct {
	Portfolio       StockPortfolio `json:"portfolio"`
	HoldingsDeleted int64          `json:"holdingsDeleted"`
}

type StockPortfolioReferenceCounts struct {
	Strategies         int `json:"strategies"`
	Watches            int `json:"watches"`
	Alerts             int `json:"alerts"`
	Reviews            int `json:"reviews"`
	ProposedOperations int `json:"proposedOperations"`
	Operations         int `json:"operations"`
	Memories           int `json:"memories"`
	AgentRuns          int `json:"agentRuns"`
}

func (r StockPortfolioReferenceCounts) Total() int {
	return r.Strategies + r.Watches + r.Alerts + r.Reviews + r.ProposedOperations + r.Operations + r.Memories + r.AgentRuns
}

func (r StockPortfolioReferenceCounts) Summary() string {
	parts := []string{}
	if r.Strategies > 0 {
		parts = append(parts, fmt.Sprintf("strategies=%d", r.Strategies))
	}
	if r.Watches > 0 {
		parts = append(parts, fmt.Sprintf("watches=%d", r.Watches))
	}
	if r.Alerts > 0 {
		parts = append(parts, fmt.Sprintf("alerts=%d", r.Alerts))
	}
	if r.Reviews > 0 {
		parts = append(parts, fmt.Sprintf("reviews=%d", r.Reviews))
	}
	if r.ProposedOperations > 0 {
		parts = append(parts, fmt.Sprintf("proposed_operations=%d", r.ProposedOperations))
	}
	if r.Operations > 0 {
		parts = append(parts, fmt.Sprintf("operations=%d", r.Operations))
	}
	if r.Memories > 0 {
		parts = append(parts, fmt.Sprintf("memories=%d", r.Memories))
	}
	if r.AgentRuns > 0 {
		parts = append(parts, fmt.Sprintf("agent_runs=%d", r.AgentRuns))
	}
	return strings.Join(parts, ", ")
}

func (s *Store) migrateStock(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS stock_portfolios (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  cash REAL NOT NULL DEFAULT 0,
  risk_level TEXT NOT NULL DEFAULT 'balanced',
  max_single_position_pct REAL NOT NULL DEFAULT 0.2,
  max_drawdown_pct REAL NOT NULL DEFAULT 0.15,
  allow_buy INTEGER NOT NULL DEFAULT 1,
  allow_add INTEGER NOT NULL DEFAULT 1,
  allow_reduce INTEGER NOT NULL DEFAULT 1,
  allow_sell INTEGER NOT NULL DEFAULT 1,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS stock_holdings (
  id TEXT PRIMARY KEY,
  portfolio_id TEXT NOT NULL,
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  quantity REAL NOT NULL DEFAULT 0,
  available_quantity REAL NOT NULL DEFAULT 0,
  cost_price REAL NOT NULL DEFAULT 0,
  last_price REAL NOT NULL DEFAULT 0,
  last_price_at TEXT NOT NULL DEFAULT '',
  tradable_status TEXT NOT NULL DEFAULT 'unknown',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(portfolio_id, symbol)
);
CREATE INDEX IF NOT EXISTS idx_stock_holdings_portfolio ON stock_holdings(portfolio_id, symbol);
CREATE TABLE IF NOT EXISTS stock_quotes (
  symbol TEXT PRIMARY KEY,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  last_price REAL NOT NULL DEFAULT 0,
  previous_close REAL NOT NULL DEFAULT 0,
  volume REAL NOT NULL DEFAULT 0,
  amount REAL NOT NULL DEFAULT 0,
  data_timestamp TEXT NOT NULL DEFAULT '',
  data_freshness TEXT NOT NULL DEFAULT 'unknown',
  tradable_status TEXT NOT NULL DEFAULT 'unknown',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS stock_opportunities (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  source_type TEXT NOT NULL DEFAULT 'manual',
  source_ref_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  theme TEXT NOT NULL DEFAULT '',
  thesis TEXT NOT NULL DEFAULT '',
  evidence_summary TEXT NOT NULL DEFAULT '',
  confidence TEXT NOT NULL DEFAULT 'medium',
  status TEXT NOT NULL DEFAULT 'candidate',
  linked_strategy_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_opportunities_status ON stock_opportunities(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_opportunities_symbol ON stock_opportunities(symbol, status);
CREATE TABLE IF NOT EXISTS stock_strategies (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  strategy_type TEXT NOT NULL DEFAULT 'account_agnostic',
  portfolio_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  direction TEXT NOT NULL DEFAULT 'watch',
  entry_price_low REAL NOT NULL DEFAULT 0,
  entry_price_high REAL NOT NULL DEFAULT 0,
  trigger_price_above REAL NOT NULL DEFAULT 0,
  trigger_price_below REAL NOT NULL DEFAULT 0,
  take_profit REAL NOT NULL DEFAULT 0,
  stop_loss REAL NOT NULL DEFAULT 0,
  target_position_pct REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active',
  source TEXT NOT NULL DEFAULT 'manual',
  thesis TEXT NOT NULL DEFAULT '',
  risk_notes TEXT NOT NULL DEFAULT '',
  current_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_strategies_status ON stock_strategies(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_strategies_symbol ON stock_strategies(symbol, status);
CREATE TABLE IF NOT EXISTS stock_strategy_versions (
  id TEXT PRIMARY KEY,
  strategy_id TEXT NOT NULL,
  version_number INTEGER NOT NULL,
  snapshot_json TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'accepted',
  created_at TEXT NOT NULL,
  accepted_at TEXT NOT NULL DEFAULT '',
  UNIQUE(strategy_id, version_number)
);
CREATE TABLE IF NOT EXISTS stock_watches (
  id TEXT PRIMARY KEY,
  strategy_id TEXT NOT NULL,
  portfolio_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  check_interval_seconds INTEGER NOT NULL DEFAULT 30,
  trigger_price_above REAL NOT NULL DEFAULT 0,
  trigger_price_below REAL NOT NULL DEFAULT 0,
  cooldown_seconds INTEGER NOT NULL DEFAULT 900,
  last_checked_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_watches_status ON stock_watches(status, symbol);
CREATE TABLE IF NOT EXISTS stock_alerts (
  id TEXT PRIMARY KEY,
  watch_id TEXT NOT NULL DEFAULT '',
  strategy_id TEXT NOT NULL DEFAULT '',
  portfolio_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  level TEXT NOT NULL DEFAULT 'info',
  status TEXT NOT NULL DEFAULT 'new',
  source_type TEXT NOT NULL DEFAULT 'market_data',
  source_ref_id TEXT NOT NULL DEFAULT '',
  dedupe_key TEXT NOT NULL DEFAULT '',
  cooldown_until TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  trigger_reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  acknowledged_at TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_stock_alerts_status_created ON stock_alerts(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_alerts_dedupe ON stock_alerts(dedupe_key, status);
CREATE TABLE IF NOT EXISTS stock_reviews (
  id TEXT PRIMARY KEY,
  alert_id TEXT NOT NULL DEFAULT '',
  watch_id TEXT NOT NULL DEFAULT '',
  strategy_id TEXT NOT NULL DEFAULT '',
  portfolio_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'queued',
  review_result TEXT NOT NULL DEFAULT '',
  input_json TEXT NOT NULL DEFAULT '{}',
  output_json TEXT NOT NULL DEFAULT '{}',
  guardrail_result TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_stock_reviews_status_created ON stock_reviews(status, created_at DESC);
CREATE TABLE IF NOT EXISTS stock_trade_signals (
  id TEXT PRIMARY KEY,
  review_id TEXT NOT NULL,
  strategy_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  direction TEXT NOT NULL DEFAULT 'watch',
  price_range TEXT NOT NULL DEFAULT '',
  trigger_summary TEXT NOT NULL DEFAULT '',
  stop_loss REAL NOT NULL DEFAULT 0,
  take_profit REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_trade_signals_review ON stock_trade_signals(review_id);
CREATE TABLE IF NOT EXISTS stock_proposed_operations (
  id TEXT PRIMARY KEY,
  review_id TEXT NOT NULL,
  strategy_id TEXT NOT NULL DEFAULT '',
  portfolio_id TEXT NOT NULL,
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  quantity REAL NOT NULL DEFAULT 0,
  price REAL NOT NULL DEFAULT 0,
  amount REAL NOT NULL DEFAULT 0,
  target_position_pct REAL NOT NULL DEFAULT 0,
  guardrail_result TEXT NOT NULL DEFAULT '',
  guardrail_reason TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending_confirmation',
  created_at TEXT NOT NULL,
  confirmed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_stock_proposed_operations_status ON stock_proposed_operations(status, created_at DESC);
CREATE TABLE IF NOT EXISTS stock_operations (
  id TEXT PRIMARY KEY,
  proposed_operation_id TEXT NOT NULL DEFAULT '',
  portfolio_id TEXT NOT NULL,
  symbol TEXT NOT NULL,
  market TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  quantity REAL NOT NULL DEFAULT 0,
  price REAL NOT NULL DEFAULT 0,
  amount REAL NOT NULL DEFAULT 0,
  occurred_at TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_operations_portfolio ON stock_operations(portfolio_id, occurred_at DESC);
CREATE TABLE IF NOT EXISTS stock_memories (
  id TEXT PRIMARY KEY,
  portfolio_id TEXT NOT NULL DEFAULT '',
  symbol TEXT NOT NULL DEFAULT '',
  object_type TEXT NOT NULL,
  object_id TEXT NOT NULL,
  summary TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stock_memories_symbol ON stock_memories(symbol, created_at DESC);
`)
	return err
}

func (s *Store) CreateStockPortfolio(ctx context.Context, p StockPortfolio) (StockPortfolio, error) {
	id, err := ids.New("stpf")
	if err != nil {
		return StockPortfolio{}, err
	}
	ts := now()
	p.ID = id
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return StockPortfolio{}, errors.New("portfolio name is required")
	}
	p.RiskLevel = defaultString(p.RiskLevel, "balanced")
	if p.MaxSinglePositionPct <= 0 {
		p.MaxSinglePositionPct = 0.2
	}
	if p.MaxDrawdownPct <= 0 {
		p.MaxDrawdownPct = 0.15
	}
	if !p.AllowBuy && !p.AllowAdd && !p.AllowReduce && !p.AllowSell {
		p.AllowBuy = true
		p.AllowAdd = true
		p.AllowReduce = true
		p.AllowSell = true
	}
	p.CreatedAt = ts
	p.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_portfolios (id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.Cash, p.RiskLevel, p.MaxSinglePositionPct, p.MaxDrawdownPct, boolInt(p.AllowBuy), boolInt(p.AllowAdd), boolInt(p.AllowReduce), boolInt(p.AllowSell), p.Notes, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return StockPortfolio{}, err
	}
	return p, nil
}

func (s *Store) ListStockPortfolios(ctx context.Context) ([]StockPortfolio, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at FROM stock_portfolios ORDER BY updated_at DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockPortfolio
	for rows.Next() {
		item, err := scanStockPortfolio(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockPortfolio(ctx context.Context, id string) (StockPortfolio, error) {
	p, err := scanStockPortfolio(s.db.QueryRowContext(ctx, `SELECT id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at FROM stock_portfolios WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockPortfolio{}, ErrNotFound
	}
	return p, err
}

func (s *Store) UpdateStockPortfolio(ctx context.Context, id string, patch StockPortfolioPatch) (StockPortfolioUpdateResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StockPortfolioUpdateResult{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockPortfolioUpdateResult{}, err
	}
	defer tx.Rollback()
	before, err := scanStockPortfolio(tx.QueryRowContext(ctx, `SELECT id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at FROM stock_portfolios WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockPortfolioUpdateResult{}, ErrNotFound
	}
	if err != nil {
		return StockPortfolioUpdateResult{}, err
	}
	updated := before
	if patch.Name != nil {
		updated.Name = strings.TrimSpace(*patch.Name)
		if updated.Name == "" {
			return StockPortfolioUpdateResult{}, errors.New("portfolio name is required")
		}
	}
	if patch.Description != nil {
		updated.Description = *patch.Description
	}
	if patch.Cash != nil {
		updated.Cash = *patch.Cash
	}
	if patch.CashDelta != nil {
		updated.Cash += *patch.CashDelta
	}
	if updated.Cash < 0 {
		return StockPortfolioUpdateResult{}, errors.New("cash cannot be negative")
	}
	if patch.RiskLevel != nil {
		updated.RiskLevel = defaultString(strings.TrimSpace(*patch.RiskLevel), "balanced")
	}
	if patch.MaxSinglePositionPct != nil {
		if *patch.MaxSinglePositionPct <= 0 {
			return StockPortfolioUpdateResult{}, errors.New("max single position pct must be positive")
		}
		updated.MaxSinglePositionPct = *patch.MaxSinglePositionPct
	}
	if patch.MaxDrawdownPct != nil {
		if *patch.MaxDrawdownPct <= 0 {
			return StockPortfolioUpdateResult{}, errors.New("max drawdown pct must be positive")
		}
		updated.MaxDrawdownPct = *patch.MaxDrawdownPct
	}
	if patch.AllowBuy != nil {
		updated.AllowBuy = *patch.AllowBuy
	}
	if patch.AllowAdd != nil {
		updated.AllowAdd = *patch.AllowAdd
	}
	if patch.AllowReduce != nil {
		updated.AllowReduce = *patch.AllowReduce
	}
	if patch.AllowSell != nil {
		updated.AllowSell = *patch.AllowSell
	}
	if patch.Notes != nil {
		updated.Notes = *patch.Notes
	}
	updated.UpdatedAt = now()
	_, err = tx.ExecContext(ctx, `UPDATE stock_portfolios SET name = ?, description = ?, cash = ?, risk_level = ?, max_single_position_pct = ?, max_drawdown_pct = ?, allow_buy = ?, allow_add = ?, allow_reduce = ?, allow_sell = ?, notes = ?, updated_at = ? WHERE id = ?`,
		updated.Name, updated.Description, updated.Cash, updated.RiskLevel, updated.MaxSinglePositionPct, updated.MaxDrawdownPct, boolInt(updated.AllowBuy), boolInt(updated.AllowAdd), boolInt(updated.AllowReduce), boolInt(updated.AllowSell), updated.Notes, updated.UpdatedAt, updated.ID)
	if err != nil {
		return StockPortfolioUpdateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return StockPortfolioUpdateResult{}, err
	}
	return StockPortfolioUpdateResult{Before: before, Portfolio: updated}, nil
}

func (s *Store) DeleteStockPortfolio(ctx context.Context, id string) (StockPortfolioDeleteImpact, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StockPortfolioDeleteImpact{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	defer tx.Rollback()
	portfolio, err := scanStockPortfolio(tx.QueryRowContext(ctx, `SELECT id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at FROM stock_portfolios WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockPortfolioDeleteImpact{}, ErrNotFound
	}
	if err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	refs, err := countStockPortfolioReferencesTx(ctx, tx, id)
	if err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	if refs.Total() > 0 {
		return StockPortfolioDeleteImpact{}, fmt.Errorf("%w: %s", ErrStockPortfolioInUse, refs.Summary())
	}
	holdingsRes, err := tx.ExecContext(ctx, `DELETE FROM stock_holdings WHERE portfolio_id = ?`, id)
	if err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	deletedRes, err := tx.ExecContext(ctx, `DELETE FROM stock_portfolios WHERE id = ?`, id)
	if err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	if affected, _ := deletedRes.RowsAffected(); affected == 0 {
		return StockPortfolioDeleteImpact{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return StockPortfolioDeleteImpact{}, err
	}
	holdingsDeleted, _ := holdingsRes.RowsAffected()
	return StockPortfolioDeleteImpact{Portfolio: portfolio, HoldingsDeleted: holdingsDeleted}, nil
}

func countStockPortfolioReferencesTx(ctx context.Context, tx *sql.Tx, portfolioID string) (StockPortfolioReferenceCounts, error) {
	var refs StockPortfolioReferenceCounts
	err := tx.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(1) FROM stock_strategies WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_watches WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_alerts WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_reviews WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_proposed_operations WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_operations WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_memories WHERE portfolio_id = ?),
  (SELECT COUNT(1) FROM stock_agent_runs WHERE portfolio_id = ?)
`, portfolioID, portfolioID, portfolioID, portfolioID, portfolioID, portfolioID, portfolioID, portfolioID).
		Scan(&refs.Strategies, &refs.Watches, &refs.Alerts, &refs.Reviews, &refs.ProposedOperations, &refs.Operations, &refs.Memories, &refs.AgentRuns)
	return refs, err
}

func (s *Store) UpsertStockHolding(ctx context.Context, h StockHolding) (StockHolding, error) {
	if _, err := s.GetStockPortfolio(ctx, h.PortfolioID); err != nil {
		return StockHolding{}, err
	}
	var inferredMarket string
	h.Symbol, inferredMarket = normalizeStockSymbolAndMarket(h.Symbol)
	if h.Symbol == "" {
		return StockHolding{}, errors.New("symbol is required")
	}
	h.Market = normalizeStockMarket(defaultString(h.Market, inferredMarket))
	if h.AvailableQuantity < 0 {
		h.AvailableQuantity = h.Quantity
	}
	if h.AvailableQuantity > h.Quantity {
		h.AvailableQuantity = h.Quantity
	}
	if h.TradableStatus == "" {
		h.TradableStatus = "unknown"
	}
	ts := now()
	existing, err := s.getStockHoldingByPortfolioSymbol(ctx, h.PortfolioID, h.Symbol)
	if err == nil {
		h.ID = existing.ID
		h.CreatedAt = existing.CreatedAt
		h.UpdatedAt = ts
		_, err = s.db.ExecContext(ctx, `UPDATE stock_holdings SET market = ?, name = ?, quantity = ?, available_quantity = ?, cost_price = ?, last_price = ?, last_price_at = ?, tradable_status = ?, updated_at = ? WHERE id = ?`,
			h.Market, h.Name, h.Quantity, h.AvailableQuantity, h.CostPrice, h.LastPrice, h.LastPriceAt, h.TradableStatus, h.UpdatedAt, h.ID)
		return h, err
	}
	if err != ErrNotFound {
		return StockHolding{}, err
	}
	id, err := ids.New("sthd")
	if err != nil {
		return StockHolding{}, err
	}
	h.ID = id
	h.CreatedAt = ts
	h.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_holdings (id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.PortfolioID, h.Symbol, h.Market, h.Name, h.Quantity, h.AvailableQuantity, h.CostPrice, h.LastPrice, h.LastPriceAt, h.TradableStatus, h.CreatedAt, h.UpdatedAt)
	return h, err
}

func (s *Store) ListStockHoldings(ctx context.Context, portfolioID string) ([]StockHolding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at FROM stock_holdings WHERE (? = '' OR portfolio_id = ?) ORDER BY updated_at DESC`, portfolioID, portfolioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockHolding
	for rows.Next() {
		item, err := scanStockHolding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockHolding(ctx context.Context, id string) (StockHolding, error) {
	h, err := scanStockHolding(s.db.QueryRowContext(ctx, `SELECT id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at FROM stock_holdings WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockHolding{}, ErrNotFound
	}
	return h, err
}

func (s *Store) getStockHoldingByPortfolioSymbol(ctx context.Context, portfolioID, symbol string) (StockHolding, error) {
	h, err := scanStockHolding(s.db.QueryRowContext(ctx, `SELECT id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at FROM stock_holdings WHERE portfolio_id = ? AND symbol = ?`, portfolioID, normalizeStockSymbol(symbol)))
	if err == sql.ErrNoRows {
		return StockHolding{}, ErrNotFound
	}
	return h, err
}

func (s *Store) UpsertStockQuote(ctx context.Context, q StockQuote) (StockQuote, error) {
	var inferredMarket string
	q.Symbol, inferredMarket = normalizeStockSymbolAndMarket(q.Symbol)
	if q.Symbol == "" {
		return StockQuote{}, errors.New("symbol is required")
	}
	q.Market = normalizeStockMarket(defaultString(q.Market, inferredMarket))
	if q.DataTimestamp == "" {
		q.DataTimestamp = now()
	}
	if q.DataFreshness == "" {
		q.DataFreshness = "fresh"
	}
	if q.TradableStatus == "" {
		q.TradableStatus = "tradable"
	}
	ts := now()
	existing, err := s.GetStockQuote(ctx, q.Symbol)
	if err == nil {
		q.CreatedAt = existing.CreatedAt
		q.UpdatedAt = ts
	} else if err == ErrNotFound {
		q.CreatedAt = ts
		q.UpdatedAt = ts
	} else {
		return StockQuote{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_quotes (symbol, market, name, last_price, previous_close, volume, amount, data_timestamp, data_freshness, tradable_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(symbol) DO UPDATE SET market = excluded.market, name = excluded.name, last_price = excluded.last_price, previous_close = excluded.previous_close, volume = excluded.volume, amount = excluded.amount, data_timestamp = excluded.data_timestamp, data_freshness = excluded.data_freshness, tradable_status = excluded.tradable_status, updated_at = excluded.updated_at`,
		q.Symbol, q.Market, q.Name, q.LastPrice, q.PreviousClose, q.Volume, q.Amount, q.DataTimestamp, q.DataFreshness, q.TradableStatus, q.CreatedAt, q.UpdatedAt)
	if err != nil {
		return StockQuote{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE stock_holdings SET last_price = ?, last_price_at = ?, tradable_status = ?, updated_at = ? WHERE symbol = ?`, q.LastPrice, q.DataTimestamp, q.TradableStatus, q.UpdatedAt, q.Symbol)
	return q, nil
}

func (s *Store) GetStockQuote(ctx context.Context, symbol string) (StockQuote, error) {
	q, err := scanStockQuote(s.db.QueryRowContext(ctx, `SELECT symbol, market, name, last_price, previous_close, volume, amount, data_timestamp, data_freshness, tradable_status, created_at, updated_at FROM stock_quotes WHERE symbol = ?`, normalizeStockSymbol(symbol)))
	if err == sql.ErrNoRows {
		return StockQuote{}, ErrNotFound
	}
	return q, err
}

func (s *Store) ListStockQuotes(ctx context.Context, limit int) ([]StockQuote, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, market, name, last_price, previous_close, volume, amount, data_timestamp, data_freshness, tradable_status, created_at, updated_at FROM stock_quotes ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockQuote
	for rows.Next() {
		item, err := scanStockQuote(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockOpportunity(ctx context.Context, op StockOpportunity) (StockOpportunity, error) {
	id, err := ids.New("stopu")
	if err != nil {
		return StockOpportunity{}, err
	}
	ts := now()
	op.ID = id
	op.Title = strings.TrimSpace(op.Title)
	if op.Title == "" {
		return StockOpportunity{}, errors.New("opportunity title is required")
	}
	var inferredMarket string
	op.Symbol, inferredMarket = normalizeStockSymbolAndMarket(op.Symbol)
	if op.Symbol == "" {
		return StockOpportunity{}, errors.New("symbol is required")
	}
	op.Market = normalizeStockMarket(defaultString(op.Market, inferredMarket))
	op.SourceType = defaultString(op.SourceType, "manual")
	op.Confidence = defaultString(op.Confidence, "medium")
	op.Status = defaultString(op.Status, "candidate")
	op.CreatedAt = ts
	op.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_opportunities (id, title, source_type, source_ref_id, symbol, market, name, theme, thesis, evidence_summary, confidence, status, linked_strategy_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.Title, op.SourceType, op.SourceRefID, op.Symbol, op.Market, op.Name, op.Theme, op.Thesis, op.EvidenceSummary, op.Confidence, op.Status, op.LinkedStrategyID, op.CreatedAt, op.UpdatedAt)
	if err != nil {
		return StockOpportunity{}, err
	}
	return op, nil
}

func (s *Store) ListStockOpportunities(ctx context.Context, status string, limit int) ([]StockOpportunity, error) {
	if limit <= 0 || limit > 500 {
		limit = 120
	}
	query := `SELECT id, title, source_type, source_ref_id, symbol, market, name, theme, thesis, evidence_summary, confidence, status, linked_strategy_id, created_at, updated_at FROM stock_opportunities`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockOpportunity
	for rows.Next() {
		item, err := scanStockOpportunity(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockOpportunity(ctx context.Context, id string) (StockOpportunity, error) {
	op, err := scanStockOpportunity(s.db.QueryRowContext(ctx, `SELECT id, title, source_type, source_ref_id, symbol, market, name, theme, thesis, evidence_summary, confidence, status, linked_strategy_id, created_at, updated_at FROM stock_opportunities WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockOpportunity{}, ErrNotFound
	}
	return op, err
}

func (s *Store) GetStockOpportunityBySource(ctx context.Context, sourceType, sourceRefID string) (StockOpportunity, error) {
	sourceType = strings.TrimSpace(sourceType)
	sourceRefID = strings.TrimSpace(sourceRefID)
	if sourceType == "" || sourceRefID == "" {
		return StockOpportunity{}, ErrNotFound
	}
	op, err := scanStockOpportunity(s.db.QueryRowContext(ctx, `SELECT id, title, source_type, source_ref_id, symbol, market, name, theme, thesis, evidence_summary, confidence, status, linked_strategy_id, created_at, updated_at FROM stock_opportunities WHERE source_type = ? AND source_ref_id = ? ORDER BY updated_at DESC LIMIT 1`, sourceType, sourceRefID))
	if err == sql.ErrNoRows {
		return StockOpportunity{}, ErrNotFound
	}
	return op, err
}

func (s *Store) LinkStockOpportunityStrategy(ctx context.Context, opportunityID, strategyID string) (StockOpportunity, error) {
	ts := now()
	_, err := s.db.ExecContext(ctx, `UPDATE stock_opportunities SET status = 'strategy_created', linked_strategy_id = ?, updated_at = ? WHERE id = ?`, strategyID, ts, opportunityID)
	if err != nil {
		return StockOpportunity{}, err
	}
	return s.GetStockOpportunity(ctx, opportunityID)
}

func (s *Store) CreateStockStrategy(ctx context.Context, st StockStrategy) (StockStrategy, error) {
	id, err := ids.New("stst")
	if err != nil {
		return StockStrategy{}, err
	}
	ts := now()
	st.ID = id
	st.Title = strings.TrimSpace(st.Title)
	if st.Title == "" {
		return StockStrategy{}, errors.New("strategy title is required")
	}
	var inferredMarket string
	st.Symbol, inferredMarket = normalizeStockSymbolAndMarket(st.Symbol)
	if st.Symbol == "" {
		return StockStrategy{}, errors.New("symbol is required")
	}
	st.Market = normalizeStockMarket(defaultString(st.Market, inferredMarket))
	st.StrategyType = defaultString(st.StrategyType, "account_agnostic")
	if st.StrategyType != "account_agnostic" && st.StrategyType != "account_bound" {
		return StockStrategy{}, errors.New("strategy type must be account_agnostic or account_bound")
	}
	if st.StrategyType == "account_bound" && st.PortfolioID == "" {
		return StockStrategy{}, errors.New("account_bound strategy requires portfolio")
	}
	st.Direction = defaultString(st.Direction, "watch")
	st.Status = defaultString(st.Status, "active")
	st.Source = defaultString(st.Source, "manual")
	st.CurrentVersion = 1
	st.CreatedAt = ts
	st.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_strategies (id, title, strategy_type, portfolio_id, symbol, market, name, direction, entry_price_low, entry_price_high, trigger_price_above, trigger_price_below, take_profit, stop_loss, target_position_pct, status, source, thesis, risk_notes, current_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.Title, st.StrategyType, st.PortfolioID, st.Symbol, st.Market, st.Name, st.Direction, st.EntryPriceLow, st.EntryPriceHigh, st.TriggerPriceAbove, st.TriggerPriceBelow, st.TakeProfit, st.StopLoss, st.TargetPositionPct, st.Status, st.Source, st.Thesis, st.RiskNotes, st.CurrentVersion, st.CreatedAt, st.UpdatedAt)
	if err != nil {
		return StockStrategy{}, err
	}
	if err := s.createStockStrategyVersion(ctx, st); err != nil {
		return StockStrategy{}, err
	}
	return st, nil
}

func (s *Store) ListStockStrategies(ctx context.Context) ([]StockStrategy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, strategy_type, portfolio_id, symbol, market, name, direction, entry_price_low, entry_price_high, trigger_price_above, trigger_price_below, take_profit, stop_loss, target_position_pct, status, source, thesis, risk_notes, current_version, created_at, updated_at FROM stock_strategies ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockStrategy
	for rows.Next() {
		item, err := scanStockStrategy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockStrategy(ctx context.Context, id string) (StockStrategy, error) {
	st, err := scanStockStrategy(s.db.QueryRowContext(ctx, `SELECT id, title, strategy_type, portfolio_id, symbol, market, name, direction, entry_price_low, entry_price_high, trigger_price_above, trigger_price_below, take_profit, stop_loss, target_position_pct, status, source, thesis, risk_notes, current_version, created_at, updated_at FROM stock_strategies WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockStrategy{}, ErrNotFound
	}
	return st, err
}

func (s *Store) createStockStrategyVersion(ctx context.Context, st StockStrategy) error {
	id, err := ids.New("stsv")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(st)
	if err != nil {
		return err
	}
	ts := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_strategy_versions (id, strategy_id, version_number, snapshot_json, status, created_at, accepted_at) VALUES (?, ?, ?, ?, 'accepted', ?, ?)`,
		id, st.ID, st.CurrentVersion, string(payload), ts, ts)
	return err
}

func (s *Store) CreateStockWatch(ctx context.Context, watch StockWatch) (StockWatch, error) {
	st, err := s.GetStockStrategy(ctx, watch.StrategyID)
	if err != nil {
		return StockWatch{}, err
	}
	id, err := ids.New("stwt")
	if err != nil {
		return StockWatch{}, err
	}
	ts := now()
	watch.ID = id
	watch.PortfolioID = st.PortfolioID
	watch.Symbol = st.Symbol
	watch.Market = st.Market
	watch.Name = st.Name
	watch.Status = defaultString(watch.Status, "active")
	if watch.CheckIntervalSeconds <= 0 {
		watch.CheckIntervalSeconds = 30
	}
	if watch.CooldownSeconds <= 0 {
		watch.CooldownSeconds = 900
	}
	if watch.TriggerPriceAbove <= 0 {
		watch.TriggerPriceAbove = st.TriggerPriceAbove
	}
	if watch.TriggerPriceBelow <= 0 {
		watch.TriggerPriceBelow = st.TriggerPriceBelow
	}
	watch.CreatedAt = ts
	watch.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_watches (id, strategy_id, portfolio_id, symbol, market, name, status, check_interval_seconds, trigger_price_above, trigger_price_below, cooldown_seconds, last_checked_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		watch.ID, watch.StrategyID, watch.PortfolioID, watch.Symbol, watch.Market, watch.Name, watch.Status, watch.CheckIntervalSeconds, watch.TriggerPriceAbove, watch.TriggerPriceBelow, watch.CooldownSeconds, watch.LastCheckedAt, watch.CreatedAt, watch.UpdatedAt)
	return watch, err
}

func (s *Store) ListStockWatches(ctx context.Context) ([]StockWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, strategy_id, portfolio_id, symbol, market, name, status, check_interval_seconds, trigger_price_above, trigger_price_below, cooldown_seconds, last_checked_at, created_at, updated_at FROM stock_watches ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockWatch
	for rows.Next() {
		item, err := scanStockWatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListActiveStockWatches(ctx context.Context) ([]StockWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, strategy_id, portfolio_id, symbol, market, name, status, check_interval_seconds, trigger_price_above, trigger_price_below, cooldown_seconds, last_checked_at, created_at, updated_at FROM stock_watches WHERE status = 'active' ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockWatch
	for rows.Next() {
		item, err := scanStockWatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockWatch(ctx context.Context, id string) (StockWatch, error) {
	w, err := scanStockWatch(s.db.QueryRowContext(ctx, `SELECT id, strategy_id, portfolio_id, symbol, market, name, status, check_interval_seconds, trigger_price_above, trigger_price_below, cooldown_seconds, last_checked_at, created_at, updated_at FROM stock_watches WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockWatch{}, ErrNotFound
	}
	return w, err
}

func (s *Store) UpdateStockWatch(ctx context.Context, id string, update StockWatch) (StockWatch, error) {
	return s.UpdateStockWatchFields(ctx, id, StockWatchUpdate{
		Status:               stringPtr(update.Status),
		CheckIntervalSeconds: intPtr(update.CheckIntervalSeconds),
		TriggerPriceAbove:    floatPtr(update.TriggerPriceAbove),
		TriggerPriceBelow:    floatPtr(update.TriggerPriceBelow),
		CooldownSeconds:      intPtr(update.CooldownSeconds),
	})
}

func (s *Store) UpdateStockWatchFields(ctx context.Context, id string, update StockWatchUpdate) (StockWatch, error) {
	existing, err := s.GetStockWatch(ctx, id)
	if err != nil {
		return StockWatch{}, err
	}
	status := existing.Status
	if update.Status != nil {
		status = strings.TrimSpace(*update.Status)
	}
	if status == "" {
		status = existing.Status
	}
	if status != "active" && status != "paused" && status != "archived" {
		return StockWatch{}, errors.New("unsupported watch status")
	}
	checkInterval := existing.CheckIntervalSeconds
	if update.CheckIntervalSeconds != nil {
		checkInterval = *update.CheckIntervalSeconds
	}
	if checkInterval <= 0 {
		checkInterval = existing.CheckIntervalSeconds
	}
	if checkInterval < 30 {
		checkInterval = 30
	}
	cooldown := existing.CooldownSeconds
	if update.CooldownSeconds != nil {
		cooldown = *update.CooldownSeconds
	}
	if cooldown <= 0 {
		cooldown = existing.CooldownSeconds
	}
	if cooldown < 60 {
		cooldown = 60
	}
	triggerAbove := existing.TriggerPriceAbove
	if update.TriggerPriceAbove != nil {
		triggerAbove = *update.TriggerPriceAbove
	}
	triggerBelow := existing.TriggerPriceBelow
	if update.TriggerPriceBelow != nil {
		triggerBelow = *update.TriggerPriceBelow
	}
	ts := now()
	_, err = s.db.ExecContext(ctx, `UPDATE stock_watches SET status = ?, check_interval_seconds = ?, trigger_price_above = ?, trigger_price_below = ?, cooldown_seconds = ?, updated_at = ? WHERE id = ?`,
		status, checkInterval, triggerAbove, triggerBelow, cooldown, ts, id)
	if err != nil {
		return StockWatch{}, err
	}
	return s.GetStockWatch(ctx, id)
}

func (s *Store) TouchStockWatch(ctx context.Context, id string) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `UPDATE stock_watches SET last_checked_at = ?, updated_at = ? WHERE id = ?`, ts, ts, id)
	return err
}

func (s *Store) OpenStockAlertExists(ctx context.Context, dedupeKey string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM stock_alerts WHERE dedupe_key = ? AND status IN ('new', 'acknowledged', 'snoozed')`, dedupeKey).Scan(&count)
	return count > 0, err
}

func (s *Store) WakeSnoozedStockAlerts(ctx context.Context, nowTime time.Time) (int64, error) {
	ts := formatTime(nowTime)
	result, err := s.db.ExecContext(ctx, `UPDATE stock_alerts SET status = 'new', updated_at = ? WHERE status = 'snoozed' AND cooldown_until != '' AND cooldown_until <= ?`, ts, ts)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) CreateStockAlert(ctx context.Context, alert StockAlert) (StockAlert, error) {
	id, err := ids.New("stal")
	if err != nil {
		return StockAlert{}, err
	}
	ts := now()
	alert.ID = id
	alert.Level = defaultString(alert.Level, "info")
	alert.Status = defaultString(alert.Status, "new")
	alert.SourceType = defaultString(alert.SourceType, "market_data")
	alert.CreatedAt = ts
	alert.UpdatedAt = ts
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_alerts (id, watch_id, strategy_id, portfolio_id, symbol, market, name, level, status, source_type, source_ref_id, dedupe_key, cooldown_until, title, summary, trigger_reason, created_at, updated_at, acknowledged_at, resolved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		alert.ID, alert.WatchID, alert.StrategyID, alert.PortfolioID, alert.Symbol, alert.Market, alert.Name, alert.Level, alert.Status, alert.SourceType, alert.SourceRefID, alert.DedupeKey, alert.CooldownUntil, alert.Title, alert.Summary, alert.TriggerReason, alert.CreatedAt, alert.UpdatedAt, alert.AcknowledgedAt, alert.ResolvedAt)
	return alert, err
}

func (s *Store) ListStockAlerts(ctx context.Context, status string, limit int) ([]StockAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, watch_id, strategy_id, portfolio_id, symbol, market, name, level, status, source_type, source_ref_id, dedupe_key, cooldown_until, title, summary, trigger_reason, created_at, updated_at, acknowledged_at, resolved_at FROM stock_alerts`
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
	var items []StockAlert
	for rows.Next() {
		item, err := scanStockAlert(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockAlert(ctx context.Context, id string) (StockAlert, error) {
	a, err := scanStockAlert(s.db.QueryRowContext(ctx, `SELECT id, watch_id, strategy_id, portfolio_id, symbol, market, name, level, status, source_type, source_ref_id, dedupe_key, cooldown_until, title, summary, trigger_reason, created_at, updated_at, acknowledged_at, resolved_at FROM stock_alerts WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockAlert{}, ErrNotFound
	}
	return a, err
}

func (s *Store) UpdateStockAlertStatus(ctx context.Context, id, status string) (StockAlert, error) {
	return s.UpdateStockAlertLifecycle(ctx, id, status, 0)
}

func (s *Store) UpdateStockAlertLifecycle(ctx context.Context, id, status string, snoozeSeconds int) (StockAlert, error) {
	switch status {
	case "new", "acknowledged", "snoozed", "ignored", "resolved":
	default:
		return StockAlert{}, errors.New("unsupported alert status")
	}
	ts := now()
	ack := ""
	resolved := ""
	if status == "acknowledged" || status == "ignored" || status == "resolved" {
		ack = ts
	}
	if status == "ignored" || status == "resolved" {
		resolved = ts
	}
	cooldownUntil := ""
	if status == "snoozed" {
		if snoozeSeconds <= 0 {
			snoozeSeconds = 30 * 60
		}
		cooldownUntil = formatTime(time.Now().UTC().Add(time.Duration(snoozeSeconds) * time.Second))
		ack = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_alerts SET status = ?, updated_at = ?, cooldown_until = CASE WHEN ? != '' THEN ? ELSE cooldown_until END, acknowledged_at = CASE WHEN ? != '' THEN ? ELSE acknowledged_at END, resolved_at = CASE WHEN ? != '' THEN ? ELSE resolved_at END WHERE id = ?`,
		status, ts, cooldownUntil, cooldownUntil, ack, ack, resolved, resolved, id)
	if err != nil {
		return StockAlert{}, err
	}
	return s.GetStockAlert(ctx, id)
}

func (s *Store) CreateStockReview(ctx context.Context, review StockReview) (StockReview, error) {
	id, err := ids.New("strv")
	if err != nil {
		return StockReview{}, err
	}
	ts := now()
	review.ID = id
	review.Status = defaultString(review.Status, "completed")
	review.CreatedAt = ts
	review.UpdatedAt = ts
	if review.CompletedAt == "" && review.Status == "completed" {
		review.CompletedAt = ts
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_reviews (id, alert_id, watch_id, strategy_id, portfolio_id, symbol, market, name, status, review_result, input_json, output_json, guardrail_result, summary, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.ID, review.AlertID, review.WatchID, review.StrategyID, review.PortfolioID, review.Symbol, review.Market, review.Name, review.Status, review.ReviewResult, review.InputJSON, review.OutputJSON, review.GuardrailResult, review.Summary, review.CreatedAt, review.UpdatedAt, review.CompletedAt)
	return review, err
}

func (s *Store) LatestStockReviewForAlert(ctx context.Context, alertID string) (StockReview, error) {
	review, err := scanStockReview(s.db.QueryRowContext(ctx, `SELECT id, alert_id, watch_id, strategy_id, portfolio_id, symbol, market, name, status, review_result, input_json, output_json, guardrail_result, summary, created_at, updated_at, completed_at FROM stock_reviews WHERE alert_id = ? AND status IN ('context_building', 'evidence_checking', 'guardrail_checking', 'completed') ORDER BY created_at DESC LIMIT 1`, alertID))
	if err == sql.ErrNoRows {
		return StockReview{}, ErrNotFound
	}
	return review, err
}

func (s *Store) UpdateStockReviewState(ctx context.Context, id, status, reviewResult, guardrailResult, summary, outputJSON string, completed bool) (StockReview, error) {
	ts := now()
	completedAt := ""
	if completed {
		completedAt = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stock_reviews SET status = ?, review_result = CASE WHEN ? != '' THEN ? ELSE review_result END, guardrail_result = CASE WHEN ? != '' THEN ? ELSE guardrail_result END, summary = CASE WHEN ? != '' THEN ? ELSE summary END, output_json = CASE WHEN ? != '' THEN ? ELSE output_json END, updated_at = ?, completed_at = CASE WHEN ? != '' THEN ? ELSE completed_at END WHERE id = ?`,
		status, reviewResult, reviewResult, guardrailResult, guardrailResult, summary, summary, outputJSON, outputJSON, ts, completedAt, completedAt, id)
	if err != nil {
		return StockReview{}, err
	}
	review, err := scanStockReview(s.db.QueryRowContext(ctx, `SELECT id, alert_id, watch_id, strategy_id, portfolio_id, symbol, market, name, status, review_result, input_json, output_json, guardrail_result, summary, created_at, updated_at, completed_at FROM stock_reviews WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockReview{}, ErrNotFound
	}
	return review, err
}

func (s *Store) ListStockReviews(ctx context.Context, limit int) ([]StockReview, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, alert_id, watch_id, strategy_id, portfolio_id, symbol, market, name, status, review_result, input_json, output_json, guardrail_result, summary, created_at, updated_at, completed_at FROM stock_reviews ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockReview
	for rows.Next() {
		item, err := scanStockReview(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockTradeSignal(ctx context.Context, signal StockTradeSignal) (StockTradeSignal, error) {
	id, err := ids.New("stsg")
	if err != nil {
		return StockTradeSignal{}, err
	}
	signal.ID = id
	signal.Status = defaultString(signal.Status, "active")
	signal.CreatedAt = now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_trade_signals (id, review_id, strategy_id, symbol, market, name, direction, price_range, trigger_summary, stop_loss, take_profit, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		signal.ID, signal.ReviewID, signal.StrategyID, signal.Symbol, signal.Market, signal.Name, signal.Direction, signal.PriceRange, signal.TriggerSummary, signal.StopLoss, signal.TakeProfit, signal.Status, signal.CreatedAt)
	return signal, err
}

func (s *Store) ListStockTradeSignals(ctx context.Context, limit int) ([]StockTradeSignal, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, review_id, strategy_id, symbol, market, name, direction, price_range, trigger_summary, stop_loss, take_profit, status, created_at FROM stock_trade_signals ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockTradeSignal
	for rows.Next() {
		item, err := scanStockTradeSignal(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateStockProposedOperation(ctx context.Context, op StockProposedOperation) (StockProposedOperation, error) {
	id, err := ids.New("stpo")
	if err != nil {
		return StockProposedOperation{}, err
	}
	op.ID = id
	op.Status = defaultString(op.Status, "pending_confirmation")
	op.CreatedAt = now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO stock_proposed_operations (id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.ReviewID, op.StrategyID, op.PortfolioID, op.Symbol, op.Market, op.Name, op.Action, op.Quantity, op.Price, op.Amount, op.TargetPositionPct, op.GuardrailResult, op.GuardrailReason, op.Status, op.CreatedAt, op.ConfirmedAt)
	return op, err
}

func (s *Store) ListStockProposedOperations(ctx context.Context, status string, limit int) ([]StockProposedOperation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at FROM stock_proposed_operations`
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
	var items []StockProposedOperation
	for rows.Next() {
		item, err := scanStockProposedOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetStockProposedOperation(ctx context.Context, id string) (StockProposedOperation, error) {
	op, err := scanStockProposedOperation(s.db.QueryRowContext(ctx, `SELECT id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at FROM stock_proposed_operations WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return StockProposedOperation{}, ErrNotFound
	}
	return op, err
}

func (s *Store) StockProposedOperationForReview(ctx context.Context, reviewID string) (StockProposedOperation, error) {
	op, err := scanStockProposedOperation(s.db.QueryRowContext(ctx, `SELECT id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at FROM stock_proposed_operations WHERE review_id = ? ORDER BY created_at DESC LIMIT 1`, reviewID))
	if err == sql.ErrNoRows {
		return StockProposedOperation{}, ErrNotFound
	}
	return op, err
}

func (s *Store) StockTradeSignalForReview(ctx context.Context, reviewID string) (StockTradeSignal, error) {
	signal, err := scanStockTradeSignal(s.db.QueryRowContext(ctx, `SELECT id, review_id, strategy_id, symbol, market, name, direction, price_range, trigger_summary, stop_loss, take_profit, status, created_at FROM stock_trade_signals WHERE review_id = ? ORDER BY created_at DESC LIMIT 1`, reviewID))
	if err == sql.ErrNoRows {
		return StockTradeSignal{}, ErrNotFound
	}
	return signal, err
}

func (s *Store) CancelStockProposedOperation(ctx context.Context, proposalID, reason string) (StockProposedOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockProposedOperation{}, err
	}
	defer tx.Rollback()
	op, err := scanStockProposedOperation(tx.QueryRowContext(ctx, `SELECT id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at FROM stock_proposed_operations WHERE id = ?`, proposalID))
	if err == sql.ErrNoRows {
		return StockProposedOperation{}, ErrNotFound
	}
	if err != nil {
		return StockProposedOperation{}, err
	}
	if op.Status != "pending_confirmation" {
		return StockProposedOperation{}, errors.New("proposed operation is not pending confirmation")
	}
	ts := now()
	if strings.TrimSpace(reason) == "" {
		reason = "用户作废操作建议"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_proposed_operations SET status = 'cancelled', confirmed_at = ? WHERE id = ?`, ts, op.ID); err != nil {
		return StockProposedOperation{}, err
	}
	var alertID string
	if err := tx.QueryRowContext(ctx, `SELECT alert_id FROM stock_reviews WHERE id = ?`, op.ReviewID).Scan(&alertID); err == nil && alertID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE stock_alerts SET status = 'resolved', updated_at = ?, resolved_at = CASE WHEN resolved_at = '' THEN ? ELSE resolved_at END WHERE id = ?`, ts, ts, alertID); err != nil {
			return StockProposedOperation{}, err
		}
	} else if err != nil && err != sql.ErrNoRows {
		return StockProposedOperation{}, err
	}
	memID, err := ids.New("stmm")
	if err != nil {
		return StockProposedOperation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_memories (id, portfolio_id, symbol, object_type, object_id, summary, created_at) VALUES (?, ?, ?, 'proposed_operation_cancelled', ?, ?, ?)`,
		memID, op.PortfolioID, op.Symbol, op.ID, fmt.Sprintf("作废%s建议 %.2f 股，参考价 %.3f，原因: %s", normalizeStockAction(op.Action), op.Quantity, op.Price, limitStockText(reason, 240)), ts); err != nil {
		return StockProposedOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return StockProposedOperation{}, err
	}
	return s.GetStockProposedOperation(ctx, proposalID)
}

func (s *Store) ConfirmStockProposedOperation(ctx context.Context, proposalID string, price, quantity float64, notes string) (StockOperation, error) {
	return s.ConfirmStockProposedOperationWithCheck(ctx, proposalID, StockOperationConfirmation{
		Price:            price,
		Quantity:         quantity,
		Notes:            notes,
		RiskAcknowledged: true,
	})
}

func (s *Store) ConfirmStockProposedOperationWithCheck(ctx context.Context, proposalID string, confirmation StockOperationConfirmation) (StockOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StockOperation{}, err
	}
	defer tx.Rollback()
	op, err := scanStockProposedOperation(tx.QueryRowContext(ctx, `SELECT id, review_id, strategy_id, portfolio_id, symbol, market, name, action, quantity, price, amount, target_position_pct, guardrail_result, guardrail_reason, status, created_at, confirmed_at FROM stock_proposed_operations WHERE id = ?`, proposalID))
	if err == sql.ErrNoRows {
		return StockOperation{}, ErrNotFound
	}
	if err != nil {
		return StockOperation{}, err
	}
	if op.Status != "pending_confirmation" {
		return StockOperation{}, errors.New("proposed operation is not pending confirmation")
	}
	if !confirmation.RiskAcknowledged {
		return StockOperation{}, errors.New("risk acknowledgement is required")
	}
	if confirmation.ExpectedAction != "" && normalizeStockAction(confirmation.ExpectedAction) != normalizeStockAction(op.Action) {
		return StockOperation{}, errors.New("confirmation action does not match proposal")
	}
	if confirmation.ExpectedSymbol != "" && normalizeStockSymbol(confirmation.ExpectedSymbol) != op.Symbol {
		return StockOperation{}, errors.New("confirmation symbol does not match proposal")
	}
	if confirmation.ExpectedGuardrail != "" && confirmation.ExpectedGuardrail != op.GuardrailResult {
		return StockOperation{}, errors.New("confirmation guardrail does not match proposal")
	}
	if op.GuardrailResult != "passed" {
		return StockOperation{}, errors.New("proposal guardrail did not pass")
	}
	price := confirmation.Price
	quantity := confirmation.Quantity
	if quantity <= 0 {
		quantity = op.Quantity
	}
	if price <= 0 {
		price = op.Price
	}
	if quantity <= 0 || price <= 0 {
		return StockOperation{}, errors.New("quantity and price are required")
	}
	amount := quantity * price
	portfolio, err := scanStockPortfolio(tx.QueryRowContext(ctx, `SELECT id, name, description, cash, risk_level, max_single_position_pct, max_drawdown_pct, allow_buy, allow_add, allow_reduce, allow_sell, notes, created_at, updated_at FROM stock_portfolios WHERE id = ?`, op.PortfolioID))
	if err != nil {
		if err == sql.ErrNoRows {
			return StockOperation{}, ErrNotFound
		}
		return StockOperation{}, err
	}
	quote, quoteErr := scanStockQuote(tx.QueryRowContext(ctx, `SELECT symbol, market, name, last_price, previous_close, volume, amount, data_timestamp, data_freshness, tradable_status, created_at, updated_at FROM stock_quotes WHERE symbol = ?`, op.Symbol))
	portfolioHoldings, err := listStockHoldingsTx(ctx, tx, op.PortfolioID)
	if err != nil {
		return StockOperation{}, err
	}
	holding, err := scanStockHolding(tx.QueryRowContext(ctx, `SELECT id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at FROM stock_holdings WHERE portfolio_id = ? AND symbol = ?`, op.PortfolioID, op.Symbol))
	if err == sql.ErrNoRows {
		holding = StockHolding{PortfolioID: op.PortfolioID, Symbol: op.Symbol, Market: op.Market, Name: op.Name, TradableStatus: "tradable"}
	} else if err != nil {
		return StockOperation{}, err
	}
	action := normalizeStockAction(op.Action)
	if err := validateStockOperationExecution(action, amount, price, quantity, portfolio, holding, portfolioHoldings, quote, quoteErr, confirmation); err != nil {
		return StockOperation{}, err
	}
	if action == "buy" || action == "add" {
		totalCost := holding.CostPrice*holding.Quantity + amount
		holding.Quantity += quantity
		holding.AvailableQuantity += quantity
		if holding.Quantity > 0 {
			holding.CostPrice = totalCost / holding.Quantity
		}
		portfolio.Cash -= amount
	} else if action == "sell" || action == "reduce" {
		if holding.AvailableQuantity+0.000001 < quantity {
			return StockOperation{}, errors.New("sell quantity exceeds available quantity")
		}
		holding.Quantity -= quantity
		holding.AvailableQuantity -= quantity
		if holding.Quantity < 0 {
			holding.Quantity = 0
		}
		if holding.AvailableQuantity < 0 {
			holding.AvailableQuantity = 0
		}
		portfolio.Cash += amount
	} else {
		return StockOperation{}, errors.New("unsupported operation action")
	}
	ts := now()
	holding.LastPrice = price
	holding.LastPriceAt = ts
	holding.UpdatedAt = ts
	if holding.ID == "" {
		id, err := ids.New("sthd")
		if err != nil {
			return StockOperation{}, err
		}
		holding.ID = id
		holding.CreatedAt = ts
		if _, err := tx.ExecContext(ctx, `INSERT INTO stock_holdings (id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			holding.ID, holding.PortfolioID, holding.Symbol, holding.Market, holding.Name, holding.Quantity, holding.AvailableQuantity, holding.CostPrice, holding.LastPrice, holding.LastPriceAt, holding.TradableStatus, holding.CreatedAt, holding.UpdatedAt); err != nil {
			return StockOperation{}, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE stock_holdings SET quantity = ?, available_quantity = ?, cost_price = ?, last_price = ?, last_price_at = ?, tradable_status = ?, updated_at = ? WHERE id = ?`,
		holding.Quantity, holding.AvailableQuantity, holding.CostPrice, holding.LastPrice, holding.LastPriceAt, holding.TradableStatus, holding.UpdatedAt, holding.ID); err != nil {
		return StockOperation{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_portfolios SET cash = ?, updated_at = ? WHERE id = ?`, portfolio.Cash, ts, portfolio.ID); err != nil {
		return StockOperation{}, err
	}
	operationID, err := ids.New("stop")
	if err != nil {
		return StockOperation{}, err
	}
	operation := StockOperation{
		ID:                  operationID,
		ProposedOperationID: op.ID,
		PortfolioID:         op.PortfolioID,
		Symbol:              op.Symbol,
		Market:              op.Market,
		Name:                op.Name,
		Action:              action,
		Quantity:            quantity,
		Price:               price,
		Amount:              amount,
		OccurredAt:          ts,
		Notes:               confirmation.Notes,
		CreatedAt:           ts,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_operations (id, proposed_operation_id, portfolio_id, symbol, market, name, action, quantity, price, amount, occurred_at, notes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID, operation.ProposedOperationID, operation.PortfolioID, operation.Symbol, operation.Market, operation.Name, operation.Action, operation.Quantity, operation.Price, operation.Amount, operation.OccurredAt, operation.Notes, operation.CreatedAt); err != nil {
		return StockOperation{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE stock_proposed_operations SET status = 'confirmed', confirmed_at = ? WHERE id = ?`, ts, op.ID); err != nil {
		return StockOperation{}, err
	}
	var alertID string
	if err := tx.QueryRowContext(ctx, `SELECT alert_id FROM stock_reviews WHERE id = ?`, op.ReviewID).Scan(&alertID); err == nil && alertID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE stock_alerts SET status = 'resolved', updated_at = ?, resolved_at = CASE WHEN resolved_at = '' THEN ? ELSE resolved_at END WHERE id = ?`, ts, ts, alertID); err != nil {
			return StockOperation{}, err
		}
	} else if err != nil && err != sql.ErrNoRows {
		return StockOperation{}, err
	}
	memID, err := ids.New("stmm")
	if err != nil {
		return StockOperation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_memories (id, portfolio_id, symbol, object_type, object_id, summary, created_at) VALUES (?, ?, ?, 'operation', ?, ?, ?)`,
		memID, op.PortfolioID, op.Symbol, operation.ID, fmt.Sprintf("确认%s %.2f 股，成交价 %.3f，金额 %.2f", action, quantity, price, amount), ts); err != nil {
		return StockOperation{}, err
	}
	return operation, tx.Commit()
}

func validateStockOperationExecution(action string, amount, price, quantity float64, portfolio StockPortfolio, holding StockHolding, holdings []StockHolding, quote StockQuote, quoteErr error, confirmation StockOperationConfirmation) error {
	switch action {
	case "buy":
		if !portfolio.AllowBuy {
			return errors.New("portfolio does not allow buy")
		}
	case "add":
		if !portfolio.AllowBuy || !portfolio.AllowAdd {
			return errors.New("portfolio does not allow add")
		}
	case "sell":
		if !portfolio.AllowSell {
			return errors.New("portfolio does not allow sell")
		}
	case "reduce":
		if !portfolio.AllowSell || !portfolio.AllowReduce {
			return errors.New("portfolio does not allow reduce")
		}
	default:
		return errors.New("unsupported operation action")
	}
	if action == "buy" || action == "add" {
		if portfolio.Cash+0.000001 < amount {
			return errors.New("cash is not enough")
		}
	} else if holding.AvailableQuantity+0.000001 < quantity {
		return errors.New("sell quantity exceeds available quantity")
	}
	if quoteErr != nil {
		return errors.New("latest quote is required before confirmation")
	}
	if quote.DataFreshness != "fresh" || quote.TradableStatus != "tradable" {
		return errors.New("latest quote is stale or not tradable")
	}
	if quote.LastPrice <= 0 {
		return errors.New("latest quote price is required")
	}
	maxAge := confirmation.MaxQuoteAgeSeconds
	if maxAge <= 0 {
		maxAge = 15 * 60
	}
	quoteTimestamp := quote.DataTimestamp
	if quoteTimestamp == "" {
		quoteTimestamp = quote.UpdatedAt
	}
	if quoteTimestamp != "" {
		quoteAt, err := time.Parse(time.RFC3339Nano, quoteTimestamp)
		if err != nil {
			return errors.New("latest quote timestamp is invalid")
		}
		if time.Since(quoteAt) > time.Duration(maxAge)*time.Second {
			return errors.New("latest quote is too old")
		}
	}
	totalAsset := portfolio.Cash + stockHoldingsMarketValue(holdings, holding.Symbol, price)
	if action == "buy" || action == "add" {
		projectedValue := (holding.Quantity + quantity) * price
		projectedAsset := totalAsset
		if projectedAsset <= 0 {
			projectedAsset = projectedValue
		}
		if portfolio.MaxSinglePositionPct > 0 && projectedAsset > 0 && projectedValue/projectedAsset > portfolio.MaxSinglePositionPct+0.000001 {
			return errors.New("single position limit would be exceeded")
		}
	}
	return nil
}

func listStockHoldingsTx(ctx context.Context, tx *sql.Tx, portfolioID string) ([]StockHolding, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, portfolio_id, symbol, market, name, quantity, available_quantity, cost_price, last_price, last_price_at, tradable_status, created_at, updated_at FROM stock_holdings WHERE portfolio_id = ?`, portfolioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockHolding
	for rows.Next() {
		item, err := scanStockHolding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func stockHoldingsMarketValue(holdings []StockHolding, currentSymbol string, currentPrice float64) float64 {
	var total float64
	for _, item := range holdings {
		price := item.LastPrice
		if item.Symbol == currentSymbol && currentPrice > 0 {
			price = currentPrice
		}
		total += item.Quantity * price
	}
	return total
}

func (s *Store) ListStockOperations(ctx context.Context, limit int) ([]StockOperation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, proposed_operation_id, portfolio_id, symbol, market, name, action, quantity, price, amount, occurred_at, notes, created_at FROM stock_operations ORDER BY occurred_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockOperation
	for rows.Next() {
		item, err := scanStockOperation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListStockMemories(ctx context.Context, limit int) ([]StockMemory, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, portfolio_id, symbol, object_type, object_id, summary, created_at FROM stock_memories ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []StockMemory
	for rows.Next() {
		var item StockMemory
		if err := rows.Scan(&item.ID, &item.PortfolioID, &item.Symbol, &item.ObjectType, &item.ObjectID, &item.Summary, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) StockDashboardSummary(ctx context.Context) (StockDashboardSummary, error) {
	var summary StockDashboardSummary
	row := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(1) FROM stock_portfolios),
  (SELECT COUNT(1) FROM stock_strategies WHERE status = 'active'),
  (SELECT COUNT(1) FROM stock_watches WHERE status = 'active'),
  (SELECT COUNT(1) FROM stock_alerts WHERE status IN ('new', 'acknowledged', 'snoozed')),
  (SELECT COUNT(1) FROM stock_reviews WHERE status IN ('queued', 'context_building', 'reviewing', 'guardrail_checking')),
  (SELECT COUNT(1) FROM stock_proposed_operations WHERE status = 'pending_confirmation'),
  COALESCE((SELECT SUM(cash) FROM stock_portfolios), 0),
  COALESCE((SELECT SUM(quantity * last_price) FROM stock_holdings), 0),
  COALESCE((SELECT created_at FROM stock_alerts ORDER BY created_at DESC LIMIT 1), '')
`)
	if err := row.Scan(&summary.PortfolioCount, &summary.StrategyCount, &summary.ActiveWatchCount, &summary.OpenAlertCount, &summary.PendingReviewCount, &summary.PendingOperationCount, &summary.TotalCash, &summary.TotalMarketValue, &summary.LastAlertAt); err != nil {
		return StockDashboardSummary{}, err
	}
	summary.TotalAssetValue = summary.TotalCash + summary.TotalMarketValue
	return summary, nil
}

func scanStockPortfolio(row interface{ Scan(...any) error }) (StockPortfolio, error) {
	var p StockPortfolio
	var allowBuy, allowAdd, allowReduce, allowSell int
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Cash, &p.RiskLevel, &p.MaxSinglePositionPct, &p.MaxDrawdownPct, &allowBuy, &allowAdd, &allowReduce, &allowSell, &p.Notes, &p.CreatedAt, &p.UpdatedAt)
	p.AllowBuy = allowBuy == 1
	p.AllowAdd = allowAdd == 1
	p.AllowReduce = allowReduce == 1
	p.AllowSell = allowSell == 1
	return p, err
}

func scanStockHolding(row interface{ Scan(...any) error }) (StockHolding, error) {
	var h StockHolding
	err := row.Scan(&h.ID, &h.PortfolioID, &h.Symbol, &h.Market, &h.Name, &h.Quantity, &h.AvailableQuantity, &h.CostPrice, &h.LastPrice, &h.LastPriceAt, &h.TradableStatus, &h.CreatedAt, &h.UpdatedAt)
	h.MarketValue = h.Quantity * h.LastPrice
	h.PnL = h.Quantity * (h.LastPrice - h.CostPrice)
	return h, err
}

func scanStockQuote(row interface{ Scan(...any) error }) (StockQuote, error) {
	var q StockQuote
	err := row.Scan(&q.Symbol, &q.Market, &q.Name, &q.LastPrice, &q.PreviousClose, &q.Volume, &q.Amount, &q.DataTimestamp, &q.DataFreshness, &q.TradableStatus, &q.CreatedAt, &q.UpdatedAt)
	return q, err
}

func scanStockStrategy(row interface{ Scan(...any) error }) (StockStrategy, error) {
	var st StockStrategy
	err := row.Scan(&st.ID, &st.Title, &st.StrategyType, &st.PortfolioID, &st.Symbol, &st.Market, &st.Name, &st.Direction, &st.EntryPriceLow, &st.EntryPriceHigh, &st.TriggerPriceAbove, &st.TriggerPriceBelow, &st.TakeProfit, &st.StopLoss, &st.TargetPositionPct, &st.Status, &st.Source, &st.Thesis, &st.RiskNotes, &st.CurrentVersion, &st.CreatedAt, &st.UpdatedAt)
	return st, err
}

func scanStockOpportunity(row interface{ Scan(...any) error }) (StockOpportunity, error) {
	var op StockOpportunity
	err := row.Scan(&op.ID, &op.Title, &op.SourceType, &op.SourceRefID, &op.Symbol, &op.Market, &op.Name, &op.Theme, &op.Thesis, &op.EvidenceSummary, &op.Confidence, &op.Status, &op.LinkedStrategyID, &op.CreatedAt, &op.UpdatedAt)
	return op, err
}

func scanStockWatch(row interface{ Scan(...any) error }) (StockWatch, error) {
	var w StockWatch
	err := row.Scan(&w.ID, &w.StrategyID, &w.PortfolioID, &w.Symbol, &w.Market, &w.Name, &w.Status, &w.CheckIntervalSeconds, &w.TriggerPriceAbove, &w.TriggerPriceBelow, &w.CooldownSeconds, &w.LastCheckedAt, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func scanStockAlert(row interface{ Scan(...any) error }) (StockAlert, error) {
	var a StockAlert
	err := row.Scan(&a.ID, &a.WatchID, &a.StrategyID, &a.PortfolioID, &a.Symbol, &a.Market, &a.Name, &a.Level, &a.Status, &a.SourceType, &a.SourceRefID, &a.DedupeKey, &a.CooldownUntil, &a.Title, &a.Summary, &a.TriggerReason, &a.CreatedAt, &a.UpdatedAt, &a.AcknowledgedAt, &a.ResolvedAt)
	return a, err
}

func scanStockReview(row interface{ Scan(...any) error }) (StockReview, error) {
	var r StockReview
	err := row.Scan(&r.ID, &r.AlertID, &r.WatchID, &r.StrategyID, &r.PortfolioID, &r.Symbol, &r.Market, &r.Name, &r.Status, &r.ReviewResult, &r.InputJSON, &r.OutputJSON, &r.GuardrailResult, &r.Summary, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	return r, err
}

func scanStockTradeSignal(row interface{ Scan(...any) error }) (StockTradeSignal, error) {
	var s StockTradeSignal
	err := row.Scan(&s.ID, &s.ReviewID, &s.StrategyID, &s.Symbol, &s.Market, &s.Name, &s.Direction, &s.PriceRange, &s.TriggerSummary, &s.StopLoss, &s.TakeProfit, &s.Status, &s.CreatedAt)
	return s, err
}

func scanStockProposedOperation(row interface{ Scan(...any) error }) (StockProposedOperation, error) {
	var op StockProposedOperation
	err := row.Scan(&op.ID, &op.ReviewID, &op.StrategyID, &op.PortfolioID, &op.Symbol, &op.Market, &op.Name, &op.Action, &op.Quantity, &op.Price, &op.Amount, &op.TargetPositionPct, &op.GuardrailResult, &op.GuardrailReason, &op.Status, &op.CreatedAt, &op.ConfirmedAt)
	return op, err
}

func scanStockOperation(row interface{ Scan(...any) error }) (StockOperation, error) {
	var op StockOperation
	err := row.Scan(&op.ID, &op.ProposedOperationID, &op.PortfolioID, &op.Symbol, &op.Market, &op.Name, &op.Action, &op.Quantity, &op.Price, &op.Amount, &op.OccurredAt, &op.Notes, &op.CreatedAt)
	return op, err
}

func normalizeStockSymbol(value string) string {
	symbol, _ := normalizeStockSymbolAndMarket(value)
	return symbol
}

func normalizeStockSymbolAndMarket(value string) (string, string) {
	raw := strings.ToUpper(strings.TrimSpace(value))
	raw = strings.TrimPrefix(raw, "$")
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return "", ""
	}
	for _, sep := range []string{".", ":", "-", "_"} {
		if idx := strings.LastIndex(raw, sep); idx > 0 && idx < len(raw)-1 {
			market := normalizeStockMarket(raw[idx+1:])
			if market != "" {
				return raw[:idx], market
			}
		}
	}
	for _, market := range []string{"SH", "SZ", "BJ"} {
		if strings.HasPrefix(raw, market) && len(raw) > len(market) {
			return raw[len(market):], market
		}
	}
	return raw, inferStockMarket(raw)
}

func normalizeStockMarket(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "SH", "SZ", "BJ":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return ""
	}
}

func inferStockMarket(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch {
	case strings.HasPrefix(symbol, "920") || strings.HasPrefix(symbol, "8") || strings.HasPrefix(symbol, "4"):
		return "BJ"
	case strings.HasPrefix(symbol, "6") || strings.HasPrefix(symbol, "9"):
		return "SH"
	case strings.HasPrefix(symbol, "0") || strings.HasPrefix(symbol, "3"):
		return "SZ"
	default:
		return ""
	}
}

func normalizeStockAction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "buy", "add", "sell", "reduce":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "watch"
	}
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func limitStockText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
