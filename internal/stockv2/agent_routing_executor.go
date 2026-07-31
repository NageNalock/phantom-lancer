package stockv2

import (
	"context"
	"fmt"
)

type agentRoutingExecutor struct {
	service *Service
	cli     *codexCLIExecutor
	api     *agentAPIExecutor
}

func (e *agentRoutingExecutor) execution(ctx context.Context, taskID string) (string, *codexCLIExecutor, error) {
	entry, ok := e.service.agentTaskPool.getTask(taskID)
	if !ok {
		return "", nil, ErrTaskNotFound
	}
	run, err := e.service.store.GetAgentRun(ctx, entry.agentRunID)
	if err != nil {
		return "", nil, err
	}
	mode := run.ExecutionMode
	if mode == "" {
		mode = AgentExecutionModeCLI
	}
	if !validAgentExecutionMode(mode) {
		return "", nil, fmt.Errorf("%w: %s", ErrInvalidAgentExecutionMode, mode)
	}
	if mode == AgentExecutionModeAPI {
		return mode, nil, nil
	}
	if e.cli == nil {
		return "", nil, ErrAgentExecutorUnavailable
	}
	provider, err := e.service.store.GetAgentProviderProfile(ctx, run.ProviderID)
	if err != nil {
		return "", nil, err
	}
	proxyBaseURL := ""
	if !isDefaultCodexCLIProvider(provider) {
		proxyBaseURL, err = e.service.agentCodexCLIProxyBaseURL(provider.ID)
		if err != nil {
			return "", nil, err
		}
	}
	cli, err := e.cli.forProvider(provider, proxyBaseURL)
	if err != nil {
		return "", nil, err
	}
	return mode, cli, nil
}

func (e *agentRoutingExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteOperationReview(ctx, taskID, pack, modelName, reasoningEffort)
	} else {
		return cli.ExecuteOperationReview(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStrategyGeneration(ctx, taskID, pack, modelName, reasoningEffort)
	} else {
		return cli.ExecuteStrategyGeneration(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, reasoningEffort)
	} else {
		return cli.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteOpportunityDiscovery(ctx, taskID, pack, modelName, reasoningEffort)
	} else {
		return cli.ExecuteOpportunityDiscovery(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecuteNewsContextAggregation(ctx context.Context, taskID string, pack NewsContextAggregationPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName, reasoningEffort)
	} else {
		return cli.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode != AgentExecutionModeCLI {
		return nil, ErrAgentTaskRequiresCLI
	} else {
		return cli.ExecutePortfolioSentinel(ctx, taskID, pack, modelName, reasoningEffort)
	}
}

func (e *agentRoutingExecutor) ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, cli, err := e.execution(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStockProfileSummary(ctx, taskID, profile, modelName, reasoningEffort)
	} else {
		return cli.ExecuteStockProfileSummary(ctx, taskID, profile, modelName, reasoningEffort)
	}
}
