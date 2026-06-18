package stockv2

import (
	"context"
	"strings"
	"time"
)

func (s *Service) CreateStrategy(ctx context.Context, req RequestCreateStrategy) (StrategyWithVersion, error) {
	strategy, version, err := s.strategyFromCreateRequest(ctx, req)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	return s.store.CreateStrategyWithVersion(ctx, strategy, version)
}

func (s *Service) ListStrategies(ctx context.Context, filter StrategyListFilter) ([]StrategyWithVersion, error) {
	return s.store.ListStrategies(ctx, filter)
}

func (s *Service) CountStrategies(ctx context.Context, filter StrategyListFilter) (int, error) {
	return s.store.CountStrategies(ctx, filter)
}

func (s *Service) GetStrategy(ctx context.Context, id string) (StrategyWithVersion, error) {
	return s.store.GetStrategy(ctx, id)
}

func (s *Service) UpdateStrategy(ctx context.Context, id string, req RequestUpdateStrategy) (StrategyWithVersion, error) {
	current, err := s.store.GetStrategy(ctx, id)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	if current.Strategy.Status == StrategyStatusArchived {
		return StrategyWithVersion{}, ErrStrategyArchived
	}

	strategy := current.Strategy
	if req.Name != nil {
		strategy.Name = strings.TrimSpace(*req.Name)
	}
	if req.Scope != nil {
		strategy.Scope = strings.TrimSpace(*req.Scope)
	}
	if req.Symbol != nil {
		strategy.Symbol = strings.TrimSpace(*req.Symbol)
	}
	if req.Market != nil {
		strategy.Market = strings.TrimSpace(*req.Market)
	}
	if req.PortfolioID != nil {
		strategy.PortfolioID = strings.TrimSpace(*req.PortfolioID)
	}
	if err := s.validateStrategy(ctx, strategy); err != nil {
		return StrategyWithVersion{}, err
	}

	var nextVersion *StockV2StrategyVersion
	if strategyVersionContentChanged(req) {
		version := StockV2StrategyVersion{}
		if current.ActiveVersion != nil {
			version = *current.ActiveVersion
		}
		applyStrategyVersionUpdate(&version, req)
		if version.CreatedBy == "" {
			version.CreatedBy = strategy.Source
		}
		if err := validateStrategyDirection(version.Direction); err != nil {
			return StrategyWithVersion{}, err
		}
		nextVersion = &version
	}

	return s.store.UpdateStrategyWithVersion(ctx, strategy, nextVersion)
}

func (s *Service) ActivateStrategy(ctx context.Context, id string) (StrategyWithVersion, error) {
	return s.setStrategyStatus(ctx, id, StrategyStatusActive)
}

func (s *Service) PauseStrategy(ctx context.Context, id string) (StrategyWithVersion, error) {
	return s.setStrategyStatus(ctx, id, StrategyStatusPaused)
}

func (s *Service) ArchiveStrategy(ctx context.Context, id string) (StrategyWithVersion, error) {
	current, err := s.store.GetStrategy(ctx, id)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	strategy := current.Strategy
	if strategy.Status == StrategyStatusArchived {
		return current, nil
	}
	strategy.Status = StrategyStatusArchived
	strategy.ArchivedAt = time.Now()
	return s.store.UpdateStrategyWithVersion(ctx, strategy, nil)
}

func (s *Service) ListStrategyVersions(ctx context.Context, strategyID string) ([]StockV2StrategyVersion, error) {
	if _, err := s.store.GetStrategy(ctx, strategyID); err != nil {
		return nil, err
	}
	return s.store.ListStrategyVersions(ctx, strategyID)
}

func (s *Service) CreatePortfolioMonitorStrategy(ctx context.Context, portfolioID string, req RequestCreatePortfolioMonitorStrategy) (StrategyWithVersion, error) {
	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	holdings, err := s.store.ListHoldings(ctx, portfolioID)
	if err != nil {
		return StrategyWithVersion{}, wrapError(err, "list holdings for portfolio monitor")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "组合监控策略 - " + portfolio.Name
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "组合日常监控"
	}
	thesis := strings.TrimSpace(req.Thesis)
	if thesis == "" {
		if len(holdings) == 0 {
			thesis = "当前组合暂无持仓，策略用于后续持仓变化、现金比例和行情新鲜度的日常监控。"
		} else {
			thesis = "系统模板生成的组合监控策略，覆盖持仓仓位、价格波动、行情新鲜度和组合风险状态。"
		}
	}
	createdBy := strings.TrimSpace(req.CreatedBy)
	if createdBy == "" {
		createdBy = StrategySourceSystemTemplate
	}

	return s.CreateStrategy(ctx, RequestCreateStrategy{
		Name:            name,
		Kind:            StrategyKindPortfolioMonitor,
		Scope:           StrategyScopePortfolioBound,
		Source:          StrategySourceSystemTemplate,
		Status:          StrategyStatusDraft,
		PortfolioID:     portfolioID,
		Title:           title,
		Direction:       StrategyDirectionWatch,
		Thesis:          thesis,
		EntryConditions: []string{"daily_review", "price_drop_pct", "price_rise_pct", "stale_quote", "position_pct_limit"},
		ExitConditions:  []string{},
		RiskNotes:       strings.TrimSpace(req.RiskNotes),
		GenerationMeta: map[string]any{
			"source":       StrategySourceSystemTemplate,
			"template":     "portfolio_monitor_v1",
			"portfolioId":  portfolioID,
			"holdingCount": len(holdings),
		},
		CreatedBy: createdBy,
	})
}

func (s *Service) setStrategyStatus(ctx context.Context, id string, status string) (StrategyWithVersion, error) {
	current, err := s.store.GetStrategy(ctx, id)
	if err != nil {
		return StrategyWithVersion{}, err
	}
	strategy := current.Strategy
	if strategy.Status == StrategyStatusArchived {
		return StrategyWithVersion{}, ErrStrategyArchived
	}
	strategy.Status = status
	return s.store.UpdateStrategyWithVersion(ctx, strategy, nil)
}

func (s *Service) strategyFromCreateRequest(ctx context.Context, req RequestCreateStrategy) (StockV2Strategy, StockV2StrategyVersion, error) {
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = StrategyStatusDraft
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = StrategySourceManual
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = StrategyScopeResearch
	}
	strategy := StockV2Strategy{
		ID:          generateID(),
		Name:        strings.TrimSpace(req.Name),
		Kind:        strings.TrimSpace(req.Kind),
		Scope:       scope,
		Source:      source,
		Status:      status,
		Symbol:      strings.TrimSpace(req.Symbol),
		Market:      strings.TrimSpace(req.Market),
		PortfolioID: strings.TrimSpace(req.PortfolioID),
	}
	if err := s.validateStrategy(ctx, strategy); err != nil {
		return StockV2Strategy{}, StockV2StrategyVersion{}, err
	}

	createdBy := strings.TrimSpace(req.CreatedBy)
	if createdBy == "" {
		createdBy = source
	}
	version := StockV2StrategyVersion{
		Title:           strings.TrimSpace(req.Title),
		Direction:       strings.TrimSpace(req.Direction),
		Thesis:          strings.TrimSpace(req.Thesis),
		EntryConditions: req.EntryConditions,
		ExitConditions:  req.ExitConditions,
		RiskNotes:       strings.TrimSpace(req.RiskNotes),
		EvidenceRefs:    req.EvidenceRefs,
		GenerationMeta:  req.GenerationMeta,
		CreatedBy:       createdBy,
	}
	if err := validateStrategyDirection(version.Direction); err != nil {
		return StockV2Strategy{}, StockV2StrategyVersion{}, err
	}
	return strategy, version, nil
}

func (s *Service) validateStrategy(ctx context.Context, strategy StockV2Strategy) error {
	if strategy.Name == "" {
		return ErrInvalidStrategyName
	}
	if !validStrategyKind(strategy.Kind) {
		return ErrInvalidStrategyKind
	}
	if !validStrategyScope(strategy.Scope) {
		return ErrInvalidStrategyScope
	}
	if !validStrategySource(strategy.Source) {
		return ErrInvalidStrategySource
	}
	if !validStrategyStatus(strategy.Status) {
		return ErrInvalidStrategyStatus
	}
	if strategy.Kind == StrategyKindSymbolStrategy && strategy.Symbol == "" {
		return ErrInvalidStrategySymbol
	}
	if strategy.Kind == StrategyKindPortfolioMonitor {
		if strategy.PortfolioID == "" {
			return ErrPortfolioNotFound
		}
		if strategy.Scope != StrategyScopePortfolioBound {
			return ErrInvalidStrategyScope
		}
	}
	if strategy.Scope == StrategyScopePortfolioBound && strategy.PortfolioID == "" {
		return ErrPortfolioNotFound
	}
	if strategy.PortfolioID != "" {
		if _, err := s.store.GetPortfolio(ctx, strategy.PortfolioID); err != nil {
			return err
		}
	}
	return nil
}

func strategyVersionContentChanged(req RequestUpdateStrategy) bool {
	return req.Title != nil ||
		req.Direction != nil ||
		req.Thesis != nil ||
		req.EntryConditions != nil ||
		req.ExitConditions != nil ||
		req.RiskNotes != nil ||
		req.EvidenceRefs != nil ||
		req.GenerationMeta != nil ||
		req.CreatedBy != nil
}

func applyStrategyVersionUpdate(version *StockV2StrategyVersion, req RequestUpdateStrategy) {
	if req.Title != nil {
		version.Title = strings.TrimSpace(*req.Title)
	}
	if req.Direction != nil {
		version.Direction = strings.TrimSpace(*req.Direction)
	}
	if req.Thesis != nil {
		version.Thesis = strings.TrimSpace(*req.Thesis)
	}
	if req.EntryConditions != nil {
		version.EntryConditions = *req.EntryConditions
	}
	if req.ExitConditions != nil {
		version.ExitConditions = *req.ExitConditions
	}
	if req.RiskNotes != nil {
		version.RiskNotes = strings.TrimSpace(*req.RiskNotes)
	}
	if req.EvidenceRefs != nil {
		version.EvidenceRefs = *req.EvidenceRefs
	}
	if req.GenerationMeta != nil {
		version.GenerationMeta = *req.GenerationMeta
	}
	if req.CreatedBy != nil {
		version.CreatedBy = strings.TrimSpace(*req.CreatedBy)
	}
}

func validStrategyKind(kind string) bool {
	return kind == StrategyKindSymbolStrategy || kind == StrategyKindPortfolioMonitor
}

func validStrategyScope(scope string) bool {
	return scope == StrategyScopeResearch || scope == StrategyScopePortfolioBound
}

func validStrategySource(source string) bool {
	return source == StrategySourceManual || source == StrategySourceSystemTemplate || source == StrategySourceAgent
}

func validStrategyStatus(status string) bool {
	return status == StrategyStatusDraft || status == StrategyStatusActive || status == StrategyStatusPaused || status == StrategyStatusArchived
}

func validateStrategyDirection(direction string) error {
	if direction == "" ||
		direction == StrategyDirectionWatch ||
		direction == StrategyDirectionBuySignal ||
		direction == StrategyDirectionSellSignal ||
		direction == StrategyDirectionHold {
		return nil
	}
	return ErrInvalidStrategyDirection
}
