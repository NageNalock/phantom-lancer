package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"phantom-lancer/internal/storage"
)

type stagingPaths struct {
	dir          string
	archivePart  string
	archive      string
	checksum     string
	stagedBinary string
}

func (s *Service) prepareStaging(jobID string) (stagingPaths, error) {
	dir := filepath.Join(s.cfg.DataDir, "updates", "staging", jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return stagingPaths{}, err
	}
	return stagingPaths{
		dir:          dir,
		archivePart:  filepath.Join(dir, "archive.tar.gz.part"),
		archive:      filepath.Join(dir, "archive.tar.gz"),
		checksum:     filepath.Join(dir, "archive.tar.gz.sha256"),
		stagedBinary: filepath.Join(dir, "phantom-lancer"),
	}, nil
}

func (s *Service) download(ctx context.Context, rawURL, path string, job *storage.SystemUpdateJob, maxBytes int64) error {
	if err := validateDownloadURL(rawURL, s.cfg.AllowInsecureDownloads); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "phantom-lancer-self-update")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return errors.New("download is larger than the allowed maximum")
	}
	if resp.ContentLength > 0 {
		job.TotalBytes = resp.ContentLength
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, 128*1024)
	var downloaded int64
	lastEvent := time.Now()
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxBytes {
				return errors.New("download exceeded the allowed maximum")
			}
			if _, err := file.Write(buffer[:n]); err != nil {
				return err
			}
			job.BytesDownloaded = downloaded
			if time.Since(lastEvent) >= 500*time.Millisecond || downloaded == resp.ContentLength {
				lastEvent = time.Now()
				if err := s.store.SaveSystemUpdateJob(ctx, *job); err != nil {
					return err
				}
				s.append(ctx, job.ID, "update.download.progress", map[string]any{"bytesDownloaded": downloaded, "totalBytes": job.TotalBytes})
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	job.BytesDownloaded = downloaded
	return s.store.SaveSystemUpdateJob(ctx, *job)
}

func (s *Service) downloadChecksum(ctx context.Context, rawURL, path string) error {
	if err := validateDownloadURL(rawURL, s.cfg.AllowInsecureDownloads); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "phantom-lancer-self-update")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum download failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxChecksumBytes {
		return errors.New("checksum file is too large")
	}
	return os.WriteFile(path, data, 0o600)
}

func rename(from, to string) error {
	return os.Rename(from, to)
}
