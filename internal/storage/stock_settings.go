package storage

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
)

// StockSettings 股票模块级配置（代理开关、行情 TTL、刷新频率等）。
// 仅作用于股票模块，不影响其他系统能力。
type StockSettings struct {
	ID                   string `json:"id"`
	ProxyEnabled         bool   `json:"proxyEnabled"`
	ProxyType            string `json:"proxyType"`            // http / socks5
	ProxyAddress         string `json:"proxyAddress"`         // host:port（不含 scheme）
	ProxyUseForEastmoney bool   `json:"proxyUseForEastmoney"` // 东财行情/主数据是否走代理
	ProxyUseForSina      bool   `json:"proxyUseForSina"`      // 新浪行情/主数据是否走代理
	ProxyUseForTencent   bool   `json:"proxyUseForTencent"`   // 腾讯行情是否走代理（预留）
	QuoteTTLSeconds      int    `json:"quoteTtlSeconds"`      // 行情有效期（秒）
	AutoRefreshEnabled   bool   `json:"autoRefreshEnabled"`   // 定时自动刷新主数据
	RefreshIntervalSecs  int    `json:"refreshIntervalSecs"`  // 自动刷新间隔（秒）
	DefaultDataSource    string `json:"defaultDataSource"`    // 默认首选数据源 eastmoney/sina
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
}

// DefaultStockSettings 返回默认配置：代理关闭，各源都不强制走代理。
func DefaultStockSettings() StockSettings {
	return StockSettings{
		ID:                   "default",
		ProxyEnabled:         false,
		ProxyType:            "http",
		ProxyAddress:         "",
		ProxyUseForEastmoney: false,
		ProxyUseForSina:      false,
		ProxyUseForTencent:   false,
		QuoteTTLSeconds:      60,
		AutoRefreshEnabled:   true,
		RefreshIntervalSecs:  14400, // 4 小时
		DefaultDataSource:    "eastmoney",
	}
}

// NormalizeStockSettings 规范化字段，做合法性裁剪（不做强校验，错误留给
// 业务层 Update handler 处理）。
func NormalizeStockSettings(s StockSettings) StockSettings {
	if s.ID == "" {
		s.ID = "default"
	}
	s.ProxyType = strings.TrimSpace(strings.ToLower(s.ProxyType))
	if s.ProxyType == "" {
		s.ProxyType = "http"
	}
	if s.ProxyType != "http" && s.ProxyType != "socks5" {
		s.ProxyType = "http"
	}
	s.ProxyAddress = strings.TrimSpace(s.ProxyAddress)
	// 代理启用但地址为空 → 自动关掉开关，避免启动全挂
	if s.ProxyEnabled && s.ProxyAddress == "" {
		s.ProxyEnabled = false
	}
	// 如果总开关关了，也把各源开关关了（保持语义一致）
	if !s.ProxyEnabled {
		s.ProxyUseForEastmoney = false
		s.ProxyUseForSina = false
		s.ProxyUseForTencent = false
	}
	s.DefaultDataSource = strings.TrimSpace(strings.ToLower(s.DefaultDataSource))
	if s.DefaultDataSource == "" {
		s.DefaultDataSource = "eastmoney"
	}
	if s.QuoteTTLSeconds < 0 {
		s.QuoteTTLSeconds = 0
	}
	if s.RefreshIntervalSecs < 300 {
		s.RefreshIntervalSecs = 300 // 最小 5 分钟
	}
	return s
}

// ValidateStockSettings 深度校验（供 handler 在保存前调用）。
// 不通过时返回人类可读错误。
func ValidateStockSettings(s StockSettings) error {
	if s.ProxyEnabled {
		if s.ProxyAddress == "" {
			return errors.New("proxy enabled but proxyAddress is empty")
		}
		if _, _, err := net.SplitHostPort(s.ProxyAddress); err != nil {
			return errors.New("proxyAddress must be in host:port format: " + err.Error())
		}
		useCount := 0
		if s.ProxyUseForEastmoney {
			useCount++
		}
		if s.ProxyUseForSina {
			useCount++
		}
		if s.ProxyUseForTencent {
			useCount++
		}
		if useCount == 0 {
			return errors.New("proxy enabled but no data source selected to route through proxy")
		}
	}
	return nil
}

// ProxyURLForSource 根据 settings 和目标源返回代理 URL。
// 如果该源不需要走代理返回空字符串。
func (s StockSettings) ProxyURLForSource(source string) string {
	if !s.ProxyEnabled || s.ProxyAddress == "" {
		return ""
	}
	use := false
	switch strings.ToLower(source) {
	case "eastmoney", "eastmoney_universe":
		use = s.ProxyUseForEastmoney
	case "sina", "sina_universe":
		use = s.ProxyUseForSina
	case "tencent":
		use = s.ProxyUseForTencent
	}
	if !use {
		return ""
	}
	scheme := s.ProxyType
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + s.ProxyAddress
}

// EnsureStockSettings 插入默认行（如果不存在）。
func (s *Store) EnsureStockSettings(ctx context.Context) error {
	defaults := DefaultStockSettings()
	ts := now()
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO stock_settings (
  id, proxy_enabled, proxy_type, proxy_address,
  proxy_use_for_eastmoney, proxy_use_for_sina, proxy_use_for_tencent,
  quote_ttl_seconds, auto_refresh_enabled, refresh_interval_seconds,
  default_data_source, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		defaults.ID,
		boolInt(defaults.ProxyEnabled),
		defaults.ProxyType,
		defaults.ProxyAddress,
		boolInt(defaults.ProxyUseForEastmoney),
		boolInt(defaults.ProxyUseForSina),
		boolInt(defaults.ProxyUseForTencent),
		defaults.QuoteTTLSeconds,
		boolInt(defaults.AutoRefreshEnabled),
		defaults.RefreshIntervalSecs,
		defaults.DefaultDataSource,
		ts, ts,
	)
	return err
}

// GetStockSettings 读取配置，不存在时自动 Ensure。
func (s *Store) GetStockSettings(ctx context.Context) (StockSettings, error) {
	if err := s.EnsureStockSettings(ctx); err != nil {
		return StockSettings{}, err
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, proxy_enabled, proxy_type, proxy_address,
       proxy_use_for_eastmoney, proxy_use_for_sina, proxy_use_for_tencent,
       quote_ttl_seconds, auto_refresh_enabled, refresh_interval_seconds,
       default_data_source, created_at, updated_at
FROM stock_settings WHERE id = 'default'`)
	set, err := scanStockSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultStockSettings(), nil
	}
	return NormalizeStockSettings(set), err
}

// UpdateStockSettings 保存配置（INSERT OR UPDATE）。
func (s *Store) UpdateStockSettings(ctx context.Context, in StockSettings) (StockSettings, error) {
	in = NormalizeStockSettings(in)
	ts := now()
	if in.CreatedAt == "" {
		in.CreatedAt = ts
	}
	in.UpdatedAt = ts
	_, err := s.db.ExecContext(ctx, `
INSERT INTO stock_settings (
  id, proxy_enabled, proxy_type, proxy_address,
  proxy_use_for_eastmoney, proxy_use_for_sina, proxy_use_for_tencent,
  quote_ttl_seconds, auto_refresh_enabled, refresh_interval_seconds,
  default_data_source, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  proxy_enabled = excluded.proxy_enabled,
  proxy_type = excluded.proxy_type,
  proxy_address = excluded.proxy_address,
  proxy_use_for_eastmoney = excluded.proxy_use_for_eastmoney,
  proxy_use_for_sina = excluded.proxy_use_for_sina,
  proxy_use_for_tencent = excluded.proxy_use_for_tencent,
  quote_ttl_seconds = excluded.quote_ttl_seconds,
  auto_refresh_enabled = excluded.auto_refresh_enabled,
  refresh_interval_seconds = excluded.refresh_interval_seconds,
  default_data_source = excluded.default_data_source,
  updated_at = excluded.updated_at`,
		in.ID,
		boolInt(in.ProxyEnabled),
		in.ProxyType,
		in.ProxyAddress,
		boolInt(in.ProxyUseForEastmoney),
		boolInt(in.ProxyUseForSina),
		boolInt(in.ProxyUseForTencent),
		in.QuoteTTLSeconds,
		boolInt(in.AutoRefreshEnabled),
		in.RefreshIntervalSecs,
		in.DefaultDataSource,
		in.CreatedAt, in.UpdatedAt,
	)
	if err != nil {
		return StockSettings{}, err
	}
	return s.GetStockSettings(ctx)
}

func scanStockSettings(row workspaceScanner) (StockSettings, error) {
	var s StockSettings
	var proxyEnabled, useEM, useSina, useTencent, autoRefresh int
	err := row.Scan(
		&s.ID,
		&proxyEnabled,
		&s.ProxyType,
		&s.ProxyAddress,
		&useEM, &useSina, &useTencent,
		&s.QuoteTTLSeconds,
		&autoRefresh,
		&s.RefreshIntervalSecs,
		&s.DefaultDataSource,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return StockSettings{}, err
	}
	s.ProxyEnabled = proxyEnabled == 1
	s.ProxyUseForEastmoney = useEM == 1
	s.ProxyUseForSina = useSina == 1
	s.ProxyUseForTencent = useTencent == 1
	s.AutoRefreshEnabled = autoRefresh == 1
	return s, nil
}
