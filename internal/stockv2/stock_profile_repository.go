package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) UpsertStockProfile(ctx context.Context, profile StockProfile) (StockProfile, error) {
	now := time.Now()
	profile.UpdatedAt = now
	profile.InstrumentType = normalizeInstrumentType(profile.InstrumentType)
	if profile.ProfileVersion <= 0 {
		profile.ProfileVersion = 1
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = profile.Symbol
	}

	aliasesJSON := marshalProfileStrings(profile.Aliases)
	sectorsJSON := marshalProfileStrings(profile.Sectors)
	conceptsJSON := marshalProfileStrings(profile.Concepts)
	tagsJSON := marshalProfileStrings(profile.Tags)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_stock_profiles (
			symbol, market, instrument_type, name, aliases_json, industry, sectors_json,
			concepts_json, tags_json, business_summary, profile_text, fund_type,
			tracking_index, theme, constituent_hint, profile_version, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			market = excluded.market,
			instrument_type = excluded.instrument_type,
			name = excluded.name,
			aliases_json = excluded.aliases_json,
			industry = excluded.industry,
			sectors_json = excluded.sectors_json,
			concepts_json = excluded.concepts_json,
			tags_json = excluded.tags_json,
			business_summary = excluded.business_summary,
			profile_text = excluded.profile_text,
			fund_type = excluded.fund_type,
			tracking_index = excluded.tracking_index,
			theme = excluded.theme,
			constituent_hint = excluded.constituent_hint,
			profile_version = excluded.profile_version,
			updated_at = excluded.updated_at
	`, profile.Symbol, profile.Market, profile.InstrumentType, profile.Name, aliasesJSON,
		profile.Industry, sectorsJSON, conceptsJSON, tagsJSON, profile.BusinessSummary,
		profile.ProfileText, profile.FundType, profile.TrackingIndex, profile.Theme,
		profile.ConstituentHint, profile.ProfileVersion, profile.UpdatedAt)
	if err != nil {
		return StockProfile{}, wrapError(err, "upsert stock profile")
	}
	return profile, nil
}

func (s *Store) GetStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	row := s.db.QueryRowContext(ctx, stockProfileSelectSQL()+` WHERE symbol = ?`, strings.TrimSpace(symbol))
	profile, err := scanStockProfile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StockProfile{}, ErrStockProfileNotFound
		}
		return StockProfile{}, wrapError(err, "get stock profile")
	}
	return profile, nil
}

func (s *Store) ListStockProfiles(ctx context.Context, filter StockProfileListFilter) ([]StockProfile, error) {
	where, args := stockProfileWhere(filter)
	args = append(args, normalizedStockProfileLimit(filter.Limit), normalizedStockProfileOffset(filter.Offset))
	rows, err := s.db.QueryContext(ctx, stockProfileSelectSQL()+where+` ORDER BY updated_at DESC, symbol ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrapError(err, "list stock profiles")
	}
	defer rows.Close()

	items := make([]StockProfile, 0)
	for rows.Next() {
		item, err := scanStockProfile(rows)
		if err != nil {
			return nil, wrapError(err, "scan stock profile")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, "iterate stock profiles")
	}
	return items, nil
}

func (s *Store) CountStockProfiles(ctx context.Context, filter StockProfileListFilter) (int, error) {
	where, args := stockProfileWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_stock_profiles`+where, args...).Scan(&total)
	return total, wrapError(err, "count stock profiles")
}

func stockProfileSelectSQL() string {
	return `
		SELECT symbol, market, COALESCE(instrument_type,'stock'), name, aliases_json,
		       COALESCE(industry,''), sectors_json, concepts_json, tags_json,
		       COALESCE(business_summary,''), profile_text, COALESCE(fund_type,''),
		       COALESCE(tracking_index,''), COALESCE(theme,''), COALESCE(constituent_hint,''),
		       profile_version, updated_at
		FROM stockv2_stock_profiles`
}

func stockProfileWhere(filter StockProfileListFilter) (string, []any) {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if market := strings.TrimSpace(filter.Market); market != "" {
		parts = append(parts, "market = ?")
		args = append(args, market)
	}
	if instrumentType := strings.TrimSpace(filter.InstrumentType); instrumentType != "" {
		parts = append(parts, "instrument_type = ?")
		args = append(args, normalizeInstrumentType(instrumentType))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		parts = append(parts, "(LOWER(symbol) LIKE ? OR LOWER(name) LIKE ? OR LOWER(profile_text) LIKE ?)")
		args = append(args, pattern, pattern, pattern)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

type stockProfileScanner interface {
	Scan(dest ...any) error
}

func scanStockProfile(scanner stockProfileScanner) (StockProfile, error) {
	var profile StockProfile
	var aliasesJSON, sectorsJSON, conceptsJSON, tagsJSON string
	if err := scanner.Scan(
		&profile.Symbol,
		&profile.Market,
		&profile.InstrumentType,
		&profile.Name,
		&aliasesJSON,
		&profile.Industry,
		&sectorsJSON,
		&conceptsJSON,
		&tagsJSON,
		&profile.BusinessSummary,
		&profile.ProfileText,
		&profile.FundType,
		&profile.TrackingIndex,
		&profile.Theme,
		&profile.ConstituentHint,
		&profile.ProfileVersion,
		&profile.UpdatedAt,
	); err != nil {
		return StockProfile{}, err
	}
	profile.InstrumentType = normalizeInstrumentType(profile.InstrumentType)
	profile.Aliases = unmarshalProfileStrings(aliasesJSON)
	profile.Sectors = unmarshalProfileStrings(sectorsJSON)
	profile.Concepts = unmarshalProfileStrings(conceptsJSON)
	profile.Tags = unmarshalProfileStrings(tagsJSON)
	if profile.ProfileVersion <= 0 {
		profile.ProfileVersion = 1
	}
	return profile, nil
}

func marshalProfileStrings(items []string) string {
	data, _ := json.Marshal(items)
	return string(data)
}

func unmarshalProfileStrings(raw string) []string {
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	return items
}

func normalizedStockProfileLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func normalizedStockProfileOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
