package stockv2

import "time"

const (
	StrategyGenerationStepStatusPending   = "pending"
	StrategyGenerationStepStatusRunning   = "running"
	StrategyGenerationStepStatusCompleted = "completed"
	StrategyGenerationStepStatusFailed    = "failed"

	StrategyGenerationStepEvidenceCollector = "evidence_collector"
	StrategyGenerationStepBullResearcher    = "bull_researcher"
	StrategyGenerationStepBearResearcher    = "bear_researcher"
	StrategyGenerationStepEvidenceChecker   = "evidence_checker"
	StrategyGenerationStepPortfolioJudge    = "portfolio_judge"
	StrategyGenerationStepFormatter         = "strategy_formatter"

	StrategyGenerationStepOutputSchema = "strategy-generation-step/v1"
)

type StrategyGenerationStepRun struct {
	ID                    string         `json:"id"`
	RunID                 string         `json:"runId"`
	StepKey               string         `json:"stepKey"`
	StepName              string         `json:"stepName"`
	Role                  string         `json:"role"`
	Status                string         `json:"status"`
	SequenceNo            int            `json:"sequenceNo"`
	InputSummary          string         `json:"inputSummary,omitempty"`
	OutputSummary         string         `json:"outputSummary,omitempty"`
	ErrorMessage          string         `json:"errorMessage,omitempty"`
	Prompt                string         `json:"prompt,omitempty"`
	OutputArtifactSummary string         `json:"outputArtifactSummary,omitempty"`
	StructuredOutput      map[string]any `json:"structuredOutput,omitempty"`
	StartedAt             time.Time      `json:"startedAt,omitempty"`
	FinishedAt            time.Time      `json:"finishedAt,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	UpdatedAt             time.Time      `json:"updatedAt"`
}

type StrategyGenerationContextItem struct {
	ID          string         `json:"id"`
	RunID       string         `json:"runId"`
	StepID      string         `json:"stepId,omitempty"`
	ContextType string         `json:"contextType"`
	Title       string         `json:"title,omitempty"`
	ContentJSON map[string]any `json:"contentJson,omitempty"`
	ContentText string         `json:"contentText,omitempty"`
	SequenceNo  int            `json:"sequenceNo"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type StrategyGenerationStepPack struct {
	RunID        string                    `json:"runId"`
	StepKey      string                    `json:"stepKey"`
	Role         string                    `json:"role"`
	Objective    string                    `json:"objective"`
	Instructions []string                  `json:"instructions,omitempty"`
	Context      StrategyGenerationContext `json:"context"`
	PriorResults map[string]any            `json:"priorResults,omitempty"`
}
