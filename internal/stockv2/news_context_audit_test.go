package stockv2

import (
	"context"
	"errors"
	"testing"
)

func TestValidateNewsContextResearchAuditUsesSubmittedResultContract(t *testing.T) {
	svc := &Service{store: newTestStore(t)}
	report := NewsContextReport{
		ThreadChanges: []NewsContextThreadChange{{MaterialChange: true}},
		SearchAudit: []NewsContextSearchAudit{{
			Question: "核实订单", Status: "partially_verified", Sources: []string{"https://example.com/source"},
		}},
	}
	if err := svc.validateNewsContextResearchAudit(context.Background(), nil, report); !errors.Is(err, ErrInvalidNewsContextResult) {
		t.Fatalf("validate error = %v, want ErrInvalidNewsContextResult", err)
	}

	report.SearchAudit = []NewsContextSearchAudit{{
		Question: "核实订单", Status: "verified", Sources: []string{"https://example.com/source"},
		Unresolved: []string{"交付时间待确认"},
	}}
	if err := svc.validateNewsContextResearchAudit(context.Background(), nil, report); err != nil {
		t.Fatalf("validate corrected audit: %v", err)
	}
}
