package stockv2

import (
	"context"
	"testing"
	"time"
)

func TestPrefetchNewsContextCandidatesHandlesEmptyThemeHistoryWithoutTools(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()

	pack := newAgentAPIExecutor(svc).prefetchNewsContextCandidates(context.Background(), NewsContextAggregationPack{
		WindowEnd:       time.Now(),
		InputNewsEvents: []NewsEvent{{ID: "news-1", Title: "新主题"}},
	})
	if pack.CandidateLookupStatus != "no_existing_threads" {
		t.Fatalf("candidate lookup status = %q", pack.CandidateLookupStatus)
	}
	if len(pack.CandidateThreads) != 0 {
		t.Fatalf("candidate threads = %#v", pack.CandidateThreads)
	}
}

func TestMergeNewsContextCandidatesDeduplicatesAndBoundsPromptInput(t *testing.T) {
	candidates := make(map[string]NewsContextPromptThread)
	mergeNewsContextCandidates(candidates, "news-1", []SemanticNewsThreadResult{
		{Score: 0.7, Thread: NewsThread{ID: "thread-1", ThemeID: "thread-1", Title: "主题一"}},
		{Score: 0.9, Thread: NewsThread{ID: "thread-2", ThemeID: "thread-2", Title: "主题二"}},
	})
	mergeNewsContextCandidates(candidates, "news-2", []SemanticNewsThreadResult{
		{Score: 0.8, Thread: NewsThread{ID: "thread-1", ThemeID: "thread-1", Title: "主题一"}},
		{Score: 0.6, Thread: NewsThread{ID: "thread-3", ThemeID: "thread-3", Title: "主题三"}},
	})

	got := sortedNewsContextCandidates(candidates, 2)
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(got))
	}
	if got[0].ID != "thread-2" || got[0].RetrievalScore != 0.9 {
		t.Fatalf("first candidate = %+v", got[0])
	}
	if got[1].ID != "thread-1" || got[1].RetrievalScore != 0.8 {
		t.Fatalf("second candidate = %+v", got[1])
	}
	if len(got[1].MatchedNewsEventIDs) != 2 ||
		got[1].MatchedNewsEventIDs[0] != "news-1" ||
		got[1].MatchedNewsEventIDs[1] != "news-2" {
		t.Fatalf("matched news ids = %#v", got[1].MatchedNewsEventIDs)
	}
}

func TestNewsContextCandidateQueryExcludesFullArticleContent(t *testing.T) {
	query := newsContextCandidateQuery(NewsEvent{
		Title:   "标题",
		Summary: "摘要",
		Content: "不应进入向量召回查询的完整正文",
	})
	if query != "标题\n摘要" {
		t.Fatalf("candidate query = %q", query)
	}
}
