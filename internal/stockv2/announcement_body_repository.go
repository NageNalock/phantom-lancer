package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	announcementBodyDailyRequestLimit = 20
	announcementBodyDailyByteLimit    = int64(64 << 20)
	announcementBodyMaxPDFBytes       = int64(8 << 20)
	announcementBodyBudgetReservation = announcementBodyMaxPDFBytes + 1
	announcementBodyMaxAttempts       = 5
	announcementBodyLeaseDuration     = 20 * time.Minute
)

var errAnnouncementBodyLeaseStale = errors.New("announcement body lease is stale")

type announcementBodyLease struct {
	Announcement  StockV2Announcement
	BudgetDate    string
	ReservedBytes int64
}

type announcementBodyClaim struct {
	Lease           *announcementBodyLease
	BudgetExhausted bool
}

func (s *Store) claimMajorAnnouncementBody(
	ctx context.Context,
	now time.Time,
) (announcementBodyClaim, error) {
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return announcementBodyClaim{}, wrapError(err, "begin announcement body claim")
	}
	defer tx.Rollback()

	budgetDate := now.In(chinaMarketTZ).Format("2006-01-02")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stockv2_announcement_body_budgets (
			budget_date, request_count, byte_budget_used, created_at, updated_at
		) VALUES (?, 0, 0, ?, ?)
		ON CONFLICT(budget_date) DO NOTHING
	`, budgetDate, now, now); err != nil {
		return announcementBodyClaim{}, wrapError(err, "initialize announcement body budget")
	}
	var requestCount int
	var byteBudgetUsed int64
	if err := tx.QueryRowContext(ctx, `
		SELECT request_count, byte_budget_used
		FROM stockv2_announcement_body_budgets
		WHERE budget_date = ?
	`, budgetDate).Scan(&requestCount, &byteBudgetUsed); err != nil {
		return announcementBodyClaim{}, wrapError(err, "read announcement body budget")
	}
	if requestCount >= announcementBodyDailyRequestLimit ||
		byteBudgetUsed+announcementBodyBudgetReservation > announcementBodyDailyByteLimit {
		return announcementBodyClaim{BudgetExhausted: true}, nil
	}

	staleBefore := now.Add(-announcementBodyLeaseDuration)
	query := announcementSelectSQL() + `
		WHERE major = TRUE
		  AND COALESCE(pdf_url, '') <> ''
		  AND COALESCE(body_attempt_count, 0) < ?
		  AND (
			COALESCE(NULLIF(body_status, ''), 'metadata_only') = 'metadata_only'
			OR (
				COALESCE(NULLIF(body_status, ''), 'metadata_only') IN ('retry_wait', 'failed')
				AND (body_next_attempt_at IS NULL OR body_next_attempt_at <= ?)
			)
			OR (
				body_status = 'processing'
				AND (body_checked_at IS NULL OR body_checked_at <= ?)
			)
		  )
		ORDER BY
		  CASE WHEN COALESCE(NULLIF(body_status, ''), 'metadata_only') = 'metadata_only' THEN 0 ELSE 1 END,
		  COALESCE(published_at, fetched_at, created_at) DESC,
		  created_at DESC
		LIMIT 1`
	item, err := scanAnnouncement(tx.QueryRowContext(
		ctx, query, announcementBodyMaxAttempts, now, staleBefore,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return announcementBodyClaim{}, nil
	}
	if err != nil {
		return announcementBodyClaim{}, wrapError(err, "select announcement body candidate")
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_announcements
		SET body_status = ?,
			body_attempt_count = COALESCE(body_attempt_count, 0) + 1,
			body_checked_at = ?,
			body_next_attempt_at = NULL,
			body_error = NULL,
			updated_at = ?
		WHERE id = ?
		  AND COALESCE(body_attempt_count, 0) = ?
	`, AnnouncementBodyStatusProcessing, now, now, item.ID, item.BodyAttemptCount)
	if err != nil {
		return announcementBodyClaim{}, wrapError(err, "claim announcement body")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return announcementBodyClaim{}, wrapError(err, "read announcement body claim result")
		}
		return announcementBodyClaim{}, errAnnouncementBodyLeaseStale
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stockv2_announcement_body_budgets
		SET request_count = request_count + 1,
			byte_budget_used = byte_budget_used + ?,
			updated_at = ?
		WHERE budget_date = ?
	`, announcementBodyBudgetReservation, now, budgetDate); err != nil {
		return announcementBodyClaim{}, wrapError(err, "reserve announcement body budget")
	}
	if err := tx.Commit(); err != nil {
		return announcementBodyClaim{}, wrapError(err, "commit announcement body claim")
	}
	item.BodyStatus = AnnouncementBodyStatusProcessing
	item.BodyAttemptCount++
	item.BodyCheckedAt = now
	item.BodyNextAttemptAt = time.Time{}
	return announcementBodyClaim{Lease: &announcementBodyLease{
		Announcement: item, BudgetDate: budgetDate, ReservedBytes: announcementBodyBudgetReservation,
	}}, nil
}

func (s *Store) settleAnnouncementBodyBudget(
	ctx context.Context,
	lease announcementBodyLease,
	actualBytes int64,
	now time.Time,
) error {
	if actualBytes < 0 {
		actualBytes = 0
	}
	if actualBytes > lease.ReservedBytes {
		actualBytes = lease.ReservedBytes
	}
	result, err := s.assetDB().ExecContext(ctx, `
		UPDATE stockv2_announcement_body_budgets
		SET byte_budget_used = GREATEST(0, byte_budget_used - ? + ?),
			updated_at = ?
		WHERE budget_date = ?
	`, lease.ReservedBytes, actualBytes, now, lease.BudgetDate)
	if err != nil {
		return wrapError(err, "settle announcement body budget")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return wrapError(err, "read announcement body budget settlement")
		}
		return fmt.Errorf("announcement body budget %s is missing", lease.BudgetDate)
	}
	return nil
}

func (s *Store) completeMajorAnnouncementBody(
	ctx context.Context,
	lease announcementBodyLease,
	excerpt string,
	bodyHash string,
	contentBytes int64,
	now time.Time,
) error {
	excerpt = strings.TrimSpace(excerpt)
	bodyHash = strings.TrimSpace(bodyHash)
	if excerpt == "" || bodyHash == "" {
		return errors.New("announcement body completion requires extracted text")
	}
	tx, err := s.assetDB().BeginTx(ctx, nil)
	if err != nil {
		return wrapError(err, "begin announcement body completion")
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE stockv2_announcements
		SET body_status = ?,
			body_text_excerpt = ?,
			body_hash = ?,
			body_checked_at = ?,
			body_next_attempt_at = NULL,
			body_error = NULL,
			body_content_bytes = ?,
			updated_at = ?
		WHERE id = ? AND body_status = ? AND body_attempt_count = ?
	`, AnnouncementBodyStatusTextReady, excerpt, bodyHash, now, contentBytes, now,
		lease.Announcement.ID, AnnouncementBodyStatusProcessing, lease.Announcement.BodyAttemptCount)
	if err != nil {
		return wrapError(err, "complete announcement body")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return wrapError(err, "read announcement body completion result")
		}
		return errAnnouncementBodyLeaseStale
	}
	revisions, err := bumpStockProfileAIAnnouncementRevisionsWithTx(
		ctx, tx, []string{lease.Announcement.Symbol}, now,
	)
	if err != nil {
		return err
	}
	if revision, ok := revisions[lease.Announcement.Symbol]; ok {
		if _, err := tx.ExecContext(ctx, `
			UPDATE stockv2_announcements SET symbol_revision = ? WHERE id = ?
		`, revision, lease.Announcement.ID); err != nil {
			return wrapError(err, "stamp announcement body revision")
		}
	}
	if err := tx.Commit(); err != nil {
		return wrapError(err, "commit announcement body completion")
	}
	return nil
}

func (s *Store) failMajorAnnouncementBody(
	ctx context.Context,
	lease announcementBodyLease,
	message string,
	nextAttemptAt time.Time,
	terminal bool,
	now time.Time,
) error {
	status := AnnouncementBodyStatusRetryWait
	if terminal {
		status = AnnouncementBodyStatusFailed
		nextAttemptAt = time.Time{}
	}
	result, err := s.assetDB().ExecContext(ctx, `
		UPDATE stockv2_announcements
		SET body_status = ?,
			body_checked_at = ?,
			body_next_attempt_at = ?,
			body_error = ?,
			updated_at = ?
		WHERE id = ? AND body_status = ? AND body_attempt_count = ?
	`, status, now, nullableTime(nextAttemptAt), nullableString(strings.TrimSpace(message)), now,
		lease.Announcement.ID, AnnouncementBodyStatusProcessing, lease.Announcement.BodyAttemptCount)
	if err != nil {
		return wrapError(err, "fail announcement body")
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return wrapError(err, "read announcement body failure result")
		}
		return errAnnouncementBodyLeaseStale
	}
	return nil
}
