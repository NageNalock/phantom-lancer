package stockv2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

// Codex CLI executor: 启动 codex exec 子进程, watch 输出, 等待 MCP submit_result 或超时。
//
// ponytail: 直接用 os/exec, 不引入 codexclient 依赖, 保持 stockv2 领域边界独立。
// 环境变量用 allowlist 转发, 与 codexclient 的策略对齐但不共享代码。

type AgentExecutorOutput struct {
	StdoutTail    string        `json:"stdoutTail"`    // ~4KB
	StderrTail    string        `json:"stderrTail"`    // ~4KB
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
	execDefaultTimeout = 5 * time.Minute
	stdoutTailMaxBytes = 4 * 1024
	stderrTailMaxBytes = 4 * 1024
	transcriptMaxBytes = 16 * 1024
)

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

	// 超时控制
	timeout := execDefaultTimeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	// 构建命令
	args := []string{"exec", "--json", "--sandbox", "read-only", "--ask-for-approval", "on-request"}
	if modelName != "" {
		args = append(args, "--model", modelName)
	}
	args = append(args, prompt)

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
		doneCh <- nil
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			stderrBuf.Write(line)
			stderrBuf.Write([]byte("\n"))
			transcriptBuf.Write([]byte("stderr: "))
			transcriptBuf.Write(line)
			transcriptBuf.Write([]byte("\n"))
		}
		doneCh <- nil
	}()

	// 同时等: result / 进程退出 / context 取消
	var execErr error
	waitDone := make(chan error, 1)
	go func() {
		<-doneCh
		<-doneCh
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

waitLoop:
	for {
		select {
		case err := <-waitDone:
			execErr = err
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
	if resultReceived && execErr == nil && !timedOut {
		// 再等 10 秒让进程自然结束
		shortCtx, shortCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shortCancel()
		select {
		case err := <-waitDone:
			execErr = err
		case <-shortCtx.Done():
			// 超时 kill
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			<-waitDone
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

	// 脱敏后输出
	stdoutTail := safelog.Text(stdoutBuf.String(), stdoutTailMaxBytes)
	stderrTail := safelog.Text(stderrBuf.String(), stderrTailMaxBytes)
	transcript := safelog.Text(transcriptBuf.String(), transcriptMaxBytes)

	output := &AgentExecutorOutput{
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

// buildOperationReviewPrompt 从 ContextPack 构建 codex exec 的 prompt。
// 结构化呈现上下文, 明确 taskID 和提交方式, 输出 schema 说明。
func buildOperationReviewPrompt(taskID string, pack AgentContextPack, mcpURL string) string {
	var b strings.Builder

	b.WriteString("# Operation Review Task\n\n")
	b.WriteString("You are performing an operation review for a stock monitoring hit.\n")
	b.WriteString("Analyze the provided context and submit your final result using the stock_agent.submit_result tool.\n\n")

	// Task ID + 提交方式
	b.WriteString("## Task Information\n\n")
	fmt.Fprintf(&b, "- Task ID: `%s`\n", taskID)
	fmt.Fprintf(&b, "- Task Type: `operation_review`\n")
	if mcpURL != "" {
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
	b.WriteString("### Output Types\n\n")
	b.WriteString("Choose ONE output type:\n\n")
	b.WriteString("1. **trade_signal** — Account-agnostic trading signal\n")
	b.WriteString("2. **proposed_operation** — Portfolio-bound operation proposal (requires guardrails check)\n")
	b.WriteString("3. **strategy_patch** — Suggested strategy modification\n")
	b.WriteString("4. **ignore** — Ignore this hit, no action needed\n")
	b.WriteString("5. **continue_monitoring** — Keep monitoring, no action now\n\n")

	b.WriteString("### Result fields by output type\n\n")
	b.WriteString("- **trade_signal**: `direction`, `priceRange`, `triggerSummary`, `stopLoss`, `takeProfit`\n")
	b.WriteString("- **proposed_operation**: `action` (buy/sell/reduce/clear), `quantity`, `price`, `amount`\n")
	b.WriteString("- **strategy_patch**: `patchSummary` (description of the patch)\n")
	b.WriteString("- **ignore**: no additional fields\n")
	b.WriteString("- **continue_monitoring**: no additional fields\n\n")

	b.WriteString("### Important\n\n")
	b.WriteString("- Only call submit_result ONCE when you have completed your analysis.\n")
	b.WriteString("- Base your analysis ONLY on the provided context. Do not invent data.\n")
	b.WriteString("- The main program will validate your result and run guardrails for proposed_operation.\n")
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

// ===== ringBuffer: 简单环形 buffer, 用于保存 stdout/stderr tail =====

type ringBuffer struct {
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
