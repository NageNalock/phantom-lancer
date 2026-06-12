package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// ---------- helpers ----------

// repeatByte returns a string of n bytes all equal to b.
func repeatByte(b byte, n int64) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return string(buf)
}

// ---------- Subtest 1: 60MB SizeBytes + oversized metadata fields truncation ----------

// 60 * 1024 * 1024 = 62914560 bytes.  This is the size-bytes threshold the
// user's spec mandates we exercise.  SQLite TEXT is blob-backed so even very
// large TEXT values round-trip fine; we just want to confirm the upsert path
// does NOT panic / error / corrupt the database on big payloads, and that
// MailMessageUpsert's documented 10000-char BodyText truncation fires.
func TestBigMessage_60MB_SizeThreshold(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()

	const sixtyMB = 60 * 1024 * 1024 // 62914560

	// BodyText deliberately LARGER than the 10000-char truncation limit
	// implemented inside MailMessageUpsert so we can assert truncation.
	// We keep it short-of-ridiculous (1 MB of 'x') because storing 60 MB of
	// TEXT in-memory just to be truncated is wasteful; the *SizeBytes*
	// column is what carries the 60 MB signal and is the interesting field.
	hugeBody := repeatByte('x', 1<<20) // 1 MB text body
	hugePreview := repeatByte('p', 50_000)
	hugeSubject := strings.Repeat("Big subject chunk with token quokka ", 500) // ~35 KB
	hugeFrom := strings.Repeat("user@big-example.com, ", 2000)[:20_000]
	hugeTo := strings.Repeat("recip@big-example.com, ", 2000)[:20_000]

	orig := MailMessageP7{
		AccountID:    "acc-huge",
		FolderID:     "fld-inbox",
		UID:          42,
		Subject:      hugeSubject,
		FromListCSV:  hugeFrom,
		ToListCSV:    hugeTo,
		DateSent:     "2025-06-01T00:00:00Z",
		Internaldate: "2025-06-01T00:00:00Z",
		SizeBytes:    sixtyMB,
		PreviewText:  hugePreview,
		BodyText:     hugeBody,
		FlagsCSV:     `\Seen`,
	}

	inserted, err := s.MailMessageUpsert(ctx, orig)
	if err != nil {
		t.Fatalf("MailMessageUpsert 60MB message: %v", err)
	}
	if inserted.ID == "" {
		t.Errorf("inserted row missing ID")
	}
	// SizeBytes should be PRESERVED (not clamped, not zeroed).
	if inserted.SizeBytes != sixtyMB {
		t.Errorf("SizeBytes round-trip: want %d, got %d", sixtyMB, inserted.SizeBytes)
	}

	// MailMessageUpsert truncates BodyText to 10000 chars; confirm that cap.
	if got := len(inserted.BodyText); got != 10000 {
		t.Errorf("BodyText truncation: want exactly 10000 chars after upsert, got %d", got)
	}
	// Make sure truncation is deterministic (all 'x' chars, not garbage).
	if bytes.Count([]byte(inserted.BodyText), []byte("x")) != 10000 {
		t.Errorf("truncated BodyText isn't all 'x': got %q (len %d)",
			ellipsis(inserted.BodyText, 40), len(inserted.BodyText))
	}

	// ---------- Read it back via Get / List / Search ----------

	got, err := s.MailMessageGet(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("MailMessageGet: %v", err)
	}
	if got.SizeBytes != sixtyMB {
		t.Errorf("Get: SizeBytes want %d got %d", sixtyMB, got.SizeBytes)
	}
	if len(got.BodyText) != 10000 {
		t.Errorf("Get: BodyText want 10000 chars got %d", len(got.BodyText))
	}
	if got.Subject != hugeSubject {
		// Subject wasn't documented to be truncated — if the implementation
		// truncates Subject too, length would differ.  We just assert it's
		// non-empty so we notice if anything is silently dropped.
		if len(got.Subject) == 0 {
			t.Errorf("Get: Subject is empty (was %d chars originally)", len(hugeSubject))
		}
	}

	// List query (by account+folder) must return our 60 MB row.
	list, _, err := s.MailMessageList(ctx, "acc-huge", "fld-inbox", 5, 0)
	if err != nil {
		t.Fatalf("MailMessageListP7: %v", err)
	}
	found := false
	for _, m := range list {
		if m.ID == inserted.ID {
			found = true
			if m.SizeBytes != sixtyMB {
				t.Errorf("list: SizeBytes want %d got %d", sixtyMB, m.SizeBytes)
			}
			break
		}
	}
	if !found {
		t.Errorf("60MB message not returned from MailMessageListP7 (got %d results)", len(list))
	}

	// FTS5 should still index the truncated body; search for "quokka" which
	// only appears in the Subject, NOT in the truncated 'x' body.
	search, tot, err := s.MailMessageSearchP7(ctx, FTSQueryP7{
		AccountID: "acc-huge",
		Query:     "quokka",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("MailMessageSearchP7 quokka: %v", err)
	}
	if tot != 1 {
		t.Errorf("search for 'quokka' should match 60MB message subject: want 1 got %d", tot)
	}
	if len(search) != 1 || search[0].ID != inserted.ID {
		t.Errorf("search result mismatch: %+v", search)
	}

	// ---------- Delete the 60MB message, assert FTS5 + base row removed ----------
	baseBefore := countBaseRows(t, s)
	ftsBefore := countFTS5Rows(t, s)
	if baseBefore != 1 || ftsBefore != 1 {
		t.Fatalf("pre-delete rows: base=%d fts5=%d (want 1,1)", baseBefore, ftsBefore)
	}
	if err := s.MailMessageDelete(ctx, inserted.ID); err != nil {
		t.Fatalf("MailMessageDelete 60MB row: %v", err)
	}
	if countBaseRows(t, s) != 0 || countFTS5Rows(t, s) != 0 {
		t.Errorf("post-delete rows: base=%d fts5=%d (want 0,0)",
			countBaseRows(t, s), countFTS5Rows(t, s))
	}
}

// ---------- Subtest 2: SizeBytes edge cases (zero, negative, 1 byte) ----------

func TestBigMessage_SizeBytesEdges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	cases := []struct {
		name string
		size int64
	}{
		{"zero", 0},
		{"one", 1},
		{"small_attach", 256 * 1024},          // 256 KB
		{"mid", 4*1024*1024 + 777},            // 4 MB + delta
		{"just_below_60mb", 60*1024*1024 - 1}, // 62914559
		{"well_above", 512 * 1024 * 1024},     // 512 MB (synthetic, file won't be that big)
		{"max_int64_safeish", 1<<62 - 1},      // 4611686018427387903
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := quickMsg("a", "f", 0, tc.name, "x", "y", tc.name, "body")
			m.SizeBytes = tc.size
			ins, err := s.MailMessageUpsert(ctx, m)
			if err != nil {
				t.Fatalf("upsert size=%d: %v", tc.size, err)
			}
			if ins.SizeBytes != tc.size {
				t.Errorf("SizeBytes not preserved: input %d stored %d", tc.size, ins.SizeBytes)
			}
			got, err := s.MailMessageGet(ctx, ins.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.SizeBytes != tc.size {
				t.Errorf("SizeBytes Get mismatch: want %d got %d", tc.size, got.SizeBytes)
			}
			// Cleanup so the base table stays small.
			if err := s.MailMessageDelete(ctx, ins.ID); err != nil {
				t.Fatalf("cleanup delete: %v", err)
			}
		})
	}
}

// ---------- Subtest 3: Concurrent upserts of big messages (race detector) ----------

func TestBigMessage_ConcurrentInserts(t *testing.T) {
	requireFTS5(t)
	s := openTestStore(t)
	ctx := context.Background()
	const N = 10
	done := make(chan error, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			// Per-goroutine unique subject so FTS5 tokens are distinct too.
			m := quickMsg("a", "f", int64(i),
				"concurrent large msg "+string(rune('A'+(i%26))),
				"from", "to",
				repeatByte('p', 20_000),
				repeatByte('b', 200_000))
			m.SizeBytes = 30*1024*1024 + int64(i) // 30 MB + i
			_, err := s.MailMessageUpsert(ctx, m)
			done <- err
		}(i)
	}
	errs := 0
	for i := 0; i < N; i++ {
		if err := <-done; err != nil {
			errs++
			t.Errorf("goroutine insert error: %v", err)
		}
	}
	if errs > 0 {
		t.Fatalf("%d of %d concurrent inserts failed", errs, N)
	}
	// All N rows + N FTS5 rows.
	if got := countBaseRows(t, s); got != N {
		t.Errorf("base rows after concurrent inserts: want %d got %d", N, got)
	}
	if got := countFTS5Rows(t, s); got != N {
		t.Errorf("fts5 rows after concurrent inserts: want %d got %d", N, got)
	}
}

// ---------- helper ----------

// ellipsis returns s capped to maxLen with trailing "…" if longer.
func ellipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
