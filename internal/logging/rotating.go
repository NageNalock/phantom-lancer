package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Path        string
	MaxSizeMB   int
	MaxFiles    int
	MaxAgeDays  int
	WriteStdout bool
}

type LoggerHandle struct {
	Logger *slog.Logger
	closer io.Closer
}

func NewLogger(cfg Config) (*LoggerHandle, error) {
	writers := []io.Writer{}
	closers := []io.Closer{}
	if strings.TrimSpace(cfg.Path) != "" {
		rotating, err := NewRotatingWriter(cfg)
		if err != nil {
			return nil, err
		}
		writers = append(writers, rotating)
		closers = append(closers, rotating)
	}
	if cfg.WriteStdout || len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}
	writer := writers[0]
	if len(writers) > 1 {
		writer = io.MultiWriter(writers...)
	}
	return &LoggerHandle{
		Logger: slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})),
		closer: multiCloser(closers),
	}, nil
}

func (h *LoggerHandle) Close() error {
	if h == nil || h.closer == nil {
		return nil
	}
	return h.closer.Close()
}

type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxSize  int64
	maxFiles int
	maxAge   time.Duration
	file     *os.File
	size     int64
}

func NewRotatingWriter(cfg Config) (*RotatingWriter, error) {
	maxSize := int64(cfg.MaxSizeMB) * 1024 * 1024
	if maxSize <= 0 {
		maxSize = 32 * 1024 * 1024
	}
	w := &RotatingWriter{
		path:     filepath.Clean(cfg.Path),
		maxSize:  maxSize,
		maxFiles: cfg.MaxFiles,
	}
	if cfg.MaxAgeDays > 0 {
		w.maxAge = time.Duration(cfg.MaxAgeDays) * 24 * time.Hour
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	if err := w.cleanupLocked(time.Now()); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.maxSize > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotateLocked(time.Now()); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	w.size = 0
	return err
}

func (w *RotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *RotatingWriter) rotateLocked(now time.Time) error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
		w.size = 0
	}
	if _, err := os.Stat(w.path); err == nil {
		rotated := w.rotatedPath(now)
		if err := os.Rename(w.path, rotated); err != nil {
			return err
		}
	}
	if err := w.open(); err != nil {
		return err
	}
	return w.cleanupLocked(now)
}

func (w *RotatingWriter) rotatedPath(now time.Time) string {
	base := w.path + "." + now.UTC().Format("20060102T150405Z")
	candidate := base
	for index := 1; ; index++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = base + "." + strconvFormat(index)
	}
}

func (w *RotatingWriter) cleanupLocked(now time.Time) error {
	rotated, err := w.rotatedFiles()
	if err != nil {
		return err
	}
	if w.maxAge > 0 {
		cutoff := now.Add(-w.maxAge)
		kept := rotated[:0]
		for _, item := range rotated {
			if item.ModTime.Before(cutoff) {
				if err := os.Remove(item.Path); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			kept = append(kept, item)
		}
		rotated = kept
	}
	if w.maxFiles > 0 && len(rotated) > w.maxFiles {
		sort.Slice(rotated, func(i, j int) bool {
			return rotated[i].ModTime.After(rotated[j].ModTime)
		})
		for _, item := range rotated[w.maxFiles:] {
			if err := os.Remove(item.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

type rotatedFile struct {
	Path    string
	ModTime time.Time
}

func (w *RotatingWriter) rotatedFiles() ([]rotatedFile, error) {
	entries, err := os.ReadDir(filepath.Dir(w.path))
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(w.path) + "."
	out := []rotatedFile{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, rotatedFile{
			Path:    filepath.Join(filepath.Dir(w.path), entry.Name()),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var first error
	for _, closer := range m {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func strconvFormat(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = digits[value%10]
		value /= 10
	}
	return string(buf[index:])
}
