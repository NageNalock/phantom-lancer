package stockv2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewsIngestDisabledAdapterDoesNotFail(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	enabled := false
	if _, err := svc.UpdateNewsSourceConfig(ctx, NewsSourceJin10, NewsSourceConfigPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("disable jin10 source: %v", err)
	}

	result, err := svc.RunNewsIngestJob(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("run disabled ingest: %v", err)
	}
	if result.Status != NewsSourceStatusDisabled {
		t.Fatalf("status = %q, want %q", result.Status, NewsSourceStatusDisabled)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, NewsSourceJin10)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !ok || state.Enabled || state.Status != NewsSourceStatusDisabled {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}
}

func TestNewsIngestDedupesAndAdvancesCursor(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	adapter := &fakeNewsAdapter{
		source: "mock_news",
		result: NewsSourceFetchResult{
			Items: []map[string]any{
				{"id": "flash-1", "title": "算力产业链消息"},
				{"id": "flash-1", "title": "重复消息"},
			},
			NextCursor: "cursor-2",
			FetchedAt:  time.Now(),
		},
	}
	svc.WithNewsSourceAdapter(adapter)

	result, err := svc.RunNewsIngestJob(ctx, adapter.source)
	if err != nil {
		t.Fatalf("run ingest: %v", err)
	}
	if result.FetchedCount != 2 || result.RawInsertedCount != 1 {
		t.Fatalf("result = %+v, want fetched=2 inserted=1", result)
	}
	if adapter.calls != 1 || len(adapter.cursors) != 1 || adapter.cursors[0].Cursor != "" {
		t.Fatalf("adapter calls=%d cursors=%+v", adapter.calls, adapter.cursors)
	}
	count, err := svc.CountRawNews(ctx, RawNewsListFilter{Source: adapter.source})
	if err != nil {
		t.Fatalf("count raw news: %v", err)
	}
	if count != 1 {
		t.Fatalf("raw count = %d, want 1", count)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, adapter.source)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !ok || state.Cursor != "cursor-2" || state.RawNewsCount != 1 {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}
}

func TestNewsIngestFailureBackoff(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	adapter := &fakeNewsAdapter{
		source: "mock_news",
		err:    errors.New("temporary upstream failure"),
	}
	svc.WithNewsSourceAdapter(adapter)

	result, err := svc.RunNewsIngestJob(ctx, adapter.source)
	if err == nil {
		t.Fatalf("run ingest err = nil, want failure")
	}
	if result.Status != NewsSourceStatusFailed {
		t.Fatalf("result status = %q, want failed", result.Status)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, adapter.source)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !ok || state.Status != NewsSourceStatusBackoff || state.ConsecutiveFailures != 1 || !state.BackoffUntil.After(time.Now()) {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}

	result, err = svc.RunNewsIngestJob(ctx, adapter.source)
	if err != nil {
		t.Fatalf("run backoff ingest: %v", err)
	}
	if result.Status != NewsSourceStatusBackoff || adapter.calls != 1 {
		t.Fatalf("result=%+v calls=%d, want backoff without second fetch", result, adapter.calls)
	}
}

func TestNewsSourceConfigSchedulesNextRun(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	adapter := &fakeNewsAdapter{
		source: "mock_sched",
		result: NewsSourceFetchResult{
			Items:     []map[string]any{{"id": "flash-sched", "title": "调度消息"}},
			FetchedAt: time.Now(),
		},
	}
	svc.WithNewsSourceAdapter(adapter)

	enabled := true
	interval := 120
	jitter := 0
	batch := 7
	process := 9
	overview, err := svc.UpdateNewsSourceConfig(ctx, adapter.source, NewsSourceConfigPatch{
		Enabled:             &enabled,
		PollIntervalSeconds: &interval,
		JitterSeconds:       &jitter,
		BatchLimit:          &batch,
		ProcessLimit:        &process,
	})
	if err != nil {
		t.Fatalf("update source config: %v", err)
	}
	if !overview.State.Enabled || overview.State.PollIntervalSeconds != interval || overview.State.BatchLimit != batch || overview.State.ProcessLimit != process {
		t.Fatalf("overview state = %+v", overview.State)
	}
	if overview.State.NextRunAt.IsZero() || time.Until(overview.State.NextRunAt) <= 0 {
		t.Fatalf("next run at = %v, want future", overview.State.NextRunAt)
	}

	result, err := svc.RunNewsPipelineOnce(ctx, adapter.source)
	if err != nil {
		t.Fatalf("run pipeline once: %v", err)
	}
	if result.RawInsertedCount != 1 {
		t.Fatalf("result = %+v, want one raw insert", result)
	}
	state, ok, err := svc.GetNewsSourceState(ctx, adapter.source)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !ok || state.LastRunStatus != NewsSourceStatusIdle || state.NextRunAt.IsZero() {
		t.Fatalf("state = %+v, ok=%v", state, ok)
	}
}

func TestNewsPipelineOnceSkipsConcurrentRun(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	adapter := &blockingNewsAdapter{
		source:  "mock_slow",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.WithNewsSourceAdapter(adapter)

	done := make(chan error, 1)
	go func() {
		_, err := svc.RunNewsPipelineOnce(ctx, adapter.source)
		done <- err
	}()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not start")
	}

	result, err := svc.RunNewsPipelineOnce(ctx, "another_source")
	if err != nil {
		t.Fatalf("concurrent pipeline err = %v", err)
	}
	if result.Status != NewsSourceStatusRateLimited || !strings.Contains(result.ErrorMessage, "already running") {
		t.Fatalf("concurrent result = %+v, want rate limited already-running", result)
	}

	close(adapter.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first pipeline err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first pipeline did not finish")
	}
}

func TestNewsSourceConfigClampsFastPolling(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	adapter := &fakeNewsAdapter{source: "mock_clamp"}
	svc.WithNewsSourceAdapter(adapter)

	enabled := true
	tooFast := 1
	overview, err := svc.UpdateNewsSourceConfig(ctx, adapter.source, NewsSourceConfigPatch{
		Enabled:             &enabled,
		PollIntervalSeconds: &tooFast,
		BackoffBaseSeconds:  &tooFast,
		BackoffMaxSeconds:   &tooFast,
	})
	if err != nil {
		t.Fatalf("update source config: %v", err)
	}
	if overview.State.PollIntervalSeconds != minNewsPollIntervalSeconds {
		t.Fatalf("poll interval = %d, want %d", overview.State.PollIntervalSeconds, minNewsPollIntervalSeconds)
	}
	if overview.State.BackoffBaseSeconds != minNewsBackoffBaseSeconds || overview.State.BackoffMaxSeconds != minNewsBackoffBaseSeconds {
		t.Fatalf("backoff = %d/%d, want %d", overview.State.BackoffBaseSeconds, overview.State.BackoffMaxSeconds, minNewsBackoffBaseSeconds)
	}
}

func TestNewsProcessingBatchCreatesEventsAndCandidates(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	const source = "mock_news"
	raw, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:   source,
		SourceID: "flash-2",
		Language: "zh-CN",
		Title:    "半导体设备公司披露订单变化",
		Snippet:  "消息提到半导体设备订单变化。",
	})
	if err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	svc.WithNewsEventLinker(NewsEventLinkerFunc(func(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error) {
		return []NewsLinkCandidate{{
			Symbol:       "688012",
			Market:       "SH",
			MatchMethod:  "keyword",
			Score:        0.7,
			Reason:       "命中半导体设备关键词",
			MatchedTerms: []string{"半导体设备"},
		}}, nil
	}))

	result, err := svc.RunNewsProcessingBatch(ctx, source, 10, 10)
	if err != nil {
		t.Fatalf("run processing batch: %v", err)
	}
	if result.NormalizedCount != 1 || result.LinkCandidateCount != 1 {
		t.Fatalf("result = %+v, want normalized=1 candidates=1", result)
	}
	processedRaw, err := svc.ListRawNews(ctx, RawNewsListFilter{Source: source, Status: NewsStatusProcessed})
	if err != nil {
		t.Fatalf("list processed raw news: %v", err)
	}
	if len(processedRaw) != 1 || processedRaw[0].ID != raw.ID {
		t.Fatalf("processed raw = %+v", processedRaw)
	}
	candidates, err := svc.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{Symbol: "688012"})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	event, err := svc.store.GetNewsEvent(ctx, candidates[0].NewsEventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if event.RawNewsID != raw.ID || event.LinkStatus != NewsEventLinkStatusLinked {
		t.Fatalf("event = %+v", event)
	}
}

func TestNewsProcessingBatchSourceFilterDoesNotStarve(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	other, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "other_news",
		Title:   "其他来源的旧消息",
		EventAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create other event: %v", err)
	}
	target, err := svc.CreateNewsEvent(ctx, NewsEvent{
		Source:  "target_news",
		Title:   "目标来源消息",
		EventAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create target event: %v", err)
	}

	result, err := svc.RunNewsProcessingBatch(ctx, "target_news", 1, 1)
	if err != nil {
		t.Fatalf("run processing batch: %v", err)
	}
	if result.NormalizedCount != 0 || result.LinkCandidateCount != 0 {
		t.Fatalf("result = %+v, want only target no-candidate processing", result)
	}
	gotTarget, err := svc.store.GetNewsEvent(ctx, target.ID)
	if err != nil {
		t.Fatalf("get target event: %v", err)
	}
	if gotTarget.LinkStatus != NewsEventLinkStatusNoCandidate {
		t.Fatalf("target link status = %q, want no_candidate", gotTarget.LinkStatus)
	}
	gotOther, err := svc.store.GetNewsEvent(ctx, other.ID)
	if err != nil {
		t.Fatalf("get other event: %v", err)
	}
	if gotOther.LinkStatus != NewsEventLinkStatusPending {
		t.Fatalf("other link status = %q, want pending", gotOther.LinkStatus)
	}
}

func TestNewsProcessingBatchLogsLinkFailureContext(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	var logs bytes.Buffer
	svc.log = slog.New(slog.NewTextHandler(&logs, nil))
	ctx := context.Background()
	const source = "mock_news"
	event, err := svc.CreateNewsEvent(ctx, NewsEvent{
		RawNewsID: "raw-log-context",
		Source:    source,
		Title:     "需要链接的消息",
		EventAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	svc.WithNewsEventLinker(NewsEventLinkerFunc(func(context.Context, NewsEvent) ([]NewsLinkCandidate, error) {
		return nil, errors.New("link backend unavailable")
	}))

	result, err := svc.RunNewsProcessingBatch(ctx, source, 0, 10)
	if err != nil {
		t.Fatalf("run processing batch: %v", err)
	}
	if result.LinkCandidateCount != 0 {
		t.Fatalf("result = %+v, want no candidates", result)
	}
	got, err := svc.store.GetNewsEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if got.LinkStatus != NewsEventLinkStatusFailed {
		t.Fatalf("link status = %q, want failed", got.LinkStatus)
	}
	text := logs.String()
	for _, want := range []string{
		"stockv2 news event link failed",
		"source=mock_news",
		"news_event_id=" + event.ID,
		"raw_news_id=raw-log-context",
		"linker=custom",
		"link backend unavailable",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log %q does not contain %q", text, want)
		}
	}
}

type fakeNewsAdapter struct {
	source  string
	result  NewsSourceFetchResult
	err     error
	calls   int
	cursors []NewsSourceCursor
}

func (a *fakeNewsAdapter) SourceName() string {
	return a.source
}

func (a *fakeNewsAdapter) FetchSince(_ context.Context, cursor NewsSourceCursor) (NewsSourceFetchResult, error) {
	a.calls++
	a.cursors = append(a.cursors, cursor)
	return a.result, a.err
}

type blockingNewsAdapter struct {
	source  string
	started chan struct{}
	release chan struct{}
}

func (a *blockingNewsAdapter) SourceName() string {
	return a.source
}

func (a *blockingNewsAdapter) FetchSince(ctx context.Context, _ NewsSourceCursor) (NewsSourceFetchResult, error) {
	close(a.started)
	select {
	case <-ctx.Done():
		return NewsSourceFetchResult{}, ctx.Err()
	case <-a.release:
		return NewsSourceFetchResult{FetchedAt: time.Now()}, nil
	}
}

func (a *blockingNewsAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	return RequestCreateRawNews{Source: a.source, SourceID: fmt.Sprint(payload["id"]), Title: fmt.Sprint(payload["title"])}, nil
}

func (a *fakeNewsAdapter) NormalizeRawPayload(payload map[string]any) (RequestCreateRawNews, error) {
	title := fmt.Sprint(payload["title"])
	sourceID := fmt.Sprint(payload["id"])
	if title == "" || title == "<nil>" {
		return RequestCreateRawNews{}, ErrInvalidRawNewsContent
	}
	if sourceID == "<nil>" {
		sourceID = ""
	}
	return RequestCreateRawNews{
		Source:     a.source,
		SourceID:   sourceID,
		Language:   "zh-CN",
		Title:      title,
		RawPayload: payload,
	}, nil
}
