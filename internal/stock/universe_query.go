package stock

import (
	"context"
	"errors"
	"strings"

	"phantom-lancer/internal/storage"
)

type SortField string

const (
	SortRelevance        SortField = "relevance"
	SortSymbolAsc        SortField = "symbol_asc"
	SortMarketThenSymbol SortField = "market_then_symbol"
	SortUpdatedDesc      SortField = "updated_desc"
)

type UniverseQuery struct {
	Query          string
	Markets        []string
	Statuses       []string
	Industries     []string
	Concepts       []string
	MinListingDate string
	Page           int
	PageSize       int
	SortBy         SortField

	MinMarketCap    int64
	MinTurnoverRate float64
	PriceRange      [2]float64
	IncludeDelisted bool
}

func (s *Service) GetInstrumentBySymbol(ctx context.Context, symbol string) (storage.StockInstrument, bool, error) {
	item, err := s.store.GetStockInstrument(ctx, symbol)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.StockInstrument{}, false, nil
	}
	if err != nil {
		return storage.StockInstrument{}, false, err
	}
	return item, true, nil
}

func (s *Service) QueryUniverse(ctx context.Context, q UniverseQuery) ([]storage.StockInstrument, int, error) {
	if q.MinMarketCap > 0 || q.MinTurnoverRate > 0 || q.PriceRange[0] > 0 || q.PriceRange[1] > 0 {
		return nil, 0, errors.New("stock OLAP quote universe is not initialized; retry without market-cap, turnover-rate or price-range filters")
	}
	search := storage.StockInstrumentSearchParams{
		Query:           q.Query,
		Markets:         q.Markets,
		Statuses:        q.Statuses,
		Concepts:        q.Concepts,
		MinListingDate:  q.MinListingDate,
		IncludeDelisted: q.IncludeDelisted,
		Page:            q.Page,
		PageSize:        q.PageSize,
		Sort:            string(q.SortBy),
	}
	if len(q.Industries) > 0 {
		search.Industry = strings.Join(q.Industries, " ")
	}
	if search.Sort == "" {
		search.Sort = string(SortRelevance)
	}
	result, err := s.store.SearchStockInstruments(ctx, search)
	if err != nil {
		return nil, 0, err
	}
	return result.Items, result.Total, nil
}
