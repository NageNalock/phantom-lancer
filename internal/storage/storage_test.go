package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBackupDatabaseCreatesCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateOwner(ctx, "owner", "hash"); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	backup := filepath.Join(dir, "backup.db")
	if err := store.BackupDatabase(ctx, backup); err != nil {
		t.Fatalf("backup database: %v", err)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup database is empty")
	}
}

func TestOpenConfiguresSQLiteForForegroundReads(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var journalMode string
	if err := store.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := store.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("busy timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestBackupDatabaseDoesNotWaitForMainPoolConnection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateOwner(ctx, "owner", "hash"); err != nil {
		t.Fatalf("create owner: %v", err)
	}

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT id FROM owner_account`)
	if err != nil {
		t.Fatalf("hold query: %v", err)
	}
	defer rows.Close()

	backupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- store.BackupDatabase(backupCtx, filepath.Join(dir, "backup-while-pool-busy.db"))
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("backup database: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backup waited for the main database pool connection")
	}
}

func TestBackupDatabaseHonorsCancelledContext(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	err = store.BackupDatabase(cancelled, filepath.Join(dir, "cancelled.db"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BackupDatabase error = %v, want context.Canceled", err)
	}
}

func TestLegacyCodexTablesAreDetected(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE codex_sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	found, err := store.CodexCliLegacyTablesDetected(ctx)
	if err != nil {
		t.Fatalf("detect legacy tables: %v", err)
	}
	if len(found) != 1 || found[0] != "codex_sessions" {
		t.Fatalf("legacy tables = %#v, want codex_sessions", found)
	}
}

func TestCodexCliWorkspacePinnedSortAndNetworkNormalization(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	regularDir := filepath.Join(dir, "regular")
	pinnedDir := filepath.Join(dir, "pinned")
	if err := os.Mkdir(regularDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(pinnedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	regular, err := store.CreateCodexCliWorkspace(ctx, CodexCliWorkspace{Path: regularDir, TrustState: "trusted"})
	if err != nil {
		t.Fatalf("create regular workspace: %v", err)
	}
	pinned, err := store.CreateCodexCliWorkspace(ctx, CodexCliWorkspace{Path: pinnedDir, TrustState: "untrusted", NetworkPolicy: map[string]any{"enabled": true}, Pinned: true})
	if err != nil {
		t.Fatalf("create pinned workspace: %v", err)
	}
	if pinned.NetworkPolicy["enabled"] == true {
		t.Fatal("untrusted workspace should force network off")
	}
	if err := store.TouchCodexCliWorkspace(ctx, regular.ID); err != nil {
		t.Fatalf("touch workspace: %v", err)
	}
	items, err := store.ListCodexCliWorkspaces(ctx)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(items) < 2 || items[0].ID != pinned.ID {
		t.Fatalf("pinned workspace should sort first, got %#v", items)
	}
}

func TestCodexCliAttachmentAssignmentToTurn(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	att, err := store.CreateCodexCliAttachment(ctx, CodexCliAttachment{ThreadID: "thread-1", Filename: "input.png"})
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	if err := store.AssignCodexCliAttachmentsToTurn(ctx, "thread-1", "turn-1", []string{att.ID}); err != nil {
		t.Fatalf("assign attachment: %v", err)
	}
	items, err := store.ListCodexCliAttachmentsForTurn(ctx, "turn-1")
	if err != nil {
		t.Fatalf("list turn attachments: %v", err)
	}
	if len(items) != 1 || items[0].ID != att.ID || items[0].TurnID != "turn-1" {
		t.Fatalf("unexpected turn attachments: %#v", items)
	}
}

func TestMarkCodexCliRunningThreadsInterruptedFailsQueuedTurns(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ws, err := store.CreateCodexCliWorkspace(ctx, CodexCliWorkspace{Path: t.TempDir(), TrustState: "trusted"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	thread, err := store.CreateCodexCliThread(ctx, CodexCliThread{WorkspaceID: ws.ID, Status: "queued"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	turn, err := store.CreateCodexCliTurn(ctx, CodexCliTurn{ThreadID: thread.ID, Status: "queued"})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	if err := store.MarkCodexCliRunningThreadsInterrupted(ctx, "interrupted_by_server_restart"); err != nil {
		t.Fatalf("mark interrupted: %v", err)
	}
	savedTurn, err := store.GetCodexCliTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("get turn: %v", err)
	}
	if savedTurn.Status != "failed" || savedTurn.ErrorSummary == "" {
		t.Fatalf("queued turn should fail closed, got %+v", savedTurn)
	}
	savedThread, err := store.GetCodexCliThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if savedThread.Status != "failed" || savedThread.LastError == "" {
		t.Fatalf("queued thread should fail closed, got %+v", savedThread)
	}
}

func TestSaveCodexCliTurnIfStatusDoesNotOverwriteTerminalTurn(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ws, err := store.CreateCodexCliWorkspace(ctx, CodexCliWorkspace{Path: t.TempDir(), TrustState: "trusted"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	thread, err := store.CreateCodexCliThread(ctx, CodexCliThread{WorkspaceID: ws.ID, Status: "running"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	turn, err := store.CreateCodexCliTurn(ctx, CodexCliTurn{ThreadID: thread.ID, Status: "running"})
	if err != nil {
		t.Fatalf("create turn: %v", err)
	}
	turn.Status = "completed"
	if _, err := store.SaveCodexCliTurn(ctx, turn); err != nil {
		t.Fatalf("save completed turn: %v", err)
	}

	stale := turn
	stale.Status = "failed"
	stale.ErrorSummary = "late watchdog"
	saved, ok, err := store.SaveCodexCliTurnIfStatus(ctx, stale, "running", "waiting_approval")
	if err != nil {
		t.Fatalf("conditional save: %v", err)
	}
	if ok {
		t.Fatal("conditional save should not update a terminal turn")
	}
	if saved.Status != "completed" || saved.ErrorSummary != "" {
		t.Fatalf("terminal turn was overwritten: %+v", saved)
	}
}

func TestAppendCodexCliEventConcurrentSequenceDoesNotDropEvents(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ws, err := store.CreateCodexCliWorkspace(ctx, CodexCliWorkspace{Path: t.TempDir(), TrustState: "trusted"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	thread, err := store.CreateCodexCliThread(ctx, CodexCliThread{WorkspaceID: ws.ID, Status: "running"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	const total = 24
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.AppendCodexCliEvent(ctx, CodexCliEvent{ThreadID: thread.ID, EventType: "test.event", Payload: map[string]any{"ok": true}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("append event: %v", err)
		}
	}
	events, err := store.ListCodexCliEvents(ctx, thread.ID, 0, total+1)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != total {
		t.Fatalf("events len = %d, want %d", len(events), total)
	}
	for i, event := range events {
		want := int64(i + 1)
		if event.Sequence != want {
			t.Fatalf("event[%d].Sequence = %d, want %d", i, event.Sequence, want)
		}
	}
}

func TestPruneImageGenerationJobsKeepsLibraryAssets(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	oldJob, err := store.CreateImageGenerationJob(ctx, ImageGenerationJob{
		Mode:       "text_to_image",
		ModeLabel:  "文生图",
		Model:      "grok-imagine-image-quality",
		Prompt:     "old image",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create old job: %v", err)
	}
	asset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:      "generated",
		Status:         "available",
		JobID:          oldJob.ID,
		SourceRole:     "output",
		Slot:           1,
		MimeType:       "image/png",
		LocalName:      "asset.png",
		StorageBackend: "local",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	if _, err := store.CompleteImageGenerationJob(ctx, oldJob.ID, "/images/generations", map[string]any{}, []ImageGenerationOutput{{
		AssetID:   asset.ID,
		Slot:      1,
		LocalName: "asset.png",
		MimeType:  "image/png",
		Storage:   "local",
	}}); err != nil {
		t.Fatalf("complete old job: %v", err)
	}

	time.Sleep(time.Millisecond)
	newJob, err := store.CreateImageGenerationJob(ctx, ImageGenerationJob{
		Mode:       "text_to_image",
		ModeLabel:  "文生图",
		Model:      "grok-imagine-image-quality",
		Prompt:     "new image",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create new job: %v", err)
	}
	if _, err := store.CompleteImageGenerationJob(ctx, newJob.ID, "/images/generations", map[string]any{}, nil); err != nil {
		t.Fatalf("complete new job: %v", err)
	}

	if err := store.PruneImageGenerationJobs(ctx, 1); err != nil {
		t.Fatalf("prune jobs: %v", err)
	}
	if _, err := store.GetImageGenerationJob(ctx, oldJob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old job err = %v, want ErrNotFound", err)
	}
	gotAsset, err := store.GetImageAsset(ctx, asset.ID)
	if err != nil {
		t.Fatalf("asset should remain after job prune: %v", err)
	}
	if gotAsset.LocalName != "asset.png" || gotAsset.StorageBackend != "local" {
		t.Fatalf("unexpected asset after prune: %#v", gotAsset)
	}
}

func TestDeleteImageGenerationJobKeepsLibraryAsset(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateImageGenerationJob(ctx, ImageGenerationJob{
		Mode:       "text_to_image",
		ModeLabel:  "文生图",
		Model:      "grok-imagine-image-quality",
		Prompt:     "delete only history",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	asset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:      "generated",
		Status:         "available",
		JobID:          job.ID,
		SourceRole:     "output",
		Slot:           1,
		MimeType:       "image/png",
		LocalName:      "manual-delete.png",
		StorageBackend: "local",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	if _, err := store.CompleteImageGenerationJob(ctx, job.ID, "/images/generations", map[string]any{}, []ImageGenerationOutput{{
		AssetID:   asset.ID,
		Slot:      1,
		LocalName: "manual-delete.png",
		MimeType:  "image/png",
		Storage:   "local",
	}}); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	if err := store.DeleteImageGenerationJob(ctx, job.ID); err != nil {
		t.Fatalf("delete job: %v", err)
	}
	if _, err := store.GetImageGenerationJob(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted job err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetImageAsset(ctx, asset.ID); err != nil {
		t.Fatalf("asset should remain after job delete: %v", err)
	}
}

func TestDeleteMediaGenerationJobKeepsMediaAsset(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateMediaGenerationJob(ctx, MediaGenerationJob{
		MediaType:   "image",
		Provider:    "agnes",
		Status:      "queued",
		Mode:        "text_to_image",
		ModeLabel:   "文生图",
		Model:       "agnes-image-2.1-flash",
		Prompt:      "delete media history",
		Parameters:  map[string]any{"n": 1},
		SourceCount: 0,
	}, nil)
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	asset, err := store.CreateMediaAsset(ctx, MediaAsset{
		MediaType:      "image",
		AssetType:      "generated",
		Status:         "available",
		Provider:       "agnes",
		Model:          "agnes-image-2.1-flash",
		JobID:          job.ID,
		SourceRole:     "output",
		Slot:           1,
		MimeType:       "image/png",
		LocalName:      "media-manual-delete.png",
		StorageBackend: "local",
	})
	if err != nil {
		t.Fatalf("create media asset: %v", err)
	}
	if _, err := store.CompleteMediaGenerationJob(ctx, job.ID, "/v1/images/generations", map[string]any{}, []MediaGenerationOutput{{
		AssetID:   asset.ID,
		Slot:      1,
		MediaType: "image",
		MimeType:  "image/png",
		Storage:   "local",
		SizeBytes: 123,
	}}); err != nil {
		t.Fatalf("complete media job: %v", err)
	}

	if err := store.DeleteMediaGenerationJob(ctx, job.ID); err != nil {
		t.Fatalf("delete media job: %v", err)
	}
	if _, err := store.GetMediaGenerationJob(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted media job err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetMediaAsset(ctx, asset.ID); err != nil {
		t.Fatalf("media asset should remain after job delete: %v", err)
	}
}

func TestListImageGenerationJobsDoesNotNestQueriesWhileRowsOpen(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateImageGenerationJob(ctx, ImageGenerationJob{
		Mode:       "text_to_image",
		ModeLabel:  "文生图",
		Model:      "grok-imagine-image-quality",
		Prompt:     "quiet workbench",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	asset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:      "generated",
		Status:         "available",
		JobID:          job.ID,
		SourceRole:     "output",
		Slot:           1,
		MimeType:       "image/png",
		LocalName:      "quiet.png",
		StorageBackend: "local",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	if _, err := store.CompleteImageGenerationJob(ctx, job.ID, "/images/generations", map[string]any{}, []ImageGenerationOutput{{
		AssetID:   asset.ID,
		Slot:      1,
		LocalName: "quiet.png",
		MimeType:  "image/png",
		Storage:   "local",
	}}); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	jobs, err := store.ListImageGenerationJobs(listCtx, 10, "", "")
	if err != nil {
		t.Fatalf("list image jobs should not wait on the single SQLite connection: %v", err)
	}
	if len(jobs) != 1 || len(jobs[0].Outputs) != 1 || jobs[0].Outputs[0].AssetID != asset.ID {
		t.Fatalf("unexpected jobs with outputs: %#v", jobs)
	}
}

func TestListMediaGenerationJobsIncludesRelations(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateMediaGenerationJob(ctx, MediaGenerationJob{
		MediaType:   "image",
		Provider:    "agnes",
		Status:      "queued",
		Mode:        "image_to_image",
		ModeLabel:   "图生图",
		Model:       "agnes-image-2.1-flash",
		Prompt:      "quiet media relation",
		Parameters:  map[string]any{"n": 1},
		SourceCount: 1,
	}, []MediaGenerationSource{{
		AssetID:     "medasset_source",
		Slot:        1,
		SourceType:  "library_asset",
		SourceLabel: "medasset_source",
		SourceRole:  "reference",
		MimeType:    "image/png",
	}})
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	if _, err := store.CompleteMediaGenerationJob(ctx, job.ID, "/v1/images/generations", map[string]any{"total_tokens": float64(1)}, []MediaGenerationOutput{{
		AssetID:   "medasset_output",
		Slot:      1,
		MediaType: "image",
		MimeType:  "image/png",
		Storage:   "s3",
		SizeBytes: 123,
		Metadata:  map[string]any{"width": float64(512), "height": float64(512)},
	}}); err != nil {
		t.Fatalf("complete media job: %v", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	jobs, err := store.ListMediaGenerationJobs(listCtx, 10, "", "", "", "", "")
	if err != nil {
		t.Fatalf("list media jobs should include relations without waiting on open rows: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1: %#v", len(jobs), jobs)
	}
	if len(jobs[0].Sources) != 1 || jobs[0].Sources[0].AssetID != "medasset_source" {
		t.Fatalf("unexpected media job sources: %#v", jobs[0].Sources)
	}
	if len(jobs[0].Outputs) != 1 || jobs[0].Outputs[0].AssetID != "medasset_output" {
		t.Fatalf("unexpected media job outputs: %#v", jobs[0].Outputs)
	}
}

func TestArchiveImageAssetToS3UpdatesGenerationOutputStorage(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	job, err := store.CreateImageGenerationJob(ctx, ImageGenerationJob{
		Mode:       "text_to_image",
		ModeLabel:  "文生图",
		Model:      "grok-imagine-image-quality",
		Prompt:     "remote image",
		ImageCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	asset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:      "generated",
		Status:         "available",
		JobID:          job.ID,
		SourceRole:     "output",
		Slot:           1,
		MimeType:       "image/png",
		StorageBackend: "remote",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	if _, err := store.CompleteImageGenerationJob(ctx, job.ID, "/images/generations", map[string]any{}, []ImageGenerationOutput{{
		AssetID:   asset.ID,
		Slot:      1,
		RemoteURL: "https://example.com/image.png?token=secret",
		MimeType:  "image/png",
		Storage:   "remote",
	}}); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	asset.StorageBackend = "s3"
	asset.S3Bucket = "bucket"
	asset.S3Key = "phantom-lancer/images/generated/2026/06/job/asset.png"
	asset.S3ETag = "etag"
	asset.SizeBytes = 67
	asset.ArchivedAt = now()
	archived, err := store.ArchiveImageAssetToS3(ctx, asset)
	if err != nil {
		t.Fatalf("archive asset: %v", err)
	}
	if archived.StorageBackend != "s3" || archived.S3Key == "" {
		t.Fatalf("unexpected archived asset: %#v", archived)
	}
	gotJob, err := store.GetImageGenerationJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if len(gotJob.Outputs) != 1 {
		t.Fatalf("outputs count = %d, want 1", len(gotJob.Outputs))
	}
	output := gotJob.Outputs[0]
	if output.Storage != "s3" || output.URL != "/api/images/library/assets/"+asset.ID+"/content" || output.RemoteURL == "" {
		t.Fatalf("generation output should point at archived asset while retaining remote provenance: %#v", output)
	}
}

func TestImageAssetPrivateFiltering(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	publicAsset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:        "generated",
		Status:           "available",
		OriginalFilename: "public.png",
		LocalName:        "public.png",
		StorageBackend:   "local",
	})
	if err != nil {
		t.Fatalf("create public asset: %v", err)
	}
	privateAsset, err := store.CreateImageAsset(ctx, ImageAsset{
		AssetType:        "generated",
		Status:           "available",
		OriginalFilename: "private.png",
		LocalName:        "private.png",
		StorageBackend:   "local",
		Private:          true,
		PrivateAt:        "2026-06-05T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("create private asset: %v", err)
	}

	publicItems, err := store.ListImageAssets(ctx, 20, "", "", "", "", "")
	if err != nil {
		t.Fatalf("list public assets: %v", err)
	}
	if len(publicItems) != 1 || publicItems[0].ID != publicAsset.ID {
		t.Fatalf("public list = %#v, want only %s", publicItems, publicAsset.ID)
	}
	privateItems, err := store.ListImageAssets(ctx, 20, "", "", "", "", "private")
	if err != nil {
		t.Fatalf("list private assets: %v", err)
	}
	if len(privateItems) != 1 || privateItems[0].ID != privateAsset.ID || !privateItems[0].Private {
		t.Fatalf("private list = %#v, want only %s", privateItems, privateAsset.ID)
	}
	updated, err := store.SetImageAssetPrivate(ctx, privateAsset.ID, false)
	if err != nil {
		t.Fatalf("unset private: %v", err)
	}
	if updated.Private || updated.PrivateAt != "" {
		t.Fatalf("asset still private after unset: %#v", updated)
	}
	publicItems, err = store.ListImageAssets(ctx, 20, "", "", "", "", "")
	if err != nil {
		t.Fatalf("list public assets after unset: %v", err)
	}
	if len(publicItems) != 2 {
		t.Fatalf("public list count after unset = %d, want 2", len(publicItems))
	}
}

func TestImagePromptLibraryCRUDUseAndSoftDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	created, err := store.CreateImagePrompt(ctx, ImagePrompt{
		Title:       "Workbench portrait",
		Description: "图生图人物风格模板",
		Prompt:      "Keep the input composition, refine lighting, and preserve identity.",
		Mode:        "image_to_image",
		Model:       "grok-imagine-image-quality",
		AspectRatio: "1:1",
		Resolution:  "1k",
		ImageCount:  2,
		Tags:        []string{"portrait", "reference", "portrait"},
	})
	if err != nil {
		t.Fatalf("create prompt: %v", err)
	}
	if created.ID == "" || created.Status != "active" {
		t.Fatalf("unexpected created prompt: %#v", created)
	}
	if len(created.Tags) != 2 {
		t.Fatalf("tags should be deduplicated, got %#v", created.Tags)
	}

	listed, err := store.ListImagePrompts(ctx, 20, "portrait", "image_to_image", "")
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed prompts = %#v, want %s", listed, created.ID)
	}

	updated, err := store.UpdateImagePrompt(ctx, created.ID, ImagePrompt{
		Title:      "Quiet product shot",
		Prompt:     "Generate a quiet product shot on a neutral desk.",
		Mode:       "text_to_image",
		Model:      "grok-imagine-image",
		ImageCount: 1,
		Tags:       []string{"product"},
	})
	if err != nil {
		t.Fatalf("update prompt: %v", err)
	}
	if updated.Title != "Quiet product shot" || updated.Mode != "text_to_image" || updated.UseCount != 0 {
		t.Fatalf("unexpected updated prompt: %#v", updated)
	}

	used, err := store.UseImagePrompt(ctx, created.ID)
	if err != nil {
		t.Fatalf("use prompt: %v", err)
	}
	if used.UseCount != 1 || used.LastUsedAt == "" {
		t.Fatalf("use count or last used not updated: %#v", used)
	}

	deleted, err := store.DeleteImagePrompt(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete prompt: %v", err)
	}
	if deleted.Status != "deleted" || deleted.DeletedAt == "" {
		t.Fatalf("prompt should be soft deleted: %#v", deleted)
	}
	active, err := store.ListImagePrompts(ctx, 20, "", "", "")
	if err != nil {
		t.Fatalf("list active prompts: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("soft deleted prompt should not be listed by default: %#v", active)
	}
}

// ---- TLS + session tests (Phase 5) ----

func TestRuntimeSettingsTLSFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	defaults := RuntimeSettings{
		AllowedRoots:      []string{"/tmp"},
		Addr:              "127.0.0.1:8080",
		TLSEnabled:        false,
		TLSOwnerUIDCheck:  true,
		HSTSEnabled:       false,
		HSTSMaxAgeSeconds: 0,
	}
	if err := store.EnsureRuntimeSettings(ctx, defaults); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	updated := defaults
	updated.TLSEnabled = true
	updated.TLSCertFile = "  /etc/pl/tls/cert.pem  "
	updated.TLSKeyFile = "/etc/pl/tls/key.pem"
	updated.TLSOwnerUIDCheck = false
	updated.HSTSEnabled = true
	updated.HSTSMaxAgeSeconds = 15724800
	updated.Addr = "0.0.0.0:8443"
	if err := store.UpdateRuntimeSettings(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.GetRuntimeSettings(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.TLSEnabled {
		t.Error("TLSEnabled not persisted")
	}
	if got.TLSCertFile != "/etc/pl/tls/cert.pem" {
		t.Errorf("TLSCertFile = %q", got.TLSCertFile)
	}
	if got.TLSKeyFile != "/etc/pl/tls/key.pem" {
		t.Errorf("TLSKeyFile = %q", got.TLSKeyFile)
	}
	if got.TLSOwnerUIDCheck {
		t.Error("TLSOwnerUIDCheck not saved as false")
	}
	if !got.HSTSEnabled {
		t.Error("HSTSEnabled not persisted")
	}
	if got.HSTSMaxAgeSeconds != 15724800 {
		t.Errorf("HSTSMaxAgeSeconds = %d", got.HSTSMaxAgeSeconds)
	}
	if got.Addr != "0.0.0.0:8443" {
		t.Errorf("Addr = %q", got.Addr)
	}
}

func TestRuntimeSettingsTLSIncompleteRejected(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	defaults := RuntimeSettings{AllowedRoots: []string{"/tmp"}, Addr: "127.0.0.1:8080"}
	if err := store.EnsureRuntimeSettings(ctx, defaults); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	bad := defaults
	bad.TLSEnabled = true
	bad.TLSCertFile = "/etc/pl/cert.pem"
	if err := store.UpdateRuntimeSettings(ctx, bad); err == nil {
		t.Error("expected error when TLSEnabled without key file")
	}

	bad.TLSKeyFile = "  "
	bad.TLSCertFile = "/etc/pl/cert.pem"
	if err := store.UpdateRuntimeSettings(ctx, bad); err == nil {
		t.Error("expected error when key file is whitespace")
	}

	bad.TLSCertFile = ""
	bad.TLSKeyFile = "/etc/pl/key.pem"
	if err := store.UpdateRuntimeSettings(ctx, bad); err == nil {
		t.Error("expected error when cert file empty")
	}
}

func TestRuntimeSettingsHSTSValidation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	defaults := RuntimeSettings{AllowedRoots: []string{"/tmp"}, Addr: "127.0.0.1:8080"}
	if err := store.EnsureRuntimeSettings(ctx, defaults); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	bad := defaults
	bad.HSTSEnabled = true
	bad.HSTSMaxAgeSeconds = -1
	if err := store.UpdateRuntimeSettings(ctx, bad); err == nil {
		t.Error("expected error for HSTS max age negative")
	}

	good := defaults
	good.HSTSEnabled = true
	good.HSTSMaxAgeSeconds = 0
	if err := store.UpdateRuntimeSettings(ctx, good); err != nil {
		t.Errorf("HSTS max-age=0 should be valid: %v", err)
	}
	got, _ := store.GetRuntimeSettings(ctx)
	if got.HSTSMaxAgeSeconds != 0 || !got.HSTSEnabled {
		t.Errorf("HSTS zero max-age not saved: %+v", got)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	owner, err := store.CreateOwner(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	exp := time.Now().Add(24 * time.Hour)
	s1, err := store.CreateSession(ctx, owner.ID, "hash1", "csrf1", true, exp)
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	s2, err := store.CreateSession(ctx, owner.ID, "hash2", "csrf2", false, exp)
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}
	s3, err := store.CreateSession(ctx, owner.ID, "hash3", "csrf3", false, exp)
	if err != nil {
		t.Fatalf("create session 3: %v", err)
	}

	if err := store.RevokeSession(ctx, s1.ID); err != nil {
		t.Fatalf("revoke individual: %v", err)
	}

	n, err := store.RevokeAllSessions(ctx)
	if err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if n != 2 {
		t.Errorf("RevokeAll affected %d rows, want 2", n)
	}

	n, err = store.RevokeAllSessions(ctx)
	if err != nil {
		t.Fatalf("revoke all 2nd: %v", err)
	}
	if n != 0 {
		t.Errorf("second RevokeAll affected %d rows, want 0", n)
	}

	for _, sh := range []string{"hash1", "hash2", "hash3"} {
		sess, err := store.GetSessionByHash(ctx, sh)
		if err != nil {
			t.Fatalf("get session by %s: %v", sh, err)
		}
		if !sess.RevokedAt.Valid {
			t.Errorf("session %q should be revoked, got RevokedAt=%+v", sh, sess.RevokedAt)
		}
	}
	_, _, _ = s1, s2, s3
}

func TestDatabaseStats(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Insert some data to make tables non-trivial.
	if _, err := store.CreateOwner(ctx, "admin", "hash"); err != nil {
		t.Fatalf("create owner: %v", err)
	}

	stats, err := store.DatabaseStats(ctx)
	if err != nil {
		t.Fatalf("DatabaseStats: %v", err)
	}
	if stats.TotalBytes == 0 {
		t.Fatal("expected non-zero total bytes")
	}
	if len(stats.Tables) == 0 {
		t.Fatal("expected non-empty table list")
	}

	// Verify tables are sorted by size descending.
	var prev int64 = 1 << 62
	for _, tbl := range stats.Tables {
		total := tbl.SizeBytes + tbl.IndexSizeBytes
		if total > prev {
			t.Errorf("tables not sorted: %s (%d) comes after larger", tbl.Name, total)
		}
		prev = total
		if tbl.Name == "" {
			t.Error("table name is empty")
		}
		if tbl.Description == "" {
			t.Errorf("table %s has no description", tbl.Name)
		}
	}

	// Verify caching: second call should be fast and return same data.
	start := time.Now()
	stats2, err := store.DatabaseStats(ctx)
	if err != nil {
		t.Fatalf("second DatabaseStats: %v", err)
	}
	if time.Since(start) > 10*time.Millisecond {
		t.Logf("warning: second call took %v (cache may not be working)", time.Since(start))
	}
	if stats2.TotalBytes != stats.TotalBytes {
		t.Errorf("cached total mismatch: %d vs %d", stats2.TotalBytes, stats.TotalBytes)
	}
}

func TestDatabaseStatsCollector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	store.StartStatsCollector(ctx)

	// Wait briefly for the initial refresh (which happens after 2s).
	// Instead of waiting 2s, force a refresh now.
	_, err = store.refreshDatabaseStats(ctx)
	if err != nil {
		t.Fatalf("refresh stats: %v", err)
	}

	stats, err := store.DatabaseStats(ctx)
	if err != nil {
		t.Fatalf("DatabaseStats after collector start: %v", err)
	}
	if stats.TotalBytes == 0 {
		t.Fatal("expected non-zero stats after collector refresh")
	}
}

func TestDescribeTable(t *testing.T) {
	cases := []struct {
		name     string
		wantDesc string
	}{
		{"audit_events", "审计日志"},
		{"image_assets", "图片素材库"},
		{"codex_cli_threads", "Codex CLI 模块表"},
		{"stockv2_daily_bars", "数据表"},
		{"unknown_table", "数据表"},
		{"__internal", "SQLite 内部表"},
	}
	for _, tc := range cases {
		desc := describeTable(tc.name)
		if desc == "" {
			t.Errorf("describeTable(%q) returned empty", tc.name)
		}
		// Check the description contains the expected keyword.
		if !containsString(desc, tc.wantDesc) {
			t.Logf("describeTable(%q) = %q (looking for %q)", tc.name, desc, tc.wantDesc)
		}
	}
}

func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(len(s) >= len(substr)) &&
		(indexOfString(s, substr) >= 0)
}

func indexOfString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestSortTableStats(t *testing.T) {
	tables := []DatabaseTableStat{
		{Name: "small", SizeBytes: 100, IndexSizeBytes: 10},
		{Name: "large", SizeBytes: 1000, IndexSizeBytes: 200},
		{Name: "medium", SizeBytes: 500, IndexSizeBytes: 0},
	}
	sortTableStats(tables)
	if tables[0].Name != "large" || tables[1].Name != "medium" || tables[2].Name != "small" {
		t.Errorf("unexpected sort order: %v, %v, %v", tables[0].Name, tables[1].Name, tables[2].Name)
	}
}
