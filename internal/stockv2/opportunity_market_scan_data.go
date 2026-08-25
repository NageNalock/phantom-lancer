package stockv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

type opportunityMarketScanRawMetric struct {
	Instrument      StockV2Instrument
	TradeDate       string
	RowCount        int
	Close           float64
	Close5          float64
	Close20         float64
	Close60         float64
	MA20            float64
	MA60            float64
	Volume5         float64
	Volume20        float64
	UpVolumeShare20 float64
	Volatility20    float64
	MedianAmount20  float64
	MaxClose120     float64
	IndustryBreadth float64
	PrefilterScore  float64
}

func (s *MarketDataStore) LoadOpportunityMarketScanCoverage(ctx context.Context) ([]opportunityMarketScanRawMetric, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.symbol, i.market, i.instrument_type, COALESCE(i.name,''),
			COALESCE(i.industry,''), COALESCE(q.latest_date,''), COALESCE(q.row_count,0)
		FROM stockv2_instruments i
		LEFT JOIN stockv2_daily_bar_quality q ON q.symbol=i.symbol AND q.adjusted='none'
		WHERE i.instrument_type='stock'
	`)
	if err != nil {
		return nil, wrapError(err, "load opportunity market scan coverage")
	}
	defer rows.Close()
	var out []opportunityMarketScanRawMetric
	for rows.Next() {
		var item opportunityMarketScanRawMetric
		if err := rows.Scan(&item.Instrument.ID, &item.Instrument.Symbol, &item.Instrument.Market,
			&item.Instrument.InstrumentType, &item.Instrument.Name, &item.Instrument.Industry,
			&item.TradeDate, &item.RowCount); err != nil {
			return nil, wrapError(err, "scan opportunity market coverage")
		}
		out = append(out, item)
	}
	return out, wrapError(rows.Err(), "iterate opportunity market coverage")
}

// LoadOpportunityMarketScanMetrics performs one bounded DuckDB pass instead of
// issuing thousands of per-symbol reads. Duplicate source rows are resolved by
// the same quality ordering used by GetDailyBars. Tencent daily bars expose
// volume in hands but no amount, so close*volume*100 is the bounded liquidity
// proxy when a source does not provide actual turnover amount.
func (s *MarketDataStore) LoadOpportunityMarketScanMetrics(ctx context.Context, completedThrough string) ([]opportunityMarketScanRawMetric, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("market data store is not initialized")
	}
	if completedThrough == "" {
		completedThrough = "9999-12-31"
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH selected AS (
			SELECT * EXCLUDE(source_rank) FROM (
				SELECT b.*, ROW_NUMBER() OVER (
					PARTITION BY b.symbol, b.trade_date
					ORDER BY CASE WHEN b.close > 0 AND b.volume > 0 THEN 0 ELSE 1 END,
						CASE WHEN b.amount > 0 THEN 0 ELSE 1 END,
						b.fetched_at DESC NULLS LAST, b.updated_at DESC, b.source ASC
				) source_rank
				FROM stockv2_daily_bars b
				WHERE b.adjusted='none' AND b.trade_date <= CAST(? AS DATE)
			) WHERE source_rank=1
		), ranked AS (
			SELECT s.*, ROW_NUMBER() OVER (PARTITION BY s.symbol ORDER BY s.trade_date DESC) rn
			FROM selected s WHERE s.close > 0
		), aggregated AS (
			SELECT symbol,
				MAX(CASE WHEN rn=1 THEN strftime(trade_date, '%Y-%m-%d') END) trade_date,
				COUNT(*) FILTER (WHERE rn<=120) row_count,
				MAX(CASE WHEN rn=1 THEN close END) close_now,
				MAX(CASE WHEN rn=6 THEN close END) close_5,
				MAX(CASE WHEN rn=21 THEN close END) close_20,
				MAX(CASE WHEN rn=61 THEN close END) close_60,
				AVG(close) FILTER (WHERE rn<=20) ma_20,
				AVG(close) FILTER (WHERE rn<=60) ma_60,
				AVG(volume) FILTER (WHERE rn<=5) volume_5,
				AVG(volume) FILTER (WHERE rn<=20) volume_20,
				SUM(CASE WHEN rn<=20 AND pct_change>0 THEN volume ELSE 0 END) /
					NULLIF(SUM(CASE WHEN rn<=20 THEN volume ELSE 0 END),0) up_volume_share_20,
				STDDEV_SAMP(pct_change) FILTER (WHERE rn<=20) volatility_20,
				MEDIAN(CASE WHEN amount>0 THEN amount
					WHEN close>0 AND volume>0 THEN close*volume*100
					ELSE NULL END) FILTER (WHERE rn<=20) median_amount_20,
				MAX(close) FILTER (WHERE rn<=120) max_close_120
			FROM ranked WHERE rn<=120 GROUP BY symbol
		)
		SELECT i.id, i.symbol, i.market, i.instrument_type, COALESCE(i.name,''),
			COALESCE(i.industry,''), COALESCE(i.sector,''), COALESCE(i.concepts,'[]'),
			i.last_update_at, i.created_at, i.updated_at,
			COALESCE(a.trade_date,''), COALESCE(a.row_count,0), COALESCE(a.close_now,0),
			COALESCE(a.close_5,0), COALESCE(a.close_20,0), COALESCE(a.close_60,0),
			COALESCE(a.ma_20,0), COALESCE(a.ma_60,0), COALESCE(a.volume_5,0),
			COALESCE(a.volume_20,0), COALESCE(a.up_volume_share_20,0),
			COALESCE(a.volatility_20,0), COALESCE(a.median_amount_20,0),
			COALESCE(a.max_close_120,0)
		FROM stockv2_instruments i LEFT JOIN aggregated a ON a.symbol=i.symbol
		WHERE i.instrument_type='stock'
	`, completedThrough)
	if err != nil {
		return nil, wrapError(err, "load opportunity market scan metrics")
	}
	defer rows.Close()
	var out []opportunityMarketScanRawMetric
	for rows.Next() {
		var item opportunityMarketScanRawMetric
		var conceptsJSON string
		var lastUpdate sql.NullTime
		if err := rows.Scan(&item.Instrument.ID, &item.Instrument.Symbol, &item.Instrument.Market,
			&item.Instrument.InstrumentType, &item.Instrument.Name, &item.Instrument.Industry,
			&item.Instrument.Sector, &conceptsJSON, &lastUpdate, &item.Instrument.CreatedAt,
			&item.Instrument.UpdatedAt, &item.TradeDate, &item.RowCount, &item.Close,
			&item.Close5, &item.Close20, &item.Close60, &item.MA20, &item.MA60,
			&item.Volume5, &item.Volume20, &item.UpVolumeShare20, &item.Volatility20,
			&item.MedianAmount20, &item.MaxClose120); err != nil {
			return nil, wrapError(err, "scan opportunity market metric")
		}
		if lastUpdate.Valid {
			item.Instrument.LastUpdate = lastUpdate.Time
		}
		_ = json.Unmarshal([]byte(conceptsJSON), &item.Instrument.Concepts)
		out = append(out, item)
	}
	return out, wrapError(rows.Err(), "iterate opportunity market metrics")
}

func isOpportunityMainBoardInstrument(item StockV2Instrument) bool {
	symbol := strings.TrimSpace(item.Symbol)
	market := strings.ToUpper(strings.TrimSpace(item.Market))
	name := strings.ToUpper(strings.TrimSpace(item.Name))
	if len(symbol) != 6 || item.InstrumentType != InstrumentTypeStock ||
		strings.Contains(name, "ST") || strings.Contains(name, "退") ||
		strings.HasPrefix(name, "N") || strings.HasPrefix(name, "C") {
		return false
	}
	if market == "SH" {
		return strings.HasPrefix(symbol, "600") || strings.HasPrefix(symbol, "601") ||
			strings.HasPrefix(symbol, "603") || strings.HasPrefix(symbol, "605")
	}
	if market == "SZ" {
		return strings.HasPrefix(symbol, "000") || strings.HasPrefix(symbol, "001") ||
			strings.HasPrefix(symbol, "002") || strings.HasPrefix(symbol, "003")
	}
	return false
}

func scoreOpportunityMarketScanMetrics(items []opportunityMarketScanRawMetric) []opportunityMarketScanRawMetric {
	eligible := make([]opportunityMarketScanRawMetric, 0, len(items))
	for _, item := range items {
		if !isOpportunityMainBoardInstrument(item.Instrument) || item.RowCount < 60 ||
			item.Close <= 0 || item.Close20 <= 0 || item.MedianAmount20 <= 0 {
			continue
		}
		eligible = append(eligible, item)
	}
	if len(eligible) == 0 {
		return nil
	}
	industryUp, industryTotal := map[string]int{}, map[string]int{}
	for _, item := range eligible {
		industry := strings.TrimSpace(item.Instrument.Industry)
		if industry == "" {
			continue
		}
		industryTotal[industry]++
		if item.Close > item.MA20 {
			industryUp[industry]++
		}
	}
	for i := range eligible {
		item := &eligible[i]
		if total := industryTotal[item.Instrument.Industry]; total > 0 {
			item.IndustryBreadth = float64(industryUp[item.Instrument.Industry]) / float64(total)
		}
	}
	momentumPct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return pctReturn(v.Close, v.Close20) })
	trendPct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return pctReturn(v.Close, v.MA20) })
	volumePct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return safeRatio(v.Volume5, v.Volume20) })
	upVolumePct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return v.UpVolumeShare20 })
	liquidityPct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return math.Log10(math.Max(v.MedianAmount20, 1)) })
	drawdownPct := opportunityMarketPercentiles(eligible, func(v opportunityMarketScanRawMetric) float64 { return pctReturn(v.Close, v.MaxClose120) })
	for i := range eligible {
		item := &eligible[i]
		symbol := item.Instrument.Symbol
		item.PrefilterScore = 0.25*momentumPct[symbol] +
			0.18*trendPct[symbol] +
			0.16*volumePct[symbol] +
			0.13*upVolumePct[symbol] +
			0.12*liquidityPct[symbol] +
			0.10*item.IndustryBreadth*100 +
			0.06*drawdownPct[symbol]
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].PrefilterScore == eligible[j].PrefilterScore {
			return eligible[i].Instrument.Symbol < eligible[j].Instrument.Symbol
		}
		return eligible[i].PrefilterScore > eligible[j].PrefilterScore
	})
	return eligible
}

func opportunityMarketPercentiles(items []opportunityMarketScanRawMetric, field func(opportunityMarketScanRawMetric) float64) map[string]float64 {
	type rankedValue struct {
		symbol string
		value  float64
	}
	values := make([]rankedValue, 0, len(items))
	for _, item := range items {
		values = append(values, rankedValue{symbol: item.Instrument.Symbol, value: field(item)})
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].value == values[j].value {
			return values[i].symbol < values[j].symbol
		}
		return values[i].value < values[j].value
	})
	out := make(map[string]float64, len(values))
	for start := 0; start < len(values); {
		end := start + 1
		for end < len(values) && values[end].value == values[start].value {
			end++
		}
		percentile := float64(end) / float64(len(values)) * 100
		for i := start; i < end; i++ {
			out[values[i].symbol] = percentile
		}
		start = end
	}
	return out
}

func pctReturn(current, base float64) float64 {
	if current <= 0 || base <= 0 {
		return 0
	}
	return (current/base - 1) * 100
}

func safeRatio(value, base float64) float64 {
	if base <= 0 {
		return 0
	}
	return value / base
}
