package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateNewsContextResearchAuditUsesSubmittedResultContract(t *testing.T) {
	svc := &Service{store: newTestStore(t)}
	report := NewsContextReport{
		ThreadChanges: []NewsContextThreadChange{{MaterialChange: true}},
		SearchAudit: []NewsContextSearchAudit{{
			Question: "核实订单", Status: "partially_verified", Sources: []string{"https://example.com/source"},
		}},
	}
	if err := svc.validateNewsContextResearchAudit(context.Background(), NewsContextRun{}, nil, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("validate error = %v, want ErrInvalidNewsContextResult", err)
	}

	report.SearchAudit = []NewsContextSearchAudit{{
		Question: "核实订单", Status: "verified", Sources: []string{"https://example.com/source"},
		Unresolved: []string{"交付时间待确认"},
	}}
	if err := svc.validateNewsContextResearchAudit(context.Background(), NewsContextRun{}, nil, report); err != nil {
		t.Fatalf("validate corrected audit: %v", err)
	}
}

func TestValidateNewsContextResearchAuditSkipsPublicResearchDuringHistoricalReconstruction(t *testing.T) {
	svc := &Service{store: newTestStore(t)}
	ctx := context.Background()
	end := time.Now().Truncate(time.Hour)
	run, err := svc.store.CreateNewsContextRun(ctx, NewsContextRun{
		WindowType: NewsContextWindowFourHour, TriggerType: NewsContextTriggerBackfill,
		Status: NewsContextRunStatusRunning, Phase: newsContextRunPhaseAggregating,
		WindowStart: end.Add(-4 * time.Hour), WindowEnd: end,
		ReviewStatus: NewsContextReviewNotRequired, CleanupStatus: NewsContextCleanupPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	backfill, err := svc.store.CreateNewsContextBackfill(ctx, NewsContextBackfill{
		Status: NewsContextBackfillStatusRunning, Phase: "four_hour",
		RangeStartAt: run.WindowStart, CutoffAt: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.store.LinkNewsContextBackfillRun(ctx, backfill.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	report := NewsContextReport{ThreadChanges: []NewsContextThreadChange{{
		MaterialChange: true, CounterEvidence: []string{"一遍归纳已保留的反证"},
	}}}
	if err := svc.validateNewsContextResearchAudit(ctx, run, nil, report); err != nil {
		t.Fatalf("validate historical reconstruction without fresh audit: %v", err)
	}

	report.SearchAudit = []NewsContextSearchAudit{{
		Question: "不应重新核实", Status: "verified", Sources: []string{"https://example.com/source"},
	}}
	if err := svc.validateNewsContextResearchAudit(ctx, run, nil, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("validate historical audit error = %v, want ErrInvalidNewsContextResult", err)
	}
}
