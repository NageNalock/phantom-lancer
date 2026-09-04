package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) ensureDecisionGateSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS stockv2_decision_gate_snapshots (
			id TEXT PRIMARY KEY,
			context_type TEXT NOT NULL,
			context_id TEXT NOT NULL,
			symbol TEXT NOT NULL,
			market TEXT,
			instrument_type TEXT,
			trade_date TEXT,
			status TEXT NOT NULL,
			market_regime TEXT,
			payload_json TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_decision_gate_context
			ON stockv2_decision_gate_snapshots(context_type, context_id, symbol, created_at DESC);
		CREATE TABLE IF NOT EXISTS stockv2_decision_gate_outcomes (
			snapshot_id TEXT NOT NULL,
			horizon INTEGER NOT NULL,
			due_trade_date TEXT,
			observed_date TEXT,
			return_pct REAL NOT NULL DEFAULT 0,
			excess_return_pct REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(snapshot_id, horizon),
			FOREIGN KEY(snapshot_id) REFERENCES stockv2_decision_gate_snapshots(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS stockv2_decision_market_events (
			symbol TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_date TEXT NOT NULL,
			announced_at TEXT NOT NULL DEFAULT '',
			title TEXT,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			PRIMARY KEY(symbol, event_type, event_date, announced_at, source)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_decision_events_symbol_date
			ON stockv2_decision_market_events(symbol, event_date);
		CREATE TABLE IF NOT EXISTS stockv2_decision_financial_facts (
			symbol TEXT NOT NULL,
			report_period TEXT NOT NULL,
			dataset TEXT NOT NULL,
			announced_at TEXT NOT NULL DEFAULT '',
			revenue REAL NOT NULL DEFAULT 0,
			net_profit REAL NOT NULL DEFAULT 0,
			operating_cash_flow REAL NOT NULL DEFAULT 0,
			roe REAL NOT NULL DEFAULT 0,
			gross_margin REAL NOT NULL DEFAULT 0,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			PRIMARY KEY(symbol, report_period, dataset, announced_at)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_decision_financial_symbol_period
			ON stockv2_decision_financial_facts(symbol, report_period DESC);
		CREATE TABLE IF NOT EXISTS stockv2_decision_reference_health (
			symbol TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			source TEXT,
			message TEXT,
			event_ok INTEGER NOT NULL DEFAULT 0,
			finance_ok INTEGER NOT NULL DEFAULT 0,
			checked_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS stockv2_decision_index_bars (
			symbol TEXT NOT NULL,
			trade_date TEXT NOT NULL,
			close REAL NOT NULL,
			pct_change REAL NOT NULL DEFAULT 0,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			PRIMARY KEY(symbol, trade_date)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_decision_index_symbol_date
			ON stockv2_decision_index_bars(symbol, trade_date DESC);
		CREATE TABLE IF NOT EXISTS stockv2_decision_trade_calendar (
			calendar_date TEXT PRIMARY KEY,
			is_open INTEGER NOT NULL,
			previous_trade_date TEXT,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS stockv2_decision_fund_flow_cache (
			symbol TEXT PRIMARY KEY,
			market TEXT,
			as_of TEXT NOT NULL,
			main_net_inflow_5 REAL NOT NULL DEFAULT 0,
			main_net_inflow_20 REAL NOT NULL DEFAULT 0,
			main_net_inflow_60 REAL NOT NULL DEFAULT 0,
			main_flow_ratio_20 REAL NOT NULL DEFAULT 0,
			positive_flow_days_20 INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS stockv2_decision_fund_flow_points (
			symbol TEXT NOT NULL,
			market TEXT,
			trade_date TEXT NOT NULL,
			main_net REAL NOT NULL DEFAULT 0,
			turnover REAL NOT NULL DEFAULT 0,
			source TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			PRIMARY KEY(symbol, trade_date)
		);
		CREATE INDEX IF NOT EXISTS idx_stockv2_decision_flow_symbol_date
			ON stockv2_decision_fund_flow_points(symbol, trade_date DESC);
	`)
	return wrapError(err, "ensure decision gate schema")
}

func (s *Store) UpsertDecisionFundFlowEvidence(ctx context.Context, item decisionFundFlowEvidence) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stockv2_decision_fund_flow_cache
		(symbol,market,as_of,main_net_inflow_5,main_net_inflow_20,main_net_inflow_60,
		 main_flow_ratio_20,positive_flow_days_20,source,fetched_at)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(symbol) DO UPDATE SET
		market=excluded.market,as_of=excluded.as_of,main_net_inflow_5=excluded.main_net_inflow_5,
		main_net_inflow_20=excluded.main_net_inflow_20,main_net_inflow_60=excluded.main_net_inflow_60,
		main_flow_ratio_20=excluded.main_flow_ratio_20,positive_flow_days_20=excluded.positive_flow_days_20,
		source=excluded.source,fetched_at=excluded.fetched_at`, item.Symbol, nullableString(item.Market), item.AsOf,
		item.MainNetInflow5, item.MainNetInflow20, item.MainNetInflow60, item.MainFlowRatio20,
		item.PositiveFlowDays20, item.Source, item.FetchedAt)
	return wrapError(err, "upsert decision fund flow evidence")
}

func (s *Store) GetDecisionFundFlowEvidence(ctx context.Context, symbol string) (decisionFundFlowEvidence, error) {
	var item decisionFundFlowEvidence
	err := s.db.QueryRowContext(ctx, `SELECT symbol,COALESCE(market,''),as_of,main_net_inflow_5,
		main_net_inflow_20,main_net_inflow_60,main_flow_ratio_20,positive_flow_days_20,source,fetched_at
		FROM stockv2_decision_fund_flow_cache WHERE symbol=?`, strings.TrimSpace(symbol)).Scan(
		&item.Symbol, &item.Market, &item.AsOf, &item.MainNetInflow5, &item.MainNetInflow20,
		&item.MainNetInflow60, &item.MainFlowRatio20, &item.PositiveFlowDays20, &item.Source, &item.FetchedAt)
	return item, err
}

func (s *Store) UpsertDecisionFundFlowPoints(ctx context.Context, symbol, market, source string, points []opportunityFundFlowPoint, fetchedAt time.Time) error {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" || len(points) == 0 {
		return nil
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, point := range points {
			if strings.TrimSpace(point.TradeDate) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_decision_fund_flow_points
				(symbol,market,trade_date,main_net,turnover,source,fetched_at) VALUES (?,?,?,?,?,?,?)
				ON CONFLICT(symbol,trade_date) DO UPDATE SET market=excluded.market,
				main_net=excluded.main_net,turnover=excluded.turnover,source=excluded.source,fetched_at=excluded.fetched_at`,
				symbol, nullableString(market), point.TradeDate, point.MainNet, point.Turnover, source, fetchedAt); err != nil {
				return wrapError(err, "upsert decision fund flow point")
			}
		}
		return nil
	})
}

func (s *Store) ListDecisionFundFlowPoints(ctx context.Context, symbol, startDate, endDate string, limit int) ([]DecisionFundFlowPoint, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return []DecisionFundFlowPoint{}, nil
	}
	if limit <= 0 || limit > 250 {
		limit = 120
	}
	where := []string{"symbol=?"}
	args := []any{symbol}
	if startDate = strings.TrimSpace(startDate); startDate != "" {
		where = append(where, "trade_date>=?")
		args = append(args, startDate)
	}
	if endDate = strings.TrimSpace(endDate); endDate != "" {
		where = append(where, "trade_date<=?")
		args = append(args, endDate)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,COALESCE(market,''),trade_date,main_net,turnover,source,fetched_at
		FROM (SELECT symbol,market,trade_date,main_net,turnover,source,fetched_at
		FROM stockv2_decision_fund_flow_points WHERE `+strings.Join(where, " AND ")+`
		ORDER BY trade_date DESC LIMIT ?) ORDER BY trade_date ASC`, args...)
	if err != nil {
		return nil, wrapError(err, "list decision fund flow points")
	}
	defer rows.Close()
	out := make([]DecisionFundFlowPoint, 0, limit)
	for rows.Next() {
		var item DecisionFundFlowPoint
		if err := rows.Scan(&item.Symbol, &item.Market, &item.TradeDate, &item.MainNet, &item.Turnover, &item.Source, &item.FetchedAt); err != nil {
			return nil, wrapError(err, "scan decision fund flow point")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveDecisionGateSnapshot(ctx context.Context, item DecisionGateSnapshot) (DecisionGateSnapshot, error) {
	if item.ID == "" {
		item.ID = generateID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return DecisionGateSnapshot{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO stockv2_decision_gate_snapshots
		(id, context_type, context_id, symbol, market, instrument_type, trade_date, status, market_regime, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET
		context_type=excluded.context_type,context_id=excluded.context_id,symbol=excluded.symbol,market=excluded.market,
		instrument_type=excluded.instrument_type,trade_date=excluded.trade_date,status=excluded.status,
		market_regime=excluded.market_regime,payload_json=excluded.payload_json,created_at=excluded.created_at`,
		item.ID, item.ContextType, item.ContextID, item.Symbol,
		nullableString(item.Market), nullableString(item.InstrumentType), nullableString(item.TradeDate), item.Status,
		nullableString(item.MarketRegime), string(payload), item.CreatedAt)
	if err != nil {
		return DecisionGateSnapshot{}, wrapError(err, "save decision gate snapshot")
	}
	for _, horizon := range []int{1, 3, 5, 10, 20} {
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stockv2_decision_gate_outcomes
			(snapshot_id,horizon,status,updated_at) VALUES (?,?,?,?)`, item.ID, horizon, "pending", item.CreatedAt)
		if err != nil {
			return DecisionGateSnapshot{}, wrapError(err, "initialize decision gate outcomes")
		}
	}
	return item, nil
}

func (s *Store) GetLatestDecisionGateSnapshot(ctx context.Context, contextType, contextID, symbol string) (DecisionGateSnapshot, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM stockv2_decision_gate_snapshots
		WHERE context_type=? AND context_id=? AND symbol=? ORDER BY created_at DESC LIMIT 1`,
		strings.TrimSpace(contextType), strings.TrimSpace(contextID), strings.TrimSpace(symbol)).Scan(&payload)
	if err != nil {
		return DecisionGateSnapshot{}, err
	}
	var item DecisionGateSnapshot
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return DecisionGateSnapshot{}, err
	}
	return item, nil
}

func (s *Store) ListDecisionGateSnapshots(ctx context.Context, contextType, contextID string) ([]DecisionGateSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM stockv2_decision_gate_snapshots
		WHERE context_type=? AND context_id=? ORDER BY created_at, symbol`, strings.TrimSpace(contextType), strings.TrimSpace(contextID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DecisionGateSnapshot
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item DecisionGateSnapshot
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetLatestDecisionGateSnapshotForSymbol(ctx context.Context, symbol string) (DecisionGateSnapshot, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM stockv2_decision_gate_snapshots
		WHERE symbol=? ORDER BY created_at DESC LIMIT 1`, strings.TrimSpace(symbol)).Scan(&payload)
	if err != nil {
		return DecisionGateSnapshot{}, err
	}
	var item DecisionGateSnapshot
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return DecisionGateSnapshot{}, err
	}
	return item, nil
}

func (s *Store) UpsertDecisionMarketEvents(ctx context.Context, items []decisionMarketEvent) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, item := range items {
			_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_decision_market_events
				(symbol,event_type,event_date,announced_at,title,source,fetched_at) VALUES (?,?,?,?,?,?,?)`,
				item.Symbol, item.EventType, item.EventDate, item.AnnouncedAt, nullableString(item.Title), item.Source, item.FetchedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListDecisionMarketEvents(ctx context.Context, symbol, start, end, asOf string) ([]decisionMarketEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,event_type,event_date,announced_at,COALESCE(title,''),source,fetched_at
		FROM stockv2_decision_market_events WHERE symbol=? AND event_date>=? AND event_date<=?
		AND (announced_at='' OR announced_at<=?) ORDER BY event_date,event_type`, symbol, start, end, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisionMarketEvent
	for rows.Next() {
		var item decisionMarketEvent
		if err := rows.Scan(&item.Symbol, &item.EventType, &item.EventDate, &item.AnnouncedAt, &item.Title, &item.Source, &item.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) UpsertDecisionFinancialFacts(ctx context.Context, items []decisionFinancialFact) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, item := range items {
			_, err := tx.ExecContext(ctx, `INSERT INTO stockv2_decision_financial_facts
				(symbol,report_period,dataset,announced_at,revenue,net_profit,operating_cash_flow,roe,gross_margin,source,fetched_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(symbol,report_period,dataset,announced_at) DO UPDATE SET
				revenue=CASE WHEN excluded.revenue!=0 THEN excluded.revenue ELSE revenue END,
				net_profit=CASE WHEN excluded.net_profit!=0 THEN excluded.net_profit ELSE net_profit END,
				operating_cash_flow=CASE WHEN excluded.operating_cash_flow!=0 THEN excluded.operating_cash_flow ELSE operating_cash_flow END,
				roe=CASE WHEN excluded.roe!=0 THEN excluded.roe ELSE roe END,
				gross_margin=CASE WHEN excluded.gross_margin!=0 THEN excluded.gross_margin ELSE gross_margin END,
				source=excluded.source,fetched_at=excluded.fetched_at`, item.Symbol, item.ReportPeriod, item.Dataset, item.AnnouncedAt, item.Revenue,
				item.NetProfit, item.OperatingCashFlow, item.ROE, item.GrossMargin, item.Source, item.FetchedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetLatestDecisionFinancialFact(ctx context.Context, symbol, asOf string) (decisionFinancialFact, error) {
	var period string
	if err := s.db.QueryRowContext(ctx, `SELECT report_period FROM stockv2_decision_financial_facts
		WHERE symbol=? AND (announced_at='' OR announced_at<=?) ORDER BY report_period DESC LIMIT 1`, symbol, asOf).Scan(&period); err != nil {
		return decisionFinancialFact{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,report_period,dataset,announced_at,revenue,net_profit,
		operating_cash_flow,roe,gross_margin,source,fetched_at FROM stockv2_decision_financial_facts
		WHERE symbol=? AND report_period=? AND (announced_at='' OR announced_at<=?)
		ORDER BY dataset,announced_at DESC,fetched_at DESC`, symbol, period, asOf)
	if err != nil {
		return decisionFinancialFact{}, err
	}
	defer rows.Close()
	out := decisionFinancialFact{Symbol: symbol, ReportPeriod: period}
	seen := map[string]bool{}
	for rows.Next() {
		var item decisionFinancialFact
		if err := rows.Scan(&item.Symbol, &item.ReportPeriod, &item.Dataset, &item.AnnouncedAt, &item.Revenue,
			&item.NetProfit, &item.OperatingCashFlow, &item.ROE, &item.GrossMargin, &item.Source, &item.FetchedAt); err != nil {
			return decisionFinancialFact{}, err
		}
		if seen[item.Dataset] {
			continue
		}
		seen[item.Dataset] = true
		if item.AnnouncedAt > out.AnnouncedAt {
			out.AnnouncedAt = item.AnnouncedAt
		}
		out.Revenue += item.Revenue
		out.NetProfit += item.NetProfit
		out.OperatingCashFlow += item.OperatingCashFlow
		out.ROE += item.ROE
		out.GrossMargin += item.GrossMargin
		if out.Source == "" {
			out.Source = item.Source
		} else if out.Source != item.Source {
			out.Source = "mixed"
		}
		if item.FetchedAt.After(out.FetchedAt) {
			out.FetchedAt = item.FetchedAt
		}
	}
	return out, rows.Err()
}

func (s *Store) ListDecisionFinancialFacts(ctx context.Context, symbol, asOf string, limit int) ([]decisionFinancialFact, error) {
	if limit <= 0 || limit > 30 {
		limit = 12
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,report_period,dataset,announced_at,revenue,net_profit,
		operating_cash_flow,roe,gross_margin,source,fetched_at FROM stockv2_decision_financial_facts
		WHERE symbol=? AND (announced_at='' OR announced_at<=?)
		ORDER BY report_period DESC,dataset,announced_at DESC,fetched_at DESC LIMIT ?`,
		strings.TrimSpace(symbol), strings.TrimSpace(asOf), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]decisionFinancialFact, 0, limit)
	for rows.Next() {
		var item decisionFinancialFact
		if err := rows.Scan(&item.Symbol, &item.ReportPeriod, &item.Dataset, &item.AnnouncedAt,
			&item.Revenue, &item.NetProfit, &item.OperatingCashFlow, &item.ROE,
			&item.GrossMargin, &item.Source, &item.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) HasFreshDecisionFinancialDataset(ctx context.Context, symbol, dataset string, fetchedAfter time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM stockv2_decision_financial_facts
		WHERE symbol=? AND dataset=? AND fetched_at>=?
	)`, strings.TrimSpace(symbol), strings.TrimSpace(dataset), fetchedAfter).Scan(&exists)
	return exists, err
}

func decisionFactMissing(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func (s *Store) SaveDecisionReferenceHealth(ctx context.Context, item decisionReferenceHealth) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_decision_reference_health
		(symbol,status,source,message,event_ok,finance_ok,checked_at) VALUES (?,?,?,?,?,?,?)`,
		item.Symbol, item.Status, nullableString(item.Source), nullableString(item.Message), item.EventOK, item.FinanceOK, item.CheckedAt)
	return err
}

func (s *Store) GetDecisionReferenceHealth(ctx context.Context, symbol string) (decisionReferenceHealth, error) {
	var item decisionReferenceHealth
	err := s.db.QueryRowContext(ctx, `SELECT symbol,status,COALESCE(source,''),COALESCE(message,''),event_ok,finance_ok,checked_at
		FROM stockv2_decision_reference_health WHERE symbol=?`, symbol).Scan(&item.Symbol, &item.Status, &item.Source, &item.Message, &item.EventOK, &item.FinanceOK, &item.CheckedAt)
	return item, err
}

func (s *Store) ListDecisionGateOutcomes(ctx context.Context, snapshotID string) ([]DecisionGateOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT snapshot_id,horizon,COALESCE(due_trade_date,''),COALESCE(observed_date,''),
		return_pct,excess_return_pct,status,updated_at FROM stockv2_decision_gate_outcomes
		WHERE snapshot_id=? ORDER BY horizon`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DecisionGateOutcome
	for rows.Next() {
		var item DecisionGateOutcome
		if err := rows.Scan(&item.SnapshotID, &item.Horizon, &item.DueTradeDate, &item.ObservedDate,
			&item.ReturnPct, &item.ExcessReturnPct, &item.Status, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveDecisionGateOutcome(ctx context.Context, item DecisionGateOutcome) error {
	item.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_decision_gate_outcomes
		(snapshot_id,horizon,due_trade_date,observed_date,return_pct,excess_return_pct,status,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, item.SnapshotID, item.Horizon, nullableString(item.DueTradeDate),
		nullableString(item.ObservedDate), item.ReturnPct, item.ExcessReturnPct, item.Status, item.UpdatedAt)
	return err
}

type decisionIndexBar struct {
	Symbol, TradeDate, Source string
	Close, PctChange          float64
	FetchedAt                 time.Time
}

func (s *Store) UpsertDecisionIndexBars(ctx context.Context, items []decisionIndexBar) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, item := range items {
			_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_decision_index_bars
				(symbol,trade_date,close,pct_change,source,fetched_at) VALUES (?,?,?,?,?,?)`,
				item.Symbol, item.TradeDate, item.Close, item.PctChange, item.Source, item.FetchedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetDecisionIndexBars(ctx context.Context, symbol, endDate string, limit int) ([]decisionIndexBar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,trade_date,close,pct_change,source,fetched_at
		FROM stockv2_decision_index_bars WHERE symbol=? AND trade_date<=? ORDER BY trade_date DESC LIMIT ?`,
		symbol, endDate, normalizedPageLimit(limit, 250))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisionIndexBar
	for rows.Next() {
		var item decisionIndexBar
		if err := rows.Scan(&item.Symbol, &item.TradeDate, &item.Close, &item.PctChange, &item.Source, &item.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type decisionTradeDay struct {
	Date, PreviousDate, Source string
	Open                       bool
	FetchedAt                  time.Time
}

func (s *Store) UpsertDecisionTradeCalendar(ctx context.Context, items []decisionTradeDay) error {
	if len(items) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, item := range items {
			_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO stockv2_decision_trade_calendar
				(calendar_date,is_open,previous_trade_date,source,fetched_at) VALUES (?,?,?,?,?)`,
				item.Date, item.Open, nullableString(item.PreviousDate), item.Source, item.FetchedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetDecisionTradeCalendar(ctx context.Context, start, end string) ([]decisionTradeDay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT calendar_date,is_open,COALESCE(previous_trade_date,''),source,fetched_at
		FROM stockv2_decision_trade_calendar WHERE calendar_date>=? AND calendar_date<=? ORDER BY calendar_date`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisionTradeDay
	for rows.Next() {
		var item decisionTradeDay
		if err := rows.Scan(&item.Date, &item.Open, &item.PreviousDate, &item.Source, &item.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
