package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/storage"
)

func newStockV2HTTPTest(t *testing.T) (*Server, *storage.Store, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.CreateOwner(ctx, "owner", "hash-not-used")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sessionRaw, sessionHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("session token: %v", err)
	}
	csrfRaw, csrfHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("csrf token: %v", err)
	}
	if _, err := store.CreateSession(ctx, owner.ID, sessionHash, csrfHash, false, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &Server{store: store}, store, sessionRaw, csrfRaw
}
