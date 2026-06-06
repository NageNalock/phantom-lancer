package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSHA256(t *testing.T) {
	sum := strings.Repeat("a", 64)
	got, err := parseSHA256(sum + "  phantom-lancer-linux-amd64.tar.gz\n")
	if err != nil {
		t.Fatalf("parseSHA256: %v", err)
	}
	if got != sum {
		t.Fatalf("checksum = %q, want %q", got, sum)
	}
	if _, err := parseSHA256("not-a-sha"); err == nil {
		t.Fatal("parseSHA256 accepted invalid digest")
	}
}

func TestExtractBinaryRejectsUnsafeArchivePath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "unsafe.tar.gz")
	if err := writeTestArchive(archive, map[string]string{"../phantom-lancer": "bad"}); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := extractBinary(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("extractBinary accepted unsafe path")
	}
}

func TestExtractBinaryOnlyWritesReleaseBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	if err := writeTestArchive(archive, map[string]string{
		"phantom-lancer/README.md":               "ignored",
		"phantom-lancer/bin/phantom-lancer":      "binary",
		"phantom-lancer/configs/phantom.toml":    "ignored config",
		"phantom-lancer/configs/phantom.example": "ignored example",
	}); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	out := filepath.Join(dir, "phantom-lancer")
	if err := extractBinary(archive, out); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(data) != "binary" {
		t.Fatalf("extracted data = %q, want binary", string(data))
	}
	if _, err := os.Stat(filepath.Join(dir, "phantom-lancer", "configs", "phantom.toml")); err == nil {
		t.Fatal("extractBinary wrote config file")
	}
}

func writeTestArchive(path string, files map[string]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()
	for name, body := range files {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			return err
		}
	}
	return nil
}
