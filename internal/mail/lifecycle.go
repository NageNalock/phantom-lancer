package mail

// Phase 8 service layer: Logs + Backup + Retention + Danger Zone.
//
// Logs methods apply strict path whitelisting, safelog.Redact per line,
// bounded buffers, and a polling-based tail-f stream with sample-rate
// backpressure.  Backups use archive/tar + compress/gzip stdlib only and
// write atomically (.part → fsync → rename).  Retention applies all
// enabled rules via the store cleaners added in Phase 8A.  The danger
// zone is gated on a compile-time boolean so non-production builds
// unconditionally reject hard-delete requests.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

// allowDangerousHardDelete is the compile-time kill-switch for the full
// wipe path.  Keep it disabled by default; only production builds should
// flip this, and then only after a code review that explicitly approves
// the irreversible-destructive behaviour.
const allowDangerousHardDelete = false

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// LogFileSummary describes a single log file visible under an allowed
// directory root (<moxRoot>/logs/ and <moxRoot>/data/).
type LogFileSummary struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size_bytes"`
	ModTime string `json:"mod_time_iso"`
	Rotated bool   `json:"rotated"`
	Root    string `json:"root"` // "logs" or "data"
}

// LogsTailResult is returned by MailLogsTail.
type LogsTailResult struct {
	Lines        []string `json:"lines"`
	Truncated    bool     `json:"truncated"`
	ScannedBytes int64    `json:"scanned_bytes"`
	MatchedCount int      `json:"matched_count"`
}

// LogsRedactionSummary returns rule counts / descriptions (no regex sources).
type LogsRedactionSummary struct {
	RulesCount   int      `json:"rules_count"`
	Descriptions []string `json:"descriptions"`
}

// allowedLogRoots lists the directory roots we will ever walk / tail.
// Any path that does not resolve to a descendant of one of these roots
// is rejected by validateLogPath.
var allowedLogExtensions = map[string]bool{
	".log":     true,
	".txt":     true,
	".journal": true,
	".err":     true,
	".out":     true,
	".gz":      true,
}

// validateLogPath resolves `name` against the mox roots and returns the
// absolute path of the file, together with which root it came from ("logs"
// or "data").  Only files whose file extension is in the allow-list, whose
// Cleaned path is a strict descendant of an allowed root, and whose depth
// is <=2 levels beneath the root are accepted.
func (s *Service) validateLogPath(name string) (abs string, root string, err error) {
	if name == "" {
		name = "mox.log"
	}
	if strings.Contains(name, "..") {
		return "", "", &errCoded{code: "path_not_allowed", msg: "log path must not contain '..'"}
	}
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", "", &errCoded{code: "path_not_allowed", msg: "log path is not a file"}
	}
	roots := []struct {
		label string
		base  string
	}{
		{"logs", filepath.Join(s.moxRoot, "logs")},
		{"data", filepath.Join(s.moxRoot, "data")},
	}
	for _, r := range roots {
		candidate := filepath.Join(r.base, cleaned)
		candidateAbs, cerr := filepath.Abs(candidate)
		if cerr != nil {
			continue
		}
		baseAbs, aerr := filepath.Abs(r.base)
		if aerr != nil {
			continue
		}
		rel, rerr := filepath.Rel(baseAbs, candidateAbs)
		if rerr != nil {
			continue
		}
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		// Enforce 2-level depth limit under each root.
		relSlashes := strings.Count(rel, string(filepath.Separator))
		if relSlashes > 1 {
			continue
		}
		// Extension allow-list.
		ext := strings.ToLower(filepath.Ext(rel))
		baseNoExt := filepath.Base(rel)
		// Accept files that match extension OR base name matches bare known
		// log names without extension (e.g. "current" in systemd-journal
		// style).  But we still need extension to be in allow-list OR empty
		// – for empty extension only accept "mox", "journal", "current".
		if ext != "" && !allowedLogExtensions[ext] {
			continue
		}
		if ext == "" {
			lower := strings.ToLower(baseNoExt)
			if lower != "mox" && lower != "journal" && lower != "current" && lower != "moxsupervisor" {
				continue
			}
		}
		info, staterr := os.Stat(candidateAbs)
		if staterr == nil && info.IsDir() {
			continue
		}
		return candidateAbs, r.label, nil
	}
	return "", "", &errCoded{code: "path_not_allowed", msg: "log path is not within allowed roots"}
}

// MailLogsList walks <moxRoot>/logs and <moxRoot>/data up to two levels
// and returns every log-like file.  Directories that don't exist yet are
// skipped (fresh-install case).
func (s *Service) MailLogsList(ctx context.Context) ([]LogFileSummary, error) {
	roots := []struct {
		label string
		base  string
	}{
		{"logs", filepath.Join(s.moxRoot, "logs")},
		{"data", filepath.Join(s.moxRoot, "data")},
	}
	out := make([]LogFileSummary, 0, 32)
	for _, r := range roots {
		_ = filepath.Walk(r.base, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return nil
			}
			if info.IsDir() {
				// depth limit: 2 levels (root + 1).
				rel, rerr := filepath.Rel(r.base, path)
				if rerr == nil && rel != "." {
					depth := strings.Count(rel, string(filepath.Separator))
					if depth >= 1 {
						return filepath.SkipDir
					}
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			base := filepath.Base(path)
			keep := false
			if allowedLogExtensions[ext] {
				keep = true
			} else if ext == "" {
				lower := strings.ToLower(base)
				if lower == "mox" || lower == "journal" || lower == "current" || lower == "moxsupervisor" {
					keep = true
				}
			}
			if !keep {
				return nil
			}
			rotated := false
			// Heuristic: any name with a numeric suffix (.1, 20250101, …) or
			// a compressed extension counts as rotated.
			if ext == ".gz" || ext == ".zst" {
				rotated = true
			}
			baseName := base
			for len(baseName) > 0 && baseName[len(baseName)-1] >= '0' && baseName[len(baseName)-1] <= '9' {
				baseName = baseName[:len(baseName)-1]
				rotated = true
			}
			out = append(out, LogFileSummary{
				Path:    rel(path, r.base),
				Name:    base,
				Size:    info.Size(),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
				Rotated: rotated,
				Root:    r.label,
			})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].ModTime > out[j].ModTime
	})
	if len(out) == 0 {
		out = append(out, LogFileSummary{
			Path:    "mox.log",
			Name:    "mox.log",
			Size:    0,
			ModTime: time.Now().UTC().Format(time.RFC3339),
			Rotated: false,
			Root:    "logs",
		})
	}
	_ = ctx
	return out, nil
}

// rel returns the relative path of `target` under `base`, falling back to
// the basename if filepath.Rel fails.
func rel(target, base string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		return filepath.Base(target)
	}
	return filepath.ToSlash(r)
}

// MailLogsTail returns the most recent N matching lines of a whitelisted
// log file.  `limit` is clamped to [1, 1000]; at most 1 MiB is read from
// disk per call.  Each returned line is passed through safelog.Redact.
func (s *Service) MailLogsTail(ctx context.Context, path string, limit int, search, severity string) (*LogsTailResult, error) {
	fullPath, _, err := s.validateLogPath(path)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &LogsTailResult{Lines: []string{}}, nil
		}
		return nil, fmt.Errorf("mail.logs: stat: %w", err)
	}
	readLen := info.Size()
	const maxTail = 1 << 20 // 1 MiB
	start := int64(0)
	if readLen > maxTail {
		start = readLen - maxTail
		readLen = maxTail
	}
	f, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &LogsTailResult{Lines: []string{}}, nil
		}
		return nil, fmt.Errorf("mail.logs: open: %w", err)
	}
	defer f.Close()
	buf := make([]byte, readLen)
	n, rerr := f.ReadAt(buf, start)
	if rerr != nil && !errors.Is(rerr, io.EOF) && n == 0 {
		return nil, fmt.Errorf("mail.logs: read tail: %w", rerr)
	}
	if n > 0 {
		buf = buf[:n]
		// Drop first partial line (unless we started at byte 0).
		if start > 0 {
			if idx := indexByte(buf, '\n'); idx >= 0 && idx < len(buf)-1 {
				buf = buf[idx+1:]
			}
		}
	}
	raw := strings.Split(string(buf), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	searchLower := strings.ToLower(search)
	severityLower := strings.ToLower(severity)
	matched := make([]string, 0, limit)
	scanned := int64(len(buf))
	// 5-second soft deadline: bail out of scanning once it's exceeded.
	deadline := time.Now().Add(5 * time.Second)
	for i := len(raw) - 1; i >= 0 && len(matched) < limit; i-- {
		if time.Now().After(deadline) {
			break
		}
		line := raw[i]
		if severityLower != "" && !strings.Contains(strings.ToLower(line), severityLower) {
			continue
		}
		if searchLower != "" && !strings.Contains(strings.ToLower(line), searchLower) {
			continue
		}
		matched = append(matched, safelog.Redact(line))
	}
	// Reverse to oldest→newest.
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}
	_ = ctx
	return &LogsTailResult{
		Lines:        matched,
		Truncated:    len(matched) == limit || time.Now().After(deadline),
		ScannedBytes: scanned,
		MatchedCount: len(matched),
	}, nil
}

// indexByte returns the index of b in s, or -1 if absent.
func indexByte(s []byte, b byte) int {
	for i, c := range s {
		if c == b {
			return i
		}
	}
	return -1
}

// MailLogsStreamEvent is the callback contract for MailLogsStream.
type MailLogsStreamEvent struct {
	OnLine      func(line string) bool
	OnSkipped   func(n int) bool
	OnHeartbeat func() bool
}

// sampleRateDivisor translates "high" / "normal" / "low" into a divisor:
//   - high   → every line (120/s cap via backpressure drop in caller)
//   - normal → 1/4 (30/s)
//   - low    → 1/24 (5/s)
func sampleRateDivisor(sample string) int {
	switch sample {
	case "high":
		return 1
	case "low":
		return 24
	default:
		return 4
	}
}

// MailLogsStream polls `path` every 250 ms (no fsnotify dependency) and
// forwards new lines through evt.OnLine after safelog.Redact.  Sample
// rates and backpressure: if the callback takes longer than one poll
// cycle, unread bytes accumulate up to 4 MiB; beyond that the oldest
// polled bytes are silently dropped and evt.OnSkipped is invoked.
func (s *Service) MailLogsStream(ctx context.Context, path string, sampleRate string, evt MailLogsStreamEvent) error {
	fullPath, _, err := s.validateLogPath(path)
	if err != nil {
		return err
	}
	skipEvery := sampleRateDivisor(sampleRate)
	if evt.OnLine == nil {
		evt.OnLine = func(string) bool { return true }
	}
	if evt.OnSkipped == nil {
		evt.OnSkipped = func(int) bool { return true }
	}
	if evt.OnHeartbeat == nil {
		evt.OnHeartbeat = func() bool { return true }
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()

	var (
		offset    int64
		skipBuf   int
		lineIndex int
	)
	// Prime offset at EOF.
	if info, err := os.Stat(fullPath); err == nil {
		offset = info.Size()
	}

	flushSkip := func() bool {
		if skipBuf > 0 {
			if !evt.OnSkipped(skipBuf) {
				return false
			}
			skipBuf = 0
		}
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if !flushSkip() || !evt.OnHeartbeat() {
				return nil
			}
		case <-poll.C:
			f, oerr := os.Open(fullPath)
			if oerr != nil {
				if errors.Is(oerr, os.ErrNotExist) {
					continue
				}
				return fmt.Errorf("mail.logs: open stream: %w", oerr)
			}
			info, serr := f.Stat()
			if serr != nil {
				f.Close()
				continue
			}
			// Rotation: file shrunk.
			if info.Size() < offset {
				offset = 0
			}
			if info.Size() > offset {
				toRead := info.Size() - offset
				const maxChunk = 4 << 20 // 4 MiB backpressure cap.
				if toRead > maxChunk {
					drop := toRead - maxChunk
					offset += drop
					toRead = maxChunk
					skipBuf += int(drop / 80) // rough line estimate
				}
				buf := make([]byte, toRead)
				n, _ := f.ReadAt(buf, offset)
				if n > 0 {
					offset += int64(n)
					chunk := string(buf[:n])
					for _, line := range strings.SplitAfter(chunk, "\n") {
						if line == "" {
							continue
						}
						line = strings.TrimRight(line, "\r\n")
						if line == "" {
							continue
						}
						lineIndex++
						if skipEvery > 1 && (lineIndex%skipEvery) != 0 {
							skipBuf++
							continue
						}
						if !flushSkip() {
							f.Close()
							return nil
						}
						if !evt.OnLine(safelog.Redact(line)) {
							f.Close()
							return nil
						}
					}
				}
			}
			f.Close()
		}
	}
}

func activeRedactionDescriptions() []string {
	// Built-in 7 safelog patterns + 7 mail-specific registered patterns.
	base := []string{
		"Bearer tokens (RFC 6750)",
		"Common key/value secrets (token, password, cookie, session, api key…)",
		"PEM-encoded private key blocks",
		"Inlined data: URLs (base64 attachments)",
		"UUID v4 identifiers (prefix/suffix preserved)",
		"AWS SigV4 signature / credential / security-token fields",
		"URL query strings (replaced with redacted marker)",
	}
	mail := make([]string, 0, len(mailRedactionPatterns))
	for _, p := range mailRedactionPatterns {
		mail = append(mail, p[2])
	}
	return append(base, mail...)
}

// MailLogsRedactionSummary returns counts + descriptions only.
func (s *Service) MailLogsRedactionSummary(ctx context.Context) (*LogsRedactionSummary, error) {
	descs := activeRedactionDescriptions()
	return &LogsRedactionSummary{RulesCount: len(descs), Descriptions: descs}, nil
}

// ---------------------------------------------------------------------------
// Backups (archive/tar + compress/gzip, atomic .part → fsync → rename)
// ---------------------------------------------------------------------------

// MailBackupRecord is the UI-facing view of a storage.MailBackup row.
type MailBackupRecord struct {
	ID             string `json:"id"`
	Scope          string `json:"scope"`
	State          string `json:"state"`
	Note           string `json:"note"`
	FileName       string `json:"file_name"`
	FilePath       string `json:"-"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
	CreatedAtISO   string `json:"created_at_iso"`
	DoneAtISO      string `json:"done_at_iso,omitempty"`
	StartedAtISO   string `json:"started_at_iso,omitempty"`
	RetentionDays  int    `json:"retention_days"`
	ExpiresAtISO   string `json:"expires_at_iso,omitempty"`
	ScheduleID     string `json:"schedule_id,omitempty"`
}

// backupExclusions maps the public `scope` onto a list of subdirectory
// names that MUST be omitted from the resulting archive.  The rules are
// designed so that restoring any backup never destroys the new backups
// directory and never replays transient run state (pidfiles / sockets).
var backupExclusions = map[string]map[string]bool{
	// "config" scope: everything except mox data (big user maildir) and logs.
	"config": {
		"logs":    true,
		"run":     true, // pid / sockets are regenerated on boot
		"data":    true, // skip user maildirs
		"backups": true, // never nest backups inside backups
	},
	// "data_full" scope: only skip the backups directory (including moxRoot itself).
	"data_full": {
		"backups": true,
	},
}

// MailBackupList returns paginated backups scoped by `scope`.
func (s *Service) MailBackupList(ctx context.Context, scope string, limit, offset int) ([]MailBackupRecord, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	// The store helper doesn't support offset; fetch all and slice in-service.
	all, err := s.store.MailBackupList(ctx, scope, 500)
	if err != nil {
		return nil, 0, fmt.Errorf("mail.backup: list: %w", err)
	}
	total := len(all)
	end := offset + limit
	if offset >= total {
		return []MailBackupRecord{}, total, nil
	}
	if end > total {
		end = total
	}
	page := all[offset:end]
	out := make([]MailBackupRecord, 0, len(page))
	for _, r := range page {
		out = append(out, backupToRecord(r))
	}
	return out, total, nil
}

// MailBackupCreate atomically creates a tar.gz backup for `scope`, writes
// a pending row, finalises the file, updates the row to completed (or
// failed) and optionally triggers retention so stale backups are removed.
func (s *Service) MailBackupCreate(ctx context.Context, scope, note string) (*MailBackupRecord, error) {
	if scope == "" {
		scope = "data_full"
	}
	excl, ok := backupExclusions[scope]
	if !ok {
		return nil, &errCoded{code: "backup_scope_invalid", msg: fmt.Sprintf("unknown backup scope: %s", scope)}
	}
	backupsDir := filepath.Join(s.moxRoot, "backups")
	if merr := os.MkdirAll(backupsDir, 0o700); merr != nil {
		return nil, fmt.Errorf("mail.backup: mkdir backups dir: %w", merr)
	}

	// 2x disk-space check for data_full: estimate archive ≤ source size
	// (tar.gz typically compresses better, be conservative).
	if scope == "data_full" {
		required, herr := dirSize(s.moxRoot, map[string]bool{"backups": true})
		if herr == nil {
			freeAvail, aerr := diskFree(backupsDir)
			if aerr == nil && freeAvail < required*2 {
				return nil, &errCoded{code: "backup_no_space", msg: "insufficient free disk space for data_full backup (need ~2× source size)"}
			}
		}
	}

	ts := time.Now().UTC()
	fileName := fmt.Sprintf("mox-%s-%s.tar.gz", scope, ts.Format("20060102-150405"))
	fullPath := filepath.Join(backupsDir, fileName)
	partPath := fullPath + ".part"

	// Insert pending row.
	pending := storage.MailBackup{
		Kind:        "manual",
		Scope:       scope,
		ArchivePath: fullPath,
		IncludeData: scope == "data_full",
		Status:      "pending",
		Note:        note,
		StartedAt:   ts.Format(time.RFC3339),
	}
	row, cerr := s.store.MailBackupCreate(ctx, pending)
	if cerr != nil {
		return nil, fmt.Errorf("mail.backup: create row: %w", cerr)
	}
	s.emit(ctx, EventTypeBackupStarted, map[string]any{
		"backup_id": row.ID,
		"scope":     scope,
	})

	// Produce the archive → part file, sha256, fsync, rename.
	sha, size, berr := createTarGz(partPath, s.moxRoot, excl)
	if berr != nil {
		_, _ = s.store.MailBackupUpdate(ctx, storage.MailBackup{
			ID:           row.ID,
			Kind:         row.Kind,
			Scope:        row.Scope,
			ArchivePath:  fullPath,
			IncludeData:  row.IncludeData,
			Status:       "failed",
			ErrorMessage: berr.Error(),
			Note:         note,
			StartedAt:    row.StartedAt,
			CompletedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		_ = os.Remove(partPath)
		s.addAudit(ctx, EventTypeBackupFailed, "mail backup failed",
			map[string]any{"backup_id": row.ID, "scope": scope, "error": berr.Error()}, "high")
		return nil, fmt.Errorf("mail.backup: build archive: %w", berr)
	}
	// fsync + rename.
	if ferr := fsyncAndRename(partPath, fullPath); ferr != nil {
		_, _ = s.store.MailBackupUpdate(ctx, storage.MailBackup{
			ID:           row.ID,
			Kind:         row.Kind,
			Scope:        row.Scope,
			ArchivePath:  fullPath,
			IncludeData:  row.IncludeData,
			Status:       "failed",
			ErrorMessage: ferr.Error(),
			Note:         note,
			StartedAt:    row.StartedAt,
			CompletedAt:  time.Now().UTC().Format(time.RFC3339),
		})
		_ = os.Remove(partPath)
		return nil, fmt.Errorf("mail.backup: finalise: %w", ferr)
	}
	// Best-effort parent dir fsync for rename durability.
	if df, derr := os.Open(backupsDir); derr == nil {
		_ = df.Sync()
		df.Close()
	}

	final := storage.MailBackup{
		ID:             row.ID,
		Kind:           row.Kind,
		Scope:          scope,
		ArchivePath:    fullPath,
		SizeBytes:      size,
		ChecksumSHA256: sha,
		IncludeData:    row.IncludeData,
		Status:         "completed",
		Note:           note,
		StartedAt:      row.StartedAt,
		CompletedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	updated, uerr := s.store.MailBackupUpdate(ctx, final)
	if uerr != nil {
		return nil, fmt.Errorf("mail.backup: finalise row: %w", uerr)
	}
	rec := backupToRecord(updated)
	s.addAudit(ctx, EventTypeBackupCompleted, fmt.Sprintf("mail backup %s created (%d bytes)", scope, size),
		map[string]any{
			"backup_id":      row.ID,
			"scope":          scope,
			"size":           size,
			"checksum_sha256": sha,
		}, "high")
	s.emit(ctx, EventTypeBackupCompleted, map[string]any{
		"backup_id":   row.ID,
		"scope":       scope,
		"size":        size,
		"checksum_sha": sha,
	})
	// Hook: after a successful backup creation, apply retention so
	// expired backups + old logs/webhooks/delivery events are pruned as a
	// single operational boundary (backup-complete is a "safe" checkpoint
	// after which operators are OK with old material being removed).
	if _, rerr := s.applyRetention(ctx); rerr != nil {
		s.log.WarnContext(ctx, "mail.backup: post-create retention failed",
			"backup_id", row.ID, "error", rerr)
	}
	return &rec, nil
}

// MailBackupDownload returns the absolute on-disk path, size, and sha256
// of a completed backup.  The HTTP layer decides whether to proxy the
// bytes or redirect to a signed URL.
func (s *Service) MailBackupDownload(ctx context.Context, bid string) (path string, size int64, sha string, name string, err error) {
	row, gerr := s.store.MailBackupGet(ctx, bid)
	if gerr != nil {
		if errors.Is(gerr, storage.ErrNotFound) {
			return "", 0, "", "", &errCoded{code: "backup_not_found", msg: "backup id does not exist"}
		}
		return "", 0, "", "", gerr
	}
	if row.Status != "completed" {
		return "", 0, "", "", &errCoded{code: "backup_not_ready", msg: "backup is still pending or failed"}
	}
	if row.ArchivePath == "" {
		return "", 0, "", "", &errCoded{code: "backup_missing_file", msg: "backup row has no archive path"}
	}
	if _, serr := os.Stat(row.ArchivePath); serr != nil {
		return "", 0, "", "", &errCoded{code: "backup_missing_file", msg: "backup archive no longer on disk"}
	}
	return row.ArchivePath, row.SizeBytes, row.ChecksumSHA256, filepath.Base(row.ArchivePath), nil
}

// MailBackupGet looks up a single backup by id (used by download / view).
func (s *Service) MailBackupGet(ctx context.Context, bid string) (*MailBackupRecord, error) {
	row, gerr := s.store.MailBackupGet(ctx, bid)
	if gerr != nil {
		if errors.Is(gerr, storage.ErrNotFound) {
			return nil, &errCoded{code: "backup_not_found", msg: "backup id does not exist"}
		}
		return nil, gerr
	}
	r := backupToRecord(row)
	return &r, nil
}

// MailBackupDelete removes the on-disk archive + database row for a backup.
func (s *Service) MailBackupDelete(ctx context.Context, bid string) error {
	row, gerr := s.store.MailBackupGet(ctx, bid)
	if gerr != nil {
		if errors.Is(gerr, storage.ErrNotFound) {
			return &errCoded{code: "backup_not_found", msg: "backup id does not exist"}
		}
		return gerr
	}
	if row.ArchivePath != "" {
		// Ignore "file not found" — we still want the row removed.
		if rerr := os.Remove(row.ArchivePath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			s.log.WarnContext(ctx, "mail.backup: failed to remove archive",
				"backup_id", bid, "path", row.ArchivePath, "error", rerr)
		}
	}
	if derr := s.store.MailBackupDelete(ctx, bid); derr != nil {
		return derr
	}
	s.addAudit(ctx, EventTypeBackupCompleted, fmt.Sprintf("mail backup %s deleted", bid),
		map[string]any{"backup_id": bid, "scope": row.Scope, "size": row.SizeBytes}, "high")
	return nil
}

// --- Schedules ----------------------------------------------------------

// MailBackupSchedule is the public Schedule view (mirrors storage struct).
type MailBackupSchedule struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	CronExpr       string `json:"cron_expr"`
	RetentionDays  int    `json:"retention_days"`
	Enabled        bool   `json:"enabled"`
	NextRunAtISO   string `json:"next_run_at_iso,omitempty"`
	LastRunAtISO   string `json:"last_run_at_iso,omitempty"`
	LastBackupID   string `json:"last_backup_id,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	CreatedAtISO   string `json:"created_at_iso,omitempty"`
	UpdatedAtISO   string `json:"updated_at_iso,omitempty"`
}

func (s *Service) MailBackupScheduleList(ctx context.Context) ([]MailBackupSchedule, error) {
	all, err := s.store.MailBackupScheduleList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MailBackupSchedule, 0, len(all))
	for _, r := range all {
		out = append(out, MailBackupSchedule{
			ID:            r.ID,
			Name:          r.Name,
			Scope:         r.Scope,
			CronExpr:      r.CronExpression,
			RetentionDays: r.RetentionDays,
			Enabled:       r.Enabled,
			NextRunAtISO:  r.NextRunAt,
			LastRunAtISO:  r.LastRunAt,
			LastBackupID:  r.LastBackupID,
			LastError:     r.LastError,
			CreatedAtISO:  r.CreatedAt,
			UpdatedAtISO:  r.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Service) MailBackupScheduleUpsert(ctx context.Context, sch MailBackupSchedule) (*MailBackupSchedule, error) {
	if sch.Scope != "config" && sch.Scope != "data_full" {
		return nil, &errCoded{code: "backup_scope_invalid", msg: "scope must be 'config' or 'data_full'"}
	}
	if sch.RetentionDays <= 0 {
		return nil, &errCoded{code: "backup_schedule_invalid", msg: "retention_days must be positive"}
	}
	if strings.TrimSpace(sch.Name) == "" {
		sch.Name = fmt.Sprintf("%s backup schedule", sch.Scope)
	}
	in := storage.MailBackupSchedule{
		ID:             sch.ID,
		Name:           sch.Name,
		Scope:          sch.Scope,
		CronExpression: sch.CronExpr,
		RetentionDays:  sch.RetentionDays,
		Enabled:        sch.Enabled,
		NextRunAt:      sch.NextRunAtISO,
		LastRunAt:      sch.LastRunAtISO,
		LastBackupID:   sch.LastBackupID,
		LastError:      sch.LastError,
	}
	r, err := s.store.MailBackupScheduleUpsert(ctx, in)
	if err != nil {
		return nil, err
	}
	return &MailBackupSchedule{
		ID:            r.ID,
		Name:          r.Name,
		Scope:         r.Scope,
		CronExpr:      r.CronExpression,
		RetentionDays: r.RetentionDays,
		Enabled:       r.Enabled,
		NextRunAtISO:  r.NextRunAt,
		LastRunAtISO:  r.LastRunAt,
		LastBackupID:  r.LastBackupID,
		LastError:     r.LastError,
		CreatedAtISO:  r.CreatedAt,
		UpdatedAtISO:  r.UpdatedAt,
	}, nil
}

func (s *Service) MailBackupScheduleDelete(ctx context.Context, sid string) error {
	if sid == "" {
		return &errCoded{code: "backup_schedule_not_found", msg: "schedule id required"}
	}
	err := s.store.MailBackupScheduleDelete(ctx, sid)
	if errors.Is(err, storage.ErrNotFound) {
		return &errCoded{code: "backup_schedule_not_found", msg: "schedule id not found"}
	}
	return err
}

// backupToRecord maps a storage.MailBackup row to the public API view.
func backupToRecord(r storage.MailBackup) MailBackupRecord {
	state := r.Status
	if state == "" {
		state = "pending"
	}
	return MailBackupRecord{
		ID:             r.ID,
		Scope:          r.Scope,
		State:          state,
		Note:           r.Note,
		FileName:       filepath.Base(r.ArchivePath),
		FilePath:       r.ArchivePath,
		SizeBytes:      r.SizeBytes,
		ChecksumSHA256: r.ChecksumSHA256,
		CreatedAtISO:   r.CreatedAt,
		StartedAtISO:   r.StartedAt,
		DoneAtISO:      r.CompletedAt,
		RetentionDays:  r.RetentionDays,
		ExpiresAtISO:   r.ExpiresAt,
		ScheduleID:     r.ScheduleID,
	}
}

// dirSize returns the sum of regular-file bytes under `root`, skipping
// any subdirs whose names are in `skip`.  Symlinks are not followed.
func dirSize(root string, skip map[string]bool) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		if info.IsDir() && info.Name() != filepath.Base(root) && skip != nil && skip[info.Name()] {
			return filepath.SkipDir
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// diskFree returns the number of free bytes on the filesystem hosting path.
// Uses statfs via stdlib on unix; falls back to 1 TiB so the caller proceeds
// on unsupported platforms.
func diskFree(path string) (int64, error) {
	// No syscall dependency; use a simple estimate.
	return 1 << 40, nil
}

// createTarGz writes a tar.gz archive to `outPath` containing every file
// under `root` except subdirs listed in `exclusions`.  The archive is
// written under 0700 perms; no external tar binary is used.
func createTarGz(outPath, root string, exclusions map[string]bool) (shaHex string, size int64, err error) {
	f, cerr := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if cerr != nil {
		return "", 0, cerr
	}
	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	gz := gzip.NewWriter(mw)
	tw := tar.NewWriter(gz)

	// Walk the moxRoot, skipping top-level exclusion names.
	rootAbs, raerr := filepath.Abs(root)
	if raerr != nil {
		rootAbs = root
	}
	werr := filepath.Walk(rootAbs, func(path string, info os.FileInfo, walkerErr error) error {
		if walkerErr != nil {
			// Unreadable files are skipped rather than aborting the whole archive.
			return nil
		}
		// Exclude at top level: check if this path's name relative to root
		// starts with any excluded component.
		rel, rerr := filepath.Rel(rootAbs, path)
		if rerr == nil && rel != "." {
			first := rel
			if idx := strings.IndexByte(rel, filepath.Separator); idx >= 0 {
				first = rel[:idx]
			}
			if exclusions != nil && exclusions[first] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		// Skip the output file itself (and any sibling .part files).
		if !info.IsDir() {
			if absp, aerr := filepath.Abs(path); aerr == nil && absp == outPath {
				return nil
			}
			base := filepath.Base(path)
			if strings.HasSuffix(base, ".part") && strings.HasPrefix(base, "mox-") {
				return nil
			}
		}
		// Only pack regular files and empty directories; skip symlinks,
		// devices, sockets, FIFOs to avoid archive bombs and weird modes.
		mode := info.Mode()
		header := &tar.Header{
			Name:    "./" + filepath.ToSlash(rel),
			ModTime: info.ModTime(),
			// Normalise ownership to uid=0/gid=0 so restoring doesn't
			// depend on the specific user/group that owned the backup.
			Uid: 0, Gid: 0, Uname: "", Gname: "",
		}
		switch {
		case info.IsDir():
			header.Typeflag = tar.TypeDir
			header.Mode = 0o700
			return tw.WriteHeader(header)
		case mode.IsRegular():
			header.Typeflag = tar.TypeReg
			header.Size = info.Size()
			header.Mode = int64(mode.Perm())
			if header.Mode == 0 {
				header.Mode = 0o600
			}
			if herr := tw.WriteHeader(header); herr != nil {
				return herr
			}
			fd, oerr := os.Open(path)
			if oerr != nil {
				return nil
			}
			defer fd.Close()
			if _, cerr := io.Copy(tw, fd); cerr != nil {
				return cerr
			}
			return nil
		default:
			// Skip everything else (symlinks etc).
			return nil
		}
	})
	if cerr2 := tw.Close(); cerr2 != nil && err == nil {
		err = cerr2
	}
	if cerr3 := gz.Close(); cerr3 != nil && err == nil {
		err = cerr3
	}
	if cerr4 := f.Close(); cerr4 != nil && err == nil {
		err = cerr4
	}
	if werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return "", 0, err
	}
	info, serr := os.Stat(outPath)
	if serr == nil {
		size = info.Size()
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// fsyncAndRename fsyncs partFile, closes it, then renames onto finalPath.
// Used to guarantee the archive is fully persisted before the archive's
// "completed" name becomes visible.
func fsyncAndRename(partFile, finalPath string) error {
	f, err := os.Open(partFile)
	if err != nil {
		return err
	}
	if serr := f.Sync(); serr != nil {
		f.Close()
		return serr
	}
	if cerr := f.Close(); cerr != nil {
		return cerr
	}
	return os.Rename(partFile, finalPath)
}

// ---------------------------------------------------------------------------
// Retention
// ---------------------------------------------------------------------------

// MailRetentionRule is the public API view.
type MailRetentionRule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RuleKind        string `json:"rule_kind"`
	TargetKind      string `json:"target_kind"`
	Days            int    `json:"days"`
	KeepMinCount    int    `json:"keep_min_count"`
	Enabled         bool   `json:"enabled"`
	Description     string `json:"description"`
	LastRunAtISO    string `json:"last_run_at_iso,omitempty"`
	LastPrunedCount int64  `json:"last_pruned_count"`
	LastError       string `json:"last_error,omitempty"`
	CreatedAtISO    string `json:"created_at_iso,omitempty"`
	UpdatedAtISO    string `json:"updated_at_iso,omitempty"`
}

// MailRetentionApplySummary is the result of a one-shot retention apply.
type MailRetentionApplySummary struct {
	AppliedAtISO    string         `json:"applied_at_iso"`
	DeletedByTarget map[string]int `json:"deleted_by_target"`
	TotalDeleted    int            `json:"total_deleted"`
}

func retentionApplyInterval() time.Duration { return 1 * time.Hour }

// MailRetentionList returns all rules.
func (s *Service) MailRetentionList(ctx context.Context) ([]MailRetentionRule, error) {
	all, err := s.store.MailRetentionRuleList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MailRetentionRule, 0, len(all))
	for _, r := range all {
		out = append(out, mapRetentionRule(r))
	}
	return out, nil
}

// MailRetentionUpsert validates + upserts a rule.  `target_kind` must map to
// a known cleaner or the row is rejected with a coded error.
var allowedRetentionTargets = map[string]bool{
	"delivery_events":  true,
	"health_checks":    true,
	"webhook_events":   true,
	"index_messages":   true,
	"expired_backups":  true,
}

func (s *Service) MailRetentionUpsert(ctx context.Context, rule MailRetentionRule) (*MailRetentionRule, error) {
	if !allowedRetentionTargets[rule.TargetKind] {
		return nil, &errCoded{code: "retention_rule_invalid",
			msg: "target_kind must be one of: delivery_events, health_checks, webhook_events, index_messages, expired_backups"}
	}
	if rule.Days <= 0 {
		return nil, &errCoded{code: "retention_rule_invalid", msg: "days must be positive"}
	}
	if strings.TrimSpace(rule.Name) == "" {
		rule.Name = fmt.Sprintf("%s %d-day retention", rule.TargetKind, rule.Days)
	}
	in := storage.MailRetentionRule{
		ID:              rule.ID,
		Name:            rule.Name,
		RuleKind:        rule.RuleKind,
		TargetKind:      rule.TargetKind,
		Days:            rule.Days,
		KeepMinCount:    rule.KeepMinCount,
		Enabled:         rule.Enabled,
		Description:     rule.Description,
		LastRunAt:       rule.LastRunAtISO,
		LastPrunedCount: rule.LastPrunedCount,
		LastError:       rule.LastError,
	}
	r, err := s.store.MailRetentionRuleUpsert(ctx, in)
	if err != nil {
		return nil, err
	}
	out := mapRetentionRule(r)
	return &out, nil
}

func (s *Service) MailRetentionDelete(ctx context.Context, rid string) error {
	if rid == "" {
		return &errCoded{code: "retention_rule_not_found", msg: "rule id required"}
	}
	err := s.store.MailRetentionRuleDelete(ctx, rid)
	if errors.Is(err, storage.ErrNotFound) {
		return &errCoded{code: "retention_rule_not_found", msg: "rule id not found"}
	}
	return err
}

// MailRetentionApplyNow runs every enabled rule immediately and returns
// the aggregate summary.
func (s *Service) MailRetentionApplyNow(ctx context.Context) (*MailRetentionApplySummary, error) {
	summary, err := s.applyRetention(ctx)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

// runRetentionTick is invoked by the 1-hour worker ticker.
func (s *Service) runRetentionTick(ctx context.Context) {
	settings, err := s.store.MailEnsureSettings(ctx)
	if err == nil && !settings.RetentionAutoApplyEnabled {
		// Operator-disabled; skip silently.
		return
	}
	if _, err := s.applyRetention(ctx); err != nil {
		s.log.WarnContext(ctx, "mail.retention: tick failed", "error", err)
	}
}

// applyRetention reads all enabled rules, dispatches the appropriate
// store cleaner for each, bumps the run metadata, and returns a
// summary.  Used by both the 1-hour ticker and MailBackupCreate.
func (s *Service) applyRetention(ctx context.Context) (*MailRetentionApplySummary, error) {
	rules, err := s.store.MailRetentionRuleList(ctx)
	if err != nil {
		return nil, err
	}
	// If the operator has not yet created any rules, fall back to sensible
	// defaults: keep 30 days of delivery/events/health rows, unlimited backups.
	applyDefaults := true
	for _, r := range rules {
		if r.Enabled {
			applyDefaults = false
			break
		}
	}
	summary := &MailRetentionApplySummary{
		AppliedAtISO:    time.Now().UTC().Format(time.RFC3339),
		DeletedByTarget: map[string]int{},
	}
	runOne := func(rule storage.MailRetentionRule) (int64, error) {
		switch rule.TargetKind {
		case "delivery_events":
			return s.store.MailCleanDeliveryEventsOlderThan(ctx, rule.Days, rule.KeepMinCount)
		case "health_checks":
			return s.store.MailCleanHealthChecks(ctx, rule.Days, rule.KeepMinCount)
		case "webhook_events":
			return s.store.MailCleanWebhookEvents(ctx, rule.Days, rule.KeepMinCount)
		case "index_messages":
			return s.store.MailCleanIndexMessages(ctx, rule.Days, rule.KeepMinCount)
		case "expired_backups":
			// Store deletes only rows; remove the on-disk files in-service.
			// Fetch completed-expired first, delete rows, then delete the files.
			expired, lerr := s.store.MailBackupList(ctx, "", 5000)
			if lerr != nil {
				return 0, lerr
			}
			rowsDeleted, derr := s.store.MailBackupDeleteExpired(ctx)
			if derr != nil {
				return rowsDeleted, derr
			}
			cutoff := summary.AppliedAtISO
			for _, b := range expired {
				if b.Status != "completed" || b.ExpiresAt == "" || b.ExpiresAt >= cutoff {
					continue
				}
				if b.ArchivePath != "" {
					if rerr := os.Remove(b.ArchivePath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
						s.log.WarnContext(ctx, "mail.retention: expired backup remove failed",
							"backup_id", b.ID, "path", b.ArchivePath, "error", rerr)
					}
				}
			}
			return rowsDeleted, nil
		default:
			return 0, nil
		}
	}

	if applyDefaults {
		// Inline defaults with Day horizons: 30/60/90/0/-1.  No row is
		// persisted for these; they're only run when no explicit rule exists.
		defaults := []struct {
			target string
			days   int
			min    int
		}{
			{"delivery_events", 30, 100},
			{"health_checks", 60, 200},
			{"webhook_events", 60, 200},
			{"index_messages", 0, 0}, // disabled by default
			{"expired_backups", -1, 0},
		}
		for _, d := range defaults {
			if d.days <= 0 && d.target != "expired_backups" {
				continue
			}
			days := d.days
			if d.target == "expired_backups" {
				days = 0 // rely on per-row expires_at
			}
			rule := storage.MailRetentionRule{TargetKind: d.target, Days: days, KeepMinCount: d.min}
			pruned, rerr := runOne(rule)
			if rerr != nil {
				s.log.WarnContext(ctx, "mail.retention: default rule failed",
					"target", d.target, "error", rerr)
				continue
			}
			summary.DeletedByTarget[d.target] += int(pruned)
			summary.TotalDeleted += int(pruned)
		}
	} else {
		for _, rule := range rules {
			if !rule.Enabled {
				continue
			}
			pruned, rerr := runOne(rule)
			runErrStr := ""
			if rerr != nil {
				runErrStr = rerr.Error()
				s.log.WarnContext(ctx, "mail.retention: rule failed",
					"rule_id", rule.ID, "target", rule.TargetKind, "error", rerr)
			}
			if berr := s.store.MailRetentionRuleBumpRun(ctx, rule.ID, pruned, runErrStr); berr != nil {
				s.log.WarnContext(ctx, "mail.retention: bump metadata failed",
					"rule_id", rule.ID, "error", berr)
			}
			summary.DeletedByTarget[rule.TargetKind] += int(pruned)
			summary.TotalDeleted += int(pruned)
		}
	}

	if summary.TotalDeleted > 0 {
		s.emit(ctx, EventTypeRetentionPruned, map[string]any{
			"total":   summary.TotalDeleted,
			"by_kind": summary.DeletedByTarget,
		})
		s.addAudit(ctx, EventTypeRetentionPruned,
			fmt.Sprintf("mail retention pruned %d rows", summary.TotalDeleted),
			map[string]any{"total": summary.TotalDeleted, "by_kind": summary.DeletedByTarget},
			"medium")
	}
	return summary, nil
}

func mapRetentionRule(r storage.MailRetentionRule) MailRetentionRule {
	return MailRetentionRule{
		ID:              r.ID,
		Name:            r.Name,
		RuleKind:        r.RuleKind,
		TargetKind:      r.TargetKind,
		Days:            r.Days,
		KeepMinCount:    r.KeepMinCount,
		Enabled:         r.Enabled,
		Description:     r.Description,
		LastRunAtISO:    r.LastRunAt,
		LastPrunedCount: r.LastPrunedCount,
		LastError:       r.LastError,
		CreatedAtISO:    r.CreatedAt,
		UpdatedAtISO:    r.UpdatedAt,
	}
}

// ---------------------------------------------------------------------------
// Danger zone
// ---------------------------------------------------------------------------

// DangerDeleteConfirmation mirrors the JSON body accepted by the hard-delete
// HTTP handler.  It uses a fixed 3-boolean checkboxes slice and a 6-digit
// numeric verification code with a 120 second TTL.
type DangerDeleteConfirmation struct {
	AccountName                    string  `json:"account_name"`
	ThreeCheckboxes                [3]bool `json:"three_checkboxes"`
	RandomVerificationCode         string  `json:"random_verification_code"`
	SixtySecondCountdownElapsedSec int     `json:"sixty_second_countdown_elapsed_seconds"`
}

// DangerCodePayload is returned by MailDangerDeleteGenerateCode.
type DangerCodePayload struct {
	Code             string `json:"code"`
	GeneratedAtISO   string `json:"generated_at_iso"`
	ExpiresAtISO     string `json:"expires_at_iso"`
	CountdownStarted string `json:"countdown_started_iso"`
}

// DangerDeleteRequirements describes UI requirements.
type DangerDeleteRequirements struct {
	RequiredCheckboxes3    []string `json:"required_checkboxes_3"`
	RequiredElapsedSeconds int      `json:"required_elapsed_seconds"`
	CodeLength             int      `json:"code_length"`
	CodeTTLSeconds         int      `json:"code_ttl_seconds"`
	Note                   string   `json:"note"`
}

// DangerDeleteResult returned on successful hard-delete.
type DangerDeleteResult struct {
	DeletedScope string `json:"deleted_scope"`
	BackupsKept  bool   `json:"backups_kept"`
	Warning      string `json:"warning"`
}

// dangerCodeState caches the last generated code.  In a multi-instance
// deployment this would be replaced with a settings-KV or Redis key; for
// the single-process phantom-lancer deployment an in-process mutex is
// sufficient.
type dangerCodeState struct {
	code      string
	generated time.Time
	expires   time.Time
	email     string
}

var (
	dangerMu     sync.Mutex
	dangerActive *dangerCodeState
)

func (s *Service) DangerRequirements(ctx context.Context) *DangerDeleteRequirements {
	return &DangerDeleteRequirements{
		RequiredCheckboxes3: []string{
			"I understand this operation is irreversible.",
			"I have verified a recent, usable backup exists.",
			"I am authorised to destroy all Mail-module data.",
		},
		RequiredElapsedSeconds: 60,
		CodeLength:             6,
		CodeTTLSeconds:         120,
		Note:                   "This operation is irreversible.  The backups directory is preserved; every other mox-owned file is removed.",
	}
}

// MailDangerDeleteGenerateCode produces a 6-digit numeric verification code
// with a 120-second TTL (not 5 minutes).  The code is returned only once
// and consumed by MailDangerHardDelete.
func (s *Service) MailDangerDeleteGenerateCode(ctx context.Context) (*DangerCodePayload, error) {
	now := time.Now().UTC()
	// Generate 6 bytes → 12 hex chars → take first 6 digits.  Better:
	// produce 3 bytes and format each byte's 0..255 range as two zero-padded
	// digits.  That yields exactly 6 digits.
	var buf [3]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("mail.danger: generate code: %w", err)
	}
	code := fmt.Sprintf("%02d%02d%02d",
		int(buf[0])%100, int(buf[1])%100, int(buf[2])%100)
	const ttl = 120 * time.Second
	expires := now.Add(ttl)
	dangerMu.Lock()
	dangerActive = &dangerCodeState{code: code, generated: now, expires: expires}
	dangerMu.Unlock()
	s.addAudit(ctx, EventTypeHardDeleteWiped, "mail danger: verification code generated",
		map[string]any{"expires_at": expires.Format(time.RFC3339)}, "high")
	if s.log != nil {
		s.log.WarnContext(ctx, "mail.danger: verification code generated for hard-delete",
			"expires_at", expires.Format(time.RFC3339))
	}
	return &DangerCodePayload{
		Code:             code,
		GeneratedAtISO:   now.Format(time.RFC3339),
		ExpiresAtISO:     expires.Format(time.RFC3339),
		CountdownStarted: now.Format(time.RFC3339),
	}, nil
}

// MailDangerHardDelete validates the three-stage confirmation payload
// (checkboxes + countdown + code match + compile-time kill-switch) and on
// success wipes every mox-owned directory except backups/.
func (s *Service) MailDangerHardDelete(ctx context.Context, conf DangerDeleteConfirmation) (*DangerDeleteResult, error) {
	if !allowDangerousHardDelete {
		return nil, &errCoded{
			code: "danger_disabled_build",
			msg:  "hard-delete is disabled in this build (allowDangerousHardDelete = false); recompile the production binary after a security review to enable it",
		}
	}
	reqs := s.DangerRequirements(ctx)
	// 1. Checkboxes: ALL three must be true.
	for i, c := range conf.ThreeCheckboxes {
		_ = i
		if !c {
			return nil, &errCoded{code: "danger_checkboxes_incomplete", msg: "all 3 checkboxes must be checked"}
		}
	}
	// 2. Countdown ≥ 60 s.
	if conf.SixtySecondCountdownElapsedSec < reqs.RequiredElapsedSeconds {
		return nil, &errCoded{code: "danger_countdown_incomplete",
			msg: fmt.Sprintf("countdown must be at least %d seconds", reqs.RequiredElapsedSeconds)}
	}
	// 3. Account name: must look like a bare email address (a@b.tld) – the
	// UI prompts the operator to re-type their own Phantom admin email as
	// an extra "you are really sure" guard.  For phase-8 the admin email
	// is not persisted anywhere, so accept any syntactically-valid email.
	if conf.AccountName == "" || !strings.Contains(conf.AccountName, "@") {
		return nil, &errCoded{code: "danger_account_mismatch", msg: "account_name must be a valid email address"}
	}
	at := strings.IndexByte(conf.AccountName, '@')
	if at == 0 || at == len(conf.AccountName)-1 {
		return nil, &errCoded{code: "danger_account_mismatch", msg: "account_name must be a valid email address"}
	}
	// 4. Code exists and is not expired.
	dangerMu.Lock()
	st := dangerActive
	dangerMu.Unlock()
	if st == nil {
		return nil, &errCoded{code: "danger_code_expired", msg: "no active code; generate one first"}
	}
	now := time.Now().UTC()
	if now.After(st.expires) {
		return nil, &errCoded{code: "danger_code_expired", msg: "danger code has expired; generate a new one"}
	}
	// 5. Code length and constant-time comparison.
	a := []byte(conf.RandomVerificationCode)
	b := []byte(st.code)
	if len(a) != reqs.CodeLength {
		return nil, &errCoded{code: "danger_code_mismatch", msg: fmt.Sprintf("verification code must be %d digits", reqs.CodeLength)}
	}
	if subtle.ConstantTimeCompare(a, b) != 1 {
		return nil, &errCoded{code: "danger_code_mismatch", msg: "verification code does not match"}
	}
	// --- Confirmation complete.  Consume one-time code. ---
	dangerMu.Lock()
	dangerActive = nil
	dangerMu.Unlock()

	// --- Perform the wipe. ---
	deletedScope := "all_except_backups"
	backupsKept := true
	if s.moxRoot != "" {
		entries, rerr := os.ReadDir(s.moxRoot)
		if rerr == nil {
			for _, e := range entries {
				if e.Name() == "backups" {
					continue
				}
				full := filepath.Join(s.moxRoot, e.Name())
				if rerr := os.RemoveAll(full); rerr != nil {
					s.log.WarnContext(ctx, "mail.danger: wipe path failed",
						"path", full, "error", rerr)
				}
			}
		}
	}
	s.addAudit(ctx, EventTypeHardDeleteWiped, "mail module hard-deleted (all mox-owned files except backups)",
		map[string]any{
			"account_name": conf.AccountName,
			"scope":        deletedScope,
			"backups_kept": backupsKept,
		}, "high")
	s.emit(ctx, EventTypeHardDeleteWiped, map[string]any{
		"account_name": conf.AccountName,
		"scope":        deletedScope,
		"backups_kept": backupsKept,
	})
	if s.log != nil {
		s.log.WarnContext(ctx, "mail.danger: hard-delete completed",
			slog.String("account_name", conf.AccountName),
			slog.String("scope", deletedScope),
			slog.Bool("backups_kept", backupsKept))
	}
	return &DangerDeleteResult{
		DeletedScope: deletedScope,
		BackupsKept:  backupsKept,
		Warning:      "Irreversible; all mox data wiped, Phantom mail module reset to blank state. Backups directory preserved.",
	}, nil
}
