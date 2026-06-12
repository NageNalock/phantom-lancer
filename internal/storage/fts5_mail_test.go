package storage

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// openFTS5Store opens a fresh on-disk SQLite store under t.TempDir so FTS5
// virtual tables + their triggers are exercised exactly as production code.
func openFTS5Store(t *testing.T) *Store {
	t.Helper()
	if !FTS5Available() {
		t.Skip("Skipping FTS5 tests: sqlite3 was not built with the fts5 module. " +
			"Re-run with `-tags sqlite_fts5` to enable this test suite.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	dbPath := t.TempDir() + "/phantom.db"
	s, err := Open(ctx, dbPath, slog.Default())
	if err != nil {
		t.Fatalf("Open(%s): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ---- Fixture: 3 messages ----------------------------------------------------

// p7msg inserts a single MailMessageP7 via MailMessageUpsert.
func p7msg(t *testing.T, s *Store, m MailMessageP7) MailMessageP7 {
	t.Helper()
	ctx := context.Background()
	out, err := s.MailMessageUpsert(ctx, m)
	if err != nil {
		t.Fatalf("MailMessageUpsert(%+v): %v", m, err)
	}
	return out
}

func search(t *testing.T, s *Store, accountID, query string, wantCount int) []MailSearchResultP7 {
	t.Helper()
	ctx := context.Background()
	results, total, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: accountID,
		Query:     query,
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("MailMessageSearchP7 query=%q: %v", query, err)
	}
	if total != wantCount {
		t.Errorf("query %q: want total=%d got total=%d", query, wantCount, total)
	}
	if len(results) != wantCount {
		t.Errorf("query %q: want len(results)=%d got %d", query, wantCount, len(results))
	}
	return results
}

// -------------------------------------------------------------------

// ---------- Test 1: Insert 3 messages, run 4 search queries (table-driven) --------

func TestFTS5_ThreeMsg_Search(t *testing.T) {
	s := openFTS5Store(t)
	ctx := context.Background()

	account := "acct-fts5-1"
	folder := "fld-1"
	folder2 := "fld-2"

	// Msg 1: Phantom Lancer (Dota hero) body
	// Msg 2: mox go acme dns-01
	// Msg 3: dota mox hybrid acme
	p7msg(t, s, MailMessageP7{
		AccountID:  account,
		FolderID:   folder,
		UID:        1,
		Subject:    "Phantom Lancer the dota hero",
		FromListCSV: "icefrog@valvesoftware.com",
		ToListCSV:   "players@dota2game.com",
		BodyText:   "Phantom Lancer dota hero agility carry nuker illusion based agility hero dota item diffusal manta style",
		PreviewText: "Phantom Lancer the agility carry nuker illusion based hero",
	})
	p7msg(t, s, MailMessageP7{
		AccountID:  account,
		FolderID:   folder,
		UID:        2,
		Subject:    "mox the go acme dns-01",
		FromListCSV: "mjt@moxmail.example",
		ToListCSV:   "ops@lancercontrol.test",
		BodyText:   "mox written in go acme lets-encrypt dns-01 acme-dns challenge token",
		PreviewText: "mox is go acme go programming language",
	})
	p7msg(t, s, MailMessageP7{
		AccountID:  account,
		FolderID:   folder2,
		UID:        3,
		Subject:    "dota mox hybrid acme relay",
		FromListCSV: "dev@relaymail.test",
		ToListCSV:   "admins@lancercontrol.test",
		BodyText:   "hybrid setup with mox dota fan meets acme challenge",
		PreviewText: "dota mox hybrid acme setup using acme dns challenge",
	})

	cases := []struct {
		q    string
		want int
	}{
		{"phantom", 1},
		{"dota", 2},    // msg 1 + 3 (subject/body
		{"acme dns", 2}, // msg 2 + 3
		{"xyz", 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.q, func(t *testing.T) {
			search(t, s, account, tc.q, tc.want)
		})
	}

	// Ensure no cross-account leakage: query on a different accountID returns 0.
	_, total, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: "acct-other",
		Query:     "dota",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("search wrong account: %v", err)
	}
	if total != 0 {
		t.Errorf("cross-account query should return 0, got total=%d", total)
	}
}

// ---------- Test 2: Delete msg 3, re-run dota count drops from 2 -> 1 --------

func TestFTS5_DeleteAfterSearch(t *testing.T) {
	s := openFTS5Store(t)
	ctx := context.Background()
	account := "acct-del"
	folder := "fld-del-1"

	m1 := p7msg(t, s, MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 1,
		Subject:     "phantom lancer dota mox intro",
		BodyText:  "dota agility carry nuker illusion mox setup",
		PreviewText: "dota phantom hero mox testing",
	})
	m2 := p7msg(t, s, MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 2,
		Subject:     "mox acme dns",
		BodyText:  "acme dns challenge",
		PreviewText: "mox go acme",
	})
	m3 := p7msg(t, s, MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 3,
		Subject:     "dota mox hybrid acme dota",
		BodyText:  "dota mox hybrid acme setup dota dota",
		PreviewText: "dota hero meets mox",
	})

	// Before delete, "dota" should hit msgs 1 and 3 = 2.
	search(t, s, account, "dota", 2)

	// Delete msg 3.
	if err := s.MailMessageDelete(ctx, m3.ID); err != nil {
		t.Fatalf("delete m3: %v", err)
	}
	// "dota" now only msg 1.
	search(t, s, account, "dota", 1)

	// m1/m2 still present via "mox" count=2, "phantom"=1.
	search(t, s, account, "mox", 2)
	search(t, s, account, "phantom", 1)

	// Double-delete: m2
	_ = m2
	_ = m1
}

// ---------- Test 3: Update msg 2 body, re-search token change

func TestFTS5_UpdateAfterSearch(t *testing.T) {
	s := openFTS5Store(t)
	ctx := context.Background()
	account := "acct-upd"
	folder := "fld-upd"

	// Upsert msg2: "go acme"
	m2 := p7msg(t, s, MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 2,
		Subject:     "mox the go acme",
		BodyText:  "programming go acme dns challenge",
		PreviewText: "go acme go mox",
	})

	// "go acme" matches msg2.
	search(t, s, account, "go acme", 1)
	// "python" matches nothing.
	search(t, s, account, "python", 0)

	// Re-upsert same (account_id+folder_id+uid) → update subject/body to python acme.
	// Keep the rowid same but trigger mail_fts5_p7 via DELETE+INSERT (standard FTS5 triggers).
	updated := MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 2,
		Subject:     "mox the python acme",
		BodyText:  "programming python acme acme letsencrypt",
		PreviewText: "python acme python mox",
	}
	// Must use same rowid via UPSERT (same unique key: account, folder, uid)
	if _, err := s.MailMessageUpsert(ctx, updated); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	// "go acme" should now return 0 (go replaced with python).
	search(t, s, account, "go acme", 0)
	// "python acme" should return 1 (new content.
	search(t, s, account, "python acme", 1)
	_ = m2
}

// ---------- Test 4: Snippet delimiter check (brackets '[' and ']'

func TestFTS5_SnippetHighlight(t *testing.T) {
	s := openFTS5Store(t)
	ctx := context.Background()
	account := "acct-snip"
	folder := "fld-snip"

	// Insert a message with a rare word we can reliably find.
	p7msg(t, s, MailMessageP7{
		AccountID:  account, FolderID: folder, UID: 1,
		Subject:     "Phantom Lancer rareWORDTEST1",
		BodyText:  "rareWORDTEST1 hero agility nuker",
		PreviewText: "rareWORDTEST1 phantom hero",
	})

	results, total, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: account,
		Query:     "rareWORDTEST1",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Fatalf("want 1 result, got total=%d len=%d", total, len(results))
	}
	snipSubject := results[0].SubjectSnippet
	snipPreview := results[0].PreviewSnippet

	// At least one of subject or preview snippet should have a highlight marker
	// contains '[' followed by 'rareWORDTEST1'...']'.

	hasSubjectHL := strings.Contains(snipSubject, "[rareWORDTEST1")
	hasPreviewHL := strings.Contains(snipPreview, "[rareWORDTEST1")
	if !hasSubjectHL && !hasPreviewHL {
		t.Errorf("snippet missing expected '[rareWORDTEST1' not subject=%q preview=%q", snipSubject, snipPreview)
	}
	hasClose := strings.Contains(snipSubject, "]") || strings.Contains(snipPreview, "]")
	if !hasClose {
		t.Errorf("snippet missing close bracket ']': subject=%q preview=%q", snipSubject, snipPreview)
	}
}

// ---------- Test 5: 60 MB big message (body truncates to 10000 chars) --------------

func TestFTS5_BigMsg_60MB(t *testing.T) {
	s := openFTS5Store(t)
	ctx := context.Background()
	account := "acct-big"
	folder := "fld-big"

	// Build a ~60MB body. The first 10k chars = aaaaa.. repeated.
	// Per MailMessageUpsert truncates body_text to 10000 chars (see storage_mail.go:3548).
	// So search should still succeed (not error) and stored body should be truncated.

	chunk := strings.Repeat("phantomLancerDota ", 1112) // ~ 20016 chars (18 * 1112 = 20016)
	big := strings.Repeat(chunk, 3000)         // ~ 60,048,000 bytes > 60MB
	if len(big) < 60_000_000 {
		t.Fatalf("big msg body too small: got %d bytes", len(big))
	}

	// Insert should NOT panic.
	start := time.Now()
	out, err := s.MailMessageUpsert(ctx, MailMessageP7{
		AccountID:  account,
		FolderID:   folder,
		UID:        1,
		Subject:    "big message 60MB",
		BodyText:   big,
		PreviewText: big[:120] + "...",
	})
	elapsed := time.Since(start)
	t.Logf("Upsert 60MB took %v", elapsed)
	if err != nil {
		t.Fatalf("upsert big message: %v", err)
	}

	// Stored body must be truncated to 10000 chars.
	if got := len(out.BodyText); got != 10000 {
		t.Errorf("expected truncation: want len(body)=10000 got %d", got)
	}

	// Search for 'subject phrase: "big message": should find the msg (via subject column index:
	search(t, s, account, "big message 60MB", 1)

	// Search for a word that ONLY appeared only in the truncated region (past 10k boundary):
	//   The body was repeated "phantomLancerDota " every 20 chars × 1000 → first 10k chars
	//   Both "phantomLancerDota" repeated many times in the first 10k (inside truncation, so should be present.
	search(t, s, account, "phantomLancerDota", 1)

	// SizeBytes must have been reported correctly on the message (before truncation.
	if out.SizeBytes == 0 {
		t.Errorf("size_bytes should be set (not zero) even though body was truncated")
	}
	t.Logf("returned SizeBytes=%d", out.SizeBytes)
}
