package stockv2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// 内存 task 池:taskID -> 结果 inbox。
//
// 仅内存,重启即失,不持久化。Codex CLI 执行时通过 MCP submit_result 把结果写入
// 此池,主程序再从中取出做 schema 校验、落库、跑 guardrails。
//
// ponytail: 简单 map + mu + resultCh 通知, 不引入额外依赖。
// taskID 用 crypto/rand 16 字节 hex, 不可猜。

var (
	ErrTaskNotFound     = errors.New("agent task not found")
	ErrTaskDuplicate    = errors.New("agent task result already submitted")
	ErrTaskExpired      = errors.New("agent task expired")
	ErrTaskTypeMismatch = errors.New("agent task type mismatch")
)

type agentTaskStatus string

const (
	agentTaskStatusWaiting   agentTaskStatus = "waiting"
	agentTaskStatusSubmitted agentTaskStatus = "submitted"
	agentTaskStatusExpired   agentTaskStatus = "expired"
	agentTaskStatusCancelled agentTaskStatus = "cancelled"
)

type AgentTaskSubmittedResult struct {
	OutputType    string
	ResultSummary string
	Result        map[string]any
	Confidence    float64
}

type agentTaskEntry struct {
	id              string
	taskType        string
	agentRunID      string
	reviewID        string
	deadline        time.Time
	status          agentTaskStatus
	submittedResult *AgentTaskSubmittedResult
	submittedAt     time.Time
	submitCount     int
	resultCh        chan struct{} // close 表示有结果
	mu              sync.Mutex
}

type agentTaskPool struct {
	tasks   map[string]*agentTaskEntry
	service *Service
	mu      sync.RWMutex
	stopCh  chan struct{}
	doneCh  chan struct{}
}

const defaultTaskTTL = 10 * time.Minute
const defaultCleanupInterval = 30 * time.Second

func newAgentTaskPool(cleanupInterval time.Duration) *agentTaskPool {
	if cleanupInterval <= 0 {
		cleanupInterval = defaultCleanupInterval
	}
	p := &agentTaskPool{
		tasks:  make(map[string]*agentTaskEntry),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go p.cleanupLoop(cleanupInterval)
	return p
}

func (p *agentTaskPool) Close() {
	close(p.stopCh)
	<-p.doneCh
}

// createTask 生成不可猜的 taskID 并注册任务。返回 taskID。
func (p *agentTaskPool) createTask(taskType, agentRunID, reviewID string, ttl time.Duration) (string, *agentTaskEntry) {
	if ttl <= 0 {
		ttl = defaultTaskTTL
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	id := "task-" + hex.EncodeToString(buf)

	entry := &agentTaskEntry{
		id:         id,
		taskType:   taskType,
		agentRunID: agentRunID,
		reviewID:   reviewID,
		deadline:   time.Now().Add(ttl),
		status:     agentTaskStatusWaiting,
		resultCh:   make(chan struct{}),
	}

	p.mu.Lock()
	p.tasks[id] = entry
	p.mu.Unlock()

	return id, entry
}

func (p *agentTaskPool) getTask(id string) (*agentTaskEntry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.tasks[id]
	return e, ok
}

// submitResult 提交结果。返回 status 字符串 (accepted / duplicate / expired / invalid_task)。
func (p *agentTaskPool) submitResult(id, taskType string, result AgentTaskSubmittedResult) (string, error) {
	entry, ok := p.getTask(id)
	if !ok {
		return "invalid_task", ErrTaskNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if time.Now().After(entry.deadline) {
		entry.status = agentTaskStatusExpired
		return "expired", ErrTaskExpired
	}
	if entry.status == agentTaskStatusExpired {
		return "expired", ErrTaskExpired
	}
	if entry.status == agentTaskStatusSubmitted {
		return "duplicate", ErrTaskDuplicate
	}
	if entry.taskType != taskType {
		return "invalid_task", ErrTaskTypeMismatch
	}

	entry.submittedResult = &result
	entry.submittedAt = time.Now()
	entry.submitCount++
	entry.status = agentTaskStatusSubmitted
	close(entry.resultCh) // 通知等待方

	return "accepted", nil
}

// waitForResult 阻塞等待结果或 ctx 取消。
func (p *agentTaskPool) waitForResult(ctx context.Context, id string) (*AgentTaskSubmittedResult, error) {
	entry, ok := p.getTask(id)
	if !ok {
		return nil, ErrTaskNotFound
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-entry.resultCh:
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if entry.submittedResult == nil {
			return nil, errors.New("result channel closed but result is nil")
		}
		return entry.submittedResult, nil
	}
}

// remove 从池中移除任务(消费后清理)。
func (p *agentTaskPool) remove(id string) {
	p.mu.Lock()
	delete(p.tasks, id)
	p.mu.Unlock()
}

func (p *agentTaskPool) cleanupLoop(interval time.Duration) {
	defer close(p.doneCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.cleanupExpired()
		}
	}
}

func (p *agentTaskPool) cleanupExpired() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, entry := range p.tasks {
		entry.mu.Lock()
		if now.After(entry.deadline) && entry.status == agentTaskStatusWaiting {
			entry.status = agentTaskStatusExpired
			close(entry.resultCh) // 唤醒等待方
		}
		// 已提交的任务保留 1 小时后清理, 给落库留充足时间
		if entry.status == agentTaskStatusSubmitted && now.After(entry.submittedAt.Add(1*time.Hour)) {
			delete(p.tasks, id)
		}
		// expired 任务保留到下个周期再清, 让调用方能读到状态
		if entry.status == agentTaskStatusExpired && now.After(entry.deadline.Add(5*time.Minute)) {
			delete(p.tasks, id)
		}
		entry.mu.Unlock()
	}
}
