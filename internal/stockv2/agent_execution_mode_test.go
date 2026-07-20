package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgentTaskExecutionModeValidatesProvider(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{
		ProviderType: AgentProviderTypeOpenAI,
		Name:         "api-provider",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "test-token",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "api-model",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	mode := AgentExecutionModeAPI
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ExecutionMode:  &mode,
		PrimaryModelID: &model.ID,
	}); err != nil {
		t.Fatalf("bind API model: %v", err)
	}
	profile, err := svc.GetAgentTaskProfileByType(ctx, AgentTaskTypeNewsEventReview)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.ExecutionMode != AgentExecutionModeAPI {
		t.Fatalf("execution mode = %q", profile.ExecutionMode)
	}

	mode = AgentExecutionModeCLI
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ExecutionMode: &mode,
	}); !errors.Is(err, ErrAgentExecutionModeModelMismatch) {
		t.Fatalf("CLI with API model error = %v", err)
	}
}

func TestAgentAPIToolNamesAreOpenAICompatible(t *testing.T) {
	name := agentAPIToolName(codexSubmitResultTool)
	if name != "stock_agent_submit_result" {
		t.Fatalf("tool name = %q", name)
	}
	if original, ok := agentAPIToolOriginalName(name); !ok || original != codexSubmitResultTool {
		t.Fatalf("reverse tool name = %q, %v", original, ok)
	}
}

func TestFailRunningAgentRunsMarksOnlyActiveRuns(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	running, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		TriggerObjectType: "test", TriggerObjectID: "running", Status: AgentRunStatusRunning,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "test", TriggerObjectID: "running",
	})
	if err != nil {
		t.Fatalf("create running run: %v", err)
	}
	completed, _, err := svc.store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeNewsEventReview, ExecutionMode: AgentExecutionModeAPI,
		TriggerObjectType: "test", TriggerObjectID: "completed", Status: AgentRunStatusCompleted, FinishedAt: now,
	}, AgentDecisionLedger{
		TaskType: AgentTaskTypeNewsEventReview, TriggerObjectType: "test", TriggerObjectID: "completed",
	})
	if err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	if count, err := svc.store.FailRunningAgentRuns(ctx, "restart"); err != nil || count != 1 {
		t.Fatalf("FailRunningAgentRuns = %d, %v", count, err)
	}
	gotRunning, _ := svc.store.GetAgentRun(ctx, running.ID)
	gotCompleted, _ := svc.store.GetAgentRun(ctx, completed.ID)
	if gotRunning.Status != AgentRunStatusFailed || gotRunning.ErrorMessage != "restart" || gotRunning.FinishedAt.IsZero() {
		t.Fatalf("running run = %#v", gotRunning)
	}
	if gotCompleted.Status != AgentRunStatusCompleted {
		t.Fatalf("completed run status = %q", gotCompleted.Status)
	}
}
