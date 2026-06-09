package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "phantom-lancer.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestObjectStorageProfileCRUDAndSecretHandling(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	created, err := store.CreateObjectStorageProfile(ctx, ObjectStorageProfile{
		Name:            "primary",
		Bucket:          "bucket-a",
		Region:          "auto",
		Endpoint:        "https://s3.example.com",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "secret-value",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated profile id")
	}
	if !created.HasCredentials {
		t.Fatal("expected HasCredentials true when key and secret provided")
	}
	if created.MaskedAccessKeyID == "AKIAEXAMPLE" || created.MaskedAccessKeyID == "" {
		t.Fatalf("expected masked access key, got %q", created.MaskedAccessKeyID)
	}

	// Update without supplying secret must preserve existing credentials.
	updated, err := store.UpdateObjectStorageProfile(ctx, ObjectStorageProfile{ID: created.ID, Name: "renamed", Bucket: "bucket-b", Endpoint: "https://s3.example.com"}, false, false)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.Name != "renamed" || updated.Bucket != "bucket-b" {
		t.Fatalf("unexpected update result: %+v", updated)
	}
	if !updated.HasCredentials {
		t.Fatal("expected credentials preserved when updateSecret=false")
	}
	if updated.SecretAccessKey != "secret-value" {
		t.Fatal("expected secret preserved when updateSecret=false")
	}

	// Clear secret wipes credentials.
	cleared, err := store.UpdateObjectStorageProfile(ctx, ObjectStorageProfile{ID: created.ID, Name: "renamed", Bucket: "bucket-b", Endpoint: "https://s3.example.com"}, false, true)
	if err != nil {
		t.Fatalf("clear secret: %v", err)
	}
	if cleared.HasCredentials || cleared.SecretAccessKey != "" || cleared.AccessKeyID != "" {
		t.Fatalf("expected credentials cleared, got %+v", cleared)
	}

	if err := store.DeleteObjectStorageProfile(ctx, created.ID); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if _, err := store.GetObjectStorageProfile(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestObjectStorageProfileReferencedByImages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	profile, err := store.CreateObjectStorageProfile(ctx, ObjectStorageProfile{Name: "p", Bucket: "b", Endpoint: "https://s3.example.com", AccessKeyID: "AK", SecretAccessKey: "SK"})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// Not referenced yet.
	refs, err := store.ObjectStorageProfileReferencedBy(ctx, profile.ID)
	if err != nil {
		t.Fatalf("refs: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no references, got %v", refs)
	}

	// Point Images storage at the profile.
	if _, err := store.UpdateImageStorageSettings(ctx, ImageStorageSettings{Backend: "object_storage", ObjectStorageProfileID: profile.ID}, false, false); err != nil {
		t.Fatalf("update image storage settings: %v", err)
	}
	refs, err = store.ObjectStorageProfileReferencedBy(ctx, profile.ID)
	if err != nil {
		t.Fatalf("refs after link: %v", err)
	}
	if len(refs) != 1 || refs[0] != "images" {
		t.Fatalf("expected images reference, got %v", refs)
	}
}

func TestObjectStorageProfileReferencedByDockerRegistry(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	profile, err := store.CreateObjectStorageProfile(ctx, ObjectStorageProfile{Name: "docker", Bucket: "b", Endpoint: "https://s3.example.com", AccessKeyID: "AK", SecretAccessKey: "SK"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDockerRegistrySettings(ctx, DockerRegistrySettings{StorageBackend: "object_storage", ObjectStorageProfileID: profile.ID, ObjectPrefix: "phantom-lancer/docker-registry"}); err != nil {
		t.Fatal(err)
	}
	refs, err := store.ObjectStorageProfileReferencedBy(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "docker_registry" {
		t.Fatalf("expected docker_registry reference, got %v", refs)
	}
}

func TestImageStorageSettingsObjectStorageBackendPersists(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	profile, err := store.CreateObjectStorageProfile(ctx, ObjectStorageProfile{Name: "p", Bucket: "b", Endpoint: "https://s3.example.com", AccessKeyID: "AK", SecretAccessKey: "SK"})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	updated, err := store.UpdateImageStorageSettings(ctx, ImageStorageSettings{Backend: "object_storage", ObjectStorageProfileID: profile.ID}, false, false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Backend != "object_storage" || updated.ObjectStorageProfileID != profile.ID {
		t.Fatalf("unexpected settings: %+v", updated)
	}
	reloaded, err := store.GetImageStorageSettings(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Backend != "object_storage" || reloaded.ObjectStorageProfileID != profile.ID {
		t.Fatalf("settings did not persist: %+v", reloaded)
	}
}

func TestMigrateImageStorageToObjectProfileIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	// Simulate legacy inline s3 storage with credentials.
	if _, err := store.UpdateImageStorageSettings(ctx, ImageStorageSettings{
		Backend:           "s3",
		S3Bucket:          "legacy-bucket",
		S3Region:          "auto",
		S3Endpoint:        "https://legacy.example.com",
		S3AccessKeyID:     "AKLEGACY",
		S3SecretAccessKey: "legacy-secret",
	}, true, false); err != nil {
		t.Fatalf("seed legacy settings: %v", err)
	}

	if err := store.migrateImageStorageToObjectProfile(ctx); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	settings, err := store.GetImageStorageSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.Backend != "object_storage" || settings.ObjectStorageProfileID == "" {
		t.Fatalf("expected migration to object_storage, got %+v", settings)
	}
	profiles, err := store.ListObjectStorageProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one profile, got %d", len(profiles))
	}
	if profiles[0].Bucket != "legacy-bucket" || profiles[0].SecretAccessKey != "legacy-secret" {
		t.Fatalf("profile did not carry legacy values: %+v", profiles[0])
	}

	// Running again must not create a duplicate profile.
	if err := store.migrateImageStorageToObjectProfile(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	profiles, err = store.ListObjectStorageProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles after rerun: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("migration not idempotent, got %d profiles", len(profiles))
	}
}
