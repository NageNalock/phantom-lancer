package stockv2

import (
	"context"
	"sort"
	"time"
)

// BuildAssetCurve 基于「初始建仓 + 交易流水 + 历史日 K」回算组合每个交易日的总资产,生成资产曲线与买卖标记。
//
// 事件源:
//   - stockv2_transactions 中的 buy/sell 交易(影响现金 + 持仓)
//   - stockv2_holdings 中的手动导入持仓(acquire 事件,只加持仓、不动现金)
//
// 核心思路(自洽):portfolio.Cash 是「现在」的现金终态。把所有交易按时间正向重放,
// 得到理论现金终值 replayedCash;二者之差 cashOffset = Cash - replayedCash 即历史注资/手工调整
// 的累计,作为曲线起点现金。再正向重放一次,即可在每个交易日得到 cash(t)、持仓 qty(t),
// 配合日 K 收盘价算出 holdingValue(t),total(t) = cash(t) + holdingValue(t)。曲线终点现金 == portfolio.Cash。
//
// 价格缺失(symbol 无日 K 或某交易日早于其首个 bar)用最新价/成本价前向填充,并标记 Estimated=true。
func (s *Service) BuildAssetCurve(ctx context.Context, portfolioID string, opts AssetCurveOptions) (AssetCurveResponse, error) {
	resp := AssetCurveResponse{PortfolioID: portfolioID, Points: []AssetCurvePoint{}, Markers: []AssetCurveMarker{}}

	portfolio, err := s.store.GetPortfolio(ctx, portfolioID)
	if err != nil {
		return resp, err
	}

	// 1. 收集所有事件:交易 + 手动建仓(acquire)
	txs, err := s.store.ListTransactions(ctx, portfolioID, 0) // 升序
	if err != nil {
		return resp, wrapError(err, "list transactions for asset curve")
	}
	holdings, _ := s.store.ListHoldings(ctx, portfolioID)
	holdingBySymbol := map[string]StockV2Holding{}
	for _, h := range holdings {
		holdingBySymbol[h.Symbol] = h
	}

	// 算出每个 symbol 的交易净量,用于推导「手动导入底仓」的数量
	txNetQty := map[string]float64{}
	for _, tx := range txs {
		if tx.Side == "buy" {
			txNetQty[tx.Symbol] += tx.Quantity
		} else if tx.Side == "sell" {
			txNetQty[tx.Symbol] -= tx.Quantity
		}
	}

	// 所有事件(交易 + 建仓),统一用 StockV2Transaction 表达,side 可以是 buy/sell/acquire
	type curveEvent = StockV2Transaction
	events := make([]curveEvent, 0, len(txs)+len(holdings))
	events = append(events, txs...)

	for _, h := range holdings {
		if h.Quantity <= 1e-9 {
			continue
		}
		acquireQty := h.Quantity - txNetQty[h.Symbol]
		if acquireQty <= 1e-9 {
			// 当前持仓完全可以被交易解释,无需额外建仓事件
			continue
		}
		// 生成一笔「建仓导入」事件:只加持仓、不动现金
		events = append(events, curveEvent{
			ID:         "acquire-" + h.ID,
			Symbol:     h.Symbol,
			Market:     h.Market,
			Name:       h.Name,
			Side:       "acquire",
			Quantity:   acquireQty,
			Price:      h.CostPrice,
			Amount:     0, // 建仓不动现金
			ExecutedAt: h.AcquiredAt,
		})
	}

	if len(events) == 0 {
		return resp, nil
	}

	// 按时间升序排序
	sort.Slice(events, func(i, j int) bool {
		if !events[i].ExecutedAt.Equal(events[j].ExecutedAt) {
			return events[i].ExecutedAt.Before(events[j].ExecutedAt)
		}
		// 同时间:acquire 优先于 buy/sell(底仓先建好再交易)
		if events[i].Side == "acquire" && events[j].Side != "acquire" {
			return true
		}
		if events[j].Side == "acquire" && events[i].Side != "acquire" {
			return false
		}
		return events[i].ID < events[j].ID
	})

	// 日期范围
	firstDay := events[0].ExecutedAt.Format("2006-01-02")
	endDay := time.Now().Format("2006-01-02")
	startDay := firstDay
	if opts.Days > 0 {
		if want := time.Now().AddDate(0, 0, -opts.Days).Format("2006-01-02"); want > firstDay {
			startDay = want
		}
	}
	if startDay > endDay {
		startDay = endDay
	}

	// 涉及的 symbol 集合(含建仓的)
	symbolSet := map[string]struct{}{}
	for _, ev := range events {
		symbolSet[ev.Symbol] = struct{}{}
	}

	// 拉日 K(允许失败:某 symbol 取不到则全程 fallback)
	symbolBars := map[string][]StockV2DailyBar{}
	for sym := range symbolSet {
		bars, barErr := s.store.marketDB.GetDailyBars(ctx, sym, DailyBarAdjustedNone, "", endDay, 0)
		if barErr != nil || len(bars) == 0 {
			symbolBars[sym] = nil
			continue
		}
		symbolBars[sym] = bars
	}

	// 交易日集合 = 所有 symbol 日 K 的 trade_date 并集 ∪ {endDay}
	tradeDaySet := map[string]struct{}{endDay: {}}
	for _, bars := range symbolBars {
		for _, b := range bars {
			if b.TradeDate >= startDay && b.TradeDate <= endDay {
				tradeDaySet[b.TradeDate] = struct{}{}
			}
		}
	}
	tradeDays := make([]string, 0, len(tradeDaySet))
	for d := range tradeDaySet {
		tradeDays = append(tradeDays, d)
	}
	sort.Strings(tradeDays)

	// fallback 价 + Estimated 判定
	estimated := false
	symbolFallback := map[string]float64{}
	for sym := range symbolSet {
		fp := 0.0
		if h, ok := holdingBySymbol[sym]; ok {
			fp = h.LastPrice
			if fp <= 0 {
				fp = h.CostPrice
			}
		}
		symbolFallback[sym] = fp
		bars := symbolBars[sym]
		if len(bars) == 0 || bars[0].TradeDate > startDay {
			estimated = true
		}
	}

	// 前向填充价格表 priceTable[sym][date]
	priceTable := map[string]map[string]float64{}
	for sym, bars := range symbolBars {
		lastClose := symbolFallback[sym]
		pm := make(map[string]float64, len(tradeDays))
		bi := 0
		for _, d := range tradeDays {
			for bi < len(bars) && bars[bi].TradeDate < d {
				bi++
			}
			if bi < len(bars) && bars[bi].TradeDate == d {
				lastClose = bars[bi].Close
				bi++
			}
			pm[d] = lastClose
		}
		priceTable[sym] = pm
	}

	// 重放事件:应用一个事件到 cash/qty
	//   buy:    cash -= amount, qty += quantity
	//   sell:   cash += amount, qty -= quantity
	//   acquire:cash 不变, qty += quantity (手动建仓导入)
	apply := func(ev StockV2Transaction, cash *float64, qty map[string]float64) {
		switch ev.Side {
		case "sell":
			*cash += ev.Amount
			qty[ev.Symbol] -= ev.Quantity
		case "acquire":
			qty[ev.Symbol] += ev.Quantity
		default: // buy
			*cash -= ev.Amount
			qty[ev.Symbol] += ev.Quantity
		}
	}

	// 扫描 1:算理论现金终值 replayedCash(只有 buy/sell 影响现金)
	replayedCash := 0.0
	for _, ev := range events {
		apply(ev, &replayedCash, map[string]float64{})
	}
	cashOffset := portfolio.Cash - replayedCash

	// 扫描 2:以 cashOffset 为起点,产出每个交易日的 point
	cash := cashOffset
	qty := map[string]float64{}
	evIdx := 0
	for evIdx < len(events) && events[evIdx].ExecutedAt.Format("2006-01-02") < startDay {
		apply(events[evIdx], &cash, qty)
		evIdx++
	}
	points := make([]AssetCurvePoint, 0, len(tradeDays))
	pointTotal := make(map[string]float64, len(tradeDays))
	for _, d := range tradeDays {
		for evIdx < len(events) && events[evIdx].ExecutedAt.Format("2006-01-02") <= d {
			apply(events[evIdx], &cash, qty)
			evIdx++
		}
		holdingValue := 0.0
		for sym, q := range qty {
			if q <= 1e-9 {
				continue
			}
			if pm := priceTable[sym]; pm != nil {
				holdingValue += q * pm[d]
			}
		}
		total := cash + holdingValue
		points = append(points, AssetCurvePoint{Date: d, Cash: cash, HoldingValue: holdingValue, Total: total})
		pointTotal[d] = total
	}

	// markers:每笔交易钉到「≤ 成交日的最近交易日」
	markers := make([]AssetCurveMarker, 0, len(txs))
	for _, tx := range txs {
		markerDate := latestTradeDayOnOrBefore(tx.ExecutedAt.Format("2006-01-02"), tradeDays)
		markers = append(markers, AssetCurveMarker{
			Date:     markerDate,
			Side:     tx.Side,
			Symbol:   tx.Symbol,
			Name:     tx.Name,
			Quantity: tx.Quantity,
			Price:    tx.Price,
			Amount:   tx.Amount,
			Total:    pointTotal[markerDate],
		})
	}

	resp.Points = points
	resp.Markers = markers
	resp.Start = startDay
	resp.End = endDay
	resp.Estimated = estimated
	return resp, nil
}

// latestTradeDayOnOrBefore 在升序 tradeDays 中找 ≤ d 的最大交易日;d 早于全部交易日时返回最早一个。
func latestTradeDayOnOrBefore(d string, tradeDays []string) string {
	if len(tradeDays) == 0 {
		return d
	}
	if d <= tradeDays[0] {
		return tradeDays[0]
	}
	if d >= tradeDays[len(tradeDays)-1] {
		return tradeDays[len(tradeDays)-1]
	}
	lo, hi := 0, len(tradeDays)
	for lo < hi {
		mid := (lo + hi) / 2
		if tradeDays[mid] <= d {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return tradeDays[lo-1]
}
