import { ArrowClockwise, Plus, Trash, Pencil, Wallet, X, Check, MagnifyingGlass } from "@phosphor-icons/react";
import { useEffect, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Holding, StockV2Instrument, StockV2Portfolio, StockV2PortfolioRefreshResult, StockV2PortfolioSnapshot, StockV2PortfolioWithHoldings } from "../../app/types";
import { Button, Field, Panel, Pill } from "../../components/ui";
import { stockV2RiskLabel, stockV2ValuationStatusLabel, stockV2ValuationStatusTone } from "../../domain/labels";

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;

export function StockV2Portfolios({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const portfolios = data.stockv2.portfolios || [];
  const [editingId, setEditingId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(portfolios[0]?.id || null);
  const [holdingsDialog, setHoldingsDialog] = useState<{ portfolioId: string; mode: "add" | "edit"; holding?: StockV2Holding } | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshResult, setRefreshResult] = useState<StockV2PortfolioRefreshResult | null>(null);
  const [snapshots, setSnapshots] = useState<StockV2PortfolioSnapshot[]>([]);

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
    if (!selectedId) return;
    let cancelled = false;
    actions.api<{ items: StockV2PortfolioSnapshot[] }>(`/api/stockv2/portfolios/${selectedId}/snapshots?limit=5`)
      .then((data) => {
        if (!cancelled) setSnapshots(data.items || []);
      })
      .catch(() => {
        if (!cancelled) setSnapshots([]);
      });
    return () => {
      cancelled = true;
    };
  }, [actions, selectedId]);

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
          title="持仓明细"
          subtitle={`${selected.name} · ${displayedHoldings.length} 只持仓`}
          actions={
            <>
              <Button onClick={() => void refreshSelectedPortfolio()} disabled={refreshing}>
                <ArrowClockwise size={14} className="mr-1.5" />
                {refreshing ? "刷新中" : "刷新资产"}
              </Button>
              <Button tone="primary" onClick={() => setHoldingsDialog({ portfolioId: selected.id, mode: "add" })}>
                <Plus size={14} className="mr-1.5" />
                添加持仓
              </Button>
            </>
          }
        >
          <PortfolioValuationSummary portfolio={selected} refreshResult={selectedRefreshResult} snapshot={latestSnapshot} />

          {displayedHoldings.length === 0 ? (
            <p className="text-sm text-[var(--muted)]">暂无持仓，点击右上角添加。</p>
          ) : (
            <div className="mt-4 overflow-x-auto">
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
                    <th className="py-2 pr-4 font-medium">状态</th>
                    <th className="py-2 pr-4 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {displayedHoldings.map((h) => (
                    <HoldingRow
                      key={h.id}
                      holding={h}
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

function HoldingRow({ holding, onEdit, onDelete }: { holding: StockV2Holding; onEdit: () => void; onDelete: () => void }) {
  const pnlPct = holding.costPrice && holding.quantity ? (holding.pnl || 0) / (holding.costPrice * holding.quantity) * 100 : 0;
  const pnlTone = (holding.pnl || 0) >= 0 ? "good" : "danger";
  const status = holding.tradableStatus || "unknown";

  return (
    <tr className="border-b border-[var(--line-soft)] last:border-b-0 hover:bg-[var(--surface-soft)]">
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
          <Button onClick={onEdit}>
            <Pencil size={12} />
          </Button>
          <Button tone="danger" onClick={onDelete}>
            <Trash size={12} />
          </Button>
        </div>
      </td>
    </tr>
  );
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
              onChange={(e) => setForm({ ...form, maxSinglePositionPct: Number(e.target.value) })}
            />
          </Field>
          <Field label="最大回撤 (%)">
            <input
              type="number"
              value={form.maxDrawdownPct}
              onChange={(e) => setForm({ ...form, maxDrawdownPct: Number(e.target.value) })}
            />
          </Field>
        </div>

        <Field label="备注">
          <textarea
            rows={2}
            value={form.notes}
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
      </div>

      <DialogActions
        onClose={onClose}
        onSubmit={() => {
          const payload: Partial<StockV2Holding> = {
            ...form,
            quantity: Number(form.quantity) || 0,
            costPrice: Number(form.costPrice) || 0,
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
