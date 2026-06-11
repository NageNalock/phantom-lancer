package selfupdate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/storage"
)

func TestConfirmBootMarksRestartVersionMismatchFailed(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateSystemUpdateJob(ctx, storage.SystemUpdateJob{
		CurrentVersion: "v0.1.0",
		TargetVersion:  "v0.2.0",
		Status:         jobStatusRestarting,
		Phase:          phaseRestarting,
	})
	if err != nil {
		t.Fatalf("create update job: %v", err)
	}

	service := NewService(store, nil, nil, Config{
		Build:           buildinfo.Info{Version: "v0.1.0"},
		DownloadTimeout: time.Second,
	})
	service.ConfirmBoot(ctx)

	got, err := store.GetSystemUpdateJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get update job: %v", err)
	}
	if got.Status != jobStatusFailed {
		t.Fatalf("status = %q, want %q", got.Status, jobStatusFailed)
	}
	if got.ErrorMessage == "" {
		t.Fatal("expected version mismatch error message")
	}
	if _, err := store.ActiveSystemUpdateJob(ctx); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("active job err = %v, want ErrNotFound", err)
	}
}
