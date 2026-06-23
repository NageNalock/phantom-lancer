package stockv2

import (
	"strings"
	"testing"
)

func TestBuildCodexExecArgsUsesReadOnlyAndSkipsRepoCheck(t *testing.T) {
	args := buildCodexExecArgs("id-202606222", "submit debug result")
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		"exec",
		"--json",
		"--sandbox\x00read-only",
		"--skip-git-repo-check",
		"--model\x00id-202606222",
		"submit debug result",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestBuildOperationReviewPromptDocumentsReviewContract(t *testing.T) {
	prompt := buildOperationReviewPrompt("task-review-123", AgentContextPack{
		Hit: MonitorHit{
			Title:   "价格突破观察线",
			Summary: "监控命中候选",
			Symbol:  "000977",
			Status:  MonitorHitStatusCandidate,
		},
		Evidence: map[string]any{
			"matchedAction":    "add_position",
			"matchedPrefilter": "breakout",
			"playbookRule":     map[string]any{"id": "breakout"},
		},
		Freshness: map[string]any{
			"quote": map[string]any{"status": QuoteStatusFresh},
		},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	mustContain := []string{
		"task-review-123",
		"stock_agent.submit_result",
		"MCP Server",
		"monitoring-hit reviewer",
		"Evidence audit",
		"Data freshness audit",
		"matched, degraded, skipped, or noise",
		"facts",
		"inferences",
		"assumptions",
		"trade_signal",
		"proposed_operation",
		"strategy_patch",
		"ignore",
		"continue_monitoring",
		"Do not fabricate",
		"guardrails",
	}
	for _, want := range mustContain {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
