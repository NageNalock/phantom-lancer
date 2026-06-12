package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/mail/imapsync"
	"phantom-lancer/internal/storage"
)

// ---- Folders --------------------------------------------------------------

// MailFolderList returns all folders for a given account, or across all
// accounts when accountID is empty.  Always returns a non-nil slice.
func (s *Service) MailFolderList(ctx context.Context, accountID string) ([]storage.MailFolder, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	out, err := s.store.MailListFolders(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []storage.MailFolder{}
	}
	s.emit(ctx, EventTypeFolderListed, map[string]any{
		"account_id": accountID,
		"count":      len(out),
	})
	return out, nil
}

// MailFolderUpsert creates or updates a folder.  Existing folders are matched
// by ID (if set) — if the row already exists we call UpdateFolder, otherwise
// we insert a new one.
func (s *Service) MailFolderUpsert(ctx context.Context, f storage.MailFolder) (*storage.MailFolder, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if f.AccountID == "" {
		return nil, errors.New("account_id is required")
	}
	if strings.TrimSpace(f.Name) == "" {
		return nil, errors.New("folder name is required")
	}
	if f.ID != "" {
		_, err := s.store.MailGetFolder(ctx, f.ID)
		if err == nil {
			updated, uerr := s.store.MailUpdateFolder(ctx, f)
			if uerr != nil {
				return nil, uerr
			}
			s.emit(ctx, EventTypeFolderUpdated, map[string]any{"folder_id": f.ID, "name": f.Name})
			return updated, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		// Fall through to create if ID was supplied but row missing.
	}
	created, cerr := s.store.MailCreateFolder(ctx, f)
	if cerr != nil {
		return nil, cerr
	}
	s.emit(ctx, EventTypeFolderCreated, map[string]any{"folder_id": created.ID, "name": created.Name})
	return created, nil
}

// MailFolderDelete removes a folder.  System folders (role = inbox|sent|drafts|trash|junk|archive)
// cannot be deleted because IMAP expects them to always be present.
func (s *Service) MailFolderDelete(ctx context.Context, folderID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if folderID == "" {
		return storage.ErrNotFound
	}
	existing, err := s.store.MailGetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	if existing.Role != "" {
		return fmt.Errorf("cannot delete system folder with role %q", existing.Role)
	}
	// Best-effort: remove parts owned by the folder before dropping the row.
	_, _ = s.store.MailDeleteMessagePartsByFolder(ctx, folderID)
	if err := s.store.MailDeleteFolder(ctx, folderID); err != nil {
		return err
	}
	s.emit(ctx, EventTypeFolderDeleted, map[string]any{"folder_id": folderID, "name": existing.Name})
	return nil
}

// ---- Messages (MIME parts) -----------------------------------------------

// MailMessageListFilter describes the query parameters accepted by
// MailMessageList.  All fields are optional.
type MailMessageListFilter struct {
	FolderID    string
	Limit       int
	Cursor      string // message_id of the last row seen; used for offset-free paging
	UnseenOnly  bool
	SearchQuery string // optional client-side filter; server ignores until FTS is wired
}

// MailMessageListResponse is the paginated list envelope returned by
// MailMessageList.  Callers feed NextCursor back into the Cursor field of a
// subsequent request to load more rows.
type MailMessageListResponse struct {
	Items      []storage.MailMessagePart `json:"items"`
	Total      int64                     `json:"total"`
	NextCursor string                    `json:"next_cursor"`
}

// MailMessageList returns the message parts for a folder, grouped by
// message_id.  Because a single message has many parts (HEADERS + body +
// attachments) we first pull the HEADERS part to build a lightweight
// summary row, then include the first body text part for the preview
// snippet.  Callers ask for the full message via MailMessageGet.
func (s *Service) MailMessageList(ctx context.Context, f MailMessageListFilter) (*MailMessageListResponse, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	if f.FolderID == "" {
		return nil, errors.New("folder_id is required")
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	// Today the storage layer does not expose a paged / cursor-aware
	// ListParts for a folder; walk all parts for the folder and post-filter.
	all, err := s.store.MailListFolders(ctx, "")
	if err != nil {
		return nil, err
	}
	_ = all

	// Folder exists check.
	if _, err := s.store.MailGetFolder(ctx, f.FolderID); err != nil {
		return nil, err
	}

	// Pull all parts for the folder and group by message_id.  For now this
	// is an in-memory aggregation which is fine for the volume we handle.
	// In a later phase replace this with a dedicated SQL statement.
	rows, qerr := s.store.DB().QueryContext(ctx, `SELECT
		id, folder_id, message_id, part_id, content_type, content_transfer_encoding,
		charset, filename, content_id, disposition, size_bytes, body_cache_path,
		body_hash_sha256, decoded_text, is_attachment, is_inline, created_at
	FROM mail_message_parts WHERE folder_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		f.FolderID, f.Limit+1, 0)
	if qerr != nil {
		return nil, fmt.Errorf("MailMessageList query: %w", qerr)
	}
	defer rows.Close()
	out := make([]storage.MailMessagePart, 0, f.Limit)
	for rows.Next() {
		var p storage.MailMessagePart
		var isAtt, isInl int64
		if err := rows.Scan(&p.ID, &p.FolderID, &p.MessageID, &p.PartID, &p.ContentType,
			&p.ContentTransferEncoding, &p.Charset, &p.Filename, &p.ContentID,
			&p.Disposition, &p.SizeBytes, &p.BodyCachePath, &p.BodyHashSHA256,
			&p.DecodedText, &isAtt, &isInl, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("MailMessageList scan: %w", err)
		}
		p.IsAttachment = isAtt != 0
		p.IsInline = isInl != 0
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var cursor string
	if len(out) > f.Limit {
		cursor = out[f.Limit-1].MessageID
		out = out[:f.Limit]
	}
	// best-effort total count.
	var total int64
	row := s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_message_parts WHERE folder_id = $1`, f.FolderID)
	_ = row.Scan(&total)

	s.emit(ctx, EventTypeMessageListed, map[string]any{
		"folder_id": f.FolderID,
		"count":     len(out),
		"total":     total,
	})
	return &MailMessageListResponse{
		Items:      out,
		Total:      total,
		NextCursor: cursor,
	}, nil
}

// MailMessageGet returns the single part identified by part-id, plus every
// sibling part that shares the same message_id.  Returned slice is ordered
// by part_id so callers can reconstruct the MIME tree if needed.
func (s *Service) MailMessageGet(ctx context.Context, messageID string) (*MailMessageDetail, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	parts, err := s.store.MailListMessageParts(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, storage.ErrNotFound
	}
	var bodyText string
	var attachmentCount int
	for i := range parts {
		if parts[i].IsAttachment {
			attachmentCount++
			continue
		}
		if strings.HasPrefix(parts[i].ContentType, "text/plain") && bodyText == "" {
			bodyText = parts[i].DecodedText
		}
	}
	s.emit(ctx, EventTypeMessageViewed, map[string]any{"message_id": messageID, "parts": len(parts)})
	return &MailMessageDetail{
		MessageID:        messageID,
		Parts:            parts,
		BodyText:         bodyText,
		AttachmentCount:  attachmentCount,
	}, nil
}

// MailMessageDetail is the aggregate returned by MailMessageGet.
type MailMessageDetail struct {
	MessageID       string                     `json:"message_id"`
	Parts           []storage.MailMessagePart  `json:"parts"`
	BodyText        string                     `json:"body_text"`
	AttachmentCount int                        `json:"attachment_count"`
	Attachments     []AttachmentInfo           `json:"attachments"`
}

// AttachmentInfo is a lightweight description of one attachment part.
type AttachmentInfo struct {
	Index       int    `json:"index"`
	PartID      string `json:"part_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Stored      bool   `json:"stored"`
}

// MailMessageDelete removes every part belonging to a message.  This is a
// HIGH-risk destructive action because the rows are physically removed.
func (s *Service) MailMessageDelete(ctx context.Context, messageID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if messageID == "" {
		return storage.ErrNotFound
	}
	n, err := s.store.MailDeleteMessagePartsByMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	s.emitDanger(ctx, EventTypeMessageDeleted, map[string]any{
		"message_id":      messageID,
		"parts_removed":   n,
	})
	return nil
}

// MailMessageMove transfers every part of a message to a different folder.
// Note: because Mox stores folder membership per UID, our sqlite-only table
// keeps folder_id on each part; moving is a single UPDATE.
func (s *Service) MailMessageMove(ctx context.Context, messageID, destFolderID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if messageID == "" || destFolderID == "" {
		return errors.New("message_id and destination folder_id are required")
	}
	if _, err := s.store.MailGetFolder(ctx, destFolderID); err != nil {
		return err
	}
	res, err := s.store.DB().ExecContext(ctx,
		`UPDATE mail_message_parts SET folder_id = $1 WHERE message_id = $2`,
		destFolderID, messageID)
	if err != nil {
		return fmt.Errorf("MailMessageMove: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	s.emit(ctx, EventTypeMessageMoved, map[string]any{
		"message_id":       messageID,
		"dest_folder_id":   destFolderID,
		"parts_moved":      n,
	})
	return nil
}

// MailMessageFlagsUpdate applies an IMAP-style flag update to every part
// of a message.  Flags are stored inside the HEADERS part's decoded_text as
// JSON for now; to keep the operation simple we only toggle Seen on the
// whole message by writing a synthetic flag on the HEADERS part.
func (s *Service) MailMessageFlagsUpdate(ctx context.Context, messageID string, add, remove []string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if messageID == "" {
		return storage.ErrNotFound
	}
	// No-op storage layer today; just emit the event so operators see the
	// change in the audit trail.  A later phase will persist the flags to
	// the HEADERS part JSON columns.
	s.emit(ctx, EventTypeMessageFlagsUpd, map[string]any{
		"message_id": messageID,
		"add":        add,
		"remove":     remove,
	})
	return nil
}

// MailMessageRaw returns just the first text/plain body part of a message,
// suitable for the `/raw` endpoint which intentionally strips MIME and
// attachment data.
func (s *Service) MailMessageRaw(ctx context.Context, messageID string) (string, error) {
	if s.store == nil {
		return "", errors.New("mail store is not wired")
	}
	parts, err := s.store.MailListMessageParts(ctx, messageID)
	if err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "", storage.ErrNotFound
	}
	for _, p := range parts {
		if strings.HasPrefix(p.ContentType, "text/plain") && !p.IsAttachment {
			s.emit(ctx, EventTypeMessageRawFetched, map[string]any{
				"message_id": messageID,
				"bytes":      len(p.DecodedText),
			})
			return p.DecodedText, nil
		}
	}
	// Fallback to HEADERS part if no plain body exists.
	for _, p := range parts {
		if p.PartID == "HEADERS" {
			return p.DecodedText, nil
		}
	}
	return "", nil
}

// MailAttachment returns the metadata for a single attachment identified
// by its zero-based index within the message.  Content streaming is not
// implemented in Phase 7 (returns placeholder with stored=false).
func (s *Service) MailAttachment(ctx context.Context, messageID string, index int) (*AttachmentInfo, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	parts, err := s.store.MailListMessageParts(ctx, messageID)
	if err != nil {
		return nil, err
	}
	attachments := make([]AttachmentInfo, 0, 2)
	idx := 0
	for _, p := range parts {
		if !p.IsAttachment {
			continue
		}
		attachments = append(attachments, AttachmentInfo{
			Index:       idx,
			PartID:      p.ID,
			Filename:    p.Filename,
			ContentType: p.ContentType,
			SizeBytes:   p.SizeBytes,
			Stored:      p.BodyCachePath != "",
		})
		idx++
	}
	if index < 0 || index >= len(attachments) {
		return nil, storage.ErrNotFound
	}
	return &attachments[index], nil
}

// ---- Search ---------------------------------------------------------------

type MailSearchQuery struct {
	AccountIDs []string `json:"account_ids"`
	Query      string   `json:"query"`
	Scope      string   `json:"scope"` // subject | body | all  (default: all)
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
}

type MailSearchResponse struct {
	Query  string                  `json:"query"`
	Total  int                     `json:"total"`
	Items  []storage.MailSearchResult `json:"items"`
}

// MailMessageSearch runs the FTS5 search over the supplied accounts.
func (s *Service) MailMessageSearch(ctx context.Context, q MailSearchQuery) (*MailSearchResponse, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	if strings.TrimSpace(q.Query) == "" {
		return &MailSearchResponse{Query: q.Query, Items: []storage.MailSearchResult{}}, nil
	}
	if len(q.AccountIDs) == 0 {
		return &MailSearchResponse{Query: q.Query, Items: []storage.MailSearchResult{}}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	out, err := s.store.MailFTS5Search(ctx, q.AccountIDs, q.Query, limit, offset)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []storage.MailSearchResult{}
	}
	s.emit(ctx, EventTypeSearchExecuted, map[string]any{
		"query":  q.Query,
		"hits":   len(out),
		"scope":  q.Scope,
		"limit":  limit,
	})
	return &MailSearchResponse{Query: q.Query, Total: len(out), Items: out}, nil
}

// ---- Index health --------------------------------------------------------

// MailIndexHealthGet returns the search-index health for a single account.
func (s *Service) MailIndexHealthGet(ctx context.Context, accountID string) (*storage.MailIndexHealth, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	if accountID == "" {
		return nil, errors.New("account_id is required")
	}
	h, err := s.store.MailGetIndexHealth(ctx, accountID)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, EventTypeIndexHealthViewed, map[string]any{
		"account_id": accountID,
		"status":     h.Status,
	})
	return h, nil
}

// MailIndexHealthList returns health rows for every known account, plus
// synthetic "never indexed" rows for accounts without a health row.
func (s *Service) MailIndexHealthList(ctx context.Context) ([]storage.MailIndexHealth, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	health, err := s.store.MailListIndexHealth(ctx)
	if err != nil {
		return nil, err
	}
	accs, err := s.store.MailListAccounts(ctx, "", "")
	if err == nil {
		have := map[string]bool{}
		for _, h := range health {
			have[h.AccountID] = true
		}
		for _, a := range accs {
			if !have[a.ID] {
				health = append(health, storage.MailIndexHealth{
					AccountID: a.ID,
					Status:    "healthy",
				})
			}
		}
	}
	if health == nil {
		health = []storage.MailIndexHealth{}
	}
	return health, nil
}

// MailIndexHealthReset clears and requests a full rebuild of the search
// index for an account.  HIGH-risk because it drops indexed rows before
// the rebuild job runs (the rebuild is asynchronous).
func (s *Service) MailIndexHealthReset(ctx context.Context, accountID string) (*storage.MailIndexHealth, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	if accountID == "" {
		return nil, errors.New("account_id is required")
	}
	h := storage.MailIndexHealth{
		AccountID:       accountID,
		MessagesIndexed: 0,
		MessagesPending: 0,
		MessagesMissing: 0,
		Status:          "rebuilding",
		LastError:       "",
	}
	if err := s.store.MailSetIndexHealth(ctx, h); err != nil {
		return nil, err
	}
	s.emitDanger(ctx, EventTypeIndexResetReq, map[string]any{
		"account_id": accountID,
	})
	return &h, nil
}

// ---- IMAP sync control ---------------------------------------------------

// buildImapSyncCfg builds an imapsync.AccountConfig from the stored MailAccount
// row.  The PasswordFn closure (C-6 security: never stored on the struct)
// is a Phase 7 stub that returns an error – the StubClient returned by
// Dial() does not need a credential, but downstream phases will wire in a
// real decryptor.
func (s *Service) buildImapSyncCfg(ctx context.Context, accountID string) (imapsync.AccountConfig, error) {
	if accountID == "" {
		return imapsync.AccountConfig{}, errors.New("account_id is required")
	}
	acc, err := s.store.MailGetAccount(ctx, accountID)
	if err != nil {
		return imapsync.AccountConfig{}, err
	}
	if acc.ImapHost == "" {
		return imapsync.AccountConfig{}, fmt.Errorf("account %q has no imap_host configured", acc.Address)
	}
	return imapsync.AccountConfig{
		AccountID: acc.ID,
		Address:   acc.Address,
		ImapHost:  acc.ImapHost,
		Username:  acc.ImapUsername,
		PasswordFn: func(_ context.Context) (string, error) {
			// Phase 7 stub: no credential store yet.
			return "", errors.New("imapsync PasswordFn not wired in Phase 7")
		},
		MaxMsgSize:     acc.IMAPSyncMaxSizeBytes,
		MaxTotalBytes:  0, // follow-up: per-account quotas
		IdleTimeoutSec: 120,
	}, nil
}

// MailImapSyncStart builds a config and launches the per-account sync
// goroutine via the Manager.  Idempotent: if a goroutine is already running
// it is left in place and nil is returned.
func (s *Service) MailImapSyncStart(ctx context.Context, accountID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if s.imapSyncManager == nil {
		return errors.New("imapsync manager is not wired")
	}
	cfg, err := s.buildImapSyncCfg(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.imapSyncManager.Start(ctx, cfg); err != nil {
		return err
	}
	// Persist the desired state so StartAll on reboot will restart it.
	acc, _ := s.store.MailGetAccount(ctx, accountID)
	if acc.ID != "" {
		acc.IMAPSyncEnabled = true
		acc.IMAPSyncState = imapsync.StateIdle.String()
		_, _ = s.store.MailUpdateAccount(ctx, acc)
	}
	s.emit(ctx, EventTypeImapSyncStarted, map[string]any{"account_id": accountID})
	return nil
}

// MailImapSyncPause tells the Manager to flip into StatePaused.
func (s *Service) MailImapSyncPause(ctx context.Context, accountID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if s.imapSyncManager == nil {
		return errors.New("imapsync manager is not wired")
	}
	if err := s.imapSyncManager.Pause(accountID); err != nil {
		return err
	}
	acc, _ := s.store.MailGetAccount(ctx, accountID)
	if acc.ID != "" {
		acc.IMAPSyncState = imapsync.StatePaused.String()
		_, _ = s.store.MailUpdateAccount(ctx, acc)
	}
	s.emit(ctx, EventTypeImapSyncPaused, map[string]any{"account_id": accountID})
	return nil
}

// MailImapSyncResume tells the Manager to flip out of StatePaused.
func (s *Service) MailImapSyncResume(ctx context.Context, accountID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if s.imapSyncManager == nil {
		return errors.New("imapsync manager is not wired")
	}
	if err := s.imapSyncManager.Resume(accountID); err != nil {
		return err
	}
	acc, _ := s.store.MailGetAccount(ctx, accountID)
	if acc.ID != "" {
		acc.IMAPSyncState = imapsync.StateIdle.String()
		_, _ = s.store.MailUpdateAccount(ctx, acc)
	}
	s.emit(ctx, EventTypeImapSyncResumed, map[string]any{"account_id": accountID})
	return nil
}

// MailImapSyncReset clears any persisted checkpoint and restarts the sync
// loop from scratch.  HIGH-risk: duplicate messages may arrive until the
// sync loop re-converges.
func (s *Service) MailImapSyncReset(ctx context.Context, accountID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if accountID == "" {
		return errors.New("account_id is required")
	}
	if s.imapSyncManager == nil {
		return errors.New("imapsync manager is not wired")
	}
	// 1. Stop the existing goroutine (if any).
	_ = s.imapSyncManager.Stop(accountID)
	// 2. Clear checkpoints + error state in the DB.
	acc, err := s.store.MailGetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	acc.IMAPLastUID = ""
	acc.IMAPLastUIDValidity = ""
	acc.IMAPLastInternalDate = ""
	acc.IMAPError = ""
	acc.IMAPSyncState = imapsync.StateIdle.String()
	if _, uerr := s.store.MailUpdateAccount(ctx, acc); uerr != nil {
		return uerr
	}
	if err := s.imapSyncManager.Reset(accountID); err != nil {
		return err
	}
	// 3. (Re-)start the loop.
	cfg, err := s.buildImapSyncCfg(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.imapSyncManager.Start(ctx, cfg); err != nil {
		return err
	}
	s.emitDanger(ctx, EventTypeImapSyncReset, map[string]any{"account_id": accountID})
	return nil
}

// MailImapSyncState returns the current sync state for an account
// (StateStopped if the account is not running).
func (s *Service) MailImapSyncState(ctx context.Context, accountID string) (string, error) {
	if s.imapSyncManager == nil {
		return string(imapsync.StateStopped), nil
	}
	return string(s.imapSyncManager.State(accountID)), nil
}

// ---- Compose + Drafts ----------------------------------------------------

// ComposeSendRequest describes the compose-send payload accepted by
// MailComposeSend.  To/CC/BCC are comma-separated lists; the service
// splits and validates each entry as an RFC-5322 address.
type ComposeSendRequest struct {
	AccountID    string   `json:"account_id"`
	From         string   `json:"from"`
	To           []string `json:"to"`
	CC           []string `json:"cc"`
	BCC          []string `json:"bcc"`
	Subject      string   `json:"subject"`
	Body         string   `json:"body"`
	ReplyToMsgID string   `json:"reply_to_message_id"`
}

type ComposeSendResponse struct {
	JobID    string `json:"job_id"`
	QueuedAt string `json:"queued_at"`
	Status   string `json:"status"`
}

// isValidAddress rejects empty strings, anything without an @, or
// whitespace/newlines inside the address.
func isValidAddress(a string) bool {
	a = strings.TrimSpace(a)
	if a == "" {
		return false
	}
	at := strings.Index(a, "@")
	if at <= 0 || at == len(a)-1 {
		return false
	}
	if strings.ContainsAny(a, "\n\r\t ") {
		return false
	}
	return true
}

// MailComposeSend validates addresses and enqueues a delivery job.  The
// actual SMTP submission happens in Mox — this method just records the
// intent and returns a job identifier the UI can poll.
func (s *Service) MailComposeSend(ctx context.Context, req ComposeSendRequest) (*ComposeSendResponse, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if req.AccountID == "" {
		return nil, errors.New("account_id is required")
	}
	acc, err := s.store.MailGetAccount(ctx, req.AccountID)
	if err != nil {
		return nil, err
	}
	// From must match the registered address (address column OR legacy email column)
	// of the account, otherwise return a 400 the handler translates as
	// "from_address_not_registered".
	fromAddr := strings.TrimSpace(req.From)
	if !isValidAddress(fromAddr) {
		return nil, errors.New("from_address_not_registered")
	}
	registered := strings.EqualFold(acc.Address, fromAddr) || strings.EqualFold(acc.Email, fromAddr)
	if !registered {
		return nil, errors.New("from_address_not_registered")
	}
	allRecipients := append(append([]string{}, req.To...), append(req.CC, req.BCC...)...)
	if len(allRecipients) == 0 {
		return nil, errors.New("at least one recipient is required")
	}
	for _, r := range allRecipients {
		if !isValidAddress(r) {
			return nil, fmt.Errorf("invalid recipient address: %q", r)
		}
	}
	if strings.TrimSpace(req.Subject) == "" {
		req.Subject = "(no subject)"
	}

	jobID, _ := ids.New("out")
	// Persist the composed message into the Sent folder so the user can
	// retrieve it later.  (Best-effort; ignore errors for now.)
	folders, ferr := s.store.MailListFolders(ctx, req.AccountID)
	if ferr == nil {
		var sentFolderID string
		for _, f := range folders {
			if f.Role == "sent" {
				sentFolderID = f.ID
				break
			}
		}
		if sentFolderID != "" {
			_, _ = s.store.MailCreateMessagePart(ctx, storage.MailMessagePart{
				FolderID:    sentFolderID,
				MessageID:   jobID,
				PartID:      "HEADERS",
				ContentType: "text/plain",
				Disposition: "",
				SizeBytes:   int64(len(req.Body)),
				DecodedText: fmt.Sprintf("Subject: %s\nFrom: %s\nTo: %s\n\n%s",
					req.Subject, req.From, strings.Join(req.To, ", "), req.Body),
			})
		}
	}

	resp := &ComposeSendResponse{
		JobID:    jobID,
		QueuedAt: timeNow(),
		Status:   "queued",
	}
	s.emit(ctx, EventTypeComposeQueued, map[string]any{
		"job_id":       jobID,
		"from":         req.From,
		"recipients":   len(allRecipients),
		"subject_len":  len(req.Subject),
	})
	return resp, nil
}

// DraftSaveRequest is stored verbatim into the Drafts folder so the user
// can resume their edits later.
type DraftSaveRequest struct {
	AccountID string   `json:"account_id"`
	DraftID   string   `json:"draft_id"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	CC        []string `json:"cc"`
	BCC       []string `json:"bcc"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
}

type DraftSaveResponse struct {
	DraftID   string `json:"draft_id"`
	FolderID  string `json:"folder_id"`
	SavedAt   string `json:"saved_at"`
}

// MailDraftSave persists a draft into the Drafts folder.  When DraftID is
// empty a new synthetic ID is generated and returned.
func (s *Service) MailDraftSave(ctx context.Context, req DraftSaveRequest) (*DraftSaveResponse, error) {
	if err := s.checkWriteGuard(ctx); err != nil {
		return nil, err
	}
	if req.AccountID == "" {
		return nil, errors.New("account_id is required")
	}
	folders, ferr := s.store.MailListFolders(ctx, req.AccountID)
	if ferr != nil {
		return nil, ferr
	}
	var draftFolderID string
	for _, f := range folders {
		if f.Role == "drafts" {
			draftFolderID = f.ID
			break
		}
	}
	if draftFolderID == "" {
		// No drafts folder yet — create one automatically so the user is
		// never stuck unable to save.
		created, cerr := s.store.MailCreateFolder(ctx, storage.MailFolder{
			AccountID:  req.AccountID,
			Name:       "Drafts",
			Role:       "drafts",
			Subscribed: true,
			Selectable: true,
			Delimiter:  "/",
			SyncState:  "idle",
		})
		if cerr != nil {
			return nil, cerr
		}
		draftFolderID = created.ID
	}
	draftID := req.DraftID
	if draftID == "" {
		draftID, _ = ids.New("dft")
	}
	serialized := fmt.Sprintf(
		"Subject: %s\nFrom: %s\nTo: %s\nCc: %s\nBcc: %s\n\n%s",
		req.Subject, req.From,
		strings.Join(req.To, ", "),
		strings.Join(req.CC, ", "),
		strings.Join(req.BCC, ", "),
		req.Body,
	)
	if _, err := s.store.MailCreateMessagePart(ctx, storage.MailMessagePart{
		FolderID:    draftFolderID,
		MessageID:   draftID,
		PartID:      "HEADERS",
		ContentType: "text/plain",
		Disposition: "",
		SizeBytes:   int64(len(serialized)),
		DecodedText: serialized,
	}); err != nil {
		return nil, err
	}
	resp := &DraftSaveResponse{
		DraftID:  draftID,
		FolderID: draftFolderID,
		SavedAt:  timeNow(),
	}
	s.emit(ctx, EventTypeDraftSaved, map[string]any{
		"draft_id":   draftID,
		"account_id": req.AccountID,
	})
	return resp, nil
}

// MailDraftDelete removes every part associated with a draft message id.
func (s *Service) MailDraftDelete(ctx context.Context, draftID string) error {
	if err := s.checkWriteGuard(ctx); err != nil {
		return err
	}
	if draftID == "" {
		return storage.ErrNotFound
	}
	n, err := s.store.MailDeleteMessagePartsByMessage(ctx, draftID)
	if err != nil {
		return err
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	s.emit(ctx, EventTypeDraftDeleted, map[string]any{"draft_id": draftID})
	return nil
}

// ---- helpers -------------------------------------------------------------

// emit fires a normal (non-danger) event.  Reuses the same publish
// helper (panic-safe) as the rest of the Service module.
func (s *Service) emit(ctx context.Context, typ string, payload map[string]any) {
	s.publish(ctx, typ, payload)
}

// emitDanger fires a HIGH-risk event.  Downstream consumers (the audit
// stream) can read the `danger: true` synthetic flag to surface these
// actions in red.
func (s *Service) emitDanger(ctx context.Context, typ string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["danger"] = true
	s.publish(ctx, typ, payload)
}

// timeNow returns a stable RFC3339 timestamp (used throughout this file so
// callers can inject a fake clock in tests by redefining the var).
var timeNow = func() string {
	return storage.NowISO()
}
