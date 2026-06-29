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
	execDefaultTimeout         = 5 * time.Minute
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
	var execErr error
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
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
		case err := <-waitDone:
			execErr = err
			processDone = true
			readerErrs = append(readerErrs, waitForExecutorReaders(doneCh, 2, executorReaderDrainTimeout)...)
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
		case err := <-waitDone:
			execErr = err
			processDone = true
			readerErrs = append(readerErrs, waitForExecutorReaders(doneCh, 2, executorReaderDrainTimeout)...)
		case <-shortCtx.Done():
			// 超时 kill
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			execErr = <-waitDone
			processDone = true
			readerErrs = append(readerErrs, waitForExecutorReaders(doneCh, 2, executorReaderDrainTimeout)...)
		}
	}
	if timedOut && !processDone {
		select {
		case err := <-waitDone:
			execErr = err
			processDone = true
			readerErrs = append(readerErrs, waitForExecutorReaders(doneCh, 2, executorReaderDrainTimeout)...)
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
	b.WriteString("Use only the provided context. Do not invent market prices, financial data, news, filings, or sources.\n")
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
	b.WriteString("3. Match audit: explain whether the hit is matched, degraded, skipped, or noise. If degraded/skipped, explain why.\n")
	b.WriteString("4. Separate `facts`, `inferences`, and `assumptions` in your result object. Keep assumptions explicit and minimal.\n\n")
	b.WriteString("### Output Types\n\n")
	b.WriteString("Choose ONE output type:\n\n")
	b.WriteString("1. **trade_signal** — Account-agnostic trading signal\n")
	b.WriteString("2. **proposed_operation** — Portfolio-bound operation proposal (requires guardrails check)\n")
	b.WriteString("3. **strategy_patch** — Suggested strategy modification\n")
	b.WriteString("4. **ignore** — Ignore this hit, no action needed\n")
	b.WriteString("5. **continue_monitoring** — Keep monitoring, no action now\n\n")

	b.WriteString("### Result fields by output type\n\n")
	b.WriteString("Common fields for every result: `facts`, `inferences`, `assumptions`, `freshnessAssessment`, `evidenceAudit`.\n")
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
	b.WriteString("- Base your analysis ONLY on the provided context. Do not fabricate or backfill missing data.\n")
	b.WriteString("- If portfolio context is absent, do not output proposed_operation; use trade_signal, strategy_patch, ignore, or continue_monitoring.\n")
	b.WriteString("- For proposed_operation, the main program will validate your result and run deterministic execution guardrails before anything can proceed.\n")
	b.WriteString("- proposed_operation must not include final execution claims; it is only a proposal pending user confirmation.\n")
	b.WriteString("- strategy_patch must set pendingAcceptance=true and must not claim the strategy has been updated.\n")
	b.WriteString("- If you are unsure, choose `continue_monitoring` or `ignore`.\n")

	// 裁剪总长度, 避免 token 爆炸
	const maxPromptLen = 8000
	if b.Len() > maxPromptLen {
		// 简单截断: 保留前 6000 字符 + 后 2000 字符
		// ponytail: 不做复杂的智能裁剪, 直接截断并加标记
		result := b.String()
		return result[:6000] + "\n... [truncated]\n...\n" + result[len(result)-2000:]
	}

	return b.String()
}

func buildOpportunityDiscoveryPrompt(taskID string, discCtx OpportunityDiscoveryContext, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Opportunity Discovery Task\n\n")
	b.WriteString("System role: you are a StockV2 opportunity discovery research agent. You are NOT a trading executor.\n")
	b.WriteString("Your job is to research the user's theme/event, connect it to StockV2 instruments, record evidence, and submit validated candidates.\n")
	b.WriteString("You must actively use Codex CLI's own public search/browse capability for external public information. Do not rely only on project MCP data.\n")
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
		result := b.String()
		return result[:9000] + "\n... [truncated]\n...\n" + result[len(result)-3000:]
	}
	return b.String()
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
		result := b.String()
		return result[:6000] + "\n... [truncated]\n...\n" + result[len(result)-2000:]
	}
	return b.String()
}

func buildStrategyGenerationPrompt(taskID string, genCtx StrategyGenerationContext, mcpURL string) string {
	var b strings.Builder
	b.WriteString("# Strategy Generation Task\n\n")
	b.WriteString("System role: you are a StockV2 strategy drafting assistant. You are NOT a trading executor.\n")
	b.WriteString("Draft monitoring strategies only. Do not place orders, do not modify holdings, do not create proposed_operation, and do not claim any operation was executed.\n")
	b.WriteString("Use only the provided context. Do not invent market prices, financial data, news, filings, or sources.\n")
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
	b.WriteString("- Do not implement or request web_search/web_fetch MCP tools from the main program. External public research, when needed, must be done by Codex CLI's own capabilities and cited conservatively in the draft.\n\n")

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
	b.WriteString("Every new strategy draft must use `playbook.rules[]`. Do not write `playbook.actions[]`, `actions`, `action_type`, `add`, `reduce`, or `clear`.\n")
	b.WriteString("Allowed rule.action values are: observe, build_position, add_position, hold, reduce_position, exit_position.\n")
	b.WriteString("Rule fields are exactly: id, action, title, trigger, preconditions, target, risk, dataPrefilters, portfolioPrefilters, newsPrefilters, priority.\n")
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
		result := b.String()
		return result[:7500] + "\n... [truncated]\n...\n" + result[len(result)-2500:]
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
