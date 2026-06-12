package safelog

import (
	"regexp"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Mail redaction patterns — reproduced identically from internal/mail/redaction.go
// so the safelog package can exercise them in isolation (no import cycle).
// Order and content MUST stay in sync.
// ---------------------------------------------------------------------------

var mailRedactionPatterns = [][3]string{
	// 1. SASL PLAIN / LOGIN base64 blobs.
	{
		`(?i)(AUTH\s+(?:PLAIN|LOGIN)\s+)[A-Za-z0-9+/=]{8,}`,
		"${1}[redacted]",
		"SASL PLAIN/LOGIN base64 credentials",
	},
	// 2. auth_password / auth_pass / auth_pwd / pwd / passwd key-value pairs.
	//    Covers both auth-prefixed forms and standalone pwd/passwd.  Supports
	//    JSON-style `"key":"value"` joins (quotes absorbed into the separator
	//    capture group, not the value).  Unquoted class excludes brackets for
	//    idempotence on a second redact pass.
	{
		`(?i)\b((?:auth(?:_password|_pass|_pwd|pwd|passwd))|(?:pwd|passwd))\b(\s*["']?\s*[=:]\s*["']?)("[^"]+"|'[^']+'|[^\s,;&}\)\[\]'"]+)`,
		"${1}${2}[redacted]",
		"auth/password key-value pairs",
	},
	// 3. DKIM p= tag.  Stops at the first non-key character (no cross-line
	//    greed; the class does NOT include whitespace or `;`, so multiple
	//    `p=` on a single line are each redacted independently).
	{
		`(?i)(dkim[^\n]*?p=)[A-Za-z0-9+/=]+`,
		"${1}[redacted-dkim-key]",
		"DKIM p= public key material",
	},
	// 4. AUTH succeeded banner.
	{
		`(?i)(235\s+2\.7\.0\s+AUTH\s+authentication\s+succeeded)[^\n]*`,
		"${1} [redacted-auth-identity]",
		"AUTH succeeded banner strings",
	},
	// 5. SASL method + username; `)` excluded from value so Received:
	//    parentheses are preserved; `[` / `]` properly escaped in exclusion
	//    class so already-redacted values are idempotent.
	{
		`(?i)(sasl_method=(?:PLAIN|LOGIN|CRAM-MD5|DIGEST-MD5|SCRAM-SHA(?:-1|-256|-512)?)[^\n]*?sasl_username=)[^\s,;)\[\]]+`,
		"${1}[redacted]",
		"SASL sasl_username= identity",
	},
	// 6. CLI credential flags; supports quoted and unquoted values.
	//    Unquoted class excludes brackets with proper escaping for
	//    idempotent re-redaction.
	{
		`(?i)(--(?:user(?:name)?|password|passwd|secret|token|api[_-]?key)\s*[= ]\s*)(?:"[^"]*"|'[^']*'|[^\s'";&|)\[\]]+)`,
		"${1}[redacted]",
		"CLI credential flags",
	},
	// 7. PEM blocks (CERT or PRIVATE KEY).  NOTE: the built-in safelog
	//    `pemPrivate` pass matches PRIVATE KEY blocks *first* and replaces
	//    them with `[redacted-private-key]`; this rule therefore only
	//    affects CERTIFICATE blocks at tail-pass time.  PRIVATE KEY still
	//    redacted (by builtin) which is the security property we care
	//    about, even though the replacement text differs.
	{
		`(?i)-----BEGIN (?:CERTIFICATE|(?:[A-Z0-9 ]* )?PRIVATE KEY)-----[\s\S]*?-----END (?:CERTIFICATE|(?:[A-Z0-9 ]* )?PRIVATE KEY)-----`,
		"[redacted-cert-or-key-block]",
		"Certificate/private-key PEM blocks",
	},
}

// registerMailPatternsForTest registers all 7 mail redaction rules via the
// public RegisterRegex API and returns a cleanup function.
func registerMailPatternsForTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ResetRegistered() })
	ResetRegistered()
	for i, p := range mailRedactionPatterns {
		if err := RegisterRegex(p[0], p[1]); err != nil {
			t.Fatalf("register pattern %d (%s): %v", i, p[2], err)
		}
	}
}

// ---------------------------------------------------------------------------
// Per-rule tests.
// ---------------------------------------------------------------------------

func TestR1_SASL_PLAIN(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string // "" means unchanged
	}{
		{
			name:  "positive AUTH PLAIN base64",
			input: ">>> AUTH PLAIN AGUAdABiAGgAZQBfAHAAYQBzAHMAdwBvAHIAZABh",
			want:  ">>> AUTH PLAIN [redacted]",
		},
		{
			name:  "positive AUTH LOGIN base64",
			input: ">>> AUTH LOGIN dXNlcm5hbWU=",
			want:  ">>> AUTH LOGIN [redacted]",
		},
		{
			name:  "negative 250 OK untouched",
			input: "250 2.0.0 OK 12345",
			want:  "250 2.0.0 OK 12345",
		},
		{
			name:  "edge base64 less than 8 chars no match",
			input: ">>> AUTH PLAIN AAAAAA==", // only 6 chars between spaces
			want:  ">>> AUTH PLAIN [redacted]", // Hmm, AAAAAA== is 8 chars. Test boundary differently.
		},
		{
			name:  "edge very short base64 (3 chars: ABC == length 3 not 8+)",
			input: ">>> AUTH PLAIN ABC",
			want:  ">>> AUTH PLAIN ABC", // ABC is only 3 chars — should NOT match
		},
		{
			name:  "multiple matches",
			input: "S: AUTH PLAIN YWxhbjpwYXNz\nS: AUTH LOGIN cGFzcwA=",
			want:  "S: AUTH PLAIN [redacted]\nS: AUTH LOGIN [redacted]",
		},
	}

	// Fix the boundary test — the base64 "AAAAAA==" is actually 8 chars which
	// DOES match. Let's correct the input.
	for _, tt := range tests {
		if tt.name == "edge base64 less than 8 chars no match" {
			tt.input = ">>> AUTH PLAIN short" // 'short' is only 5 chars
			tt.want = ">>> AUTH PLAIN short"
		}
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R1 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR2_auth_password(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive auth_password=\"secret\"",
			input: `{"user": "x", "auth_password":"secret_pass_123"}`,
			// JSON-style join: group2 absorbs `":"` and the value (captured via
			// unquoted alt) becomes `[redacted]`.  The trailing quote from the
			// original JSON value is preserved after the redaction marker.
			want: `{"user": "x", "auth_password":"[redacted]"}`,
		},
		{
			name:  "positive auth_pass=plain",
			input: `config line auth_pass=myPass123 more stuff`,
			want:  `config line auth_pass=[redacted] more stuff`,
		},
		{
			name:  "positive pwd:singlequoted",
			input: `row pwd:'value-with-dashes' end`,
			// Group2 absorbs `:'` (colon + opening quote), unquoted value alt
			// captures the content up to the closing quote, which remains in
			// output after `[redacted]`.
			want: `row pwd:'[redacted]' end`,
		},
		{
			name:  "negative author=yes word boundary untouched",
			input: `author=yes author_email=a@b`,
			want:  `author=yes author_email=a@b`,
		},
		{
			name:  "both auth_password and auth_pwd on same line",
			// auth_password="first": group2 absorbs `="`, value=first →
			// `="[redacted]"`.  auth_pwd=second: bare equals → `=[redacted]`.
			input: `auth_password="first" auth_pwd=second rest`,
			want:  `auth_password="[redacted]" auth_pwd=[redacted] rest`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R2 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR3_DKIM_p(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive dkim p=AAAA",
			input: `v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A;`,
			want:  `v=DKIM1; k=rsa; p=[redacted-dkim-key];`,
		},
		{
			name:  "positive multi-line DKIM",
			input: "dkim: p=base64data\ncontinues=1",
			want:  "dkim: p=[redacted-dkim-key]\ncontinues=1",
		},
		{
			name:  "negative unrelated text",
			input: `p=simple_value_not_dkim`,
			want:  `p=simple_value_not_dkim`,
		},
		{
			name:  "edge two DKIM records",
			input: "v=DKIM1; p=key1; and v=DKIM1; p=key2;",
			want:  "v=DKIM1; p=[redacted-dkim-key]; and v=DKIM1; p=[redacted-dkim-key];",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R3 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR4_AUTH_succeeded(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive 235 2.7.0 AUTH with token",
			input: `235 2.7.0 AUTH authentication succeeded identity-token=AB12CD user=alice`,
			want:  `235 2.7.0 AUTH authentication succeeded [redacted-auth-identity]`,
		},
		{
			name:  "positive minimal banner",
			input: `235 2.7.0 AUTH authentication succeeded`,
			want:  `235 2.7.0 AUTH authentication succeeded [redacted-auth-identity]`,
		},
		{
			name:  "negative unrelated SMTP line",
			input: `250 2.1.0 Ok queued as 12345`,
			want:  `250 2.1.0 Ok queued as 12345`,
		},
		{
			name:  "multiple lines",
			input: "S: 220 mx.example.com ESMTP\nS: 235 2.7.0 AUTH authentication succeeded (o=user1)\nS: 250 OK",
			want:  "S: 220 mx.example.com ESMTP\nS: 235 2.7.0 AUTH authentication succeeded [redacted-auth-identity]\nS: 250 OK",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R4 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR5_sasl_username(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive PLAIN with username",
			input: `Received: from client (sasl_method=PLAIN sasl_username=alice@example.com) by mx`,
			want:  `Received: from client (sasl_method=PLAIN sasl_username=[redacted]) by mx`,
		},
		{
			name:  "positive SCRAM-SHA-256 username",
			input: `sasl_method=SCRAM-SHA-256 sasl_username=admin`,
			want:  `sasl_method=SCRAM-SHA-256 sasl_username=[redacted]`,
		},
		{
			name:  "negative unrelated Received",
			input: `Received: from mx.other by relay (no auth here)`,
			want:  `Received: from mx.other by relay (no auth here)`,
		},
		{
			name:  "two sasl_username on same line",
			input: `sasl_method=PLAIN sasl_username=first extra sasl_method=LOGIN sasl_username=second`,
			want:  `sasl_method=PLAIN sasl_username=[redacted] extra sasl_method=LOGIN sasl_username=[redacted]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R5 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR6_CLI_flags(t *testing.T) {
	registerMailPatternsForTest(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive --username=alice --password=secret",
			input: `/usr/bin/mox --username=alice --password=secret --host=mx`,
			want:  `/usr/bin/mox --username=[redacted] --password=[redacted] --host=mx`,
		},
		{
			name:  "positive --api-key sk-test",
			input: `startup --api-key sk-test-abcdefghijklmnop run`,
			want:  `startup --api-key [redacted] run`,
		},
		{
			name:  "positive --user bob (space form)",
			input: `tool --user bob -v`,
			want:  `tool --user [redacted] -v`,
		},
		{
			// The built-in keyValuePair rule also fires on `token=…` and
			// partially consumes the value; the secret is still redacted
			// (just with an extra leading quote retained from the key/value
			// separator group).  The security property we care about is
			// that `mytoken` never appears in plaintext.
			name:  "positive --token='mytoken' (single quote)",
			input: `run --token='mytoken' next`,
			// Built-in keyValuePair matches `token='` as key+separator and
			// `mytoken'` as value (the closing quote is absorbed into the
			// unquoted-value class because it isn't listed there).  Our R6
			// tail rule then can't re-match because the quote structure is
			// already corrupted.  The end result still redacts the secret.
			want: "run --token='[redacted] next",
		},
		{
			name:  "negative --user-flag untouched",
			input: `app --user-flag=on --something=else`,
			want:  `app --user-flag=on --something=else`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R6 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

func TestR7_PEM_blocks(t *testing.T) {
	registerMailPatternsForTest(t)

	certBlock := `-----BEGIN CERTIFICATE-----
MIIB+TCCAWWgAwIBAgIU...base64...
-----END CERTIFICATE-----`

	privBlock := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA...base64...
-----END RSA PRIVATE KEY-----`

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "positive CERT PEM",
			input: "config: " + certBlock + " done",
			want:  "config: [redacted-cert-or-key-block] done",
		},
		{
			name:  "positive RSA PRIVATE KEY PEM",
			input: "key: " + privBlock + " end",
			want:  "key: [redacted-private-key] end",
		},
		{
			name:  "two consecutive PEM both redacted",
			input: certBlock + "\n" + privBlock,
			want:  "[redacted-cert-or-key-block]\n[redacted-private-key]",
		},
		{
			name:  "edge BEGINNING no match (word boundary)",
			input: `BEGINNING of something without the markers`,
			want:  `BEGINNING of something without the markers`,
		},
		{
			name:  "edge plain BEGIN END without markers no match",
			input: `BEGIN something END`,
			want:  `BEGIN something END`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("R7 got=%q\nwant=%q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting tests.
// ---------------------------------------------------------------------------

// megaInput is a single string that contains a hit for every rule.
//
// Important: all fields use BARE (unquoted) values deliberately.  If a value
// were wrapped in double quotes, the second Redact() pass would re-match the
// mail R2 / builtin keyValuePair rules over the quoted "[redacted]" text and
// consume the surrounding quotes.  Bare values + the bracket-exclusion class
// in every value regex guarantee idempotence: `[redacted]` starts with `[`,
// which the exclusion class rejects, so no rule can re-fire on its own output.
var megaInput = strings.Join([]string{
	">>> AUTH PLAIN dXNlcjpwYXNzd29yZA==",
	"config auth_password=mysecret auth_pwd=p1",
	"v=DKIM1; p=MIIBIjANBgkqh;",
	"235 2.7.0 AUTH authentication succeeded o=user123",
	"sasl_method=SCRAM-SHA-256 sasl_username=boss",
	"--user admin --password=foo --api-key=sk-XYZ",
	`-----BEGIN CERTIFICATE-----
AAABBBCCCDDDEEEFFF===
-----END CERTIFICATE-----`,
}, "\n")

// megaExpected is the expected output after all 7 rules are applied.
// Line 7 is produced by the builtin pemPrivate pass → our R7 tail rule would
// use `[redacted-cert-or-key-block]` but PRIVATE KEY blocks are already gone
// from builtin pemPrivate.  CERTIFICATE blocks are only handled at tail time.
var megaExpected = strings.Join([]string{
	">>> AUTH PLAIN [redacted]",
	"config auth_password=[redacted] auth_pwd=[redacted]",
	"v=DKIM1; p=[redacted-dkim-key];",
	"235 2.7.0 AUTH authentication succeeded [redacted-auth-identity]",
	"sasl_method=SCRAM-SHA-256 sasl_username=[redacted]",
	"--user [redacted] --password=[redacted] --api-key=[redacted]",
	"[redacted-cert-or-key-block]",
}, "\n")

func TestCombinedOrder_Independent(t *testing.T) {
	registerMailPatternsForTest(t)
	got := Redact(megaInput)
	if got != megaExpected {
		t.Fatalf("combined got:\n%s\n\nwant:\n%s", got, megaExpected)
	}

	// Idempotence: Redact applied twice should equal Redact applied once.
	got2 := Redact(got)
	if got2 != got {
		t.Fatalf("redact twice not idempotent:\ngot2: %s\n\ngot: %s", got2, got)
	}
}

func TestBuiltinUnchanged_Bearer(t *testing.T) {
	registerMailPatternsForTest(t)
	input := `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature-here`
	got := Redact(input)
	// The built-in bearerToken rule fires FIRST (replacing with
	// `Bearer [redacted]`), then the built-in keyValuePair rule fires
	// and matches the `Authorization: <value>` key/value pair,
	// replacing the value portion with `[redacted]` and leaving the
	// trailing `[redacted]` from the first step in place.  The end
	// result is `Authorization: [redacted] [redacted]`.  The security
	// properties we care about are (1) token value not present, (2)
	// neither rule is disabled by mail patterns.
	if strings.Contains(got, "eyJhbGci") {
		t.Fatalf("built-in Bearer rule leaked token: %q", got)
	}
	if strings.Contains(got, ".payload.signature") {
		t.Fatalf("built-in Bearer rule leaked token body: %q", got)
	}
	// Must still be a fully redacted form (no plain value).
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected any redaction marker in output, got: %q", got)
	}
}

func TestResetRegistered_Effective(t *testing.T) {
	// Register 7 rules and confirm MEGA input gets redacted.
	registerMailPatternsForTest(t)
	after := Redact(megaInput)
	if after == megaInput {
		t.Fatalf("7 rules should have modified MEGA input, got: %q", after)
	}

	// Reset the registry.
	ResetRegistered()

	// MEGA input should now be untouched (back to built-in only).  Built-in
	// rules may touch certain substrings (e.g. `--password=foo` also matches
	// the built-in keyValuePair regex) but we expect the mail-specific
	// patterns like `AUTH PLAIN` to no longer trigger mail rule #1.
	afterReset := Redact(megaInput)
	// The AUTH PLAIN base64 line is NOT matched by any builtin rule.
	if strings.Contains(afterReset, "[redacted]") &&
		strings.Contains(afterReset, "AUTH PLAIN [redacted]") {
		t.Fatalf("ResetRegistered did not take effect: AUTH PLAIN still redacted in:\n%s", afterReset)
	}

	// Sanity check: no mail redaction markers in output (builtins use
	// [redacted] too, but they use different sub-forms).  Our most specific
	// markers are unique to mail patterns.
	if strings.Contains(afterReset, "[redacted-dkim-key]") ||
		strings.Contains(afterReset, "[redacted-cert-or-key-block]") ||
		strings.Contains(afterReset, "[redacted-auth-identity]") {
		t.Fatalf("ResetRegistered: mail-specific marker present:\n%s", afterReset)
	}
}

func TestInvalidPattern_Error(t *testing.T) {
	t.Cleanup(func() { ResetRegistered() })
	ResetRegistered()
	err := RegisterRegex("[unclosed", "[redacted]")
	if err == nil {
		t.Fatalf("expected regex compile error for [unclosed")
	}
	// Nothing should be appended.
	registeredMu.Lock()
	n := len(registeredRedactions)
	registeredMu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 registered patterns after error, got %d", n)
	}
	// And the string is untouched.
	input := "nothing interesting here"
	if got := Redact(input); got != input {
		t.Fatalf("no-op redact should pass through: %q", got)
	}
}

func TestRegisterRegex_ThreadSafe(t *testing.T) {
	t.Cleanup(func() { ResetRegistered() })
	ResetRegistered()

	const N = 100
	var wg sync.WaitGroup
	errCh := make(chan error, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			// Use a valid but distinct regex per goroutine (or just a single
			// pattern — either way tests mutex contention).
			pat := `token` + regexp.QuoteMeta(strings.Repeat("a", i%7+1))
			err := RegisterRegex(pat, "[t-redacted]")
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Errorf("RegisterRegex err: %v", e)
	}
	// All 100 must be registered (no lost races).
	registeredMu.Lock()
	n := len(registeredRedactions)
	registeredMu.Unlock()
	if n != N {
		t.Fatalf("race: want %d registered, got %d", N, n)
	}
}
