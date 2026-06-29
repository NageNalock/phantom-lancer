package stockv2

import (
	"database/sql"
	"strings"
	"time"
)

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func normalizedPageLimit(limit, max int) int {
	if limit <= 0 {
		return 50
	}
	if limit > max {
		return max
	}
	return limit
}

func normalizedPageOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func compactStringList(values []string, limit int) []string {
	if limit <= 0 {
		limit = len(values)
	}
	out := make([]string, 0, min(len(values), limit))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func scanRows[T any](rows *sql.Rows, scan func(rowScanner) (T, error), scanLabel, iterateLabel string) ([]T, error) {
	defer rows.Close()
	items := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, wrapError(err, scanLabel)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, iterateLabel)
	}
	return items, nil
}

func scanStrings(rows *sql.Rows, scanLabel, iterateLabel string) ([]string, error) {
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, wrapError(err, scanLabel)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapError(err, iterateLabel)
	}
	return items, nil
}
