package stockv2

import (
	"context"
	"strings"
	"time"
)

const stockV2WriteConflictRetries = 3

func retryStockV2TransientWriteConflict(ctx context.Context, exec func() error) error {
	var err error
	for attempt := 0; attempt <= stockV2WriteConflictRetries; attempt++ {
		err = exec()
		if err == nil || !isDuckDBTransientWriteConflict(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 80 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func isDuckDBTransientWriteConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "write-write conflict") ||
		strings.Contains(msg, "conflict on tuple deletion") ||
		strings.Contains(msg, "duplicate key")
}
