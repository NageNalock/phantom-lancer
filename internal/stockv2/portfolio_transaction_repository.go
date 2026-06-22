package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// transactionColumns 与 stockv2_transactions 表结构对齐,供 INSERT/SELECT 复用。
const transactionSelectColumns = `
    id, portfolio_id, symbol, COALESCE(market,''), COALESCE(name,''), side, quantity, price,
    amount, executed_at, COALESCE(note,''), created_at
`

func scanTransaction(row interface {
	Scan(dest ...any) error
}) (StockV2Transaction, error) {
	var t StockV2Transaction
	if err := row.Scan(
		&t.ID,
		&t.PortfolioID,
		&t.Symbol,
		&t.Market,
		&t.Name,
		&t.Side,
		&t.Quantity,
		&t.Price,
		&t.Amount,
		&t.ExecutedAt,
		&t.Note,
		&t.CreatedAt,
	); err != nil {
		return StockV2Transaction{}, wrapError(err, "scan transaction")
	}
	return t, nil
}

// CreateTransaction 写入一条交易流水(非事务版本,主要供测试用)。
func (s *Store) CreateTransaction(ctx context.Context, t StockV2Transaction) error {
	now := time.Now()
	if t.ID == "" {
		t.ID = generateID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_transactions (
			id, portfolio_id, symbol, market, name, side, quantity, price, amount,
			executed_at, note, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.ID, t.PortfolioID, t.Symbol, t.Market, t.Name, t.Side,
		t.Quantity, t.Price, t.Amount, t.ExecutedAt, t.Note, t.CreatedAt,
	)
	return wrapError(err, "create transaction")
}

// ListTransactions 列出组合的交易流水。limit<=0 表示不限(给资产曲线回算用);
// limit>0 时按时间倒序截断(给流水列表用)。
func (s *Store) ListTransactions(ctx context.Context, portfolioID string, limit int) ([]StockV2Transaction, error) {
	if limit > 0 {
		rows, err := s.db.QueryContext(ctx, `
			SELECT `+transactionSelectColumns+`
			FROM stockv2_transactions
			WHERE portfolio_id = ?
			ORDER BY executed_at DESC, created_at DESC
			LIMIT ?
		`, portfolioID, limit)
		if err != nil {
			return nil, wrapError(err, "list transactions")
		}
		defer rows.Close()
		return collectTransactions(rows)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionSelectColumns+`
		FROM stockv2_transactions
		WHERE portfolio_id = ?
		ORDER BY executed_at ASC, created_at ASC
	`, portfolioID)
	if err != nil {
		return nil, wrapError(err, "list transactions")
	}
	defer rows.Close()
	return collectTransactions(rows)
}

func collectTransactions(rows *sql.Rows) ([]StockV2Transaction, error) {
	items := make([]StockV2Transaction, 0)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate transactions")
	}
	return items, nil
}

// GetTransaction 按 id 取单条交易流水。
func (s *Store) GetTransaction(ctx context.Context, id string) (StockV2Transaction, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+transactionSelectColumns+`
		FROM stockv2_transactions
		WHERE id = ?
	`, id)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Transaction{}, ErrTransactionNotFound
		}
		return StockV2Transaction{}, err
	}
	return t, nil
}

// DeleteTransaction 按 id 删除单条交易流水(第一版 UI 不暴露,预留给冲账场景)。
func (s *Store) DeleteTransaction(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM stockv2_transactions WHERE id = ?`, id)
	if err != nil {
		return wrapError(err, "delete transaction")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check transaction affected rows")
	}
	if rows == 0 {
		return ErrTransactionNotFound
	}
	return nil
}

// --- 事务内辅助(*WithTx)---

// insertTransactionWithTx 在事务里写一条流水。CreatedAt 由调用方设好。
func insertTransactionWithTx(ctx context.Context, tx *sql.Tx, t StockV2Transaction) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_transactions (
			id, portfolio_id, symbol, market, name, side, quantity, price, amount,
			executed_at, note, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		t.ID, t.PortfolioID, t.Symbol, t.Market, t.Name, t.Side,
		t.Quantity, t.Price, t.Amount, t.ExecutedAt, t.Note, t.CreatedAt,
	)
	return wrapError(err, "insert transaction")
}

// getPortfolioCashWithTx 读取组合当前现金;组合不存在返回 ErrPortfolioNotFound。
func getPortfolioCashWithTx(ctx context.Context, tx *sql.Tx, portfolioID string) (float64, error) {
	var cash float64
	err := tx.QueryRowContext(ctx, `SELECT cash FROM stockv2_portfolios WHERE id = ?`, portfolioID).Scan(&cash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrPortfolioNotFound
		}
		return 0, wrapError(err, "get portfolio cash")
	}
	return cash, nil
}

// updatePortfolioCashWithTx 更新组合现金与 updated_at。
func updatePortfolioCashWithTx(ctx context.Context, tx *sql.Tx, portfolioID string, newCash float64, updatedAt time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE stockv2_portfolios SET cash = ?, updated_at = ? WHERE id = ?`, newCash, updatedAt, portfolioID)
	if err != nil {
		return wrapError(err, "update portfolio cash")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check portfolio cash affected rows")
	}
	if rows == 0 {
		return ErrPortfolioNotFound
	}
	return nil
}

// getHoldingBySymbolWithTx 按 (portfolioID, symbol) 取持仓;found=false 表示当前无持仓。
func getHoldingBySymbolWithTx(ctx context.Context, tx *sql.Tx, portfolioID, symbol string) (StockV2Holding, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, portfolio_id, symbol, COALESCE(market,''), COALESCE(name,''), quantity, available_quantity,
		       cost_price, last_price, last_price_at, COALESCE(tradable_status,'unknown'), market_value,
		       pnl, position_pct, acquired_at, created_at, updated_at
		FROM stockv2_holdings
		WHERE portfolio_id = ? AND symbol = ?
		LIMIT 1
	`, portfolioID, symbol)
	var holding StockV2Holding
	var lastPriceAt sql.NullTime
	var acquiredAt sql.NullTime
	err := row.Scan(
		&holding.ID,
		&holding.PortfolioID,
		&holding.Symbol,
		&holding.Market,
		&holding.Name,
		&holding.Quantity,
		&holding.AvailableQuantity,
		&holding.CostPrice,
		&holding.LastPrice,
		&lastPriceAt,
		&holding.TradableStatus,
		&holding.MarketValue,
		&holding.PnL,
		&holding.PositionPct,
		&acquiredAt,
		&holding.CreatedAt,
		&holding.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Holding{}, false, nil
		}
		return StockV2Holding{}, false, wrapError(err, "get holding by symbol")
	}
	if lastPriceAt.Valid {
		holding.LastPriceAt = lastPriceAt.Time
	}
	if acquiredAt.Valid {
		holding.AcquiredAt = acquiredAt.Time
	} else {
		holding.AcquiredAt = holding.CreatedAt
	}
	return holding, true, nil
}

// createHoldingWithTx 在事务里新建一条持仓(CreatedAt/UpdatedAt/AcquiredAt 由调用方设好)。
func createHoldingWithTx(ctx context.Context, tx *sql.Tx, holding StockV2Holding) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_holdings (
			id, portfolio_id, symbol, market, name, quantity, available_quantity,
			cost_price, last_price, last_price_at, tradable_status, market_value,
			pnl, position_pct, acquired_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		holding.ID, holding.PortfolioID, holding.Symbol, holding.Market, holding.Name,
		holding.Quantity, holding.AvailableQuantity, holding.CostPrice,
		holding.LastPrice, holding.LastPriceAt, holding.TradableStatus,
		holding.MarketValue, holding.PnL, holding.PositionPct,
		holding.AcquiredAt, holding.CreatedAt, holding.UpdatedAt,
	)
	return wrapError(err, "create holding in tx")
}

// deleteHoldingWithTx 在事务里按 id 删除持仓。
func deleteHoldingWithTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM stockv2_holdings WHERE id = ?`, id)
	return wrapError(err, "delete holding in tx")
}
