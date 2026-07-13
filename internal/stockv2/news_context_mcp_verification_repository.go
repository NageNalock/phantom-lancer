package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	NewsContextMCPVerificationReady  = "ready"
	NewsContextMCPVerificationFailed = "failed"
)

type NewsContextMCPVerification struct {
	ThreadID     string    `json:"threadId"`
	VersionID    string    `json:"versionId"`
	Status       string    `json:"status"`
	CheckedAt    time.Time `json:"checkedAt"`
	VerifiedAt   time.Time `json:"verifiedAt,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
}

func (s *Store) GetNewsContextMCPVerification(ctx context.Context, threadID string) (NewsContextMCPVerification, bool, error) {
	var item NewsContextMCPVerification
	var verifiedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT thread_id, version_id, status, checked_at, verified_at,
		       COALESCE(error_message, '')
		FROM stockv2_news_context_mcp_verifications
		WHERE thread_id = ?
	`, strings.TrimSpace(threadID)).Scan(
		&item.ThreadID, &item.VersionID, &item.Status, &item.CheckedAt,
		&verifiedAt, &item.ErrorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, wrapError(err, "get news context MCP verification")
	}
	assignNullTime(&item.VerifiedAt, verifiedAt)
	return item, true, nil
}

func (s *Store) GetLatestNewsContextMCPVerification(ctx context.Context) (NewsContextMCPVerification, bool, error) {
	var item NewsContextMCPVerification
	var verifiedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT thread_id, version_id, status, checked_at, verified_at,
		       COALESCE(error_message, '')
		FROM stockv2_news_context_mcp_verifications
		ORDER BY checked_at DESC, thread_id ASC
		LIMIT 1
	`).Scan(
		&item.ThreadID, &item.VersionID, &item.Status, &item.CheckedAt,
		&verifiedAt, &item.ErrorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	if err != nil {
		return item, false, wrapError(err, "get latest news context MCP verification")
	}
	assignNullTime(&item.VerifiedAt, verifiedAt)
	return item, true, nil
}

func (s *Store) UpsertNewsContextMCPVerification(ctx context.Context, item NewsContextMCPVerification) (NewsContextMCPVerification, error) {
	now := time.Now()
	item.ThreadID = strings.TrimSpace(item.ThreadID)
	item.VersionID = strings.TrimSpace(item.VersionID)
	if item.CheckedAt.IsZero() {
		item.CheckedAt = now
	}
	if item.Status != NewsContextMCPVerificationReady {
		item.VerifiedAt = time.Time{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stockv2_news_context_mcp_verifications
			(thread_id, version_id, status, checked_at, verified_at, error_message, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(thread_id) DO UPDATE SET
			version_id = excluded.version_id,
			status = excluded.status,
			checked_at = excluded.checked_at,
			verified_at = excluded.verified_at,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`, item.ThreadID, item.VersionID, item.Status, item.CheckedAt,
		nullableTime(item.VerifiedAt), nullableString(item.ErrorMessage), now)
	if err != nil {
		return NewsContextMCPVerification{}, wrapError(err, "upsert news context MCP verification")
	}
	stored, _, err := s.GetNewsContextMCPVerification(ctx, item.ThreadID)
	return stored, err
}
