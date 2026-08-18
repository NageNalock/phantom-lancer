package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBuildCodexExecArgsUsesFullAccessAndSkipsRepoCheck(t *testing.T) {
	args := buildCodexExecArgs("id-202606222", "high", "submit debug result", []codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		RequiredTools: []string{codexSubmitResultTool},
	}})
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		"exec",
		"--json",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--dangerously-bypass-approvals-and-sandbox",
		"--skip-git-repo-check",
		"-c\x00mcp_servers={}",
		"-c\x00mcp_servers.stock_agent.url=\"http://127.0.0.1:8080/api/stockv2/agent/mcp\"",
		"-c\x00model_reasoning_effort=\"high\"",
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

func TestCustomCodexCLIProviderUsesIsolatedHomeAndWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	base := newCodexCLIExecutor(
		nil,
		"codex",
		codexHome,
		"http://127.0.0.1:8080/api/stockv2/agent/mcp",
		dataDir,
		newAgentTaskPool(defaultCleanupInterval),
	)
	defer base.taskPool.Close()
	provider := AgentProviderProfile{
		ID:           "provider-1",
		ProviderType: AgentProviderTypeCodexCLI,
		Metadata: mergeAgentProviderRuntimeMetadata(
			nil,
			"https://ark.cn-beijing.volces.com/api/coding/v3",
			"provider-secret",
		),
	}
	custom, err := base.forProvider(provider, "http://127.0.0.1:8080/api/stockv2/agent/codex-proxy/provider-1")
	if err != nil {
		t.Fatalf("custom executor: %v", err)
	}
	home, workDir, cleanup, err := custom.prepareCodexRunPaths()
	if err != nil {
		t.Fatalf("prepare custom run paths: %v", err)
	}
	wantRoot := filepath.Join(dataDir, "stockv2", "codex-home")
	if filepath.Dir(home) != wantRoot || workDir != filepath.Join(home, "workspace") {
		t.Fatalf("custom paths = home %q, workdir %q", home, workDir)
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		t.Fatalf("custom workspace was not created: %v", err)
	}
	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("custom task home was not removed: %v", err)
	}
	if base.codexHome != codexHome || base.provider != nil {
		t.Fatalf("base executor mutated: %#v", base)
	}
}

func TestBuildCodexExecArgsOmitsEmptyReasoningEffort(t *testing.T) {
	args := buildCodexExecArgs("gpt-5.5", "", "prompt", nil)
	if got := strings.Join(args, "\x00"); strings.Contains(got, "model_reasoning_effort") {
		t.Fatalf("empty reasoning effort must not change Codex CLI args: %#v", args)
	}
}

func TestBuildCodexExecArgsPlacesLiveSearchBeforeExec(t *testing.T) {
	args := buildCodexExecArgs("gpt-5.5", "medium", "prompt", nil, codexExecOptions{NativeSearch: true})
	if len(args) < 2 || args[0] != "--search" || args[1] != "exec" {
		t.Fatalf("live search args = %#v, want --search before exec", args)
	}
}

func TestBuildCodexExecArgsUsesTaskProviderAndSearchMCPWithoutNativeSearch(t *testing.T) {
	args := buildCodexExecArgs(
		"ark-code-latest",
		"medium",
		"prompt",
		[]codexMCPServerCapability{{
			Name:                codexSearchMCPName,
			Command:             "/opt/search/bin/duckduckgo-mcp-server",
			Args:                []string{"--search-backend", "curl"},
			DefaultApprovalMode: "approve",
			RequiredTools:       []string{"search"},
			DisabledTools:       []string{"unused_tool"},
		}},
		codexExecOptions{
			ModelProvider: &codexCLIProviderRuntime{
				BaseURL: "http://127.0.0.1:1234/api/stockv2/agent/codex-proxy/provider-1",
			},
			OutputSchemaPath:      "/tmp/output-schema.json",
			OutputLastMessagePath: "/tmp/last-message.json",
		},
	)
	got := strings.Join(args, "\x00")
	for _, want := range []string{
		`mcp_servers.ddg.command="/opt/search/bin/duckduckgo-mcp-server"`,
		`mcp_servers.ddg.args=["--search-backend","curl"]`,
		`mcp_servers.ddg.default_tools_approval_mode="approve"`,
		`mcp_servers.ddg.disabled_tools=["unused_tool"]`,
		`model_provider="stockv2_task_provider"`,
		`model_providers.stockv2_task_provider.base_url="http://127.0.0.1:1234/api/stockv2/agent/codex-proxy/provider-1"`,
		`model_providers.stockv2_task_provider.wire_api="responses"`,
		`model_providers.stockv2_task_provider.requires_openai_auth=false`,
		"--output-schema\x00/tmp/output-schema.json",
		"--output-last-message\x00/tmp/last-message.json",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("custom Provider args missing %q: %#v", want, args)
		}
	}
	for _, arg := range args {
		if arg == "--search" {
			t.Fatalf("custom Provider args contain native search: %#v", args)
		}
	}
	if strings.Contains(strings.ToLower(got), "api_key") {
		t.Fatalf("custom Provider args contain a key: %#v", args)
	}
}

func TestSubmitCodexDirectResultUsesExistingSubmissionValidation(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	taskID, _ := pool.createTask(AgentTaskTypeOperationReview, "run-1", "", time.Minute)
	body := "```json\n" + `{
		"taskID":"` + taskID + `",
		"taskType":"operation_review",
		"result":{
			"outputType":"continue_monitoring",
			"resultSummary":"continue",
			"result":{"reason":"verified"},
			"confidence":0.8
		}
	}` + "\n```"
	executor := &codexCLIExecutor{taskPool: pool}
	result, err := executor.submitCodexDirectResult([]byte(body), taskID, AgentTaskTypeOperationReview)
	if err != nil {
		t.Fatalf("submit direct result: %v", err)
	}
	if result.OutputType != OperationReviewOutputContinueMonitoring ||
		result.ResultSummary != "continue" ||
		result.Result["reason"] != "verified" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSubmitCodexDirectResultRejectsTaskIdentityMismatch(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	taskID, _ := pool.createTask(AgentTaskTypeOperationReview, "run-1", "", time.Minute)
	body := `{"taskID":"wrong-task","taskType":"operation_review","result":{"outputType":"continue_monitoring","resultSummary":"continue","result":{},"confidence":0.8}}`
	executor := &codexCLIExecutor{taskPool: pool}
	if _, err := executor.submitCodexDirectResult([]byte(body), taskID, AgentTaskTypeOperationReview); err == nil ||
		!strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("err = %v, want identity mismatch", err)
	}
}

func TestCLIResearchAuditCollectorKeepsOnlyBoundedCallMetadata(t *testing.T) {
	audit := newCLIResearchAudit(true)
	for _, line := range []string{
		`{"type":"item.completed","item":{"type":"web_search","query":"must not persist"}}`,
		`{"type":"item.completed","item":{"type":"mcp_tool_call","server":"stock_agent","tool":"semantic_search_news_threads","arguments":{"secret":"must not persist"}}}`,
		`{"type":"item.completed","item":{"type":"dynamic_tool_call","name":"research_agent","arguments":"must not persist"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"{\"final\":true}"}}`,
	} {
		audit.record([]byte(line))
	}
	got := audit.snapshot()
	if !got.LiveSearchEnabled || got.WebSearchCount != 1 {
		t.Fatalf("audit = %+v, want one enabled web search", got)
	}
	if got.MCPToolCalls["stock_agent.semantic_search_news_threads"] != 1 || got.AgentToolCalls["research_agent"] != 1 {
		t.Fatalf("audit calls = %+v / %+v", got.MCPToolCalls, got.AgentToolCalls)
	}
	if !portfolioSentinelHasExternalResearch(AgentCLIResearchAudit{AgentToolCalls: map[string]int{"research_agent": 1}}) {
		t.Fatal("named research agent should satisfy external research audit")
	}
	raw := agentExecutorOutputSummary(&AgentExecutorOutput{ResearchAudit: got})
	if strings.Contains(raw, "must not persist") {
		t.Fatalf("audit leaked query or arguments: %s", raw)
	}
}

func TestReadCodexDirectResultFileUsesOnlyBoundedLastMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last-message.json")
	want := []byte(`{"taskID":"task-1","result":{}}`)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("write last message: %v", err)
	}
	got, err := readCodexDirectResultFile(path)
	if err != nil {
		t.Fatalf("read last message: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("last message = %q, want %q", got, want)
	}
}

func TestCustomProviderPortfolioSentinelUsesSchemaAndLastMessageFiles(t *testing.T) {
	root := t.TempDir()
	resultPath := filepath.Join(root, "result.json")
	capturedSchema := filepath.Join(root, "captured-schema.json")
	capturedArgs := filepath.Join(root, "captured-args.txt")
	script := filepath.Join(root, "fake-codex")
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	taskID, _ := pool.createTask(AgentTaskTypePortfolioSentinel, "run-1", "", time.Minute)
	result, err := json.Marshal(map[string]any{
		"taskID":   taskID,
		"taskType": AgentTaskTypePortfolioSentinel,
		"result": map[string]any{
			"outputType":    PortfolioSentinelOutputType,
			"resultSummary": "structured result",
			"confidence":    0.8,
			"result": map[string]any{
				"schema_version":                  PortfolioSentinelReportSchemaVersion,
				"overall_risk_level":              PortfolioSentinelRiskLow,
				"run_summary":                     "complete",
				"positive_items":                  []any{},
				"negative_items":                  []any{},
				"noise_items":                     []any{},
				"affected_holdings":               []any{},
				"action_plans":                    []any{},
				"research_audit":                  []any{},
				"review_requests":                 []any{},
				"data_quality_notes":              []any{},
				"next_watch_focus":                []any{},
				"checked_news_thread_version_ids": []any{},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if err := os.WriteFile(resultPath, result, 0o600); err != nil {
		t.Fatalf("write result: %v", err)
	}
	scriptBody := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + strconv.Quote(capturedArgs) + "\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    --output-schema) schema=\"$2\"; shift 2 ;;\n" +
		"    --output-last-message) last=\"$2\"; shift 2 ;;\n" +
		"    *) shift ;;\n" +
		"  esac\n" +
		"done\n" +
		"test -s \"$schema\"\n" +
		"cp \"$schema\" " + strconv.Quote(capturedSchema) + "\n" +
		"cp " + strconv.Quote(resultPath) + " \"$last\"\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	isolatedRoot := filepath.Join(root, "codex-home")
	if err := os.MkdirAll(isolatedRoot, 0o700); err != nil {
		t.Fatalf("prepare isolated root: %v", err)
	}
	executor := &codexCLIExecutor{
		binary:            script,
		taskPool:          pool,
		mcpURL:            "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		searchMCPCommand:  "/bin/true",
		isolatedCodexRoot: isolatedRoot,
		provider:          &codexCLIProviderRuntime{BaseURL: "http://127.0.0.1:8080/api/stockv2/agent/codex-proxy/provider-1"},
	}
	output, err := executor.ExecutePortfolioSentinel(context.Background(), taskID, PortfolioSentinelContext{}, "deepseek-v4-flash", "medium")
	if err != nil {
		t.Fatalf("execute sentinel: %v, output: %+v", err, output)
	}
	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	for _, flag := range []string{"--output-schema", "--output-last-message"} {
		if !strings.Contains(string(args), flag) {
			t.Fatalf("args missing %s: %s", flag, args)
		}
	}
	schema, err := os.ReadFile(capturedSchema)
	if err != nil {
		t.Fatalf("read captured schema: %v", err)
	}
	if !strings.Contains(string(schema), `"const":"`+taskID+`"`) || !strings.Contains(string(schema), `"action_plans"`) {
		t.Fatalf("captured schema does not bind task and action plans: %s", schema)
	}
}

func TestCustomProviderUsesBoundedAgentMessageWhenLastMessageIsMalformed(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "final-event.jsonl")
	script := filepath.Join(root, "fake-codex")
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	taskID, _ := pool.createTask(AgentTaskTypeOperationReview, "run-1", "", time.Minute)
	reason := strings.Repeat("verified-", 1024)
	result := `{"taskID":"` + taskID + `","taskType":"operation_review","result":` +
		`{"outputType":"continue_monitoring","resultSummary":"continue","result":{"reason":` + strconv.Quote(reason) + `},"confidence":0.8}}`
	event, err := json.Marshal(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": result},
	})
	if err != nil {
		t.Fatalf("encode final event: %v", err)
	}
	if err := os.WriteFile(eventPath, append(event, '\n'), 0o600); err != nil {
		t.Fatalf("write final event: %v", err)
	}
	scriptBody := "#!/bin/sh\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    --output-last-message) last=\"$2\"; shift 2 ;;\n" +
		"    *) shift ;;\n" +
		"  esac\n" +
		"done\n" +
		"printf '%s\\n' 'Answer: malformed final wrapper' > \"$last\"\n" +
		"cat " + strconv.Quote(eventPath) + "\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	isolatedRoot := filepath.Join(root, "codex-home")
	if err := os.MkdirAll(isolatedRoot, 0o700); err != nil {
		t.Fatalf("prepare isolated root: %v", err)
	}
	executor := &codexCLIExecutor{
		binary:            script,
		taskPool:          pool,
		mcpURL:            "http://127.0.0.1:8080/api/stockv2/agent/mcp",
		isolatedCodexRoot: isolatedRoot,
		provider:          &codexCLIProviderRuntime{BaseURL: "http://127.0.0.1:8080/api/stockv2/agent/codex-proxy/provider-1"},
	}
	output, err := executor.ExecuteOperationReview(context.Background(), taskID, AgentContextPack{}, "deepseek-v4-flash", "medium")
	if err != nil {
		t.Fatalf("execute with JSONL final-message fallback: %v, output=%+v", err, output)
	}
	entry, ok := pool.getTask(taskID)
	if !ok {
		t.Fatal("submitted task disappeared")
	}
	entry.mu.Lock()
	submitted := entry.submittedResult
	entry.mu.Unlock()
	if submitted == nil || submitted.OutputType != OperationReviewOutputContinueMonitoring ||
		submitted.Result["reason"] != reason {
		t.Fatalf("submitted fallback result = %#v", submitted)
	}
	if len(output.ResultCandidates) != 2 || output.ResultCandidates[0].Status != "rejected" ||
		output.ResultCandidates[1].Status != "accepted" || output.ResultCandidates[1].Bytes < len(reason) {
		t.Fatalf("result candidate diagnostics = %#v", output.ResultCandidates)
	}
}

func TestCustomProviderResultCandidateErrorPrefersStructuredEnvelope(t *testing.T) {
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	taskID, _ := pool.createTask(AgentTaskTypeOperationReview, "run-1", "", time.Minute)
	executor := &codexCLIExecutor{taskPool: pool}
	candidates := []codexDirectResultCandidate{
		{Source: "output_last_message", Raw: []byte("Answer: analysis complete")},
		{
			Source: "codex_jsonl_agent_message[0]",
			Raw: []byte(`{"taskID":"` + taskID + `","taskType":"operation_review","result":` +
				`{"outputType":"not_a_valid_output","resultSummary":"done","result":{},"confidence":0.8}}`),
		},
	}

	result, diagnostics, err := executor.submitCodexDirectResultCandidates(candidates, taskID, AgentTaskTypeOperationReview)
	if err == nil || result != nil {
		t.Fatalf("result = %#v, error = %v; want structured candidate rejection", result, err)
	}
	if !strings.Contains(err.Error(), "codex_jsonl_agent_message[0]") ||
		!strings.Contains(err.Error(), "invalid result.outputType") || strings.Contains(err.Error(), "invalid character 'A'") {
		t.Fatalf("candidate error = %q, want structured validation failure", err)
	}
	if len(diagnostics) != 2 || diagnostics[0].ResultShaped || !diagnostics[1].ResultShaped ||
		diagnostics[1].Status != "rejected" || diagnostics[1].SHA256Prefix == "" {
		t.Fatalf("candidate diagnostics = %#v", diagnostics)
	}
}

func TestDecodeCodexDirectResultExtractsUniqueEnvelopeFromAnalysisProse(t *testing.T) {
	taskID := "task-analysis-prefix"
	result := `{"taskID":"` + taskID + `","taskType":"news_event_review","result":` +
		`{"outputType":"news_context_result","resultSummary":"完整结果","result":{"facts":["保留全部字段"]},"confidence":0.8}}`
	raw := []byte("Research completed after three checks.\n" + result + "\nAll requested items were covered.")

	decoded, err := decodeCodexDirectResult(raw, taskID, AgentTaskTypeNewsEventReview)
	if err != nil {
		t.Fatalf("decode result with analysis prose: %v", err)
	}
	if string(decoded) != result || !codexDirectResultLooksLikeEnvelope(raw) {
		t.Fatalf("decoded envelope = %s, want exact result %s", decoded, result)
	}
}

func TestDecodeCodexDirectResultRejectsAmbiguousEnvelopes(t *testing.T) {
	taskID := "task-ambiguous"
	envelope := `{"taskID":"` + taskID + `","taskType":"stock_profile_summary","result":` +
		`{"outputType":"stock_profile_summary","resultSummary":"one","result":{},"confidence":0.8}}`
	_, err := decodeCodexDirectResult(
		[]byte("first\n"+envelope+"\nsecond\n"+envelope),
		taskID,
		AgentTaskTypeStockProfileSummary,
	)
	if err == nil || !strings.Contains(err.Error(), "multiple JSON envelopes") {
		t.Fatalf("ambiguous envelope error = %v", err)
	}
}

func TestDecodeCodexDirectResultRepairsOnlyMissingTerminalDelimiter(t *testing.T) {
	taskID := "task-missing-terminal-brace"
	complete := `{"taskID":"` + taskID + `","taskType":"news_event_review","result":` +
		`{"outputType":"news_context_result","resultSummary":"完整内容","result":{"items":["全部保留"]},"confidence":0.8}}`
	truncated := []byte("Analysis complete.\n" + complete[:len(complete)-1])

	decoded, err := decodeCodexDirectResult(truncated, taskID, AgentTaskTypeNewsEventReview)
	if err != nil {
		t.Fatalf("repair missing terminal brace: %v", err)
	}
	if string(decoded) != complete {
		t.Fatalf("repaired envelope = %s, want %s", decoded, complete)
	}
}

func TestDecodeCodexDirectResultDoesNotRepairTruncatedContent(t *testing.T) {
	taskID := "task-truncated-content"
	tests := []string{
		`{"taskID":"` + taskID + `","taskType":"stock_profile_summary","result":{"outputType":"stock_profile_summary","resultSummary":"cut`,
		`{"taskID":"` + taskID + `","taskType":"stock_profile_summary","result":{"outputType":`,
		`{"taskID":"` + taskID + `","taskType":"stock_profile_summary","result":{"outputType":"stock_profile_summary",`,
	}
	for _, raw := range tests {
		if _, err := decodeCodexDirectResult([]byte(raw), taskID, AgentTaskTypeStockProfileSummary); err == nil {
			t.Fatalf("truncated content unexpectedly repaired: %s", raw)
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

	output, err := executor.executePrompt(context.Background(), "task-1", "prompt", "model", "")
	if output != nil {
		t.Fatalf("output = %+v, want nil", output)
	}
	if err == nil || !strings.Contains(err.Error(), "must configure exactly one URL or command") {
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

	output, err := executor.executePrompt(context.Background(), taskID, "prompt", "gpt-5.5", "")
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

func TestExecutePromptTimeoutKillsResidualProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := dir + "/child.pid"
	script := dir + "/fake-codex"
	body := "#!/bin/sh\n" +
		"trap 'exit 0' TERM\n" +
		"(trap '' TERM; sleep 30) &\n" +
		"echo $! > " + pidFile + "\n" +
		"wait\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	pool := newAgentTaskPool(defaultCleanupInterval)
	defer pool.Close()
	executor := &codexCLIExecutor{
		binary:   script,
		taskPool: pool,
		mcpURL:   "http://127.0.0.1:8080/api/stockv2/agent/mcp",
	}
	taskID, _ := pool.createTask(AgentTaskTypeNewsEventReview, "run-timeout", "", time.Minute)

	output, err := executor.executePromptWithTimeout(context.Background(), taskID, "prompt", "gpt-5.5", "", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if output == nil || !output.TimedOut || output.ProcessGroupID <= 0 {
		t.Fatalf("output = %+v, want timed out process group", output)
	}
	rawPID, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("read child pid: %v", readErr)
	}
	childPID, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if parseErr != nil {
		t.Fatalf("parse child pid: %v", parseErr)
	}
	defer syscall.Kill(childPID, syscall.SIGKILL)
	deadline := time.Now().Add(executorReaderDrainTimeout)
	for syscall.Kill(childPID, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("residual child process %d is still running", childPID)
	}
	if err := ensureExecutorProcessGroupStopped(output.ProcessGroupID); err != nil {
		t.Fatalf("verify stopped process group: %v", err)
	}
}

func TestBuildStockProfileSummaryPromptTruncatesUTF8Safely(t *testing.T) {
	profile := StockProfile{
		Symbol:          "000815",
		Market:          "SZ",
		InstrumentType:  InstrumentTypeStock,
		Name:            "美利云",
		BusinessSummary: strings.Repeat("中冶美利云产业投资股份有限公司推进云计算数据中心与造纸业务协同。", 300),
	}

	prompt := buildStockProfileSummaryPrompt("task-profile", profile, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	if !utf8.ValidString(prompt) {
		t.Fatalf("prompt is invalid utf8")
	}
	if !strings.Contains(prompt, "... [truncated]") {
		t.Fatalf("prompt was not truncated")
	}
}

func TestBuildStockProfileSummaryAPIPromptExcludesPreviousAIOutput(t *testing.T) {
	prompt := buildStockProfileSummaryPrompt("task-profile", StockProfile{
		Symbol:            "300750",
		Market:            "SZ",
		InstrumentType:    InstrumentTypeStock,
		Name:              "宁德时代",
		Aliases:           []string{"300750", "宁德时代"},
		Industry:          "电力设备",
		Concepts:          []string{"锂电池"},
		BusinessSummary:   "动力电池与储能系统",
		BusinessSummaryEn: "PREVIOUS_AI_SUMMARY",
		KeywordsEn:        []string{"PREVIOUS_AI_KEYWORD"},
		AIProfileError:    "PREVIOUS_AI_ERROR",
		ProfileTextEn:     "PREVIOUS_AI_RENDERED_TEXT",
	}, "")

	for _, unwanted := range []string{
		"PREVIOUS_AI_SUMMARY",
		"PREVIOUS_AI_KEYWORD",
		"PREVIOUS_AI_ERROR",
		"PREVIOUS_AI_RENDERED_TEXT",
		"Submit your final result using the stock_agent.submit_result MCP tool",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("API profile prompt contains excluded value %q", unwanted)
		}
	}
	for _, want := range []string{
		"Deterministic Profile Input",
		"动力电池与储能系统",
		"Return exactly ONE complete submission envelope as JSON content",
		`"taskType":"stock_profile_summary"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("API profile prompt missing %q", want)
		}
	}
}

func TestBuildNewsContextAggregationPromptEnforcesCoverageAndResearch(t *testing.T) {
	windowEnd := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	prompt := buildNewsContextAggregationPrompt("task-news-context", NewsContextAggregationPack{
		RunID:           "context-run-1",
		WindowType:      "daily",
		WindowEnd:       windowEnd,
		InputNewsEvents: []NewsEvent{{ID: "news-1"}},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"task-news-context",
		"context-run-1",
		"news_context_result",
		"processed_news_ids",
		"mechanically compare the exact ids in InputNewsEvents",
		"news_event_id",
		"thread_id",
		"core_thesis",
		"material_change",
		"counter_evidence",
		"Use only the canonical thread-change fields shown in the example",
		"Put sectors in industries, instruments in symbols or funds, rotation clues in relations",
		"affected_instruments",
		"invalidation_conditions",
		"rotation_clues",
		"sources",
		"do not invent aliases",
		"reviewed_thread_ids",
		"unchanged_thread_ids",
		"four-hour window is the sole regular model aggregation boundary",
		"do not request or reproduce an all-theme daily conclusion",
		"stock_agent.semantic_search_news_threads",
		"stock_agent.get_news_thread",
		"semantic_search_news_threads with asOf `2026-07-13T08:00:00Z`",
		"get_news_thread with the same asOf `2026-07-13T08:00:00Z`",
		"Both calls MUST use this exact aggregation WindowEnd cutoff",
		"Public verification is mandatory",
		"every ResearchReasons item",
		"Write every user-facing conclusion in Simplified Chinese",
		"Keep schema field names, enum values, identifiers, symbols, and source URLs unchanged",
		"When InputThreads is empty, both arrays must be empty",
		"CandidateThreads and themes discovered through semantic search are candidates, not batch review inputs",
		"Do not defer merely because an item is low impact",
		"stock_agent.submit_result",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if !validAgentTaskOutputType(AgentTaskTypeNewsEventReview, "news_context_result") {
		t.Fatal("news context result must be accepted for news_event_review")
	}
	if strings.Contains(prompt, "affected sectors/instruments") {
		t.Fatalf("prompt requests fields rejected by the strict result schema: %s", prompt)
	}
	if validAgentTaskOutputType(AgentTaskTypeNewsEventReview, AgentTaskTypeNewsEventReview) {
		t.Fatal("legacy task key must not be accepted as a result type")
	}

	apiPrompt := buildNewsContextAggregationPrompt("task-news-context", NewsContextAggregationPack{
		RunID:                 "context-run-1",
		WindowType:            "daily",
		WindowEnd:             windowEnd,
		InputNewsEvents:       []NewsEvent{{ID: "news-1"}},
		InputThreads:          []NewsContextPromptThread{{ID: "thread-1", ThemeID: "thread-1"}},
		CandidateThreads:      []NewsContextPromptThread{{ID: "candidate-1", ThemeID: "candidate-1", RetrievalScore: 0.8}},
		CandidateLookupStatus: "ready",
	}, "")
	for _, want := range []string{
		"InputThreads already contains the authoritative point-in-time snapshot",
		"do not search for or fetch the same themes again",
		"CandidateThreads contains the service-prefetched point-in-time snapshots",
		"Candidate lookup completed for this batch",
		"This API execution has no public search or browsing capability",
		"search_audit status unavailable",
		"service already performed bounded candidate recall",
		"do not call stock_agent.submit_result",
	} {
		if !strings.Contains(apiPrompt, want) {
			t.Fatalf("API prompt missing %q: %s", want, apiPrompt)
		}
	}
	if strings.Contains(apiPrompt, "Actively use Codex CLI public search/browse") {
		t.Fatalf("API prompt incorrectly requires Codex CLI browsing: %s", apiPrompt)
	}
	if strings.Contains(apiPrompt, "stock_agent.semantic_search_news_threads with asOf") {
		t.Fatalf("API prompt incorrectly requests runtime theme lookup: %s", apiPrompt)
	}
}

func TestBuildNewsContextAggregationPromptUsesThreadOnlyExampleWithoutInventedNews(t *testing.T) {
	prompt := buildNewsContextAggregationPrompt("task-thread-only", NewsContextAggregationPack{
		RunID:      "context-run-thread-only",
		WindowType: NewsContextWindowFourHour,
		WindowEnd:  time.Date(2026, 7, 18, 20, 0, 0, 0, time.UTC),
		InputThreads: []NewsContextPromptThread{
			{ID: "thread-stable-1", ThemeID: "thread-stable-1"},
			{ID: "thread-stable-1", ThemeID: "thread-stable-1"},
			{ID: "thread-stable-2", ThemeID: "thread-stable-2"},
		},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	for _, want := range []string{
		"This batch has no InputNewsEvents",
		"Do not perform new public search/browse or semantic theme lookup during this thread-only parent aggregation",
		"Do not use shell commands or any other external lookup",
		"persisted InputThreads are the complete evidence boundary",
		"perform only parent-window synthesis",
		`"processed_news_ids":[]`,
		`"reviewed_thread_ids":[]`,
		`"unchanged_thread_ids":["thread-stable-1","thread-stable-2"]`,
		`"news_decisions":[]`,
		`"thread_changes":[]`,
		`"search_audit":[]`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("thread-only prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "news-event-id") {
		t.Fatalf("thread-only prompt contains invented news example: %s", prompt)
	}
	for _, forbidden := range []string{
		"Actively use Codex CLI public search/browse", "This API execution has no public search or browsing capability",
		"When RequiredResearch is true", "For every public verification",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("thread-only prompt must not contain %q", forbidden)
		}
	}
}

func TestBuildPortfolioSentinelPromptRequiresCompleteNewsContextReview(t *testing.T) {
	prompt := buildPortfolioSentinelPrompt("task-sentinel", PortfolioSentinelContext{
		RunID: "sentinel-run-1",
		NewsContext: &PortfolioSentinelNewsContext{
			RunID:                    "context-run-1",
			ChangedThreadCount:       123,
			RequiredMCPTool:          mcpToolListNewsContextChanges,
			ImpactReviewRequiredTool: mcpToolListPortfolioSentinelImpactReviewScope,
			ImpactReviewScope: PortfolioSentinelImpactReviewScopeSummary{
				HoldingCount: 2, MonitorCount: 3, AlertCount: 4, OpportunityCount: 5, StrategyCount: 6,
			},
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
		mcpToolListPortfolioSentinelImpactReviewScope,
		"sentinel-run-1",
		"objectType `holdings`, `monitors`, `alerts`, `opportunities`, and `strategies`",
		"respectively 2, 3, 4, 5, and 6 identifiers",
		"impact_review_coverage",
		"holding_ids",
		"monitor_ids",
		"alert_ids",
		"opportunity_ids",
		"strategy_ids",
		"explicit empty list",
		"server owns monitor_window and valid_until",
		"only the next trading window/session",
		"plan validity period",
		"at most 8 external search/fetch tool calls",
		"Do not re-fetch quotes, daily bars, profiles, portfolio context, news, or links that are already present and usable",
		"use at most 8 discretionary MCP retrieval calls in total",
		"final stock_agent.submit_result call is not part of this retrieval budget",
		"Say external search is unavailable only when the tool cannot be invoked",
		"do not say external search is unavailable",
		"invented",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildPortfolioSentinelPromptKeepsFinalReviewContractWhenContextIsOversized(t *testing.T) {
	prompt := buildPortfolioSentinelPrompt("task-oversized", PortfolioSentinelContext{
		RunID: "sentinel-run-oversized",
		NewsContext: &PortfolioSentinelNewsContext{
			RunID:                    "context-run-oversized",
			ChangedThreadCount:       12345,
			RequiredMCPTool:          mcpToolListNewsContextChanges,
			ImpactReviewRequiredTool: mcpToolListPortfolioSentinelImpactReviewScope,
			ImpactReviewScope: PortfolioSentinelImpactReviewScopeSummary{
				HoldingCount: 101, MonitorCount: 202, AlertCount: 303, OpportunityCount: 404, StrategyCount: 505,
			},
		},
		Note: strings.Repeat("这是可裁剪的超大上下文正文。", 3000),
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	if len(prompt) > 22000 {
		t.Fatalf("prompt length = %d, want <= 22000", len(prompt))
	}
	if !utf8.ValidString(prompt) {
		t.Fatal("prompt is invalid utf8")
	}
	if !strings.Contains(prompt, "... [truncated]") {
		t.Fatal("oversized context was not truncated")
	}
	for _, want := range []string{
		"task-oversized",
		"context-run-oversized",
		"sentinel-run-oversized",
		mcpToolListNewsContextChanges,
		mcpToolListPortfolioSentinelImpactReviewScope,
		"respectively 101, 202, 303, 404, and 505 identifiers",
		"do not stop after the first page",
		"until every page is read",
		"checked_news_thread_version_ids` is required",
		"complete, duplicate-free versionId set returned by all pages",
		"`impact_review_coverage` is required",
		"Each list must exactly match the duplicate-free frozen identifiers returned from all pages",
		`"impact_review_coverage":{`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("oversized prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildPortfolioSentinelPromptKeepsEveryHoldingInUntruncatedCoverage(t *testing.T) {
	prompt := buildPortfolioSentinelPrompt("task-holding-coverage", PortfolioSentinelContext{
		RunID: "sentinel-run-holding-coverage",
		Portfolios: []PortfolioSentinelPortfolioContext{{
			Portfolio: StockV2Portfolio{ID: "portfolio-1", Name: "测试组合"},
			Holdings: []PortfolioSentinelHoldingContext{
				{
					Holding: StockV2Holding{ID: "holding-large", PortfolioID: "portfolio-1", Symbol: "588940", Market: "SH", Name: "科创50ETF富国"},
					Profile: &StockProfile{ProfileText: strings.Repeat("超大画像正文", 3000)},
				},
				{
					Holding: StockV2Holding{ID: "holding-second", PortfolioID: "portfolio-1", Symbol: "000977", Market: "SZ", Name: "浪潮信息"},
				},
			},
		}},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")

	if len(prompt) > 22000 {
		t.Fatalf("prompt length = %d, want <= 22000", len(prompt))
	}
	if !strings.Contains(prompt, "... [truncated]") {
		t.Fatal("oversized context was not compacted")
	}
	start := strings.Index(prompt, "## Required Holding Coverage (Untruncated)")
	end := strings.Index(prompt, "## Required Review Workflow")
	if start < 0 || end <= start {
		t.Fatalf("holding coverage section missing:\n%s", prompt)
	}
	coverage := prompt[start:end]
	for _, want := range []string{
		`"portfolio_id": "portfolio-1"`,
		`"holding_id": "holding-large"`,
		`"symbol": "588940"`,
		`"holding_id": "holding-second"`,
		`"symbol": "000977"`,
		`"name": "浪潮信息"`,
		"return `hold`",
	} {
		if !strings.Contains(coverage, want) {
			t.Fatalf("untruncated holding coverage missing %q:\n%s", want, coverage)
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

func TestSuppressCodexStderrLineDropsKnownIncidentalNoise(t *testing.T) {
	notionLine := []byte(`ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed, when AuthRequired(AuthRequiredError { resource_metadata="https://mcp.notion.com/.well-known/oauth-protected-resource/mcp" })`)
	if !suppressCodexStderrLine(notionLine) {
		t.Fatal("expected Notion auth worker noise to be suppressed")
	}
	if !suppressCodexStderrLine([]byte("Reading additional input from stdin...")) {
		t.Fatal("expected Codex stdin status notice to be suppressed")
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
		output, err := executor.executePrompt(ctx, taskID, rawPrompt, "model", "")
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
		"internal_recall",
		"candidate_ranking",
		"Do not silently fall back",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildOpportunityDiscoveryPromptBoundsMarketScanCandidates(t *testing.T) {
	candidates := make([]OpportunityMarketScanCandidate, 0, opportunityMarketScanResearchLimit)
	for i := 0; i < opportunityMarketScanResearchLimit; i++ {
		candidates = append(candidates, OpportunityMarketScanCandidate{
			Symbol: fmt.Sprintf("600%03d", i), Market: "SH", Name: fmt.Sprintf("候选%d%s", i+1, strings.Repeat("长", 80)),
			Industry: strings.Repeat("电子", 80), Concepts: []string{strings.Repeat("产业链", 80)},
			Stage: OpportunityMarketScanCandidateResearch, FinalRank: i + 1,
			Metrics: OpportunityMarketScanMetrics{
				TradeDate: "2026-08-10", Return20Pct: float64(i), QFQAvailable: true,
				ThemeSignals: []string{strings.Repeat("消息脉络", 100)},
			},
		})
	}
	prompt := buildOpportunityDiscoveryPrompt("task-market-scan", OpportunityDiscoveryContext{
		Mode:             OpportunityDiscoveryModeMarketScan,
		MarketScanRunID:  "scan-1",
		Opportunity:      Opportunity{ID: "opp-1", Title: "主板扫描", UserThesis: "复核预筛候选"},
		DiscoveryRun:     OpportunityDiscoveryRun{ID: "run-1", OpportunityID: "opp-1"},
		MarketCandidates: candidates,
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	for _, want := range []string{"complete allowed universe", "do not rediscover the full market", "600000", "600019", "at most 10"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("market scan prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, promptTruncatedMarker) {
		t.Fatalf("bounded market scan prompt unexpectedly truncated at %d bytes", len(prompt))
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

func TestBuildStrategyGenerationFormatterPromptRequiresPerDraftConfidence(t *testing.T) {
	prompt := buildStrategyGenerationStepPrompt("task-strategy-formatter", StrategyGenerationStepPack{
		RunID:     "run-1",
		StepKey:   StrategyGenerationStepFormatter,
		Role:      StrategyGenerationStepFormatter,
		Objective: "Format final strategy report",
		Context: StrategyGenerationContext{Input: StrategyGenerationInput{
			Mode: StrategyGenerationModeManualTarget,
		}},
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	for _, want := range []string{"Every new_strategy draft must include its own numeric confidence", `"confidence":0.7`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("formatter prompt missing %q:\n%s", want, prompt)
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
