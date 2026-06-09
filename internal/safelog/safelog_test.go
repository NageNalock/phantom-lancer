package safelog

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorRedactsSecretsAndTruncates(t *testing.T) {
	got := Error(errors.New(`authorization: Bearer secret-token api_key="abc123" trailing text`), 48)
	if strings.Contains(got, "secret-token") || strings.Contains(got, "abc123") {
		t.Fatalf("Error leaked secret: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
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
