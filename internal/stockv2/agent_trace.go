package stockv2

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode"

	"phantom-lancer/internal/objectstore"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

const (
	// ponytail: these are trace-v1 protocol/resource bounds, not owner tuning
	// knobs. Changing them requires a trace-format review (and a new format
	// version if readers would observe different semantics), so they stay fixed.
	agentTraceFormatVersion = "trace-v1"
	agentTraceQueueBytes    = 8 << 20
	agentTracePartBytes     = 8 << 20
	agentTraceRecordBytes   = 1 << 20
	agentTraceChunkBytes    = 720 << 10
)

type agentTracePipelineSpec struct {
	Pipeline string
	Revision string
	Seed     string
}

var agentTracePipelineSpecs = map[string]agentTracePipelineSpec{
	AgentTaskTypeOperationReview: {
		Pipeline: "operation-review", Revision: "r0001",
		Seed: "operation_review|prompt-v1|submit-result-v1|operation-guardrails-v1",
	},
	AgentTaskTypeStrategyGeneration: {
		Pipeline: "strategy-generation", Revision: "r0001",
		Seed: "strategy_generation|six-role-pipeline-v1|submit-result-v1|strategy-guardrails-v1",
	},
	AgentTaskTypeOpportunityDiscovery: {
		Pipeline: "opportunity-discovery", Revision: "r0001",
		Seed: "opportunity_discovery|research-steps-v1|submit-result-v1|candidate-guardrails-v1",
	},
	AgentTaskTypePortfolioSentinel: {
		Pipeline: "portfolio-sentinel", Revision: "r0001",
		Seed: "portfolio_sentinel|search-and-mcp-v1|direct-output-v1|portfolio-guardrails-v1",
	},
}

func agentTraceSupportedTask(taskType string) bool {
	_, ok := agentTracePipelineSpecs[taskType]
	return ok
}

type agentTraceProfileStore interface {
	GetObjectStorageProfile(context.Context, string) (storage.ObjectStorageProfile, error)
}

type agentTraceUploadClient interface {
	PutStream(context.Context, string, io.Reader, string, int64) (string, error)
}

type objectStoreTraceUploadClient struct{ client *objectstore.Client }

func (c objectStoreTraceUploadClient) PutStream(ctx context.Context, key string, body io.Reader, contentType string, partSize int64) (string, error) {
	return c.client.PutStream(ctx, key, body, contentType, partSize)
}

type agentTraceUploadFactory func(storage.ObjectStorageProfile) (agentTraceUploadClient, error)

type agentTraceManager struct {
	service       *Service
	log           *slog.Logger
	uploadFactory agentTraceUploadFactory
	mu            sync.Mutex
	recorders     map[string]*agentTraceRecorder
}

func newAgentTraceManager(service *Service, log *slog.Logger) *agentTraceManager {
	return &agentTraceManager{
		service: service,
		log:     log,
		uploadFactory: func(profile storage.ObjectStorageProfile) (agentTraceUploadClient, error) {
			client, err := objectstore.New(profile)
			if err != nil {
				return nil, err
			}
			return objectStoreTraceUploadClient{client: client}, nil
		},
		recorders: map[string]*agentTraceRecorder{},
	}
}

func (m *agentTraceManager) ensureTask(ctx context.Context, taskID string) *agentTraceRecorder {
	if m == nil || m.service == nil || m.service.agentTaskPool == nil {
		return nil
	}
	entry, ok := m.service.agentTaskPool.getTask(taskID)
	if !ok {
		return nil
	}
	return m.ensureRun(ctx, entry.agentRunID)
}

func (m *agentTraceManager) ensureRun(ctx context.Context, runID string) *agentTraceRecorder {
	if m == nil || m.service == nil || m.service.store == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	m.mu.Lock()
	if recorder := m.recorders[runID]; recorder != nil {
		m.mu.Unlock()
		return recorder
	}
	m.mu.Unlock()

	run, err := m.service.store.GetAgentRun(ctx, runID)
	if err != nil || !agentTraceSupportedTask(run.TaskType) {
		return nil
	}
	profile, err := m.service.store.GetAgentTaskProfileByType(ctx, run.TaskType)
	if err != nil || !profile.ArchiveEnabled || strings.TrimSpace(profile.ArchiveObjectStorageProfileID) == "" || m.service.agentTraceProfiles == nil {
		return nil
	}
	storageProfile, err := m.service.agentTraceProfiles.GetObjectStorageProfile(ctx, profile.ArchiveObjectStorageProfileID)
	if err != nil {
		m.disableWithWarning(run, profile.ArchiveObjectStorageProfileID, err)
		return nil
	}
	uploader, err := m.uploadFactory(storageProfile)
	if err != nil {
		m.disableWithWarning(run, profile.ArchiveObjectStorageProfileID, err)
		return nil
	}

	manifest := m.manifest(ctx, run, profile.ArchiveObjectStorageProfileID)
	key := agentTraceObjectKey(run, manifest)
	recorder := newAgentTraceRecorder(run.ID, profile.ArchiveObjectStorageProfileID, key, uploader, m.log, m.uploadDone)
	m.mu.Lock()
	if current := m.recorders[runID]; current != nil {
		m.mu.Unlock()
		recorder.abort(errors.New("duplicate trace recorder"))
		return current
	}
	m.recorders[runID] = recorder
	m.mu.Unlock()
	if !recorder.record("manifest", manifest) {
		return nil
	}
	return recorder
}

func (m *agentTraceManager) manifest(ctx context.Context, run AgentRun, profileID string) map[string]any {
	spec := agentTracePipelineSpecs[run.TaskType]
	model, _ := m.service.store.GetAgentModelProfile(ctx, run.ModelID)
	provider, _ := m.service.store.GetAgentProviderProfile(ctx, run.ProviderID)
	parentRunID := ""
	attempt := 1
	if ledger, err := m.service.store.GetAgentDecisionLedger(ctx, run.DecisionLedgerID); err == nil && ledger.RedactionSummary != nil {
		parentRunID = strings.TrimSpace(fmt.Sprint(ledger.RedactionSummary["fallbackFromAgentRunId"]))
		if parentRunID != "" && parentRunID != "<nil>" {
			attempt = 2
		} else {
			parentRunID = ""
		}
	}
	return map[string]any{
		"traceVersion":         agentTraceFormatVersion,
		"pipeline":             spec.Pipeline,
		"pipelineRevision":     spec.Revision,
		"pipelineFingerprint":  agentTracePipelineFingerprint(spec),
		"gitCommit":            agentTraceGitCommit(),
		"agentRunId":           run.ID,
		"logicalOperationId":   agentTraceLogicalOperationID(run),
		"parentRunId":          parentRunID,
		"attempt":              attempt,
		"taskType":             run.TaskType,
		"triggerObjectType":    run.TriggerObjectType,
		"triggerObjectId":      run.TriggerObjectID,
		"executionMode":        run.ExecutionMode,
		"providerId":           run.ProviderID,
		"providerType":         provider.ProviderType,
		"providerName":         provider.Name,
		"modelId":              run.ModelID,
		"modelName":            model.ModelName,
		"reasoningEffort":      run.ReasoningEffort,
		"objectStorageProfile": profileID,
		"startedAt":            run.StartedAt,
	}
}

func agentTracePipelineFingerprint(spec agentTracePipelineSpec) string {
	fingerprint := sha256.Sum256([]byte(spec.Seed))
	return "sha256:" + hex.EncodeToString(fingerprint[:])
}

func (m *agentTraceManager) recordTask(ctx context.Context, taskID, event string, payload any) {
	if recorder := m.ensureTask(ctx, taskID); recorder != nil {
		recorder.record(event, payload)
	}
}

func (m *agentTraceManager) recordRun(ctx context.Context, runID, event string, payload any) {
	if recorder := m.ensureRun(ctx, runID); recorder != nil {
		recorder.record(event, payload)
	}
}

func (m *agentTraceManager) finishRun(ctx context.Context, run AgentRun, payload any) {
	if recorder := m.ensureRun(ctx, run.ID); recorder != nil {
		recorder.finish(run.Status, map[string]any{
			"run":     run,
			"details": payload,
		})
	}
}

func (m *agentTraceManager) disableWithWarning(run AgentRun, profileID string, err error) {
	if m.log != nil {
		m.log.Warn("stockv2 agent trace archive unavailable", "run_id", run.ID, "task_type", run.TaskType, "profile_id", profileID, "error", safelog.Error(err, 240))
	}
}

func (m *agentTraceManager) uploadDone(runID string, _ error) {
	m.mu.Lock()
	delete(m.recorders, runID)
	m.mu.Unlock()
}

func (m *agentTraceManager) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	recorders := make([]*agentTraceRecorder, 0, len(m.recorders))
	for _, recorder := range m.recorders {
		recorders = append(recorders, recorder)
	}
	m.mu.Unlock()
	for _, recorder := range recorders {
		recorder.abort(errors.New("service shutdown"))
	}
}

type agentTracePendingEvent struct {
	event      string
	recordedAt time.Time
	payload    json.RawMessage
}

type agentTraceRecorder struct {
	runID     string
	profileID string
	key       string
	log       *slog.Logger
	onDone    func(string, error)
	cancel    context.CancelFunc
	pipe      *io.PipeWriter

	mu          sync.Mutex
	cond        *sync.Cond
	queue       []agentTracePendingEvent
	queuedBytes int
	closed      bool
	aborted     bool
	artifactSeq int
}

func newAgentTraceRecorder(runID, profileID, key string, uploader agentTraceUploadClient, log *slog.Logger, onDone func(string, error)) *agentTraceRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	recorder := &agentTraceRecorder{
		runID: runID, profileID: profileID, key: key, log: log, onDone: onDone,
		cancel: cancel, pipe: writer,
	}
	recorder.cond = sync.NewCond(&recorder.mu)
	go recorder.writeLoop()
	go func() {
		_, err := uploader.PutStream(ctx, key, reader, "application/gzip", agentTracePartBytes)
		_ = reader.CloseWithError(err)
		if err != nil {
			recorder.abort(err)
			if log != nil {
				log.Warn("stockv2 agent trace upload failed", "run_id", runID, "profile_id", profileID, "error", safelog.Error(err, 240))
			}
		}
		if onDone != nil {
			onDone(runID, err)
		}
	}()
	return recorder
}

func (r *agentTraceRecorder) record(event string, payload any) bool {
	encoded, err := marshalAgentTracePayload(payload)
	if err != nil {
		r.abort(err)
		return false
	}
	if len(encoded) <= agentTraceRecordBytes {
		return r.enqueue(agentTracePendingEvent{event: event, recordedAt: time.Now(), payload: encoded})
	}
	digest := sha256.Sum256(encoded)
	r.mu.Lock()
	r.artifactSeq++
	artifactID := fmt.Sprintf("artifact-%04d", r.artifactSeq)
	r.mu.Unlock()
	if !r.record("artifact_start", map[string]any{
		"artifactId": artifactID, "originalEvent": event, "encoding": "json+base64-chunks",
		"sizeBytes": len(encoded), "sha256": hex.EncodeToString(digest[:]),
	}) {
		return false
	}
	chunkCount := (len(encoded) + agentTraceChunkBytes - 1) / agentTraceChunkBytes
	for index, start := 0, 0; start < len(encoded); index, start = index+1, start+agentTraceChunkBytes {
		end := start + agentTraceChunkBytes
		if end > len(encoded) {
			end = len(encoded)
		}
		if !r.record("artifact_chunk", map[string]any{
			"artifactId": artifactID, "index": index, "count": chunkCount,
			"dataBase64": base64.StdEncoding.EncodeToString(encoded[start:end]),
		}) {
			return false
		}
	}
	return r.record("artifact_end", map[string]any{
		"artifactId": artifactID, "chunkCount": chunkCount, "sha256": hex.EncodeToString(digest[:]),
	})
}

func (r *agentTraceRecorder) enqueue(event agentTracePendingEvent) bool {
	size := len(event.payload) + len(event.event) + 96
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.aborted {
		return false
	}
	if size > agentTraceQueueBytes || r.queuedBytes+size > agentTraceQueueBytes {
		r.abortLocked(errors.New("agent trace queue exceeded 8 MiB"))
		return false
	}
	r.queue = append(r.queue, event)
	r.queuedBytes += size
	r.cond.Signal()
	return true
}

func (r *agentTraceRecorder) finish(status string, payload any) {
	terminal := "task_failed"
	if status == AgentRunStatusCompleted {
		terminal = "task_completed"
	}
	encoded, err := marshalAgentTracePayload(payload)
	if err != nil {
		r.abort(err)
		return
	}
	r.mu.Lock()
	if r.closed || r.aborted {
		r.mu.Unlock()
		return
	}
	size := len(encoded) + len(terminal) + 96
	if size > agentTraceQueueBytes || r.queuedBytes+size > agentTraceQueueBytes {
		r.abortLocked(errors.New("agent trace queue exceeded 8 MiB before terminal record"))
		r.mu.Unlock()
		return
	}
	r.queue = append(r.queue, agentTracePendingEvent{event: terminal, recordedAt: time.Now(), payload: encoded})
	r.queuedBytes += size
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}

func (r *agentTraceRecorder) abort(err error) {
	r.mu.Lock()
	r.abortLocked(err)
	r.mu.Unlock()
}

func (r *agentTraceRecorder) abortLocked(err error) {
	if r.aborted {
		return
	}
	r.aborted = true
	r.closed = true
	r.queue = nil
	r.queuedBytes = 0
	r.cancel()
	_ = r.pipe.CloseWithError(err)
	r.cond.Broadcast()
}

func (r *agentTraceRecorder) next() (agentTracePendingEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.queue) == 0 && !r.closed && !r.aborted {
		r.cond.Wait()
	}
	if r.aborted || len(r.queue) == 0 {
		return agentTracePendingEvent{}, false
	}
	event := r.queue[0]
	r.queue[0] = agentTracePendingEvent{}
	r.queue = r.queue[1:]
	r.queuedBytes -= len(event.payload) + len(event.event) + 96
	return event, true
}

func (r *agentTraceRecorder) writeLoop() {
	gzipWriter, err := gzip.NewWriterLevel(r.pipe, gzip.BestSpeed)
	if err != nil {
		r.abort(err)
		return
	}
	hasher := sha256.New()
	sequence := 0
	for {
		pending, ok := r.next()
		if !ok {
			break
		}
		sequence++
		payload := pending.payload
		if pending.event == "task_completed" || pending.event == "task_failed" {
			var terminal map[string]any
			_ = json.Unmarshal(payload, &terminal)
			terminal["lastSequence"] = sequence
			terminal["eventCount"] = sequence
			terminal["priorEventCount"] = sequence - 1
			terminal["priorEventsSha256"] = hex.EncodeToString(hasher.Sum(nil))
			payload, _ = json.Marshal(terminal)
		}
		line, marshalErr := json.Marshal(map[string]any{
			"traceVersion": agentTraceFormatVersion,
			"sequence":     sequence,
			"recordedAt":   pending.recordedAt.UTC().Format(time.RFC3339Nano),
			"event":        pending.event,
			"data":         json.RawMessage(payload),
		})
		if marshalErr != nil {
			r.abort(marshalErr)
			return
		}
		line = append(line, '\n')
		if _, err = gzipWriter.Write(line); err != nil {
			r.abort(err)
			return
		}
		if pending.event != "task_completed" && pending.event != "task_failed" {
			_, _ = hasher.Write(line)
		}
	}
	r.mu.Lock()
	aborted := r.aborted
	r.mu.Unlock()
	if aborted {
		_ = gzipWriter.Close()
		return
	}
	if err := gzipWriter.Close(); err != nil {
		r.abort(err)
		return
	}
	_ = r.pipe.Close()
}

func marshalAgentTracePayload(payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(redactAgentTraceValue("", value))
}

func redactAgentTraceValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			if agentTraceSecretKey(childKey) {
				out[childKey] = "[REDACTED]"
				continue
			}
			out[childKey] = redactAgentTraceValue(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactAgentTraceValue(key, child)
		}
		return out
	case string:
		return safelog.Redact(typed)
	default:
		return value
	}
}

func agentTraceSecretKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	switch normalized {
	case "authorization", "cookie", "setcookie", "apikey", "password", "privatekey",
		"secret", "secretaccesskey", "sessiontoken", "accesstoken", "refreshtoken", "csrftoken",
		"presignedurl":
		return true
	default:
		return false
	}
}

func agentTraceObjectKey(run AgentRun, manifest map[string]any) string {
	spec := agentTracePipelineSpecs[run.TaskType]
	started := run.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	attempt := 1
	if value, ok := manifest["attempt"].(int); ok && value > 0 {
		attempt = value
	}
	logicalID := agentTraceLogicalOperationID(run)
	return fmt.Sprintf(
		"stockv2/agent-traces/%s/%s/%s/%s__%s-%s__logical-%s__run-%s__attempt-%02d__%s.jsonl.gz",
		spec.Pipeline,
		spec.Revision,
		started.UTC().Format("2006/01/02"),
		started.UTC().Format("20060102T150405.000000000Z"),
		spec.Pipeline,
		spec.Revision,
		logicalID,
		agentTraceSafeSegment(run.ID),
		attempt,
		agentTraceFormatVersion,
	)
}

func agentTraceLogicalOperationID(run AgentRun) string {
	digest := sha256.Sum256([]byte(run.TaskType + "\x00" + run.TriggerObjectType + "\x00" + run.TriggerObjectID))
	return hex.EncodeToString(digest[:8])
}

func agentTraceSafeSegment(value string) string {
	value = strings.TrimSpace(value)
	var out strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
		if out.Len() >= 80 {
			break
		}
	}
	if out.Len() == 0 {
		return "unknown"
	}
	return out.String()
}

func agentTraceGitCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision := ""
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return "unknown"
	}
	if modified {
		return revision + "+modified"
	}
	return revision
}

func agentTraceErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
