package stockv2

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreatePortfolioSnapshot(ctx context.Context, snapshot PortfolioSnapshot) error {
	query := `
		INSERT INTO stockv2_portfolio_snapshots (
			id, portfolio_id, valuation_at, cash, holding_market_value, total_asset_value,
			cash_pct, position_count, stale_quote_count, estimated_quote_count, source,
			status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	if snapshot.ID == "" {
		snapshot.ID = generateID()
	}
	if snapshot.ValuationAt.IsZero() {
		snapshot.ValuationAt = now
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, query,
		snapshot.ID,
		snapshot.PortfolioID,
		snapshot.ValuationAt,
		snapshot.Cash,
		snapshot.HoldingMarketValue,
		snapshot.TotalAssetValue,
		snapshot.CashPct,
		snapshot.PositionCount,
		snapshot.StaleQuoteCount,
		snapshot.EstimatedQuoteCount,
		snapshot.Source,
		snapshot.Status,
		snapshot.CreatedAt,
	)
	return wrapError(err, "create portfolio snapshot")
}

func (s *Store) SavePortfolioValuation(ctx context.Context, holdings []StockV2Holding, snapshot PortfolioSnapshot, writeSnapshot ...bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin portfolio valuation transaction")
	}
	defer tx.Rollback()

	for _, holding := range holdings {
		if err := updateHoldingWithTx(ctx, tx, holding); err != nil {
			return err
		}
	}
	shouldWriteSnapshot := true
	if len(writeSnapshot) > 0 {
		shouldWriteSnapshot = writeSnapshot[0]
	}
	if shouldWriteSnapshot {
		if err := insertPortfolioSnapshotWithTx(ctx, tx, snapshot); err != nil {
			return err
		}
	}
	return wrapError(tx.Commit(), "commit portfolio valuation")
}

func (s *Store) GetPortfolioSnapshots(ctx context.Context, portfolioID string, limit int) ([]PortfolioSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, portfolio_id, valuation_at, cash, holding_market_value,
		       total_asset_value, cash_pct, position_count, stale_quote_count,
		       estimated_quote_count, source, status, created_at
		FROM stockv2_portfolio_snapshots
		WHERE portfolio_id = ?
		ORDER BY valuation_at DESC, created_at DESC
		LIMIT ?
	`, portfolioID, limit)
	if err != nil {
		return nil, wrapError(err, "get portfolio snapshots")
	}
	defer rows.Close()

	var snapshots []PortfolioSnapshot
	for rows.Next() {
		var snapshot PortfolioSnapshot
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.PortfolioID,
			&snapshot.ValuationAt,
			&snapshot.Cash,
			&snapshot.HoldingMarketValue,
			&snapshot.TotalAssetValue,
			&snapshot.CashPct,
			&snapshot.PositionCount,
			&snapshot.StaleQuoteCount,
			&snapshot.EstimatedQuoteCount,
			&snapshot.Source,
			&snapshot.Status,
			&snapshot.CreatedAt,
		); err != nil {
			return nil, wrapError(err, "scan portfolio snapshot")
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate portfolio snapshots")
	}
	return snapshots, nil
}

func updateHoldingWithTx(ctx context.Context, tx *sql.Tx, holding StockV2Holding) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_holdings
		SET symbol = ?, market = ?, name = ?, quantity = ?, available_quantity = ?,
		    cost_price = ?, last_price = ?, last_price_at = ?, tradable_status = ?,
		    market_value = ?, pnl = ?, position_pct = ?, updated_at = ?
		WHERE id = ?
	`,
		holding.Symbol,
		holding.Market,
		holding.Name,
		holding.Quantity,
		holding.AvailableQuantity,
		holding.CostPrice,
		holding.LastPrice,
		holding.LastPriceAt,
		holding.TradableStatus,
		holding.MarketValue,
		holding.PnL,
		holding.PositionPct,
		holding.UpdatedAt,
		holding.ID,
	)
	if err != nil {
		return wrapError(err, "update holding valuation")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check holding valuation affected rows")
	}
	if rows == 0 {
		return ErrHoldingNotFound
	}
	return nil
}

func insertPortfolioSnapshotWithTx(ctx context.Context, tx *sql.Tx, snapshot PortfolioSnapshot) error {
	now := time.Now()
	if snapshot.ID == "" {
		snapshot.ID = generateID()
	}
	if snapshot.ValuationAt.IsZero() {
		snapshot.ValuationAt = now
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_portfolio_snapshots (
			id, portfolio_id, valuation_at, cash, holding_market_value, total_asset_value,
			cash_pct, position_count, stale_quote_count, estimated_quote_count, source,
			status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		snapshot.ID,
		snapshot.PortfolioID,
		snapshot.ValuationAt,
		snapshot.Cash,
		snapshot.HoldingMarketValue,
		snapshot.TotalAssetValue,
		snapshot.CashPct,
		snapshot.PositionCount,
		snapshot.StaleQuoteCount,
		snapshot.EstimatedQuoteCount,
		snapshot.Source,
		snapshot.Status,
		snapshot.CreatedAt,
	)
	return wrapError(err, "create portfolio snapshot")
}
