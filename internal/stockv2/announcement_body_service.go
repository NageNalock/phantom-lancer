package stockv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"
)

const (
	announcementBodyMaxRequestsPerRun = 4
	announcementBodyProcessInterval   = 5 * time.Minute
	announcementBodyDownloadTimeout   = 15 * time.Second
	announcementBodyExtractTimeout    = 12 * time.Second
	announcementBodyExtractPages      = 8
	announcementBodyMaxExtractBytes   = 1 << 20
	announcementBodyExcerptBytes      = 24 << 10
	announcementBodyMinTextRunes      = 80
)

func (s *Service) runMajorAnnouncementBodyScheduler(ctx context.Context) {
	ticker := time.NewTicker(announcementBodyProcessInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.currentResourceGate().State != ResourceGateNormal {
				continue
			}
			result, err := s.ProcessPendingMajorAnnouncementBodies(ctx)
			if err != nil && s.log != nil {
				s.log.Warn(
					"major announcement body batch stopped",
					"claimed", result.Claimed,
					"ready", result.Ready,
					"error", safelog.Error(err, 240),
				)
			}
		}
	}
}

var (
	errAnnouncementBodyInvalidURL   = errors.New("announcement PDF URL is not an official CNINFO HTTPS URL")
	errAnnouncementBodyInvalidPDF   = errors.New("announcement response is not a valid PDF")
	errAnnouncementBodyPDFTooLarge  = errors.New("announcement PDF exceeds the size limit")
	errAnnouncementBodyTextTooLarge = errors.New("announcement extracted text exceeds the size limit")
	errAnnouncementBodyTextEmpty    = errors.New("announcement PDF has no substantive extractable text")
)

type AnnouncementBodyProcessResult struct {
	ParserAvailable bool `json:"parserAvailable"`
	Claimed         int  `json:"claimed"`
	Ready           int  `json:"ready"`
	Retrying        int  `json:"retrying"`
	Failed          int  `json:"failed"`
	BudgetExhausted bool `json:"budgetExhausted"`
}

func announcementBodyParserAvailable() bool {
	_, err := exec.LookPath("pdftotext")
	return err == nil
}

// ProcessPendingMajorAnnouncementBodies processes only durable major-announcement
// candidates. Missing Poppler is an explicit deployment prerequisite: without
// pdftotext this method performs no claim and no network request, leaving strict
// readiness blocked by metadata_only.
func (s *Service) ProcessPendingMajorAnnouncementBodies(ctx context.Context) (AnnouncementBodyProcessResult, error) {
	result := AnnouncementBodyProcessResult{}
	if s == nil || s.store == nil {
		return result, errors.New("announcement body processor is not configured")
	}
	pdfToTextPath, err := exec.LookPath("pdftotext")
	if err != nil {
		return result, nil
	}
	result.ParserAvailable = true
	now := time.Now()
	for result.Claimed < announcementBodyMaxRequestsPerRun {
		claim, err := s.store.claimMajorAnnouncementBody(ctx, now)
		if err != nil {
			return result, err
		}
		if claim.BudgetExhausted {
			result.BudgetExhausted = true
			break
		}
		if claim.Lease == nil {
			break
		}
		result.Claimed++
		lease := *claim.Lease
		pdf, downloadedBytes, fetchErr := fetchOfficialCNINFOAnnouncementPDF(
			ctx, s.httpClient, lease.Announcement.PDFURL,
		)
		if settleErr := s.store.settleAnnouncementBodyBudget(ctx, lease, downloadedBytes, time.Now()); settleErr != nil {
			return result, settleErr
		}
		if fetchErr != nil {
			terminal := errors.Is(fetchErr, errAnnouncementBodyInvalidURL) ||
				errors.Is(fetchErr, errAnnouncementBodyInvalidPDF) ||
				errors.Is(fetchErr, errAnnouncementBodyPDFTooLarge) ||
				lease.Announcement.BodyAttemptCount >= announcementBodyMaxAttempts
			if err := s.failAnnouncementBodyLease(ctx, lease, fetchErr, terminal); err != nil {
				return result, err
			}
			if terminal {
				result.Failed++
			} else {
				result.Retrying++
			}
			continue
		}

		text, extractErr := extractAnnouncementPDFText(ctx, pdfToTextPath, pdf)
		if extractErr == nil && !substantiveAnnouncementBodyText(text, lease.Announcement.Title) {
			extractErr = errAnnouncementBodyTextEmpty
		}
		if extractErr != nil {
			terminal := errors.Is(extractErr, errAnnouncementBodyTextEmpty) ||
				errors.Is(extractErr, errAnnouncementBodyTextTooLarge) ||
				lease.Announcement.BodyAttemptCount >= announcementBodyMaxAttempts
			if err := s.failAnnouncementBodyLease(ctx, lease, extractErr, terminal); err != nil {
				return result, err
			}
			if terminal {
				result.Failed++
			} else {
				result.Retrying++
			}
			continue
		}

		normalized := normalizeAnnouncementBodyText(text)
		hash := sha256.Sum256([]byte(normalized))
		if err := s.store.completeMajorAnnouncementBody(
			ctx,
			lease,
			utf8Prefix(normalized, announcementBodyExcerptBytes),
			hex.EncodeToString(hash[:]),
			downloadedBytes,
			time.Now(),
		); err != nil {
			return result, err
		}
		result.Ready++
	}
	return result, nil
}

func (s *Service) failAnnouncementBodyLease(
	ctx context.Context,
	lease announcementBodyLease,
	cause error,
	terminal bool,
) error {
	now := time.Now()
	nextAttemptAt := now.Add(announcementBodyRetryDelay(lease.Announcement.BodyAttemptCount))
	message := safelog.Text(cause.Error(), 240)
	return s.store.failMajorAnnouncementBody(ctx, lease, message, nextAttemptAt, terminal, now)
}

func announcementBodyRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Hour
	case 2:
		return 4 * time.Hour
	case 3:
		return 12 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func fetchOfficialCNINFOAnnouncementPDF(
	ctx context.Context,
	baseClient *http.Client,
	rawURL string,
) ([]byte, int64, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !validOfficialCNINFOAnnouncementPDFURL(parsed) {
		return nil, 0, errAnnouncementBodyInvalidURL
	}
	client := http.DefaultClient
	if baseClient != nil {
		client = baseClient
	}
	cloned := *client
	cloned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !validOfficialCNINFOAnnouncementPDFURL(req.URL) {
			return errAnnouncementBodyInvalidURL
		}
		return nil
	}
	downloadCtx, cancel := context.WithTimeout(ctx, announcementBodyDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, 0, errors.New("create announcement PDF request")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.cninfo.com.cn/")
	resp, err := cloned.Do(req)
	if err != nil {
		return nil, 0, errors.New("download announcement PDF failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("download announcement PDF returned status %d", resp.StatusCode)
	}
	if resp.ContentLength > announcementBodyMaxPDFBytes {
		return nil, 0, errAnnouncementBodyPDFTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, announcementBodyBudgetReservation))
	readBytes := int64(len(body))
	if err != nil {
		return nil, readBytes, errors.New("read announcement PDF failed")
	}
	if readBytes > announcementBodyMaxPDFBytes {
		return nil, readBytes, errAnnouncementBodyPDFTooLarge
	}
	if !validPDFMagic(body) {
		return nil, readBytes, errAnnouncementBodyInvalidPDF
	}
	return body, readBytes, nil
}

func validOfficialCNINFOAnnouncementPDFURL(value *url.URL) bool {
	return value != nil && strings.EqualFold(value.Scheme, "https") &&
		strings.EqualFold(value.Hostname(), "static.cninfo.com.cn") && value.User == nil
}

func validPDFMagic(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	limit := min(len(body), 1024)
	return bytes.Contains(body[:limit], []byte("%PDF-"))
}

func extractAnnouncementPDFText(ctx context.Context, pdfToTextPath string, pdf []byte) (string, error) {
	file, err := os.CreateTemp("", "stockv2-announcement-*.pdf")
	if err != nil {
		return "", errors.New("create temporary announcement PDF failed")
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return "", errors.New("secure temporary announcement PDF failed")
	}
	if _, err := file.Write(pdf); err != nil {
		file.Close()
		return "", errors.New("write temporary announcement PDF failed")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("close temporary announcement PDF failed")
	}

	extractCtx, cancel := context.WithTimeout(ctx, announcementBodyExtractTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		extractCtx,
		pdfToTextPath,
		"-f", "1",
		"-l", fmt.Sprintf("%d", announcementBodyExtractPages),
		"-layout",
		"-enc", "UTF-8",
		"-nopgbrk",
		path,
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", errors.New("open pdftotext output failed")
	}
	var stderr truncatingBuffer
	stderr.limit = 2048
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", errors.New("start pdftotext failed")
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, announcementBodyMaxExtractBytes+1))
	if len(output) > announcementBodyMaxExtractBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", errAnnouncementBodyTextTooLarge
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", errors.New("read pdftotext output failed")
	}
	if extractCtx.Err() != nil {
		return "", errors.New("pdftotext timed out")
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return "", errors.New("pdftotext failed")
		}
		return "", fmt.Errorf("pdftotext failed: %s", safelog.Text(message, 180))
	}
	return normalizeAnnouncementBodyText(string(output)), nil
}

type truncatingBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *truncatingBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *truncatingBuffer) String() string {
	return b.buffer.String()
}

func normalizeAnnouncementBodyText(raw string) string {
	raw = strings.ToValidUTF8(raw, "")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func substantiveAnnouncementBodyText(text, title string) bool {
	normalized := normalizeAnnouncementBodyText(text)
	if normalized == "" || normalized == normalizeAnnouncementBodyText(title) {
		return false
	}
	count := 0
	for _, value := range normalized {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			count++
			if count >= announcementBodyMinTextRunes {
				return true
			}
		}
	}
	return false
}

func utf8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) && len(value) > 0 {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}
