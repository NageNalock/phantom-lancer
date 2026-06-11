package storage

import (
	"strings"
	"testing"
)

// TestAuditRedactionKeyAwareAndValueAware verifies the defence-in-depth
// redaction applied by AddAudit / redactAuditPayload:
//
//  1. Keys matching the sensitive list (password, token, secret, ...) are
//     always fully replaced with a key-named marker — EVEN if their value
//     wouldn't trigger safelog.Redact (e.g. a bare `password: "hunter2"`
//     or a non-prefixed opaque token like "a3f0x9qz…").
//  2. Non-sensitive keys still have their string values run through
//     safelog.Redact so Bearer tokens, api_key="…" k/v pairs, AWS
//     signatures etc. are caught even when tucked under innocuous keys
//     like "headers" or "raw_message".
//  3. Recursion: rules apply at every map depth and inside slices.
//  4. Benign fields (counts, IDs, enums, URLs without secrets) are
//     preserved untouched so the audit JSON stays useful.
func TestAuditRedactionKeyAwareAndValueAware(t *testing.T) {
	payload := map[string]any{
		// --- Case 1: Sensitive keys, various shapes. ---
		"password":      "hunter2",                                // bare value, no regex match
		"PassWord":      "hunter2",                                // case-insensitive
		"api_key":       "sk-abcdefghijklmnop",                    // underscore normalisation
		"Api-Key":       "another-secret",                         // dash + mixed case
		"access.token":  "opaque-long-token-xyzabc123xyzabc123",   // dot
		"refresh_token": "a3f0x9qzk2m8vbr1tyc4n0twgu3ss7hisvalue", // non-prefixed
		"CSRF-Token":    "csrf-session-value",
		"sessionId":     "session-cookie-id",
		"authorization": "custom scheme not matching regex",
		"cookie":        "sid=abc; uid=def",
		"nested": map[string]any{
			"Password":    "deep-hunter2", // recursive key-aware
			"description": "ok",
		},
		// --- Case 2: Non-sensitive keys with regex-matchable values. ---
		"headers": "Authorization: Bearer tok-12345 api_key=\"leaked-here\"",
		"message": "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260611/us-east-1/s3/aws4_request",
		// --- Case 3: Benign values preserved. ---
		"count":   42,
		"success": true,
		"url":     "https://example.com/path", // no query = no secret
		"list":    []string{"public-label-1", "public-label-2"},
	}

	redacted := redactAuditPayload(payload)

	// ---- Key-aware assertions ----
	secretCases := []struct{ key, expectPrefix string }{
		{"password", "[redacted:password]"},
		{"PassWord", "[redacted:PassWord]"},
		{"api_key", "[redacted:api_key]"},
		{"Api-Key", "[redacted:Api-Key]"},
		{"access.token", "[redacted:access.token]"},
		{"refresh_token", "[redacted:refresh_token]"},
		{"CSRF-Token", "[redacted:CSRF-Token]"},
		{"sessionId", "[redacted:sessionId]"},
		{"authorization", "[redacted:authorization]"},
		{"cookie", "[redacted:cookie]"},
	}
	for _, c := range secretCases {
		got, ok := redacted[c.key].(string)
		if !ok {
			t.Fatalf("key %q: want string, got %T", c.key, redacted[c.key])
		}
		if got != c.expectPrefix {
			t.Errorf("key %q: want %q, got %q", c.key, c.expectPrefix, got)
		}
		if strings.Contains(got, "hunter2") || strings.Contains(got, "secret") || strings.Contains(got, "leaked") {
			t.Errorf("key %q: value marker contains a secret fragment: %q", c.key, got)
		}
	}
	nested, ok := redacted["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested: want map, got %T", redacted["nested"])
	}
	if nested["Password"] != "[redacted:Password]" {
		t.Errorf("nested.Password: want marker, got %v", nested["Password"])
	}
	if nested["description"] != "ok" {
		t.Errorf("nested.description: want \"ok\" preserved, got %v", nested["description"])
	}

	// ---- Value-aware assertions (strings run through safelog.Redact) ----
	hdrs, _ := redacted["headers"].(string)
	if strings.Contains(hdrs, "tok-12345") || strings.Contains(hdrs, "leaked-here") {
		t.Errorf("headers value leaked secrets: %q", hdrs)
	}
	if !strings.Contains(hdrs, "[redacted]") {
		t.Errorf("headers value missing safelog marker: %q", hdrs)
	}
	msg, _ := redacted["message"].(string)
	if strings.Contains(msg, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("message leaked AWS credential: %q", msg)
	}

	// ---- Benign assertions ----
	if redacted["count"] != 42 {
		t.Errorf("count: want 42 preserved, got %v", redacted["count"])
	}
	if redacted["success"] != true {
		t.Errorf("success: want true preserved, got %v", redacted["success"])
	}
	if redacted["url"] != "https://example.com/path" {
		t.Errorf("url: want preserved, got %v", redacted["url"])
	}
	lst, _ := redacted["list"].([]string)
	if len(lst) != 2 || lst[0] != "public-label-1" || lst[1] != "public-label-2" {
		t.Errorf("list: want preserved, got %v", lst)
	}

	// ---- Sanity: the original input plaintext does NOT appear verbatim. ----
	full := mapToString(redacted)
	forbidden := []string{
		"hunter2", "sk-abcdefghijklmnop", "another-secret",
		"opaque-long-token-xyzabc123xyzabc123",
		"a3f0x9qzk2m8vbr1tyc4n0twgu3ss7hisvalue",
		"csrf-session-value", "session-cookie-id", "deep-hunter2",
		"tok-12345", "leaked-here", "AKIAIOSFODNN7EXAMPLE",
	}
	for _, f := range forbidden {
		if strings.Contains(full, f) {
			t.Errorf("forbidden plaintext %q still present in redacted payload", f)
		}
	}
}

// mapToString is a small helper for leak-checking the whole tree. We
// deliberately re-serialise via printf (not JSON) so type annotations
// cannot mask a stray plaintext.
func mapToString(m map[string]any) string {
	var sb strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			sb.WriteString(x)
		case []string:
			for _, s := range x {
				sb.WriteString(s)
			}
		case []any:
			for _, i := range x {
				walk(i)
			}
		case map[string]any:
			for _, mv := range x {
				walk(mv)
			}
		}
	}
	walk(m)
	return sb.String()
}

// TestIsSecretKeyNormalisation covers the key-name normaliser used by
// isSecretKey so mixed case + separators can't slip past.
func TestIsSecretKeyNormalisation(t *testing.T) {
	cases := map[string]bool{
		"password":         true,
		"PASSWORD":         true,
		"Pass_Word":        true, // underscore + mixed = password
		"api-key":          true,
		"API.KEY":          true,
		"Refresh-Token":    true,
		"csrf token":       true,
		"path":             false,
		"username":         false,
		"status_code":      false,
		"repositoryPrefix": false,
		"credential_id":    false, // id is suffix, not = credential
		"credential":       true,
		"credentials":      true,
	}
	for in, want := range cases {
		got := isSecretKey(in)
		if got != want {
			t.Errorf("isSecretKey(%q) = %v, want %v", in, got, want)
		}
	}
}
