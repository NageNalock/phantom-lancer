package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterRotatesAndCleansByCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.jsonl")
	writer, err := NewRotatingWriter(Config{
		Path:       path,
		MaxSizeMB:  1,
		MaxFiles:   1,
		MaxAgeDays: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk := append(bytes.Repeat([]byte("x"), 700*1024), '\n')
	for i := 0; i < 4; i++ {
		if _, err := writer.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	rotated := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "service.jsonl.") {
			rotated++
		}
	}
	if rotated != 1 {
		t.Fatalf("rotated files = %d, want 1", rotated)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("active log is empty")
	}
}
