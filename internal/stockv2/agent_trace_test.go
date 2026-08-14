package stockv2

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"phantom-lancer/internal/storage"
)

type captureAgentTraceUploader struct {
	mu   sync.Mutex
	key  string
	data []byte
	err  error
	done chan struct{}
}

type blockedAgentTraceUploader struct{ done chan struct{} }

func (u *blockedAgentTraceUploader) PutStream(ctx context.Context, _ string, _ io.Reader, _ string, _ int64) (string, error) {
	<-ctx.Done()
	close(u.done)
	return "", ctx.Err()
}

func newCaptureAgentTraceUploader() *captureAgentTraceUploader {
	return &captureAgentTraceUploader{done: make(chan struct{})}
}

func (u *captureAgentTraceUploader) PutStream(_ context.Context, key string, body io.Reader, _ string, _ int64) (string, error) {
	data, readErr := io.ReadAll(body)
	u.mu.Lock()
	u.key = key
	u.data = data
	err := u.err
	u.mu.Unlock()
	close(u.done)
	if readErr != nil {
		return "", readErr
	}
	return "etag", err
}

func (u *captureAgentTraceUploader) records(t *testing.T) []map[string]any {
	t.Helper()
	select {
	case <-u.done:
	case <-time.After(3 * time.Second):
		t.Fatal("trace upload did not finish")
	}
	u.mu.Lock()
	data := append([]byte(nil), u.data...)
	u.mu.Unlock()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open trace gzip: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read trace gzip: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(decompressed)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode trace record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func TestAgentTraceRecorderStreamsTerminalAndRedactsSecrets(t *testing.T) {
	uploader := newCaptureAgentTraceUploader()
	recorder := newAgentTraceRecorder("run-1", "profile-1", "trace.jsonl.gz", uploader, nil, nil)
	if !recorder.record("manifest", map[string]any{"pipelineRevision": "r0001"}) {
		t.Fatal("manifest was not recorded")
	}
	if !recorder.record("model_request", map[string]any{
		"apiKey":      "top-secret",
		"prompt":      "Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
		"inputTokens": 321,
	}) {
		t.Fatal("model request was not recorded")
	}
	recorder.finish(AgentRunStatusCompleted, map[string]any{"result": "ok"})

	records := uploader.records(t)
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	for index, record := range records {
		if got := int(record["sequence"].(float64)); got != index+1 {
			t.Fatalf("sequence[%d] = %d", index, got)
		}
	}
	request := records[1]["data"].(map[string]any)
	if request["apiKey"] != "[REDACTED]" || request["inputTokens"].(float64) != 321 {
		t.Fatalf("unexpected redacted request: %#v", request)
	}
	if strings.Contains(request["prompt"].(string), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("bearer secret was retained: %q", request["prompt"])
	}
	terminal := records[2]["data"].(map[string]any)
	if terminal["lastSequence"].(float64) != 3 || terminal["priorEventCount"].(float64) != 2 || terminal["priorEventsSha256"] == "" {
		t.Fatalf("unexpected terminal integrity: %#v", terminal)
	}
}

func TestAgentTraceRecorderFragmentsLargePayload(t *testing.T) {
	uploader := newCaptureAgentTraceUploader()
	recorder := newAgentTraceRecorder("run-2", "profile-1", "trace.jsonl.gz", uploader, nil, nil)
	large := strings.Repeat("x", agentTraceRecordBytes+128)
	if !recorder.record("input_context", map[string]any{"text": large}) {
		t.Fatal("large payload was not recorded")
	}
	recorder.finish(AgentRunStatusFailed, map[string]any{"error": "expected"})
	records := uploader.records(t)
	events := make([]string, 0, len(records))
	for _, record := range records {
		events = append(events, record["event"].(string))
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "artifact_start") || !strings.Contains(joined, "artifact_chunk") || !strings.Contains(joined, "artifact_end") || events[len(events)-1] != "task_failed" {
		t.Fatalf("events = %s", joined)
	}
}

func TestAgentTraceRecorderAbandonsArchiveWhenQueueExceedsEightMiB(t *testing.T) {
	uploader := &blockedAgentTraceUploader{done: make(chan struct{})}
	recorder := newAgentTraceRecorder("run-overflow", "profile-1", "trace.jsonl.gz", uploader, nil, nil)
	if recorder.record("input_context", map[string]any{"text": strings.Repeat("x", agentTraceQueueBytes+1)}) {
		t.Fatal("oversized queued trace unexpectedly succeeded")
	}
	select {
	case <-uploader.done:
	case <-time.After(3 * time.Second):
		t.Fatal("overflow did not cancel multipart upload")
	}
	recorder.mu.Lock()
	aborted, queuedBytes := recorder.aborted, recorder.queuedBytes
	recorder.mu.Unlock()
	if !aborted || queuedBytes != 0 {
		t.Fatalf("aborted=%t queuedBytes=%d", aborted, queuedBytes)
	}
}

func TestAgentTracePipelineRevisionFingerprints(t *testing.T) {
	want := map[string]string{
		AgentTaskTypeOperationReview:      "sha256:391cebbffe4766ab202f30be394f527a219c276e33bc9125755748f751ea98fa",
		AgentTaskTypeStrategyGeneration:   "sha256:fbb34e7d1d3f1fe5c80cadae3a9007bec692dece9ec16449fb48c06d63ab4b6b",
		AgentTaskTypeOpportunityDiscovery: "sha256:d0c45a267153d8226c68cc06fb6df97bb6f9803061667b69398851ce46afac3b",
		AgentTaskTypePortfolioSentinel:    "sha256:8c1c1db852ba7b9d220398c12c9abd81e33828fb23a1c48d07d8ebadc8882318",
	}
	for taskType, fingerprint := range want {
		spec, ok := agentTracePipelineSpecs[taskType]
		if !ok || spec.Revision != "r0001" {
			t.Fatalf("pipeline spec %s = %#v", taskType, spec)
		}
		if got := agentTracePipelineFingerprint(spec); got != fingerprint {
			t.Fatalf("fingerprint %s = %s, want %s", taskType, got, fingerprint)
		}
	}
	if agentTraceSupportedTask(AgentTaskTypeNewsEventReview) || agentTraceSupportedTask(AgentTaskTypeStockProfileSummary) {
		t.Fatal("high-volume news/profile tasks must not be archived")
	}
}

func TestAgentTraceObjectKeyCarriesRevisionAndAttempt(t *testing.T) {
	run := AgentRun{
		ID: "run-123", TaskType: AgentTaskTypePortfolioSentinel,
		TriggerObjectType: "portfolio_sentinel_run", TriggerObjectID: "sentinel-456",
		StartedAt: time.Date(2026, 8, 14, 1, 2, 3, 4, time.UTC),
	}
	key := agentTraceObjectKey(run, map[string]any{"attempt": 2})
	want := "stockv2/agent-traces/portfolio-sentinel/r0001/2026/08/14/20260814T010203.000000004Z__portfolio-sentinel-r0001__logical-"
	if !strings.HasPrefix(key, want) || !strings.Contains(key, "__run-run-123__attempt-02__trace-v1.jsonl.gz") {
		t.Fatalf("object key = %q", key)
	}
}

type fakeAgentTraceProfileStore struct{ profile storage.ObjectStorageProfile }

func (s fakeAgentTraceProfileStore) GetObjectStorageProfile(context.Context, string) (storage.ObjectStorageProfile, error) {
	return s.profile, nil
}

func TestAgentTraceTaskProfileConfigValidation(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	svc.agentTraceProfiles = fakeAgentTraceProfileStore{profile: storage.ObjectStorageProfile{
		Bucket: "private-bucket", Endpoint: "https://s3.example.invalid",
		AccessKeyID: "access", SecretAccessKey: "secret", HasCredentials: true,
	}}
	enabled := true
	profileID := "profile-1"
	updated, err := svc.UpdateAgentTaskProfile(context.Background(), AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		ArchiveEnabled: &enabled, ArchiveObjectStorageProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("enable trace archive: %v", err)
	}
	if !updated.ArchiveEnabled || updated.ArchiveObjectStorageProfileID != profileID {
		t.Fatalf("updated profile = %#v", updated)
	}
	referenced, err := svc.AgentTraceObjectStorageProfileReferenced(context.Background(), profileID)
	if err != nil || !referenced {
		t.Fatalf("profile reference = %t, err=%v", referenced, err)
	}
	reloaded, err := svc.GetAgentTaskProfileByType(context.Background(), AgentTaskTypeOperationReview)
	if err != nil || !reloaded.ArchiveEnabled || reloaded.ArchiveObjectStorageProfileID != profileID {
		t.Fatalf("reloaded profile = %#v, err=%v", reloaded, err)
	}
	if _, err := svc.UpdateAgentTaskProfile(context.Background(), AgentTaskTypeNewsEventReview, RequestUpdateAgentTaskProfile{
		ArchiveEnabled: &enabled, ArchiveObjectStorageProfileID: &profileID,
	}); err != ErrAgentTraceNotSupported {
		t.Fatalf("excluded task error = %v", err)
	}
}

func TestAgentTraceUploadFailureDoesNotChangeCompletedRun(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	storageProfile := storage.ObjectStorageProfile{
		Bucket: "private-bucket", Endpoint: "https://s3.example.invalid",
		AccessKeyID: "access", SecretAccessKey: "secret", HasCredentials: true,
	}
	svc.agentTraceProfiles = fakeAgentTraceProfileStore{profile: storageProfile}
	uploader := newCaptureAgentTraceUploader()
	uploader.err = errors.New("simulated upload failure")
	svc.agentTrace.uploadFactory = func(storage.ObjectStorageProfile) (agentTraceUploadClient, error) {
		return uploader, nil
	}
	enabled := true
	profileID := "profile-1"
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeOperationReview, RequestUpdateAgentTaskProfile{
		ArchiveEnabled: &enabled, ArchiveObjectStorageProfileID: &profileID,
	}); err != nil {
		t.Fatalf("enable trace archive: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{
		ProviderID: agentProviderCodexCLIDefaultID, ModelName: "trace-test-model", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	run, ledger, err := svc.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType: AgentTaskTypeOperationReview, ExecutionMode: AgentExecutionModeCLI,
		ProviderID: model.ProviderID, ModelID: model.ID,
		TriggerObjectType: "unit_test", TriggerObjectID: "trace-upload-failure",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	taskID, _ := svc.agentTaskPool.createTask(run.TaskType, run.ID, "", time.Minute)
	if _, err := svc.agentTaskPool.submitResult(taskID, run.TaskType, AgentTaskSubmittedResult{
		OutputType: OperationReviewOutputContinueMonitoring, ResultSummary: "keep watching",
		Result: map[string]any{"reason": "test"}, Confidence: 0.6,
	}); err != nil {
		t.Fatalf("submit result: %v", err)
	}
	svc.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, &AgentExecutorOutput{ExitCode: 0}, nil)
	select {
	case <-uploader.done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed upload did not finish")
	}
	finalRun, err := svc.store.GetAgentRun(ctx, run.ID)
	if err != nil || finalRun.Status != AgentRunStatusCompleted {
		t.Fatalf("final run = %#v, err=%v", finalRun, err)
	}
}
