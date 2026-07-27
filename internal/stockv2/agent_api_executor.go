package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	agentAPIMaxTurns             = 16
	agentAPIDeepSeekNewsMaxTurns = 4
	agentAPIDeepSeekSubmitTurns  = 4
	agentAPIDeepSeekMaxTokens    = 32 << 10
	agentAPIResponseSize         = 4 << 20
)

type agentAPIExecutionOptions struct {
	toolNames          []string
	submitResultSchema map[string]any
	forceSubmit        bool
}

type agentAPIExecutor struct {
	service *Service
}

func newAgentAPIExecutor(service *Service) *agentAPIExecutor {
	return &agentAPIExecutor{service: service}
}

func (e *agentAPIExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildOperationReviewPrompt(taskID, pack, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

func (e *agentAPIExecutor) ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildStrategyGenerationPrompt(taskID, pack, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

func (e *agentAPIExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildStrategyGenerationStepPrompt(taskID, pack, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

func (e *agentAPIExecutor) ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildOpportunityDiscoveryPrompt(taskID, pack, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

func (e *agentAPIExecutor) ExecuteNewsContextAggregation(ctx context.Context, taskID string, pack NewsContextAggregationPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	toolNames := []string{codexSubmitResultTool}
	if len(pack.InputNewsEvents) > 0 {
		toolNames = []string{mcpToolSemanticSearchNewsThreads, mcpToolGetNewsThread, codexSubmitResultTool}
	}
	return e.executePrompt(
		ctx, taskID, buildNewsContextAggregationPrompt(taskID, pack, ""), modelName, reasoningEffort,
		newsContextAgentTimeout, agentAPIExecutionOptions{
			toolNames:          toolNames,
			submitResultSchema: agentAPINewsContextSubmitResultSchema(taskID, pack),
			forceSubmit:        len(toolNames) == 1,
		},
	)
}

func (e *agentAPIExecutor) ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildPortfolioSentinelPrompt(taskID, pack, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

func (e *agentAPIExecutor) ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	return e.executePrompt(ctx, taskID, buildStockProfileSummaryPrompt(taskID, profile, ""), modelName, reasoningEffort, execDefaultTimeout, agentAPIExecutionOptions{})
}

type agentAPIChatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role             string             `json:"role"`
			Content          string             `json:"content"`
			ReasoningContent string             `json:"reasoning_content,omitempty"`
			ToolCalls        []agentAPIToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

type agentAPIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (e *agentAPIExecutor) executePrompt(
	ctx context.Context,
	taskID, prompt, modelName, reasoningEffort string,
	timeout time.Duration,
	options agentAPIExecutionOptions,
) (*AgentExecutorOutput, error) {
	if e == nil || e.service == nil {
		return nil, ErrAgentExecutorUnavailable
	}
	entry, ok := e.service.agentTaskPool.getTask(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}
	run, err := e.service.store.GetAgentRun(ctx, entry.agentRunID)
	if err != nil {
		return nil, err
	}
	provider, err := e.service.store.GetAgentProviderProfile(ctx, run.ProviderID)
	if err != nil {
		return nil, err
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(provider)
	if err != nil {
		return nil, err
	}
	deepSeek := isDeepSeekAPI(baseURL, modelName)

	prompt = agentAPIModePrompt(prompt)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	systemPrompt := "You execute one StockV2 analysis task. Use the provided functions for project data and submit the final structured result with stock_agent_submit_result. Do not claim access to Codex CLI browsing in API mode."
	if deepSeek {
		// ponytail: DeepSeek JSON Output requires an explicit JSON instruction and
		// example. Final persistence still crosses the validated submit tool.
		systemPrompt += ` DeepSeek JSON mode is enabled. Any assistant message content must be exactly one JSON object, for example {"message":"continuing_with_tool_calls"}. Do not put the final task result only in message content; call stock_agent_submit_result.`
	}
	messages := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": prompt + "\n\nAPI execution mode: call the provided OpenAI functions. Function names use underscores instead of dots; stock_agent_submit_result is the required final submission."},
	}
	tools := agentAPITools(options.toolNames, options.submitResultSchema)
	maxTurns := agentAPIMaxTurns
	if deepSeek && run.TaskType == AgentTaskTypeNewsEventReview {
		maxTurns = agentAPIDeepSeekNewsMaxTurns
		if options.forceSubmit {
			maxTurns = agentAPIDeepSeekSubmitTurns
		}
	}
	output := &AgentExecutorOutput{
		Command:  "POST " + safelog.URLLabel(strings.TrimRight(baseURL, "/")+"/chat/completions"),
		Prompt:   prompt,
		ExitCode: -1,
	}
	var transcript strings.Builder
	lastToolError := ""

	for turn := 0; turn < maxTurns; turn++ {
		body := map[string]any{
			"model":       modelName,
			"messages":    messages,
			"tools":       tools,
			"tool_choice": "auto",
			"stream":      false,
		}
		applyAgentAPIReasoning(body, deepSeek, reasoningEffort)
		if deepSeek {
			applyDeepSeekAPICompatibility(body, reasoningEffort)
		}
		if deepSeek && run.TaskType == AgentTaskTypeNewsEventReview &&
			!deepSeekThinkingEnabled(reasoningEffort) &&
			(options.forceSubmit || turn >= maxTurns-2) {
			body["tool_choice"] = agentAPIRequiredSubmitToolChoice()
		}
		response, requestCount, err := e.chatCompletion(execCtx, baseURL, apiKey, body)
		output.RequestCount += requestCount
		if err != nil {
			output.Duration = time.Since(started)
			output.StderrTail = safelog.Text(err.Error(), stderrTailMaxBytes)
			output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
			output.TimedOut = errors.Is(execCtx.Err(), context.DeadlineExceeded)
			return output, err
		}
		output.PromptTokens += response.Usage.PromptTokens
		output.CachedTokens += response.Usage.PromptCacheHitTokens
		output.OutputTokens += response.Usage.CompletionTokens
		if len(response.Choices) == 0 {
			return output, errors.New("OpenAI-compatible response has no choices")
		}
		choice := response.Choices[0]
		assistant := map[string]any{
			"role":       "assistant",
			"content":    choice.Message.Content,
			"tool_calls": choice.Message.ToolCalls,
		}
		// DeepSeek requires the complete reasoning_content to be echoed after a
		// thinking-mode tool call.
		if choice.Message.ReasoningContent != "" {
			assistant["reasoning_content"] = choice.Message.ReasoningContent
		}
		messages = append(messages, assistant)
		if choice.Message.Content != "" {
			transcript.WriteString(choice.Message.Content)
			transcript.WriteByte('\n')
		}
		if len(choice.Message.ToolCalls) == 0 {
			if deepSeek {
				if content, ok := agentAPIJSONSubmissionContent(choice.Message.Content); ok {
					params, paramsErr := agentAPIToolCallParams(codexSubmitResultTool, content, taskID)
					if paramsErr == nil {
						toolResult, toolErr := e.service.mcpToolsCall(params)
						if toolErr == nil {
							data, _ := json.Marshal(toolResult)
							fmt.Fprintf(&transcript, "content %s: %s\n", agentAPIToolName(codexSubmitResultTool), safelog.Text(string(data), 1000))
							output.ExitCode = 0
							output.Duration = time.Since(started)
							output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
							output.StdoutTail = safelog.Text(string(data), stdoutTailMaxBytes)
							return output, nil
						}
						lastToolError = safelog.Text(agentAPIToolName(codexSubmitResultTool)+": "+toolErr.Message, stderrTailMaxBytes)
					} else {
						lastToolError = safelog.Text(agentAPIToolName(codexSubmitResultTool)+": "+paramsErr.Message, stderrTailMaxBytes)
					}
					output.Duration = time.Since(started)
					output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
					output.StdoutTail = safelog.Text(choice.Message.Content, stdoutTailMaxBytes)
					output.StderrTail = lastToolError
					return output, fmt.Errorf("API model stopped with %q without submitting a valid result: %s", choice.FinishReason, lastToolError)
				}
			}
			if deepSeek && run.TaskType == AgentTaskTypeNewsEventReview && turn < maxTurns-1 {
				// ponytail: DeepSeek documents that JSON Output can occasionally
				// return empty content. Keep the retry inside this cached
				// conversation instead of restarting the full news batch.
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": `The previous response did not submit the task result. Continue this same task now and call stock_agent_submit_result with one valid JSON result covering the complete requested batch.`,
				})
				continue
			}
			output.Duration = time.Since(started)
			output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
			output.StdoutTail = safelog.Text(choice.Message.Content, stdoutTailMaxBytes)
			return output, fmt.Errorf("API model stopped with %q without submitting a result", choice.FinishReason)
		}

		for _, call := range choice.Message.ToolCalls {
			originalName, ok := agentAPIToolOriginalName(call.Function.Name)
			var toolResult any
			var toolErr *mcpError
			if !ok {
				toolErr = &mcpError{Code: mcpErrMethodNotFound, Message: "unknown function"}
			} else {
				params, paramsErr := agentAPIToolCallParams(originalName, call.Function.Arguments, taskID)
				if paramsErr != nil {
					toolErr = paramsErr
				} else {
					toolResult, toolErr = e.service.mcpToolsCall(params)
				}
			}
			content := ""
			if toolErr != nil {
				lastToolError = safelog.Text(call.Function.Name+": "+toolErr.Message, stderrTailMaxBytes)
				data, _ := json.Marshal(map[string]any{"error": toolErr.Message, "code": toolErr.Code})
				content = string(data)
			} else {
				data, _ := json.Marshal(toolResult)
				content = string(data)
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": call.ID,
				"content":      content,
			})
			fmt.Fprintf(&transcript, "tool %s: %s\n", call.Function.Name, safelog.Text(content, 1000))
			if originalName == codexSubmitResultTool && toolErr == nil {
				output.ExitCode = 0
				output.Duration = time.Since(started)
				output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
				output.StdoutTail = safelog.Text(content, stdoutTailMaxBytes)
				return output, nil
			}
		}
	}
	output.Duration = time.Since(started)
	output.RawTranscript = safelog.Text(transcript.String(), transcriptMaxBytes)
	if lastToolError != "" {
		output.StderrTail = lastToolError
		return output, fmt.Errorf("API model exceeded %d tool-call turns without submitting a result; last tool error: %s", maxTurns, lastToolError)
	}
	return output, fmt.Errorf("API model exceeded %d tool-call turns without submitting a result", maxTurns)
}

func agentAPIJSONSubmissionContent(content string) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &object) != nil || object == nil {
		return "", false
	}
	// ponytail: accept only the exact submit-tool envelope. JSON-mode status
	// messages and arbitrary model objects remain non-submissions, while the
	// accepted path still crosses the normal MCP validation and persistence gate.
	if len(object["taskType"]) == 0 || len(object["result"]) == 0 {
		return "", false
	}
	return content, true
}

func agentAPIToolCallParams(name, arguments, taskID string) ([]byte, *mcpError) {
	argumentBytes := bytes.TrimSpace([]byte(arguments))
	var raw json.RawMessage
	err := json.Unmarshal(argumentBytes, &raw)
	// ponytail: DeepSeek occasionally appends one separator after an otherwise
	// complete tool-call object. Accept only that exact, lossless repair; every
	// other malformed payload still goes back to the model for correction.
	if err != nil && len(argumentBytes) > 0 && argumentBytes[len(argumentBytes)-1] == ',' {
		candidate := bytes.TrimSpace(argumentBytes[:len(argumentBytes)-1])
		if candidateErr := json.Unmarshal(candidate, &raw); candidateErr == nil {
			err = nil
		}
	}
	if err != nil {
		// ponytail: return only the parser position and byte count. The model gets
		// enough feedback to correct its next call without logging argument data.
		return nil, &mcpError{
			Code:    mcpErrInvalidParams,
			Message: fmt.Sprintf("arguments must be valid JSON (%d bytes): %s", len(arguments), safelog.Text(err.Error(), 240)),
		}
	}
	if name == codexSubmitResultTool && strings.TrimSpace(taskID) != "" {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && object != nil {
			// ponytail: task identity belongs to the executor trust boundary, not
			// to model output. Always bind submissions to the task being executed.
			object["taskID"], _ = json.Marshal(taskID)
			raw, _ = json.Marshal(object)
		}
	}
	params, err := json.Marshal(map[string]any{"name": name, "arguments": raw})
	if err != nil {
		return nil, &mcpError{Code: mcpErrInvalidParams, Message: "could not encode tool arguments"}
	}
	return params, nil
}

func applyAgentAPIReasoning(body map[string]any, deepSeek bool, reasoningEffort string) {
	effort := strings.ToLower(strings.TrimSpace(reasoningEffort))
	if effort == "" {
		return
	}
	if !deepSeek {
		body["reasoning_effort"] = effort
		return
	}
	if effort == AgentReasoningEffortLow {
		// ponytail: DeepSeek maps low to high while thinking is enabled. The
		// existing low binding is the only compatible way to request its binary
		// non-thinking mode without adding a second provider-specific setting.
		body["thinking"] = map[string]string{"type": "disabled"}
		return
	}
	body["thinking"] = map[string]string{"type": "enabled"}
	if effort == AgentReasoningEffortXHigh || effort == AgentReasoningEffortMax {
		body["reasoning_effort"] = "max"
		return
	}
	body["reasoning_effort"] = "high"
}

func applyDeepSeekAPICompatibility(body map[string]any, reasoningEffort string) {
	body["response_format"] = map[string]string{"type": "json_object"}
	body["max_tokens"] = agentAPIDeepSeekMaxTokens
	if deepSeekThinkingEnabled(reasoningEffort) {
		// DeepSeek V4 thinking mode supports tools but rejects tool_choice,
		// including the otherwise harmless "auto" value.
		delete(body, "tool_choice")
	}
}

func deepSeekThinkingEnabled(reasoningEffort string) bool {
	// DeepSeek defaults to thinking mode when the field is omitted. This project
	// maps only the existing low option to its binary non-thinking mode.
	return strings.ToLower(strings.TrimSpace(reasoningEffort)) != AgentReasoningEffortLow
}

func isDeepSeekAPI(baseURL, modelName string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "deepseek-") {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "deepseek.com" || strings.HasSuffix(host, ".deepseek.com")
}

func agentAPIRequiredSubmitToolChoice() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]string{
			"name": agentAPIToolName(codexSubmitResultTool),
		},
	}
}

func agentAPIModePrompt(prompt string) string {
	// The shared task prompts describe the richer CLI surface. This explicit
	// tail is authoritative for API runs and prevents false browsing claims.
	return prompt + "\n\n## API mode capability boundary\n" +
		"This run has no Codex CLI, shell, browser, web search, or web fetch capability. " +
		"Use only the supplied context and OpenAI functions. If external verification would be required, record it as unavailable and reduce confidence; never fabricate verification or sources. " +
		"Call stock_agent_submit_result for the final submission.\n"
}

func (e *agentAPIExecutor) chatCompletion(ctx context.Context, baseURL, apiKey string, body map[string]any) (agentAPIChatResponse, int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return agentAPIChatResponse{}, 0, err
	}
	client := *e.service.agentHTTPClient()
	client.Timeout = 0
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return agentAPIChatResponse{}, attempt - 1, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if attempt < 3 && ctx.Err() == nil {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return agentAPIChatResponse{}, attempt, fmt.Errorf("API request failed: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, agentAPIResponseSize))
		_ = resp.Body.Close()
		if readErr != nil {
			return agentAPIChatResponse{}, attempt, fmt.Errorf("read API response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := safelog.Text(string(data), 1200)
			if attempt < 3 && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && ctx.Err() == nil {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return agentAPIChatResponse{}, attempt, fmt.Errorf("API returned HTTP %d: %s", resp.StatusCode, message)
		}
		var response agentAPIChatResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return agentAPIChatResponse{}, attempt, fmt.Errorf("decode API response: %w", err)
		}
		return response, attempt, nil
	}
	return agentAPIChatResponse{}, 3, errors.New("API request retries exhausted")
}

func agentAPITools(names []string, submitResultSchema map[string]any) []map[string]any {
	if len(names) == 0 {
		names = stockAgentMCPRequiredTools()
	}
	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		parameters := stockAgentMCPToolInputSchema(name)
		if name == codexSubmitResultTool && submitResultSchema != nil {
			parameters = submitResultSchema
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        agentAPIToolName(name),
				"description": stockAgentMCPToolDescription(name),
				"parameters":  parameters,
			},
		})
	}
	return tools
}

func agentAPIToolName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

func agentAPIToolOriginalName(name string) (string, bool) {
	for _, original := range stockAgentMCPRequiredTools() {
		if agentAPIToolName(original) == name {
			return original, true
		}
	}
	return "", false
}
