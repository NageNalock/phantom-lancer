package stockv2

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildCodexExecArgsUsesFullAccessAndSkipsRepoCheck(t *testing.T) {
	args := buildCodexExecArgs("id-202606222", "submit debug result", []codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		RequiredTools: []string{codexSubmitResultTool},
	}})
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		"exec",
		"--json",
		"--ignore-user-config",
		"--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check",
		"-c\x00mcp_servers={}",
		"-c\x00mcp_servers.stock_agent.url=\"http://127.0.0.1:8080/api/stockv2/agent/mcp\"",
		"--model\x00id-202606222",
		"submit debug result",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
	if strings.Contains(got, "--sandbox") {
		t.Fatalf("stockv2 agent args should bypass sandbox, got %#v", args)
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

func TestSuppressCodexStderrLineOnlyDropsKnownExternalMCPNoise(t *testing.T) {
	notionLine := []byte(`ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { resource_metadata="https://mcp.notion.com/.well-known/oauth-protected-resource/mcp" })`)
	if !suppressCodexStderrLine(notionLine) {
		t.Fatal("expected Notion auth worker noise to be suppressed")
	}
	stockLine := []byte(`ERROR codex_core::tools::router: stock_agent submit_result failed`)
	if suppressCodexStderrLine(stockLine) {
		t.Fatal("stock_agent errors must remain visible")
	}
}

func TestExecutePromptReturnsAfterResultAndCleanProcessExit(t *testing.T) {
	script := t.TempDir() + "/fake-codex"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'fake stdout\\n'\nsleep 0.05\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	executor := &codexCLIExecutor{
		binary:   script,
		taskPool: pool,
		mcpURL:   "http://127.0.0.1:8080/api/stockv2/agent/mcp",
	}
	taskID, _ := pool.createTask(AgentTaskTypeOperationReview, "run-clean-exit", "", time.Minute)
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = pool.submitResult(taskID, AgentTaskTypeOperationReview, AgentTaskSubmittedResult{
			OutputType:    OperationReviewOutputContinueMonitoring,
			ResultSummary: "debug ok",
			Result:        map[string]any{"debug": true},
			Confidence:    1,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct {
		output *AgentExecutorOutput
		err    error
	}, 1)
	go func() {
		output, err := executor.executePrompt(ctx, taskID, "prompt", "model")
		done <- struct {
			output *AgentExecutorOutput
			err    error
		}{output: output, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("execute prompt: %v", got.err)
		}
		if got.output == nil || got.output.ExitCode != 0 || !strings.Contains(got.output.StdoutTail, "fake stdout") {
			t.Fatalf("output = %+v, want clean stdout exit", got.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executePrompt did not return after MCP result and clean process exit")
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
