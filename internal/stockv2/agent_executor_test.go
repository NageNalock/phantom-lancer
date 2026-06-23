package stockv2

import (
	"context"
	"strings"
	"testing"
)

func TestBuildCodexExecArgsUsesReadOnlyAndSkipsRepoCheck(t *testing.T) {
	args := buildCodexExecArgs("id-202606222", "submit debug result", []codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		RequiredTools: []string{codexSubmitResultTool},
	}})
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		"exec",
		"--json",
		"--sandbox\x00read-only",
		"--skip-git-repo-check",
		"-c\x00mcp_servers.stock_agent.url=\"http://127.0.0.1:8080/api/stockv2/agent/mcp\"",
		"--model\x00id-202606222",
		"submit debug result",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestPreflightCodexMCPServersChecksSubmitTool(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	executor := &codexCLIExecutor{
		taskPool: pool,
		mcpURL:   "http://127.0.0.1:8080/api/stockv2/agent/mcp",
	}

	if err := executor.preflightCodexMCPServers(executor.codexMCPServers()); err != nil {
		t.Fatalf("preflight: %v", err)
	}
}

func TestPreflightCodexMCPServersRejectsMissingTool(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	executor := &codexCLIExecutor{taskPool: pool}

	err := executor.preflightCodexMCPServers([]codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		RequiredTools: []string{"stock_agent.missing_tool"},
	}})
	if err == nil || !strings.Contains(err.Error(), "required tool missing") {
		t.Fatalf("err = %v, want missing tool", err)
	}
}

func TestPreflightCodexMCPServersRejectsInvalidURL(t *testing.T) {
	executor := &codexCLIExecutor{taskPool: newAgentTaskPool(defaultCleanupInterval)}
	defer executor.taskPool.Close()

	err := executor.preflightCodexMCPServers([]codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           "127.0.0.1:8080/api/stockv2/agent/mcp",
		RequiredTools: []string{codexSubmitResultTool},
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid codex MCP server URL") {
		t.Fatalf("err = %v, want invalid URL", err)
	}
}

func TestExecutePromptFailsBeforeCodexWhenMCPMissing(t *testing.T) {
	executor := &codexCLIExecutor{
		binary:   "definitely-not-started",
		taskPool: newAgentTaskPool(defaultCleanupInterval),
	}
	defer executor.taskPool.Close()

	output, err := executor.executePrompt(context.Background(), "task-1", "prompt", "model")
	if output != nil {
		t.Fatalf("output = %+v, want nil", output)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid codex MCP server URL") {
		t.Fatalf("err = %v, want MCP preflight URL error", err)
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
		"MCP Server Name",
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
