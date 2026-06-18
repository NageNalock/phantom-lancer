package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Store) UpsertLatestQuote(ctx context.Context, quote StockV2QuoteLatest) error {
	query := `
		INSERT INTO stockv2_quotes_latest (
			symbol, market, name, last_price, prev_close, open_price, high_price,
			low_price, volume, amount, pct_change, quote_at, fetched_at, source,
			status, error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market,
			name = excluded.name,
			last_price = excluded.last_price,
			prev_close = excluded.prev_close,
			open_price = excluded.open_price,
			high_price = excluded.high_price,
			low_price = excluded.low_price,
			volume = excluded.volume,
			amount = excluded.amount,
			pct_change = excluded.pct_change,
			quote_at = excluded.quote_at,
			fetched_at = excluded.fetched_at,
			source = excluded.source,
			status = excluded.status,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`

	now := time.Now()
	if quote.FetchedAt.IsZero() {
		quote.FetchedAt = now
	}
	if quote.QuoteAt.IsZero() {
		quote.QuoteAt = quote.FetchedAt
	}
	if quote.Status == "" {
		quote.Status = QuoteStatusFresh
	}

	_, err := s.db.ExecContext(ctx, query,
		quote.Symbol,
		quote.Market,
		quote.Name,
		quote.LastPrice,
		quote.PrevClose,
		quote.OpenPrice,
		quote.HighPrice,
		quote.LowPrice,
		quote.Volume,
		quote.Amount,
		quote.PctChange,
		quote.QuoteAt,
		quote.FetchedAt,
		quote.Source,
		quote.Status,
		quote.ErrorMessage,
		now,
		now,
	)
	return wrapError(err, "upsert latest quote")
}

func (s *Store) GetLatestQuotes(ctx context.Context, symbols []string) ([]StockV2QuoteLatest, error) {
	if len(symbols) == 0 {
		return []StockV2QuoteLatest{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]any, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, symbol)
	}

	query := fmt.Sprintf(`
		SELECT symbol, market, COALESCE(name,''), last_price, prev_close, open_price,
		       high_price, low_price, volume, amount, pct_change, quote_at, fetched_at,
		       source, status, COALESCE(error_message,'')
		FROM stockv2_quotes_latest
		WHERE symbol IN (%s)
	`, placeholders)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapError(err, "get latest quotes")
	}
	defer rows.Close()

	bySymbol := make(map[string]StockV2QuoteLatest, len(symbols))
	for rows.Next() {
		quote, err := scanLatestQuote(rows)
		if err != nil {
			return nil, wrapError(err, "scan latest quote")
		}
		bySymbol[quote.Symbol] = quote
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate latest quotes")
	}

	items := make([]StockV2QuoteLatest, 0, len(bySymbol))
	for _, symbol := range symbols {
		if quote, ok := bySymbol[symbol]; ok {
			items = append(items, quote)
		}
	}
	return items, nil
}

func (s *Store) MarkLatestQuoteFailed(ctx context.Context, symbol, reason string) (StockV2QuoteLatest, bool, error) {
	reason = safelog.Text(reason, 240)
	quote, err := s.getLatestQuote(ctx, symbol)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2QuoteLatest{}, false, nil
		}
		return StockV2QuoteLatest{}, false, wrapError(err, "get latest quote before failed mark")
	}

	now := time.Now()
	_, err = s.db.ExecContext(ctx, `
		UPDATE stockv2_quotes_latest
		SET status = ?, error_message = ?, updated_at = ?
		WHERE symbol = ?
	`, QuoteStatusFailed, reason, now, symbol)
	if err != nil {
		return StockV2QuoteLatest{}, false, wrapError(err, "mark latest quote failed")
	}

	quote.Status = QuoteStatusFailed
	quote.ErrorMessage = reason
	return quote, true, nil
}

func (s *Store) getLatestQuote(ctx context.Context, symbol string) (StockV2QuoteLatest, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT symbol, market, COALESCE(name,''), last_price, prev_close, open_price,
		       high_price, low_price, volume, amount, pct_change, quote_at, fetched_at,
		       source, status, COALESCE(error_message,'')
		FROM stockv2_quotes_latest
		WHERE symbol = ?
	`, symbol)
	return scanLatestQuote(row)
}

func scanLatestQuote(row rowScanner) (StockV2QuoteLatest, error) {
	var quote StockV2QuoteLatest
	err := row.Scan(
		&quote.Symbol,
		&quote.Market,
		&quote.Name,
		&quote.LastPrice,
		&quote.PrevClose,
		&quote.OpenPrice,
		&quote.HighPrice,
		&quote.LowPrice,
		&quote.Volume,
		&quote.Amount,
		&quote.PctChange,
		&quote.QuoteAt,
		&quote.FetchedAt,
		&quote.Source,
		&quote.Status,
		&quote.ErrorMessage,
	)
	return quote, err
}
