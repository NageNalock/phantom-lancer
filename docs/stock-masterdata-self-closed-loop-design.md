# 股票主数据自闭环系统设计

> **⚠️ DEPRECATED（过期文档，请勿参考）⚠️**
>
> 文档日期：2026-06-15
> 标记过期：2026-06-17
> 原状态：已进入开发（M1–M5 一次性交付）
>
> 本文档描述的 A 股主数据自闭环方案（数据源选型、刷新链路、FTS5 检索、
> Combobox 录入、Agent 参考层、DuckDB 分层等）均已作废。股票模块正处于全面重构阶段，
> 任何实现、评审或 Agent 参考都**不得**以本文档为准，
> 请以重构过程中产出的新设计文档 / 最新代码为准。

本文描述 Phantom Lancer 股票模块的 A 股主数据（symbol/market/name/industry/concept/listingDate）自闭环系统设计。
面向个人使用，不构成投资建议。本系统包含 5 个交付里程碑：后台自动刷新、FTS5 全文检索 REST API、前端股票列表页、搜索式录入 Combobox、Agent 参考层 + DuckDB 分层存储。

---

## 1. 背景与核心问题

历史实现存在以下 4 个缺口：

1. **全量主数据刷新缺失**：`internal/stock/service.go:RunDataMaintenance` 的任务链（health/quote/market data derivation/news/reports/opportunities）不包含任何刷新 `stock_instruments` 表的调用。`RefreshInstruments` 只在手动 POST `/api/stock/instruments` 时才会执行，系统从未"自己"灌过 A 股全量数据。
2. **查询能力不足**：`ListStockInstruments(ctx, limit)` 仅支持 `ORDER BY updated_at DESC LIMIT ?`，limit 上限为 1000；没有 symbol 前缀、名称模糊、拼音首字母、行业概念等维度的查询。`GET /api/stock/instruments` 只暴露了 limit 参数。
3. **录入表单易出错**：symbol/market/name 是 3 个分散的 text input，用户必须手填代码、市场、名称，容易写错市场（SH/SZ/BJ）或名称错别字。
4. **Agent 横截面参考无底座**：Stock Agent 后续要参考全市场数据做筛选（"科创板半导体 换手率>10%"），当前 SQLite 行存对亿级 K 线做范围聚合太慢，但主数据仅 5.5k 行迁移列存反而是负优化——需要分层策略。

本方案一次性补齐上述 4 个缺口。

---

## 2. 存储选型决策（SQLite vs DuckDB）

### 2.1 最终建议：**分层存储，不全量迁移**

| 数据域 | 量级 | 存储选型 | 理由 |
|---|---|---|---|
| `stock_instruments` 主数据 | ≈5500 行 × 11 列 ≈ 800KB | **保留 SQLite** | ① point query 性能优于列存 ② 单行 UPSERT 是 DuckDB 弱项 ③ FTS5 在 go-sqlite3 内建，DuckDB 当前无成熟中文 tokenizer |
| `stock_market_data_points` K 线/行情快照/资金流 | 亿级行（5500 只 × 日 K 10 年 + 分钟级）≈10GB+ | **Phase 5 引入 DuckDB（独立 Store）** | 列存 + 分区 + 向量化 10–100x 优于 SQLite 行存 range agg，Agent 横截面分析刚需 |
| 其他 stock_* 小表（<10 万行） | MB 级 | 保留 SQLite | 读写混合、point query 为主，迁移无收益 |

### 2.2 为什么 instruments 不进 DuckDB
1. DuckDB 的单写者 MVCC 对单行 UPSERT 会重写整列 parquet block，成本比 SQLite 高 10–100 倍。
2. 每天 1 次全量刷新 + 每 6 小时增量 = UPSERT 写密集，SQLite B-Tree 更合适。
3. FTS5 pattern 在 Mail 模块（`mail_fts5_p7` + `MailMessageSearchP7`）已生产验证，DuckDB 无等效功能。

### 2.3 DuckDB 引入方式
- **不拆分现有 Store**：现有 `storage.go:Store` 保持单一 `*sql.DB`。新增 `storage/duckdb.go` 的 `DuckDBStore` 独立 struct，由 `stock.Service` 同时持有两者。
- **Go 驱动**：`github.com/marcboeker/go-duckdb` v1.7+（CGO；项目已用 CGO 驱动 `mattn/go-sqlite3`，不存在禁止 CGO 约束）。
- **Agent 调用透明路由**：`QueryUniverse()` 内部判断条件，如果只涉及 instruments 维度 → 走 SQLite FTS5；涉及 market cap/turnover rate 等行情聚合 → 走 DuckDB 筛选得到 symbol 集合再回 SQLite 取完整信息。
- **UT 兼容性**：DuckDB 相关 UT 用 `go:build duckdb_integration` tag 隔离，默认 UT 不跑。跨平台 build 失败时跳过 DuckDB 功能，不阻塞 M1–M4 交付。

---

## 3. 后台自动刷新 A 股全量主数据（PR1）

### 3.1 数据源（3 级互补）

| 层级 | 数据源 | 覆盖 | 用途 |
|---|---|---|---|
| L1 **主源** | `push2.eastmoney.com/api/qt/clist/get` | A 股全市场（主板/创业板/科创板/北交所）≈5500 | 拉取 symbol、名称、状态、f13(市场 0=SZ 1=SH 2=BJ)、f100=行业代码、f102=概念代码映射 |
| L2 **辅源** | `push2.eastmoney.com/api/qt/slist/get` | 行业/概念成员列表 | 补 industry / concept 中文列 |
| L3 **兜底** | `money.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData` | 同上 | 东财被限流/封 IP 时自动切 |

**L1 请求模板**：
```
GET https://push2.eastmoney.com/api/qt/clist/get
  ?pn={1..11}
  &pz=500
  &po=1&np=1&fltt=2&invt=2
  &fid=f3
  &fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23,m:0+t:81+s:2048
  &fields=f12,f13,f14,f2,f3,f4,f18,f100,f102
```
- `f12=symbol`, `f13=market`, `f14=name`, `f18=listingDate`, `f100=industry_id`, `f102=concept_ids`
- 每页 500 条，共 11 页；每页请求间 sleep 100ms 防反爬。
- Header：`Referer: https://quote.eastmoney.com/`、3 套 User-Agent 轮换。

### 3.2 刷新频率 & 调度

复用 `StartBackground()`（`service.go:156`）现有 ticker，不新增 goroutine。

| 任务 | 频率 | 入口 |
|---|---|---|
| 全量主数据刷新（含行业概念） | **每日 06:30 一次** + 12:00 补偿 | `RunDataMaintenance()` 头部插入 `s.refreshAStockUniverse(ctx, MaintenanceModeDaily)`，作为其他所有任务（行情、盯盘、机会发现）的源数据底座 |
| 增量状态更新（停牌/涨跌停） | watchTicker=30s（已有） | 复用 `fetchManagedPublicQuotes` 写 quote，不额外刷新主数据 |
| 退市/停复牌校验 | healthTicker=6h（已有） | `RunDataMaintenance()` 中在 health check 之后追加 |

**调度判定**（伪代码）：
```go
func (s *Service) refreshAStockUniverse(ctx context.Context, mode MaintenanceMode) {
    // 1. 根据 last_run_at 做频率门控（Daily 模式下 < 20h 不重复跑）
    last := s.store.LastTaskCompletedAt(ctx, "universe_refresh")
    if mode == MaintenanceModeDaily && s.now().Sub(last) < 20*time.Hour { return }

    // 2. 拉取远端
    items, fetchErrs := s.fetchAStockUniverse(ctx) // 东财主源失败切新浪
    // 3. 对比本地集合 → diff
    localSet := s.store.AllInstrumentSymbols(ctx)
    diff := computeDiff(localSet, items)
    // 4. 批量 UPSERT
    upserted := s.store.BatchUpsertInstruments(ctx, diff.ToUpsert)
    // 5. 标记退市（软删除，不 DELETE）
    delisted := s.store.MarkInstrumentsDelisted(ctx, diff.Orphans)
    // 6. 写审计任务（新 taskType="universe_refresh"）
    s.store.CreateStockDataTask(ctx, StockDataTask{
        TaskType: "universe_refresh", Source: "eastmoney_universe",
        Status: taskStatus(len(upserted), len(fetchErrs)),
        ProcessedCount: len(upserted), FailedCount: len(fetchErrs),
        FailureSummary: failureSummary(len(fetchErrs), strings.Join(fetchErrs, "; ")),
        ResultJSON: mustJSON(map[string]any{"upserted": len(upserted), "delisted": delisted}),
    })
}
```

### 3.3 幂等与质量
1. **UPSERT**：继续用 `UpsertStockInstrument`（`ON CONFLICT(symbol) DO UPDATE`）。但对于 `source='manual_override'` 的行，name/industry/concept 三列保留用户值不被自动覆盖。
2. **退市软删除**：远端不存在而本地存在 → `status = 'delisted'`，不 DELETE。
3. **失败退避**：复用 `quoteProviderBackoff` 算法和 `stock_data_sources.consecutive_failures`。
4. **质量字段**：全量成功 → `quality='fresh'`；部分成功 → `quality='partial'`；全失败 → 不覆盖原 quality。

### 3.4 手动触发
- **API**：复用 `POST /api/stock/instruments`。当 body 中 `auto == true` 且 `source == 'eastmoney_universe'` 时，内部调用 `refreshAStockUniverse(ctx, MaintenanceModeManual)`，忽略 items。不新增 endpoint。

---

## 4. FTS5 全文检索 + Search API（PR2）

### 4.1 FTS5 虚拟表 DDL

落位：`storage.go` 的 `baseSQL` 和 `fts5SQL` 末尾各追加一段，pattern 完全对齐 `mail_fts5_p7` 三张表 + 三个触发器。`stock_instruments` 表新增 `py` / `py_full` 两物理列。

```sql
ALTER TABLE stock_instruments ADD COLUMN py TEXT DEFAULT '';
ALTER TABLE stock_instruments ADD COLUMN py_full TEXT DEFAULT '';

CREATE VIRTUAL TABLE IF NOT EXISTS stock_instruments_fts USING fts5(
    symbol, name, py, py_full, industry, concept,
    market UNINDEXED, status UNINDEXED, quality UNINDEXED,
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER stock_instruments_ai AFTER INSERT ON stock_instruments BEGIN
    INSERT INTO stock_instruments_fts(rowid, symbol, name, py, py_full, industry, concept, market, status, quality)
    VALUES (new.rowid, new.symbol, new.name, new.py, new.py_full, new.industry, new.concept, new.market, new.status, new.quality);
END;
CREATE TRIGGER stock_instruments_au AFTER UPDATE ON stock_instruments BEGIN
    DELETE FROM stock_instruments_fts WHERE rowid = old.rowid;
    INSERT INTO stock_instruments_fts(rowid, symbol, name, py, py_full, industry, concept, market, status, quality)
    VALUES (new.rowid, new.symbol, new.name, new.py, new.py_full, new.industry, new.concept, new.market, new.status, new.quality);
END;
CREATE TRIGGER stock_instruments_ad AFTER DELETE ON stock_instruments BEGIN
    DELETE FROM stock_instruments_fts WHERE rowid = old.rowid;
END;
```

**拼音生成**：Go 端用 `github.com/mozillazg/go-pinyin`（纯 Go），在 `refreshAStockUniverse` 的 UPSERT 前为每只股票预计算 `py`（首字母）和 `py_full`（无空格全拼），写入 `stock_instruments` 表的新列，由触发器自动入 FTS。

**降级兼容**：如果 `FTS5Available()` 返回 false，`stripFTS5()` 剥离上述 DDL。`SearchStockInstruments` 函数自动退化为 `symbol LIKE || name LIKE || industry LIKE` 组合查询。

### 4.2 REST API

```
GET /api/stock/instruments/search
```

| 参数 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `q` | string | `""` | 搜索关键字，多 token 空格分隔 = AND |
| `market` | string | `""` | SH / SZ / BJ，可多个逗号分隔 |
| `status` | string | `""` | tradable/halted/delisted/limit_up/limit_down/unknown，可多个 |
| `industry` | string | `""` | 行业子串匹配 |
| `quality` | string | `""` | fresh/stale/partial/failed |
| `sort` | string | `relevance` | relevance(sort bm25,有 q 时默认) / symbol_asc / market_then_symbol / updated_desc |
| `page` | int | `1` | 起始页 1 |
| `pageSize` | int | `20` | 10–200，> 200 clamp |

**响应**：
```json
{
  "total": 5532, "page": 1, "pageSize": 20,
  "items": [ { "symbol":"600519","market":"SH","name":"贵州茅台", ... } ],
  "snippets": { "600519": { "name":"贵州[茅台]", "industry":"[白酒]..." } }
}
```

### 4.3 搜索排序规则（Combobox 体验关键）
1. symbol **前缀完全匹配** → boost ×100（搜 "600" → 600000 排第一）。
2. `py` **前缀完全匹配** → boost ×20（搜 "GZMT" → 贵州茅台排第一）。
3. 命中 symbol **子串但非前缀** → boost ×5。
4. 其余按 bm25(`stock_instruments_fts`) 排序。
5. Tie-break：`symbol ASC`。

### 4.4 现有接口小改动（向后兼容）
- `GET /api/stock/instruments`：limit 上限从 1000 → 10000；新增 `include_delisted` query 参数（默认 true）。
- `ListStockInstruments`（`stock_data.go:494`）：limit clamp 放宽到 10000。

---

## 5. 前端股票主数据列表页（PR3）

### 5.1 信息架构

现有 `StockDataWorkbench.tsx` 重构为 Tab 容器（不新增路由，减少跳转）：

```
StockDataWorkbench (作为 StockView → Data Tab 内的组件)
  ├─ Tab 1 [📚 全量股票]       ← 新增，默认激活
  ├─ Tab 2 [🔌 数据源治理]     ← 已有原样搬
  ├─ Tab 3 [📝 主数据录入]     ← 已有
  ├─ Tab 4 [📈 历史 K 线]      ← 已有
  └─ Tab 5 [📰 消息面]         ← 已有
```

### 5.2 布局（StockInstrumentList.tsx）
```
┌─────────────────────────────────────────────────────────────────┐
│ 工具栏：搜索框(即 Combobox) + 4 个筛选器 + 手动刷新按钮         │
│ [🔍 代码/名称/拼音首字母…] [市场▼] [状态▼] [行业▼] [质量▼] [🔄]│
│                                                   < 1 / 111 > ▾50│
├─────────────────────────────────────────────────────────────────┤
│ 代码    名称    市场  状态     行业    概念      上市日期  质量  ▼│
│ 600519 贵州茅台  SH  可交易   白酒    消费龙头… 2001-08   🟢     │
│  ↓ 展开详情：concept 完整 + 数据源 + updatedAt + 快捷操作按钮    │
│     [录入持仓] [生成策略] [关注] [查看行情] [编辑主数据]        │
├─────────────────────────────────────────────────────────────────┤
│ 分页：页码 + 跳转到第 X 页                                     │
└─────────────────────────────────────────────────────────────────┘
```

### 5.3 关键交互
1. 搜索 debounce 200ms。
2. 行业筛选使用 `<datalist>`，首次进入 tab 时异步请求唯一行业值。
3. 空态（`total == 0`）：展示引导卡片 + 「🚀 执行首次全量初始化」按钮。
4. 操作列按钮带参跳转到其他录入表单或 Tab：
   - `录入持仓` → 切换 StockView → Holdings Tab + 调用表单预填 action（URL hash：`#holdings?prefill=600519.SH`）
   - `编辑主数据` → Modal 修改，保存时写 `source='manual_override'`

### 5.4 依赖改动
- `web/src/app/types.ts`：新增 `StockInstrumentSearchResponse`、`StockInstrumentQueryParams` 接口。
- **零新增 npm 依赖**，复用现有 UI 组件库。

---

## 6. 搜索式录入 Combobox（PR4）

### 6.1 组件定义
```tsx
<StockSymbolCombobox
  onSelect={(inst: StockInstrument) => { ... }}
  placeholder="输入代码 / 名称 / 拼音首字母..."
  allowFreeInput={true}   // true: 搜不到允许手填(兼容其他市场)
  initialValue={{symbol, market, name}}
/>
```

**键盘交互**：↑↓ 导航 / Enter 选中 / ESC 关闭 / Tab 跳出；`onCompositionStart/End` IME 合成暂停 debounce；空输入时展示"历史常用 6 只"（watch + holdings + strategies 聚合统计本地 TOP6，useMemo 缓存）。

### 6.2 替换的 4 处录入表单
| 文件 | 原 3 字段（symbol/market/name） | 替换后 |
|---|---|---|
| `StockView.tsx` 录入/更新持仓 | `<input>+<select>+<input>` | 1 个 StockSymbolCombobox |
| `StockView.tsx` 人工策略录入 | 同上 | 同上 |
| `StockOpportunities.tsx` 候选机会录入（L83–91） | 同上 | 同上 |
| `StockDataWorkbench.tsx` 3 个表单（主数据/历史K线/消息面） | 多处 | 同上 |

**替换规则**：onSelect 回调中 `setFormValue(symbol) / setFormValue(market) / setFormValue(name)`，对后端完全透明，不改变 POST body 的字段结构。

---

## 7. Agent 参考层 + DuckDB 分层（PR5）

### 7.1 新增两个服务层函数
放置在 `internal/stock/data_automation.go`，与 `discoveryQueries` 平级。

```go
// point lookup：基于 symbol 主键索引 < 0.01ms
func (s *Service) GetInstrumentBySymbol(ctx context.Context, symbol string) (storage.StockInstrument, bool, error)

// 横截面筛选：内部路由
func (s *Service) QueryUniverse(ctx context.Context, q UniverseQuery) ([]storage.StockInstrument, int, error)

type UniverseQuery struct {
    Markets, Statuses    []string
    Industries, Concepts []string
    MinListingDate       string
    Page, PageSize       int
    SortBy               SortField

    // —— 以下字段需要行情数据，路由到 DuckDB，未来生效 ——
    MinMarketCap   int64
    MinTurnoverRate float64
    PriceRange     [2]float64
}
```

**内部路由规则**：
- 未设置 MinMarketCap / MinTurnoverRate / PriceRange → 直接走 `SearchStockInstruments` FTS5。
- 任一字段设置 → 走 `DuckDBStore.QuerySymbolsByMarketQuote(...)` → 得到 symbol 集合 → 回 SQLite FTS5 取完整信息（两阶段）。
- DuckDB 未初始化（驱动不可用或首次运行无数据）→ 返回有意义的错误，Agent 走降级 path：仅基于 instruments 维度。

### 7.2 DuckDBStore（storage/duckdb.go）
- **文件路径**：`data/stock_olap.duckdb`（独立于主 SQLite `data/app.db`）。
- **初始化建表**：
  ```sql
  CREATE TABLE IF NOT EXISTS daily_ohlcv (
      symbol      VARCHAR,
      market      VARCHAR,
      trade_date  DATE,
      open, high, low, close DOUBLE,
      volume      BIGINT,
      amount      DOUBLE,
      turnover_rate DOUBLE,
      market_cap  DOUBLE
  );
  CREATE INDEX IF NOT EXISTS idx_daily_ohlcv_sym_dt ON daily_ohlcv(symbol, trade_date);
  -- 按年度分区（Hive 风格分区目录，外挂 parquet 可）
  ```
- **冷启动**：首次运行不 import 历史。提供 `ImportMarketDataPoints(ctx, since)` 函数，后续可由 Agent 手动触发；每日刷新从 `stock_market_data_points` 增量 ETL 1 次。

### 7.3 `discoveryQueries` 改造
旧实现（`data_automation.go:432`）硬编码 `ListStockInstruments(ctx, 80)` → 改为走 `QueryUniverse(ctx, UniverseQuery{PageSize:80, SortBy:UpdatedDesc})`，天然支持按市场/行业扩展筛选条件。

### 7.4 编译 tag 隔离
- DuckDB 相关 UT 加 `//go:build duckdb_integration`，普通 `go test ./...` 不运行。
- 主二进制**不做 build tag 隔离**：因为项目已用 CGO go-sqlite3，DuckDB CGO 驱动同环境；跨平台构建失败时退化为「不 Open DuckDBStore」即可，不影响其他功能。

---

## 8. 交付分期与里程碑

| 里程碑 | 范围 | 预计代码量 | 交付物 |
|---|---|---|---|
| **M1 (PR1)** | 后台自动刷新 | ~400 Go | 全量 5532 条入库，后台自动跑，手动触发可用 |
| **M2 (PR2)** | FTS5 + Search API | ~350 Go | `/api/stock/instruments/search` 可用，FTS5 不可用时 LIKE 降级 |
| **M3 (PR3)** | 前端列表页 UI | ~500 TS | 浏览 / 搜索 / 过滤器 / 分页 / 快捷操作 |
| **M4 (PR4)** | Combobox 搜索录入 | ~300 TS | 4 处录入表单统一走搜索 |
| **M5 (PR5)** | Agent 参考 + DuckDB | ~600 Go | Agent 可做横截面筛选，为回测铺路 |

全部里程碑在本周期一次性落地（开发顺序串行：M1→M5），但代码提交可按 5 个 commit 切分便于 review。

---

## 9. 风险与边界

| ID | 风险 | 影响 | 应对 |
|---|---|---|---|
| R1 | 东财反爬封 IP | 全量刷新失败 | 3 UA 轮换 + 100ms sleep + 新浪兜底 + 连续 3 次 blocked + 手动 xlsx 导入按钮 |
| R2 | 新股/更名不及时 | Combobox 搜不到新代码 | 12:00 补偿刷新 + manual_override 不被覆盖 |
| R3 | CI 上 FTS5 不可用 → UT 全挂 | 无法合并 | `FTS5Available()` 探针 + `stripFTS5` + FTS UT 条件 Skip + LIKE 降级路径覆盖 |
| R4 | DuckDB CGO 跨平台编译失败 | M5 延期但不阻塞 M1–M4 | UT build tag 隔离 + DuckDBStore Open 失败不 panic，QueryUniverse 检测到 nil 只走 instruments 路径 |
| R5 | 拼音歧义（ZGLT=多只） | 搜索顺序不对 | PR2 先按 symbol 字典序 tie-break；PR5 引入 market_cap 后按市值排 + 搜索命中记忆表 |
| R6 | 退市股长期留存污染 Agent 分析 | 结果包含已退市 | QueryUniverse 默认排除 `delisted`；列表页默认隐藏；可开关展示；每年归档（留接口，暂不实现） |
| R7 | 东财行业分类标准变更 | 旧分类不兼容 | 每次刷新 OVERWRITE industry/concept；manual_override 行保留 |
