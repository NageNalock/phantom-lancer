package configapply

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// HashFile returns SHA-256 hex of a file's byte contents.  Empty file path
// or missing file → returns an empty string and an error.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("configapply: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("configapply: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashBytes returns SHA-256 hex of byte slice.
func HashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// DiffBytes performs a set-difference of newline-delimited lines, returning
// added / removed line lists.  Ordering of both outputs is deterministic
// (sorted).  Used for the quick "what changed" UI summary; not security
// sensitive.
func DiffBytes(a, b []byte) (added, removed []string) {
	setA := readSet(a)
	setB := readSet(b)
	// added = B \ A
	for line := range setB {
		if !setA[line] {
			added = append(added, line)
		}
	}
	// removed = A \ B
	for line := range setA {
		if !setB[line] {
			removed = append(removed, line)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func readSet(b []byte) map[string]bool {
	m := make(map[string]bool)
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1MB line buffer for large mox.conf
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" {
			continue
		}
		m[line] = true
	}
	return m
}
