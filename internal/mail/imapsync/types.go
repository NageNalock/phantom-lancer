// Package imapsync defines the abstract interface stubs for the Phase 7 IMAP
// background sync subsystem.  This package deliberately imports nothing from
// third-party IMAP client libraries – concrete implementations (go-imap, etc.)
// live in a separate adapter package so the storage layer, Service layer,
// and HTTP handlers can compile without pulling in heavy network deps.
//
// Lifecycle: the Manager (see manager.go) owns a per-account goroutine.
// Each goroutine builds a ClientIFace via Dial(), drives fetch/index/
// checkpoint in a loop, and publishes Sync* events via the events.Hub.
package imapsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ---- State / lifecycle ---------------------------------------------------

// State enumerates the possible states of a single account's sync loop.
// Values are stored in mail_accounts.imap_sync_state (TEXT column),
// mail_folders.sync_state, and surfaced through Manager.State().
type State string

const (
	StateIdle    State = "idle"    // waiting for next scheduled run
	StateQueued  State = "queued"  // user requested; waiting for worker pick-up
	StateSyncing State = "syncing" // actively fetching/indexing
	StatePaused  State = "paused"  // operator paused; loop will not resume
	StateError   State = "error"   // last run failed; in backoff
	StateStopped State = "stopped" // Stop() called; no goroutine running
)

// String returns the textual form; safe to store in TEXT columns.
func (s State) String() string { return string(s) }

// ---- Per-account configuration ------------------------------------------

// AccountConfig carries everything the sync loop needs to reach the
// upstream IMAP server for a single Phantom account.
//
// IMPORTANT C-6 SECURITY: Password is NEVER stored in the struct.  The
// PasswordFn closure is invoked once per Dial() inside the goroutine so
// the plaintext credential lives only on the stack for as long as the
// TLS handshake needs it.  Crashes / core dumps / heap snapshots will
// never reveal the credential.
type AccountConfig struct {
	AccountID     string
	Address       string // user@domain – used for logs/events
	ImapHost      string // host:port or bare host (Dial fills a default)
	Username      string // IMAP login; often == Address
	PasswordFn    func(ctx context.Context) (string, error)

	// Per-account safety limits.
	MaxMsgSize    int64 // bytes; skip larger messages (log + skip)
	MaxTotalBytes int64 // bytes; pause sync once per-account storage exceeds this

	// IdleTimeoutSec is how long to block on IDLE before falling back to
	// the regular poll interval.  <= 0 disables IDLE entirely.
	IdleTimeoutSec int
}

// ---- Remote IMAP data-transfer types ------------------------------------

// Folder describes a single upstream folder (IMAP LIST / LSUB output
// merged with post-SELECT metadata).
type Folder struct {
	Name        string   // full folder path, e.g. "INBOX" or "INBOX/Sent"
	Path        string   // canonical server path (may differ from Name on some servers)
	Delim       string   // separator; "/" or "."
	UIDValidity uint32   // UIDVALIDITY from SELECT
	UIDNext     uint32   // UIDNEXT from SELECT
	Attrs       []string // attributes e.g. \Noselect, \Drafts, \Sent, \Trash, \Junk, \Flagged, \All
	Total       uint32   // EXISTS count from SELECT
	Unseen      uint32   // RECENT/UNSEEN count from STATUS/SELECT
}

// AttachmentInfo describes one attachment from the BODYSTRUCTURE response
// so the sync loop can decide whether to cache the part bytes (per the
// MaxMsgSize / attachment-cache policy).
type AttachmentInfo struct {
	Name     string // decoded filename parameter
	Size     int64  // decoded part size in octets
	MimeType string // e.g. "application/pdf"
}

// Envelope is the minimal set of fields the sync loop needs to build a
// MailMessage row.  The fields map 1:1 to IMAP's ENVELOPE + FLAGS + RFC822.SIZE.
type Envelope struct {
	UID          string // IMAP UID as a string (fits uint32, kept as string for JSON ease)
	MessageID    string // RFC 5322 Message-Id header (synthetic if absent)
	Subject      string // decoded Subject
	FromDisplay  string // display form of From, e.g. "Alice <a@x.com>"
	FromAddress  string // bare address of From
	ToDisplay    string // display form of To (comma-separated if multiple)
	ToAddresses  []string
	CcAddresses  []string
	BccAddresses []string
	ReplyTo      []string
	InReplyTo    string // Message-Id this message replies to
	References   string // thread-link references header
	DateSent     string // RFC 5322 Date header as ISO-8601; "" if absent
	InternalDate string // IMAP INTERNALDATE as ISO-8601
	SizeBytes    int64  // RFC822.SIZE
	Seen         bool
	Flagged      bool
	Answered     bool
	Deleted      bool
	Draft        bool
	ExtraFlags   []string // any non-standard flags not captured above
	HasAttachment bool
	Attachments  []AttachmentInfo
}

// FetchedMessage pairs an Envelope with the plaintext body preview + the
// full decoded text.  BodyText is trimmed to the per-account preview limit
// if needed; BodyText is always UTF-8 (adapter is responsible for charset
// conversion via Charset below).
type FetchedMessage struct {
	Envelope
	Preview  string // first ~140 runes of plain text (UI snippet)
	BodyText string // full decoded plaintext body
	Charset  string // original charset before conversion, informational only
}

// ---- Transport interface ------------------------------------------------

// ClientIFace is the minimum subset of IMAP commands that Phase 7 uses.
// Implementations are free to wrap a real IMAP library internally; this
// narrow interface keeps the sync loop testable and backend-agnostic.
//
// All methods accept a context; implementations MUST honour cancellation
// when the account's Stop() has been called.
type ClientIFace interface {
	// List returns the (subscribed) folder list.  An empty pattern is
	// mapped to "*" by the implementation.
	List(ctx context.Context, pattern string) ([]Folder, error)
	// Select opens folderName and returns its current UIDValidity /
	// UIDNext / counts.
	Select(ctx context.Context, folderName string) (*Folder, error)
	// UIDFetchSince returns all messages whose INTERNALDATE is >= since
	// (as an ISO-8601 string).  If since is empty the adapter should
	// return the full UID range 1:*.
	UIDFetchSince(ctx context.Context, folderName string, since string) ([]Envelope, error)
	// UIDMove atomically moves uid to destinationFolder.  If the upstream
	// does not support MOVE, implementations fall back to COPY+STORE \Deleted+EXPUNGE.
	UIDMove(ctx context.Context, folderName string, uid uint32, destinationFolder string) error
	// UIDStoreFlags replaces (add=true) or removes (add=false) flags for
	// the given UID.
	UIDStoreFlags(ctx context.Context, folderName string, uid uint32, add bool, flags []string) error
	// Append uploads a new RFC822-formatted message to the given folder
	// with the supplied flags and optional internal date.
	Append(ctx context.Context, folderName string, flags []string, internalDate string, rfc822 []byte) error
	// IDLEStart instructs the server to enter IDLE mode.  The adapter
	// unblocks and returns a cancel function when new mail arrives, when
	// ctx is cancelled, or when IdleTimeoutSec expires.  Implementations
	// that do not support IDLE should return (noop func, nil) immediately;
	// the caller will then fall back to polling.
	IDLEStart(ctx context.Context, folderName string) (stop func(), err error)
	// Close logs out and closes the transport.  Safe to call multiple times.
	Close(ctx context.Context) error
}

// ---- Stub transport ------------------------------------------------------

// StubClient is a no-op ClientIFace.  Every method logs a "stub called"
// line and returns a benign success (or an empty slice).  It exists so:
//   - the sync loop compiles,
//   - unit tests that exercise the Manager goroutine machinery don't need
//     a real IMAP server,
//   - the Service layer can be constructed during local dev even before a
//     concrete adapter is installed.
type StubClient struct {
	Log *slog.Logger
}

// Ensure StubClient conforms to ClientIFace at compile time.
var _ ClientIFace = (*StubClient)(nil)

func (s *StubClient) log(ctx context.Context, name string, args ...any) {
	if s == nil {
		return
	}
	if s.Log != nil {
		s.Log.DebugContext(ctx, "imapsync: StubClient."+name, args...)
		return
	}
	slog.Debug("imapsync: StubClient."+name, args...)
}

func (s *StubClient) List(ctx context.Context, pattern string) ([]Folder, error) {
	s.log(ctx, "List", "pattern", pattern)
	return []Folder{}, nil
}

func (s *StubClient) Select(ctx context.Context, folderName string) (*Folder, error) {
	s.log(ctx, "Select", "folder", folderName)
	return &Folder{Name: folderName, Path: folderName, Delim: "/"}, nil
}

func (s *StubClient) UIDFetchSince(ctx context.Context, folderName string, since string) ([]Envelope, error) {
	s.log(ctx, "UIDFetchSince", "folder", folderName, "since", since)
	return []Envelope{}, nil
}

func (s *StubClient) UIDMove(ctx context.Context, folderName string, uid uint32, destinationFolder string) error {
	s.log(ctx, "UIDMove", "folder", folderName, "uid", uid, "dest", destinationFolder)
	return nil
}

func (s *StubClient) UIDStoreFlags(ctx context.Context, folderName string, uid uint32, add bool, flags []string) error {
	s.log(ctx, "UIDStoreFlags", "folder", folderName, "uid", uid, "add", add, "flags", flags)
	return nil
}

func (s *StubClient) Append(ctx context.Context, folderName string, flags []string, internalDate string, rfc822 []byte) error {
	s.log(ctx, "Append", "folder", folderName, "flags", flags, "bytes", len(rfc822))
	return nil
}

func (s *StubClient) IDLEStart(ctx context.Context, folderName string) (func(), error) {
	s.log(ctx, "IDLEStart", "folder", folderName)
	stop := func() {}
	return stop, nil
}

func (s *StubClient) Close(ctx context.Context) error {
	s.log(ctx, "Close")
	return nil
}

// ---- Dial factory --------------------------------------------------------

// ErrNoAdapter is returned by Dial when no registered adapter can handle
// the cfg.ImapHost (see adapter registration, which is intentionally NOT
// wired until a later phase brings in a real IMAP library).
var ErrNoAdapter = errors.New("imapsync: no dial adapter registered; using stub")

// DialMock is a package-level test hook.  When non-nil it completely
// replaces the production Dial() function so unit tests can return a
// pre-configured StubClient (or a ClientIFace that returns specific
// errors / data) without the loop touching any network code.
//
// Tests must restore DialMock = nil in a t.Cleanup to avoid cross-test
// pollution.
var DialMock func(ctx context.Context, cfg AccountConfig) (ClientIFace, error)

// Dial builds a ClientIFace for the supplied account.
//
// In Phase 7 this always returns (*StubClient, ErrNoAdapter) – concrete
// adapters register themselves later.  The sync loop treats ErrNoAdapter
// as a soft error: it logs once, swaps to the stub, and keeps the
// goroutine alive so Start/Stop/Pause/Resume all work end-to-end in tests.
func Dial(ctx context.Context, cfg AccountConfig) (ClientIFace, error) {
	if DialMock != nil {
		return DialMock(ctx, cfg)
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("imapsync: Dial: AccountID is required")
	}
	if cfg.ImapHost == "" {
		return nil, fmt.Errorf("imapsync: Dial: ImapHost is required")
	}
	log := slog.With("module", "mail.imapsync", "account_id", cfg.AccountID)
	log.DebugContext(ctx, "imapsync: Dial (stub) – no real adapter in Phase 7", "host", cfg.ImapHost)
	return &StubClient{Log: log}, ErrNoAdapter
}
