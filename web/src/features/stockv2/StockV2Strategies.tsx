import { Archive, ArrowsClockwise, MagnifyingGlass, Pause, Pencil, Play, Plus, Sparkle } from "@phosphor-icons/react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { AppActions } from "../../app/App";
import type {
  AppData,
  StockV2Instrument,
  StockV2Portfolio,
  StockV2Strategy,
  StockV2StrategyInput,
  StockV2StrategyKind,
  StockV2StrategyListResponse,
  StockV2StrategyVersion,
  StockV2StrategyVersionListResponse,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, ContextList, Drawer, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
import {
  formatDate,
  stockV2StrategyDirectionLabel,
  stockV2StrategyKindLabel,
  stockV2StrategyScopeLabel,
  stockV2StrategySourceLabel,
  stockV2StrategyStatusLabel,
  stockV2StrategyStatusTone,
  stockV2StrategyVersionStatusLabel,
  stockV2StrategyVersionStatusTone,
} from "../../domain/labels";

// 策略是长期判断依据,与 Watch(何时检查)、Review(当次判断)分离。
// 本页只做 Strategy 对象的 CRUD + 版本展示;不接 Agent、不接 Watch、不接 Review。
// 后端策略 API 尚在落地中,任何 404/异常都不能让页面崩溃,只给轻量错误。

type DrawerState =
  | { type: "closed" }
  | { type: "create"; initialKind?: StockV2StrategyKind }
  | { type: "detail"; id: string }
  | { type: "edit"; strategy: StockV2Strategy };

interface SymbolRef {
  symbol: string;
  market?: string;
  name?: string;
}

interface PriceForm {
  entryPriceLow: string;
  entryPriceHigh: string;
  triggerPriceAbove: string;
  triggerPriceBelow: string;
  stopLoss: string;
  takeProfit: string;
}

const EMPTY_PRICE_FORM: PriceForm = {
  entryPriceLow: "",
  entryPriceHigh: "",
  triggerPriceAbove: "",
  triggerPriceBelow: "",
  stopLoss: "",
  takeProfit: "",
};

export function StockV2Strategies({ actions, data }: { actions: AppActions; data: AppData }) {
  const portfolios = data.stockv2.portfolios || [];

  const [strategies, setStrategies] = useState<StockV2Strategy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [kindFilter, setKindFilter] = useState<string>("all");
  const [portfolioFilter, setPortfolioFilter] = useState<string>("all");
  const [keyword, setKeyword] = useState("");

  const [drawer, setDrawer] = useState<DrawerState>({ type: "closed" });
  const [versions, setVersions] = useState<StockV2StrategyVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  async function fetchStrategies() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2StrategyListResponse>("/api/stockv2/strategies");
      setStrategies(res.items || []);
    } catch (err) {
      // API 未就绪或失败:保留轻量错误,不抛崩溃。列表降级为空。
      setError(friendlyError(err));
      setStrategies([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void fetchStrategies();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [actions]);

  // 详情 drawer 打开时拉取版本历史;失败静默(版本历史是辅助信息)。
  useEffect(() => {
    if (drawer.type !== "detail") {
      setVersions([]);
      return;
    }
    let cancelled = false;
    setVersionsLoading(true);
    actions.api<StockV2StrategyVersionListResponse>(`/api/stockv2/strategies/${drawer.id}/versions`)
      .then((res) => {
        if (!cancelled) setVersions(res.items || []);
      })
      .catch(() => {
        if (!cancelled) setVersions([]);
      })
      .finally(() => {
        if (!cancelled) setVersionsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [actions, drawer]);

  const filtered = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return strategies.filter((s) => {
      if (statusFilter !== "all" && s.status !== statusFilter) return false;
      if (kindFilter !== "all" && s.kind !== kindFilter) return false;
      if (portfolioFilter !== "all" && s.portfolioId !== portfolioFilter) return false;
      if (!q) return true;
      return (
        s.name?.toLowerCase().includes(q) ||
        s.symbol?.toLowerCase().includes(q) ||
        s.instrumentName?.toLowerCase().includes(q) ||
        false
      );
    });
  }, [strategies, statusFilter, kindFilter, portfolioFilter, keyword]);

  const detailStrategy =
    drawer.type === "detail" ? strategies.find((s) => s.id === drawer.id) || null : null;

  // 统一的操作执行器:成功后刷新列表与详情版本,失败给 toast。返回是否成功,
  // 调用方据此决定是否关闭 drawer(失败时保留 drawer 方便用户重试)。
  async function runStrategyAction(label: string, fn: () => Promise<unknown>): Promise<boolean> {
    setSubmitting(true);
    try {
      await fn();
      actions.setToast(label, "good");
      await fetchStrategies();
      return true;
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
      return false;
    } finally {
      setSubmitting(false);
    }
  }

  async function createSymbolStrategy(input: StockV2StrategyInput) {
    await actions.api("/api/stockv2/strategies", { method: "POST", body: { ...input, kind: "symbol_strategy" } });
  }

  async function createPortfolioMonitor(portfolioId: string, input: StockV2StrategyInput) {
    await actions.api(`/api/stockv2/portfolios/${portfolioId}/monitor-strategy`, {
      method: "POST",
      body: { name: input.name, riskNotes: input.riskNotes },
    });
  }

  async function updateStrategy(id: string, input: StockV2StrategyInput) {
    await actions.api(`/api/stockv2/strategies/${id}`, { method: "PUT", body: input });
  }

  async function changeStatus(strategy: StockV2Strategy, action: "activate" | "pause" | "archive") {
    await actions.api(`/api/stockv2/strategies/${strategy.id}/${action}`, { method: "POST" });
  }

  const hasAny = strategies.length > 0;

  return (
    <div className="grid gap-4">
      <Panel
        title="策略"
        subtitle={`${strategies.length} 个 · 长期判断依据,编辑生效策略会生成新版本`}
        actions={
          <>
            <Button onClick={() => void fetchStrategies()} disabled={loading}>
              <ArrowsClockwise size={14} className="mr-1.5" />
              {loading ? "加载中" : "刷新"}
            </Button>
            <Button tone="primary" onClick={() => setDrawer({ type: "create" })}>
              <Plus size={14} className="mr-1.5" />
              新建策略
            </Button>
          </>
        }
      >
        {error ? (
          <div className="mb-3">
            <Notice tone="warn">策略接口暂不可用:{error}</Notice>
          </div>
        ) : null}

        {hasAny ? (
          <>
            <StrategyToolbar
              statusFilter={statusFilter}
              kindFilter={kindFilter}
              portfolioFilter={portfolioFilter}
              keyword={keyword}
              portfolios={portfolios}
              onStatus={setStatusFilter}
              onKind={setKindFilter}
              onPortfolio={setPortfolioFilter}
              onKeyword={setKeyword}
            />

            {filtered.length === 0 ? (
              <p className="py-6 text-center text-sm text-[var(--muted)]">没有匹配的策略,调整筛选条件试试。</p>
            ) : (
              <div className="mt-3 grid gap-2">
                {filtered.map((s) => (
                  <StrategyRow
                    key={s.id}
                    strategy={s}
                    portfolios={portfolios}
                    onSelect={() => setDrawer({ type: "detail", id: s.id })}
                    onEdit={() => setDrawer({ type: "edit", strategy: s })}
                  />
                ))}
              </div>
            )}
          </>
        ) : loading ? (
          <p className="py-6 text-center text-sm text-[var(--muted)]">加载策略…</p>
        ) : (
          <StrategyEmptyState
            hasPortfolio={portfolios.length > 0}
            onCreateSymbol={() => setDrawer({ type: "create" })}
            onCreateMonitor={() => setDrawer({ type: "create", initialKind: "portfolio_monitor" })}
          />
        )}

        {/* Agent 生成策略尚未落地:入口可见但禁用,保留语义占位。 */}
        <div className="mt-4 border-t border-[var(--line)] pt-3">
          <Button disabled title="Agent 生成策略将在后续版本提供">
            <Sparkle size={14} className="mr-1.5" />
            Agent 生成策略(即将支持)
          </Button>
        </div>
      </Panel>

      {drawer.type === "create" ? (
        <CreateStrategyDrawer
          initialKind={drawer.initialKind}
          portfolios={portfolios}
          actions={actions}
          onClose={() => setDrawer({ type: "closed" })}
          onSubmitSymbol={async (input) => {
            const ok = await runStrategyAction("创建策略", () => createSymbolStrategy(input));
            if (ok) setDrawer({ type: "closed" });
          }}
          onSubmitMonitor={async (portfolioId, input) => {
            const ok = await runStrategyAction("创建组合监控策略", () => createPortfolioMonitor(portfolioId, input));
            if (ok) setDrawer({ type: "closed" });
          }}
          submitting={submitting}
        />
      ) : null}

      {drawer.type === "edit" ? (
        <EditStrategyDrawer
          strategy={drawer.strategy}
          portfolios={portfolios}
          actions={actions}
          onClose={() => setDrawer({ type: "closed" })}
          onSubmit={async (input) => {
            const ok = await runStrategyAction("保存策略(新版本)", () => updateStrategy(drawer.strategy.id, input));
            if (ok) setDrawer({ type: "closed" });
          }}
          submitting={submitting}
        />
      ) : null}

      {drawer.type === "detail" && detailStrategy ? (
        <StrategyDetailDrawer
          strategy={detailStrategy}
          portfolios={portfolios}
          versions={versions}
          versionsLoading={versionsLoading}
          submitting={submitting}
          onClose={() => setDrawer({ type: "closed" })}
          onEdit={() => setDrawer({ type: "edit", strategy: detailStrategy })}
          onActivate={() => void runStrategyAction("启用策略", () => changeStatus(detailStrategy, "activate"))}
          onPause={async () => {
            const ok = await confirmDanger({
              title: "暂停策略",
              body: "暂停后该策略不会被后续 Watch 触发,可随时重新启用。",
              objectName: detailStrategy.name,
              confirmLabel: "暂停",
            });
            if (ok) await runStrategyAction("暂停策略", () => changeStatus(detailStrategy, "pause"));
          }}
          onArchive={async () => {
            const ok = await confirmDanger({
              title: "归档策略",
              body: "归档后策略变为只读,不再参与任何 Watch/Review。归档不删除版本历史,仍可回看。",
              objectName: detailStrategy.name,
              impact: ["相关 Watch(若存在)将不再被该策略触发", "策略进入只读,需重新创建才能恢复使用"],
              confirmLabel: "归档",
            });
            if (ok) {
              const done = await runStrategyAction("归档策略", () => changeStatus(detailStrategy, "archive"));
              if (done) setDrawer({ type: "closed" });
            }
          }}
        />
      ) : null}

      {dangerConfirmDialog}
    </div>
  );
}

// ============================ 列表 ============================

function StrategyToolbar({
  statusFilter,
  kindFilter,
  portfolioFilter,
  keyword,
  portfolios,
  onStatus,
  onKind,
  onPortfolio,
  onKeyword,
}: {
  statusFilter: string;
  kindFilter: string;
  portfolioFilter: string;
  keyword: string;
  portfolios: StockV2Portfolio[];
  onStatus: (v: string) => void;
  onKind: (v: string) => void;
  onPortfolio: (v: string) => void;
  onKeyword: (v: string) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      <label className="field">
        <span>状态</span>
        <select value={statusFilter} onChange={(e) => onStatus(e.target.value)}>
          <option value="all">全部状态</option>
          <option value="draft">草稿</option>
          <option value="active">生效中</option>
          <option value="paused">已暂停</option>
          <option value="archived">已归档</option>
        </select>
      </label>
      <label className="field">
        <span>类型</span>
        <select value={kindFilter} onChange={(e) => onKind(e.target.value)}>
          <option value="all">全部类型</option>
          <option value="symbol_strategy">单票策略</option>
          <option value="portfolio_monitor">组合监控</option>
        </select>
      </label>
      <label className="field">
        <span>组合</span>
        <select value={portfolioFilter} onChange={(e) => onPortfolio(e.target.value)}>
          <option value="all">全部组合</option>
          {portfolios.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span>关键词</span>
        <input
          type="text"
          value={keyword}
          placeholder="名称 / 代码"
          onChange={(e) => onKeyword(e.target.value)}
        />
      </label>
    </div>
  );
}

function StrategyRow({
  strategy,
  portfolios,
  onSelect,
  onEdit,
}: {
  strategy: StockV2Strategy;
  portfolios: StockV2Portfolio[];
  onSelect: () => void;
  onEdit: () => void;
}) {
  const portfolio = strategy.portfolioId ? portfolios.find((p) => p.id === strategy.portfolioId) : null;
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 transition hover:border-[var(--line-strong)]">
      <button type="button" onClick={onSelect} className="min-w-0 cursor-pointer text-left">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{strategy.name}</strong>
          <Pill tone="neutral">{stockV2StrategyKindLabel(strategy.kind)}</Pill>
          <Pill tone={stockV2StrategyStatusTone(strategy.status)}>{stockV2StrategyStatusLabel(strategy.status)}</Pill>
          {strategy.scope ? <Pill tone="neutral">{stockV2StrategyScopeLabel(strategy.scope)}</Pill> : null}
          <span className="text-xs text-[var(--muted)]">· {stockV2StrategySourceLabel(strategy.source)}</span>
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          {strategy.symbol ? (
            <span>
              标的 <span className="font-mono text-[var(--text)]">{strategy.symbol}</span>
              {strategy.instrumentName ? ` · ${strategy.instrumentName}` : ""}
            </span>
          ) : null}
          {portfolio ? <span>组合 <span className="text-[var(--text)]">{portfolio.name}</span></span> : null}
          {strategy.direction ? <span>{stockV2StrategyDirectionLabel(strategy.direction)}</span> : null}
          <span>v{strategy.activeVersionNo ?? (strategy.hasDraft ? "草稿" : "-")}</span>
          <span>更新 {formatDate(strategy.updatedAt) || "-"}</span>
        </div>
      </button>
      <div className="flex items-start gap-1">
        <Button onClick={onEdit} title="编辑">
          <Pencil size={12} />
        </Button>
      </div>
    </div>
  );
}

function StrategyEmptyState({
  hasPortfolio,
  onCreateSymbol,
  onCreateMonitor,
}: {
  hasPortfolio: boolean;
  onCreateSymbol: () => void;
  onCreateMonitor: () => void;
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <button
        type="button"
        onClick={onCreateSymbol}
        className="grid cursor-pointer content-start gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4 text-left transition hover:border-[var(--line-strong)]"
      >
        <Plus size={18} className="text-[var(--muted)]" />
        <strong className="text-sm">新建单票策略</strong>
        <span className="text-xs leading-relaxed text-[var(--muted)]">
          为某只股票建立长期判断:方向、逻辑、入场/出场条件和风险备注。可账户无关,也可绑定组合。
        </span>
      </button>
      <button
        type="button"
        onClick={hasPortfolio ? onCreateMonitor : undefined}
        disabled={!hasPortfolio}
        className="grid cursor-pointer content-start gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4 text-left transition enabled:hover:border-[var(--line-strong)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        <Plus size={18} className="text-[var(--muted)]" />
        <strong className="text-sm">为组合开启智能监控</strong>
        <span className="text-xs leading-relaxed text-[var(--muted)]">
          {hasPortfolio
            ? "生成组合监控策略,作为定时监控仓位的长期判断依据。后续 Watch 才负责定时检查。"
            : "需要先在『仓位』创建一个投资组合。"}
        </span>
      </button>
    </div>
  );
}

// ============================ 创建 ============================

function CreateStrategyDrawer({
  initialKind,
  portfolios,
  actions,
  onClose,
  onSubmitSymbol,
  onSubmitMonitor,
  submitting,
}: {
  initialKind?: StockV2StrategyKind;
  portfolios: StockV2Portfolio[];
  actions: AppActions;
  onClose: () => void;
  onSubmitSymbol: (input: StockV2StrategyInput) => Promise<void>;
  onSubmitMonitor: (portfolioId: string, input: StockV2StrategyInput) => Promise<void>;
  submitting: boolean;
}) {
  const [kind, setKind] = useState<StockV2StrategyKind>(initialKind || "symbol_strategy");

  return (
    <Drawer title="新建策略" subtitle="选择策略类型,生成对应长期判断依据" onClose={onClose}>
      <div className="grid gap-4">
        <div className="grid grid-cols-2 gap-2">
          <Button tone={kind === "symbol_strategy" ? "primary" : "neutral"} onClick={() => setKind("symbol_strategy")}>
            单票策略
          </Button>
          <Button tone={kind === "portfolio_monitor" ? "primary" : "neutral"} onClick={() => setKind("portfolio_monitor")}>
            组合监控
          </Button>
        </div>

        {kind === "symbol_strategy" ? (
          <SymbolStrategyForm
            mode="create"
            portfolios={portfolios}
            actions={actions}
            onCancel={onClose}
            submitting={submitting}
            onSubmit={onSubmitSymbol}
          />
        ) : (
          <PortfolioMonitorForm
            mode="create"
            portfolios={portfolios}
            onCancel={onClose}
            submitting={submitting}
            onSubmit={onSubmitMonitor}
          />
        )}
      </div>
    </Drawer>
  );
}

// ============================ 编辑 ============================

function EditStrategyDrawer({
  strategy,
  portfolios,
  actions,
  onClose,
  onSubmit,
  submitting,
}: {
  strategy: StockV2Strategy;
  portfolios: StockV2Portfolio[];
  actions: AppActions;
  onClose: () => void;
  onSubmit: (input: StockV2StrategyInput) => Promise<void>;
  submitting: boolean;
}) {
  const isMonitor = strategy.kind === "portfolio_monitor";
  return (
    <Drawer title={`编辑策略 · ${strategy.name}`} onClose={onClose}>
      {isMonitor ? (
        <PortfolioMonitorForm
          mode="edit"
          initial={strategy}
          portfolios={portfolios}
          onCancel={onClose}
          submitting={submitting}
          onSubmit={async (_portfolioId, input) => onSubmit(input)}
        />
      ) : (
        <SymbolStrategyForm
          mode="edit"
          initial={strategy}
          portfolios={portfolios}
          actions={actions}
          onCancel={onClose}
          submitting={submitting}
          onSubmit={onSubmit}
        />
      )}
    </Drawer>
  );
}

// ============================ 表单:单票策略 ============================

function SymbolStrategyForm({
  mode,
  initial,
  portfolios,
  actions,
  onCancel,
  onSubmit,
  submitting,
}: {
  mode: "create" | "edit";
  initial?: StockV2Strategy;
  portfolios: StockV2Portfolio[];
  actions: AppActions;
  onCancel: () => void;
  onSubmit: (input: StockV2StrategyInput) => Promise<void>;
  submitting: boolean;
}) {
  const [name, setName] = useState(initial?.name || "");
  const [symbolRef, setSymbolRef] = useState<SymbolRef>({
    symbol: initial?.symbol || "",
    market: initial?.market,
    name: initial?.instrumentName,
  });
  const [scope, setScope] = useState<string>(initial?.scope || "research");
  const [portfolioId, setPortfolioId] = useState<string>(initial?.portfolioId || "");
  const [direction, setDirection] = useState<string>(initial?.direction || "long");
  const [thesis, setThesis] = useState(initial?.thesis || "");
  const [entryConditions, setEntryConditions] = useState(initial?.entryConditions || "");
  const [exitConditions, setExitConditions] = useState(initial?.exitConditions || "");
  const [riskNotes, setRiskNotes] = useState(initial?.riskNotes || "");
  const [changeSummary, setChangeSummary] = useState("");
  const [price, setPrice] = useState<PriceForm>({
    entryPriceLow: numToStr(initial?.entryPriceLow),
    entryPriceHigh: numToStr(initial?.entryPriceHigh),
    triggerPriceAbove: numToStr(initial?.triggerPriceAbove),
    triggerPriceBelow: numToStr(initial?.triggerPriceBelow),
    stopLoss: numToStr(initial?.stopLoss),
    takeProfit: numToStr(initial?.takeProfit),
  });

  const boundScope = scope === "portfolio_bound";
  const canSubmit =
    name.trim().length > 0 &&
    symbolRef.symbol.trim().length > 0 &&
    (!boundScope || portfolioId.length > 0) &&
    !submitting;

  function buildInput(): StockV2StrategyInput {
    return {
      name: name.trim(),
      kind: "symbol_strategy",
      scope: boundScope ? "portfolio_bound" : "research",
      symbol: symbolRef.symbol.trim(),
      market: symbolRef.market,
      portfolioId: boundScope ? portfolioId : undefined,
      direction,
      thesis: thesis.trim() || undefined,
      entryConditions: entryConditions.trim() || undefined,
      exitConditions: exitConditions.trim() || undefined,
      riskNotes: riskNotes.trim() || undefined,
      entryPriceLow: numOrUndef(price.entryPriceLow),
      entryPriceHigh: numOrUndef(price.entryPriceHigh),
      triggerPriceAbove: numOrUndef(price.triggerPriceAbove),
      triggerPriceBelow: numOrUndef(price.triggerPriceBelow),
      stopLoss: numOrUndef(price.stopLoss),
      takeProfit: numOrUndef(price.takeProfit),
      changeSummary: mode === "edit" ? changeSummary.trim() || undefined : undefined,
    };
  }

  return (
    <div className="grid gap-3">
      {mode === "edit" && initial?.status && initial.status !== "draft" ? (
        <Notice tone="warn">
          保存将生成新版本 v{(initial.activeVersionNo || 0) + 1},当前 v{initial.activeVersionNo ?? "-"} 保留为历史版本,供后续 Alert/Review 回看依据。
        </Notice>
      ) : null}
      {mode === "edit" && initial?.status === "draft" ? (
        <Notice tone="warn">当前是草稿,保存会直接更新草稿内容,不生成新版本。</Notice>
      ) : null}

      <Field label="策略名称">
        <input type="text" value={name} placeholder="例如:302132 中期看多" onChange={(e) => setName(e.target.value)} />
      </Field>

      <Field label="标的股票">
        <SymbolPicker actions={actions} value={symbolRef} onChange={setSymbolRef} />
      </Field>

      <div className="grid grid-cols-2 gap-3">
        <Field label="作用域">
          <select value={scope} onChange={(e) => setScope(e.target.value)}>
            <option value="research">账户无关</option>
            <option value="portfolio_bound">绑定组合</option>
          </select>
        </Field>
        <Field label="方向">
          <select value={direction} onChange={(e) => setDirection(e.target.value)}>
            <option value="long">看多</option>
            <option value="short">看空</option>
            <option value="neutral">中性</option>
            <option value="watch">仅观察</option>
          </select>
        </Field>
      </div>

      {boundScope ? (
        <Field label="绑定组合" help="绑定组合后,未来 Review 可读取组合快照并进入 proposed_operation 流程。">
          <select value={portfolioId} onChange={(e) => setPortfolioId(e.target.value)}>
            <option value="">请选择组合</option>
            {portfolios.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
      ) : (
        <p className="text-xs text-[var(--muted)]">
          账户无关策略未来只能产生 trade_signal,不会直接生成操作提案。
        </p>
      )}

      <Field label="核心逻辑 / Thesis">
        <textarea rows={3} value={thesis} placeholder="为什么看好/看空这只股票" onChange={(e) => setThesis(e.target.value)} />
      </Field>
      <Field label="入场条件">
        <textarea rows={2} value={entryConditions} placeholder="例如:回踩 20 日线企稳、放量突破前高" onChange={(e) => setEntryConditions(e.target.value)} />
      </Field>
      <Field label="出场条件">
        <textarea rows={2} value={exitConditions} placeholder="例如:逻辑证伪、达到目标价、跌破止损" onChange={(e) => setExitConditions(e.target.value)} />
      </Field>
      <Field label="风险备注">
        <textarea rows={2} value={riskNotes} placeholder="仓位上限、最大可承受回撤、需关注的事件" onChange={(e) => setRiskNotes(e.target.value)} />
      </Field>

      <CollapsibleSection title="价格与触发(可选)" subtitle="结构化触发价,供后续 Watch 直接使用">
        <div className="grid grid-cols-2 gap-3">
          <Field label="入场价下限">
            <input type="number" step="0.01" value={price.entryPriceLow} onChange={(e) => setPrice({ ...price, entryPriceLow: e.target.value })} />
          </Field>
          <Field label="入场价上限">
            <input type="number" step="0.01" value={price.entryPriceHigh} onChange={(e) => setPrice({ ...price, entryPriceHigh: e.target.value })} />
          </Field>
          <Field label="突破触发价">
            <input type="number" step="0.01" value={price.triggerPriceAbove} onChange={(e) => setPrice({ ...price, triggerPriceAbove: e.target.value })} />
          </Field>
          <Field label="跌破触发价">
            <input type="number" step="0.01" value={price.triggerPriceBelow} onChange={(e) => setPrice({ ...price, triggerPriceBelow: e.target.value })} />
          </Field>
          <Field label="止损价">
            <input type="number" step="0.01" value={price.stopLoss} onChange={(e) => setPrice({ ...price, stopLoss: e.target.value })} />
          </Field>
          <Field label="止盈价">
            <input type="number" step="0.01" value={price.takeProfit} onChange={(e) => setPrice({ ...price, takeProfit: e.target.value })} />
          </Field>
        </div>
      </CollapsibleSection>

      {mode === "edit" ? (
        <Field label="本次变更说明(可选)">
          <input type="text" value={changeSummary} placeholder="例如:上调止损价" onChange={(e) => setChangeSummary(e.target.value)} />
        </Field>
      ) : null}

      <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
        <Button onClick={onCancel}>取消</Button>
        <Button tone="primary" disabled={!canSubmit} onClick={() => void onSubmit(buildInput())}>
          {submitting ? "保存中…" : mode === "create" ? "创建策略" : "保存(生成新版本)"}
        </Button>
      </div>
    </div>
  );
}

// ============================ 表单:组合监控 ============================

function PortfolioMonitorForm({
  mode,
  initial,
  portfolios,
  onCancel,
  onSubmit,
  submitting,
}: {
  mode: "create" | "edit";
  initial?: StockV2Strategy;
  portfolios: StockV2Portfolio[];
  onCancel: () => void;
  onSubmit: (portfolioId: string, input: StockV2StrategyInput) => Promise<void>;
  submitting: boolean;
}) {
  const [portfolioId, setPortfolioId] = useState<string>(initial?.portfolioId || "");
  const [name, setName] = useState(initial?.name || "");
  const [riskNotes, setRiskNotes] = useState(initial?.riskNotes || "");
  const canSubmit = portfolioId.length > 0 && !submitting;

  function buildInput(): StockV2StrategyInput {
    return {
      name: name.trim() || undefined,
      kind: "portfolio_monitor",
      scope: "portfolio_bound",
      portfolioId,
      riskNotes: riskNotes.trim() || undefined,
    };
  }

  return (
    <div className="grid gap-3">
      <Notice tone="warn">
        组合监控策略只承载长期判断依据。后续「每天几点检查、盘中大跌是否触发」属于 Watch,本轮不会创建真实 Watch。
      </Notice>

      <Field label="目标组合" help="将为该组合生成一条 portfolio_monitor 策略">
        <select value={portfolioId} onChange={(e) => setPortfolioId(e.target.value)}>
          <option value="">{portfolios.length ? "请选择组合" : "暂无组合,请先在『仓位』创建"}</option>
          {portfolios.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>

      <Field label="策略名称(可选)">
        <input type="text" value={name} placeholder="留空则使用组合名 + 智能监控" onChange={(e) => setName(e.target.value)} />
      </Field>

      <Field label="风险备注(可选)">
        <textarea rows={3} value={riskNotes} placeholder="整体仓位约束、关注的行业集中度、需回避的情景" onChange={(e) => setRiskNotes(e.target.value)} />
      </Field>

      <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
        <Button onClick={onCancel}>取消</Button>
        <Button tone="primary" disabled={!canSubmit} onClick={() => void onSubmit(portfolioId, buildInput())}>
          {submitting ? "保存中…" : mode === "create" ? "创建监控策略" : "保存"}
        </Button>
      </div>
    </div>
  );
}

// ============================ 详情 ============================

function StrategyDetailDrawer({
  strategy,
  portfolios,
  versions,
  versionsLoading,
  submitting,
  onClose,
  onEdit,
  onActivate,
  onPause,
  onArchive,
}: {
  strategy: StockV2Strategy;
  portfolios: StockV2Portfolio[];
  versions: StockV2StrategyVersion[];
  versionsLoading: boolean;
  submitting: boolean;
  onClose: () => void;
  onEdit: () => void;
  onActivate: () => void;
  onPause: () => Promise<void>;
  onArchive: () => Promise<void>;
}) {
  const portfolio = strategy.portfolioId ? portfolios.find((p) => p.id === strategy.portfolioId) : null;
  const archived = strategy.status === "archived";

  const items: Array<[string, ReactNode]> = [
    ["类型", stockV2StrategyKindLabel(strategy.kind)],
    ["状态", <Pill tone={stockV2StrategyStatusTone(strategy.status)}>{stockV2StrategyStatusLabel(strategy.status)}</Pill>],
    ["作用域", stockV2StrategyScopeLabel(strategy.scope)],
    ["来源", stockV2StrategySourceLabel(strategy.source)],
    ...(strategy.symbol ? [["标的", <span key="s" className="font-mono">{strategy.symbol}{strategy.instrumentName ? ` · ${strategy.instrumentName}` : ""}</span>]] as Array<[string, ReactNode]> : []),
    ...(portfolio ? [["组合", portfolio.name]] as Array<[string, ReactNode]> : []),
    ...(strategy.direction ? [["方向", stockV2StrategyDirectionLabel(strategy.direction)]] as Array<[string, ReactNode]> : []),
    ["当前版本", `v${strategy.activeVersionNo ?? (strategy.hasDraft ? "草稿" : "-")}`],
    ["创建", formatDate(strategy.createdAt) || "-"],
    ["更新", formatDate(strategy.updatedAt) || "-"],
  ];

  return (
    <Drawer
      title={strategy.name}
      subtitle={archived ? "已归档 · 只读" : `${stockV2StrategyKindLabel(strategy.kind)} · ${stockV2StrategyStatusLabel(strategy.status)}`}
      onClose={onClose}
      width={520}
      footer={
        archived ? (
          <span className="text-xs text-[var(--muted)]">归档策略只读,版本历史仍可查看</span>
        ) : (
          <>
            {strategy.status === "paused" || strategy.status === "draft" ? (
              <Button tone="primary" onClick={onActivate} disabled={submitting}>
                <Play size={14} className="mr-1.5" />
                启用
              </Button>
            ) : null}
            {strategy.status === "active" ? (
              <Button onClick={onPause} disabled={submitting}>
                <Pause size={14} className="mr-1.5" />
                暂停
              </Button>
            ) : null}
            <Button onClick={onEdit} disabled={submitting}>
              <Pencil size={14} className="mr-1.5" />
              编辑
            </Button>
            <Button tone="danger" onClick={onArchive} disabled={submitting}>
              <Archive size={14} className="mr-1.5" />
              归档
            </Button>
          </>
        )
      }
    >
      <div className="grid gap-4">
        <ContextList items={items} />

        {(strategy.thesis || strategy.entryConditions || strategy.exitConditions || strategy.riskNotes) ? (
          <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm">
            {strategy.thesis ? <DetailField label="核心逻辑" value={strategy.thesis} /> : null}
            {strategy.entryConditions ? <DetailField label="入场条件" value={strategy.entryConditions} /> : null}
            {strategy.exitConditions ? <DetailField label="出场条件" value={strategy.exitConditions} /> : null}
            {strategy.riskNotes ? <DetailField label="风险备注" value={strategy.riskNotes} /> : null}
          </div>
        ) : null}

        <PriceSummary strategy={strategy} />

        <div>
          <div className="mb-2 text-xs font-medium text-[var(--muted-strong)]">版本历史</div>
          {versionsLoading ? (
            <p className="text-xs text-[var(--muted)]">加载版本…</p>
          ) : versions.length === 0 ? (
            <p className="text-xs text-[var(--muted)]">暂无版本记录(接口可能未就绪)。</p>
          ) : (
            <div className="grid gap-2">
              {versions.map((v) => (
                <div
                  key={v.versionNo}
                  className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs"
                >
                  <span className="font-mono text-[var(--muted-strong)]">v{v.versionNo}</span>
                  <span className="min-w-0 truncate text-[var(--muted-strong)]">
                    {v.changeSummary || stockV2StrategyDirectionLabel(v.direction) || "—"}
                  </span>
                  <div className="flex items-center gap-2">
                    <Pill tone={stockV2StrategyVersionStatusTone(v.status)}>{stockV2StrategyVersionStatusLabel(v.status)}</Pill>
                    <span className="text-[var(--muted)]">{formatDate(v.createdAt) || "-"}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Drawer>
  );
}

function DetailField({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid gap-1">
      <span className="text-xs text-[var(--muted)]">{label}</span>
      <span className="whitespace-pre-wrap leading-relaxed text-[var(--text)]">{value}</span>
    </div>
  );
}

function PriceSummary({ strategy }: { strategy: StockV2Strategy }) {
  const rows: Array<[string, string]> = [];
  if (definedPrice(strategy.entryPriceLow) || definedPrice(strategy.entryPriceHigh)) {
    rows.push(["入场区间", `${strategy.entryPriceLow ?? "—"} ~ ${strategy.entryPriceHigh ?? "—"}`]);
  }
  if (definedPrice(strategy.triggerPriceAbove)) rows.push(["突破触发", String(strategy.triggerPriceAbove)]);
  if (definedPrice(strategy.triggerPriceBelow)) rows.push(["跌破触发", String(strategy.triggerPriceBelow)]);
  if (definedPrice(strategy.stopLoss)) rows.push(["止损", String(strategy.stopLoss)]);
  if (definedPrice(strategy.takeProfit)) rows.push(["止盈", String(strategy.takeProfit)]);
  if (rows.length === 0) return null;
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="mb-2 text-xs font-medium text-[var(--muted-strong)]">价格与触发</div>
      <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3">
        {rows.map(([label, value]) => (
          <div key={label} className="grid gap-0.5">
            <span className="text-[var(--muted)]">{label}</span>
            <span className="font-mono text-[var(--text)]">{value}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ============================ 标的搜索 ============================

function SymbolPicker({
  actions,
  value,
  onChange,
}: {
  actions: AppActions;
  value: SymbolRef;
  onChange: (ref: SymbolRef) => void;
}) {
  const [query, setQuery] = useState(value.symbol && value.name ? `${value.symbol} · ${value.name}` : value.symbol || "");
  const [results, setResults] = useState<StockV2Instrument[]>([]);
  const [open, setOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    if (!query.trim()) {
      setResults([]);
      return;
    }
    timerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const res = await actions.api<{ items: StockV2Instrument[] }>(
          `/api/stockv2/instruments/search?q=${encodeURIComponent(query)}&limit=20`,
        );
        setResults(res.items || []);
        setOpen(true);
      } catch {
        setResults([]);
      } finally {
        setSearching(false);
      }
    }, 200);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [query, actions]);

  function pick(inst: StockV2Instrument) {
    onChange({ symbol: inst.symbol, market: inst.market, name: inst.name });
    setQuery(`${inst.symbol} · ${inst.name || ""}`);
    setOpen(false);
  }

  return (
    <div className="relative" ref={wrapRef}>
      <div className="relative">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
        <input
          type="text"
          className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
          placeholder="输入代码或名称搜索"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => {
            if (results.length) setOpen(true);
          }}
        />
      </div>
      {open ? (
        <div className="absolute left-0 right-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]">
          {searching ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">搜索中…</div>
          ) : results.length === 0 ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">{query ? "未找到匹配的股票" : "输入关键词开始搜索"}</div>
          ) : (
            results.map((inst) => (
              <button
                key={inst.id}
                type="button"
                onClick={() => pick(inst)}
                className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-[var(--surface-soft)]"
              >
                <span className="font-mono">{inst.symbol}</span>
                <span className="mx-2 min-w-0 truncate text-[var(--muted)]">{inst.name}</span>
                <Pill tone="neutral" className="text-xs">
                  {inst.market === "SH" ? "沪" : inst.market === "SZ" ? "深" : inst.market === "BJ" ? "北" : inst.market}
                </Pill>
              </button>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}

// ============================ helpers ============================

function numOrUndef(value: string): number | undefined {
  if (value.trim() === "") return undefined;
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function numToStr(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

function definedPrice(value?: number): value is number {
  return typeof value === "number" && Number.isFinite(value);
}
