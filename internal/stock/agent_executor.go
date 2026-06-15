package stock

import (
	"context"

	"phantom-lancer/internal/storage"
)

type AgentExecutionInput struct {
	Profile                 storage.StockAgentModelProfile
	TaskType                string
	Protocol                string
	ReviewID                string
	AlertID                 string
	StrategyID              string
	Symbol                  string
	Prompt                  string
	InputJSON               string
	DeterministicOutputJSON string
}

type AgentExecutionResult struct {
	StepKey        string
	Role           string
	Status         string
	Prompt         string
	InputJSON      string
	OutputJSON     string
	ToolCallsJSON  string
	LatencyMs      int
	TokenEstimate  int
	OutputSnapshot string
	Summary        string
	ErrorSummary   string
}

type AgentExecutor interface {
	ExecuteStockReview(ctx context.Context, input AgentExecutionInput) (AgentExecutionResult, error)
}
