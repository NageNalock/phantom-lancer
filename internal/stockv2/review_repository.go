package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Store) CreateOperationReview(ctx context.Context, review OperationReview) (OperationReview, error) {
	now := time.Now()
	if review.ID == "" {
		review.ID = generateID()
	}
	if review.Status == "" {
		review.Status = OperationReviewStatusPending
	}
	if review.CreatedAt.IsZero() {
		review.CreatedAt = now
	}
	if review.UpdatedAt.IsZero() {
		review.UpdatedAt = now
	}
	if review.Result == nil {
		review.Result = map[string]any{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_operation_reviews (
			id, hit_id, run_id, status, output_type, strategy_id, portfolio_id, symbol, market,
			input_context_json, result_json, result_summary, error_message,
			created_at, updated_at, completed_at, closed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		review.ID,
		review.HitID,
		nullableString(review.RunID),
		review.Status,
		nullableString(review.OutputType),
		nullableString(review.StrategyID),
		nullableString(review.PortfolioID),
		nullableString(review.Symbol),
		nullableString(review.Market),
		marshalJSON(review.InputContext),
		marshalMap(review.Result),
		nullableString(safelog.Text(review.ResultSummary, 800)),
		nullableString(safelog.Text(review.ErrorMessage, 400)),
		review.CreatedAt,
		review.UpdatedAt,
		nullableTime(review.CompletedAt),
		nullableTime(review.ClosedAt),
	)
	if err != nil {
		return OperationReview{}, wrapError(err, "create operation review")
	}
	return review, nil
}

func (s *Store) GetOperationReview(ctx context.Context, id string) (OperationReview, error) {
	row := s.db.QueryRowContext(ctx, operationReviewSelectSQL+" WHERE id = ?", id)
	review, err := scanOperationReview(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OperationReview{}, ErrOperationReviewNotFound
		}
		return OperationReview{}, wrapError(err, "get operation review")
	}
	return review, nil
}

func (s *Store) GetActiveOperationReviewByHit(ctx context.Context, hitID string) (*OperationReview, error) {
	row := s.db.QueryRowContext(ctx, operationReviewSelectSQL+`
		WHERE hit_id = ? AND status <> ?
		ORDER BY created_at DESC
		LIMIT 1
	`, hitID, OperationReviewStatusClosed)
	review, err := scanOperationReview(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapError(err, "get active operation review by hit")
	}
	return &review, nil
}

func (s *Store) ListOperationReviews(ctx context.Context, filter OperationReviewListFilter) ([]OperationReview, error) {
	where, args := operationReviewFilterSQL(filter)
	limit := normalizedPageLimit(filter.Limit, 200)
	offset := normalizedPageOffset(filter.Offset)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		%s WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?
	`, operationReviewSelectSQL, where), args...)
	if err != nil {
		return nil, wrapError(err, "list operation reviews")
	}
	return scanRows(rows, scanOperationReview, "scan operation review", "iterate operation reviews")
}

func (s *Store) CountOperationReviews(ctx context.Context, filter OperationReviewListFilter) (int, error) {
	where, args := operationReviewFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM stockv2_operation_reviews WHERE %s`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count operation reviews")
	}
	return count, nil
}

func (s *Store) SaveOperationReviewResult(ctx context.Context, review OperationReview) (OperationReview, error) {
	review.UpdatedAt = time.Now()
	review.ResultSummary = safelog.Text(review.ResultSummary, 800)
	review.ErrorMessage = safelog.Text(review.ErrorMessage, 400)
	result, err := s.db.ExecContext(ctx, `
		UPDATE stockv2_operation_reviews
		SET status = ?, output_type = ?, result_json = ?, result_summary = ?,
		    error_message = ?, updated_at = ?, completed_at = ?, closed_at = ?
		WHERE id = ?
	`,
		review.Status,
		nullableString(review.OutputType),
		marshalMap(review.Result),
		nullableString(review.ResultSummary),
		nullableString(review.ErrorMessage),
		review.UpdatedAt,
		nullableTime(review.CompletedAt),
		nullableTime(review.ClosedAt),
		review.ID,
	)
	if err != nil {
		return OperationReview{}, wrapError(err, "save operation review result")
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return OperationReview{}, ErrOperationReviewNotFound
	}
	return s.GetOperationReview(ctx, review.ID)
}

const operationReviewSelectSQL = `
	SELECT id, hit_id, COALESCE(run_id,''), status, COALESCE(output_type,''),
	       COALESCE(strategy_id,''), COALESCE(portfolio_id,''), COALESCE(symbol,''),
	       COALESCE(market,''), input_context_json, result_json,
	       COALESCE(result_summary,''), COALESCE(error_message,''),
	       created_at, updated_at, completed_at, closed_at
	FROM stockv2_operation_reviews
`

func scanOperationReview(row rowScanner) (OperationReview, error) {
	var review OperationReview
	var inputJSON, resultJSON sql.NullString
	var completedAt, closedAt sql.NullTime
	if err := row.Scan(
		&review.ID,
		&review.HitID,
		&review.RunID,
		&review.Status,
		&review.OutputType,
		&review.StrategyID,
		&review.PortfolioID,
		&review.Symbol,
		&review.Market,
		&inputJSON,
		&resultJSON,
		&review.ResultSummary,
		&review.ErrorMessage,
		&review.CreatedAt,
		&review.UpdatedAt,
		&completedAt,
		&closedAt,
	); err != nil {
		return review, err
	}
	review.InputContext = unmarshalAgentContextPack(inputJSON.String)
	review.Result = unmarshalMap(resultJSON.String)
	if completedAt.Valid {
		review.CompletedAt = completedAt.Time
	}
	if closedAt.Valid {
		review.ClosedAt = closedAt.Time
	}
	return review, nil
}

func operationReviewFilterSQL(filter OperationReviewListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, strings.TrimSpace(value))
	}
	add("status", filter.Status)
	add("output_type", filter.OutputType)
	add("hit_id", filter.HitID)
	add("run_id", filter.RunID)
	add("strategy_id", filter.StrategyID)
	add("portfolio_id", filter.PortfolioID)
	add("symbol", filter.Symbol)
	return strings.Join(where, " AND "), args
}

func marshalJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func unmarshalAgentContextPack(raw string) AgentContextPack {
	var pack AgentContextPack
	if raw == "" {
		return pack
	}
	_ = json.Unmarshal([]byte(raw), &pack)
	return pack
}
