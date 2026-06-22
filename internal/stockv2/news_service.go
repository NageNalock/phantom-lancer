package stockv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

func (s *Service) CreateRawNews(ctx context.Context, req RequestCreateRawNews) (StockV2RawNews, error) {
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return StockV2RawNews{}, ErrInvalidRawNewsSource
	}
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	snippet := strings.TrimSpace(req.Snippet)
	newsURL := strings.TrimSpace(req.URL)
	if title == "" && content == "" && snippet == "" {
		return StockV2RawNews{}, ErrInvalidRawNewsContent
	}
	fetchedAt := req.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	hash := rawNewsContentHash(source, req.SourceID, title, content, snippet, newsURL, req.PublishedAt)
	dedupeKey := strings.TrimSpace(req.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = rawNewsDedupeKey(source, req.SourceID, hash)
	}
	item := StockV2RawNews{
		Source:      source,
		SourceID:    strings.TrimSpace(req.SourceID),
		Language:    strings.TrimSpace(req.Language),
		Title:       title,
		Content:     content,
		Snippet:     snippet,
		PublishedAt: req.PublishedAt,
		URL:         newsURL,
		FetchedAt:   fetchedAt,
		RawPayload:  req.RawPayload,
		ContentHash: hash,
		DedupeKey:   dedupeKey,
		Quality:     newsQualityOrDefault(req.Quality),
		Status:      newsStatusOrDefault(req.Status),
	}
	return s.store.CreateRawNews(ctx, item)
}

func (s *Service) ListRawNews(ctx context.Context, filter RawNewsListFilter) ([]StockV2RawNews, error) {
	return s.store.ListRawNews(ctx, filter)
}

func (s *Service) CountRawNews(ctx context.Context, filter RawNewsListFilter) (int, error) {
	return s.store.CountRawNews(ctx, filter)
}

func (s *Service) ListUnprocessedRawNews(ctx context.Context, before time.Time, limit int) ([]StockV2RawNews, error) {
	return s.store.ListUnprocessedRawNews(ctx, before, limit)
}

func rawNewsContentHash(source, sourceID, title, content, snippet, newsURL string, publishedAt time.Time) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(source),
		strings.TrimSpace(sourceID),
		strings.TrimSpace(title),
		strings.TrimSpace(content),
		strings.TrimSpace(snippet),
		strings.TrimSpace(newsURL),
		publishedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func rawNewsDedupeKey(source, sourceID, contentHash string) string {
	if sid := strings.TrimSpace(sourceID); sid != "" {
		return strings.TrimSpace(source) + ":" + sid
	}
	return strings.TrimSpace(source) + ":hash:" + contentHash
}

func newsStatusOrDefault(value string) string {
	switch strings.TrimSpace(value) {
	case NewsStatusProcessed, NewsStatusFailed, NewsStatusIgnored:
		return strings.TrimSpace(value)
	default:
		return NewsStatusNew
	}
}

func newsQualityOrDefault(value string) string {
	switch strings.TrimSpace(value) {
	case NewsQualityOK, NewsQualityLow:
		return strings.TrimSpace(value)
	default:
		return NewsQualityUnknown
	}
}
