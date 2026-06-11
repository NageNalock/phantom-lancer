package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestLegacyCodexTablesAreDetectedButNotPurged(t *testing.T) {
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
	if err := store.PurgeLegacyCodexData(ctx); err != nil {
		t.Fatalf("legacy purge no-op: %v", err)
	}
	found, err = store.CodexCliLegacyTablesDetected(ctx)
	if err != nil {
		t.Fatalf("detect legacy tables after no-op purge: %v", err)
	}
	if len(found) != 1 || found[0] != "codex_sessions" {
		t.Fatalf("legacy table was removed: %#v", found)
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
