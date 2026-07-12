package stockv2

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEvaluateResourceGate(t *testing.T) {
	tests := []struct {
		name    string
		metrics ResourceGateMetrics
		state   ResourceGateState
		reasons []string
	}{
		{name: "unknown fails open", state: ResourceGateNormal},
		{
			name: "healthy", state: ResourceGateNormal,
			metrics: ResourceGateMetrics{
				MemoryKnown: true, MemAvailableBytes: 2 << 30,
				LoadKnown: true, Load1: 0.5,
				DiskKnown: true, DiskAvailableBytes: 20 << 30,
			},
		},
		{
			name: "memory throttled", state: ResourceGateThrottled,
			metrics: ResourceGateMetrics{MemoryKnown: true, MemAvailableBytes: 900 << 20},
			reasons: []string{"memory_throttled"},
		},
		{
			name: "load throttled", state: ResourceGateThrottled,
			metrics: ResourceGateMetrics{LoadKnown: true, Load1: resourceGateMaxLoad1},
			reasons: []string{"load_high"},
		},
		{
			name: "memory paused", state: ResourceGatePaused,
			metrics: ResourceGateMetrics{MemoryKnown: true, MemAvailableBytes: 700 << 20},
			reasons: []string{"memory_low"},
		},
		{
			name: "memory critical", state: ResourceGatePaused,
			metrics: ResourceGateMetrics{MemoryKnown: true, MemAvailableBytes: 500 << 20},
			reasons: []string{"memory_critical"},
		},
		{
			name: "disk paused", state: ResourceGatePaused,
			metrics: ResourceGateMetrics{DiskKnown: true, DiskAvailableBytes: 9 << 30},
			reasons: []string{"disk_low"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateResourceGate(tt.metrics)
			if got.State != tt.state {
				t.Fatalf("state = %q, want %q", got.State, tt.state)
			}
			if !reflect.DeepEqual(got.Reasons, tt.reasons) {
				t.Fatalf("reasons = %#v, want %#v", got.Reasons, tt.reasons)
			}
		})
	}
}

func TestMaintenanceConcurrencyForResourceGate(t *testing.T) {
	for state, want := range map[ResourceGateState]int{
		ResourceGateNormal:    resourceGateNormalMaintenanceConcurrency,
		ResourceGateThrottled: resourceGateThrottledMaintenanceConcurrency,
		ResourceGatePaused:    0,
	} {
		if got := maintenanceConcurrencyForResourceGate(ResourceGateStatus{State: state}); got != want {
			t.Fatalf("state %q concurrency = %d, want %d", state, got, want)
		}
	}
}

func TestExecuteUniverseUpdatePersistsPausedResourceGateWithoutStartingWorkers(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewService(store, nil, nil)
	svc.resourceGateReader = func() ResourceGateStatus {
		return ResourceGateStatus{State: ResourceGatePaused, Reasons: []string{"memory_low"}}
	}

	job, err := svc.ExecuteUniverseUpdate(ctx, UniverseUpdateRequest{
		TriggerType: "manual", TriggerSource: "test", Symbols: []string{"000001"},
	})
	if err != nil {
		t.Fatalf("execute paused update: %v", err)
	}
	if job.Status != "paused" || job.FreshnessStatus != AssetMaintenanceFreshnessRetrying ||
		job.CoverageStatus != AssetMaintenanceCoverageIncomplete || !strings.Contains(job.ErrorMessage, "memory_low") {
		t.Fatalf("paused job = %+v", job)
	}
	stored, err := store.GetUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "paused" || stored.FreshnessStatus != AssetMaintenanceFreshnessRetrying {
		t.Fatalf("stored paused job = %+v", stored)
	}
	items, err := store.CountAssetMaintenanceItems(ctx, AssetMaintenanceItemListFilter{JobID: job.ID})
	if err != nil || items != 0 {
		t.Fatalf("paused job item count = %d, err=%v", items, err)
	}
	svc.bulkMaintenanceMu.Lock()
	running := svc.bulkMaintenanceRun
	svc.bulkMaintenanceMu.Unlock()
	if running {
		t.Fatal("paused resource gate started maintenance workers")
	}
}

func TestScheduledUniverseUpdateKeepsSinglePausedResourceRecord(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewService(store, nil, nil)
	svc.resourceGateReader = func() ResourceGateStatus {
		return ResourceGateStatus{State: ResourceGatePaused, Reasons: []string{"disk_low"}}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 12, 23, 5, 0, 0, loc)
	svc.checkAndExecuteScheduledUpdateAt(ctx, now)
	svc.checkAndExecuteScheduledUpdateAt(ctx, now.Add(time.Minute))
	jobs, err := store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != "paused" || !jobs[0].SlotStart.Equal(now.Truncate(time.Hour)) {
		t.Fatalf("scheduled paused jobs = %+v", jobs)
	}
}

func TestScheduledUniverseUpdateWaitsForPausedFrozenTailAfterResourcesRecover(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	slotStart := time.Date(2026, 7, 12, 23, 0, 0, 0, loc)
	snapshot, err := store.EnsureAssetUniverseSnapshot(ctx, []string{"000001"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	job := testAssetMaintenanceJob("job-paused-frozen-slot", AssetMaintenanceScopeFullUniverse, slotStart)
	if err := store.CreateUpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareAssetMaintenanceJob(ctx, job, snapshot, []string{"000001"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PauseAssetMaintenanceJob(ctx, job.ID, "resource gate paused", slotStart.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	svc := NewService(store, nil, nil)
	svc.resourceGateReader = func() ResourceGateStatus {
		return ResourceGateStatus{State: ResourceGateNormal}
	}
	svc.checkAndExecuteScheduledUpdateAt(ctx, slotStart.Add(5*time.Minute))
	jobs, err := store.ListUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != "paused" {
		t.Fatalf("scheduled recovery duplicated paused frozen job: %+v", jobs)
	}
}

func TestResourceGateParsers(t *testing.T) {
	available, err := parseMemAvailable(strings.NewReader("MemTotal: 4096 kB\nMemAvailable: 1024 kB\n"))
	if err != nil || available != 1024*1024 {
		t.Fatalf("MemAvailable = %d, err = %v", available, err)
	}
	load1, err := parseLoad1(strings.NewReader("1.25 0.80 0.30 1/100 42\n"))
	if err != nil || load1 != 1.25 {
		t.Fatalf("load1 = %v, err = %v", load1, err)
	}
}

func TestReadResourceGateReturnsMetrics(t *testing.T) {
	dir := t.TempDir()
	meminfo := filepath.Join(dir, "meminfo")
	loadavg := filepath.Join(dir, "loadavg")
	if err := os.WriteFile(meminfo, []byte("MemAvailable: 2097152 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loadavg, []byte("0.25 0.10 0.05 1/10 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := readResourceGate(meminfo, loadavg, dir)
	if !status.Metrics.MemoryKnown || !status.Metrics.LoadKnown || !status.Metrics.DiskKnown {
		t.Fatalf("metrics = %#v", status.Metrics)
	}
	if len(status.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", status.Diagnostics)
	}
}

func TestReadResourceGateFailsOpenWithDiagnostics(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	status := readResourceGate(missing, missing, missing)
	if status.State != ResourceGateNormal {
		t.Fatalf("state = %q, want normal", status.State)
	}
	if len(status.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %#v, want three read failures", status.Diagnostics)
	}
}
