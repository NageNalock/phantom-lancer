import { ArrowClockwise, CaretLeft, CaretRight, Clock, ClockCounterClockwise, MagnifyingGlass, Plus, X } from "@phosphor-icons/react";
import { useEffect, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Instrument, StockV2StockProfileSummary, StockV2UpdateJob, StockV2UniverseUpdateRequest } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Field, Panel, Pill } from "../../components/ui";
import { stockV2InstrumentTypeLabel, stockV2TriggerTypeLabel, stockV2UpdateStatusLabel, stockV2UpdateStatusTone } from "../../domain/labels";
import { StockV2InstrumentDetail } from "./StockV2InstrumentDetail";
import { StockV2ProfileRecords, StockV2ProfileSettings } from "./StockV2ProfileWorkbench";

const PAGE_SIZE = 50;
type MasterDataView = "instruments" | "profileSettings" | "profileRecords";

// 生成页码数组：首页、当前页附近、末页，用省略号间隔
function buildPageNumbers(currentPage: number, totalPages: number): (number | "...")[] {
  if (totalPages <= 0) return [1];
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }

  const current = currentPage + 1;
  const pages: (number | "...")[] = [1];

  // 左侧省略号
  if (current > 4) {
    pages.push("...");
  }

  // 中间页码：当前页前后各 2 页
  const start = Math.max(2, current - 2);
  const end = Math.min(totalPages - 1, current + 2);
  for (let i = start; i <= end; i++) {
    pages.push(i);
  }

  // 右侧省略号
  if (current < totalPages - 3) {
    pages.push("...");
  }

  pages.push(totalPages);
  return pages;
}

interface InstrumentsPage {
  items: StockV2Instrument[];
  total: number;
  limit: number;
  offset: number;
}

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;

export function StockV2Universe({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const stockv2 = data.stockv2;
  const jobs = stockv2.updateJobs || [];
  const runningJob = jobs.find(j => j.status === "running");
  const [historyOpen, setHistoryOpen] = useState(false);
  const [page, setPage] = useState(0);
  const [instrumentsPage, setInstrumentsPage] = useState<InstrumentsPage | null>(null);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [jumpInput, setJumpInput] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [marketFilter, setMarketFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [masterDataView, setMasterDataView] = useState<MasterDataView>("instruments");
  const [addHolding, setAddHolding] = useState<{ inst: StockV2Instrument } | null>(null);
  const [selectedInst, setSelectedInst] = useState<StockV2Instrument | null>(null);
  const [profileSummaries, setProfileSummaries] = useState<Record<string, StockV2StockProfileSummary>>({});

  const portfolios = stockv2.portfolios || [];
  const isSearching = searchQuery.trim().length > 0;

  const totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));
  const progressPct = runningJob
    ? Math.min(100, Math.round(((runningJob.processedCount || 0) / (runningJob.totalCount || runningJob.processedCount || 1)) * 100))
    : 0;

  function handleJump() {
    const n = parseInt(jumpInput, 10);
    if (!isNaN(n) && n >= 1 && n <= totalPages) {
      void loadPage(n - 1);
      setJumpInput("");
    }
  }

  // 拉取分页数据；输入搜索词时改走后端模糊搜索，避免一次性加载全市场后前端过滤。
  async function loadPage(pageNum: number, query = searchQuery, market = marketFilter, instrumentType = typeFilter) {
    const keyword = query.trim();
    const params = new URLSearchParams();
    if (market) params.set("market", market);
    if (instrumentType) params.set("instrumentType", instrumentType);
    setLoading(true);
    try {
      if (keyword) {
        params.set("q", keyword);
        params.set("limit", "100");
        const data = await actions.api<Partial<InstrumentsPage>>(
          `/api/stockv2/instruments/search?${params.toString()}`
        );
        const items = Array.isArray(data.items) ? data.items : [];
        setInstrumentsPage({
          items,
          total: data.total ?? items.length,
          limit: data.limit ?? 100,
          offset: 0,
        });
        setPage(0);
        setTotalCount(data.total ?? items.length);
        await loadProfileSummaries(items);
      } else {
        params.set("limit", String(PAGE_SIZE));
        params.set("offset", String(pageNum * PAGE_SIZE));
        const data = await actions.api<InstrumentsPage>(
          `/api/stockv2/instruments?${params.toString()}`
        );
        const items = Array.isArray(data.items) ? data.items : [];
        setInstrumentsPage({
          ...data,
          items,
        });
        setPage(pageNum);
        if (data.total !== undefined) {
          setTotalCount(data.total);
        }
        await loadProfileSummaries(items);
      }
    } catch (e) {
      actions.setToast(`加载失败：${friendlyError(e)}`, "danger");
    } finally {
      setLoading(false);
    }
  }

  async function loadProfileSummaries(items: StockV2Instrument[]) {
    const symbols = items.map((item) => item.symbol).filter(Boolean);
    if (symbols.length === 0) {
      setProfileSummaries({});
      return;
    }
    try {
      const res = await actions.api<{ items?: Record<string, StockV2StockProfileSummary> }>(
        `/api/stockv2/profiles/summaries?symbols=${encodeURIComponent(symbols.join(","))}`,
      );
      setProfileSummaries(res.items ?? {});
    } catch {
      setProfileSummaries({});
    }
  }

  // 初始加载 + 搜索防抖刷新
  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadPage(0, searchQuery, marketFilter, typeFilter);
    }, searchQuery.trim() ? 250 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery, marketFilter, typeFilter]);

  // 当更新状态从 running 变为完成时，刷新第一页
  const wasRunningRef = useRef(false);
  useEffect(() => {
    const isRunning = !!runningJob;
    if (wasRunningRef.current && !isRunning) {
      void loadPage(0);
    }
    wasRunningRef.current = isRunning;
  }, [runningJob]);

  // 轮询进度
  useEffect(() => {
    if (!runningJob) {
      return;
    }
    const timer = setInterval(async () => {
      try {
        await actions.refreshStockV2();
      } catch {
        // 静默
      }
    }, 2000);
    return () => clearInterval(timer);
  }, [runningJob?.id, actions]);

  async function handleTriggerUpdate() {
    const req: StockV2UniverseUpdateRequest = {
      triggerType: "manual",
      triggerSource: "web",
    };
    await actions.api("/api/stockv2/update/trigger", {
      method: "POST",
      body: req,
    });
    actions.setToast("更新任务已启动", "good");
    // 立刻刷新一次显示进度
    setTimeout(() => void actions.refreshStockV2(), 500);
  }

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap gap-1 border-b border-[var(--line)] pb-2">
        {[
          { id: "instruments" as const, label: "标的主数据" },
          { id: "profileSettings" as const, label: "画像配置" },
          { id: "profileRecords" as const, label: "画像记录" },
        ].map((tab) => (
          <button
            key={tab.id}
            type="button"
            onClick={() => setMasterDataView(tab.id)}
            className={[
              "rounded-md px-3 py-1.5 text-sm transition",
              masterDataView === tab.id
                ? "bg-[var(--surface-strong)] font-medium text-[var(--text)]"
                : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]",
            ].join(" ")}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {masterDataView === "profileSettings" ? (
        <StockV2ProfileSettings actions={actions} data={data} runAction={runAction} />
      ) : null}
      {masterDataView === "profileRecords" ? <StockV2ProfileRecords actions={actions} /> : null}
      {masterDataView !== "instruments" ? null : (
      <>
      {/* 进度条区域：始终展示占位，运行时有动效 */}
      <Panel
        title="数据更新进度"
        subtitle={
          runningJob
            ? `正在更新 ${runningJob.totalCount || runningJob.processedCount} 只标的 · ${stockV2TriggerTypeLabel(runningJob.triggerType)}`
            : jobs[0]
              ? `上次更新：${stockV2UpdateStatusLabel(jobs[0])} · ${stockV2TriggerTypeLabel(jobs[0].triggerType)} · ${formatCompactTime(jobs[0].startAt)}`
              : "尚未执行过更新"
        }
        actions={
          <div className="flex gap-2">
            <Button onClick={() => setHistoryOpen(true)}>
              <ClockCounterClockwise size={14} className="mr-1.5" />
              更新历史
            </Button>
            <Button tone="primary" onClick={() => void runAction("触发更新", handleTriggerUpdate)} disabled={!!runningJob}>
              <ArrowClockwise size={14} className="mr-1.5" />
              {runningJob ? "更新中..." : "立即更新"}
            </Button>
          </div>
        }
      >
        <div className="space-y-2">
          <div className="h-2 overflow-hidden rounded-full bg-[var(--line)]">
            <div
              className="h-full rounded-full transition-all duration-500"
              style={{
                width: `${progressPct}%`,
                backgroundColor: runningJob ? "var(--accent)" : "var(--muted)",
              }}
            />
          </div>
          <div className="flex justify-between text-xs text-[var(--muted)]">
            <span>
              已处理 <strong className="text-[var(--text)]">{runningJob?.processedCount || 0}</strong> / {runningJob?.totalCount || "-"}
            </span>
            <span>
              成功 <strong className="text-[var(--good)]">{runningJob?.successCount || 0}</strong>
              <span className="mx-2">|</span>
              失败 <strong className="text-[var(--danger)]">{runningJob?.failedCount || 0}</strong>
            </span>
          </div>
        </div>
      </Panel>

      {/* 标的列表 */}
      <Panel
        title={`标的主数据 (${totalCount > 0 ? totalCount : "..."})`}
        subtitle="新浪列表源 + 腾讯行情源 · A 股股票与场内基金"
      >
        <div className="mb-3 flex items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-[340px] max-w-full">
              <MagnifyingGlass size={15} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
              <input
                type="search"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="搜索代码或名称，例如 302132 / 510300"
                className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              />
            </div>
            <select
              value={marketFilter}
              onChange={(e) => setMarketFilter(e.target.value)}
              className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              aria-label="按市场过滤"
            >
              <option value="">全部市场</option>
              <option value="SH">沪市</option>
              <option value="SZ">深市</option>
              <option value="BJ">北市</option>
            </select>
            <select
              value={typeFilter}
              onChange={(e) => setTypeFilter(e.target.value)}
              className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              aria-label="按类型过滤"
            >
              <option value="">全部类型</option>
              <option value="stock">股票</option>
              <option value="exchange_fund">场内基金</option>
            </select>
          </div>
          <span className="shrink-0 text-xs text-[var(--muted)]">
            {isSearching ? `搜索结果 ${instrumentsPage?.items.length || 0} 条` : `每页 ${PAGE_SIZE} 条`}
          </span>
        </div>
        {loading && !instrumentsPage ? (
          <div className="py-8 text-center text-sm text-[var(--muted)]">
            <Clock size={24} className="mx-auto mb-2 opacity-50 animate-spin" />
            加载中...
          </div>
        ) : !instrumentsPage || instrumentsPage.items.length === 0 ? (
          <div className="py-8 text-center text-sm text-[var(--muted)]">
            <Clock size={24} className="mx-auto mb-2 opacity-50" />
            {isSearching ? `未找到「${searchQuery.trim()}」匹配的标的。` : "暂无标的数据，点击右上角「立即更新」开始首次同步。"}
          </div>
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-[var(--line)] text-left text-xs text-[var(--muted)]">
                    <th className="py-2 pr-4 font-medium">代码</th>
                    <th className="py-2 pr-4 font-medium">名称</th>
                    <th className="py-2 pr-4 font-medium">市场</th>
                    <th className="py-2 pr-4 font-medium">类型</th>
                    <th className="py-2 pr-4 font-medium">画像摘要</th>
                    <th className="py-2 pr-4 font-medium">画像状态</th>
                    <th className="py-2 pr-4 font-medium">状态</th>
                    <th className="py-2 pr-4 font-medium">更新时间</th>
                    <th className="py-2 pr-2 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {instrumentsPage.items.map((inst) => (
                    <StockRow
                      key={inst.id}
                      inst={inst}
                      onAdd={() => setAddHolding({ inst })}
                      onClick={() => setSelectedInst(inst)}
                      profile={profileSummaries[inst.symbol]}
                    />
                  ))}
                </tbody>
              </table>
            </div>

            {/* 分页控件 */}
            {isSearching ? (
              <div className="mt-4 border-t border-[var(--line)] pt-3 text-xs text-[var(--muted)]">
                搜索最多展示前 100 条结果，清空搜索词后恢复分页浏览。
              </div>
            ) : (
            <div className="mt-4 flex items-center justify-between border-t border-[var(--line)] pt-3">
              <div className="text-xs text-[var(--muted)]">
                第 {page * PAGE_SIZE + 1} - {Math.min((page + 1) * PAGE_SIZE, totalCount || page * PAGE_SIZE + instrumentsPage.items.length)} 条
                {totalCount > 0 ? ` / 共 ${totalCount} 条` : ""}
              </div>
              <div className="flex items-center gap-2">
                {/* 首页 */}
                <Button
                  onClick={() => loadPage(0)}
                  disabled={page === 0 || loading}
                >
                  首页
                </Button>
                {/* 上一页 */}
                <Button
                  onClick={() => loadPage(page - 1)}
                  disabled={page === 0 || loading}
                >
                  <CaretLeft size={14} />
                </Button>

                {/* 页码按钮 */}
                <div className="flex items-center gap-1">
                  {buildPageNumbers(page, totalPages).map((p, idx) =>
                    p === "..." ? (
                      <span key={`ellipsis-${idx}`} className="px-2 text-sm text-[var(--muted)]">
                        ...
                      </span>
                    ) : (
                      <button
                        key={p}
                        onClick={() => loadPage((p as number) - 1)}
                        disabled={loading}
                        className={[
                          "min-w-[32px] rounded px-2 py-1 text-sm",
                          p === page + 1
                            ? "bg-[var(--accent)] text-white font-medium"
                            : "text-[var(--text)] hover:bg-[var(--surface-soft)]",
                        ].join(" ")}
                      >
                        {p}
                      </button>
                    )
                  )}
                </div>

                {/* 下一页 */}
                <Button
                  onClick={() => loadPage(page + 1)}
                  disabled={page + 1 >= totalPages || loading}
                >
                  <CaretRight size={14} />
                </Button>
                {/* 末页 */}
                <Button
                  onClick={() => loadPage(totalPages - 1)}
                  disabled={page + 1 >= totalPages || loading}
                >
                  末页
                </Button>

                {/* 跳转输入框 */}
                <div className="ml-2 flex items-center gap-1 text-xs text-[var(--muted)]">
                  跳至
                  <input
                    type="number"
                    min={1}
                    max={totalPages}
                    value={jumpInput}
                    onChange={(e) => setJumpInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") handleJump();
                    }}
                    className="w-14 rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-center text-sm text-[var(--text)] focus:outline-none focus:border-[var(--accent)]"
                  />
                  页
                  <Button onClick={handleJump} disabled={loading}>
                    跳转
                  </Button>
                </div>
              </div>
            </div>
            )}
          </>
        )}
      </Panel>

      {/* 加入持仓弹窗 */}
      {addHolding ? (
        <AddHoldingDialog
          inst={addHolding.inst}
          portfolios={portfolios}
          actions={actions}
          onClose={() => setAddHolding(null)}
          onAdded={() => {
            setAddHolding(null);
            actions.setToast(`已将 ${addHolding.inst.symbol} 加入持仓`, "good");
          }}
        />
      ) : null}

      {/* 更新历史弹窗 */}
      {historyOpen ? (
        <UpdateHistoryDialog jobs={jobs} onClose={() => setHistoryOpen(false)} />
      ) : null}

      {/* 标的详情 Drawer */}
      {selectedInst ? (
        <StockV2InstrumentDetail
          inst={selectedInst}
          actions={actions}
          onClose={() => setSelectedInst(null)}
        />
      ) : null}
      </>
      )}
    </div>
  );
}

function StockRow({ inst, onAdd, onClick, profile }: { inst: StockV2Instrument; onAdd: () => void; onClick?: () => void; profile?: StockV2StockProfileSummary }) {
  const marketLabel = { SH: "沪市", SZ: "深市", BJ: "北市" }[inst.market] || inst.market;
  const statusTone = inst.status === "active" ? "good" : "neutral";
  const profileTone = profile?.status === "ready" ? "good" : profile?.status === "partial" ? "warn" : "neutral";
  const aiTone = profile?.aiProfileStatus === "ready" ? "good" : profile?.aiProfileStatus === "failed" ? "danger" : "neutral";

  return (
    <tr
      className="border-b border-[var(--line-soft)] last:border-b-0 cursor-pointer hover:bg-[var(--surface-soft)] transition"
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (onClick && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onClick();
        }
      }}
    >
      <td className="py-2 pr-4 font-mono text-sm">{inst.symbol}</td>
      <td className="py-2 pr-4 font-medium">{inst.name || "-"}</td>
      <td className="py-2 pr-4">
        <Pill tone="neutral">{marketLabel}</Pill>
      </td>
      <td className="py-2 pr-4">
        <Pill tone="neutral">{stockV2InstrumentTypeLabel(inst.instrumentType)}</Pill>
      </td>
      <td className="max-w-[360px] py-2 pr-4 text-xs text-[var(--muted-strong)]">
        <span className="block max-h-10 overflow-hidden leading-5">{profile?.businessSummary || "-"}</span>
      </td>
      <td className="py-2 pr-4">
        <div className="flex flex-wrap gap-1">
          <Pill tone={profileTone}>{profile?.status === "ready" ? "基础" : profile?.status === "partial" ? "部分" : "缺失"}</Pill>
          <Pill tone={aiTone}>AI {profile?.aiProfileStatus || "missing"}</Pill>
        </div>
      </td>
      <td className="py-2 pr-4">
        <Pill tone={statusTone}>{inst.status === "active" ? "活跃" : inst.status}</Pill>
      </td>
      <td className="py-2 pr-4 text-xs text-[var(--muted)]">
        {formatCompactTime(inst.lastUpdate)}
      </td>
      <td className="py-2 pl-2 text-right">
        <Button
          onClick={(e) => {
            e.stopPropagation();
            onAdd();
          }}
          tone="primary"
          type="button"
        >
          <Plus size={12} />
        </Button>
      </td>
    </tr>
  );
}

function AddHoldingDialog({
  inst,
  portfolios,
  actions,
  onClose,
  onAdded,
}: {
  inst: StockV2Instrument;
  portfolios: { id: string; name: string }[];
  actions: AppActions;
  onClose: () => void;
  onAdded: () => void;
}) {
  const [portfolioId, setPortfolioId] = useState(portfolios[0]?.id || "");
  const [quantity, setQuantity] = useState("");
  const [costPrice, setCostPrice] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function handleSubmit() {
    if (!portfolioId) {
      actions.setToast("请先选择投资组合", "warn");
      return;
    }
    const qtyNum = Number(quantity);
    if (!qtyNum || qtyNum <= 0) {
      actions.setToast("请输入数量", "warn");
      return;
    }
    setSubmitting(true);
    try {
      await actions.api(`/api/stockv2/portfolios/${portfolioId}/holdings`, {
        method: "POST",
        body: {
          symbol: inst.symbol,
          quantity: qtyNum,
          costPrice: Number(costPrice) || 0,
        },
      });
      onAdded();
      await actions.refreshStockV2();
    } catch (e) {
      actions.setToast(`添加失败：${friendlyError(e)}`, "danger");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--line)] px-5 py-3">
          <h3 className="m-0 text-base font-semibold">加入持仓</h3>
          <Button onClick={onClose}>
            <X size={14} />
          </Button>
        </div>
        <div className="p-5">
          <div className="mb-4 flex items-center gap-3 rounded-lg bg-[var(--surface-soft)] p-3">
            <span className="font-mono font-semibold">{inst.symbol}</span>
            <span className="text-sm text-[var(--muted)]">{inst.name}</span>
            <Pill tone="neutral" className="ml-auto text-xs">
              {inst.market === "SH" ? "沪市" : inst.market === "SZ" ? "深市" : "北市"}
            </Pill>
          </div>

          <div className="grid gap-3">
            <Field label="投资组合">
              <select
                value={portfolioId}
                onChange={(e) => setPortfolioId(e.target.value)}
              >
                {portfolios.length === 0 ? (
                  <option value="">暂无组合，请到仓位页面创建</option>
                ) : (
                  portfolios.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))
                )}
              </select>
            </Field>

            <div className="grid grid-cols-2 gap-3">
              <Field label="数量 (股)">
                <input
                  type="number"
                  value={quantity}
                  placeholder="请输入数量"
                  onChange={(e) => setQuantity(e.target.value)}
                />
              </Field>
              <Field label="成本价 (¥)">
                <input
                  type="number"
                  step="0.01"
                  value={costPrice}
                  placeholder="请输入成本价"
                  onChange={(e) => setCostPrice(e.target.value)}
                />
              </Field>
            </div>
          </div>
        </div>

        <div className="flex justify-end gap-2 border-t border-[var(--line)] px-5 py-3">
          <Button onClick={onClose}>取消</Button>
          <Button
            tone="primary"
            onClick={() => void handleSubmit()}
            disabled={submitting || portfolios.length === 0}
          >
            <Plus size={14} className="mr-1.5" />
            {submitting ? "添加中..." : "添加"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function UpdateHistoryDialog({ jobs, onClose }: { jobs: StockV2UpdateJob[]; onClose: () => void }) {
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null);

  // ESC 关闭
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  function toggleFailed(job: StockV2UpdateJob) {
    if (!job.failedItems?.length) return;
    setExpandedJobId(expandedJobId === job.id ? null : job.id);
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="w-full max-w-2xl rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-[var(--line)] px-5 py-3">
          <div>
            <h3 className="m-0 text-base font-semibold">更新历史记录</h3>
            <p className="muted mt-0.5 mb-0 text-xs">每次数据更新的执行情况</p>
          </div>
          <Button onClick={onClose}>
            <X size={14} />
          </Button>
        </div>

        <div className="max-h-[60vh] overflow-y-auto p-5">
          {jobs.length === 0 ? (
            <p className="py-8 text-center text-sm text-[var(--muted)]">暂无更新记录</p>
          ) : (
            <div className="grid gap-3">
              {jobs.map((job) => (
                <div
                  className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4"
                  key={job.id}
                >
                  <div className="flex items-start justify-between">
                    <div className="flex items-center gap-2">
                      <Pill tone={stockV2UpdateStatusTone(job)}>
                        {stockV2UpdateStatusLabel(job)}
                      </Pill>
                      <span className="text-sm font-medium">
                        {stockV2TriggerTypeLabel(job.triggerType)}
                      </span>
                      {job.triggerSource ? (
                        <span className="text-xs text-[var(--muted)]">· {job.triggerSource}</span>
                      ) : null}
                    </div>
                    <span className="text-xs text-[var(--muted)]">
                      {formatDateTime(job.startAt)}
                    </span>
                  </div>

                  <div className="mt-3 grid grid-cols-4 gap-3 text-sm">
                    <div>
                      <div className="text-xs text-[var(--muted)]">总计</div>
                      <div className="font-semibold">{job.totalCount || job.processedCount}</div>
                    </div>
                    <div>
                      <div className="text-xs text-[var(--muted)]">已处理</div>
                      <div className="font-semibold">{job.processedCount}</div>
                    </div>
                    <div>
                      <div className="text-xs text-[var(--good)]">成功</div>
                      <div className="font-semibold text-[var(--good)]">{job.successCount}</div>
                    </div>
                    <div>
                      <div className="text-xs text-[var(--danger)]">失败</div>
                      {job.failedCount > 0 && job.failedItems?.length ? (
                        <button
                          onClick={() => toggleFailed(job)}
                          className="text-left font-semibold text-[var(--danger)] underline decoration-dotted underline-offset-2 hover:text-[var(--danger)]"
                          title="点击查看失败详情"
                        >
                          {job.failedCount}
                        </button>
                      ) : (
                        <div className="font-semibold text-[var(--danger)]">{job.failedCount}</div>
                      )}
                    </div>
                  </div>

                  {expandedJobId === job.id && job.failedItems?.length ? (
                    <div className="mt-3 rounded border border-[var(--danger-soft)] bg-[var(--danger-soft)/20] p-3">
                      <div className="mb-2 text-xs font-medium text-[var(--danger)]">
                        失败详情（{job.failedItems.length} 只）
                      </div>
                      <div className="max-h-40 overflow-y-auto space-y-1">
                        {job.failedItems.map((item, idx) => (
                          <div
                            key={idx}
                            className="flex items-start justify-between gap-4 text-xs"
                          >
                            <span className="font-mono text-[var(--text)]">{item.symbol}</span>
                            <span className="text-[var(--muted)] text-right flex-1">
                              {item.reason}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : null}

                  {job.errorMessage ? (
                    <div className="mt-3 rounded border border-[var(--danger-soft)] bg-[var(--danger-soft)/30] px-3 py-2 text-xs text-[var(--danger)]">
                      {job.errorMessage}
                    </div>
                  ) : null}

                  {job.endAt ? (
                    <div className="mt-2 text-xs text-[var(--muted)]">
                      耗时 {formatDuration(job.startAt, job.endAt)}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="flex justify-end border-t border-[var(--line)] px-5 py-3">
          <Button tone="primary" onClick={onClose}>关闭</Button>
        </div>
      </div>
    </div>
  );
}

function formatCompactTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin} 分钟前`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr} 小时前`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay} 天前`;
  return d.toLocaleDateString("zh-CN");
}

function formatDateTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatDuration(start: string, end: string): string {
  const s = new Date(start).getTime();
  const e = new Date(end).getTime();
  if (!s || !e) return "-";
  const ms = e - s;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec} 秒`;
  const min = Math.floor(sec / 60);
  const rem = sec % 60;
  return `${min} 分 ${rem} 秒`;
}
