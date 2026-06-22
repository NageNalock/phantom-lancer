import { ArrowClockwise, Eye, Plus, Minus, Trash, Pencil, Wallet, X, Check, MagnifyingGlass } from "@phosphor-icons/react";
import { useEffect, useRef, useState } from "react";
import { AreaSeries, createChart, createSeriesMarkers, type CrosshairMode, type IChartApi, type ISeriesApi, type Time } from "lightweight-charts";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2AssetCurveResponse, StockV2Holding, StockV2Instrument, StockV2Portfolio, StockV2PortfolioRefreshResult, StockV2PortfolioSnapshot, StockV2PortfolioWithHoldings, StockV2Transaction } from "../../app/types";
import { Button, EmptyState, Field, Notice, Panel, Pill, SubTabs } from "../../components/ui";
import { stockV2RiskLabel, stockV2ValuationStatusLabel, stockV2ValuationStatusTone } from "../../domain/labels";
import { StockV2InstrumentDetail } from "./StockV2InstrumentDetail";

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;

export function StockV2Portfolios({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const portfolios = data.stockv2.portfolios || [];
  const [editingId, setEditingId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(portfolios[0]?.id || null);
  const [holdingsDialog, setHoldingsDialog] = useState<{ portfolioId: string; mode: "add" | "edit"; holding?: StockV2Holding } | null>(null);
  const [tradeDialog, setTradeDialog] = useState<{ portfolioId: string; mode: "buy" | "sell"; holding?: StockV2Holding } | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshResult, setRefreshResult] = useState<StockV2PortfolioRefreshResult | null>(null);
  const [snapshots, setSnapshots] = useState<StockV2PortfolioSnapshot[]>([]);
  const [transactions, setTransactions] = useState<StockV2Transaction[]>([]);
  const [assetCurve, setAssetCurve] = useState<StockV2AssetCurveResponse | null>(null);
  const [detailTab, setDetailTab] = useState<"holdings" | "transactions" | "curve">("holdings");
  const [selectedInstrument, setSelectedInstrument] = useState<StockV2Instrument | null>(null);

  // 选中第一个组合
  useEffect(() => {
    if (!selectedId && portfolios.length > 0) {
      setSelectedId(portfolios[0].id);
    }
  }, [portfolios, selectedId]);

  const selected = portfolios.find((p) => p.id === selectedId) || null;
  const selectedRefreshResult = refreshResult && refreshResult.portfolioId === selected?.id ? refreshResult : null;
  const latestSnapshot = selectedRefreshResult?.snapshot || snapshots[0];
  const displayedHoldings = selectedRefreshResult?.holdings || selected?.holdings || [];

  useEffect(() => {
    setRefreshResult(null);
    setSnapshots([]);
    setTransactions([]);
    setAssetCurve(null);
    if (!selectedId) return;
    let cancelled = false;
    actions.api<{ items: StockV2PortfolioSnapshot[] }>(`/api/stockv2/portfolios/${selectedId}/snapshots?limit=5`)
      .then((data) => {
        if (!cancelled) setSnapshots(data.items || []);
      })
      .catch(() => {
        if (!cancelled) setSnapshots([]);
      });
    actions.api<{ items: StockV2Transaction[] }>(`/api/stockv2/portfolios/${selectedId}/transactions?limit=100`)
      .then((data) => {
        if (!cancelled) setTransactions(data.items || []);
      })
      .catch(() => {
        if (!cancelled) setTransactions([]);
      });
    return () => {
      cancelled = true;
    };
  }, [actions, selectedId]);

  // 资产曲线懒加载:切到「资产图」tab 才拉
  useEffect(() => {
    if (detailTab !== "curve" || !selectedId) return;
    let cancelled = false;
    actions.api<StockV2AssetCurveResponse>(`/api/stockv2/portfolios/${selectedId}/asset-curve`)
      .then((data) => {
        if (!cancelled) setAssetCurve(data);
      })
      .catch(() => {
        if (!cancelled) setAssetCurve(null);
      });
    return () => {
      cancelled = true;
    };
  }, [actions, selectedId, detailTab]);

  async function refreshSelectedPortfolio() {
    if (!selected) return;
    setRefreshing(true);
    try {
      await runAction("刷新资产", async () => {
        const result = await actions.api<StockV2PortfolioRefreshResult>(`/api/stockv2/portfolios/${selected.id}/refresh`, {
          method: "POST",
          body: { triggerSource: "web" },
        });
        setRefreshResult(result);
        const history = await actions.api<{ items: StockV2PortfolioSnapshot[] }>(`/api/stockv2/portfolios/${selected.id}/snapshots?limit=5`);
        setSnapshots(history.items || []);
      });
    } finally {
      setRefreshing(false);
    }
  }

  return (
    <div className="grid gap-4">
      {/* 组合列表 */}
      <Panel
        title="投资组合"
        subtitle={`${portfolios.length} 个组合 · 独立管理仓位和风控`}
        actions={
          <Button tone="primary" onClick={() => setCreating(true)}>
            <Plus size={14} className="mr-1.5" />
            新建组合
          </Button>
        }
      >
        {portfolios.length === 0 ? (
          <EmptyPortfolios onAdd={() => setCreating(true)} />
        ) : (
          <div className="grid gap-2">
            {portfolios.map((p) => (
              <PortfolioRow
                key={p.id}
                portfolio={p}
                selected={p.id === selectedId}
                onSelect={() => setSelectedId(p.id)}
                onEdit={() => setEditingId(p.id)}
                onDelete={() => void runAction("删除组合", async () => {
                  await actions.api(`/api/stockv2/portfolios/${p.id}`, { method: "DELETE" });
                  if (selectedId === p.id) setSelectedId(null);
                })}
              />
            ))}
          </div>
        )}
      </Panel>

      {/* 组合详情 / 持仓 */}
      {selected ? (
        <Panel
          title="组合详情"
          subtitle={`${selected.name} · ${displayedHoldings.length} 只持仓 · ${transactions.length} 笔交易`}
          actions={
            <>
              <Button onClick={() => void refreshSelectedPortfolio()} disabled={refreshing}>
                <ArrowClockwise size={14} className="mr-1.5" />
                {refreshing ? "刷新中" : "刷新资产"}
              </Button>
              <Button onClick={openStockV2MonitorTab}>
                <Eye size={14} className="mr-1.5" />
                监控与任务
              </Button>
              <Button onClick={() => setHoldingsDialog({ portfolioId: selected.id, mode: "add" })}>
                <Plus size={14} className="mr-1.5" />
                添加持仓
              </Button>
              <Button onClick={() => setTradeDialog({ portfolioId: selected.id, mode: "sell" })} disabled={displayedHoldings.length === 0}>
                <Minus size={14} className="mr-1.5" />
                卖出
              </Button>
              <Button tone="primary" onClick={() => setTradeDialog({ portfolioId: selected.id, mode: "buy" })}>
                <Plus size={14} className="mr-1.5" />
                买入
              </Button>
            </>
          }
        >
          <PortfolioValuationSummary portfolio={selected} refreshResult={selectedRefreshResult} snapshot={latestSnapshot} />

          <div className="mt-4">
            <SubTabs
              tabs={[
                { id: "holdings", label: "持仓" },
                { id: "transactions", label: "流水" },
                { id: "curve", label: "资产图" },
              ]}
              activeId={detailTab}
              onChange={setDetailTab}
              ariaLabel="组合详情"
            />
          </div>

          {detailTab === "holdings" ? (
            <>
              {displayedHoldings.length === 0 ? (
                <p className="mt-3 text-sm text-[var(--muted)]">暂无持仓，点击右上角【买入】添加。</p>
              ) : (
                <div className="mt-3 overflow-x-auto">
                  <p className="mb-2 text-xs text-[var(--muted)]">
                    行情状态用于说明当前持仓估值依据：最新行情、旧价沿用、成本价估算或无可用价格。后续操作建议会把它作为约束检查输入。
                  </p>
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-[var(--line)] text-left text-xs text-[var(--muted)]">
                        <th className="py-2 pr-4 font-medium">代码</th>
                        <th className="py-2 pr-4 font-medium">名称</th>
                        <th className="py-2 pr-4 font-medium">数量</th>
                        <th className="py-2 pr-4 font-medium">成本价</th>
                        <th className="py-2 pr-4 font-medium">现价</th>
                        <th className="py-2 pr-4 font-medium">价格时间</th>
                        <th className="py-2 pr-4 font-medium">市值</th>
                        <th className="py-2 pr-4 font-medium">盈亏</th>
                        <th className="py-2 pr-4 font-medium">占比</th>
                        <th className="py-2 pr-4 font-medium">行情状态</th>
                        <th className="py-2 pr-4 font-medium">操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {displayedHoldings.map((h) => (
                        <HoldingRow
                          key={h.id}
                          holding={h}
                          onOpen={() => setSelectedInstrument(instrumentFromHolding(h))}
                          onBuy={() => setTradeDialog({ portfolioId: selected.id, mode: "buy", holding: h })}
                          onSell={() => setTradeDialog({ portfolioId: selected.id, mode: "sell", holding: h })}
                          onEdit={() => setHoldingsDialog({ portfolioId: selected.id, mode: "edit", holding: h })}
                          onDelete={() => void runAction("删除持仓", async () => {
                            await actions.api(`/api/stockv2/portfolios/${selected.id}/holdings/${h.id}`, { method: "DELETE" });
                          })}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {snapshots.length > 0 ? <SnapshotHistory snapshots={snapshots} /> : null}
            </>
          ) : null}

          {detailTab === "transactions" ? (
            <TransactionsTable transactions={transactions} />
          ) : null}

          {detailTab === "curve" ? (
            <div className="mt-3">
              {assetCurve?.estimated ? (
                <Notice tone="warn" className="mb-2">
                  部分日期缺少行情数据，已用最近收盘价 / 最新价估算。补全日 K 后曲线会更准确。
                </Notice>
              ) : null}
              <StockV2PortfolioAssetChart curve={assetCurve} />
            </div>
          ) : null}
        </Panel>
      ) : null}

      {/* 新建/编辑组合弹窗 */}
      {creating ? (
        <PortfolioDialog
          mode="create"
          onClose={() => setCreating(false)}
          onSubmit={async (data) => {
            await runAction("创建组合", async () => {
              await actions.api("/api/stockv2/portfolios", {
                method: "POST",
                body: data,
              });
              setCreating(false);
            });
          }}
        />
      ) : null}

      {editingId ? (
        <PortfolioDialog
          mode="edit"
          initial={portfolios.find((p) => p.id === editingId)}
          onClose={() => setEditingId(null)}
          onSubmit={async (data) => {
            await runAction("保存组合", async () => {
              await actions.api(`/api/stockv2/portfolios/${editingId}`, {
                method: "PUT",
                body: data,
              });
              setEditingId(null);
            });
          }}
        />
      ) : null}

      {/* 添加/编辑持仓弹窗 */}
      {holdingsDialog ? (
        <HoldingDialog
          mode={holdingsDialog.mode}
          initial={holdingsDialog.holding}
          actions={actions}
          onClose={() => setHoldingsDialog(null)}
          onSubmit={async (data) => {
            await runAction(holdingsDialog.mode === "add" ? "添加持仓" : "保存持仓", async () => {
              if (holdingsDialog.mode === "add") {
                await actions.api(`/api/stockv2/portfolios/${holdingsDialog.portfolioId}/holdings`, {
                  method: "POST",
                  body: data,
                });
              } else if (holdingsDialog.holding) {
                await actions.api(`/api/stockv2/portfolios/${holdingsDialog.portfolioId}/holdings/${holdingsDialog.holding.id}`, {
                  method: "PUT",
                  body: data,
                });
              }
              setHoldingsDialog(null);
            });
          }}
        />
      ) : null}

      {/* 买入/卖出交易弹窗 */}
      {tradeDialog ? (
        <TradeDialog
          mode={tradeDialog.mode}
          portfolioId={tradeDialog.portfolioId}
          holding={tradeDialog.holding}
          actions={actions}
          cash={selected?.cash ?? 0}
          onClose={() => setTradeDialog(null)}
          onSubmit={async (payload) => {
            await runAction(tradeDialog.mode === "buy" ? "买入" : "卖出", async () => {
              await actions.api(`/api/stockv2/portfolios/${tradeDialog.portfolioId}/transactions`, {
                method: "POST",
                body: payload,
              });
              setTradeDialog(null);
              // 刷新持仓估值 + 流水
              await refreshSelectedPortfolio();
              const t = await actions.api<{ items: StockV2Transaction[] }>(`/api/stockv2/portfolios/${tradeDialog.portfolioId}/transactions?limit=100`);
              setTransactions(t.items || []);
              if (detailTab === "curve") {
                const c = await actions.api<StockV2AssetCurveResponse>(`/api/stockv2/portfolios/${tradeDialog.portfolioId}/asset-curve`);
                setAssetCurve(c);
              }
            });
          }}
        />
      ) : null}

      {selectedInstrument ? (
        <StockV2InstrumentDetail
          inst={selectedInstrument}
          actions={actions}
          onClose={() => setSelectedInstrument(null)}
        />
      ) : null}
    </div>
  );
}

function PortfolioRow({
  portfolio,
  selected,
  onSelect,
  onEdit,
  onDelete,
}: {
  portfolio: StockV2PortfolioWithHoldings;
  selected: boolean;
  onSelect: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={`grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border p-3 transition ${
        selected ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--surface-soft)] hover:border-[var(--line-strong)]"
      }`}
    >
      <div onClick={onSelect} className="cursor-pointer">
        <div className="flex items-center gap-2">
          <Wallet size={16} className="text-[var(--muted)]" />
          <strong className="text-sm">{portfolio.name}</strong>
          <Pill tone="neutral">{stockV2RiskLabel(portfolio.riskLevel)}</Pill>
          <span className="text-xs text-[var(--muted)]">· {portfolio.holdings?.length || 0} 只持仓</span>
        </div>
        {portfolio.description ? <p className="muted mt-1 text-xs">{portfolio.description}</p> : null}
        <div className="mt-2 flex flex-wrap gap-4 text-xs">
          <span>现金 <strong className="text-[var(--text)]">{formatMoney(portfolio.cash)}</strong></span>
          <span>总市值 <strong className="text-[var(--text)]">{formatMoney(portfolio.totalValue || 0)}</strong></span>
          <span>总资产 <strong className="text-[var(--text)]">{formatMoney(portfolio.totalAssetValue || portfolio.cash)}</strong></span>
          <span>
            单票上限 <strong className="text-[var(--text)]">{portfolio.maxSinglePositionPct}%</strong>
          </span>
          <span>
            最大回撤 <strong className="text-[var(--text)]">{portfolio.maxDrawdownPct}%</strong>
          </span>
        </div>
      </div>
      <div className="flex items-start gap-1">
        <Button onClick={onEdit}>
          <Pencil size={12} />
        </Button>
        <Button tone="danger" onClick={onDelete}>
          <Trash size={12} />
        </Button>
      </div>
    </div>
  );
}

function HoldingRow({
  holding,
  onOpen,
  onBuy,
  onSell,
  onEdit,
  onDelete,
}: {
  holding: StockV2Holding;
  onOpen: () => void;
  onBuy: () => void;
  onSell: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const pnlPct = holding.costPrice && holding.quantity ? (holding.pnl || 0) / (holding.costPrice * holding.quantity) * 100 : 0;
  const pnlTone = (holding.pnl || 0) >= 0 ? "good" : "danger";
  const status = holding.tradableStatus || "unknown";

  return (
    <tr
      className="cursor-pointer border-b border-[var(--line-soft)] last:border-b-0 hover:bg-[var(--surface-soft)]"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      title="点击查看 K 线"
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen();
        }
      }}
    >
      <td className="py-2 pr-4 font-mono text-sm">{holding.symbol}</td>
      <td className="py-2 pr-4 font-medium">{holding.name || "-"}</td>
      <td className="py-2 pr-4">{holding.quantity.toLocaleString()}</td>
      <td className="py-2 pr-4 font-mono">{holding.costPrice?.toFixed(2) || "-"}</td>
      <td className="py-2 pr-4 font-mono">{holding.lastPrice?.toFixed(2) || "-"}</td>
      <td className="py-2 pr-4 text-xs text-[var(--muted)]">{formatDateTime(holding.lastPriceAt)}</td>
      <td className="py-2 pr-4 font-mono">{formatMoney(holding.marketValue || 0)}</td>
      <td className={`py-2 pr-4 font-mono ${pnlTone === "good" ? "text-[var(--good)]" : "text-[var(--danger)]"}`}>
        {formatMoney(holding.pnl || 0)} ({pnlPct >= 0 ? "+" : ""}{pnlPct.toFixed(2)}%)
      </td>
      <td className="py-2 pr-4">{holding.positionPct?.toFixed(2) || "-"}%</td>
      <td className="py-2 pr-4">
        <Pill tone={stockV2ValuationStatusTone(status)}>{stockV2ValuationStatusLabel(status)}</Pill>
      </td>
      <td className="py-2 pr-4">
        <div className="flex gap-1">
          <Button tone="primary" onClick={(e) => {
            e.stopPropagation();
            onBuy();
          }} title="加仓买入">
            <Plus size={12} />
          </Button>
          <Button onClick={(e) => {
            e.stopPropagation();
            onSell();
          }} title="卖出">
            <Minus size={12} />
          </Button>
          <Button onClick={(e) => {
            e.stopPropagation();
            onEdit();
          }} title="编辑">
            <Pencil size={12} />
          </Button>
          <Button tone="danger" onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }} title="删除">
            <Trash size={12} />
          </Button>
        </div>
      </td>
    </tr>
  );
}

function instrumentFromHolding(holding: StockV2Holding): StockV2Instrument {
  return {
    id: `holding-${holding.portfolioId}-${holding.symbol}`,
    symbol: holding.symbol,
    market: holding.market,
    name: holding.name || holding.symbol,
    industry: "",
    sector: "",
    concepts: [],
    listDate: "",
    delistDate: "",
    status: "active",
    lastUpdate: holding.lastPriceAt || "",
    createdAt: "",
    updatedAt: holding.updatedAt || "",
  };
}

function PortfolioValuationSummary({
  portfolio,
  refreshResult,
  snapshot,
}: {
  portfolio: StockV2PortfolioWithHoldings;
  refreshResult: StockV2PortfolioRefreshResult | null;
  snapshot?: StockV2PortfolioSnapshot;
}) {
  const totalAssetValue = snapshot?.totalAssetValue ?? portfolio.totalAssetValue ?? portfolio.cash;
  const holdingMarketValue = snapshot?.holdingMarketValue ?? portfolio.totalValue ?? 0;
  const cashPct = snapshot?.cashPct ?? portfolio.cashPct ?? 0;
  const status = snapshot?.status || "unknown";
  return (
    <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="grid grid-cols-4 gap-3 text-xs max-lg:grid-cols-2">
        <SummaryCell label="总资产" value={formatMoney(totalAssetValue)} />
        <SummaryCell label="持仓市值" value={formatMoney(holdingMarketValue)} />
        <SummaryCell label="现金比例" value={`${cashPct.toFixed(2)}%`} />
        <SummaryCell label="估值时间" value={formatDateTime(snapshot?.valuationAt)} muted />
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
        <Pill tone={stockV2ValuationStatusTone(status)}>{stockV2ValuationStatusLabel(status)}</Pill>
        {refreshResult ? (
          <>
            <span>fresh {refreshResult.refreshedCount}</span>
            <span>stale {refreshResult.staleCount}</span>
            <span>estimated {refreshResult.estimatedCount}</span>
            <span>failed {refreshResult.failedCount}</span>
          </>
        ) : snapshot ? (
          <>
            <span>stale {snapshot.staleQuoteCount}</span>
            <span>estimated {snapshot.estimatedQuoteCount}</span>
            <span>positions {snapshot.positionCount}</span>
          </>
        ) : (
          <span>尚未生成 snapshot</span>
        )}
      </div>
    </div>
  );
}

function SummaryCell({ label, value, muted = false }: { label: string; value: string; muted?: boolean }) {
  return (
    <div>
      <span className="block text-[var(--muted)]">{label}</span>
      <strong className={`mt-1 block font-mono text-sm ${muted ? "font-normal text-[var(--muted-strong)]" : "text-[var(--text)]"}`}>{value || "-"}</strong>
    </div>
  );
}

function SnapshotHistory({ snapshots }: { snapshots: StockV2PortfolioSnapshot[] }) {
  return (
    <div className="mt-4 border-t border-[var(--line)] pt-3">
      <div className="mb-2 text-xs font-medium text-[var(--muted)]">最近 snapshot</div>
      <div className="grid gap-2">
        {snapshots.slice(0, 5).map((snapshot) => (
          <div key={snapshot.id} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
            <span className="font-mono text-[var(--muted-strong)]">{formatDateTime(snapshot.valuationAt)}</span>
            <span className="font-mono">{formatMoney(snapshot.totalAssetValue)}</span>
            <Pill tone={stockV2ValuationStatusTone(snapshot.status)}>{stockV2ValuationStatusLabel(snapshot.status)}</Pill>
          </div>
        ))}
      </div>
    </div>
  );
}

function EmptyPortfolios({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="py-8 text-center">
      <Wallet size={28} className="mx-auto mb-2 text-[var(--muted)]" />
      <p className="text-sm text-[var(--muted)]">还没有投资组合</p>
      <Button tone="primary" className="mt-3" onClick={onAdd}>
        <Plus size={14} className="mr-1.5" />
        创建第一个组合
      </Button>
    </div>
  );
}

function PortfolioDialog({
  mode,
  initial,
  onClose,
  onSubmit,
}: {
  mode: "create" | "edit";
  initial?: StockV2Portfolio;
  onClose: () => void;
  onSubmit: (data: Partial<StockV2Portfolio>) => Promise<void>;
}) {
  const [form, setForm] = useState({
    name: initial?.name || "",
    description: initial?.description || "",
    cash: initial?.cash ?? 0,
    riskLevel: initial?.riskLevel || "medium",
    maxSinglePositionPct: initial?.maxSinglePositionPct ?? 20,
    maxDrawdownPct: initial?.maxDrawdownPct ?? 30,
    notes: initial?.notes || "",
  });

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <Dialog title={mode === "create" ? "新建投资组合" : "编辑投资组合"} onClose={onClose}>
      <div className="grid gap-3">
        <Field label="组合名称">
          <input
            type="text"
            value={form.name}
            placeholder="例如：稳健型组合"
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label="描述">
          <textarea
            rows={2}
            value={form.description}
            placeholder="简单描述这个组合的策略"
            onChange={(e) => setForm({ ...form, description: e.target.value })}
          />
        </Field>
        <Field label="持有现金 (¥)">
          <input
            type="number"
            value={form.cash}
            min={0}
            step="0.01"
            placeholder="例如：100000"
            onChange={(e) => setForm({ ...form, cash: Number(e.target.value) })}
          />
        </Field>

        <div className="grid grid-cols-3 gap-3">
          <Field label="风险等级">
            <select
              value={form.riskLevel}
              onChange={(e) => setForm({ ...form, riskLevel: e.target.value })}
            >
              <option value="low">保守</option>
              <option value="medium">稳健</option>
              <option value="high">激进</option>
            </select>
          </Field>
          <Field label="单票上限 (%)">
            <input
              type="number"
              value={form.maxSinglePositionPct}
              min={1}
              max={100}
              step={1}
              placeholder="例如：20"
              onChange={(e) => setForm({ ...form, maxSinglePositionPct: Number(e.target.value) })}
            />
          </Field>
          <Field label="最大回撤 (%)">
            <input
              type="number"
              value={form.maxDrawdownPct}
              min={1}
              max={100}
              step={1}
              placeholder="例如：30"
              onChange={(e) => setForm({ ...form, maxDrawdownPct: Number(e.target.value) })}
            />
          </Field>
        </div>

        <Field label="备注">
          <textarea
            rows={2}
            value={form.notes}
            placeholder="记录资金来源、交易限制或需要记住的组合约束"
            onChange={(e) => setForm({ ...form, notes: e.target.value })}
          />
        </Field>
      </div>

      <DialogActions onClose={onClose} onSubmit={() => void onSubmit(form)} submitLabel={mode === "create" ? "创建" : "保存"} />
    </Dialog>
  );
}

function HoldingDialog({
  mode,
  initial,
  actions,
  onClose,
  onSubmit,
}: {
  mode: "add" | "edit";
  initial?: StockV2Holding;
  actions: AppActions;
  onClose: () => void;
  onSubmit: (data: Partial<StockV2Holding>) => Promise<void>;
  }) {
  const [form, setForm] = useState({
    symbol: initial?.symbol || "",
    name: initial?.name || "",
    quantity: initial?.quantity ? String(initial.quantity) : "",
    costPrice: initial?.costPrice ? String(initial.costPrice) : "",
    market: initial?.market || "SH",
    acquiredAt: initial?.acquiredAt ? toLocalDateTimeLocal(new Date(initial.acquiredAt)) : toLocalDateTimeLocal(new Date()),
  });
  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<StockV2Instrument[]>([]);
  const [searching, setSearching] = useState(false);
  const [showDropdown, setShowDropdown] = useState(false);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        if (showDropdown) {
          setShowDropdown(false);
        } else {
          onClose();
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, showDropdown]);

  // 点击外部关闭下拉
  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowDropdown(false);
      }
    }
    if (showDropdown) {
      document.addEventListener("mousedown", onClick);
    }
    return () => document.removeEventListener("mousedown", onClick);
  }, [showDropdown]);

  // 防抖搜索
  useEffect(() => {
    if (mode === "edit") return;
    if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    if (!searchQuery || searchQuery.length < 1) {
      setSearchResults([]);
      return;
    }

    searchTimerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const data = await actions.api<{ items: StockV2Instrument[] }>(
          `/api/stockv2/instruments/search?q=${encodeURIComponent(searchQuery)}&limit=20`
        );
        setSearchResults(data.items || []);
      } catch {
        setSearchResults([]);
      } finally {
        setSearching(false);
      }
    }, 200);

    return () => {
      if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    };
  }, [searchQuery, actions, mode]);

  function selectStock(inst: StockV2Instrument) {
    setForm({
      ...form,
      symbol: inst.symbol,
      name: inst.name || "",
      market: inst.market,
    });
    setSearchQuery(`${inst.symbol} · ${inst.name || ""}`);
    setShowDropdown(false);
  }

  return (
    <Dialog title={mode === "add" ? "添加持仓" : "编辑持仓"} onClose={onClose}>
      <div className="grid gap-3">
        <Field label="股票代码 / 名称">
          <div className="relative" ref={dropdownRef}>
            <div className="relative">
              <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
              <input
                type="text"
                value={mode === "edit" ? form.symbol : searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  setShowDropdown(true);
                }}
                onFocus={() => {
                  if (searchQuery && searchResults.length > 0) setShowDropdown(true);
                }}
                placeholder="输入代码或名称搜索..."
                disabled={mode === "edit"}
                className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:outline-none focus:border-[var(--accent)]"
              />
            </div>
            {showDropdown && mode === "add" ? (
              <div className="absolute left-0 right-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]">
                {searching ? (
                  <div className="px-3 py-2 text-xs text-[var(--muted)]">搜索中...</div>
                ) : searchResults.length === 0 ? (
                  <div className="px-3 py-2 text-xs text-[var(--muted)]">
                    {searchQuery ? "未找到匹配的股票" : "输入关键词开始搜索"}
                  </div>
                ) : (
                  searchResults.map((inst) => (
                    <button
                      key={inst.id}
                      type="button"
                      onClick={() => selectStock(inst)}
                      className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-[var(--surface-soft)]"
                    >
                      <span className="font-mono">{inst.symbol}</span>
                      <span className="text-[var(--muted)]">{inst.name}</span>
                      <Pill tone="neutral" className="text-xs">
                        {inst.market === "SH" ? "沪市" : inst.market === "SZ" ? "深市" : "北市"}
                      </Pill>
                    </button>
                  ))
                )}
              </div>
            ) : null}
          </div>
        </Field>

        <Field label="股票名称">
          <input
            type="text"
            value={form.name}
            placeholder="选中股票后自动填入，也可手动修正"
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="市场">
            <select
              value={form.market}
              onChange={(e) => setForm({ ...form, market: e.target.value })}
            >
              <option value="SH">沪市</option>
              <option value="SZ">深市</option>
              <option value="BJ">北市</option>
            </select>
          </Field>
          <Field label="数量 (股)">
            <input
              type="number"
              value={form.quantity}
              placeholder="请输入数量"
              onChange={(e) => setForm({ ...form, quantity: e.target.value })}
            />
          </Field>
        </div>

        <Field label="成本价 (¥)">
          <input
            type="number"
            step="0.01"
            value={form.costPrice}
            placeholder="请输入成本价"
            onChange={(e) => setForm({ ...form, costPrice: e.target.value })}
          />
        </Field>

        <Field label="建仓时间" help="初始建仓的时间,用于资产曲线回算,默认现在">
          <input
            type="datetime-local"
            value={form.acquiredAt}
            onChange={(e) => setForm({ ...form, acquiredAt: e.target.value })}
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 px-3 text-sm text-[var(--text)] focus:outline-none focus:border-[var(--accent)]"
          />
        </Field>
      </div>

      <DialogActions
        onClose={onClose}
        onSubmit={() => {
          const payload: Partial<StockV2Holding> & { acquiredAt?: string } = {
            ...form,
            quantity: Number(form.quantity) || 0,
            costPrice: Number(form.costPrice) || 0,
            acquiredAt: form.acquiredAt ? new Date(form.acquiredAt).toISOString() : "",
          };
          void onSubmit(payload);
        }}
        submitLabel={mode === "add" ? "添加" : "保存"}
      />
    </Dialog>
  );
}

// ---- 通用对话框 ----
function Dialog({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-lg rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--line)] px-5 py-3">
          <h3 className="m-0 text-base font-semibold">{title}</h3>
          <Button onClick={onClose}>
            <X size={14} />
          </Button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  );
}

function DialogActions({ onClose, onSubmit, submitLabel = "保存" }: { onClose: () => void; onSubmit: () => void; submitLabel?: string }) {
  return (
    <div className="mt-5 flex justify-end gap-2 border-t border-[var(--line)] pt-4">
      <Button onClick={onClose}>取消</Button>
      <Button tone="primary" onClick={onSubmit}>
        <Check size={14} className="mr-1.5" />
        {submitLabel}
      </Button>
    </div>
  );
}

// ---- 交易流水表格 ----
function TransactionsTable({ transactions }: { transactions: StockV2Transaction[] }) {
  if (transactions.length === 0) {
    return <EmptyState title="暂无交易流水" body="买入或卖出后会在这里记录每一笔交易。" />;
  }
  return (
    <div className="mt-3 overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-[var(--line)] text-left text-xs text-[var(--muted)]">
            <th className="py-2 pr-4 font-medium">时间</th>
            <th className="py-2 pr-4 font-medium">方向</th>
            <th className="py-2 pr-4 font-medium">代码</th>
            <th className="py-2 pr-4 font-medium">名称</th>
            <th className="py-2 pr-4 font-medium">数量</th>
            <th className="py-2 pr-4 font-medium">价格</th>
            <th className="py-2 pr-4 font-medium">金额</th>
            <th className="py-2 pr-4 font-medium">备注</th>
          </tr>
        </thead>
        <tbody>
          {transactions.map((t) => (
            <tr key={t.id} className="border-b border-[var(--line-soft)] last:border-b-0">
              <td className="py-2 pr-4 text-xs text-[var(--muted)]">{formatDateTime(t.executedAt)}</td>
              <td className="py-2 pr-4">
                <Pill tone={t.side === "buy" ? "danger" : "good"}>
                  {t.side === "buy" ? "买入" : "卖出"}
                </Pill>
              </td>
              <td className="py-2 pr-4 font-mono">{t.symbol}</td>
              <td className="py-2 pr-4">{t.name || "-"}</td>
              <td className="py-2 pr-4">{t.quantity.toLocaleString()}</td>
              <td className="py-2 pr-4 font-mono">{t.price.toFixed(2)}</td>
              <td className="py-2 pr-4 font-mono">{formatMoney(t.amount)}</td>
              <td className="py-2 pr-4 text-xs text-[var(--muted)]">{t.note || "-"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// ---- 资产变化图 ----
function StockV2PortfolioAssetChart({ curve }: { curve: StockV2AssetCurveResponse | null }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Area"> | null>(null);

  useEffect(() => {
    if (!hostRef.current || !curve || curve.points.length === 0) return;

    // 每次数据变更重建图表(简化);后续可改为 series.setData 增量
    chartRef.current?.remove();
    chartRef.current = null;
    seriesRef.current = null;

    const host = hostRef.current;
    const css = getComputedStyle(host);
    const surface = css.getPropertyValue("--surface").trim() || "#0f1115";
    const line = css.getPropertyValue("--line").trim() || "#2a2f36";
    const text = css.getPropertyValue("--text").trim() || "#e6e8eb";
    const accent = css.getPropertyValue("--accent").trim() || "#3b82f6";

    const rect = host.getBoundingClientRect();
    const chart = createChart(host, {
      width: Math.max(1, Math.floor(rect.width || host.clientWidth || 640)),
      height: Math.max(1, Math.floor(rect.height || host.clientHeight || 360)),
      layout: {
        background: { color: surface },
        textColor: text,
        fontFamily: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', sans-serif",
      },
      grid: {
        vertLines: { color: line, style: 3 },
        horzLines: { color: line, style: 3 },
      },
      crosshair: { mode: 1 as CrosshairMode.Normal, vertLine: { color: line }, horzLine: { color: line } },
      rightPriceScale: { borderColor: line, scaleMargins: { top: 0.1, bottom: 0.1 } },
      timeScale: {
        borderColor: line,
        timeVisible: false,
        secondsVisible: false,
        minBarSpacing: 2,
      },
      handleScroll: { mouseWheel: true, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: true },
      handleScale: { axisPressedMouseMove: true, mouseWheel: true, pinch: true },
    });

    const series = chart.addSeries(AreaSeries, {
      lineColor: accent,
      topColor: accent + "33",
      bottomColor: accent + "00",
      lineWidth: 2,
      priceFormat: { type: "price", precision: 2, minMove: 0.01 },
    });

    series.setData(curve.points.map((p) => ({ time: p.date as Time, value: p.total })));

    // markers:买点箭头向上(绿) belowBar,卖点箭头向下(红) aboveBar
    const markers = curve.markers.map((m) => ({
      time: m.date as Time,
      position: m.side === "buy" ? ("belowBar" as const) : ("aboveBar" as const),
      color: m.side === "buy" ? "#12844f" : "#cf1f32",
      shape: m.side === "buy" ? ("arrowUp" as const) : ("arrowDown" as const),
      text: `${m.side === "buy" ? "买" : "卖"} ${m.symbol} ${m.quantity}@${m.price.toFixed(2)}`,
    }));
    createSeriesMarkers(series, markers);

    chart.timeScale().fitContent();

    const ro = new ResizeObserver(() => {
      if (hostRef.current && chartRef.current) {
        const r = hostRef.current.getBoundingClientRect();
        if (r.width > 0 && r.height > 0) {
          chartRef.current.resize(Math.floor(r.width), Math.floor(r.height), true);
        }
      }
    });
    ro.observe(host);

    chartRef.current = chart;
    seriesRef.current = series;

    return () => {
      ro.disconnect();
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
    };
  }, [curve]);

  if (!curve || curve.points.length === 0) {
    return (
      <div className="h-[360px] w-full">
        <EmptyState title="暂无资产曲线" body="记录第一笔交易后即可看到资产变化图。" />
      </div>
    );
  }
  return <div ref={hostRef} className="h-[360px] w-full" />;
}

// ---- 交易弹窗(买入/卖出) ----
function TradeDialog({
  mode,
  portfolioId: _portfolioId,
  holding,
  actions,
  cash,
  onClose,
  onSubmit,
}: {
  mode: "buy" | "sell";
  portfolioId: string;
  holding?: StockV2Holding;
  actions: AppActions;
  cash: number;
  onClose: () => void;
  onSubmit: (data: { symbol: string; name: string; market: string; side: "buy" | "sell"; quantity: number; price: number; executedAt: string; note: string }) => Promise<void>;
}) {
  const [form, setForm] = useState({
    symbol: holding?.symbol || "",
    name: holding?.name || "",
    market: holding?.market || "SH",
    quantity: "",
    price: holding?.lastPrice ? String(holding.lastPrice) : "",
    executedAt: toLocalDateTimeLocal(new Date()),
    note: "",
  });
  const [searchQuery, setSearchQuery] = useState(holding ? `${holding.symbol} · ${holding.name || ""}` : "");
  const [searchResults, setSearchResults] = useState<StockV2Instrument[]>([]);
  const [searching, setSearching] = useState(false);
  const [showDropdown, setShowDropdown] = useState(false);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const hasHolding = !!holding;
  const canSearch = !hasHolding;
  const maxQty = mode === "sell" ? (holding?.availableQuantity || 0) : Infinity;
  const amount = Number(form.quantity) * Number(form.price) || 0;
  const willNegativeCash = mode === "buy" && amount > cash;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        if (showDropdown) {
          setShowDropdown(false);
        } else {
          onClose();
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, showDropdown]);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowDropdown(false);
      }
    }
    if (showDropdown) {
      document.addEventListener("mousedown", onClick);
    }
    return () => document.removeEventListener("mousedown", onClick);
  }, [showDropdown]);

  useEffect(() => {
    if (!canSearch) return;
    if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    if (!searchQuery || searchQuery.length < 1) {
      setSearchResults([]);
      return;
    }
    searchTimerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const data = await actions.api<{ items: StockV2Instrument[] }>(
          `/api/stockv2/instruments/search?q=${encodeURIComponent(searchQuery)}&limit=20`
        );
        setSearchResults(data.items || []);
      } catch {
        setSearchResults([]);
      } finally {
        setSearching(false);
      }
    }, 200);
    return () => {
      if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    };
  }, [searchQuery, actions, canSearch]);

  function selectStock(inst: StockV2Instrument) {
    setForm({ ...form, symbol: inst.symbol, name: inst.name || "", market: inst.market });
    setSearchQuery(`${inst.symbol} · ${inst.name || ""}`);
    setShowDropdown(false);
  }

  function handleSubmit() {
    const qty = Number(form.quantity) || 0;
    const price = Number(form.price) || 0;
    if (!form.symbol || qty <= 0 || price <= 0) return;
    if (mode === "sell" && qty > maxQty + 1e-9) return;
    if (!form.executedAt) return;
    void onSubmit({
      symbol: form.symbol,
      name: form.name,
      market: form.market,
      side: mode,
      quantity: qty,
      price,
      executedAt: new Date(form.executedAt).toISOString(),
      note: form.note,
    });
  }

  return (
    <Dialog title={mode === "buy" ? "买入" : "卖出"} onClose={onClose}>
      <div className="grid gap-3">
        <Field label="股票代码 / 名称">
          <div className="relative" ref={dropdownRef}>
            <div className="relative">
              <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
              <input
                type="text"
                value={canSearch ? searchQuery : form.symbol}
                onChange={(e) => {
                  if (!canSearch) return;
                  setSearchQuery(e.target.value);
                  setShowDropdown(true);
                }}
                onFocus={() => {
                  if (canSearch && searchQuery && searchResults.length > 0) setShowDropdown(true);
                }}
                placeholder="输入代码或名称搜索..."
                disabled={!canSearch}
                className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:outline-none focus:border-[var(--accent)] disabled:bg-[var(--surface-soft)]"
              />
            </div>
            {showDropdown && canSearch ? (
              <div className="absolute left-0 right-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]">
                {searching ? (
                  <div className="px-3 py-2 text-xs text-[var(--muted)]">搜索中...</div>
                ) : searchResults.length === 0 ? (
                  <div className="px-3 py-2 text-xs text-[var(--muted)]">
                    {searchQuery ? "未找到匹配的股票" : "输入关键词开始搜索"}
                  </div>
                ) : (
                  searchResults.map((inst) => (
                    <button
                      key={inst.id}
                      type="button"
                      onClick={() => selectStock(inst)}
                      className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-[var(--surface-soft)]"
                    >
                      <span className="font-mono">{inst.symbol}</span>
                      <span className="text-[var(--muted)]">{inst.name}</span>
                      <Pill tone="neutral" className="text-xs">
                        {inst.market === "SH" ? "沪市" : inst.market === "SZ" ? "深市" : "北市"}
                      </Pill>
                    </button>
                  ))
                )}
              </div>
            ) : null}
          </div>
        </Field>

        <Field label="股票名称">
          <input
            type="text"
            value={form.name}
            placeholder="选中股票后自动填入,也可手动修正"
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="市场">
            <select
              value={form.market}
              onChange={(e) => setForm({ ...form, market: e.target.value })}
              disabled={hasHolding}
              className={hasHolding ? "opacity-60" : ""}
            >
              <option value="SH">沪市</option>
              <option value="SZ">深市</option>
              <option value="BJ">北市</option>
            </select>
          </Field>
          <Field label="数量 (股)">
            <input
              type="number"
              value={form.quantity}
              placeholder="请输入数量"
              onChange={(e) => setForm({ ...form, quantity: e.target.value })}
            />
            {mode === "sell" ? (
              <div className="mt-1 flex items-center justify-between text-xs text-[var(--muted)]">
                <span>可卖 {maxQty.toLocaleString()}</span>
                <button
                  type="button"
                  className="text-[var(--accent)] hover:underline"
                  onClick={() => setForm({ ...form, quantity: String(maxQty) })}
                >
                  全部
                </button>
              </div>
            ) : null}
          </Field>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <Field label={mode === "buy" ? "买入价 (¥)" : "卖出价 (¥)"} help="默认填最新价,可自行修改">
            <input
              type="number"
              step="0.01"
              value={form.price}
              placeholder="请输入价格"
              onChange={(e) => setForm({ ...form, price: e.target.value })}
            />
          </Field>
          <Field label="成交金额">
            <div className="font-mono text-sm">{formatMoney(amount)}</div>
          </Field>
        </div>

        <Field label="成交时间" help="可填过去某次实际成交时间,默认现在">
          <input
            type="datetime-local"
            value={form.executedAt}
            onChange={(e) => setForm({ ...form, executedAt: e.target.value })}
            className="w-full"
          />
        </Field>

        <Field label="备注">
          <input
            type="text"
            value={form.note}
            placeholder="例如:建仓/加仓/止盈"
            onChange={(e) => setForm({ ...form, note: e.target.value })}
          />
        </Field>

        {willNegativeCash ? (
          <Pill tone="warn">买入金额超过当前现金,组合现金将变为负(事后补录或后续注资)</Pill>
        ) : null}
      </div>

      <DialogActions onClose={onClose} onSubmit={handleSubmit} submitLabel={mode === "buy" ? "确认买入" : "确认卖出"} />
    </Dialog>
  );
}

function toLocalDateTimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatMoney(value: number): string {
  if (!value) return "¥0";
  if (Math.abs(value) >= 100000000) {
    return `¥${(value / 100000000).toFixed(2)} 亿`;
  }
  if (Math.abs(value) >= 10000) {
    return `¥${(value / 10000).toFixed(2)} 万`;
  }
  return `¥${value.toFixed(2)}`;
}

function formatDateTime(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return "-";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function openStockV2MonitorTab() {
  const url = new URL(window.location.href);
  url.searchParams.set("tab", "stockv2");
  url.searchParams.set("stockv2", "dailyBars");
  const href = `${url.pathname}${url.search}${url.hash}`;
  window.history.pushState(null, "", href);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
