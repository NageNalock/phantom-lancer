package mail

// registerMailRedactions appends module-specific redaction patterns to the
// safelog tail pass.  These regexes catch secrets that only appear in mail
// components (MTA logs, SMTP transcript dumps, IMAP AUTH negotiations, DKIM
// configuration, DMARC/SPF TXT records, and mail account passwords).

import (
	"context"
	"log/slog"

	"phantom-lancer/internal/safelog"
)

// mailRedactionPatterns lists (pattern, replacement, description).  Registering is best-effort:
// individual pattern failures are logged as warnings and skipped, so a mis-built
// regex never breaks Service boot.
var mailRedactionPatterns = [][3]string{
	// 1. SASL PLAIN / AUTH PLAIN base64 blobs.  RFC 4616 format: the base64
	//    is `<NUL>user<NUL>pass` and would be replayed verbatim in SMTP/IMAP
	//    transcripts.  The regex swallows 8+ base64 chars that look like real SASL
	//    exchanges.
	{
		`(?i)(AUTH\s+(?:PLAIN|LOGIN)\s+)[A-Za-z0-9+/=]{8,}`,
		"${1}[redacted]",
		"SASL PLAIN/LOGIN base64 credentials",
	},
	// 2. Password-bearing auth_password / auth_pass / pwd / auth_pwd / passwd
	//    key-value pairs commonly logged by MTA config dumps and account
	//    exports.  Matches both `auth_*` prefixed forms AND standalone
	//    `pwd`/`passwd` forms.  Supports bare `=`, bare `:`, and JSON-style
	//    `":"` / `'='` separators (quotes around the separator are absorbed
	//    into the capture group, not the value).  The unquoted-value class
	//    excludes brackets for idempotence on a second redact pass.
	{
		`(?i)\b((?:auth(?:_password|_pass|_pwd|pwd|passwd))|(?:pwd|passwd))\b(\s*["']?\s*[=:]\s*["']?)("[^"]+"|'[^']+'|[^\s,;&}\)\[\]'"]+)`,
		"${1}${2}[redacted]",
		"auth/password key-value pairs",
	},
	// 3. DKIM "p=" tag – the public-key base64 in DKIM TXT records.
	//    Technically public, but redacted to avoid noise in UI logs.
	//    Capture stops at the first non-key character (newlines excluded).
	{
		`(?i)(dkim[^\n]*?p=)[A-Za-z0-9+/=]+`,
		"${1}[redacted-dkim-key]",
		"DKIM p= public key material",
	},
	// 4. AUTH succeeded banner strings (may leak identity token fragments).
	{
		`(?i)(235\s+2\.7\.0\s+AUTH\s+authentication\s+succeeded)[^\n]*`,
		"${1} [redacted-auth-identity]",
		"AUTH succeeded banner strings",
	},
	// 5. SASL method / username identity fields logged in Received: headers.
	//    Excludes parens and brackets from the value class so that
	//    already-redacted values are stable on a second redact pass and
	//    so Received: parentheses are preserved.
	{
		`(?i)(sasl_method=(?:PLAIN|LOGIN|CRAM-MD5|DIGEST-MD5|SCRAM-SHA(?:-1|-256|-512)?)[^\n]*?sasl_username=)[^\s,;)\[\]]+`,
		"${1}[redacted]",
		"SASL sasl_username= identity",
	},
	// 6. --username / --password style CLI flags in process logs.  Handles
	//    quoted values (both " and ') and unquoted forms; unquoted class
	//    excludes brackets so idempotent redactions don't accumulate.
	{
		`(?i)(--(?:user(?:name)?|password|passwd|secret|token|api[_-]?key)\s*[= ]\s*)(?:"[^"]*"|'[^']*'|[^\s'";&|)\[\]]+)`,
		"${1}[redacted]",
		"CLI credential flags",
	},
	// 7. PEM blocks (certificate or private key) in mail config dumps.
	{
		`(?i)-----BEGIN (?:CERTIFICATE|(?:[A-Z0-9 ]* )?PRIVATE KEY)-----[\s\S]*?-----END (?:CERTIFICATE|(?:[A-Z0-9 ]* )?PRIVATE KEY)-----`,
		"[redacted-cert-or-key-block]",
		"Certificate/private-key PEM blocks in mail config",
	},
}

// mailRedactionDescriptions exposes the human-readable list of every rule
// description so the redaction-summary HTTP endpoint can return a unified
// list covering both builtin + mail module rules.
func mailRedactionDescriptions() []string {
	out := make([]string, 0, len(mailRedactionPatterns))
	for _, p := range mailRedactionPatterns {
		out = append(out, p[2])
	}
	return out
}

// registerMailRedactions should be called ONCE during Service.Ensure(),
// after settings upsert, before any worker startup.  All registration
// errors are aggregated into a single warning log.
func registerMailRedactions(ctx context.Context, log *slog.Logger) {
	registered := 0
	var warns []string
	for _, p := range mailRedactionPatterns {
		if err := safelog.RegisterRegex(p[0], p[1]); err != nil {
			warns = append(warns, p[2]+": "+err.Error())
			continue
		}
		registered++
	}
	if log != nil {
		if len(warns) > 0 {
			log.WarnContext(ctx, "mail.redactions: some mail patterns skipped",
				"registered_count", registered,
				"skipped_count", len(warns),
				"skipped", warns,
			)
		} else {
			log.DebugContext(ctx, "mail.redactions: registered mail redaction patterns",
				"count", registered,
			)
		}
	}
}
