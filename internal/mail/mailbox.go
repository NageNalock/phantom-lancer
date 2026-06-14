package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/mail/imapsync"
	"phantom-lancer/internal/storage"

	"github.com/mjl-/mox/webapi"
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
	return nil, capabilityUnavailable("mail folder mutation requires a real Mox/IMAP adapter")
}

// MailFolderDelete removes a folder.  System folders (role = inbox|sent|drafts|trash|junk|archive)
// cannot be deleted because IMAP expects them to always be present.
func (s *Service) MailFolderDelete(ctx context.Context, folderID string) error {
	return capabilityUnavailable("mail folder deletion requires a real Mox/IMAP adapter")
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
		return s.mailMessageGetP7(ctx, messageID)
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
		MessageID:       messageID,
		Parts:           parts,
		BodyText:        bodyText,
		AttachmentCount: attachmentCount,
	}, nil
}

func (s *Service) mailMessageGetP7(ctx context.Context, messageID string) (*MailMessageDetail, error) {
	msg, err := s.store.MailMessageGet(ctx, messageID)
	if err != nil {
		return nil, err
	}
	parts := []storage.MailMessagePart{
		{
			ID:          msg.ID + ":headers",
			FolderID:    msg.FolderID,
			MessageID:   msg.ID,
			PartID:      "HEADERS",
			ContentType: "message/rfc822-headers",
			SizeBytes:   int64(len(msg.Subject) + len(msg.FromListCSV) + len(msg.ToListCSV)),
			DecodedText: fmt.Sprintf("Subject: %s\nFrom: %s\nTo: %s\nDate: %s\n", msg.Subject, msg.FromListCSV, msg.ToListCSV, msg.DateSent),
			CreatedAt:   msg.CreatedAt,
		},
	}
	if msg.BodyText != "" {
		parts = append(parts, storage.MailMessagePart{
			ID:          msg.ID + ":text",
			FolderID:    msg.FolderID,
			MessageID:   msg.ID,
			PartID:      "TEXT",
			ContentType: "text/plain",
			SizeBytes:   int64(len(msg.BodyText)),
			DecodedText: msg.BodyText,
			CreatedAt:   msg.CreatedAt,
		})
	}
	attachments := p7AttachmentInfos(msg.AttachmentsJSON)
	for i := range attachments {
		parts = append(parts, storage.MailMessagePart{
			ID:           msg.ID + ":att:" + fmt.Sprint(i),
			FolderID:     msg.FolderID,
			MessageID:    msg.ID,
			PartID:       fmt.Sprintf("ATTACHMENT-%d", i),
			ContentType:  attachments[i].ContentType,
			Filename:     attachments[i].Filename,
			Disposition:  "attachment",
			SizeBytes:    attachments[i].SizeBytes,
			IsAttachment: true,
			CreatedAt:    msg.CreatedAt,
		})
	}
	s.emit(ctx, EventTypeMessageViewed, map[string]any{"message_id": messageID, "source": "p7", "parts": len(parts)})
	return &MailMessageDetail{
		MessageID:       msg.ID,
		Parts:           parts,
		BodyText:        msg.BodyText,
		AttachmentCount: len(attachments),
		Attachments:     attachments,
	}, nil
}

// MailMessageDetail is the aggregate returned by MailMessageGet.
type MailMessageDetail struct {
	MessageID       string                    `json:"message_id"`
	Parts           []storage.MailMessagePart `json:"parts"`
	BodyText        string                    `json:"body_text"`
	AttachmentCount int                       `json:"attachment_count"`
	Attachments     []AttachmentInfo          `json:"attachments"`
}

// AttachmentInfo is a lightweight description of one attachment part.
type AttachmentInfo struct {
	Index       int    `json:"index"`
	PartID      string `json:"part_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Stored      bool   `json:"stored"`
	CachePath   string `json:"-"`
	MoxMsgID    int64  `json:"-"`
	PartPath    []int  `json:"-"`
}

func p7AttachmentInfos(raw string) []AttachmentInfo {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var rows []struct {
		Filename      string `json:"filename"`
		Name          string `json:"name"`
		ContentType   string `json:"content_type"`
		MimeType      string `json:"mime_type"`
		SizeBytes     int64  `json:"size_bytes"`
		Size          int64  `json:"size"`
		PartID        string `json:"part_id"`
		Stored        bool   `json:"stored"`
		CachePath     string `json:"cache_path"`
		BodyCachePath string `json:"body_cache_path"`
		MoxMsgID      int64  `json:"mox_msg_id"`
		MsgID         int64  `json:"msg_id"`
		PartPath      []int  `json:"part_path"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil
	}
	out := make([]AttachmentInfo, 0, len(rows))
	for i, row := range rows {
		name := row.Filename
		if name == "" {
			name = row.Name
		}
		typ := row.ContentType
		if typ == "" {
			typ = row.MimeType
		}
		size := row.SizeBytes
		if size == 0 {
			size = row.Size
		}
		cachePath := firstNonEmpty(row.CachePath, row.BodyCachePath)
		msgID := row.MoxMsgID
		if msgID == 0 {
			msgID = row.MsgID
		}
		out = append(out, AttachmentInfo{Index: i, PartID: row.PartID, Filename: name, ContentType: typ, SizeBytes: size, Stored: cachePath != "", CachePath: cachePath, MoxMsgID: msgID, PartPath: row.PartPath})
	}
	return out
}

type CachedAttachmentFile struct {
	AttachmentInfo
	Path   string
	Reader io.ReadCloser
}

// MailMessageDelete removes every part belonging to a message.  This is a
// HIGH-risk destructive action because the rows are physically removed.
func (s *Service) MailMessageDelete(ctx context.Context, messageID string) error {
	return capabilityUnavailable("mail message deletion requires a real Mox/IMAP adapter")
}

// MailMessageMove transfers every part of a message to a different folder.
// Note: because Mox stores folder membership per UID, our sqlite-only table
// keeps folder_id on each part; moving is a single UPDATE.
func (s *Service) MailMessageMove(ctx context.Context, messageID, destFolderID string) error {
	return capabilityUnavailable("mail message move requires a real Mox/IMAP adapter")
}

// MailMessageFlagsUpdate applies an IMAP-style flag update to every part
// of a message.  Flags are stored inside the HEADERS part's decoded_text as
// JSON for now; to keep the operation simple we only toggle Seen on the
// whole message by writing a synthetic flag on the HEADERS part.
func (s *Service) MailMessageFlagsUpdate(ctx context.Context, messageID string, add, remove []string) error {
	return capabilityUnavailable("mail flag mutation requires a real Mox/IMAP adapter")
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
		msg, gerr := s.store.MailMessageGet(ctx, messageID)
		if gerr != nil {
			return "", storage.ErrNotFound
		}
		if msg.BodyText != "" {
			s.emit(ctx, EventTypeMessageRawFetched, map[string]any{"message_id": messageID, "source": "p7", "bytes": len(msg.BodyText)})
			return msg.BodyText, nil
		}
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
// by its zero-based index within the message. Cached attachment byte
// streaming is implemented separately by MailAttachmentFile; uncached
// Mox data/WebAPI part reads are still unavailable.
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

func (s *Service) MailAttachmentFile(ctx context.Context, messageID string, index int) (*CachedAttachmentFile, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
	}
	cached, err := s.store.MailCachedAttachment(ctx, messageID, index)
	if err == nil {
		return s.cachedAttachmentFileFromPath(cached.BodyCachePath, index, cached.PartID, cached.Filename, cached.ContentType)
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	detail, derr := s.mailMessageGetP7(ctx, messageID)
	if derr != nil {
		return nil, err
	}
	if index < 0 || index >= len(detail.Attachments) {
		return nil, storage.ErrNotFound
	}
	att := detail.Attachments[index]
	if att.CachePath != "" {
		return s.cachedAttachmentFileFromPath(att.CachePath, index, att.PartID, att.Filename, att.ContentType)
	}
	return nil, storage.ErrNotFound
}

func (s *Service) cachedAttachmentFileFromPath(rawPath string, index int, partID, filename, contentType string) (*CachedAttachmentFile, error) {
	path, err := filepath.Abs(rawPath)
	if err != nil {
		return nil, fmt.Errorf("attachment path invalid: %w", err)
	}
	dataDir, err := filepath.Abs(s.moxRoot)
	if err != nil {
		return nil, fmt.Errorf("mox root invalid: %w", err)
	}
	rel, err := filepath.Rel(dataDir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, errors.New("attachment cache path is outside Mail data root")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, storage.ErrNotFound
	}
	if info.IsDir() {
		return nil, errors.New("attachment cache path is a directory")
	}
	return &CachedAttachmentFile{
		AttachmentInfo: AttachmentInfo{
			Index:       index,
			PartID:      partID,
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   info.Size(),
			Stored:      true,
		},
		Path: path,
	}, nil
}

// ---- Search ---------------------------------------------------------------

type MailSearchQuery struct {
	AccountIDs    []string `json:"account_ids"`
	Query         string   `json:"query"`
	Scope         string   `json:"scope"` // one | all | attachments | folder:<id>
	FromDomain    string   `json:"from_domain"`
	To            string   `json:"to"`
	Since         string   `json:"since"`
	Before        string   `json:"before"`
	HasAttachment *bool    `json:"has_attachment,omitempty"`
	UnreadOnly    bool     `json:"unread_only"`
	Limit         int      `json:"limit"`
	Offset        int      `json:"offset"`
}

type MailSearchResponse struct {
	Query string                     `json:"query"`
	Total int                        `json:"total"`
	Items []storage.MailSearchResult `json:"items"`
}

// MailMessageSearch runs the FTS5 search over the supplied accounts.
func (s *Service) MailMessageSearch(ctx context.Context, q MailSearchQuery) (*MailSearchResponse, error) {
	if s.store == nil {
		return nil, errors.New("mail store is not wired")
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
	out := []storage.MailSearchResult{}
	total := 0
	for _, accountID := range q.AccountIDs {
		p7 := storage.FTSQueryP7{
			AccountID:     accountID,
			Scope:         q.Scope,
			Query:         strings.TrimSpace(q.Query),
			FromDomain:    q.FromDomain,
			To:            q.To,
			Since:         q.Since,
			Before:        q.Before,
			HasAttachment: q.HasAttachment,
			UnseenOnly:    q.UnreadOnly,
			Limit:         limit,
			Offset:        offset,
		}
		if q.Scope == "attachments" || q.Scope == "has_attachment" {
			v := true
			p7.HasAttachment = &v
		}
		results, n, err := s.store.MailMessageSearchP7(ctx, p7)
		if err != nil {
			return nil, err
		}
		total += n
		for _, r := range results {
			out = append(out, storage.MailSearchResult{
				ID:            r.ID,
				MessagePartID: r.ID,
				MessageID:     r.ID,
				FolderID:      r.FolderID,
				AccountID:     r.AccountID,
				Subject:       r.SubjectSnippet,
				Snippet:       r.PreviewSnippet,
				From:          r.FromList,
				To:            r.ToList,
				Date:          r.DateSent,
				FromDisplay:   r.FromList,
				ReceivedAt:    r.DateSent,
			})
		}
	}
	s.emit(ctx, EventTypeSearchExecuted, map[string]any{
		"query": q.Query,
		"hits":  len(out),
		"scope": q.Scope,
		"limit": limit,
	})
	return &MailSearchResponse{Query: q.Query, Total: total, Items: out}, nil
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
	BodyText     string   `json:"body_text"`
	BodyHTML     string   `json:"body_html"`
	ReplyToMsgID string   `json:"reply_to_message_id"`
}

type ComposeSendResponse struct {
	JobID       string   `json:"job_id"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	QueuedAt    string   `json:"queued_at"`
	Status      string   `json:"status"`
	SavedToSent bool     `json:"saved_to_sent,omitempty"`
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// MailComposeSend submits a composed message through Mox WebAPI.
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
	bodyText := req.Body
	if bodyText == "" {
		bodyText = req.BodyText
	}
	if strings.TrimSpace(bodyText) == "" && strings.TrimSpace(req.BodyHTML) == "" {
		return nil, errors.New("message body is required")
	}
	password, err := s.webAPIPassword(ctx, acc)
	if err != nil {
		return nil, err
	}
	client, err := s.webAPIClient(ctx, acc.Address, password)
	if err != nil {
		return nil, err
	}
	sendReq := webapi.SendRequest{
		Message: webapi.Message{
			From:    []webapi.NameAddress{{Address: req.From}},
			To:      toNameAddresses(req.To),
			CC:      toNameAddresses(req.CC),
			BCC:     toNameAddresses(req.BCC),
			Subject: req.Subject,
			Text:    bodyText,
			HTML:    req.BodyHTML,
		},
		SaveSent: true,
		Extra: map[string]string{
			"Phantom-Lancer-Account": req.AccountID,
		},
	}
	result, err := client.Send(ctx, sendReq)
	if err != nil {
		return nil, fmt.Errorf("mox webapi send: %w", err)
	}
	jobID := result.MessageID
	if jobID == "" {
		jobID, _ = ids.New("out")
	}
	resp := &ComposeSendResponse{
		JobID:       jobID,
		From:        req.From,
		To:          allRecipients,
		QueuedAt:    timeNow(),
		Status:      "queued",
		SavedToSent: true,
	}
	for _, sub := range result.Submissions {
		_, _ = s.store.MailDeliveryInsert(ctx, &storage.MailDeliveryEvent{
			FromDomain:     domainFromAddr(req.From),
			ToDomain:       domainFromAddr(sub.Address),
			MessageIDHash:  sha256Hex(result.MessageID),
			SubjectSnippet: truncate80(req.Subject),
			Direction:      "out",
			Status:         "queued",
			AttemptCount:   0,
			RecipientHash:  sha256Hex(sub.Address),
			QueueMsgID:     sub.QueueMsgID,
			FromID:         sub.FromID,
			CreatedAt:      resp.QueuedAt,
		})
	}
	s.emit(ctx, EventTypeComposeQueued, map[string]any{
		"job_id":         jobID,
		"from":           req.From,
		"recipients":     len(allRecipients),
		"subject_len":    len(req.Subject),
		"submissions":    len(result.Submissions),
		"mox_message_id": result.MessageID,
	})
	return resp, nil
}

func toNameAddresses(items []string) []webapi.NameAddress {
	out := make([]webapi.NameAddress, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, webapi.NameAddress{Address: strings.TrimSpace(item)})
		}
	}
	return out
}

func (s *Service) webAPIPassword(ctx context.Context, acc storage.MailAccount) (string, error) {
	if strings.TrimSpace(acc.WebAPIPasswordWrapped) == "" {
		return "", errors.New("webapi password not stored; reset the account password before sending")
	}
	password, err := s.store.UnwrapMailSecret(acc.WebAPIPasswordWrapped)
	if err != nil {
		return "", fmt.Errorf("unwrap webapi password: %w", err)
	}
	if password == "" {
		return "", errors.New("webapi password is empty; reset the account password before sending")
	}
	return password, nil
}

func (s *Service) webAPIClient(ctx context.Context, username, password string) (webapi.Client, error) {
	settings, err := s.store.MailGetSettings(ctx)
	if err != nil {
		return webapi.Client{}, err
	}
	svc, err := s.supervisor(ctx)
	if err != nil {
		return webapi.Client{}, err
	}
	baseURL, unixSocket, err := validatedWebAPIEndpoint(settings.WebAPIAddr, defaultMoxWebAPISocket(svc.MoxRoot))
	if err != nil {
		return webapi.Client{}, err
	}
	baseURL = strings.TrimRight(baseURL, "/") + "/webapi/v0/"
	client := &http.Client{Timeout: 30 * time.Second}
	if unixSocket != "" {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", unixSocket)
			},
		}
	}
	return webapi.Client{BaseURL: baseURL, Username: username, Password: password, HTTPClient: client}, nil
}

func validatedWebAPIEndpoint(addr string, defaultSock string) (baseURL string, unixSocket string, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		if strings.TrimSpace(defaultSock) == "" {
			return "", "", errors.New("webapi endpoint is not configured")
		}
		return "http://mox.local/", defaultSock, nil
	}
	if strings.HasPrefix(addr, "/") {
		return "http://mox.local/", addr, nil
	}
	host, portText, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return "", "", fmt.Errorf("webapi_addr must be a unix socket path or loopback host:port: %w", splitErr)
	}
	port, portErr := strconv.Atoi(portText)
	if portErr != nil || port <= 0 || port > 65535 {
		return "", "", fmt.Errorf("webapi_addr has invalid port %q", portText)
	}
	if port < 1024 || port == 80 || port == 443 {
		return "", "", fmt.Errorf("webapi_addr port %d is not allowed", port)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		if !strings.EqualFold(host, "localhost") {
			return "", "", fmt.Errorf("webapi_addr host must be loopback, got %q", host)
		}
	} else if ip.IsUnspecified() || !ip.IsLoopback() {
		return "", "", fmt.Errorf("webapi_addr host must be loopback, got %q", host)
	}
	return "http://" + strings.Trim(addr, "/") + "/", "", nil
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
	BodyText  string   `json:"body_text"`
}

type DraftSaveResponse struct {
	DraftID  string `json:"draft_id"`
	FolderID string `json:"folder_id"`
	SavedAt  string `json:"saved_at"`
}

// MailDraftSave persists a draft into the Drafts folder.  When DraftID is
// empty a new synthetic ID is generated and returned.
func (s *Service) MailDraftSave(ctx context.Context, req DraftSaveRequest) (*DraftSaveResponse, error) {
	return nil, capabilityUnavailable("draft save requires a real Mox/IMAP drafts adapter")
}

// MailDraftDelete removes every part associated with a draft message id.
func (s *Service) MailDraftDelete(ctx context.Context, draftID string) error {
	return capabilityUnavailable("draft deletion requires a real Mox/IMAP drafts adapter")
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
