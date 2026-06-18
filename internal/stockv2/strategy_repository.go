package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateStrategyWithVersion(ctx context.Context, strategy StockV2Strategy, version StockV2StrategyVersion) (StrategyWithVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StrategyWithVersion{}, wrapError(err, "begin create strategy transaction")
	}
	defer tx.Rollback()

	now := time.Now()
	if strategy.ID == "" {
		strategy.ID = generateID()
	}
	strategy.CreatedAt = now
	strategy.UpdatedAt = now
	strategy.ActiveVersionID = ""

	if err := insertStrategyWithTx(ctx, tx, strategy); err != nil {
		return StrategyWithVersion{}, err
	}

	if version.ID == "" {
		version.ID = generateID()
	}
	version.StrategyID = strategy.ID
	version.VersionNo = 1
	version.CreatedAt = now
	if version.CreatedBy == "" {
		version.CreatedBy = strategy.Source
	}
	if err := insertStrategyVersionWithTx(ctx, tx, version); err != nil {
		return StrategyWithVersion{}, err
	}

	strategy.ActiveVersionID = version.ID
	if err := updateStrategyWithTx(ctx, tx, strategy); err != nil {
		return StrategyWithVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return StrategyWithVersion{}, wrapError(err, "commit create strategy")
	}
	return StrategyWithVersion{Strategy: strategy, ActiveVersion: &version}, nil
}

func (s *Store) GetStrategy(ctx context.Context, id string) (StrategyWithVersion, error) {
	strategy, err := s.getStrategy(ctx, id)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	item := StrategyWithVersion{Strategy: strategy}
	if strategy.ActiveVersionID != "" {
		version, err := s.getStrategyVersionByID(ctx, strategy.ActiveVersionID)
		if err != nil && !errors.Is(err, ErrStrategyVersionNotFound) {
			return StrategyWithVersion{}, err
		}
		if err == nil {
			item.ActiveVersion = &version
		}
	}
	return item, nil
}

func (s *Store) ListStrategies(ctx context.Context, filter StrategyListFilter) ([]StrategyWithVersion, error) {
	where, args := strategyFilterSQL(filter)
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, kind, scope, source, status, symbol, market, portfolio_id,
		       active_version_id, created_at, updated_at, archived_at
		FROM stockv2_strategies
		WHERE %s
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, where), args...)
	if err != nil {
		return nil, wrapError(err, "list strategies")
	}

	strategies := make([]StockV2Strategy, 0)
	for rows.Next() {
		strategy, err := scanStrategy(rows)
		if err != nil {
			_ = rows.Close()
			return nil, wrapError(err, "scan strategy")
		}
		strategies = append(strategies, strategy)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, wrapError(err, "iterate strategies")
	}
	if err := rows.Close(); err != nil {
		return nil, wrapError(err, "close strategies rows")
	}

	items := make([]StrategyWithVersion, 0, len(strategies))
	for _, strategy := range strategies {
		item := StrategyWithVersion{Strategy: strategy}
		if strategy.ActiveVersionID != "" {
			version, err := s.getStrategyVersionByID(ctx, strategy.ActiveVersionID)
			if err != nil && !errors.Is(err, ErrStrategyVersionNotFound) {
				return nil, err
			}
			if err == nil {
				item.ActiveVersion = &version
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) CountStrategies(ctx context.Context, filter StrategyListFilter) (int, error) {
	where, args := strategyFilterSQL(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM stockv2_strategies
		WHERE %s
	`, where), args...).Scan(&count); err != nil {
		return 0, wrapError(err, "count strategies")
	}
	return count, nil
}

func (s *Store) UpdateStrategyWithVersion(ctx context.Context, strategy StockV2Strategy, version *StockV2StrategyVersion) (StrategyWithVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StrategyWithVersion{}, wrapError(err, "begin update strategy transaction")
	}
	defer tx.Rollback()

	var activeVersion *StockV2StrategyVersion
	if version != nil {
		nextVersion, err := nextStrategyVersionNo(ctx, tx, strategy.ID)
		if err != nil {
			return StrategyWithVersion{}, err
		}
		version.ID = generateID()
		version.StrategyID = strategy.ID
		version.VersionNo = nextVersion
		version.CreatedAt = time.Now()
		if version.CreatedBy == "" {
			version.CreatedBy = strategy.Source
		}
		if err := insertStrategyVersionWithTx(ctx, tx, *version); err != nil {
			return StrategyWithVersion{}, err
		}
		strategy.ActiveVersionID = version.ID
		activeVersion = version
	}
	strategy.UpdatedAt = time.Now()

	if err := updateStrategyWithTx(ctx, tx, strategy); err != nil {
		return StrategyWithVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return StrategyWithVersion{}, wrapError(err, "commit update strategy")
	}
	item := StrategyWithVersion{Strategy: strategy, ActiveVersion: activeVersion}
	if activeVersion == nil && strategy.ActiveVersionID != "" {
		version, err := s.getStrategyVersionByID(ctx, strategy.ActiveVersionID)
		if err != nil && !errors.Is(err, ErrStrategyVersionNotFound) {
			return StrategyWithVersion{}, err
		}
		if err == nil {
			item.ActiveVersion = &version
		}
	}
	return item, nil
}

func (s *Store) ListStrategyVersions(ctx context.Context, strategyID string) ([]StockV2StrategyVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, strategy_id, version_no, title, direction, thesis,
		       entry_conditions_json, exit_conditions_json, risk_notes,
		       evidence_refs_json, generation_meta_json, created_by, created_at
		FROM stockv2_strategy_versions
		WHERE strategy_id = ?
		ORDER BY version_no ASC
	`, strategyID)
	if err != nil {
		return nil, wrapError(err, "list strategy versions")
	}
	defer rows.Close()

	versions := make([]StockV2StrategyVersion, 0)
	for rows.Next() {
		version, err := scanStrategyVersion(rows)
		if err != nil {
			return nil, wrapError(err, "scan strategy version")
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate strategy versions")
	}
	return versions, nil
}

func (s *Store) getStrategy(ctx context.Context, id string) (StockV2Strategy, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, scope, source, status, symbol, market, portfolio_id,
		       active_version_id, created_at, updated_at, archived_at
		FROM stockv2_strategies
		WHERE id = ?
	`, id)
	strategy, err := scanStrategy(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2Strategy{}, ErrStrategyNotFound
		}
		return StockV2Strategy{}, wrapError(err, "get strategy")
	}
	return strategy, nil
}

func (s *Store) getStrategyVersionByID(ctx context.Context, id string) (StockV2StrategyVersion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, strategy_id, version_no, title, direction, thesis,
		       entry_conditions_json, exit_conditions_json, risk_notes,
		       evidence_refs_json, generation_meta_json, created_by, created_at
		FROM stockv2_strategy_versions
		WHERE id = ?
	`, id)
	version, err := scanStrategyVersion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockV2StrategyVersion{}, ErrStrategyVersionNotFound
		}
		return StockV2StrategyVersion{}, wrapError(err, "get strategy version")
	}
	return version, nil
}

func insertStrategyWithTx(ctx context.Context, tx *sql.Tx, strategy StockV2Strategy) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_strategies (
			id, name, kind, scope, source, status, symbol, market, portfolio_id,
			active_version_id, created_at, updated_at, archived_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		strategy.ID,
		strategy.Name,
		strategy.Kind,
		strategy.Scope,
		strategy.Source,
		strategy.Status,
		nullableStrategyString(strategy.Symbol),
		nullableStrategyString(strategy.Market),
		nullableStrategyString(strategy.PortfolioID),
		nullableStrategyString(strategy.ActiveVersionID),
		strategy.CreatedAt,
		strategy.UpdatedAt,
		nullableStrategyTime(strategy.ArchivedAt),
	)
	return wrapError(err, "insert strategy")
}

func updateStrategyWithTx(ctx context.Context, tx *sql.Tx, strategy StockV2Strategy) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_strategies
		SET name = ?, kind = ?, scope = ?, source = ?, status = ?, symbol = ?,
		    market = ?, portfolio_id = ?, active_version_id = ?, updated_at = ?,
		    archived_at = ?
		WHERE id = ?
	`,
		strategy.Name,
		strategy.Kind,
		strategy.Scope,
		strategy.Source,
		strategy.Status,
		nullableStrategyString(strategy.Symbol),
		nullableStrategyString(strategy.Market),
		nullableStrategyString(strategy.PortfolioID),
		nullableStrategyString(strategy.ActiveVersionID),
		strategy.UpdatedAt,
		nullableStrategyTime(strategy.ArchivedAt),
		strategy.ID,
	)
	if err != nil {
		return wrapError(err, "update strategy")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return wrapError(err, "check strategy affected rows")
	}
	if rows == 0 {
		return ErrStrategyNotFound
	}
	return nil
}

func insertStrategyVersionWithTx(ctx context.Context, tx *sql.Tx, version StockV2StrategyVersion) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_strategy_versions (
			id, strategy_id, version_no, title, direction, thesis,
			entry_conditions_json, exit_conditions_json, risk_notes,
			evidence_refs_json, generation_meta_json, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		version.ID,
		version.StrategyID,
		version.VersionNo,
		version.Title,
		version.Direction,
		version.Thesis,
		marshalStrings(version.EntryConditions),
		marshalStrings(version.ExitConditions),
		version.RiskNotes,
		marshalStrings(version.EvidenceRefs),
		marshalMap(version.GenerationMeta),
		version.CreatedBy,
		version.CreatedAt,
	)
	return wrapError(err, "insert strategy version")
}

func nextStrategyVersionNo(ctx context.Context, tx *sql.Tx, strategyID string) (int, error) {
	var next int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) + 1
		FROM stockv2_strategy_versions
		WHERE strategy_id = ?
	`, strategyID).Scan(&next); err != nil {
		return 0, wrapError(err, "next strategy version")
	}
	return next, nil
}

func scanStrategy(row rowScanner) (StockV2Strategy, error) {
	var strategy StockV2Strategy
	var symbol, market, portfolioID, activeVersionID sql.NullString
	var archivedAt sql.NullTime
	err := row.Scan(
		&strategy.ID,
		&strategy.Name,
		&strategy.Kind,
		&strategy.Scope,
		&strategy.Source,
		&strategy.Status,
		&symbol,
		&market,
		&portfolioID,
		&activeVersionID,
		&strategy.CreatedAt,
		&strategy.UpdatedAt,
		&archivedAt,
	)
	if err != nil {
		return strategy, err
	}
	strategy.Symbol = symbol.String
	strategy.Market = market.String
	strategy.PortfolioID = portfolioID.String
	strategy.ActiveVersionID = activeVersionID.String
	if archivedAt.Valid {
		strategy.ArchivedAt = archivedAt.Time
	}
	return strategy, nil
}

func scanStrategyVersion(row rowScanner) (StockV2StrategyVersion, error) {
	var version StockV2StrategyVersion
	var entryJSON, exitJSON, evidenceJSON, metaJSON sql.NullString
	err := row.Scan(
		&version.ID,
		&version.StrategyID,
		&version.VersionNo,
		&version.Title,
		&version.Direction,
		&version.Thesis,
		&entryJSON,
		&exitJSON,
		&version.RiskNotes,
		&evidenceJSON,
		&metaJSON,
		&version.CreatedBy,
		&version.CreatedAt,
	)
	if err != nil {
		return version, err
	}
	version.EntryConditions = unmarshalStrings(entryJSON.String)
	version.ExitConditions = unmarshalStrings(exitJSON.String)
	version.EvidenceRefs = unmarshalStrings(evidenceJSON.String)
	version.GenerationMeta = unmarshalMap(metaJSON.String)
	return version, nil
}

func strategyFilterSQL(filter StrategyListFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0)
	add := func(column, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		where = append(where, column+" = ?")
		args = append(args, value)
	}
	add("kind", filter.Kind)
	add("scope", filter.Scope)
	add("source", filter.Source)
	add("status", filter.Status)
	add("symbol", filter.Symbol)
	add("portfolio_id", filter.PortfolioID)
	return strings.Join(where, " AND "), args
}

func marshalStrings(items []string) string {
	if items == nil {
		items = []string{}
	}
	data, _ := json.Marshal(items)
	return string(data)
}

func unmarshalStrings(raw string) []string {
	var items []string
	if raw == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	return items
}

func marshalMap(items map[string]any) string {
	if items == nil {
		items = map[string]any{}
	}
	data, _ := json.Marshal(items)
	return string(data)
}

func unmarshalMap(raw string) map[string]any {
	items := map[string]any{}
	if raw == "" {
		return items
	}
	_ = json.Unmarshal([]byte(raw), &items)
	return items
}

func nullableStrategyString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableStrategyTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
