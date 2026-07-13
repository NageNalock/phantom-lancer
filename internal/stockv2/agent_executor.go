package stockv2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"
)

// Codex CLI executor: 启动 codex exec 子进程, watch 输出, 等待 MCP submit_result 或超时。
//
// ponytail: 直接用 os/exec, 不引入 codexclient 依赖, 保持 stockv2 领域边界独立。
// 环境变量用 allowlist 转发, 与 codexclient 的策略对齐但不共享代码。

type AgentExecutorOutput struct {
	Command       string        `json:"command,omitempty"` // redacted, prompt omitted
	Prompt        string        `json:"-"`
	StdoutTail    string        `json:"stdoutTail"` // ~4KB
	StderrTail    string        `json:"stderrTail"` // ~4KB
	ExitCode      int           `json:"exitCode"`
	TimedOut      bool          `json:"timedOut"`
	Duration      time.Duration `json:"duration"`
	RawTranscript string        `json:"rawTranscript"` // ~16KB 摘要, 用于 ledger
}

type codexCLIExecutor struct {
	log       *slog.Logger
	binary    string
	codexHome string
	taskPool  *agentTaskPool
	mcpURL    string // 本地 MCP server 地址, 如 http://127.0.0.1:PORT/api/stockv2/agent/mcp
}

const (
	execDefaultTimeout         = 10 * time.Minute
	stdoutTailMaxBytes         = 4 * 1024
	stderrTailMaxBytes         = 4 * 1024
	transcriptMaxBytes         = 16 * 1024
	executorReaderDrainTimeout = 2 * time.Second
	codexStockAgentMCPName     = "stock_agent"
	codexSubmitResultTool      = "stock_agent.submit_result"
)

type codexMCPServerCapability struct {
	Name          string
	URL           string
	RequiredTools []string
}

// 允许转发给子进程的环境变量(与 codexclient 对齐, 但独立维护)
var executorAllowedEnvKeys = []string{
	"PATH", "HOME", "USER", "SHELL", "TERM",
	"LANG", "LC_ALL", "LC_CTYPE",
	"TMPDIR", "XDG_CACHE_HOME", "XDG_CONFIG_HOME",
}

var secretEnvHints = []string{
	"TOKEN", "SECRET", "KEY", "PASSWORD", "API_KEY", "AUTH",
	"COOKIE", "SESSION", "CSRF", "BEARER", "CREDENTIAL",
}

func newCodexCLIExecutor(log *slog.Logger, binary, codexHome, mcpURL string, pool *agentTaskPool) *codexCLIExecutor {
	return &codexCLIExecutor{
		log:       log,
		binary:    binary,
		codexHome: codexHome,
		taskPool:  pool,
		mcpURL:    mcpURL,
	}
}

func (e *codexCLIExecutor) ExecuteOperationReview(
	ctx context.Context,
	taskID string,
	pack AgentContextPack,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}

	// 构建 prompt
	prompt := buildOperationReviewPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecuteStrategyGeneration(
	ctx context.Context,
	taskID string,
	pack StrategyGenerationContext,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildStrategyGenerationPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecuteStrategyGenerationStep(
	ctx context.Context,
	taskID string,
	pack StrategyGenerationStepPack,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildStrategyGenerationStepPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecuteOpportunityDiscovery(
	ctx context.Context,
	taskID string,
	pack OpportunityDiscoveryContext,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildOpportunityDiscoveryPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecuteNewsContextAggregation(
	ctx context.Context,
	taskID string,
	pack NewsContextAggregationPack,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildNewsContextAggregationPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecutePortfolioSentinel(
	ctx context.Context,
	taskID string,
	pack PortfolioSentinelContext,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildPortfolioSentinelPrompt(taskID, pack, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) ExecuteStockProfileSummary(
	ctx context.Context,
	taskID string,
	profile StockProfile,
	modelName string,
) (*AgentExecutorOutput, error) {
	if e.binary == "" {
		return nil, fmt.Errorf("codex binary path not configured")
	}
	prompt := buildStockProfileSummaryPrompt(taskID, profile, e.mcpURL)
	return e.executePrompt(ctx, taskID, prompt, modelName)
}

func (e *codexCLIExecutor) executePrompt(
	ctx context.Context,
	taskID string,
	prompt string,
	modelName string,
) (*AgentExecutorOutput, error) {
	// 超时控制
	timeout := execDefaultTimeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	mcpServers := e.codexMCPServers()
	if err := e.preflightCodexMCPServers(mcpServers); err != nil {
		return nil, err
	}

	args := buildCodexExecArgs(modelName, prompt, mcpServers)
	cmd := exec.CommandContext(execCtx, e.binary, args...)
	cmd.Env = e.buildEnv()

	var stdoutBuf, stderrBuf, transcriptBuf ringBuffer
	stdoutBuf.Init(stdoutTailMaxBytes)
	stderrBuf.Init(stderrTailMaxBytes)
	transcriptBuf.Init(transcriptMaxBytes)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}

	// 异步读取 stdout / stderr
	doneCh := make(chan error, 2)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			stdoutBuf.Write(line)
			stdoutBuf.Write([]byte("\n"))
			transcriptBuf.Write(line)
			transcriptBuf.Write([]byte("\n"))
		}
		doneCh <- scanner.Err()
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if suppressCodexStderrLine(line) {
				continue
			}
			stderrBuf.Write(line)
			stderrBuf.Write([]byte("\n"))
			transcriptBuf.Write([]byte("stderr: "))
			transcriptBuf.Write(line)
			transcriptBuf.Write([]byte("\n"))
		}
		doneCh <- scanner.Err()
	}()

	// 同时等: result / 进程退出 / context 取消
	type executorWaitResult struct {
		err        error
		readerErrs []error
	}
	var execErr error
	waitDone := make(chan executorWaitResult, 1)
	go func() {
		readerErrs := waitForExecutorReaders(doneCh, 2, execDefaultTimeout+executorReaderDrainTimeout)
		waitDone <- executorWaitResult{err: cmd.Wait(), readerErrs: readerErrs}
	}()

	var submittedResult *AgentTaskSubmittedResult
	var resultErr error

	// 等 result
	resultCh := make(chan struct {
		result *AgentTaskSubmittedResult
		err    error
	}, 1)
	go func() {
		res, err := e.taskPool.waitForResult(execCtx, taskID)
		resultCh <- struct {
			result *AgentTaskSubmittedResult
			err    error
		}{res, err}
	}()

	// 主等待循环
	timedOut := false
	resultReceived := false
	processDone := false
	var readerErrs []error

waitLoop:
	for {
		select {
		case result := <-waitDone:
			execErr = result.err
			readerErrs = append(readerErrs, result.readerErrs...)
			processDone = true
			break waitLoop
		case r := <-resultCh:
			if r.err == nil {
				submittedResult = r.result
				resultReceived = true
				// result 已收到, 继续等进程正常退出或超时
			} else {
				resultErr = r.err
			}
		case <-execCtx.Done():
			if execCtx.Err() == context.DeadlineExceeded {
				timedOut = true
			}
			break waitLoop
		}
	}

	// 如果 result 已收到但进程还在跑, 给一点收尾时间然后 kill
	if resultReceived && !processDone && !timedOut {
		// 再等 10 秒让进程自然结束
		shortCtx, shortCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shortCancel()
		select {
		case result := <-waitDone:
			execErr = result.err
			readerErrs = append(readerErrs, result.readerErrs...)
			processDone = true
		case <-shortCtx.Done():
			// 超时 kill
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			result := <-waitDone
			execErr = result.err
			readerErrs = append(readerErrs, result.readerErrs...)
			processDone = true
		}
	}
	if timedOut && !processDone {
		select {
		case result := <-waitDone:
			execErr = result.err
			readerErrs = append(readerErrs, result.readerErrs...)
			processDone = true
		case <-time.After(executorReaderDrainTimeout):
		}
	}

	duration := time.Since(start)

	exitCode := -1
	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	} else {
		exitCode = 0
	}
	for _, err := range readerErrs {
		if err == nil {
			continue
		}
		line := []byte("executor_output_read_error: " + err.Error() + "\n")
		stderrBuf.Write(line)
		transcriptBuf.Write([]byte("stderr: "))
		transcriptBuf.Write(line)
	}

	// 脱敏后输出
	stdoutTail := safelog.Text(stdoutBuf.String(), stdoutTailMaxBytes)
	stderrTail := safelog.Text(stderrBuf.String(), stderrTailMaxBytes)
	transcript := safelog.Text(transcriptBuf.String(), transcriptMaxBytes)

	output := &AgentExecutorOutput{
		Command:       codexCommandSummary(e.binary, args),
		Prompt:        prompt,
		StdoutTail:    stdoutTail,
		StderrTail:    stderrTail,
		ExitCode:      exitCode,
		TimedOut:      timedOut,
		Duration:      duration,
		RawTranscript: transcript,
	}

	// result 没收到但进程退出了 → 失败
	if submittedResult == nil {
		if resultErr != nil {
			return output, fmt.Errorf("no result submitted: %w", resultErr)
		}
		if timedOut {
			return output, fmt.Errorf("execution timed out after %s, no result submitted", duration)
		}
		return output, fmt.Errorf("process exited (code %d) without submitting result", exitCode)
	}

	return output, nil
}

func buildCodexExecArgs(modelName, prompt string, mcpServers []codexMCPServerCapability) []string {
	// ponytail: StockV2 agent runs are owner-triggered local tasks; isolate user config so unrelated MCPs cannot pollute stderr.
	args := []string{"exec", "--json", "--ignore-user-config", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-c", "mcp_servers={}"}
	for _, server := range mcpServers {
		args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.url=%s", server.Name, strconv.Quote(strings.TrimSpace(server.URL))))
	}
	if modelName != "" {
		args = append(args, "--model", modelName)
	}
	return append(args, prompt)
}

func codexCommandSummary(binary string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, filepathBase(binary))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if i == len(args)-1 {
			parts = append(parts, fmt.Sprintf("<prompt:%d chars>", len(arg)))
			break
		}
		if arg == "-c" && i+1 < len(args) {
			parts = append(parts, "-c", redactCodexConfigArg(args[i+1]))
			i++
			continue
		}
		parts = append(parts, arg)
	}
	return safelog.Text(strings.Join(parts, " "), 2000)
}

func truncatePromptUTF8(value string, headBytes, tailBytes int) string {
	if value == "" || headBytes <= 0 || tailBytes <= 0 || len(value) <= headBytes+tailBytes {
		return value
	}
	head := value[:utf8SafePrefixLen(value, headBytes)]
	tailStart := utf8SafeSuffixStart(value, len(value)-tailBytes)
	return head + promptTruncatedMarker + value[tailStart:]
}

const promptTruncatedMarker = "\n... [truncated]\n...\n"

func truncatePromptUTF8ToLimit(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	if maxBytes <= len(promptTruncatedMarker) {
		return promptTruncatedMarker[:maxBytes]
	}
	payloadBytes := maxBytes - len(promptTruncatedMarker)
	headBytes := payloadBytes * 3 / 4
	tailBytes := payloadBytes - headBytes
	return truncatePromptUTF8(value, headBytes, tailBytes)
}

func utf8SafePrefixLen(value string, limit int) int {
	if limit >= len(value) {
		return len(value)
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return limit
}

func utf8SafeSuffixStart(value string, start int) int {
	if start <= 0 {
		return 0
	}
	if start >= len(value) {
		return len(value)
	}
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return start
}

func filepathBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "codex"
	}
	path = strings.TrimRight(path, "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func redactCodexConfigArg(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "key") || strings.Contains(lower, "password") || strings.Contains(lower, "cookie") {
		if idx := strings.Index(value, "="); idx >= 0 {
			return value[:idx+1] + "<redacted>"
		}
		return "<redacted>"
	}
	return value
}

func (e *codexCLIExecutor) codexMCPServers() []codexMCPServerCapability {
	return []codexMCPServerCapability{{
		Name:          codexStockAgentMCPName,
		URL:           strings.TrimSpace(e.mcpURL),
		RequiredTools: stockAgentMCPRequiredTools(),
	}}
}

func (e *codexCLIExecutor) preflightCodexMCPServers(servers []codexMCPServerCapability) error {
	if len(servers) == 0 {
		return errors.New("codex MCP capability list is empty")
	}
	for _, server := range servers {
		if err := validateCodexMCPServerCapability(server); err != nil {
			return err
		}
		if server.Name == codexStockAgentMCPName {
			if err := e.preflightStockAgentMCPTools(server.RequiredTools); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCodexMCPServerCapability(server codexMCPServerCapability) error {
	name := strings.TrimSpace(server.Name)
	if name == "" || strings.ContainsAny(name, ". \t\r\n") {
		return fmt.Errorf("invalid codex MCP server name %q", server.Name)
	}
	endpoint, err := url.Parse(strings.TrimSpace(server.URL))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("invalid codex MCP server URL for %s", name)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return fmt.Errorf("unsupported codex MCP server URL scheme for %s", name)
	}
	if len(server.RequiredTools) == 0 {
		return fmt.Errorf("codex MCP server %s has no required tools", name)
	}
	for _, tool := range server.RequiredTools {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("codex MCP server %s has an empty required tool", name)
		}
	}
	return nil
}

func (e *codexCLIExecutor) preflightStockAgentMCPTools(requiredTools []string) error {
	if e.taskPool == nil {
		return errors.New("stock agent MCP task pool is not configured")
	}
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "stock-agent-preflight-tools",
		"method":  "tools/list",
	})
	raw := e.taskPool.HandleMCPRequest(req)
	var resp struct {
		Error  *mcpError `json:"error"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("stock agent MCP tools/list decode failed: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("stock agent MCP tools/list failed: %s", resp.Error.Message)
	}
	available := make(map[string]bool, len(resp.Result.Tools))
	for _, tool := range resp.Result.Tools {
		available[strings.TrimSpace(tool.Name)] = true
	}
	for _, tool := range requiredTools {
		if !available[strings.TrimSpace(tool)] {
			return fmt.Errorf("stock agent MCP required tool missing: %s", tool)
		}
	}
	return nil
}

func (e *codexCLIExecutor) buildEnv() []string {
	out := make([]string, 0, len(executorAllowedEnvKeys)+2)
	for _, key := range executorAllowedEnvKeys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			if isSecretEnvKey(key) {
				continue
			}
			out = append(out, key+"="+value)
		}
	}
	if strings.TrimSpace(e.codexHome) != "" {
		out = append(out, "CODEX_HOME="+e.codexHome)
	}
	return out
}

func isSecretEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, hint := range secretEnvHints {
		if strings.Contains(upper, hint) {
			return true
		}
	}
	return false
}

func suppressCodexStderrLine(line []byte) bool {
	text := string(line)
	return strings.Contains(text, "mcp.notion.com") && strings.Contains(text, "AuthRequired")
}

func waitForExecutorReaders(doneCh <-chan error, count int, timeout time.Duration) []error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	errs := make([]error, 0, count)
	for count > 0 {
		select {
		case err := <-doneCh:
			if err != nil {
				errs = append(errs, err)
			}
			count--
		case <-timer.C:
			return errs
		}
	}
	return errs
}

// buildOperationReviewPrompt 从 ContextPack 构建 codex exec 的 prompt。
// 结构化呈现上下文, 明确 taskID 和提交方式, 输出 schema 说明。
func buildOperationReviewPrompt(taskID string, pack AgentContextPack, mcpURL string) string {
	var b strings.Builder

	b.WriteString("# Operation Review Task\n\n")
	b.WriteString("System role: you are a StockV2 monitoring-hit reviewer. You are NOT a trading executor.\n")
	b.WriteString("Your job is to audit whether this MonitorHit is real, actionable, stale/degraded, or noise.\n")
	b.WriteString("Do not place orders, do not modify holdings, and do not update formal strategies.\n")
	b.WriteString("Use provided context, stock_agent MCP data, and Codex CLI public search/browse for external verification when facts are stale, conflicting, high-impact, or not directly supported. Do not invent market prices, financial data, news, filings, or sources.\n")
	b.WriteString("Do not implement or request web_search/web_fetch MCP tools from the main program; external public verification is your responsibility inside Codex CLI.\n")
	b.WriteString("Submit your final result using the stock_agent.submit_result MCP tool.\n\n")
	b.WriteString("Do not use shell commands or curl to submit the result; use the MCP tool directly.\n\n")

	// Task ID + 提交方式
	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `operation_review`\n")
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	// Hit
	b.WriteString("## Monitor Hit\n\n")
	fmt.Fprintf(&b, "- Title: %s\n", pack.Hit.Title)
	fmt.Fprintf(&b, "- Summary: %s\n", pack.Hit.Summary)
	fmt.Fprintf(&b, "- Symbol: %s\n", pack.Hit.Symbol)
	fmt.Fprintf(&b, "- Status: %s\n", pack.Hit.Status)
	fmt.Fprintf(&b, "- Task Type: %s\n", pack.Hit.TaskType)
	if pack.Hit.StrategyID != "" {
		fmt.Fprintf(&b, "- Strategy ID: %s\n", pack.Hit.StrategyID)
	}
	if pack.Hit.PortfolioID != "" {
		fmt.Fprintf(&b, "- Portfolio ID: %s\n", pack.Hit.PortfolioID)
	}
	b.WriteString("\n")

	// Evidence
	if len(pack.Evidence) > 0 {
		b.WriteString("## Hit Evidence\n\n")
		b.WriteString("```json\n")
		evidenceJSON, _ := json.MarshalIndent(pack.Evidence, "", "  ")
		b.Write(evidenceJSON)
		b.WriteString("\n```\n\n")
	}

	if pack.Hit.TaskType == "agent_cli_debug" {
		googleNewsDate := stringFromAny(pack.Evidence["googleNewsDate"])
		if googleNewsDate == "" {
			googleNewsDate = time.Now().Format("2006-01-02")
		}
		b.WriteString("## CLI Debug Search Check\n\n")
		b.WriteString("This is a debug-only task. For the Google News check only, use any available web/search MCP or search tool instead of relying only on the provided context.\n")
		fmt.Fprintf(&b, "- Search target: Google News headlines for `%s`.\n", googleNewsDate)
		b.WriteString("- Return all human-readable text in Chinese.\n")
		b.WriteString("- Use outputType `continue_monitoring`.\n")
		b.WriteString("- The result object must include `googleNewsSearchStatus`, `googleNewsTodayZh`, and `searchAudit`.\n")
		b.WriteString("- `googleNewsTodayZh` should contain 3-5 items with `title`, `source`, `publishedAt`, `url`, and `summaryZh` when search succeeds.\n")
		b.WriteString("- If no search/web tool is available or search fails, set `googleNewsSearchStatus` to `unavailable` or `failed`, keep `googleNewsTodayZh` as an empty array, and explain in Chinese. Do not fabricate news.\n\n")
	}

	// Strategy
	if pack.Strategy != nil {
		b.WriteString("## Strategy\n\n")
		fmt.Fprintf(&b, "- Name: %s\n", pack.Strategy.Strategy.Name)
		fmt.Fprintf(&b, "- Kind: %s\n", pack.Strategy.Strategy.Kind)
		fmt.Fprintf(&b, "- Scope: %s\n", pack.Strategy.Strategy.Scope)
		fmt.Fprintf(&b, "- Status: %s\n", pack.Strategy.Strategy.Status)
		if pack.Strategy.ActiveVersion != nil {
			fmt.Fprintf(&b, "- Active Version: #%d %s\n", pack.Strategy.ActiveVersion.VersionNo, pack.Strategy.ActiveVersion.Title)
			if pack.Strategy.ActiveVersion.Direction != "" {
				fmt.Fprintf(&b, "- Direction: %s\n", pack.Strategy.ActiveVersion.Direction)
			}
		}
		b.WriteString("\n")
	}

	// News
	if pack.NewsEvent != nil {
		b.WriteString("## News Context\n\n")
		fmt.Fprintf(&b, "- Source: %s\n", pack.NewsEvent.Source)
		fmt.Fprintf(&b, "- Title: %s\n", pack.NewsEvent.Title)
		if pack.NewsEvent.Summary != "" {
			fmt.Fprintf(&b, "- Summary: %s\n", pack.NewsEvent.Summary)
		}
		if !pack.NewsEvent.EventAt.IsZero() {
			fmt.Fprintf(&b, "- Event Time: %s\n", pack.NewsEvent.EventAt.Format(time.RFC3339))
		}
		if pack.NewsLink != nil {
			fmt.Fprintf(&b, "- Candidate Score: %.2f\n", pack.NewsLink.Score)
			fmt.Fprintf(&b, "- Match Method: %s\n", pack.NewsLink.MatchMethod)
			fmt.Fprintf(&b, "- Match Reason: %s\n", pack.NewsLink.Reason)
			if len(pack.NewsLink.MatchedTerms) > 0 {
				fmt.Fprintf(&b, "- Matched Terms: %s\n", strings.Join(pack.NewsLink.MatchedTerms, ", "))
			}
		}
		if pack.Profile != nil {
			fmt.Fprintf(&b, "- Stock Profile: %s %s %s\n", pack.Profile.Symbol, pack.Profile.Name, pack.Profile.ProfileText)
		}
		b.WriteString("- Treat the link candidate as high-recall evidence, not as a confirmed fact.\n\n")
	}

	// Quote
	if pack.Quote != nil {
		b.WriteString("## Latest Quote\n\n")
		fmt.Fprintf(&b, "- Price: %.2f\n", pack.Quote.LastPrice)
		if pack.Quote.PctChange != 0 {
			fmt.Fprintf(&b, "- Change %%: %.2f%%\n", pack.Quote.PctChange)
		}
		if pack.Quote.Volume > 0 {
			fmt.Fprintf(&b, "- Volume: %.0f\n", pack.Quote.Volume)
		}
		b.WriteString("\n")
	}

	// Daily Bars
	if pack.DailyBars != nil {
		b.WriteString("## Daily Bars Context\n\n")
		fmt.Fprintf(&b, "- Count: %d bars\n", pack.DailyBars.Count)
		fmt.Fprintf(&b, "- Latest Close: %.2f\n", pack.DailyBars.LatestClose)
		if pack.DailyBars.LatestTradeDate != "" {
			fmt.Fprintf(&b, "- Latest Trade Date: %s\n", pack.DailyBars.LatestTradeDate)
		}
		fmt.Fprintf(&b, "- Quality: %s\n", pack.DailyBars.Quality)
		if pack.DailyBars.Summary != nil {
			b.WriteString("- Summary:\n")
			for k, v := range pack.DailyBars.Summary {
				fmt.Fprintf(&b, "  - %s: %.2f\n", k, v)
			}
		}
		b.WriteString("\n")
	}

	if pack.MinuteBars != nil {
		b.WriteString("## Intraday Minute Bars Context\n\n")
		fmt.Fprintf(&b, "- Count: %d bars\n", pack.MinuteBars.Count)
		fmt.Fprintf(&b, "- Latest Close: %.2f\n", pack.MinuteBars.LatestClose)
		if !pack.MinuteBars.LatestMinuteAt.IsZero() {
			fmt.Fprintf(&b, "- Latest Minute: %s\n", pack.MinuteBars.LatestMinuteAt.Format(time.RFC3339))
		}
		if pack.MinuteBars.Source != "" {
			fmt.Fprintf(&b, "- Source: %s\n", pack.MinuteBars.Source)
		}
		if pack.MinuteBars.Summary != nil {
			b.WriteString("- Summary:\n")
			for k, v := range pack.MinuteBars.Summary {
				fmt.Fprintf(&b, "  - %s: %.2f\n", k, v)
			}
		}
		b.WriteString("\n")
	}

	// Portfolio
	if pack.Portfolio != nil {
		b.WriteString("## Portfolio Context\n\n")
		fmt.Fprintf(&b, "- Name: %s\n", pack.Portfolio.Portfolio.Name)
		fmt.Fprintf(&b, "- Cash: %.2f\n", pack.Portfolio.Portfolio.Cash)
		if pack.Portfolio.Snapshot != nil {
			fmt.Fprintf(&b, "- Snapshot Total Asset Value: %.2f\n", pack.Portfolio.Snapshot.TotalAssetValue)
			fmt.Fprintf(&b, "- Snapshot Valuation At: %s\n", pack.Portfolio.Snapshot.ValuationAt.Format("2006-01-02 15:04"))
			fmt.Fprintf(&b, "- Position Count: %d\n", pack.Portfolio.Snapshot.PositionCount)
		}
		if len(pack.Portfolio.Holdings) > 0 {
			b.WriteString("- Holdings:\n")
			for _, h := range pack.Portfolio.Holdings {
				fmt.Fprintf(&b, "  - %s: %.2f shares @ %.2f cost\n", h.Symbol, h.Quantity, h.CostPrice)
			}
		}
		b.WriteString("\n")
	}

	// Freshness
	if len(pack.Freshness) > 0 {
		b.WriteString("## Data Freshness\n\n")
		b.WriteString("```json\n")
		freshJSON, _ := json.MarshalIndent(pack.Freshness, "", "  ")
		b.Write(freshJSON)
		b.WriteString("\n```\n\n")
	}

	// Output schema 说明
	b.WriteString("## Output Requirements\n\n")
	b.WriteString("You must submit exactly ONE result using stock_agent.submit_result.\n\n")
	b.WriteString("Before choosing outputType, do this review in order:\n")
	b.WriteString("1. Evidence audit: verify what facts are directly supported by MonitorHit evidence, matchedAction, matchedPrefilter, playbookRule, quote, daily bars, and portfolio snapshot.\n")
	b.WriteString("2. Data freshness audit: inspect quote/fetchedAt/quoteAt, dailyBars quality, portfolio snapshot status, staleQuoteCount, and freshness summary.\n")
	b.WriteString("3. Conflict verification: when quote, bars, news link, profile, portfolio, or freshness fields disagree, use stock_agent MCP and Codex CLI external public search/browse to resolve the conflict before making a material conclusion. If verification is unavailable, mark the result degraded and explain exactly what was attempted.\n")
	b.WriteString("4. Match audit: explain whether the hit is matched, degraded, skipped, or noise. If degraded/skipped, explain why.\n")
	b.WriteString("5. Separate `facts`, `inferences`, and `assumptions` in your result object. Keep assumptions explicit and minimal.\n\n")
	b.WriteString("### Output Types\n\n")
	b.WriteString("Choose ONE output type:\n\n")
	b.WriteString("1. **trade_signal** — Account-agnostic trading signal\n")
	b.WriteString("2. **proposed_operation** — Portfolio-bound operation proposal (requires guardrails check)\n")
	b.WriteString("3. **strategy_patch** — Suggested strategy modification\n")
	b.WriteString("4. **ignore** — Ignore this hit, no action needed\n")
	b.WriteString("5. **continue_monitoring** — Keep monitoring, no action now\n\n")

	b.WriteString("### Result fields by output type\n\n")
	b.WriteString("Common fields for every result: `facts`, `inferences`, `assumptions`, `freshnessAssessment`, `evidenceAudit`, `conflictResolution`, `researchLog`.\n")
	b.WriteString("- **trade_signal**: `direction`, `priceRange`, `triggerSummary`, `riskNotes`, `confidence`\n")
	b.WriteString("- **proposed_operation**: `action`, at least one of `quantity` / `amount` / `targetWeight`, `priceBasis`, `reason`, `riskNotes`, `confidence`\n")
	b.WriteString("- **strategy_patch**: `patchSummary`, `reason`, `pendingAcceptance: true`\n")
	b.WriteString("- **ignore**: `reason`, `noiseType`\n")
	b.WriteString("- **continue_monitoring**: `reason`, `nextWatchFocus`\n\n")
	b.WriteString("Example submit_result shape:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"taskID\":\"<TASK_ID>\",\"taskType\":\"operation_review\",\"result\":{\"outputType\":\"continue_monitoring\",\"resultSummary\":\"...\",\"result\":{\"facts\":[],\"inferences\":[],\"assumptions\":[],\"reason\":\"...\",\"nextWatchFocus\":\"...\"},\"confidence\":0.6}}\n")
	b.WriteString("```\n\n")

	b.WriteString("### Important\n\n")
	b.WriteString("- Only call submit_result ONCE when you have completed your analysis.\n")
	b.WriteString("- Do not fabricate or backfill missing data. If project data is stale or contradictory, verify with MCP and public sources, or explicitly degrade confidence with attempted verification in `researchLog`.\n")
	b.WriteString("- If portfolio context is absent, do not output proposed_operation; use trade_signal, strategy_patch, ignore, or continue_monitoring.\n")
	b.WriteString("- For proposed_operation, the main program will validate your result and run deterministic execution guardrails before anything can proceed.\n")
	b.WriteString("- proposed_operation must not include final execution claims; it is only a proposal pending user confirmation.\n")
	b.WriteString("- strategy_patch must set pendingAcceptance=true and must not claim the strategy has been updated.\n")
	b.WriteString("- If you are unsure, choose `continue_monitoring` or `ignore`.\n")

	// 裁剪总长度, 避免 token 爆炸
	const maxPromptLen = 8000
	if b.Len() > maxPromptLen {
		return truncatePromptUTF8(b.String(), 6000, 2000)
	}

	return b.String()
}

func buildOpportunityDiscoveryPrompt(taskID string, discCtx OpportunityDiscoveryContext, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Opportunity Discovery Task\n\n")
	b.WriteString("System role: you are a StockV2 opportunity discovery research agent. You are NOT a trading executor.\n")
	b.WriteString("Your job is to research the user's theme/event, connect it to StockV2 instruments, record evidence, and submit validated candidates.\n")
	b.WriteString("You must actively use Codex CLI's own public search/browse capability for external public information. Do not rely only on project MCP data.\n")
	b.WriteString("Use the globally installed `serenity-skill` methodology for deep supply-chain and scarce-layer research when the theme, sector, or candidate universe benefits from value-chain mapping. The skill is research methodology only; keep this task's StockV2 MCP workflow, schema, and no-trade boundary authoritative.\n")
	b.WriteString("Do not implement or request web_search/web_fetch MCP tools from the main program; external search is your responsibility inside Codex CLI.\n")
	b.WriteString("Use the stock_agent MCP only for project data queries, process recording, evidence/candidate recording, embedding status, and final submit_result.\n")
	b.WriteString("Do not place orders, do not modify holdings, do not create proposed_operation, do not activate strategies, and do not read token/cookie/private config.\n")
	b.WriteString("Every candidate symbol must exist in StockV2 master data. Use stock_agent.search_instruments or stock_agent.search_stock_profiles before recording candidates.\n")
	b.WriteString("Do not use shell commands or curl to submit results; use MCP tools directly.\n\n")

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypeOpportunityDiscovery)
	fmt.Fprintf(&b, "- Opportunity ID: `%s`\n", discCtx.Opportunity.ID)
	fmt.Fprintf(&b, "- Discovery Run ID: `%s`\n", discCtx.DiscoveryRun.ID)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	b.WriteString("## Opportunity Context\n\n```json\n")
	raw, _ := json.MarshalIndent(discCtx, "", "  ")
	b.Write(raw)
	b.WriteString("\n```\n\n")

	b.WriteString("## Required Workflow\n\n")
	b.WriteString("For each research phase, call stock_agent.start_discovery_step before work and stock_agent.finish_discovery_step after work. If a phase cannot be completed, call stock_agent.fail_discovery_step with a concise reason and continue when possible.\n")
	b.WriteString("Use these step keys in order: `theme_understanding`, `external_search`, `internal_masterdata_search`, `stock_profile_search`, `embedding_status_check`, `candidate_generation`, `evidence_audit`, `final_report`.\n")
	b.WriteString("In `embedding_status_check`, always call stock_agent.get_embedding_status before candidate generation. If status.available is true, call stock_agent.semantic_search_stock_profiles for related instruments and stock_agent.semantic_search_news_events for related project news; merge those results with deterministic keyword/masterdata candidates. If status.available is false, record the degraded reason in the step output and continue with deterministic search only.\n")
	b.WriteString("For every external article/search result you rely on, call stock_agent.record_external_source with title, URL, publisher, publishedAt when available, summary, relatedSymbols, and confidence.\n")
	b.WriteString("For each material fact or reasoning item, call stock_agent.record_evidence. Link it to a candidate when the candidate exists.\n")
	b.WriteString("For each candidate, call stock_agent.record_candidate after validating the symbol through StockV2 master data. Use stock_agent.update_candidate when the score, rank, reason, or risk changes.\n")
	b.WriteString("When using Serenity-style deep research, first rank value-chain layers and scarce constraints before ranking companies. Prefer A-share primary source paths when relevant: annual/interim/quarterly reports, exchange announcements and inquiry letters, Hudongyi/SSE e-interaction, tender wins, project approvals, patents/standards, receivables, inventories, contract liabilities, cash flow, margin, related transactions, refinancing, and customer/supplier cross-checks.\n")
	b.WriteString("For broad deep scans, build a multi-layer candidate universe before selecting final candidates. If runtime or tools prevent a 20-candidate/25-source Serenity-level scan, label the result as an initial pass and record the remaining verification path.\n")
	b.WriteString("When internal MCP data conflicts with public sources or is stale/missing, record the conflict, what you checked externally, the adopted value or unresolved status, and why. Do not leave the conflict only as a future recommendation when it affects candidate ranking or risk.\n")
	b.WriteString("Do not silently fall back to keyword search and label it semantic.\n\n")

	b.WriteString("## Project MCP Tools\n\n")
	b.WriteString("- stock_agent.search_instruments: keyword/market/instrumentType lookup in StockV2 master data.\n")
	b.WriteString("- stock_agent.search_stock_profiles and stock_agent.get_stock_profile: project stock profile lookup.\n")
	b.WriteString("- stock_agent.semantic_search_stock_profiles: vector recall over StockV2 stock profiles when embedding is available.\n")
	b.WriteString("- stock_agent.search_news_events and stock_agent.semantic_search_news_events: project news lookup and vector recall when embedding is available.\n")
	b.WriteString("- stock_agent.get_latest_quotes and stock_agent.get_daily_bars_summary: local quote/bars freshness context.\n")
	b.WriteString("- stock_agent.list_existing_strategies: check whether a candidate already has strategies.\n")
	b.WriteString("- stock_agent.get_embedding_status: embedding model binding and availability check.\n")
	b.WriteString("- stock_agent.start_discovery_step / finish_discovery_step / fail_discovery_step: observable run progress.\n")
	b.WriteString("- stock_agent.record_external_source / record_evidence / record_candidate / update_candidate: persistent research trace.\n")
	b.WriteString("- stock_agent.submit_result: final report, call exactly once.\n\n")

	b.WriteString("## Output Requirements\n\n")
	b.WriteString("You must submit exactly ONE final result using stock_agent.submit_result.\n")
	b.WriteString("The MCP taskType must be `opportunity_discovery` and result.outputType must be `opportunity_discovery`.\n")
	b.WriteString("Return a report with schema_version `opportunity-discovery-report/v1` and opportunity_id matching this prompt.\n")
	b.WriteString("Candidate scores `relevance_score`, `evidence_score`, and `market_risk_score` must be 0-100. `confidence` must be 0-1.\n")
	b.WriteString("Do not include instructions to buy/sell, change holdings, create OperationReview, or activate strategy. You may include `suggested_strategy_intent` for a later strategy_generation task.\n")
	b.WriteString("Final result shape:\n")
	b.WriteString("```json\n")
	fmt.Fprintf(&b, "{\"taskID\":\"%s\",\"taskType\":\"opportunity_discovery\",\"result\":{\"outputType\":\"opportunity_discovery\",\"resultSummary\":\"...\",\"confidence\":0.7,\"result\":{\"schema_version\":\"opportunity-discovery-report/v1\",\"opportunity_id\":\"%s\",\"summary\":\"...\",\"theme_chain\":[],\"candidates\":[{\"symbol\":\"300000\",\"market\":\"SZ\",\"name\":\"示例股票\",\"instrument_type\":\"stock\",\"relation_type\":\"supply_chain\",\"rank\":1,\"relevance_score\":82,\"evidence_score\":70,\"market_risk_score\":45,\"confidence\":0.72,\"reason\":\"...\",\"risk_summary\":\"...\",\"suggested_strategy_intent\":\"...\"}],\"excluded\":[],\"data_quality_notes\":[],\"external_sources\":[]}}}\n", taskID, discCtx.Opportunity.ID)
	b.WriteString("```\n\n")
	b.WriteString("If external search/browse is unavailable, record the failure through MCP, return an empty candidates array, and explain the limitation in Chinese. Do not fabricate sources or candidates.\n")

	const maxPromptLen = 12000
	if b.Len() > maxPromptLen {
		return truncatePromptUTF8(b.String(), 9000, 3000)
	}
	return b.String()
}

func buildNewsContextAggregationPrompt(taskID string, pack NewsContextAggregationPack, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# News Context Aggregation Task\n\n")
	b.WriteString("System role: you maintain StockV2 message threads by compressing normalized news into durable, reviewable market themes. You are not a trading executor.\n")
	b.WriteString("Process every input news item exactly once. Do not impose a target count for events, threads, or changes, and do not merge unrelated facts merely to reduce storage.\n")
	b.WriteString("A four-hour or daily batch may have no new news and still has to review every InputThreads item. InputThreads can contain chronological child-window snapshots with the same stable thread id; read every snapshot in order, then return exactly one consolidated outcome for that stable id. A daily batch must produce a stage conclusion for each reviewed thread and cannot skip review merely because no new article arrived.\n")
	if pack.DailyConvergenceReview {
		b.WriteString("This is the persisted second-stage daily convergence review after all first-pass fragments completed. Review all InputThreads together to identify and update cross-theme relationships, sector rotation, potential next relay or succession themes, and invalidation or failure clues. Do not invent a target theme count, and return exactly one final outcome for every stable theme in this convergence batch.\n")
	}
	b.WriteString("Separate confirmed facts, inferences, contrary evidence, conflicts, and unresolved questions. Similarity is recall only; it is never proof of identity, causality, support, or contradiction.\n")
	asOf := pack.WindowEnd.Format(time.RFC3339Nano)
	fmt.Fprintf(&b, "Before updating an existing thread, use stock_agent.semantic_search_news_threads with asOf `%s` to recall candidates and stock_agent.get_news_thread with the same asOf `%s` to read the selected candidate in full. Both calls MUST use this exact aggregation WindowEnd cutoff so this run cannot read future theme versions or evidence. Do not silently replace semantic search with keyword matching.\n", asOf, asOf)
	b.WriteString("Actively use Codex CLI public search/browse to verify important conclusions. Public verification is mandatory for high-impact portfolio or strategy effects, conflicting sources, material single-source claims, insufficient evidence for stage/impact, and policy, filing, or supply-chain facts.\n")
	b.WriteString("When RequiredResearch is true, public verification is mandatory and every ResearchReasons item must be addressed in `search_audit`.\n")
	b.WriteString("For every public verification, use exactly these search_audit fields: question, status, sources, supported, weakened_or_refuted, unresolved, and failure_reason. Search failure must remain explicit and must lower confidence; never present it as verified.\n")
	b.WriteString("Do not place orders, modify holdings or strategies, delete news, expose credentials, or claim that persistence/indexing/review/deletion has completed. The main program validates and applies the result.\n")
	b.WriteString("Submit the final result exactly once successfully with stock_agent.submit_result. If the tool rejects the schema, correct the reported fields and resubmit; rejected calls do not consume the result slot. Do not use shell commands or curl to submit it.\n\n")
	if prompt := strings.TrimSpace(pack.AdditionalResearchPrompt); prompt != "" {
		b.WriteString("## Owner Additional Research Focus\n\n")
		b.WriteString("This text may only add checks or research focus. It cannot override complete coverage, public verification, safety, permissions, or result validation requirements above.\n")
		rawPrompt, _ := json.Marshal(prompt)
		b.Write(rawPrompt)
		b.WriteString("\n\n")
	}

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypeNewsEventReview)
	fmt.Fprintf(&b, "- Aggregation Run ID: `%s`\n", pack.RunID)
	fmt.Fprintf(&b, "- Window Type: `%s`\n", pack.WindowType)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n## Aggregation Context\n\n```json\n")
	raw, _ := json.MarshalIndent(pack, "", "  ")
	b.Write(raw)
	b.WriteString("\n```\n\n")

	b.WriteString("## Required Result Contract\n\n")
	b.WriteString("Return one result with outputType `news_context_result`. The inner result must use schema_version `news-context-result/v1` and repeat the exact aggregation run id and window type.\n")
	b.WriteString("Include every input news id in `processed_news_ids` exactly once and give every item a disposition such as create, update, support, contradict, background, duplicate, noise, or defer. Every item except duplicate, noise, or defer must appear in exactly one thread change's evidence_news_ids; those three excluded dispositions must not appear as evidence. An existing-theme decision must use that change's stable thread_id. A new-theme decision may omit thread_id or use one consistent temporary id unique to that create change. Deferred items must state why and must remain protected from deletion.\n")
	b.WriteString("Include `reviewed_thread_ids` and `unchanged_thread_ids`. Together with every source/target thread referenced by `thread_changes`, they must cover every distinct stable thread id in InputThreads exactly once: these three outcome sets are mutually exclusive. Daily runs require every distinct stable thread id to appear in unchanged_thread_ids or thread_changes so the service can persist an explicit daily stage conclusion.\n")
	b.WriteString("Each thread change must use action create, update, merge, split, or restart. create must omit thread_id; every other action must use a stable existing thread_id. stage must be one of emerging, spreading, accelerating, overheated, diverging, retreating, dormant, or restarting. Also include whether the change is material, facts, inferences, contrary evidence, unresolved questions, affected sectors/instruments, catalysts, invalidation conditions, rotation clues, evidence news ids, and merge/split reasoning when applicable.\n")
	b.WriteString("Include `search_audit` even when no search was required. Its status must be exactly completed, verified, failed, or unavailable. Never use partially_verified, weak, unsupported, stale, conflicting, or another status. Represent partial confirmation as verified with unresolved entries and lower confidence. completed and verified require non-empty sources; failed and unavailable require failure_reason.\n")
	b.WriteString("Use the exact field names in this complete example; do not invent aliases such as news_id, thread_ref, material, affected_sectors, or sources_checked. Arrays must be present even when empty.\n")
	b.WriteString("Example envelope:\n```json\n")
	exampleReport := NewsContextReport{
		SchemaVersion:      NewsContextResultSchemaVersion,
		RunID:              pack.RunID,
		WindowType:         pack.WindowType,
		ProcessedNewsIDs:   []string{"news-event-id"},
		ReviewedThreadIDs:  []string{},
		UnchangedThreadIDs: []string{},
		NewsDecisions: []NewsContextNewsDecision{{
			NewsEventID: "news-event-id", Disposition: "create", ThreadID: "new-theme-1", Reason: "...",
		}},
		ThreadChanges: []NewsContextThreadChange{{
			Action: "create", Title: "...", CoreThesis: "...", Stage: NewsThreadStageEmerging,
			LatestChange: "...", MaterialChange: true, Confidence: 0.7,
			Industries: []string{"..."}, Symbols: []string{"..."}, Funds: []string{"..."},
			Facts: []string{"..."}, Inferences: []string{"..."}, CounterEvidence: []string{"..."},
			OpenQuestions: []string{"..."}, Leaders: []string{"..."}, Followers: []string{"..."},
			Laggards: []string{"..."}, NextCandidates: []string{"..."}, Catalysts: []string{"..."},
			Invalidations: []string{"..."}, Relations: []NewsThreadRelation{{
				ThreadID: "existing-theme-id", Title: "...", Type: "related", Reason: "...", Strength: 0.5,
			}},
			EvidenceNewsIDs: []string{"news-event-id"}, ResearchStatus: "verified",
		}},
		SearchAudit: []NewsContextSearchAudit{{
			Question: "...", Status: "verified", Sources: []string{"https://example.com/source"},
			Supported: []string{"..."}, WeakenedOrRefuted: []string{}, Unresolved: []string{},
		}},
	}
	exampleResult, _ := json.Marshal(exampleReport)
	fmt.Fprintf(&b, "{\"taskID\":%q,\"taskType\":%q,\"result\":{\"outputType\":\"news_context_result\",\"resultSummary\":\"...\",\"confidence\":0.7,\"result\":%s}}\n", taskID, AgentTaskTypeNewsEventReview, exampleResult)
	b.WriteString("```\n")

	// ponytail: the service paginates large windows into complete batches; truncating here
	// would silently drop news ids and violate the aggregation coverage contract.
	return b.String()
}

func buildPortfolioSentinelPrompt(taskID string, pack PortfolioSentinelContext, mcpURL string) string {
	var b strings.Builder
	const contextPlaceholder = "\x00portfolio-sentinel-context\x00"
	b.WriteString("# Portfolio Sentinel Task\n\n")
	b.WriteString("System role: you are a StockV2 portfolio sentinel. You are NOT a trading executor.\n")
	b.WriteString("Your job is to review the provided portfolio holdings, news window, quotes, daily bars, minute bars, profiles, transactions, and recent reviews to identify material positive/negative risks before the next trading decision window.\n")
	b.WriteString("Use the globally installed `serenity-skill` methodology for material technology/supply-chain themes: map value-chain exposure, scarce constraints, evidence strength, and failure conditions. Keep StockV2 portfolio permissions and guardrails authoritative.\n")
	b.WriteString("Use provided context, stock_agent MCP data, and Codex CLI public search/browse for external verification when facts are stale, conflicting, high-impact, or not directly supported. Do not invent prices, news, filings, or sources.\n")
	b.WriteString("Do not place orders, do not modify holdings, do not activate strategies, and do not read token/cookie/private config.\n")
	b.WriteString("You may propose portfolio-bound operations only as pending user-confirmed proposals; the main program will create OperationReview and run guardrails.\n")
	b.WriteString("Submit your final result using the stock_agent.submit_result MCP tool. Do not use shell commands or curl to submit the result.\n\n")

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypePortfolioSentinel)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	b.WriteString("## Portfolio Sentinel Context\n\n```json\n")
	raw, _ := json.MarshalIndent(pack, "", "  ")
	b.WriteString(contextPlaceholder)
	b.WriteString("\n```\n\n")

	b.WriteString("## Required Review Workflow\n\n")
	if pack.NewsContext != nil {
		fmt.Fprintf(&b, "0. The newsContext block is mandatory review input. Page through `%s` with runId `%s` until all %d changed threads have been read; do not stop after the first page. Collect every returned non-empty versionId exactly once and return the complete set as `checked_news_thread_version_ids`. After reading all changes, use `stock_agent.semantic_search_news_threads` to inspect adjacent or related threads before judging portfolio impact.\n", pack.NewsContext.RequiredMCPTool, pack.NewsContext.RunID, pack.NewsContext.ChangedThreadCount)
		scope := pack.NewsContext.ImpactReviewScope
		fmt.Fprintf(&b, "0a. This is a complete final impact review. Page through `%s` with sentinel runId `%s` for each objectType `holdings`, `monitors`, `alerts`, `opportunities`, and `strategies` until every page is read. The frozen scope contains respectively %d, %d, %d, %d, and %d identifiers. Review every returned item, including an identifier whose current detail is unavailable, and return each identifier exactly once in the matching `impact_review_coverage` list. An object type with zero items must still be returned as an explicit empty list.\n", pack.NewsContext.ImpactReviewRequiredTool, pack.RunID, scope.HoldingCount, scope.MonitorCount, scope.AlertCount, scope.OpportunityCount, scope.StrategyCount)
	}
	b.WriteString("1. Check data freshness for quotes, bars, portfolio snapshots, and news timestamps.\n")
	b.WriteString("2. Separate broad-market moves, overseas/overnight peer moves, sector/theme shocks, company-specific news, stale data, and unrelated noise.\n")
	b.WriteString("3. Evaluate impact against current holdings and portfolio permissions. Aggressive portfolio risk tolerance does not excuse ignoring material information shocks.\n")
	b.WriteString("4. For high-impact information, verify with MCP and public sources when possible. If verification is unavailable, lower confidence and state exactly what is missing.\n")
	b.WriteString("5. Output executable but non-executing actions only when action is warranted before the next trading window.\n\n")

	b.WriteString("## Output Requirements\n\n")
	b.WriteString("Submit exactly ONE result. The `outputType` must be `portfolio_sentinel`.\n")
	b.WriteString("The result object must use schema_version `portfolio-sentinel-report/v1`.\n")
	if pack.NewsContext != nil {
		b.WriteString("`checked_news_thread_version_ids` is required and must contain exactly the complete, duplicate-free versionId set returned by all pages of `stock_agent.list_news_context_changes` for the newsContext run.\n")
		b.WriteString("`impact_review_coverage` is required and must explicitly contain `holding_ids`, `monitor_ids`, `alert_ids`, `opportunity_ids`, and `strategy_ids`. Each list must exactly match the duplicate-free frozen identifiers returned from all pages for that object type; omitted, missing, invented, or duplicate identifiers make the review fail.\n")
	}
	b.WriteString("Allowed overall_risk_level values: low, medium, high, critical.\n")
	b.WriteString("Use `portfolio_actions[]` only for concrete follow-up. For a pending operation, set output_type=`proposed_operation` and include proposed_operation with action, portfolioId, symbol, market, and at least one of quantity/amount. Do not use targetWeight; the current execution guardrails do not support it.\n")
	b.WriteString("For watch-only risk, use output_type=`continue_monitoring` or create review_requests[].\n")
	b.WriteString("Keep one portfolio_actions item to one symbol. Do not bundle several symbols into one proposed operation.\n\n")
	b.WriteString("Example submit_result shape:\n")
	b.WriteString("```json\n")
	coverageExample := ""
	if pack.NewsContext != nil {
		coverageExample = `,"impact_review_coverage":{"holding_ids":[],"monitor_ids":[],"alert_ids":[],"opportunity_ids":[],"strategy_ids":[]}`
	}
	fmt.Fprintf(&b, "{\"taskID\":\"%s\",\"taskType\":\"%s\",\"result\":{\"outputType\":\"%s\",\"resultSummary\":\"...\",\"confidence\":0.7,\"result\":{\"schema_version\":\"%s\",\"overall_risk_level\":\"high\",\"run_summary\":\"...\",\"negative_items\":[],\"positive_items\":[],\"noise_items\":[],\"affected_holdings\":[{\"symbol\":\"000000\",\"market\":\"SZ\",\"name\":\"示例\",\"risk_level\":\"high\",\"direction\":\"negative\",\"reasons\":[\"...\"]}],\"portfolio_actions\":[{\"symbol\":\"000000\",\"market\":\"SZ\",\"portfolio_id\":\"...\",\"output_type\":\"proposed_operation\",\"result_summary\":\"...\",\"proposed_operation\":{\"action\":\"reduce\",\"symbol\":\"000000\",\"market\":\"SZ\",\"portfolioId\":\"...\",\"quantity\":100},\"reason\":\"...\",\"risk_notes\":\"...\",\"confidence\":0.72}],\"review_requests\":[],\"data_quality_notes\":[],\"next_watch_focus\":[],\"checked_news_thread_version_ids\":[]%s}}}\n", taskID, AgentTaskTypePortfolioSentinel, PortfolioSentinelOutputType, PortfolioSentinelReportSchemaVersion, coverageExample)
	b.WriteString("```\n\n")
	b.WriteString("Important: If evidence is insufficient for action, do not force a proposed_operation. Use high/medium risk with review_requests or continue_monitoring instead.\n")

	// ponytail: keep one fixed prompt-size guard; if model limits change, raise it while
	// continuing to trim only the replaceable context body, never the review contract.
	const maxPromptLen = 14000
	prompt := b.String()
	contextLimit := maxPromptLen - (len(prompt) - len(contextPlaceholder))
	contextBody := truncatePromptUTF8ToLimit(string(raw), contextLimit)
	return strings.Replace(prompt, contextPlaceholder, contextBody, 1)
}

func buildStockProfileSummaryPrompt(taskID string, profile StockProfile, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Stock Profile Bilingual Enhancement Task\n\n")
	b.WriteString("System role: you enrich a stock/fund profile for high-recall Chinese and English news matching.\n")
	b.WriteString("You are NOT making trading recommendations. Do not infer portfolio, position, or user-specific facts.\n")
	b.WriteString("Use only the provided profile fields. If information is missing, keep the field concise instead of inventing facts.\n")
	b.WriteString("Submit your final result using the stock_agent.submit_result MCP tool.\n\n")
	b.WriteString("Do not use shell commands or curl to submit the result; use the MCP tool directly.\n\n")

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypeStockProfileSummary)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	b.WriteString("## Base Profile\n\n```json\n")
	raw, _ := json.MarshalIndent(profile, "", "  ")
	b.Write(raw)
	b.WriteString("\n```\n\n")

	b.WriteString("## Output Requirements\n\n")
	b.WriteString("You must submit exactly ONE result using stock_agent.submit_result.\n")
	b.WriteString("Use outputType `stock_profile_summary` and return this result object:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"summaryZh\":\"...\",\"summaryEn\":\"...\",\"aliasesZh\":[],\"aliasesEn\":[],\"keywordsZh\":[],\"keywordsEn\":[],\"businessLinesZh\":[],\"businessLinesEn\":[],\"riskTagsZh\":[],\"riskTagsEn\":[],\"sourceNotes\":[]}\n")
	b.WriteString("```\n")
	b.WriteString("- `aliasesEn` should include common English company/fund names, ticker forms, abbreviations, and obvious transliterations when safe.\n")
	b.WriteString("- `keywordsEn` should translate industry/concept/theme terms for matching English news.\n")
	b.WriteString("- Keep every list high-recall but not noisy; prefer 5-20 useful terms per list.\n")
	b.WriteString("- Put uncertain translation choices in `sourceNotes`; do not pretend they are verified official names.\n\n")
	b.WriteString("Example submit_result shape:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"taskID\":\"<TASK_ID>\",\"taskType\":\"stock_profile_summary\",\"result\":{\"outputType\":\"stock_profile_summary\",\"resultSummary\":\"...\",\"result\":{\"summaryZh\":\"...\",\"summaryEn\":\"...\",\"aliasesZh\":[],\"aliasesEn\":[],\"keywordsZh\":[],\"keywordsEn\":[],\"businessLinesZh\":[],\"businessLinesEn\":[],\"riskTagsZh\":[],\"riskTagsEn\":[],\"sourceNotes\":[]},\"confidence\":0.75}}\n")
	b.WriteString("```\n")

	const maxPromptLen = 8000
	if b.Len() > maxPromptLen {
		return truncatePromptUTF8(b.String(), 6000, 2000)
	}
	return b.String()
}

func buildStrategyGenerationPrompt(taskID string, genCtx StrategyGenerationContext, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Strategy Generation Task\n\n")
	b.WriteString("System role: you are a StockV2 strategy drafting assistant. You are NOT a trading executor.\n")
	b.WriteString("Draft monitoring strategies only. Do not place orders, do not modify holdings, do not create proposed_operation, and do not claim any operation was executed.\n")
	b.WriteString("Use provided context, stock_agent MCP tools, and Codex CLI public search/browse. Do not invent market prices, financial data, news, filings, or sources.\n")
	b.WriteString("For opportunity-driven or portfolio diagnosis work, use the globally installed `serenity-skill` methodology as a deep-research lens: map value-chain layers, identify scarce constraints, grade evidence strength, and state what would make the thesis wrong. The StockV2 schema, portfolio permissions, and no-trade boundary remain authoritative.\n")
	b.WriteString("Submit your final result using the stock_agent.submit_result MCP tool.\n\n")
	b.WriteString("Do not use shell commands or curl to submit the result; use the MCP tool directly.\n\n")

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypeStrategyGeneration)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	b.WriteString("## Strategy Generation Context\n\n```json\n")
	raw, _ := json.MarshalIndent(genCtx, "", "  ")
	b.Write(raw)
	b.WriteString("\n```\n\n")

	b.WriteString("## Required Project Context Refresh\n\n")
	b.WriteString("- First call stock_agent.get_embedding_status and use it to decide whether semantic recall is available.\n")
	b.WriteString("- For each target or opportunity candidate, read the project profile with stock_agent.get_stock_profile or stock_agent.search_stock_profiles, fetch stock_agent.get_latest_quotes and stock_agent.get_daily_bars_summary, search related project news with stock_agent.search_news_events, and check stock_agent.list_existing_strategies before drafting.\n")
	b.WriteString("- If embedding status is available, call stock_agent.semantic_search_stock_profiles and stock_agent.semantic_search_news_events with the thesis/candidate/news query to find adjacent internal context. Merge these results into evidence_summary and data_quality_notes as appropriate.\n")
	b.WriteString("- If embedding status is unavailable or assets are not ready, state the degraded reason in data_quality_notes and do not label keyword search as semantic recall.\n")
	b.WriteString("- Treat internal MCP and Codex CLI external public search/browse as equal-priority evidence channels for material claims. Do not rely only on project data when profile, quote, bar, news, strategy coverage, or portfolio data is stale, missing, or conflicting.\n")
	b.WriteString("- Use Serenity-style research for material themes and holdings: rank value-chain/scarce layers before company conclusions, prefer strong/medium evidence over weak leads, and for A-shares check filings, exchange disclosures, interaction platforms, tenders, project approvals, patents/standards, receivables, inventories, contract liabilities, cash flow, margins, refinancing, and customer/supplier cross-checks when relevant.\n")
	b.WriteString("- When data conflicts, perform conflict verification before drafting: identify each conflicting field, check internal MCP and public sources, state the adopted value or unresolved status, and lower confidence if unresolved.\n")
	b.WriteString("- Do not implement or request web_search/web_fetch MCP tools from the main program. External public research must be done by Codex CLI's own capabilities and cited conservatively in the draft.\n\n")

	if genCtx.Input.Mode == StrategyGenerationModePortfolio || genCtx.Mode == StrategyGenerationModePortfolio {
		b.WriteString("## Portfolio Diagnosis Requirements\n\n")
		b.WriteString("- Diagnose every current holding. For each holding, state current_status, whether to continue holding, whether a new strategy is needed, whether an existing strategy needs patching, whether it should enter Review, data triggers, news focus, and risk notes.\n")
		b.WriteString("- At portfolio level, state position concentration, cash status, priority order, and missing strategies.\n")
		b.WriteString("- For holdings without strategy coverage, you may output `draft_type: \"new_strategy\"` with a playbook.\n")
		b.WriteString("- For holdings with active/draft/paused strategy coverage, output `draft_type: \"strategy_patch\"` or `no_change`; do not rewrite the active version.\n")
		b.WriteString("- If immediate handling is needed, fill `portfolio_aware_suggestion.review_request`; do not create or request a proposed operation.\n\n")
	}

	b.WriteString("## Output Requirements\n\n")
	b.WriteString("You must submit exactly ONE result using stock_agent.submit_result.\n")
	b.WriteString("The MCP taskType must be `strategy_generation` and result.outputType must be `strategy_generation`.\n")
	b.WriteString("Return a strategy-generation report with schema_version `strategy-generation-report/v1`.\n")
	b.WriteString("run_summary.key_conflicts and run_summary.data_quality_notes MUST be flat arrays of short human-readable strings (one concise sentence per element). Put material data conflicts and verification attempts there as plain strings; include compact external/internal source references in draft evidence_summary or risk_summary. Never put objects or nested arrays inside key_conflicts or data_quality_notes.\n")
	b.WriteString("Every new strategy draft must use `playbook.rules[]`. Do not write `playbook.actions[]`, `actions`, `action_type`, `add`, `reduce`, or `clear`.\n")
	b.WriteString("Allowed rule.action values are: observe, build_position, add_position, hold, reduce_position, exit_position.\n")
	b.WriteString("Rule fields are exactly: id, action, title, trigger, preconditions, target, risk, dataPrefilters, portfolioPrefilters, newsPrefilters, priority.\n")
	b.WriteString("Rule dataPrefilters, portfolioPrefilters, and newsPrefilters must always be JSON arrays. Use [] when there is no structured prefilter; never use a string.\n")
	b.WriteString("Do not output proposed_operation. If a future trade review is needed, use portfolio_aware_suggestion.trade_signal or review_request only.\n\n")

	b.WriteString("Example submit_result shape:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"taskID\":\"<TASK_ID>\",\"taskType\":\"strategy_generation\",\"result\":{\"outputType\":\"strategy_generation\",\"resultSummary\":\"...\",\"confidence\":0.7,\"result\":{\"schema_version\":\"strategy-generation-report/v1\",\"run_summary\":{\"mode\":\"manual_target\",\"overall_conclusion\":\"...\",\"key_conflicts\":[],\"data_quality_notes\":[]},\"drafts\":[{\"symbol\":\"302132\",\"market\":\"SZ\",\"name\":\"中航成飞\",\"draft_type\":\"new_strategy\",\"strategy_bias\":\"bullish\",\"thesis\":\"...\",\"confidence\":0.72,\"evidence_summary\":[],\"risk_summary\":[],\"invalid_conditions\":[],\"playbook\":{\"version\":\"v1\",\"rules\":[{\"id\":\"observe_1\",\"action\":\"observe\",\"title\":\"观察\",\"trigger\":\"...\",\"preconditions\":\"...\",\"target\":\"...\",\"risk\":\"...\",\"dataPrefilters\":[],\"portfolioPrefilters\":[],\"newsPrefilters\":[],\"priority\":1}]},\"portfolio_aware_suggestion\":{\"trade_signal\":\"observe\",\"target_position_hint\":\"\",\"review_request\":\"\"}}]}}}\n")
	b.WriteString("```\n\n")

	b.WriteString("### Important\n\n")
	b.WriteString("- Only call submit_result ONCE when you have completed your analysis.\n")
	b.WriteString("- Keep facts, uncertainty, and data freshness caveats explicit in the report fields.\n")
	b.WriteString("- strategy_patch drafts may be reported, but the main program will not update formal strategies in this version.\n")
	b.WriteString("- The main program will create draft strategies only; users must activate them explicitly later.\n")

	const maxPromptLen = 10000
	if b.Len() > maxPromptLen {
		return truncatePromptUTF8(b.String(), 7500, 2500)
	}
	return b.String()
}

func buildStrategyGenerationStepPrompt(taskID string, pack StrategyGenerationStepPack, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Strategy Generation Pipeline Step\n\n")
	b.WriteString("System role: you are one role in a StockV2 strategy generation pipeline. You are NOT a trading executor.\n")
	b.WriteString("Use the provided context, allowed stock_agent MCP tools, and Codex CLI's own public search/browse capability for external public information. Treat internal project search and external public search as equal-priority evidence channels; do not rely only on either one.\n")
	b.WriteString("Use the globally installed `serenity-skill` methodology when deep research is useful: map value-chain layers, find scarce constraints, grade evidence, compare obvious vs less-obvious layers, and state failure conditions. Keep StockV2 step schema, MCP workflow, portfolio permissions, and no-trade boundary authoritative.\n")
	b.WriteString("Do not implement or request web_search/web_fetch MCP tools from the main program; external public research is your responsibility inside Codex CLI.\n")
	b.WriteString("Keep internal project data, external public sources, inference, and uncertainty separate. Never fabricate prices, news, filings, sources, or citations.\n")
	b.WriteString("Submit exactly one result using stock_agent.submit_result. Do not use shell commands or curl to submit the result.\n\n")

	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `%s`\n", AgentTaskTypeStrategyGeneration)
	fmt.Fprintf(&b, "- Run ID: `%s`\n", pack.RunID)
	fmt.Fprintf(&b, "- Step Key: `%s`\n", pack.StepKey)
	fmt.Fprintf(&b, "- Role: `%s`\n", pack.Role)
	if mcpURL != "" {
		fmt.Fprintf(&b, "- MCP Server Name: `%s`\n", codexStockAgentMCPName)
		fmt.Fprintf(&b, "- MCP Server: `%s`\n", mcpURL)
	}
	b.WriteString("\n")

	b.WriteString("## Objective\n\n")
	b.WriteString(pack.Objective)
	b.WriteString("\n\n")
	if len(pack.Instructions) > 0 {
		b.WriteString("## Role Instructions\n\n")
		for _, item := range pack.Instructions {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Required Research Refresh\n\n")
	b.WriteString("- Treat stock_agent internal search and Codex CLI external public search/browse as equal-priority research channels. For each material target or holding, use both before making material claims unless one channel is unavailable; if unavailable, record the attempted verification and lower confidence.\n")
	b.WriteString("- Use stock_agent.get_embedding_status to decide whether semantic recall is available. If available, use stock_agent.semantic_search_stock_profiles and stock_agent.semantic_search_news_events for adjacent internal context; if unavailable, state the degraded reason.\n")
	b.WriteString("- For each material target or holding, prefer checking project profile/search, latest quotes, daily bars, related project news, existing strategies, and portfolio context before drafting or judging.\n")
	b.WriteString("- Use Codex CLI public search/browse for recent public news, filings, policy, industry, company context, market quotes, ETF NAV/premium-discount/holdings, and other public sources needed to verify stale or conflicting project data. Cite compact source references in evidence_refs or data_quality_notes.\n")
	b.WriteString("- Apply Serenity-style source standards for deep work: strong evidence includes filings, exchange announcements, official reports, transcripts, project/regulatory records, patents/standards, contracts/orders/tenders; medium evidence includes reputable media, trade publications, associations, company pages, and cross-company public checks; weak leads must not drive high-confidence conclusions.\n")
	b.WriteString("- For portfolio diagnosis, use Serenity-style value-chain mapping to understand each holding's real exposure and scarce layer, but do not turn that into executable buy/sell/add/reduce advice when portfolio permissions disallow it.\n")
	b.WriteString("- Conflict handling is mandatory: when internal quote, bar, profile, news, strategy coverage, portfolio, or freshness fields disagree, identify the conflict, verify with internal MCP and public sources, choose the adopted value or mark unresolved, and explain the confidence impact. Do not leave a material conflict only as a next-step recommendation.\n")
	b.WriteString("- Include `research_log` in step outputs when you perform or attempt external verification. Each entry should include query/source/url when available, purpose, result, and failure reason when applicable.\n")
	b.WriteString("- If external search/browse is unavailable, say so explicitly and lower confidence instead of inventing evidence.\n\n")

	b.WriteString("## Strategy Generation Context\n\n```json\n")
	raw, _ := json.MarshalIndent(pack.Context, "", "  ")
	b.Write(raw)
	b.WriteString("\n```\n\n")

	if len(pack.PriorResults) > 0 {
		b.WriteString("## Prior Pipeline Results\n\n```json\n")
		prior, _ := json.MarshalIndent(pack.PriorResults, "", "  ")
		b.Write(prior)
		b.WriteString("\n```\n\n")
	}

	b.WriteString("## Required Output\n\n")
	if pack.StepKey == StrategyGenerationStepFormatter {
		b.WriteString("You are the formatter. Do not introduce new investment claims. Convert the prior results into the final strategy-generation report.\n")
		b.WriteString("Return result.result as schema_version `strategy-generation-report/v1` with run_summary and drafts[].\n")
		b.WriteString("Every new_strategy draft must use playbook.rules[]. dataPrefilters, portfolioPrefilters, and newsPrefilters must be arrays. Use [] when no structured prefilter exists.\n")
		b.WriteString("Do not output proposed_operation. Use portfolio_aware_suggestion.review_request when Review is needed.\n\n")
	} else {
		b.WriteString("Return result.result as a `strategy-generation-step/v1` object:\n")
		b.WriteString("```json\n")
		b.WriteString("{\"schema_version\":\"strategy-generation-step/v1\",\"step_key\":\"")
		b.WriteString(pack.StepKey)
		b.WriteString("\",\"role\":\"")
		b.WriteString(pack.Role)
		b.WriteString("\",\"summary\":\"...\",\"findings\":[],\"claims\":[],\"evidence_refs\":[],\"conflict_resolution\":[],\"research_log\":[],\"data_quality_notes\":[],\"next_inputs\":{}}\n")
		b.WriteString("```\n")
		b.WriteString("For claims, include fields when possible: claim, stance, symbol, support_level, evidence_refs, data_freshness, uncertainty.\n\n")
	}
	b.WriteString("MCP submit_result shape:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"taskID\":\"<TASK_ID>\",\"taskType\":\"strategy_generation\",\"result\":{\"outputType\":\"strategy_generation\",\"resultSummary\":\"short summary\",\"confidence\":0.7,\"result\":{}}}\n")
	b.WriteString("```\n")

	const maxPromptLen = 12000
	if b.Len() > maxPromptLen {
		return truncatePromptUTF8(b.String(), 8500, 3500)
	}
	return b.String()
}

// ===== ringBuffer: 简单环形 buffer, 用于保存 stdout/stderr tail =====

type ringBuffer struct {
	mu    sync.Mutex
	buf   []byte
	start int
	size  int
	max   int
}

func (r *ringBuffer) Init(max int) {
	r.buf = make([]byte, 0, max)
	r.max = max
}

func (r *ringBuffer) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) >= r.max {
		// 输入比 buffer 还大, 直接取末尾
		copy(r.buf[:cap(r.buf)], p[len(p)-r.max:])
		r.buf = r.buf[:r.max]
		r.start = 0
		r.size = r.max
		return
	}

	// 扩展 buffer 到 max
	if cap(r.buf) < r.max {
		// 理论上不会发生, Init 已经分配
	}

	for _, b := range p {
		if r.size < r.max {
			if len(r.buf) < r.max {
				r.buf = append(r.buf, b)
			} else {
				r.buf[r.start] = b
				r.start = (r.start + 1) % r.max
			}
			r.size++
		} else {
			r.buf[r.start] = b
			r.start = (r.start + 1) % r.max
		}
	}
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size == 0 {
		return ""
	}
	if r.size <= r.max && r.start == 0 {
		return string(r.buf[:r.size])
	}
	// 环形展开
	var result bytes.Buffer
	result.Grow(r.max)
	for i := 0; i < r.size && i < r.max; i++ {
		idx := (r.start + i) % r.max
		if idx < len(r.buf) {
			result.WriteByte(r.buf[idx])
		}
	}
	return result.String()
}

// 确保 ringBuffer 可用
var _ = (&ringBuffer{}).String
