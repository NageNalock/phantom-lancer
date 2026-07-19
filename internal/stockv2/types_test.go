package stockv2

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateIDUses128BitRandomSuffix(t *testing.T) {
	id := generateID()
	suffix := id[strings.LastIndex(id, "-")+1:]
	decoded, err := hex.DecodeString(suffix)
	if err != nil {
		t.Fatalf("decode generated ID suffix %q: %v", suffix, err)
	}
	if len(decoded) != 16 {
		t.Fatalf("generated ID random suffix bytes = %d, want 16", len(decoded))
	}
}
