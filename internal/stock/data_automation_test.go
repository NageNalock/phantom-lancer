package stock

import (
	"testing"
	"time"
)

func TestNormalizeFeedTimeParsesLocalChinaTime(t *testing.T) {
	got := normalizeFeedTime("2026-06-24 16:30:00")
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("parse normalized time %q: %v", got, err)
	}
	if parsed.Format(time.RFC3339) != "2026-06-24T16:30:00+08:00" {
		t.Fatalf("normalized time = %s, want China local time", parsed.Format(time.RFC3339))
	}
}
