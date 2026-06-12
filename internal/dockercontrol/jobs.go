package dockercontrol

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"time"

	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/safelog"
)

const (
	eventScopeDockerJob = "docker.job"

	jobStatusQueued    = "queued"
	jobStatusRunning   = "running"
	jobStatusCompleted = "completed"
	jobStatusFailed    = "failed"
	jobStatusCancelled = "cancelled"
)

// Job is the redacted status surfaced to the UI for Docker host operations.
type Job struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Title        string         `json:"title"`
	Status       string         `json:"status"`
	RiskLevel    string         `json:"riskLevel"`
	Target       string         `json:"target,omitempty"`
	Error        string         `json:"error,omitempty"`
	EventScope   string         `json:"eventScope"`
	EventScopeID string         `json:"eventScopeId"`
	CreatedAt    string         `json:"createdAt"`
	StartedAt    string         `json:"startedAt,omitempty"`
	CompletedAt  string         `json:"completedAt,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
}

// OperationResult is returned by mutating HTTP routes so the frontend can
// attach to the persisted SSE stream immediately.
type OperationResult struct {
	Job          Job    `json:"job"`
	EventScope   string `json:"eventScope"`
	EventScopeID string `json:"eventScopeId"`
}

type jobRunner func(context.Context, func(string, map[string]any)) error

func (s *Service) StartJob(ctx context.Context, kind, title, risk, target string, payload map[string]any, run jobRunner) (OperationResult, error) {
	if run == nil {
		return OperationResult{}, errors.New("docker job runner is required")
	}
	id, err := ids.New("dockjob")
	if err != nil {
		return OperationResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	job := Job{
		ID:           id,
		Type:         kind,
		Title:        title,
		Status:       jobStatusQueued,
		RiskLevel:    risk,
		Target:       safelog.Text(target, 120),
		EventScope:   eventScopeDockerJob,
		EventScopeID: id,
		CreatedAt:    now,
		Payload:      payload,
	}
	s.saveJob(job)
	s.append(ctx, id, "docker.job.created", map[string]any{"type": kind, "title": title, "riskLevel": risk, "target": job.Target})

	runCtx, cancel := context.WithCancel(context.Background())
	s.saveJobCancel(id, cancel)
	go s.runJob(runCtx, job, run)
	return OperationResult{Job: job, EventScope: eventScopeDockerJob, EventScopeID: id}, nil
}

func (s *Service) ListJobs(limit int) []Job {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Service) CancelJob(ctx context.Context, id string) (Job, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	cancel := s.cancels[id]
	s.mu.Unlock()
	if !ok {
		return Job{}, errors.New("docker job not found")
	}
	if job.Status != jobStatusQueued && job.Status != jobStatusRunning {
		return job, errors.New("docker job can no longer be cancelled")
	}
	if cancel == nil {
		return job, errors.New("docker job has no active cancel handle")
	}
	cancel()
	s.append(ctx, id, "docker.job.cancel.requested", map[string]any{"type": job.Type, "title": job.Title})
	return job, nil
}

func (s *Service) ActiveJob() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *Job
	for _, job := range s.jobs {
		if job.Status != jobStatusQueued && job.Status != jobStatusRunning {
			continue
		}
		candidate := job
		if latest == nil || candidate.CreatedAt > latest.CreatedAt {
			latest = &candidate
		}
	}
	return latest
}

func (s *Service) LatestJob() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *Job
	for _, job := range s.jobs {
		candidate := job
		if latest == nil || candidate.CreatedAt > latest.CreatedAt {
			latest = &candidate
		}
	}
	return latest
}

func (s *Service) saveJob(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = make(map[string]Job)
	}
	s.jobs[job.ID] = job
}

func (s *Service) saveJobCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancels == nil {
		s.cancels = make(map[string]context.CancelFunc)
	}
	s.cancels[id] = cancel
}

func (s *Service) clearJobCancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, id)
}

func (s *Service) runJob(ctx context.Context, job Job, run jobRunner) {
	defer s.clearJobCancel(job.ID)

	if job.Type == "docker.image.pull" {
		select {
		case s.pullSem <- struct{}{}:
		case <-ctx.Done():
			job.Status = jobStatusCancelled
			job.Error = "cancelled by owner"
			job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			s.saveJob(job)
			s.append(context.Background(), job.ID, "docker.job.cancelled", map[string]any{"type": job.Type, "title": job.Title})
			return
		}
		defer func() { <-s.pullSem }()
	}

	job.Status = jobStatusRunning
	job.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.saveJob(job)
	s.append(ctx, job.ID, "docker.job.started", map[string]any{"type": job.Type, "title": job.Title})

	emit := func(eventType string, payload map[string]any) {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["type"] = job.Type
		s.append(ctx, job.ID, eventType, payload)
	}

	if err := run(ctx, emit); err != nil {
		if errors.Is(err, context.Canceled) {
			job.Status = jobStatusCancelled
			job.Error = "cancelled by owner"
		} else {
			job.Status = jobStatusFailed
			job.Error = safelog.Error(err, 240)
		}
		job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.saveJob(job)
		if job.Status == jobStatusCancelled {
			s.append(context.Background(), job.ID, "docker.job.cancelled", map[string]any{"type": job.Type, "title": job.Title})
		} else {
			s.append(ctx, job.ID, "docker.job.failed", map[string]any{"type": job.Type, "error": job.Error})
		}
		return
	}

	job.Status = jobStatusCompleted
	job.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.saveJob(job)
	s.append(ctx, job.ID, "docker.job.completed", map[string]any{"type": job.Type, "title": job.Title})
}

func (s *Service) append(ctx context.Context, scopeID, eventType string, payload map[string]any) {
	if s.store == nil {
		return
	}
	event, err := s.store.AppendEvent(ctx, eventScopeDockerJob, scopeID, eventType, payload)
	if err != nil {
		if s.log != nil && s.logSampler.Allow("docker:job-append:"+scopeID) {
			s.log.Warn("docker job event append failed", "type", eventType, "error", safelog.Error(err, 160))
		}
		return
	}
	if s.hub != nil {
		s.hub.Publish(event)
	}
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
