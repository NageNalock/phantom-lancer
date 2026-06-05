package logs

import (
	"strings"
	"testing"
)

func TestParseLineRedactsSensitiveValues(t *testing.T) {
	raw := `{"time":"2026-06-05T10:00:00Z","level":"ERROR","msg":"provider failed token=sk-1234567890 password=secret","uuid":"550e8400-e29b-41d4-a716-446655440000","url":"https://example.test/image.png?signature=abc"}`
	line := ParseLine("service.phantom", 12, raw)
	if line.Level != "error" {
		t.Fatalf("Level = %q, want error", line.Level)
	}
	for _, value := range []string{"sk-1234567890", "password=secret", "550e8400-e29b-41d4-a716-446655440000", "signature=abc"} {
		if contains(line.Message, value) || contains(line.Raw, value) {
			t.Fatalf("sensitive value %q leaked in line: %#v", value, line)
		}
	}
}

func TestRedactStringHandlesBearerAndDataURL(t *testing.T) {
	value := RedactString("Authorization: Bearer abcdefghijklmnop image=data:image/png;base64,abcdef0123456789")
	if contains(value, "abcdefghijklmnop") || contains(value, "abcdef0123456789") {
		t.Fatalf("secret leaked: %s", value)
	}
	if !contains(value, "[redacted]") || !contains(value, "[redacted-image-data]") {
		t.Fatalf("redaction markers missing: %s", value)
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
