package stockv2

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type blockingBulkMaintenanceTransport struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingBulkMaintenanceTransport() *blockingBulkMaintenanceTransport {
	return &blockingBulkMaintenanceTransport{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (t *blockingBulkMaintenanceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.once.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return nil, errors.New("test data source released")
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func (t *blockingBulkMaintenanceTransport) unblock() {
	select {
	case <-t.release:
	default:
		close(t.release)
	}
}

func newBulkMaintenanceTestService(t *testing.T, transport http.RoundTripper) *Service {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	client := &http.Client{Transport: transport}
	svc := NewService(store, nil, client)
	if err := svc.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize service: %v", err)
	}
	return svc
}

func waitUpdateJobTerminal(t *testing.T, svc *Service, jobID string) StockV2UpdateJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.GetUpdateJob(context.Background(), jobID)
		if err == nil && job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("update job %s did not finish", jobID)
	return StockV2UpdateJob{}
}

func waitDailyBarJobTerminal(t *testing.T, svc *Service, jobID string) StockV2DailyBarJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.GetDailyBarJob(context.Background(), jobID)
		if err == nil && job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daily bar job %s did not finish", jobID)
	return StockV2DailyBarJob{}
}

func waitBulkMaintenanceIdle(t *testing.T, svc *Service) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.bulkMaintenanceMu.Lock()
		running := svc.bulkMaintenanceRun
		svc.bulkMaintenanceMu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bulk maintenance lease was not released")
}

func TestExecuteUniverseUpdateCheckCreateIsAtomicAndBlocksDailyBulk(t *testing.T) {
	transport := newBlockingBulkMaintenanceTransport()
	t.Cleanup(transport.unblock)
	svc := newBulkMaintenanceTestService(t, transport)

	const callers = 12
	type startResult struct {
		job StockV2UpdateJob
		err error
	}
	start := make(chan struct{})
	results := make(chan startResult, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			job, err := svc.ExecuteUniverseUpdate(context.Background(), UniverseUpdateRequest{
				Symbols: []string{"600000"},
			})
			results <- startResult{job: job, err: err}
		}()
	}
	close(start)

	var accepted StockV2UpdateJob
	for i := 0; i < callers; i++ {
		result := <-results
		if result.err == nil {
			if accepted.ID != "" {
				t.Fatalf("multiple universe jobs accepted: %s and %s", accepted.ID, result.job.ID)
			}
			accepted = result.job
			continue
		}
		if !errors.Is(result.err, ErrUpdateJobAlreadyRunning) {
			t.Fatalf("concurrent start error = %v, want ErrUpdateJobAlreadyRunning", result.err)
		}
	}
	if accepted.ID == "" {
		t.Fatal("no universe job was accepted")
	}

	select {
	case <-transport.started:
	case <-time.After(5 * time.Second):
		t.Fatal("universe job did not reach the blocked data source")
	}
	if _, err := svc.RunDailyBarsJob(context.Background(), DailyBarsJobRequest{Mode: DailyBarJobModeHot}); !errors.Is(err, ErrDailyBarJobAlreadyRunning) {
		t.Fatalf("daily bulk start during universe job error = %v, want ErrDailyBarJobAlreadyRunning", err)
	}

	transport.unblock()
	_ = waitUpdateJobTerminal(t, svc, accepted.ID)
	waitBulkMaintenanceIdle(t, svc)

	// A terminal universe run must release the shared lease for the other entry point.
	dailyJob, err := svc.RunDailyBarsJob(context.Background(), DailyBarsJobRequest{Mode: DailyBarJobModeHot})
	if err != nil {
		t.Fatalf("start daily bulk after universe termination: %v", err)
	}
	if got := waitDailyBarJobTerminal(t, svc, dailyJob.ID); got.Status != "completed" {
		t.Fatalf("daily job after universe termination status = %q, want completed", got.Status)
	}
	waitBulkMaintenanceIdle(t, svc)
}

func TestRunDailyBarsJobCheckCreateIsAtomicAndFailureReleasesBulkLease(t *testing.T) {
	transport := newBlockingBulkMaintenanceTransport()
	t.Cleanup(transport.unblock)
	svc := newBulkMaintenanceTestService(t, transport)
	if err := svc.store.UpsertInstrument(context.Background(), StockV2Instrument{
		ID:             "instrument-600000",
		Symbol:         "600000",
		Market:         "SH",
		InstrumentType: InstrumentTypeStock,
		Name:           "test stock",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	const callers = 12
	type startResult struct {
		job StockV2DailyBarJob
		err error
	}
	start := make(chan struct{})
	results := make(chan startResult, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			job, err := svc.RunDailyBarsJob(context.Background(), DailyBarsJobRequest{
				Mode:     DailyBarJobModeUniverseIncremental,
				Adjusted: DailyBarAdjustedNone,
			})
			results <- startResult{job: job, err: err}
		}()
	}
	close(start)

	var accepted StockV2DailyBarJob
	for i := 0; i < callers; i++ {
		result := <-results
		if result.err == nil {
			if accepted.ID != "" {
				t.Fatalf("multiple daily bulk jobs accepted: %s and %s", accepted.ID, result.job.ID)
			}
			accepted = result.job
			continue
		}
		if !errors.Is(result.err, ErrDailyBarJobAlreadyRunning) {
			t.Fatalf("concurrent start error = %v, want ErrDailyBarJobAlreadyRunning", result.err)
		}
	}
	if accepted.ID == "" {
		t.Fatal("no daily bulk job was accepted")
	}

	select {
	case <-transport.started:
	case <-time.After(5 * time.Second):
		t.Fatal("daily bulk job did not reach the blocked data source")
	}
	if _, err := svc.ExecuteUniverseUpdate(context.Background(), UniverseUpdateRequest{Symbols: []string{"600000"}}); !errors.Is(err, ErrUpdateJobAlreadyRunning) {
		t.Fatalf("universe start during daily bulk job error = %v, want ErrUpdateJobAlreadyRunning", err)
	}

	transport.unblock()
	job := waitDailyBarJobTerminal(t, svc, accepted.ID)
	if job.Status != "failed" {
		t.Fatalf("daily bulk job status = %q, want failed", job.Status)
	}
	waitBulkMaintenanceIdle(t, svc)

	// Failure must release the lease too; this empty hot run completes locally.
	retry, err := svc.RunDailyBarsJob(context.Background(), DailyBarsJobRequest{Mode: DailyBarJobModeHot})
	if err != nil {
		t.Fatalf("start daily bulk after failure: %v", err)
	}
	if got := waitDailyBarJobTerminal(t, svc, retry.ID); got.Status != "completed" {
		t.Fatalf("retry daily job status = %q, want completed", got.Status)
	}
}

func TestDailyBarBatchInstrumentPreservesPersistedTypeAndInfersMissing(t *testing.T) {
	persisted := StockV2Instrument{
		Symbol:         "510300",
		Market:         "SH",
		InstrumentType: InstrumentTypeExchangeFund,
		ListDate:       "2012-05-28",
	}
	got := dailyBarBatchInstrument("510300", map[string]StockV2Instrument{"510300": persisted})
	if got.InstrumentType != InstrumentTypeExchangeFund || got.ListDate != persisted.ListDate {
		t.Fatalf("persisted instrument = %+v, want fund type and listing date preserved", got)
	}

	fallback := dailyBarBatchInstrument("159915", nil)
	if fallback.Market != "SZ" || fallback.InstrumentType != InstrumentTypeExchangeFund {
		t.Fatalf("fallback instrument = %+v, want inferred SZ exchange fund", fallback)
	}
}
