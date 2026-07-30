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

func (e *agentRoutingExecutor) mode(ctx context.Context, taskID string) (string, error) {
	entry, ok := e.service.agentTaskPool.getTask(taskID)
	if !ok {
		return "", ErrTaskNotFound
	}
	run, err := e.service.store.GetAgentRun(ctx, entry.agentRunID)
	if err != nil {
		return "", err
	}
	mode := run.ExecutionMode
	if mode == "" {
		mode = AgentExecutionModeCLI
	}
	if !validAgentExecutionMode(mode) {
		return "", fmt.Errorf("%w: %s", ErrInvalidAgentExecutionMode, mode)
	}
	return mode, nil
}

func (e *agentRoutingExecutor) ExecuteOperationReview(ctx context.Context, taskID string, pack AgentContextPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteOperationReview(ctx, taskID, pack, modelName, reasoningEffort)
	}
	return e.cli.ExecuteOperationReview(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecuteStrategyGeneration(ctx context.Context, taskID string, pack StrategyGenerationContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStrategyGeneration(ctx, taskID, pack, modelName, reasoningEffort)
	}
	return e.cli.ExecuteStrategyGeneration(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecuteStrategyGenerationStep(ctx context.Context, taskID string, pack StrategyGenerationStepPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, reasoningEffort)
	}
	return e.cli.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecuteOpportunityDiscovery(ctx context.Context, taskID string, pack OpportunityDiscoveryContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteOpportunityDiscovery(ctx, taskID, pack, modelName, reasoningEffort)
	}
	return e.cli.ExecuteOpportunityDiscovery(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecuteNewsContextAggregation(ctx context.Context, taskID string, pack NewsContextAggregationPack, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName, reasoningEffort)
	}
	return e.cli.ExecuteNewsContextAggregation(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecutePortfolioSentinel(ctx context.Context, taskID string, pack PortfolioSentinelContext, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode != AgentExecutionModeCLI {
		return nil, ErrAgentTaskRequiresCLI
	}
	return e.cli.ExecutePortfolioSentinel(ctx, taskID, pack, modelName, reasoningEffort)
}

func (e *agentRoutingExecutor) ExecuteStockProfileSummary(ctx context.Context, taskID string, profile StockProfile, modelName, reasoningEffort string) (*AgentExecutorOutput, error) {
	if mode, err := e.mode(ctx, taskID); err != nil {
		return nil, err
	} else if mode == AgentExecutionModeAPI {
		return e.api.ExecuteStockProfileSummary(ctx, taskID, profile, modelName, reasoningEffort)
	}
	return e.cli.ExecuteStockProfileSummary(ctx, taskID, profile, modelName, reasoningEffort)
}
