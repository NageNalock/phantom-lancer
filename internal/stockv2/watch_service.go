package stockv2

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) CreateWatch(ctx context.Context, req RequestCreateWatch) (StockV2Watch, error) {
	watch := watchFromCreateRequest(req)
	if err := s.fillWatchStrategyVersion(ctx, &watch); err != nil {
		return StockV2Watch{}, err
	}
	if err := s.validateWatch(ctx, watch); err != nil {
		return StockV2Watch{}, err
	}
	return s.store.CreateWatch(ctx, watch)
}

func (s *Service) ListWatches(ctx context.Context, filter WatchListFilter) ([]StockV2Watch, error) {
	return s.store.ListWatches(ctx, filter)
}

func (s *Service) CountWatches(ctx context.Context, filter WatchListFilter) (int, error) {
	return s.store.CountWatches(ctx, filter)
}

func (s *Service) GetWatch(ctx context.Context, id string) (StockV2Watch, error) {
	return s.store.GetWatch(ctx, id)
}

func (s *Service) UpdateWatch(ctx context.Context, id string, req RequestUpdateWatch) (StockV2Watch, error) {
	watch, err := s.store.GetWatch(ctx, id)
	if err != nil {
		return StockV2Watch{}, err
	}
	if watch.Status == WatchStatusArchived {
		return StockV2Watch{}, ErrWatchArchived
	}
	applyWatchUpdate(&watch, req)
	if err := s.fillWatchStrategyVersion(ctx, &watch); err != nil {
		return StockV2Watch{}, err
	}
	if err := s.validateWatch(ctx, watch); err != nil {
		return StockV2Watch{}, err
	}
	return s.store.UpdateWatch(ctx, watch)
}

func (s *Service) ActivateWatch(ctx context.Context, id string) (StockV2Watch, error) {
	return s.setWatchStatus(ctx, id, WatchStatusActive)
}

func (s *Service) PauseWatch(ctx context.Context, id string) (StockV2Watch, error) {
	return s.setWatchStatus(ctx, id, WatchStatusPaused)
}

func (s *Service) ArchiveWatch(ctx context.Context, id string) (StockV2Watch, error) {
	watch, err := s.store.GetWatch(ctx, id)
	if err != nil {
		return StockV2Watch{}, err
	}
	if watch.Status == WatchStatusArchived {
		return watch, nil
	}
	watch.Status = WatchStatusArchived
	watch.ArchivedAt = time.Now()
	return s.store.UpdateWatch(ctx, watch)
}

func (s *Service) CreateStrategyWatch(ctx context.Context, strategyID string) (StockV2Watch, error) {
	if existing, err := s.store.FindNonArchivedStrategyWatch(ctx, strategyID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrWatchNotFound) {
		return StockV2Watch{}, err
	}

	strategy, err := s.store.GetStrategy(ctx, strategyID)
	if err != nil {
		return StockV2Watch{}, err
	}
	if strategy.ActiveVersion == nil || strategy.Strategy.ActiveVersionID == "" {
		return StockV2Watch{}, ErrStrategyVersionNotFound
	}
	if strategy.Strategy.Symbol == "" && strategy.Strategy.PortfolioID == "" {
		return StockV2Watch{}, ErrInvalidStrategySymbol
	}

	triggerConfig, err := s.triggerConfigFromStrategy(ctx, strategy)
	if err != nil {
		return StockV2Watch{}, err
	}
	return s.CreateWatch(ctx, RequestCreateWatch{
		Name:              "策略盯盘 - " + strategy.Strategy.Name,
		Source:            WatchSourceStrategy,
		Symbol:            strategy.Strategy.Symbol,
		Market:            strategy.Strategy.Market,
		PortfolioID:       strategy.Strategy.PortfolioID,
		StrategyID:        strategy.Strategy.ID,
		StrategyVersionID: strategy.Strategy.ActiveVersionID,
		TriggerPolicy:     WatchTriggerPolicyAny,
		TriggerConfig:     triggerConfig,
		ScheduleKind:      WatchScheduleDaily,
		CooldownSeconds:   3600,
	})
}

func (s *Service) CreatePortfolioMonitorWatch(ctx context.Context, portfolioID string) (StockV2Watch, error) {
	if existing, err := s.store.FindNonArchivedPortfolioMonitorWatch(ctx, portfolioID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrWatchNotFound) {
		return StockV2Watch{}, err
	}

	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		return StockV2Watch{}, err
	}
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return StockV2Watch{}, err
	}

	return s.CreateWatch(ctx, RequestCreateWatch{
		Name:            "组合监控盯盘 - " + portfolio.Name,
		Source:          WatchSourcePortfolioMonitor,
		PortfolioID:     portfolio.ID,
		TriggerPolicy:   WatchTriggerPolicyAny,
		TriggerConfig:   triggerConfigFromPortfolio(portfolio, holdings),
		ScheduleKind:    WatchScheduleDaily,
		CooldownSeconds: 3600,
	})
}

func (s *Service) CreateAlert(ctx context.Context, req RequestCreateAlert) (StockV2Alert, error) {
	alert := alertFromCreateRequest(req)
	if err := validateAlert(alert); err != nil {
		return StockV2Alert{}, err
	}

	watch, err := s.store.GetWatch(ctx, alert.WatchID)
	if err != nil {
		return StockV2Alert{}, err
	}
	if watch.Status == WatchStatusArchived {
		return StockV2Alert{}, ErrWatchArchived
	}
	if watch.Status != WatchStatusActive {
		return StockV2Alert{}, ErrWatchNotActive
	}

	if alert.DedupeKey != "" {
		existing, err := s.store.FindLatestAlertByDedupeKey(ctx, alert.WatchID, alert.DedupeKey)
		if err != nil && !errors.Is(err, ErrAlertNotFound) {
			return StockV2Alert{}, err
		}
		if err == nil && reusableAlert(existing, alert.TriggeredAt, watch.CooldownSeconds) {
			return existing, nil
		}
	}

	created, err := s.store.CreateAlert(ctx, alert)
	if err != nil {
		return StockV2Alert{}, err
	}
	watch.LastTriggeredAt = created.TriggeredAt
	if _, err := s.store.UpdateWatch(ctx, watch); err != nil {
		return StockV2Alert{}, err
	}
	return created, nil
}

func (s *Service) ListAlerts(ctx context.Context, filter AlertListFilter) ([]StockV2Alert, error) {
	return s.store.ListAlerts(ctx, filter)
}

func (s *Service) CountAlerts(ctx context.Context, filter AlertListFilter) (int, error) {
	return s.store.CountAlerts(ctx, filter)
}

func (s *Service) AcknowledgeAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusAcknowledged)
}

func (s *Service) IgnoreAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusIgnored)
}

func (s *Service) ResolveAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusResolved)
}

func (s *Service) setWatchStatus(ctx context.Context, id string, status string) (StockV2Watch, error) {
	watch, err := s.store.GetWatch(ctx, id)
	if err != nil {
		return StockV2Watch{}, err
	}
	if watch.Status == WatchStatusArchived {
		return StockV2Watch{}, ErrWatchArchived
	}
	watch.Status = status
	return s.store.UpdateWatch(ctx, watch)
}

func (s *Service) setAlertStatus(ctx context.Context, id string, status string) (StockV2Alert, error) {
	if !validAlertStatus(status) {
		return StockV2Alert{}, ErrInvalidAlertStatus
	}
	alert, err := s.store.GetAlert(ctx, id)
	if err != nil {
		return StockV2Alert{}, err
	}
	now := time.Now()
	alert.Status = status
	switch status {
	case AlertStatusAcknowledged, AlertStatusIgnored:
		alert.AcknowledgedAt = now
	case AlertStatusResolved:
		alert.ResolvedAt = now
	}
	return s.store.UpdateAlert(ctx, alert)
}

func watchFromCreateRequest(req RequestCreateWatch) StockV2Watch {
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = WatchStatusActive
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = WatchSourceManual
	}
	triggerPolicy := strings.TrimSpace(req.TriggerPolicy)
	if triggerPolicy == "" {
		triggerPolicy = WatchTriggerPolicyAny
	}
	scheduleKind := strings.TrimSpace(req.ScheduleKind)
	if scheduleKind == "" {
		scheduleKind = WatchScheduleManual
	}
	triggerConfig := req.TriggerConfig
	if triggerConfig == nil {
		triggerConfig = map[string]any{}
	}
	return StockV2Watch{
		ID:                generateID(),
		Name:              strings.TrimSpace(req.Name),
		Status:            status,
		Source:            source,
		Symbol:            strings.TrimSpace(req.Symbol),
		Market:            strings.TrimSpace(req.Market),
		PortfolioID:       strings.TrimSpace(req.PortfolioID),
		StrategyID:        strings.TrimSpace(req.StrategyID),
		StrategyVersionID: strings.TrimSpace(req.StrategyVersionID),
		TriggerPolicy:     triggerPolicy,
		TriggerConfig:     triggerConfig,
		ScheduleKind:      scheduleKind,
		CooldownSeconds:   req.CooldownSeconds,
	}
}

func applyWatchUpdate(watch *StockV2Watch, req RequestUpdateWatch) {
	if req.Name != nil {
		watch.Name = strings.TrimSpace(*req.Name)
	}
	if req.Source != nil {
		watch.Source = strings.TrimSpace(*req.Source)
	}
	if req.Symbol != nil {
		watch.Symbol = strings.TrimSpace(*req.Symbol)
	}
	if req.Market != nil {
		watch.Market = strings.TrimSpace(*req.Market)
	}
	if req.PortfolioID != nil {
		watch.PortfolioID = strings.TrimSpace(*req.PortfolioID)
	}
	if req.StrategyID != nil {
		watch.StrategyID = strings.TrimSpace(*req.StrategyID)
		if req.StrategyVersionID == nil {
			watch.StrategyVersionID = ""
		}
	}
	if req.StrategyVersionID != nil {
		watch.StrategyVersionID = strings.TrimSpace(*req.StrategyVersionID)
	}
	if req.TriggerPolicy != nil {
		watch.TriggerPolicy = strings.TrimSpace(*req.TriggerPolicy)
	}
	if req.TriggerConfig != nil {
		watch.TriggerConfig = *req.TriggerConfig
	}
	if req.ScheduleKind != nil {
		watch.ScheduleKind = strings.TrimSpace(*req.ScheduleKind)
	}
	if req.CooldownSeconds != nil {
		watch.CooldownSeconds = *req.CooldownSeconds
	}
}

func (s *Service) fillWatchStrategyVersion(ctx context.Context, watch *StockV2Watch) error {
	if watch.StrategyID == "" {
		watch.StrategyVersionID = ""
		return nil
	}
	strategy, err := s.store.GetStrategy(ctx, watch.StrategyID)
	if err != nil {
		return err
	}
	if watch.StrategyVersionID == "" {
		watch.StrategyVersionID = strategy.Strategy.ActiveVersionID
		return nil
	}
	versions, err := s.store.ListStrategyVersions(ctx, watch.StrategyID)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if version.ID == watch.StrategyVersionID {
			return nil
		}
	}
	return ErrStrategyVersionNotFound
}

func (s *Service) validateWatch(ctx context.Context, watch StockV2Watch) error {
	if watch.Name == "" {
		return ErrInvalidWatchName
	}
	if !validWatchStatus(watch.Status) {
		return ErrInvalidWatchStatus
	}
	if !validWatchSource(watch.Source) {
		return ErrInvalidWatchSource
	}
	if !validWatchPolicy(watch.TriggerPolicy) {
		return ErrInvalidWatchPolicy
	}
	if !validWatchSchedule(watch.ScheduleKind) {
		return ErrInvalidWatchSchedule
	}
	if watch.CooldownSeconds < 0 {
		return ErrInvalidWatchCooldown
	}
	if watch.Source == WatchSourceStrategy && watch.StrategyID == "" {
		return ErrInvalidWatchTarget
	}
	if watch.Source == WatchSourcePortfolioMonitor && watch.PortfolioID == "" {
		return ErrInvalidWatchTarget
	}
	if watch.Symbol == "" && watch.PortfolioID == "" && watch.StrategyID == "" {
		return ErrInvalidWatchTarget
	}
	if watch.PortfolioID != "" {
		if _, err := s.store.GetPortfolio(ctx, watch.PortfolioID); err != nil {
			return err
		}
	}
	return nil
}

func alertFromCreateRequest(req RequestCreateAlert) StockV2Alert {
	level := strings.TrimSpace(req.Level)
	if level == "" {
		level = AlertLevelInfo
	}
	triggeredAt := req.TriggeredAt
	if triggeredAt.IsZero() {
		triggeredAt = time.Now()
	}
	evidence := req.Evidence
	if evidence == nil {
		evidence = map[string]any{}
	}
	return StockV2Alert{
		ID:          generateID(),
		WatchID:     strings.TrimSpace(req.WatchID),
		Status:      AlertStatusOpen,
		Level:       level,
		Title:       strings.TrimSpace(req.Title),
		Summary:     strings.TrimSpace(req.Summary),
		DedupeKey:   strings.TrimSpace(req.DedupeKey),
		Evidence:    evidence,
		TriggeredAt: triggeredAt,
	}
}

func validateAlert(alert StockV2Alert) error {
	if alert.WatchID == "" {
		return ErrWatchNotFound
	}
	if alert.Title == "" {
		return ErrInvalidAlertTitle
	}
	if !validAlertStatus(alert.Status) {
		return ErrInvalidAlertStatus
	}
	if !validAlertLevel(alert.Level) {
		return ErrInvalidAlertLevel
	}
	return nil
}

func reusableAlert(existing StockV2Alert, triggeredAt time.Time, cooldownSeconds int) bool {
	if existing.Status == AlertStatusOpen || existing.Status == AlertStatusAcknowledged {
		return true
	}
	if cooldownSeconds <= 0 {
		return false
	}
	if triggeredAt.IsZero() {
		triggeredAt = time.Now()
	}
	cutoff := triggeredAt.Add(-time.Duration(cooldownSeconds) * time.Second)
	return existing.TriggeredAt.After(cutoff) || existing.TriggeredAt.Equal(cutoff)
}

func validWatchStatus(status string) bool {
	return status == WatchStatusActive || status == WatchStatusPaused || status == WatchStatusArchived
}

func validWatchSource(source string) bool {
	return source == WatchSourceManual || source == WatchSourceStrategy || source == WatchSourcePortfolioMonitor
}

func validWatchPolicy(policy string) bool {
	return policy == WatchTriggerPolicyAny || policy == WatchTriggerPolicyAll
}

func validWatchSchedule(kind string) bool {
	return kind == WatchScheduleManual || kind == WatchScheduleMarketSession || kind == WatchScheduleDaily
}

func validAlertStatus(status string) bool {
	return status == AlertStatusOpen || status == AlertStatusAcknowledged || status == AlertStatusIgnored || status == AlertStatusResolved
}

func validAlertLevel(level string) bool {
	return level == AlertLevelInfo || level == AlertLevelWarning || level == AlertLevelCritical
}

func (s *Service) triggerConfigFromStrategy(ctx context.Context, strategy StrategyWithVersion) (map[string]any, error) {
	if strategy.Strategy.Symbol == "" && strategy.Strategy.PortfolioID != "" {
		portfolio, err := s.store.GetPortfolio(ctx, strategy.Strategy.PortfolioID)
		if err != nil {
			return nil, err
		}
		holdings, err := s.store.ListHoldings(ctx, strategy.Strategy.PortfolioID)
		if err != nil {
			return nil, err
		}
		config := triggerConfigFromPortfolio(portfolio, holdings)
		config["source"] = WatchSourceStrategy
		config["template"] = "strategy_portfolio_monitor_watch_v1"
		config["strategyId"] = strategy.Strategy.ID
		config["strategyVersionId"] = strategy.Strategy.ActiveVersionID
		return config, nil
	}

	priceTriggers := mapFromAny(strategy.ActiveVersion.GenerationMeta["priceTriggers"])
	playbook := mapFromAny(strategy.ActiveVersion.GenerationMeta["playbook"])
	rules := make([]any, 0)
	symbol := strategy.Strategy.Symbol

	if v, ok := numberFromAny(priceTriggers["entryPriceLow"]); ok {
		if high, ok := numberFromAny(priceTriggers["entryPriceHigh"]); ok {
			rules = append(rules, map[string]any{"key": "entry_range", "type": WatchRulePriceBetween, "symbol": symbol, "low": v, "high": high})
		}
	}
	if v, ok := numberFromAny(priceTriggers["triggerPriceAbove"]); ok {
		rules = append(rules, map[string]any{"key": "trigger_price_above", "type": WatchRulePriceAbove, "symbol": symbol, "threshold": v})
	}
	if v, ok := numberFromAny(priceTriggers["triggerPriceBelow"]); ok {
		rules = append(rules, map[string]any{"key": "trigger_price_below", "type": WatchRulePriceBelow, "symbol": symbol, "threshold": v})
	}
	if v, ok := numberFromAny(priceTriggers["stopLoss"]); ok {
		rules = append(rules, map[string]any{"key": "stop_loss", "type": WatchRulePriceBelow, "symbol": symbol, "threshold": v})
	}
	if v, ok := numberFromAny(priceTriggers["takeProfit"]); ok {
		rules = append(rules, map[string]any{"key": "take_profit", "type": WatchRulePriceAbove, "symbol": symbol, "threshold": v})
	}

	template := "strategy_price_triggers_v1"
	if len(rules) == 0 {
		template = "strategy_default_watch_v1"
		rules = append(rules,
			map[string]any{"key": "quote_stale", "type": WatchRuleQuoteStale, "symbol": symbol, "maxAgeSeconds": 1800},
			map[string]any{"key": "pct_change_above_5", "type": WatchRulePctChangeAbove, "symbol": symbol, "threshold": 5},
			map[string]any{"key": "pct_change_below_minus_5", "type": WatchRulePctChangeBelow, "symbol": symbol, "threshold": -5},
		)
	}
	return map[string]any{
		"source":            WatchSourceStrategy,
		"template":          template,
		"strategyId":        strategy.Strategy.ID,
		"strategyVersionId": strategy.Strategy.ActiveVersionID,
		"playbook":          playbook,
		"rules":             rules,
	}, nil
}

func triggerConfigFromPortfolio(portfolio StockV2Portfolio, holdings []StockV2Holding) map[string]any {
	limit := portfolio.MaxSinglePositionPct
	if limit <= 0 {
		limit = 20
	}
	rules := make([]any, 0, len(holdings)*2)
	for _, holding := range holdings {
		if holding.Symbol == "" {
			continue
		}
		rules = append(rules,
			map[string]any{"key": "quote_stale_" + holding.Symbol, "type": WatchRuleQuoteStale, "symbol": holding.Symbol, "maxAgeSeconds": 1800},
			map[string]any{"key": "weight_above_" + holding.Symbol, "type": WatchRulePortfolioSymbolWeightOver, "symbol": holding.Symbol, "portfolioId": portfolio.ID, "threshold": limit},
		)
	}
	return map[string]any{
		"source":       WatchSourcePortfolioMonitor,
		"template":     "portfolio_monitor_watch_v1",
		"portfolioId":  portfolio.ID,
		"holdingCount": len(holdings),
		"rules":        rules,
	}
}

func mapFromAny(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func numberFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case jsonNumber:
		n, err := v.Float64()
		return n, err == nil
	}
	return 0, false
}
