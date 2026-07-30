package stockv2

import "time"

const (
	ExecutionGuardrailsStatusPass     = "pass"
	ExecutionGuardrailsStatusBlocked  = "blocked"
	ExecutionGuardrailsStatusDegraded = "degraded"
)

const (
	ProposedOperationActionBuildPosition  = "build_position"
	ProposedOperationActionAddPosition    = "add_position"
	ProposedOperationActionReducePosition = "reduce_position"
	ProposedOperationActionExitPosition   = "exit_position"
)

type ProposedOperation struct {
	Action             string  `json:"action"`
	PortfolioID        string  `json:"portfolioId,omitempty"`
	Symbol             string  `json:"symbol,omitempty"`
	Market             string  `json:"market,omitempty"`
	Quantity           float64 `json:"quantity,omitempty"`
	Amount             float64 `json:"amount,omitempty"`
	Price              float64 `json:"price,omitempty"`
	TargetPortfolioPct float64 `json:"targetPortfolioPct,omitempty"`
}

type ExecutionGuardrailsInput struct {
	Operation ProposedOperation   `json:"operation"`
	Portfolio StockV2Portfolio    `json:"portfolio,omitempty"`
	Snapshot  *PortfolioSnapshot  `json:"snapshot,omitempty"`
	Holdings  []StockV2Holding    `json:"holdings,omitempty"`
	Quote     *StockV2QuoteLatest `json:"quote,omitempty"`
}

type ExecutionGuardrailReason struct {
	Code     string         `json:"code"`
	Status   string         `json:"status"`
	Message  string         `json:"message"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

type ExecutionGuardrailsResult struct {
	Status    string                     `json:"status"`
	Reasons   []ExecutionGuardrailReason `json:"reasons,omitempty"`
	CheckedAt time.Time                  `json:"checkedAt"`
}

func EvaluateExecutionGuardrails(input ExecutionGuardrailsInput) ExecutionGuardrailsResult {
	result := ExecutionGuardrailsResult{Status: ExecutionGuardrailsStatusPass, CheckedAt: time.Now()}
	add := func(status, code, message string, evidence map[string]any) {
		result.Reasons = append(result.Reasons, ExecutionGuardrailReason{
			Code: code, Status: status, Message: message, Evidence: evidence,
		})
		if status == ExecutionGuardrailsStatusBlocked {
			result.Status = ExecutionGuardrailsStatusBlocked
		} else if result.Status == ExecutionGuardrailsStatusPass {
			result.Status = ExecutionGuardrailsStatusDegraded
		}
	}

	op := normalizeProposedOperation(input.Operation)
	quoteUsable := input.Quote != nil && input.Quote.LastPrice > 0
	if op.Symbol == "" {
		add(ExecutionGuardrailsStatusBlocked, "symbol_missing", "缺少标的代码，不能生成操作提案。", nil)
	}
	if !isBuyOperation(op.Action) && !isSellOperation(op.Action) {
		add(ExecutionGuardrailsStatusBlocked, "operation_unsupported", "操作类型不在确定性约束检查范围内。", map[string]any{"action": op.Action})
	}
	if input.Portfolio.ID == "" {
		add(ExecutionGuardrailsStatusBlocked, "portfolio_missing", "缺少绑定组合，不能生成账户绑定操作提案。", nil)
	}
	if input.Snapshot == nil {
		add(ExecutionGuardrailsStatusDegraded, "portfolio_snapshot_missing", "缺少组合快照，操作提案只能降级审阅。", nil)
	} else if input.Snapshot.Status != PortfolioValuationStatusFresh || input.Snapshot.StaleQuoteCount > 0 {
		add(ExecutionGuardrailsStatusDegraded, "portfolio_snapshot_stale", "组合快照不是 fresh 状态。", map[string]any{
			"status":          input.Snapshot.Status,
			"staleQuoteCount": input.Snapshot.StaleQuoteCount,
			"valuationAt":     input.Snapshot.ValuationAt,
		})
	}
	if !quoteUsable {
		add(ExecutionGuardrailsStatusDegraded, "quote_missing", "缺少可用最新价。", nil)
	} else if input.Quote.Status != QuoteStatusFresh {
		add(ExecutionGuardrailsStatusDegraded, "quote_stale", "最新行情不是 fresh 状态。", map[string]any{
			"status":    input.Quote.Status,
			"fetchedAt": input.Quote.FetchedAt,
			"quoteAt":   input.Quote.QuoteAt,
		})
	}
	if preciseOperation(op) && !quoteUsable {
		add(ExecutionGuardrailsStatusBlocked, "quote_required_for_precise_operation", "没有可用最新价时不能生成精确股数或金额型操作。", nil)
	}

	holding, hasHolding := findHoldingForOperation(input.Holdings, op.Symbol)
	switch {
	case isBuyOperation(op.Action):
		checkBuyGuardrails(input, op, holding, hasHolding, add)
	case isSellOperation(op.Action):
		checkSellGuardrails(input, op, holding, hasHolding, quoteUsable, add)
	}
	return result
}

func checkBuyGuardrails(input ExecutionGuardrailsInput, op ProposedOperation, holding StockV2Holding, hasHolding bool, add func(string, string, string, map[string]any)) {
	value := operationNotional(op, input.Quote)
	if value > 0 && input.Portfolio.Cash < value {
		add(ExecutionGuardrailsStatusBlocked, "cash_insufficient", "现金不足，不能买入或加仓。", map[string]any{
			"cash": input.Portfolio.Cash, "required": value,
		})
	}
	limit := input.Portfolio.MaxSinglePositionPct
	if limit <= 0 {
		limit = 20
	}
	total := portfolioTotalValue(input)
	if value <= 0 || total <= 0 || limit <= 0 {
		return
	}
	currentValue := 0.0
	if hasHolding {
		currentValue = holding.MarketValue
		if currentValue <= 0 && input.Quote != nil {
			currentValue = holding.Quantity * input.Quote.LastPrice
		}
	}
	projectedPct := (currentValue + value) / total * 100
	if projectedPct > limit {
		add(ExecutionGuardrailsStatusBlocked, "position_limit_exceeded", "单票仓位将超过组合上限。", map[string]any{
			"projectedPct": projectedPct, "limit": limit,
		})
	}
}

func checkSellGuardrails(input ExecutionGuardrailsInput, op ProposedOperation, holding StockV2Holding, hasHolding bool, quoteUsable bool, add func(string, string, string, map[string]any)) {
	if !hasHolding || holding.Quantity <= 0 {
		add(ExecutionGuardrailsStatusBlocked, "holding_empty", "没有该标的持仓，不能减仓或清仓。", map[string]any{"symbol": op.Symbol})
		return
	}
	qty := operationQuantity(op, input.Quote)
	if qty <= 0 && op.Action == ProposedOperationActionExitPosition {
		return
	}
	if qty <= 0 || !quoteUsable {
		return
	}
	available := holding.AvailableQuantity
	if available <= 0 {
		available = holding.Quantity
	}
	if qty > available {
		add(ExecutionGuardrailsStatusBlocked, "sell_quantity_insufficient", "可卖数量不足，不能减仓或清仓。", map[string]any{
			"available": available, "required": qty,
		})
	}
}

func normalizeProposedOperation(op ProposedOperation) ProposedOperation {
	op.Action = normalizeProposedOperationAction(op.Action)
	if op.Market == "" {
		op.Market = inferAStockMarket(op.Symbol)
	}
	return op
}

func normalizeProposedOperationAction(action string) string {
	switch action {
	case "buy", "open", "build":
		return ProposedOperationActionBuildPosition
	case "add":
		return ProposedOperationActionAddPosition
	case "sell", "reduce":
		return ProposedOperationActionReducePosition
	case "clear", "exit", "close":
		return ProposedOperationActionExitPosition
	default:
		return action
	}
}

func isBuyOperation(action string) bool {
	return action == ProposedOperationActionBuildPosition || action == ProposedOperationActionAddPosition
}

func isSellOperation(action string) bool {
	return action == ProposedOperationActionReducePosition || action == ProposedOperationActionExitPosition
}

func preciseOperation(op ProposedOperation) bool {
	return op.Quantity > 0 || op.Amount > 0
}

func operationNotional(op ProposedOperation, quote *StockV2QuoteLatest) float64 {
	if op.Amount > 0 {
		return op.Amount
	}
	price := operationPrice(op, quote)
	if op.Quantity > 0 && price > 0 {
		return op.Quantity * price
	}
	return 0
}

func operationQuantity(op ProposedOperation, quote *StockV2QuoteLatest) float64 {
	if op.Quantity > 0 {
		return op.Quantity
	}
	price := operationPrice(op, quote)
	if op.Amount > 0 && price > 0 {
		return op.Amount / price
	}
	return 0
}

func operationPrice(op ProposedOperation, quote *StockV2QuoteLatest) float64 {
	if op.Price > 0 {
		return op.Price
	}
	if quote != nil {
		return quote.LastPrice
	}
	return 0
}

func portfolioTotalValue(input ExecutionGuardrailsInput) float64 {
	if input.Snapshot != nil && input.Snapshot.TotalAssetValue > 0 {
		return input.Snapshot.TotalAssetValue
	}
	total := input.Portfolio.Cash
	for _, holding := range input.Holdings {
		total += holding.MarketValue
	}
	return total
}

func findHoldingForOperation(holdings []StockV2Holding, symbol string) (StockV2Holding, bool) {
	for _, holding := range holdings {
		if holding.Symbol == symbol {
			return holding, true
		}
	}
	return StockV2Holding{}, false
}
