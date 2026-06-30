package stockv2

import (
	"context"
	"fmt"
	"testing"
)

func TestSnapshotReturnsInstrumentTotalWithSmallSample(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	for i := 0; i < 25; i++ {
		symbol := fmt.Sprintf("30%04d", i)
		if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
			ID:     "inst-" + symbol,
			Symbol: symbol,
			Market: "SZ",
			Name:   "样本" + symbol,
			Status: "active",
		}); err != nil {
			t.Fatalf("seed instrument %s: %v", symbol, err)
		}
	}

	snapshot, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.InstrumentTotal != 25 {
		t.Fatalf("instrument total = %d, want 25", snapshot.InstrumentTotal)
	}
	if len(snapshot.Instruments) != 20 {
		t.Fatalf("instrument sample length = %d, want 20", len(snapshot.Instruments))
	}
}
