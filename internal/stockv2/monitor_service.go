package stockv2

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

// 监控服务:把系统固化的后台监控任务收敛成可观测对象。
// 复用 watch_evaluator 的规则判断(data_strategy scan),不重写规则逻辑;
// data_strategy / portfolio_risk 产生 MonitorHit(候选),不生成买卖建议、不改持仓。
// quote 刷新记录复用 quote task state,不复制执行逻辑。

const (
	newsMonitorBatchLimit         = 200
	newsMonitorHighScoreThreshold = 80
)

// ListMonitorTasks 返回系统内置任务定义 + 当前配置 + 最近一次运行摘要。
func (s *Service) ListMonitorTasks(ctx context.Context) ([]MonitorTask, error) {
	configs, err := s.store.ListMonitorTaskConfigs(ctx)
	if err != nil {
		return nil, err
	}
	defs := builtinMonitorTaskDefinitions()
	tasks := make([]MonitorTask, 0, len(defs))
	for _, def := range defs {
		cfg, ok := configs[def.TaskType]
		if !ok {
			cfg = def.DefaultConfig
		}
		latest, _ := s.latestTaskRunSummary(ctx, def.TaskType)
		tasks = append(tasks, MonitorTask{
			Definition: def,
			Config:     cfg,
			LatestRun:  latest,
		})
	}
	return tasks, nil
}

// GetMonitorTask 返回单个任务聚合视图。
func (s *Service) GetMonitorTask(ctx context.Context, taskType string) (MonitorTask, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorTask{}, ErrInvalidMonitorTaskType
	}
	cfg, err := s.store.GetMonitorTaskConfig(ctx, taskType)
	if err != nil {
		if errors.Is(err, ErrMonitorTaskNotFound) {
			cfg = def.DefaultConfig
		} else {
			return MonitorTask{}, err
		}
	}
	latest, _ := s.latestTaskRunSummary(ctx, taskType)
	return MonitorTask{Definition: def, Config: cfg, LatestRun: latest}, nil
}

// UpdateMonitorTaskConfig 修改任务配置(开关/周期/范围/敏感度/冷却/Agent 开关)。
func (s *Service) UpdateMonitorTaskConfig(ctx context.Context, taskType string, req RequestUpdateMonitorTaskConfig) (MonitorTask, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorTask{}, ErrInvalidMonitorTaskType
	}
	current, err := s.store.GetMonitorTaskConfig(ctx, taskType)
	if err != nil {
		if errors.Is(err, ErrMonitorTaskNotFound) {
			current = def.DefaultConfig
		} else {
			return MonitorTask{}, err
		}
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.IntervalSeconds != nil && *req.IntervalSeconds > 0 {
		current.IntervalSeconds = *req.IntervalSeconds
	}
	if req.Scope != nil {
		current.Scope = *req.Scope
	}
	if req.Sensitivity != nil {
		current.Sensitivity = *req.Sensitivity
	}
	if req.CooldownSeconds != nil && *req.CooldownSeconds >= 0 {
		current.CooldownSeconds = *req.CooldownSeconds
	}
	if req.AgentDoublecheckEnabled != nil {
		current.AgentDoublecheckEnabled = *req.AgentDoublecheckEnabled
	}
	if err := s.store.UpsertMonitorTaskConfig(ctx, taskType, current); err != nil {
		return MonitorTask{}, err
	}
	// 启用任一监控任务时确保后台调度在运行(幂等);周期触发由 runScheduledMonitors 检查 enabled。
	if current.Enabled {
		s.StartBackground(context.Background())
	}
	latest, _ := s.latestTaskRunSummary(ctx, taskType)
	return MonitorTask{Definition: def, Config: current, LatestRun: latest}, nil
}

// RunMonitorTask 手动或调度触发一次监控任务。disabled(Runnable=false)任务不执行。
func (s *Service) RunMonitorTask(ctx context.Context, taskType, triggerType string) (MonitorRun, error) {
	def, ok := monitorTaskDefinition(taskType)
	if !ok {
		return MonitorRun{}, ErrInvalidMonitorTaskType
	}
	if !def.Runnable {
		return MonitorRun{}, ErrMonitorTaskNotConfigured
	}
	if triggerType == "" {
		triggerType = MonitorTriggerManual
	}
	if taskType == MonitorTaskLatestQuoteRefresh {
		state, err := s.RunLatestQuoteRefreshTask(ctx, triggerType)
		if err != nil {
			return MonitorRun{}, err
		}
		return quoteRefreshTaskStateAsMonitorRun(state), nil
	}
	// 并发保护:同任务已有 running 则拒绝重复触发
	if latest, _ := s.store.GetLatestMonitorRun(ctx, taskType); latest != nil && latest.Status == MonitorRunStatusRunning {
		return MonitorRun{}, ErrMonitorTaskAlreadyRunning
	}

	now := time.Now()
	run := MonitorRun{
		ID:          generateID(),
		TaskType:    taskType,
		Status:      MonitorRunStatusRunning,
		TriggerType: triggerType,
		StartedAt:   now,
		CreatedAt:   now,
		Metadata:    map[string]any{},
	}
	created, err := s.store.CreateMonitorRun(ctx, run)
	if err != nil {
		return MonitorRun{}, err
	}

	cfg, cfgErr := s.store.GetMonitorTaskConfig(ctx, taskType)
	if cfgErr != nil {
		cfg = def.DefaultConfig
	}

	var final MonitorRun
	switch taskType {
	case MonitorTaskDataStrategyMonitor:
		final = s.runDataStrategyMonitor(ctx, created, cfg)
	case MonitorTaskPortfolioRiskMonitor:
		final = s.runPortfolioRiskMonitor(ctx, created, cfg)
	case MonitorTaskNewsStrategyMonitor:
		final = s.runNewsStrategyMonitor(ctx, created, cfg)
	default:
		final = created
		final.Status = MonitorRunStatusFailed
		final.ErrorMessage = "task type not supported"
		final.FinishedAt = time.Now()
	}
	if _, err := s.store.UpdateMonitorRun(ctx, final); err != nil {
		return final, err
	}
	return final, nil
}

func (s *Service) latestTaskRunSummary(ctx context.Context, taskType string) (*MonitorRun, error) {
	if taskType == MonitorTaskLatestQuoteRefresh {
		state, err := s.store.GetQuoteRefreshTaskState(ctx, taskType)
		if err != nil || state == nil {
			return nil, err
		}
		run := quoteRefreshTaskStateAsMonitorRun(*state)
		return &run, nil
	}
	return s.store.GetLatestMonitorRun(ctx, taskType)
}

func (s *Service) GetMonitorRun(ctx context.Context, id string) (MonitorRun, error) {
	return s.store.GetMonitorRun(ctx, id)
}

func (s *Service) ListMonitorRuns(ctx context.Context, filter MonitorRunListFilter) ([]MonitorRun, error) {
	return s.store.ListMonitorRuns(ctx, filter)
}

func (s *Service) CountMonitorRuns(ctx context.Context, filter MonitorRunListFilter) (int, error) {
	return s.store.CountMonitorRuns(ctx, filter)
}

func (s *Service) ListMonitorHits(ctx context.Context, filter MonitorHitListFilter) ([]MonitorHit, error) {
	return s.store.ListMonitorHits(ctx, filter)
}

func (s *Service) GetMonitorHit(ctx context.Context, id string) (MonitorHit, error) {
	return s.store.GetMonitorHit(ctx, id)
}

func (s *Service) CountMonitorHits(ctx context.Context, filter MonitorHitListFilter) (int, error) {
	return s.store.CountMonitorHits(ctx, filter)
}

type monitorHitPostProcessResult struct {
	ReviewID       string
	ReviewCreated  bool
	AgentRunID     string
	AgentRunStatus string
	AlertID        string
	AlertCreated   bool
}

func (s *Service) processCreatedMonitorHit(ctx context.Context, hit MonitorHit, cfg MonitorTaskConfig) (monitorHitPostProcessResult, error) {
	result := monitorHitPostProcessResult{}
	evidence := copyStringAnyMap(hit.Evidence)
	pipeline := map[string]any{
		"agentDoublecheckEnabled": cfg.AgentDoublecheckEnabled,
	}

	existingReview, existingErr := s.store.GetActiveOperationReviewByHit(ctx, hit.ID)
	if existingErr != nil {
		pipeline["reviewStatus"] = "failed"
		pipeline["error"] = safelog.Text(existingErr.Error(), 400)
		evidence["reviewPipeline"] = pipeline
		_ = s.store.UpdateMonitorHitEvidence(ctx, hit.ID, evidence, hit.AgentDecisionID)
		return result, existingErr
	}

	review, err := s.CreateReviewFromMonitorHit(ctx, hit.ID)
	if err != nil {
		pipeline["reviewStatus"] = "failed"
		pipeline["error"] = safelog.Text(err.Error(), 400)
		evidence["reviewPipeline"] = pipeline
		_ = s.store.UpdateMonitorHitEvidence(ctx, hit.ID, evidence, hit.AgentDecisionID)
		return result, err
	}
	result.ReviewID = review.ID
	result.ReviewCreated = existingReview == nil
	pipeline["reviewId"] = review.ID
	pipeline["reviewCreated"] = result.ReviewCreated
	pipeline["reviewStatus"] = review.Status

	if !cfg.AgentDoublecheckEnabled {
		pipeline["agentStatus"] = "skipped"
		pipeline["agentSkippedReason"] = "agent_doublecheck_disabled"
		evidence["agentDoublecheck"] = "not_enabled"
		evidence["reviewPipeline"] = pipeline
		if err := s.store.UpdateMonitorHitEvidence(ctx, hit.ID, evidence, hit.AgentDecisionID); err != nil {
			return result, err
		}
		alert, created, err := s.upsertMonitorAlert(ctx, hit, cfg, review, nil, AlertTriggerSourceDeterministic, "", evidence)
		if err != nil {
			return result, err
		}
		result.AlertID = alert.ID
		result.AlertCreated = created
		return result, nil
	}

	pipeline["agentAttempted"] = true
	agentRun, err := s.RunAgentReviewForReview(ctx, review.ID, "monitor:"+hit.TaskType)
	if err != nil {
		pipeline["agentStatus"] = "unavailable"
		pipeline["agentError"] = safelog.Text(err.Error(), 400)
		evidence["agentDoublecheck"] = "unavailable"
		evidence["degraded_reason"] = "agent_unavailable"
		evidence["reviewPipeline"] = pipeline
		if updateErr := s.store.UpdateMonitorHitEvidence(ctx, hit.ID, evidence, hit.AgentDecisionID); updateErr != nil {
			return result, updateErr
		}
		alert, created, alertErr := s.upsertMonitorAlert(ctx, hit, cfg, review, nil, AlertTriggerSourceDegraded, "agent_unavailable", evidence)
		if alertErr != nil {
			return result, alertErr
		}
		result.AlertID = alert.ID
		result.AlertCreated = created
		return result, nil
	}

	result.AgentRunID = agentRun.ID
	result.AgentRunStatus = agentRun.Status
	pipeline["agentRunId"] = agentRun.ID
	pipeline["agentRunStatus"] = agentRun.Status
	evidence["agentRunId"] = agentRun.ID
	evidence["decisionLedgerId"] = agentRun.DecisionLedgerID
	triggerSource := ""
	degradedReason := ""
	if s.agentExecutor == nil && agentRun.Status == AgentRunStatusReady {
		pipeline["agentStatus"] = "enabled_no_executor"
		evidence["agentDoublecheck"] = "enabled_no_executor"
		degradedReason = "agent_ready_without_executor"
		triggerSource = AlertTriggerSourceDegraded
	} else {
		pipeline["agentStatus"] = "started"
		evidence["agentDoublecheck"] = "started"
	}
	if degradedReason != "" {
		evidence["degraded_reason"] = degradedReason
	}
	evidence["reviewPipeline"] = pipeline
	if err := s.store.UpdateMonitorHitEvidence(ctx, hit.ID, evidence, agentRun.ID); err != nil {
		return result, err
	}
	if triggerSource == "" {
		return result, nil
	}
	alert, created, err := s.upsertMonitorAlert(ctx, hit, cfg, review, &agentRun, triggerSource, degradedReason, evidence)
	if err != nil {
		return result, err
	}
	result.AlertID = alert.ID
	result.AlertCreated = created
	return result, nil
}

func (s *Service) upsertMonitorAlert(
	ctx context.Context,
	hit MonitorHit,
	cfg MonitorTaskConfig,
	review OperationReview,
	agentRun *AgentRun,
	triggerSource string,
	degradedReason string,
	sourceEvidence map[string]any,
) (StockV2Alert, bool, error) {
	now := time.Now()
	evidence := monitorAlertEvidence(hit, review, agentRun, triggerSource, degradedReason, sourceEvidence)
	dedupeKey := monitorAlertDedupeKey(hit, evidence)
	if dedupeKey != "" {
		existing, err := s.store.FindLatestAlertByDedupeKey(ctx, dedupeKey)
		if err != nil && !errors.Is(err, ErrAlertNotFound) {
			return StockV2Alert{}, false, err
		}
		if err == nil {
			if existing.MonitorHitID == hit.ID {
				existing.ReviewID = review.ID
				existing.ReviewStatus = review.Status
				if agentRun != nil {
					existing.AgentRunID = agentRun.ID
					existing.DecisionLedgerID = agentRun.DecisionLedgerID
				}
				existing.TriggerSource = triggerSource
				existing.Level = monitorAlertLevel(hit, triggerSource)
				existing.Summary = hit.Summary
				existing.LastSeenAt = now
				existing.TriggeredAt = now
				existing.Evidence = mergeMonitorAlertEvidence(existing.Evidence, evidence, hit)
				updated, updateErr := s.store.UpdateAlert(ctx, existing)
				if updateErr != nil {
					return StockV2Alert{}, false, updateErr
				}
				if linkErr := s.store.UpdateMonitorHitAlert(ctx, hit.ID, existing.ID, MonitorHitStatusAlerted); linkErr != nil {
					return StockV2Alert{}, false, linkErr
				}
				return updated, false, nil
			}
			if monitorAlertWithinCooldown(existing, now, cfg.CooldownSeconds) {
				existing.MonitorHitID = hit.ID
				existing.MonitorRunID = hit.RunID
				existing.TaskType = hit.TaskType
				existing.StrategyID = hit.StrategyID
				existing.PortfolioID = hit.PortfolioID
				existing.Symbol = hit.Symbol
				existing.Market = hit.Market
				existing.ReviewID = review.ID
				existing.ReviewStatus = review.Status
				if agentRun != nil {
					existing.AgentRunID = agentRun.ID
					existing.DecisionLedgerID = agentRun.DecisionLedgerID
				}
				existing.TriggerSource = triggerSource
				existing.Level = monitorAlertLevel(hit, triggerSource)
				existing.Summary = hit.Summary
				existing.OccurrenceCount++
				existing.LastSeenAt = now
				existing.TriggeredAt = now
				existing.Evidence = mergeMonitorAlertEvidence(existing.Evidence, evidence, hit)
				updated, updateErr := s.store.UpdateAlert(ctx, existing)
				if updateErr != nil {
					return StockV2Alert{}, false, updateErr
				}
				if linkErr := s.store.UpdateMonitorHitAlert(ctx, hit.ID, updated.ID, MonitorHitStatusAlerted); linkErr != nil {
					return StockV2Alert{}, false, linkErr
				}
				return updated, false, nil
			}
		}
	}

	alert := StockV2Alert{
		ID:              generateID(),
		WatchID:         "",
		MonitorHitID:    hit.ID,
		MonitorRunID:    hit.RunID,
		TaskType:        hit.TaskType,
		StrategyID:      hit.StrategyID,
		PortfolioID:     hit.PortfolioID,
		Symbol:          hit.Symbol,
		Market:          hit.Market,
		ReviewID:        review.ID,
		ReviewStatus:    review.Status,
		TriggerSource:   triggerSource,
		Status:          AlertStatusOpen,
		Level:           monitorAlertLevel(hit, triggerSource),
		Title:           strings.TrimSpace(hit.Title),
		Summary:         strings.TrimSpace(hit.Summary),
		DedupeKey:       dedupeKey,
		Evidence:        mergeMonitorAlertEvidence(nil, evidence, hit),
		OccurrenceCount: 1,
		FirstSeenAt:     now,
		LastSeenAt:      now,
		TriggeredAt:     now,
	}
	if alert.Title == "" {
		alert.Title = "监控提醒"
	}
	if agentRun != nil {
		alert.AgentRunID = agentRun.ID
		alert.DecisionLedgerID = agentRun.DecisionLedgerID
	}
	created, err := s.store.CreateAlert(ctx, alert)
	if err != nil {
		return StockV2Alert{}, false, err
	}
	if linkErr := s.store.UpdateMonitorHitAlert(ctx, hit.ID, created.ID, MonitorHitStatusAlerted); linkErr != nil {
		return StockV2Alert{}, false, linkErr
	}
	return created, true, nil
}

func monitorAlertEvidence(
	hit MonitorHit,
	review OperationReview,
	agentRun *AgentRun,
	triggerSource string,
	degradedReason string,
	source map[string]any,
) map[string]any {
	evidence := copyStringAnyMap(source)
	evidence["trigger_source"] = triggerSource
	switch triggerSource {
	case AlertTriggerSourceAgentConfirmed:
		evidence["trigger_decision"] = "agent_confirmed"
	case AlertTriggerSourceManualReviewConfirmed:
		evidence["trigger_decision"] = "manual_review_confirmed"
	case AlertTriggerSourceDeterministic:
		evidence["trigger_decision"] = "deterministic_policy"
	default:
		evidence["trigger_decision"] = "degraded_policy"
	}
	if degradedReason != "" {
		evidence["degraded_reason"] = degradedReason
		evidence["agent_status"] = degradedReason
	}
	evidence["monitorHitId"] = hit.ID
	evidence["monitorRunId"] = hit.RunID
	evidence["taskType"] = hit.TaskType
	evidence["reviewId"] = review.ID
	evidence["reviewStatus"] = review.Status
	if review.OutputType != "" {
		evidence["reviewOutputType"] = review.OutputType
	}
	if review.ResultSummary != "" {
		evidence["reviewResultSummary"] = review.ResultSummary
	}
	if agentRun != nil {
		evidence["agentRunId"] = agentRun.ID
		evidence["agentRunStatus"] = agentRun.Status
		evidence["decisionLedgerId"] = agentRun.DecisionLedgerID
	}
	return evidence
}

func monitorAlertDedupeKey(hit MonitorHit, evidence map[string]any) string {
	action := firstNonEmpty(
		stringFromAny(evidence["matchedAction"]),
		stringFromAny(evidence["matchedActionLabel"]),
		stringFromAny(evidence["matchedRuleId"]),
	)
	prefilter := firstNonEmpty(
		stringFromAny(evidence["matchedPrefilterKey"]),
		stringFromAny(evidence["matchedPrefilterType"]),
		monitorAlertThresholdKey(evidence),
		stringFromAny(evidence["matchedRuleTitle"]),
	)
	if action == "" {
		action = strings.TrimSpace(hit.Title)
	}
	if prefilter == "" {
		prefilter = strings.TrimSpace(hit.Title)
	}
	parts := []string{
		"monitor",
		strings.TrimSpace(hit.TaskType),
		strings.TrimSpace(hit.StrategyID),
		strings.TrimSpace(hit.PortfolioID),
		strings.TrimSpace(hit.Symbol),
		strings.TrimSpace(action),
		strings.TrimSpace(prefilter),
	}
	return strings.Join(parts, "|")
}

func monitorAlertThresholdKey(evidence map[string]any) string {
	ruleType := stringFromAny(evidence["matchedPrefilterType"])
	threshold := stringFromAny(evidence["matchedThreshold"])
	if ruleType == "" && threshold == "" {
		return ""
	}
	return ruleType + ":" + threshold
}

func monitorAlertWithinCooldown(alert StockV2Alert, now time.Time, cooldownSeconds int) bool {
	if cooldownSeconds <= 0 {
		return false
	}
	last := alert.LastSeenAt
	if last.IsZero() {
		last = alert.TriggeredAt
	}
	if last.IsZero() {
		last = alert.CreatedAt
	}
	if last.IsZero() {
		return false
	}
	return !now.After(last.Add(time.Duration(cooldownSeconds) * time.Second))
}

func mergeMonitorAlertEvidence(current map[string]any, next map[string]any, hit MonitorHit) map[string]any {
	merged := copyStringAnyMap(current)
	for key, value := range next {
		merged[key] = value
	}
	merged["lastHitId"] = hit.ID
	merged["lastRunId"] = hit.RunID
	merged["lastSummary"] = hit.Summary
	merged["lastTitle"] = hit.Title
	return merged
}

func monitorAlertLevel(hit MonitorHit, triggerSource string) string {
	if hit.TaskType == MonitorTaskPortfolioRiskMonitor && strings.Contains(hit.Title, "超限") {
		return AlertLevelCritical
	}
	if triggerSource == AlertTriggerSourceDeterministic {
		return AlertLevelWarning
	}
	if triggerSource == AlertTriggerSourceDegraded {
		return AlertLevelWarning
	}
	return AlertLevelInfo
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// runDataStrategyMonitor 扫描 active 单票策略,优先用操作剧本里的数据/组合预筛产出动作候选。
// ponytail: 保留旧 priceTriggers 兜底,兼容已有策略版本,监控判断仍复用 watch evaluator。
func (s *Service) runDataStrategyMonitor(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig) MonitorRun {
	run.Metadata["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	strategies, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:   StrategyKindSymbolStrategy,
		Status: StrategyStatusActive,
		Limit:  500,
	})
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	run.ScannedCount = len(strategies)
	for _, sw := range strategies {
		if sw.ActiveVersion == nil || sw.Strategy.Symbol == "" {
			continue
		}
		hasPlaybookPrefilters, playbookMatched, playbookHits, playbookFailures, playbookReviews, playbookAlerts := s.runStrategyPlaybookPrefilters(ctx, run, cfg, sw)
		run.HitCount += playbookHits
		run.FailedCount += playbookFailures
		run.ReviewCount += playbookReviews
		run.AlertCount += playbookAlerts
		if hasPlaybookPrefilters {
			if playbookMatched {
				run.SuccessCount++
			}
			continue
		}
		triggerConfig, err := s.triggerConfigFromStrategy(ctx, sw)
		if err != nil {
			run.FailedCount++
			continue
		}
		tempWatch := StockV2Watch{
			Symbol:        sw.Strategy.Symbol,
			Market:        sw.Strategy.Market,
			PortfolioID:   sw.Strategy.PortfolioID,
			TriggerPolicy: WatchTriggerPolicyAny,
			TriggerConfig: triggerConfig,
		}
		rules := watchRulesFromConfig(tempWatch)
		matched := false
		for _, rule := range rules {
			rr := s.evaluateWatchRule(ctx, tempWatch, rule, run.StartedAt)
			if rr.Status == WatchRunStatusMatched {
				evidence := monitorEvidenceWithAgentState(rr.Evidence, cfg)
				if playbook := mapFromAny(sw.ActiveVersion.GenerationMeta["playbook"]); len(playbook) > 0 {
					evidence["playbook"] = playbook
				}
				hit := MonitorHit{
					RunID:      run.ID,
					TaskType:   MonitorTaskDataStrategyMonitor,
					Status:     MonitorHitStatusCandidate,
					StrategyID: sw.Strategy.ID,
					Symbol:     sw.Strategy.Symbol,
					Market:     sw.Strategy.Market,
					Title:      alertTitleForRule(rr),
					Summary:    rr.Reason,
					Evidence:   evidence,
				}
				hit.Evidence["matchedPrefilterType"] = rr.RuleType
				hit.Evidence["matchedThreshold"] = rr.Threshold
				hit.Evidence["matchedPrefilterKey"] = rr.RuleKey
				if threshold := stringFromAny(rr.Threshold); threshold != "" {
					hit.Evidence["matchedPrefilterKey"] = rr.RuleKey + ":" + threshold
				}
				createdHit, err := s.store.CreateMonitorHit(ctx, hit)
				if err != nil {
					run.FailedCount++
					continue
				}
				post, err := s.processCreatedMonitorHit(ctx, createdHit, cfg)
				if post.ReviewCreated {
					run.ReviewCount++
				}
				if post.AlertID != "" {
					run.AlertCount++
				}
				if err != nil {
					run.FailedCount++
				}
				run.HitCount++
				matched = true
			}
		}
		if matched {
			run.SuccessCount++
		}
	}
	run.Status = MonitorRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ScopeSummary = scopeSummaryFromCount(run.ScannedCount, "strategies")
	return run
}

func (s *Service) runStrategyPlaybookPrefilters(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig, sw StrategyWithVersion) (bool, bool, int, int, int, int) {
	actions := playbookActionMapsFromMeta(sw.ActiveVersion.GenerationMeta)
	hasPrefilters := false
	matched := false
	hitCount := 0
	failedCount := 0
	reviewCount := 0
	alertCount := 0

	for _, action := range actions {
		rules := playbookActionWatchRules(action, sw.Strategy.Symbol, sw.Strategy.PortfolioID)
		if len(rules) == 0 {
			continue
		}
		hasPrefilters = true
		tempWatch := StockV2Watch{
			Symbol:        sw.Strategy.Symbol,
			Market:        sw.Strategy.Market,
			PortfolioID:   sw.Strategy.PortfolioID,
			TriggerPolicy: WatchTriggerPolicyAny,
		}
		for _, rule := range rules {
			rr := s.evaluateWatchRule(ctx, tempWatch, rule, run.StartedAt)
			if rr.Status != WatchRunStatusMatched {
				continue
			}
			evidence := monitorEvidenceWithAgentState(rr.Evidence, cfg)
			actionID := firstRuleString(action, "id")
			actionType := firstRuleString(action, "action")
			actionTitle := firstRuleString(action, "title")
			evidence["matchedAction"] = actionType
			evidence["matchedActionLabel"] = strategyActionLabel(actionType)
			evidence["matchedRuleId"] = actionID
			evidence["matchedRuleTitle"] = actionTitle
			evidence["matchedPrefilterKey"] = rr.RuleKey
			evidence["matchedPrefilterType"] = rr.RuleType
			evidence["playbookRule"] = action

			title := "策略动作候选: " + strategyActionLabel(actionType)
			if actionTitle != "" {
				title += " · " + actionTitle
			}
			hit := MonitorHit{
				RunID:       run.ID,
				TaskType:    MonitorTaskDataStrategyMonitor,
				Status:      MonitorHitStatusCandidate,
				StrategyID:  sw.Strategy.ID,
				PortfolioID: sw.Strategy.PortfolioID,
				Symbol:      sw.Strategy.Symbol,
				Market:      sw.Strategy.Market,
				Title:       title,
				Summary:     rr.Reason,
				Evidence:    evidence,
			}
			createdHit, err := s.store.CreateMonitorHit(ctx, hit)
			if err != nil {
				failedCount++
				continue
			}
			post, err := s.processCreatedMonitorHit(ctx, createdHit, cfg)
			if post.ReviewCreated {
				reviewCount++
			}
			if post.AlertID != "" {
				alertCount++
			}
			if err != nil {
				failedCount++
			}
			hitCount++
			matched = true
		}
	}
	return hasPrefilters, matched, hitCount, failedCount, reviewCount, alertCount
}

// runNewsStrategyMonitor 把消息候选接入现有 MonitorHit -> Review 链路。
// ponytail: 第一版只用候选分数和已有关联对象判断;向量召回/复杂重要性识别留在 NewsLink 层升级。
func (s *Service) runNewsStrategyMonitor(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig) MonitorRun {
	run.Metadata["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	run.Metadata["highScoreThreshold"] = newsMonitorHighScoreThreshold

	candidates, err := s.store.ListPendingNewsLinkCandidates(ctx, newsMonitorBatchLimit)
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	run.ScannedCount = len(candidates)
	portfolioBySymbol, err := s.newsMonitorPortfolioBySymbol(ctx)
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	strategyBySymbol, err := s.newsMonitorActiveStrategyBySymbol(ctx)
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}

	now := time.Now()
	for _, candidate := range candidates {
		event, err := s.store.GetNewsEvent(ctx, candidate.NewsEventID)
		if err != nil {
			run.FailedCount++
			_ = s.store.MarkNewsLinkCandidateMonitorStatus(ctx, candidate.ID, NewsLinkMonitorStatusFailed, "", now)
			continue
		}
		decision := newsMonitorCandidateDecision(candidate, event, portfolioBySymbol, strategyBySymbol)
		if !decision.Hit {
			if err := s.store.MarkNewsLinkCandidateMonitorStatus(ctx, candidate.ID, NewsLinkMonitorStatusSkipped, "", now); err != nil {
				run.FailedCount++
				continue
			}
			run.SuccessCount++
			continue
		}

		evidence := monitorEvidenceWithAgentState(newsMonitorEvidence(event, candidate, decision), cfg)
		hit := MonitorHit{
			RunID:       run.ID,
			TaskType:    MonitorTaskNewsStrategyMonitor,
			Status:      MonitorHitStatusCandidate,
			StrategyID:  decision.StrategyID,
			PortfolioID: decision.PortfolioID,
			Symbol:      candidate.Symbol,
			Market:      candidate.Market,
			Title:       newsMonitorHitTitle(candidate),
			Summary:     event.Title,
			Evidence:    evidence,
		}
		createdHit, err := s.store.CreateMonitorHit(ctx, hit)
		if err != nil {
			run.FailedCount++
			_ = s.store.MarkNewsLinkCandidateMonitorStatus(ctx, candidate.ID, NewsLinkMonitorStatusFailed, "", now)
			continue
		}
		if err := s.store.MarkNewsLinkCandidateMonitorStatus(ctx, candidate.ID, NewsLinkMonitorStatusHit, createdHit.ID, now); err != nil {
			run.FailedCount++
		}
		post, err := s.processCreatedMonitorHit(ctx, createdHit, cfg)
		if post.ReviewCreated {
			run.ReviewCount++
		}
		if post.AlertID != "" {
			run.AlertCount++
		}
		if err != nil {
			run.FailedCount++
		}
		run.HitCount++
		run.SuccessCount++
	}
	run.Status = MonitorRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ScopeSummary = scopeSummaryFromCount(run.ScannedCount, "news candidates")
	return run
}

// runPortfolioRiskMonitor 扫描组合快照与持仓,检查单票权重与数据新鲜度,命中产候选 hit。
func (s *Service) runPortfolioRiskMonitor(ctx context.Context, run MonitorRun, cfg MonitorTaskConfig) MonitorRun {
	run.Metadata["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		run.Status = MonitorRunStatusFailed
		run.ErrorMessage = err.Error()
		run.FinishedAt = time.Now()
		return run
	}
	run.ScannedCount = len(portfolios)
	for _, portfolio := range portfolios {
		snapshots, err := s.store.GetPortfolioSnapshots(ctx, portfolio.ID, 1)
		if err != nil || len(snapshots) == 0 {
			continue
		}
		snapshot := snapshots[0]
		// 数据过期:估值非 fresh 或存在 stale quote
		if snapshot.Status != PortfolioValuationStatusFresh || snapshot.StaleQuoteCount > 0 {
			hit := MonitorHit{
				RunID:       run.ID,
				TaskType:    MonitorTaskPortfolioRiskMonitor,
				Status:      MonitorHitStatusCandidate,
				PortfolioID: portfolio.ID,
				Title:       "组合行情数据过期",
				Summary:     "最新组合快照估值不新鲜或存在 stale quote,需先刷新行情。",
				Evidence:    monitorEvidenceWithAgentState(portfolioEvidence(snapshot, nil), cfg),
			}
			createdHit, err := s.store.CreateMonitorHit(ctx, hit)
			if err == nil {
				post, postErr := s.processCreatedMonitorHit(ctx, createdHit, cfg)
				if post.ReviewCreated {
					run.ReviewCount++
				}
				if post.AlertID != "" {
					run.AlertCount++
				}
				if postErr != nil {
					run.FailedCount++
				}
				run.HitCount++
			} else {
				run.FailedCount++
			}
		}
		// 单票权重过高
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			continue
		}
		limit := portfolio.MaxSinglePositionPct
		if limit <= 0 {
			limit = 20
		}
		for _, holding := range holdings {
			weight := holding.PositionPct
			if weight <= 0 && holding.MarketValue > 0 && snapshot.TotalAssetValue > 0 {
				weight = holding.MarketValue / snapshot.TotalAssetValue * 100
			}
			if weight > limit {
				hit := MonitorHit{
					RunID:       run.ID,
					TaskType:    MonitorTaskPortfolioRiskMonitor,
					Status:      MonitorHitStatusCandidate,
					PortfolioID: portfolio.ID,
					Symbol:      holding.Symbol,
					Market:      holding.Market,
					Title:       "单票仓位占比超限",
					Summary:     "持仓权重超过组合单票上限约束。",
					Evidence:    monitorEvidenceWithAgentState(portfolioEvidence(snapshot, &holding), cfg),
				}
				createdHit, err := s.store.CreateMonitorHit(ctx, hit)
				if err == nil {
					post, postErr := s.processCreatedMonitorHit(ctx, createdHit, cfg)
					if post.ReviewCreated {
						run.ReviewCount++
					}
					if post.AlertID != "" {
						run.AlertCount++
					}
					if postErr != nil {
						run.FailedCount++
					}
					run.HitCount++
				} else {
					run.FailedCount++
				}
			}
		}
		run.SuccessCount++
	}
	run.Status = MonitorRunStatusCompleted
	run.FinishedAt = time.Now()
	run.ScopeSummary = scopeSummaryFromCount(run.ScannedCount, "portfolios")
	return run
}

func (s *Service) RunLatestQuoteRefreshTask(ctx context.Context, triggerType string) (QuoteRefreshTaskState, error) {
	if triggerType == "" {
		triggerType = MonitorTriggerManual
	}
	if current, _ := s.store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh); current != nil && current.Status == MonitorRunStatusRunning {
		return QuoteRefreshTaskState{}, ErrMonitorTaskAlreadyRunning
	}

	startedAt := time.Now()
	symbols := s.collectMonitorSymbols(ctx)
	state := QuoteRefreshTaskState{
		TaskType:     MonitorTaskLatestQuoteRefresh,
		Status:       MonitorRunStatusRunning,
		TriggerType:  triggerType,
		StartedAt:    startedAt,
		ScopeSummary: scopeSummaryFromCount(len(symbols), "symbols"),
		ScannedCount: len(symbols),
		UpdatedAt:    startedAt,
	}
	if err := s.store.UpsertQuoteRefreshTaskState(ctx, state); err != nil {
		return QuoteRefreshTaskState{}, err
	}

	if len(symbols) == 0 {
		finishedAt := time.Now()
		state.Status = MonitorRunStatusCompleted
		state.FinishedAt = finishedAt
		state.UpdatedAt = finishedAt
		if err := s.store.UpsertQuoteRefreshTaskState(ctx, state); err != nil {
			return state, err
		}
		return state, nil
	}

	result, err := s.RefreshLatestQuotes(ctx, symbols, "monitor")
	if err == nil {
		err = s.RefreshPortfoliosFromLatestQuotes(ctx, result.Items)
	}
	finishedAt := time.Now()
	state.FinishedAt = finishedAt
	state.UpdatedAt = finishedAt
	state.SuccessCount = result.RefreshedCount
	state.FailedCount = result.FailedCount
	if err != nil {
		state.Status = MonitorRunStatusFailed
		state.ErrorMessage = err.Error()
	} else {
		state.Status = MonitorRunStatusCompleted
	}
	if saveErr := s.store.UpsertQuoteRefreshTaskState(ctx, state); saveErr != nil {
		if err != nil {
			return state, err
		}
		return state, saveErr
	}
	return state, err
}

func quoteRefreshTaskStateAsMonitorRun(state QuoteRefreshTaskState) MonitorRun {
	return MonitorRun{
		ID:           state.TaskType,
		TaskType:     state.TaskType,
		Status:       state.Status,
		TriggerType:  state.TriggerType,
		StartedAt:    state.StartedAt,
		FinishedAt:   state.FinishedAt,
		ScopeSummary: state.ScopeSummary,
		ScannedCount: state.ScannedCount,
		SuccessCount: state.SuccessCount,
		FailedCount:  state.FailedCount,
		ErrorMessage: state.ErrorMessage,
		CreatedAt:    state.StartedAt,
		Metadata:     map[string]any{"stateOnly": true},
	}
}

func (s *Service) collectMonitorSymbols(ctx context.Context) []string {
	seen := make(map[string]struct{})
	symbols := make([]string, 0)
	addSymbol := func(symbol string) {
		if symbol == "" {
			return
		}
		if _, ok := seen[symbol]; ok {
			return
		}
		seen[symbol] = struct{}{}
		symbols = append(symbols, symbol)
	}

	portfolios, err := s.store.ListPortfolios(ctx)
	if err == nil {
		for _, portfolio := range portfolios {
			holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
			if err != nil {
				continue
			}
			for _, holding := range holdings {
				addSymbol(holding.Symbol)
			}
		}
	}

	strategies, err := s.store.ListStrategies(ctx, StrategyListFilter{
		Kind:   StrategyKindSymbolStrategy,
		Status: StrategyStatusActive,
		Limit:  500,
	})
	if err == nil {
		for _, strategy := range strategies {
			addSymbol(strategy.Strategy.Symbol)
		}
	}
	return symbols
}

func monitorAgentDecisionState(cfg MonitorTaskConfig) string {
	if cfg.AgentDoublecheckEnabled {
		return "enabled_no_executor"
	}
	return "not_enabled"
}

func monitorEvidenceWithAgentState(evidence map[string]any, cfg MonitorTaskConfig) map[string]any {
	next := make(map[string]any, len(evidence)+1)
	for key, value := range evidence {
		next[key] = value
	}
	next["agentDoublecheck"] = monitorAgentDecisionState(cfg)
	return next
}

type newsMonitorDecision struct {
	Hit         bool
	StrategyID  string
	PortfolioID string
	Reasons     []string
}

func newsMonitorCandidateDecision(
	candidate NewsLinkCandidate,
	event NewsEvent,
	portfolioBySymbol map[string]string,
	strategyBySymbol map[string]StrategyWithVersion,
) newsMonitorDecision {
	if newsEventLowQuality(event) {
		return newsMonitorDecision{Reasons: []string{"low_quality"}}
	}
	decision := newsMonitorDecision{}
	if portfolioID := portfolioBySymbol[strings.TrimSpace(candidate.Symbol)]; portfolioID != "" {
		decision.Hit = true
		decision.PortfolioID = portfolioID
		decision.Reasons = append(decision.Reasons, "current_holding")
	}
	if strategy, ok := strategyBySymbol[strings.TrimSpace(candidate.Symbol)]; ok {
		decision.Hit = true
		decision.StrategyID = strategy.Strategy.ID
		if decision.PortfolioID == "" {
			decision.PortfolioID = strategy.Strategy.PortfolioID
		}
		decision.Reasons = append(decision.Reasons, "active_strategy")
	}
	if newsEventImportant(event) {
		decision.Hit = true
		decision.Reasons = append(decision.Reasons, "important_news")
	}
	if candidate.Score >= newsMonitorHighScoreThreshold {
		decision.Hit = true
		decision.Reasons = append(decision.Reasons, "high_score")
	}
	if !decision.Hit {
		decision.Reasons = append(decision.Reasons, "low_score_no_relevant_object")
	}
	return decision
}

func newsEventLowQuality(event NewsEvent) bool {
	quality := strings.ToLower(strings.TrimSpace(event.QualityStatus))
	return quality == NewsQualityLow || quality == "invalid" || quality == "spam"
}

func newsEventImportant(event NewsEvent) bool {
	quality := strings.ToLower(strings.TrimSpace(event.QualityStatus))
	return quality == NewsImportanceHigh || quality == "important" || quality == "high"
}

func newsMonitorEvidence(event NewsEvent, candidate NewsLinkCandidate, decision newsMonitorDecision) map[string]any {
	eventTime := ""
	if !event.EventAt.IsZero() {
		eventTime = event.EventAt.Format(time.RFC3339)
	}
	return map[string]any{
		"sourceType":            "news",
		"matchedAction":         "news_review",
		"matchedPrefilterType":  "news_link_candidate",
		"matchedPrefilterKey":   candidate.ID,
		"monitorMatchReason":    strings.Join(decision.Reasons, "；"),
		"news_event_id":         event.ID,
		"raw_news_id":           event.RawNewsID,
		"candidate_id":          candidate.ID,
		"match_method":          candidate.MatchMethod,
		"score":                 candidate.Score,
		"reason":                candidate.Reason,
		"matched_terms":         candidate.MatchedTerms,
		"source":                event.Source,
		"event_time":            eventTime,
		"quality_status":        event.QualityStatus,
		"instrument_name":       candidate.InstrumentName,
		"news_title":            event.Title,
		"news_summary":          event.Summary,
		"news_url":              event.URL,
		"news_monitor_decision": decision.Reasons,
	}
}

func newsMonitorHitTitle(candidate NewsLinkCandidate) string {
	name := strings.TrimSpace(candidate.InstrumentName)
	if name == "" {
		name = strings.TrimSpace(candidate.Symbol)
	}
	if name == "" {
		return "消息面候选"
	}
	return "消息面候选: " + name
}

func (s *Service) newsMonitorPortfolioBySymbol(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		return nil, err
	}
	for _, portfolio := range portfolios {
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			return nil, err
		}
		for _, holding := range holdings {
			symbol := strings.TrimSpace(holding.Symbol)
			if symbol == "" || holding.Quantity <= 0 || out[symbol] != "" {
				continue
			}
			out[symbol] = portfolio.ID
		}
	}
	return out, nil
}

func (s *Service) newsMonitorActiveStrategyBySymbol(ctx context.Context) (map[string]StrategyWithVersion, error) {
	out := map[string]StrategyWithVersion{}
	const pageSize = 200
	for offset := 0; ; offset += pageSize {
		items, err := s.store.ListStrategies(ctx, StrategyListFilter{Status: StrategyStatusActive, Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			symbol := strings.TrimSpace(item.Strategy.Symbol)
			if symbol == "" {
				continue
			}
			if _, exists := out[symbol]; !exists {
				out[symbol] = item
			}
		}
	}
	return out, nil
}

func playbookActionMapsFromMeta(meta map[string]any) []map[string]any {
	playbook := mapFromAny(meta["playbook"])
	rawRules := arrayFromAny(playbook["rules"])
	actions := make([]map[string]any, 0, len(rawRules))
	for _, raw := range rawRules {
		if action := mapFromAny(raw); len(action) > 0 {
			actions = append(actions, action)
		}
	}
	return actions
}

func playbookActionWatchRules(action map[string]any, symbol, portfolioID string) []watchRule {
	rawRules := make([]any, 0)
	rawRules = append(rawRules, arrayFromAny(action["dataPrefilters"])...)
	rawRules = append(rawRules, arrayFromAny(action["portfolioPrefilters"])...)
	if len(rawRules) == 0 {
		return nil
	}
	filtered := make([]any, 0, len(rawRules))
	for _, raw := range rawRules {
		prefilter := mapFromAny(raw)
		if playbookPrefilterReady(prefilter) {
			filtered = append(filtered, prefilter)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	watch := StockV2Watch{
		Symbol:      symbol,
		PortfolioID: portfolioID,
		TriggerConfig: map[string]any{
			"rules": filtered,
		},
	}
	return watchRulesFromConfig(watch)
}

func playbookPrefilterReady(prefilter map[string]any) bool {
	ruleType := normalizeWatchRuleType(firstRuleString(prefilter, "type", "ruleType"))
	switch ruleType {
	case "":
		return false
	case WatchRuleQuoteStale:
		return true
	case WatchRulePriceBetween:
		return ruleNumberPresent(prefilter, "low", "lower", "min") && ruleNumberPresent(prefilter, "high", "upper", "max")
	default:
		return ruleNumberPresent(prefilter, "threshold", "value")
	}
}

func ruleNumberPresent(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := m[key].(type) {
		case float64, float32, int, int64:
			return true
		case jsonNumber:
			_, err := value.Float64()
			return err == nil
		case string:
			_, err := strconv.ParseFloat(value, 64)
			return err == nil
		}
	}
	return false
}

func arrayFromAny(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, item)
		}
		return items
	default:
		return nil
	}
}

func strategyActionLabel(action string) string {
	switch action {
	case "observe":
		return "观察"
	case "build_position":
		return "建仓"
	case "add_position":
		return "加仓"
	case "hold":
		return "持有"
	case "reduce_position":
		return "减仓"
	case "exit_position":
		return "清仓"
	default:
		if action == "" {
			return "动作"
		}
		return action
	}
}

func scopeSummaryFromCount(count int, unit string) string {
	if count <= 0 {
		return ""
	}
	return "scanned " + strconv.Itoa(count) + " " + unit
}
