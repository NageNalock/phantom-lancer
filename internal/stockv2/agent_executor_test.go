package stockv2

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestExecutePromptCapturesFastExitStderr(t *testing.T) {
	script := t.TempDir() + "/fake-codex"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'unknown model gpt-5.5\\nFor more information, try --help.\\n' >&2\nexit 2\n"), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	executor := &codexCLIExecutor{
		binary:   script,
		taskPool: pool,
		mcpURL:   "http://127.0.0.1:8080/api/stockv2/agent/mcp",
	}
	taskID, _ := pool.createTask(AgentTaskTypeStockProfileSummary, "run-fast-exit", "", time.Minute)

	output, err := executor.executePrompt(context.Background(), taskID, "prompt", "gpt-5.5")
	if err == nil || !strings.Contains(err.Error(), "process exited (code 2)") {
		t.Fatalf("err = %v, want code 2 without result", err)
	}
	if output == nil {
		t.Fatal("output nil, want captured command and stderr")
	}
	if output.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2", output.ExitCode)
	}
	if !strings.Contains(output.StderrTail, "unknown model gpt-5.5") || !strings.Contains(output.StderrTail, "try --help") {
		t.Fatalf("stderr tail = %q, want fast cli usage error", output.StderrTail)
	}
}

func TestBuildStockProfileSummaryPromptTruncatesUTF8Safely(t *testing.T) {
	profile := StockProfile{
		Symbol:            "000815",
		Market:            "SZ",
		InstrumentType:    InstrumentTypeStock,
		Name:              "美利云",
		BusinessSummaryZh: strings.Repeat("中冶美利云产业投资股份有限公司推进云计算数据中心与造纸业务协同。", 180),
		ProfileTextZh:     strings.Repeat("绿色能源 数据中心 造纸 央企 混改 ", 220),
	}

	prompt := buildStockProfileSummaryPrompt("task-profile", profile, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	if !utf8.ValidString(prompt) {
		t.Fatalf("prompt is invalid utf8")
	}
	if !strings.Contains(prompt, "... [truncated]") {
		t.Fatalf("prompt was not truncated")
	}
}

func TestBuildNewsContextAggregationPromptEnforcesCoverageAndResearch(t *testing.T) {
	prompt := buildNewsContextAggregationPrompt("task-news-context", NewsContextAggregationPack{
		RunID:      "context-run-1",
		WindowType: "daily",
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"task-news-context",
		"context-run-1",
		"news_context_result",
		"processed_news_ids",
		"reviewed_thread_ids",
		"unchanged_thread_ids",
		"every InputThreads item",
		"daily batch must produce a stage conclusion",
		"stock_agent.semantic_search_news_threads",
		"stock_agent.get_news_thread",
		"Public verification is mandatory",
		"every ResearchReasons item",
		"stock_agent.submit_result",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if !validAgentTaskOutputType(AgentTaskTypeNewsEventReview, "news_context_result") {
		t.Fatal("news context result must be accepted for news_event_review")
	}
	if validAgentTaskOutputType(AgentTaskTypeNewsEventReview, AgentTaskTypeNewsEventReview) {
		t.Fatal("legacy task key must not be accepted as a result type")
	}
}

func TestBuildPortfolioSentinelPromptRequiresCompleteNewsContextReview(t *testing.T) {
	prompt := buildPortfolioSentinelPrompt("task-sentinel", PortfolioSentinelContext{
		NewsContext: &PortfolioSentinelNewsContext{
			RunID:              "context-run-1",
			ChangedThreadCount: 123,
		},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"stock_agent.list_news_context_changes",
		"context-run-1",
		"all 123 changed threads",
		"do not stop after the first page",
		"checked_news_thread_version_ids",
		"complete, duplicate-free versionId set",
		"stock_agent.semantic_search_news_threads",
		"adjacent or related threads",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestTruncatePromptUTF8KeepsValidUTF8AtByteBoundary(t *testing.T) {
	value := strings.Repeat("中", 3000) + strings.Repeat("尾", 1000)
	got := truncatePromptUTF8(value, 6000, 2000)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated prompt is invalid utf8")
	}
	if !strings.Contains(got, "... [truncated]") {
		t.Fatalf("truncated marker missing")
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct {
		output *AgentExecutorOutput
		err    error
	}, 1)
	rawPrompt := "raw prompt body"
	go func() {
		output, err := executor.executePrompt(ctx, taskID, rawPrompt, "model")
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
		if !strings.Contains(got.output.Command, "fake-codex exec") || strings.Contains(got.output.Command, rawPrompt) {
			t.Fatalf("command summary = %q, want binary and redacted prompt", got.output.Command)
		}
		if got.output.Prompt != rawPrompt {
			t.Fatalf("prompt = %q, want raw prompt for service-layer ledger redaction", got.output.Prompt)
		}
	case <-time.After(4 * time.Second):
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

func TestBuildOpportunityDiscoveryPromptRequiresEmbeddingSemanticRecall(t *testing.T) {
	prompt := buildOpportunityDiscoveryPrompt("task-opp", OpportunityDiscoveryContext{
		BuiltAt: time.Now(),
		Opportunity: Opportunity{
			ID:         "opp-1",
			Title:      "AI 端侧机会",
			UserThesis: "端侧模型带动算力链",
		},
		DiscoveryRun: OpportunityDiscoveryRun{ID: "run-1", OpportunityID: "opp-1"},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"stock_agent.get_embedding_status",
		"stock_agent.semantic_search_stock_profiles",
		"stock_agent.semantic_search_news_events",
		"embedding_status_check",
		"Do not silently fall back",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildStrategyGenerationStepPromptEncouragesInternalAndExternalSearch(t *testing.T) {
	prompt := buildStrategyGenerationStepPrompt("task-strategy-step", StrategyGenerationStepPack{
		RunID:     "run-1",
		StepKey:   StrategyGenerationStepEvidenceCollector,
		Role:      StrategyGenerationStepEvidenceCollector,
		Objective: "Collect evidence",
		Instructions: []string{
			"Call project MCP tools and Codex CLI external public search/browse as equal-priority evidence channels.",
		},
		Context: StrategyGenerationContext{
			BuiltAt: time.Now(),
			Input: StrategyGenerationInput{
				Mode:              StrategyGenerationModeSingleInstrument,
				TargetInstruments: []StrategyGenerationTargetInstrument{{Symbol: "302132"}},
			},
		},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"Treat internal project search and external public search as equal-priority evidence channels",
		"Codex CLI's own public search/browse capability",
		"stock_agent.get_embedding_status",
		"stock_agent.semantic_search_stock_profiles",
		"stock_agent.semantic_search_news_events",
		"Do not implement or request web_search/web_fetch MCP tools",
		"If external search/browse is unavailable",
		"Never fabricate prices, news, filings, sources, or citations",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildOperationReviewPromptIncludesDebugGoogleNewsSearchCheck(t *testing.T) {
	prompt := buildOperationReviewPrompt("task-debug", AgentContextPack{
		Hit: MonitorHit{
			Title:    "Agent CLI debug self check with Google News search",
			TaskType: "agent_cli_debug",
			Status:   MonitorHitStatusCandidate,
			Evidence: map[string]any{
				"googleNewsDate":           "2026-06-26",
				"googleNewsSearchRequired": true,
				"requiredResultField":      "googleNewsTodayZh",
			},
		},
		Evidence: map[string]any{
			"googleNewsDate":           "2026-06-26",
			"googleNewsSearchRequired": true,
			"requiredResultField":      "googleNewsTodayZh",
		},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"CLI Debug Search Check",
		"Google News headlines",
		"2026-06-26",
		"web/search MCP",
		"Return all human-readable text in Chinese",
		"googleNewsTodayZh",
		"googleNewsSearchStatus",
		"searchAudit",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
