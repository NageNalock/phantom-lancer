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
