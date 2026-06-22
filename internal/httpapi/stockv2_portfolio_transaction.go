package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"phantom-lancer/internal/stockv2"
)

// handleStockV2RecordTransaction 记录一笔买入/卖出交易。
func (s *Server) handleStockV2RecordTransaction(w http.ResponseWriter, r *http.Request) {
	portfolioID := r.PathValue("id")
	if portfolioID == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestRecordTransaction
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.RecordTransaction(r.Context(), portfolioID, req)
	if err != nil {
		http.Error(w, err.Error(), transactionErrorStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// handleStockV2ListTransactions 列出组合的交易流水(默认按时间倒序,上限 200)。
func (s *Server) handleStockV2ListTransactions(w http.ResponseWriter, r *http.Request) {
	portfolioID := r.PathValue("id")
	if portfolioID == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	items, err := s.stockV2.ListTransactions(r.Context(), portfolioID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]interface{}{"items": items})
}

// handleStockV2GetAssetCurve 返回组合资产曲线(每日总资产 + 买卖标记)。
func (s *Server) handleStockV2GetAssetCurve(w http.ResponseWriter, r *http.Request) {
	portfolioID := r.PathValue("id")
	if portfolioID == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	curve, err := s.stockV2.BuildAssetCurve(r.Context(), portfolioID, stockv2.AssetCurveOptions{Days: days})
	if err != nil {
		http.Error(w, err.Error(), transactionErrorStatus(err))
		return
	}
	s.writeJSON(w, curve)
}

// transactionErrorStatus 把领域错误映射到合适的 HTTP 状态码。
func transactionErrorStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrPortfolioNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrInvalidTransactionSide),
		errors.Is(err, stockv2.ErrInsufficientHolding):
		return http.StatusBadRequest
	default:
		// parseTransactionExecutedAt 等校验错误是普通 errors.New,无哨兵,统一 400
		return http.StatusBadRequest
	}
}
