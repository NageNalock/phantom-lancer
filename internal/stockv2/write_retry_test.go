package stockv2

import (
	"context"
	"errors"
	"testing"
)

func TestRetryStockV2TransientWriteConflictRetriesThenSucceeds(t *testing.T) {
	attempts := 0
	err := retryStockV2TransientWriteConflict(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New(`TransactionContext Error: Failed to commit: write-write conflict on key: "000710"`)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryStockV2TransientWriteConflictDoesNotRetryPermanentError(t *testing.T) {
	attempts := 0
	err := retryStockV2TransientWriteConflict(context.Background(), func() error {
		attempts++
		return errors.New("syntax error")
	})
	if err == nil || err.Error() != "syntax error" {
		t.Fatalf("err = %v, want syntax error", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
