package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	releaseBinaryPath     = "phantom-lancer/bin/phantom-lancer"
	releaseSupervisorPath = "phantom-lancer/bin/phantom-supervisor"
)

// ExtractResult holds the staged paths of binaries extracted from a release
// archive. SupervisorBinary may be empty when extracting older archives that
// did not ship the supervisor binary.
type ExtractResult struct {
	MainBinary       string
	SupervisorBinary string
}

func verifyChecksum(archivePath, checksumPath string) (string, error) {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return "", err
	}
	want, err := parseSHA256(string(data))
	if err != nil {
		return "", err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, want) {
		return "", errors.New("release checksum does not match")
	}
	return got, nil
}

func parseSHA256(value string) (string, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", errors.New("checksum file is empty")
	}
	sum := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(sum) != 64 {
		return "", errors.New("checksum must be a SHA-256 hex digest")
	}
	for _, char := range sum {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return "", errors.New("checksum contains non-hex characters")
		}
	}
	return sum, nil
}

// extractBinaries extracts the release binaries from the archive into
// sibling output paths. stagedMainPath is the destination for phantom-lancer;
// the supervisor is written to stagedMainPath + ".supervisor" (and present in
// the result only when the archive contains one).
func extractBinaries(archivePath, stagedMainPath string) (ExtractResult, error) {
	result := ExtractResult{MainBinary: stagedMainPath}
	stagedSuperPath := stagedMainPath + ".supervisor"

	file, err := os.Open(archivePath)
	if err != nil {
		return result, err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return result, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)

	mainFound := false
	supervisorFound := false
	var files int
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, err
		}
		files++
		if files > 200 {
			return result, errors.New("release archive contains too many files")
		}
		clean, err := safeTarName(header.Name)
		if err != nil {
			return result, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if total > maxArchiveBytes {
				return result, errors.New("release archive expands beyond the allowed maximum")
			}
			switch clean {
			case releaseBinaryPath:
				if err := writeExtractedBinary(reader, stagedMainPath); err != nil {
					return result, err
				}
				mainFound = true
			case releaseSupervisorPath:
				if err := writeExtractedBinary(reader, stagedSuperPath); err != nil {
					return result, err
				}
				supervisorFound = true
				result.SupervisorBinary = stagedSuperPath
			default:
				// Other release assets (scripts, configs, README) are not
				// touched by the self-updater. Drain the entry.
				_, _ = io.Copy(io.Discard, reader)
			}
		default:
			return result, fmt.Errorf("release archive contains unsupported file type at %s", clean)
		}
	}
	if !mainFound {
		return result, errors.New("release archive does not contain phantom-lancer binary")
	}
	_ = supervisorFound
	return result, nil
}

// extractBinary is a thin compatibility wrapper around extractBinaries for
// existing callers/tests that only need the main binary.
func extractBinary(archivePath, outputPath string) error {
	_, err := extractBinaries(archivePath, outputPath)
	return err
}

func safeTarName(name string) (string, error) {
	clean := path.Clean(strings.TrimSpace(name))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.Contains(clean, "/../") {
		return "", errors.New("release archive contains unsafe path")
	}
	return clean, nil
}

func writeExtractedBinary(reader io.Reader, outputPath string) error {
	if err := os.MkdirAll(pathDir(outputPath), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	if err := file.Chmod(0o755); err != nil {
		return err
	}
	return file.Sync()
}

func pathDir(value string) string {
	if index := strings.LastIndex(value, string(os.PathSeparator)); index >= 0 {
		return value[:index]
	}
	return "."
}
