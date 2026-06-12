package storage

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	fts5Once   sync.Once
	fts5Cached bool
)

// FTS5Available returns true iff the linked SQLite driver supports the FTS5
// virtual-table module.  For mattn/go-sqlite3 this is true only when the
// binary was built with the `-tags sqlite_fts5` build tag.
//
// Tests and runtime code that depends on FTS5 can call this once up-front
// and either skip (tests) or fall back to LIKE-based search (production).
func FTS5Available() bool {
	fts5Once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Opening a pure :memory: DSN — never touches disk, closes when probe is done.
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			return
		}
		defer db.Close()
		_, err = db.ExecContext(ctx,
			`CREATE VIRTUAL TABLE IF NOT EXISTS __fts5_probe USING fts5(c)`)
		if err == nil {
			fts5Cached = true
			return
		}
		msg := strings.ToLower(err.Error())
		// "no such module: fts5" is the canonical mattn message; also
		// tolerate alternate drivers that phrase it differently.
		if strings.Contains(msg, "fts5") &&
			(strings.Contains(msg, "no such module") ||
				strings.Contains(msg, "unknown") ||
				strings.Contains(msg, "not available") ||
				strings.Contains(msg, "missing")) {
			fts5Cached = false
			return
		}
		// Any other error (e.g., SQLITE_NOMEM) — conservatively report false.
		fts5Cached = false
	})
	return fts5Cached
}
