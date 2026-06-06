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
	store, err := Open(ctx, filepath.Join(dir, "phantom-lancer.db"))
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

func TestPruneImageGenerationJobsKeepsLibraryAssets(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"))
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

func TestImageAssetPrivateFiltering(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"))
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
