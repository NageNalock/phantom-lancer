package moxcli

// ConfigTestResult is the parsed output of `mox config test`.
type ConfigTestResult struct {
	OK       bool
	Output   string
	Errors   []string
	Warnings []string
}

// ParsedConfig is a shallow parsed output of `mox config list`.
type ParsedConfig map[string]any

// DomainEntry describes one domain registered with `mox domain add/set`.
type DomainEntry struct {
	Domain          string
	DKIMSelector    string
	DMARCPolicy     string // "none" / "quarantine" / "reject"
	DMARCRUA        string
	SPFInclude      string
	WebmailLoginURL string
}

// AccountEntry is the payload used to create a local mailbox.
// The password is passed through an os.Pipe — never via argv.
type AccountEntry struct {
	Email       string
	DisplayName string
	Role        string // "user" / "admin" / ""
}

// AliasEntry describes an alias, catch-all, or mailing-list alias.
type AliasEntry struct {
	AliasAddr  string
	Mode       string // "forward" / "list" / "catch-all"
	Recipients []string
}

// QueueSummary is the best-effort aggregated queue counts.
type QueueSummary struct {
	Hold       int64
	Scheduled  int64
	Failed     int64
	Dropped    int64
	Suppressed int64
}
