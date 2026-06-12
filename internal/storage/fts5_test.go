package storage

import (
	"context"
	"strings"
	"testing"
)

// ---------- helpers ----------

// requireFTS5 skips the calling test when the linked SQLite driver does not
// ship the FTS5 virtual-table module.  Tests that assert row counts in
// mail_fts5_p7 or expect FTS5-specific behaviour (BM25 ranking, snippets with
// [ ] delimiters, trigger-driven sync) MUST call this first; tests that only
// exercise search through MailMessageSearchP7 (which has a LIKE fallback) or
// base-table CRUD can omit the guard.
func requireFTS5(t *testing.T) {
	t.Helper()
	if !FTS5Available() {
		t.Skip("Skipping FTS5-dependent test: sqlite3 was not built with the fts5 module. " +
			"Re-run with `-tags sqlite_fts5` to enable this suite.")
	}
}

// NOTE: openTestStore is imported from object_storage_test.go (same package).
// It calls storage.Open with t.TempDir() + a quiet logger + t.Cleanup Close.
//
// To keep fts5_test.go self-contained we only rely on the common helper
// being present (it is, in object_storage_test.go).

// quickMsg builds a MailMessageP7 with enough fields populated to survive
// upsert + to show up in FTS searches.  body is stored in BodyText.
func quickMsg(accountID, folderID string, uid int64, subject, from, to, preview, body string) MailMessageP7 {
	return MailMessageP7{
		AccountID:    accountID,
		FolderID:     folderID,
		UID:          uid,
		Subject:      subject,
		FromListCSV:  from,
		ToListCSV:    to,
		DateSent:     "2025-06-01T12:00:00Z",
		Internaldate: "2025-06-01T12:00:00Z",
		SizeBytes:    int64(len(subject) + len(body)),
		PreviewText:  preview,
		BodyText:     body,
	}
}

// countFTS5Rows returns the number of rows in the mail_fts5_p7 content table.
// FTS5 exposes contentless-table row count via the special `mail_fts5_p7`
// query syntax; a cheap SELECT rowid FROM mail_fts5_p7 is enough.
func countFTS5Rows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	row := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM mail_fts5_p7`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count mail_fts5_p7: %v", err)
	}
	return n
}

// countBaseRows returns the mail_messages_p7 row count (all).
func countBaseRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	row := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM mail_messages_p7`)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count mail_messages_p7: %v", err)
	}
	return n
}

// ---------- Subtest 1: FTS5 inserts match base table row count ----------

func TestFTS5_InsertSync(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()

	msgs := []MailMessageP7{
		quickMsg("acc-1", "fld-inbox", 1,
			"Project kickoff", "Alice <a@x.com>", "team@x.com",
			"Let's schedule the kickoff meeting next week",
			"Hi team, let's schedule the project kickoff meeting next Tuesday. Please bring your slides."),
		quickMsg("acc-1", "fld-inbox", 2,
			"Weekly report", "Bob <b@x.com>", "alice@x.com, charlie@x.com",
			"Attached is the weekly status report for the project",
			"The project is on track. Please review the attached weekly report. Velocity looks good."),
		quickMsg("acc-1", "fld-sent", 3,
			"Lunch invitation", "Charlie <c@x.com>", "alice@x.com",
			"Want to grab lunch after the meeting?",
			"Hey Alice, want to grab lunch? I know a great project-shaped burger place."),
		quickMsg("acc-2", "fld-inbox", 101,
			"Re: Project kickoff", "Dave <d@y.com>", "eve@y.com",
			"Regarding the kickoff, I'll be traveling that week",
			"I won't be able to make the kickoff meeting; please share notes."),
	}
	for _, m := range msgs {
		if _, err := s.MailMessageUpsert(ctx, m); err != nil {
			t.Fatalf("upsert uid=%d: %v", m.UID, err)
		}
	}
	if got, want := countBaseRows(t, s), len(msgs); got != want {
		t.Fatalf("base row count: want %d, got %d", want, got)
	}
	if got, want := countFTS5Rows(t, s), len(msgs); got != want {
		t.Fatalf("fts5 row count: want %d, got %d", want, got)
	}
}

// ---------- Subtest 2: Search tokens correctly ranked by bm25 + snippets have [ ] delimiters ----------

func TestFTS5_SearchAndSnippets(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()

	// Msg A: subject contains "wombat" once.
	// Msg B: body contains "wombat" 30 times → higher bm25 rank.
	mkRep := func(token string, n int) string {
		return strings.TrimRight(strings.Repeat(token+" ", n), " ")
	}
	if _, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 1,
		"wombat sighting", "x@x", "y@y",
		"A rare subject-only mention",
		"This body doesn't mention the subject keyword more than once — ranking: low.")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 2,
		"Monthly roundup", "x@x", "z@z",
		"Weekly roundup of "+mkRep("wombat", 30),
		"Roundup: full-body content unrelated to the search token")); err != nil {
		t.Fatal(err)
	}
	// Msg C: doesn't contain the term.
	if _, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 3,
		"Unrelated", "q@q", "r@r",
		"completely different subject",
		"nothing to see here, moving right along.")); err != nil {
		t.Fatal(err)
	}

	q := FTSQueryP7{AccountID: "a", Query: "wombat", Limit: 10}
	results, total, err := s.MailMessageSearchP7(ctx, q)
	if err != nil {
		t.Fatalf("MailMessageSearchP7: %v", err)
	}
	if total != 2 {
		t.Fatalf("total hits: want 2, got %d", total)
	}
	if len(results) != 2 {
		t.Fatalf("len(results): want 2, got %d", len(results))
	}
	// Result #1 (highest bm25) should be msg uid=2 (many wombat mentions).
	if results[0].UID != 2 {
		t.Errorf("first result should be UID=2 (heaviest term density), got UID=%d snippet=%q",
			results[0].UID, results[0].SubjectSnippet+results[0].PreviewSnippet)
	}
	// Either subject_snippet or preview_snippet should contain a `[wombat]`.
	combined := results[0].SubjectSnippet + " | " + results[0].PreviewSnippet
	if !strings.Contains(combined, "[") || !strings.Contains(combined, "]") {
		t.Errorf("top result snippet should use [term] delimiters, got %q", combined)
	}
	// Msg C should be absent (no [wombat] in it at all).
	for _, r := range results {
		if r.UID == 3 {
			t.Errorf("UID=3 matched but shouldn't have: %+v", r)
		}
	}
}

// ---------- Subtest 3: Update → FTS5 re-indexes (subject change) ----------

func TestFTS5_UpdateReindex(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()
	m := quickMsg("a", "f", 1,
		"ORIGINAL subject with hedgehog", "x@x", "y@y",
		"preview",
		"hedgehog body content hedgehog")
	inserted, err := s.MailMessageUpsert(ctx, m)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Search "hedgehog" → 1 hit.
	hits1, tot1, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "hedgehog", Limit: 10})
	if err != nil {
		t.Fatalf("search 1: %v", err)
	}
	if tot1 != 1 || len(hits1) != 1 {
		t.Fatalf("after insert: want 1 hit, got %d (total=%d)", len(hits1), tot1)
	}

	// Update subject/body: remove "hedgehog", add "narwhal".
	updated := inserted
	updated.Subject = "Completely new title about narwhal"
	updated.BodyText = "entire new body with narwhal narwhal narwhal"
	if _, err := s.MailMessageUpsert(ctx, updated); err != nil {
		t.Fatalf("re-upsert (update): %v", err)
	}
	// FTS5 and base still have exactly 1 row (1 update, not 1 insert + 1).
	if got := countBaseRows(t, s); got != 1 {
		t.Errorf("base rows after update: want 1, got %d", got)
	}
	if got := countFTS5Rows(t, s); got != 1 {
		t.Errorf("fts5 rows after update: want 1, got %d", got)
	}

	// "hedgehog" should now return 0 hits (term fully removed).
	hits2, tot2, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "hedgehog", Limit: 10})
	if err != nil {
		t.Fatalf("search 2: %v", err)
	}
	if tot2 != 0 || len(hits2) != 0 {
		t.Errorf("hedgehog should be gone after update, got %d hits (total=%d)", len(hits2), tot2)
	}
	// "narwhal" should return exactly 1 hit now.
	hits3, tot3, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "narwhal", Limit: 10})
	if err != nil {
		t.Fatalf("search 3: %v", err)
	}
	if tot3 != 1 || len(hits3) != 1 {
		t.Errorf("narwhal should appear exactly once after update, got %d hits (total=%d)", len(hits3), tot3)
	}
}

// ---------- Subtest 4: Delete → FTS5 removes the row; search returns 0 ----------

func TestFTS5_DeleteSync(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()
	m1, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 1,
		"axolotl", "x", "y", "axolotl preview", "axolotl axolotl axolotl"))
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	m2, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 2,
		"beluga", "x", "y", "beluga preview", "beluga beluga beluga"))
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if countFTS5Rows(t, s) != 2 || countBaseRows(t, s) != 2 {
		t.Fatalf("after 2 inserts: base=%d fts5=%d (want 2 each)",
			countBaseRows(t, s), countFTS5Rows(t, s))
	}

	// Delete axolotl.
	if err := s.MailMessageDelete(ctx, m1.ID); err != nil {
		t.Fatalf("delete m1: %v", err)
	}
	if got := countBaseRows(t, s); got != 1 {
		t.Errorf("base rows after delete: want 1, got %d", got)
	}
	if got := countFTS5Rows(t, s); got != 1 {
		t.Errorf("fts5 rows after delete: want 1, got %d", got)
	}

	// axolotl search → 0 hits.
	_, totA, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "axolotl", Limit: 10})
	if err != nil {
		t.Fatalf("axolotl search: %v", err)
	}
	if totA != 0 {
		t.Errorf("axolotl still has %d hits after deletion", totA)
	}
	// beluga still there.
	res, totB, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "beluga", Limit: 10})
	if err != nil {
		t.Fatalf("beluga search: %v", err)
	}
	if totB != 1 || len(res) != 1 || res[0].ID != m2.ID {
		t.Errorf("beluga should return exactly 1 hit matching m2, got %d total=%d first_id=%s",
			len(res), totB, pickID(res))
	}
}

// ---------- Subtest 5: UpdateFlags updates FTS5 (flags_csv change, subject unchanged) ----------

// The FTS5 trigger runs on ANY UPDATE of mail_messages_p7, even when only
// non-FTS columns change.  We still want to verify the trigger does not
// corrupt the FTS5 index in this case.
func TestFTS5_UpdateFlags_StillSearchable(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()
	m, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 1,
		"subject quokka", "x", "y", "preview", "body says quokka"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.MailMessageUpdateFlags(ctx, m.ID, `\Seen,\Answered`); err != nil {
		t.Fatalf("update flags: %v", err)
	}
	// Exactly 1 base row + 1 fts5 row.
	if countBaseRows(t, s) != 1 {
		t.Errorf("base rows after flag update: want 1 got %d", countBaseRows(t, s))
	}
	if countFTS5Rows(t, s) != 1 {
		t.Errorf("fts5 rows after flag update: want 1 got %d", countFTS5Rows(t, s))
	}
	// Term "quokka" still matches exactly once.
	_, tot, err := s.MailMessageSearchP7(ctx, FTSQueryP7{AccountID: "a", Query: "quokka", Limit: 10})
	if err != nil {
		t.Fatalf("quokka search: %v", err)
	}
	if tot != 1 {
		t.Errorf("quokka still searchable after flag update (want 1, got %d)", tot)
	}
}

// ---------- Subtest 6: Per-account scoping (term present in both, AccountID filter) ----------

func TestFTS5_AccountScoping(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()
	accounts := []string{"acc-a", "acc-b", "acc-c"}
	for i, acc := range accounts {
		for j := 0; j < 3; j++ {
			uid := int64(i*100 + j)
			body := "platypus"
			if (i+j)%2 == 0 {
				body += " platypus platypus" // make denser to vary ranking
			}
			if _, err := s.MailMessageUpsert(ctx, quickMsg(acc, "f", uid,
				"Message about platypus", "x", "y", "platypus news", body)); err != nil {
				t.Fatalf("upsert acc=%s j=%d: %v", acc, j, err)
			}
		}
	}
	if countFTS5Rows(t, s) != 9 {
		t.Fatalf("after 9 inserts: fts5 rows want 9 got %d", countFTS5Rows(t, s))
	}

	// Querying with AccountID = "acc-b" should give exactly 3 hits.
	res, tot, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: "acc-b",
		Query:     "platypus",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("search acc-b platypus: %v", err)
	}
	if tot != 3 {
		t.Errorf("acc-b total hits want 3 got %d", tot)
	}
	if len(res) != 3 {
		t.Errorf("acc-b results want 3 got %d", len(res))
	}
	// All returned results should belong to acc-b.
	for _, r := range res {
		if r.AccountID != "acc-b" {
			t.Errorf("leaked result from account %s when querying acc-b", r.AccountID)
		}
	}
	// No AccountID filter returns ALL 9 rows.
	_, totAll, err := s.MailMessageSearchP7(ctx, FTSQueryP7{Query: "platypus", Limit: 100})
	if err != nil {
		t.Fatalf("search all accounts: %v", err)
	}
	if totAll != 9 {
		t.Errorf("global platypus search: want 9 total, got %d", totAll)
	}
}

// ---------- Subtest 7: Graceful fallback when query token doesn't match ----------

func TestFTS5_NoMatchReturnsEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if _, err := s.MailMessageUpsert(ctx, quickMsg("a", "f", 1,
		"Hello", "x", "y", "p", "body here")); err != nil {
		t.Fatal(err)
	}
	_, tot, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: "a",
		Query:     "zzzno_such_token_xyz",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("search for nonexistent term: %v", err)
	}
	if tot != 0 {
		t.Errorf("nonexistent term returned %d hits", tot)
	}
}

// ---------- helper ----------

func pickID(res []MailSearchResultP7) string {
	if len(res) == 0 {
		return ""
	}
	return res[0].ID
}
