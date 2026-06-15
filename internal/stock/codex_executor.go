package stock

import (
	"context"
	"errors"
	"strings"
	"time"

	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/storage"
)

const stockCodexExecTimeout = 2 * time.Minute

type codexExecSettings struct {
	Enabled             bool
	ExecFallbackEnabled bool
	BinaryPath          string
	CodexHome           string
	DefaultModel        string
}

type CodexCLIExecutor struct {
	store  *storage.Store
	client *codexclient.ExecClient
	mapper *codexclient.EventMapper
}

func NewCodexCLIExecutor(store *storage.Store) *CodexCLIExecutor {
	return &CodexCLIExecutor{
		store:  store,
		client: codexclient.NewExecClient(),
		mapper: codexclient.NewEventMapper(1200),
	}
}

func (e *CodexCLIExecutor) ExecuteStockReview(ctx context.Context, input AgentExecutionInput) (AgentExecutionResult, error) {
	result := AgentExecutionResult{
		StepKey: "agent_executor",
		Role:    input.Profile.Provider,
		Status:  "failed",
		Summary: "Codex CLI executor 未完成，已回落到 system guardrails 输出",
	}
	if input.Profile.Provider != "codex_cli" {
		result.ErrorSummary = "unsupported executor provider"
		result.OutputJSON = mustJSON(map[string]any{"status": result.Status, "error": result.ErrorSummary})
		return result, errors.New(result.ErrorSummary)
	}
	settings, err := e.loadSettings(ctx)
	if err != nil {
		result.ErrorSummary = codexclient.Redact(err.Error(), 200)
		result.OutputJSON = mustJSON(map[string]any{"status": result.Status, "error": result.ErrorSummary})
		return result, err
	}
	if !settings.Enabled || !settings.ExecFallbackEnabled {
		result.ErrorSummary = "codex exec fallback is disabled"
		result.OutputJSON = mustJSON(map[string]any{"status": result.Status, "error": result.ErrorSummary})
		return result, errors.New(result.ErrorSummary)
	}
	detector := codexclient.NewDetector(func() string { return settings.BinaryPath }, func() string { return settings.CodexHome })
	binary := detector.ResolveBinary()
	if binary == "" {
		result.ErrorSummary = "codex binary not found in PATH"
		result.OutputJSON = mustJSON(map[string]any{"status": result.Status, "error": result.ErrorSummary})
		return result, errors.New(result.ErrorSummary)
	}

	model := strings.TrimSpace(input.Profile.Model)
	if model == "" || model == "default" {
		model = settings.DefaultModel
	}
	approval := "never"
	if input.Profile.AuthMode == "confirm_required" {
		approval = "on-request"
	}
	prompt := buildCodexStockReviewPrompt(input)
	result.Prompt = codexclient.Preview(prompt, 12000)
	result.InputJSON = mustJSON(map[string]any{
		"review_id":      input.ReviewID,
		"alert_id":       input.AlertID,
		"strategy_id":    input.StrategyID,
		"symbol":         input.Symbol,
		"provider":       input.Profile.Provider,
		"model":          model,
		"sandbox":        "read-only",
		"approval":       approval,
		"prompt_preview": codexclient.Preview(prompt, 1200),
	})
	started := time.Now()
	var messages []string
	var eventSummaries []map[string]any
	terminalStatus := ""
	runCtx, cancel := context.WithTimeout(ctx, stockCodexExecTimeout)
	defer cancel()
	err = e.client.Run(runCtx, codexclient.ExecOptions{
		Binary:    binary,
		CodexHome: settings.CodexHome,
		Sandbox:   "read-only",
		Approval:  approval,
		Model:     model,
		Prompt:    prompt,
	}, func(line []byte) {
		mapped, ok := e.mapper.MapExecLine(line)
		if !ok {
			return
		}
		if mapped.TurnStatus != "" {
			terminalStatus = mapped.TurnStatus
		}
		if mapped.TextPreview != "" && mapped.EventType == codexclient.EventMessageAgent {
			messages = append(messages, mapped.TextPreview)
		}
		if len(eventSummaries) < 24 {
			eventSummaries = append(eventSummaries, map[string]any{
				"type":    mapped.EventType,
				"item":    mapped.ItemType,
				"preview": codexclient.Preview(mapped.TextPreview, 300),
			})
		}
	})
	result.LatencyMs = int(time.Since(started).Milliseconds())
	result.TokenEstimate = estimateRunesAsTokens(prompt)
	if len(messages) > 0 {
		result.TokenEstimate += estimateRunesAsTokens(strings.Join(messages, "\n"))
		result.OutputSnapshot = codexclient.Preview(strings.Join(messages, "\n\n"), 16000)
	} else {
		result.OutputSnapshot = "Codex CLI executor completed without an agent message"
	}
	result.ToolCallsJSON = mustJSON([]map[string]any{{
		"name":        "codex.exec",
		"mode":        "jsonl",
		"sandbox":     "read-only",
		"approval":    approval,
		"event_count": len(eventSummaries),
	}})
	if err != nil {
		result.ErrorSummary = codexclient.Redact(err.Error(), 240)
		result.OutputJSON = mustJSON(map[string]any{
			"status":          "failed",
			"terminal_status": terminalStatus,
			"agent_messages":  messages,
			"events":          eventSummaries,
			"error":           result.ErrorSummary,
		})
		return result, err
	}
	result.Status = "completed"
	result.Summary = "Codex CLI executor 已完成，只读输出作为 Review 辅助审计留痕"
	result.OutputJSON = mustJSON(map[string]any{
		"status":          "completed",
		"terminal_status": terminalStatus,
		"agent_messages":  messages,
		"events":          eventSummaries,
	})
	return result, nil
}

func (e *CodexCLIExecutor) loadSettings(ctx context.Context) (codexExecSettings, error) {
	settings := codexExecSettings{Enabled: true, ExecFallbackEnabled: true}
	values, err := e.store.GetSettingsByPrefix(ctx, "codex_cli.")
	if err != nil {
		return settings, err
	}
	if value, ok := values["codex_cli.enabled"]; ok {
		settings.Enabled = stockSettingBool(value)
	}
	if value, ok := values["codex_cli.exec_fallback_enabled"]; ok {
		settings.ExecFallbackEnabled = stockSettingBool(value)
	}
	settings.BinaryPath = strings.TrimSpace(values["codex_cli.binary_path"])
	settings.CodexHome = strings.TrimSpace(values["codex_cli.codex_home"])
	settings.DefaultModel = strings.TrimSpace(values["codex_cli.default_model"])
	return settings, nil
}

func stockSettingBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func buildCodexStockReviewPrompt(input AgentExecutionInput) string {
	return strings.Join([]string{
		"你是股票 Agent 工作台的 Codex CLI Review 执行者。",
		"只读分析，不要修改文件，不要调用会产生交易或外部副作用的工具。",
		"不要输出隐藏思维链。只输出一个 JSON 对象和极短中文摘要。",
		"你需要验证 system guardrails 已生成的 operation-review-report/v1 是否存在明显问题。",
		"输出 JSON 字段: validation(agree|challenge|insufficient_data), summary, evidence_notes, risk_notes, suggested_watch_action, strategy_patch_needed。",
		"禁止在未绑定账户时输出数量、金额或目标仓位；账户绑定操作必须尊重 guardrails。",
		"当前协议: " + input.Protocol,
		"",
		"Prompt:",
		input.Prompt,
		"",
		"operation-review-input/v1:",
		limitText(input.InputJSON, 20000),
		"",
		"system deterministic output:",
		limitText(input.DeterministicOutputJSON, 12000),
	}, "\n")
}

func estimateRunesAsTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	tokens := runes / 3
	if tokens < 1 {
		return 1
	}
	return tokens
}
