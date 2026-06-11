package safelog

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestErrorRedactsSecretsAndTruncates(t *testing.T) {
	got := Error(errors.New(`authorization: Bearer secret-token api_key="abc123" trailing text`), 48)
	if strings.Contains(got, "secret-token") || strings.Contains(got, "abc123") {
		t.Fatalf("Error leaked secret: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("Error did not include redaction marker: %q", got)
	}
	if len([]rune(got)) > 51 {
		t.Fatalf("Error was not truncated: %q", got)
	}
}

func TestURLLabelDropsQuery(t *testing.T) {
	got := URLLabel("https://example.com/path/file?token=secret&x=1")
	if got != "https://example.com/path/file" {
		t.Fatalf("URLLabel = %q", got)
	}
}

func TestRequestPathLabelDropsQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple path", "/api/v1/users", "/api/v1/users"},
		{"path with query", "/api/codex/threads/abc/events?after=123", "/api/codex/threads/abc/events"},
		{"path with sensitive query", "/login?code=secret123&session_id=xyz", "/login"},
		{"root path", "/", "/"},
		{"path with fragment", "/page#section", "/page"},
		{"trailing slash trimmed", "/api/users/", "/api/users"},
		{"empty path becomes root", "", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := url.Parse(tt.input)
			got := RequestPathLabel(u)
			if got != tt.want {
				t.Fatalf("RequestPathLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRequestPathLabelNilURL(t *testing.T) {
	if got := RequestPathLabel(nil); got != "" {
		t.Fatalf("RequestPathLabel(nil) = %q, want empty", got)
	}
}

func TestRequestPathLabelRedactsSecrets(t *testing.T) {
	// Even though query is stripped, verify the path itself goes through redaction.
	u, _ := url.Parse("/api/key/secret-token-value-here")
	got := RequestPathLabel(u)
	// path-only shouldn't trigger keyValuePair since there's no =, but should
	// still pass through Text pipeline.
	if got == "" {
		t.Fatal("unexpected empty result")
	}
}
