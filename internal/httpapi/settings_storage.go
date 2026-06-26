package httpapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type localDatabaseFileStat struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Path      string `json:"path,omitempty"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type localStorageStats struct {
	SQLite localDatabaseFileStat   `json:"sqlite"`
	DuckDB []localDatabaseFileStat `json:"duckdb"`
}

func (s *Server) settingsLocalStorageStats() localStorageStats {
	sqlite := localDatabaseFileStat{
		Kind:  "sqlite",
		Label: "SQLite 主库",
		Path:  s.cfg.DBPath,
	}
	if s.cfg.DBPath == ":memory:" {
		sqlite.Exists = true
		sqlite.Label = "SQLite 内存库"
	} else if s.cfg.DBPath != "" {
		if size, err := s.store.DatabaseSizeBytes(); err == nil {
			sqlite.Exists = true
			sqlite.SizeBytes = size
			if info, statErr := os.Stat(s.cfg.DBPath); statErr == nil {
				sqlite.UpdatedAt = info.ModTime().Format(time.RFC3339Nano)
			}
		}
	}

	return localStorageStats{
		SQLite: sqlite,
		DuckDB: collectDuckDBFileStats(s.cfg.DataDir, s.cfg.DBPath),
	}
}

func collectDuckDBFileStats(dataDir, dbPath string) []localDatabaseFileStat {
	seen := make(map[string]bool)
	var dirs []string
	addDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean, err := filepath.Abs(dir)
		if err != nil {
			clean = filepath.Clean(dir)
		}
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, clean)
		}
	}
	addDir(dataDir)
	if dbPath != "" && dbPath != ":memory:" {
		addDir(filepath.Dir(dbPath))
	}

	paths := make(map[string]bool)
	var out []localDatabaseFileStat
	for _, dir := range dirs {
		for _, candidateDir := range duckDBCandidateDirs(dir) {
			matches, _ := filepath.Glob(filepath.Join(candidateDir, "*.duckdb*"))
			for _, match := range matches {
				if paths[match] {
					continue
				}
				info, err := os.Stat(match)
				if err != nil || info.IsDir() {
					continue
				}
				name := filepath.Base(match)
				if !isDuckDBFileName(name) {
					continue
				}
				paths[match] = true
				kind := "duckdb"
				label := "DuckDB"
				if strings.HasSuffix(strings.ToLower(name), ".wal") {
					kind = "duckdb-wal"
					label = "DuckDB WAL"
				}
				out = append(out, localDatabaseFileStat{
					Kind:      kind,
					Label:     label,
					Path:      match,
					Exists:    true,
					SizeBytes: info.Size(),
					UpdatedAt: info.ModTime().Format(time.RFC3339Nano),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func duckDBCandidateDirs(dataDir string) []string {
	dirs := []string{dataDir}
	for _, name := range []string{"stockv2", "market", "markets"} {
		dirs = append(dirs, filepath.Join(dataDir, name))
	}
	return dirs
}

func isDuckDBFileName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".duckdb") || strings.HasSuffix(lower, ".duckdb.wal")
}
