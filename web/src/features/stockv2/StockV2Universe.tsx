import { ArrowClockwise, CaretLeft, CaretRight, Clock, ClockCounterClockwise, MagnifyingGlass, Plus, X } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Announcement, StockV2AssetMaintenanceItem, StockV2AssetReadinessOverview, StockV2AssetSummary, StockV2Instrument, StockV2UpdateJob, StockV2UniverseUpdateRequest } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Field, Panel, Pill, SubTabs } from "../../components/ui";
import {
  stockV2DailyBarsQualityLabel,
  stockV2DailyBarsQualityTone,
  stockV2InstrumentTypeLabel,
  stockV2TriggerTypeLabel,
  stockV2UpdateStatusLabel,
  stockV2UpdateStatusTone,
} from "../../domain/labels";
import { StockV2DailyBarsMaintenance } from "./StockV2DailyBars";
import { StockV2InstrumentDetail } from "./StockV2InstrumentDetail";
import { StockV2MaintenanceProgress, StockV2ReadinessOverview, stockV2AIProgressActive } from "./StockV2MaintenanceProgress";
import { StockV2ProfileRecords } from "./StockV2ProfileWorkbench";
import { StockV2Settings } from "./StockV2Settings";

const PAGE_SIZE = 50;
const SUPPLEMENT_CACHE_TTL_MS = 60_000;
const READINESS_REFRESH_MIN_INTERVAL_MS = 30_000;
type MasterDataView = "overview" | "maintenance" | "maintenanceSettings" | "announcements" | "profileRecords";
type SupplementCacheEntry<T> = { value: T; expiresAt: number };

const assetSummaryCache = new Map<string, SupplementCacheEntry<StockV2AssetSummary>>();

function readFreshCache<T>(cache: Map<string, SupplementCacheEntry<T>>, key: string, now = Date.now()): T | undefined {
  const item = cache.get(key);
  if (!item) return undefined;
  if (item.expiresAt <= now) {
    cache.delete(key);
    return undefined;
  }
  return item.value;
}

function writeFreshCache<T>(cache: Map<string, SupplementCacheEntry<T>>, key: string, value: T) {
  cache.set(key, { value, expiresAt: Date.now() + SUPPLEMENT_CACHE_TTL_MS });
}

function clearInstrumentSupplementCache() {
  assetSummaryCache.clear();
}

function uniqueSymbols(items: StockV2Instrument[]) {
  return Array.from(new Set(items.map((item) => item.symbol).filter(Boolean)));
}

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
  const latestJob = jobs[0];
  const aiProgressActive = jobs.some(stockV2AIProgressActive);
  const aiProgressSignature = jobs.map((job) => {
    const progress = job.maintenanceProgress?.aiProfile;
    return [job.id, progress?.status, progress?.queued, progress?.running, progress?.retrying, progress?.completed, progress?.failed, progress?.outstanding].join(":");
  }).join("|");
  const [historyOpen, setHistoryOpen] = useState(false);
  const [page, setPage] = useState(0);
  const [instrumentsPage, setInstrumentsPage] = useState<InstrumentsPage | null>(null);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [jumpInput, setJumpInput] = useState("");
  const [searchQuery, setSearchQuery] = useState("");
  const [marketFilter, setMarketFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [profileStatusFilter, setProfileStatusFilter] = useState("");
  const [masterDataView, setMasterDataView] = useState<MasterDataView>("overview");
  const [addHolding, setAddHolding] = useState<{ inst: StockV2Instrument } | null>(null);
  const [selectedInst, setSelectedInst] = useState<StockV2Instrument | null>(null);
  const [assetSummaries, setAssetSummaries] = useState<Record<string, StockV2AssetSummary>>({});
  const [supplementLoading, setSupplementLoading] = useState(false);
  const [readinessOverview, setReadinessOverview] = useState<StockV2AssetReadinessOverview | null>(null);
  const [readinessOverviewLoading, setReadinessOverviewLoading] = useState(false);
  const [readinessOverviewError, setReadinessOverviewError] = useState("");
  const supplementRequestRef = useRef(0);
  const readinessRequestRef = useRef(0);
  const readinessLoadingRef = useRef(false);
  const readinessLastLoadedAtRef = useRef(0);
  const readinessRefreshTimerRef = useRef<number | null>(null);

  const portfolios = stockv2.portfolios || [];
  const isSearching = searchQuery.trim().length > 0;

  const totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));

  const loadReadinessOverview = useCallback(async () => {
    if (readinessLoadingRef.current) return;
    readinessLoadingRef.current = true;
    const requestID = ++readinessRequestRef.current;
    setReadinessOverviewLoading(true);
    setReadinessOverviewError("");
    try {
      const overview = await actions.api<StockV2AssetReadinessOverview>("/api/stockv2/assets/readiness/overview");
      if (requestID === readinessRequestRef.current) {
        setReadinessOverview(overview);
      }
    } catch (error) {
      if (requestID === readinessRequestRef.current) {
        setReadinessOverviewError(`资产就绪度刷新失败：${friendlyError(error)}`);
      }
    } finally {
      readinessLoadingRef.current = false;
      if (requestID === readinessRequestRef.current) {
        readinessLastLoadedAtRef.current = Date.now();
        setReadinessOverviewLoading(false);
      }
    }
  }, [actions]);

  function handleReadinessOverviewRefresh() {
    if (readinessRefreshTimerRef.current !== null) {
      window.clearTimeout(readinessRefreshTimerRef.current);
      readinessRefreshTimerRef.current = null;
    }
    void loadReadinessOverview();
  }

  function handleJump() {
    const n = parseInt(jumpInput, 10);
    if (!isNaN(n) && n >= 1 && n <= totalPages) {
      void loadPage(n - 1);
      setJumpInput("");
    }
  }

  // 拉取分页数据；输入搜索词时改走后端模糊搜索，避免一次性加载全市场后前端过滤。
  async function loadPage(
    pageNum: number,
    query = searchQuery,
    market = marketFilter,
    instrumentType = typeFilter,
    profileStatus = profileStatusFilter,
  ) {
    const keyword = query.trim();
    const params = new URLSearchParams();
    if (market) params.set("market", market);
    if (instrumentType) params.set("instrumentType", instrumentType);
    if (profileStatus) params.set("profileStatus", profileStatus);
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
        void loadSupplementalColumns(items);
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
        void loadSupplementalColumns(items);
      }
    } catch (e) {
      actions.setToast(`加载失败：${friendlyError(e)}`, "danger");
    } finally {
      setLoading(false);
    }
  }

  async function loadSupplementalColumns(items: StockV2Instrument[]) {
    const requestID = ++supplementRequestRef.current;
    const symbols = uniqueSymbols(items);
    if (symbols.length === 0) {
      setAssetSummaries({});
      return;
    }

    const cachedSummaries: Record<string, StockV2AssetSummary> = {};
    const missingSymbols: string[] = [];
    const now = Date.now();

    for (const symbol of symbols) {
      const summary = readFreshCache(assetSummaryCache, symbol, now);
      if (summary) {
        cachedSummaries[symbol] = summary;
      } else {
        missingSymbols.push(symbol);
      }
    }

    setAssetSummaries(cachedSummaries);
    if (missingSymbols.length === 0) {
      setSupplementLoading(false);
      return;
    }

    setSupplementLoading(true);
    try {
      const res = await actions.api<{ items?: Record<string, StockV2AssetSummary> }>(
        `/api/stockv2/assets/summaries?symbols=${encodeURIComponent(missingSymbols.join(","))}`,
      );
      const summaries = res.items ?? {};
      for (const [symbol, summary] of Object.entries(summaries)) {
        writeFreshCache(assetSummaryCache, symbol, summary);
      }
      if (requestID === supplementRequestRef.current) {
        setAssetSummaries({ ...cachedSummaries, ...summaries });
      }
    } catch {
      if (requestID === supplementRequestRef.current) {
        setAssetSummaries(cachedSummaries);
      }
    } finally {
      if (requestID === supplementRequestRef.current) {
        setSupplementLoading(false);
      }
    }
  }

  // 初始加载 + 搜索防抖刷新
  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadPage(0, searchQuery, marketFilter, typeFilter, profileStatusFilter);
    }, searchQuery.trim() ? 250 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery, marketFilter, typeFilter, profileStatusFilter]);

  // 当更新状态从 running 变为完成时，刷新第一页
  const wasRunningRef = useRef(false);
  useEffect(() => {
    const isRunning = !!runningJob;
    if (wasRunningRef.current && !isRunning) {
      clearInstrumentSupplementCache();
      void loadPage(0);
    }
    wasRunningRef.current = isRunning;
  }, [runningJob]);

  // 基础维护运行时保持原有实时轮询；基础完成后若 AI 队列仍活跃，
  // 复用同一 snapshot 低频刷新，不增加逐任务或逐标的请求。
  useEffect(() => {
    const progressVisible = masterDataView === "overview" || masterDataView === "maintenance";
    const intervalMs = runningJob ? 2_000 : aiProgressActive && progressVisible ? 30_000 : 0;
    if (intervalMs === 0) return;
    const timer = setInterval(async () => {
      try {
        await actions.refreshStockV2();
      } catch {
        // 静默
      }
    }, intervalMs);
    return () => clearInterval(timer);
  }, [runningJob?.id, aiProgressActive, masterDataView, actions]);

  // readiness 汇总跟随任务和 AI 计数变化，但最多每 30 秒重算一次；
  // 维护中的 2 秒 snapshot 轮询不会放大全市场本地查询。
  useEffect(() => {
    if (masterDataView !== "overview") return;
    const elapsed = Date.now() - readinessLastLoadedAtRef.current;
    const delay = readinessLastLoadedAtRef.current === 0
      ? 0
      : Math.max(0, READINESS_REFRESH_MIN_INTERVAL_MS - elapsed);
    readinessRefreshTimerRef.current = window.setTimeout(() => {
      readinessRefreshTimerRef.current = null;
      void loadReadinessOverview();
    }, delay);
    return () => {
      if (readinessRefreshTimerRef.current !== null) {
        window.clearTimeout(readinessRefreshTimerRef.current);
        readinessRefreshTimerRef.current = null;
      }
    };
  }, [masterDataView, latestJob?.id, latestJob?.status, aiProgressSignature, loadReadinessOverview]);

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
      <SubTabs
        activeId={masterDataView}
        onChange={(id) => setMasterDataView(id as MasterDataView)}
        tabs={[
          { id: "overview", label: "资产总览" },
          { id: "maintenance", label: "维护任务" },
          { id: "maintenanceSettings", label: "维护配置" },
          { id: "announcements", label: "公告 / 重大事项" },
          { id: "profileRecords", label: "画像记录" },
        ]}
      />

      {masterDataView === "maintenance" ? (
        <StockV2MaintenanceTasks
          actions={actions}
          data={data}
          jobs={jobs}
          runAction={runAction}
          runningJob={runningJob}
          onTriggerUpdate={handleTriggerUpdate}
        />
      ) : null}
      {masterDataView === "maintenanceSettings" ? (
        <StockV2Settings actions={actions} data={data} runAction={runAction} />
      ) : null}
      {masterDataView === "announcements" ? <StockV2AnnouncementsPanel actions={actions} /> : null}
      {masterDataView === "profileRecords" ? <StockV2ProfileRecords actions={actions} /> : null}
      {masterDataView !== "overview" ? null : (
      <>
      {/* 进度条区域：始终展示占位，运行时有动效 */}
      <Panel
        title="数据资产维护进度"
        subtitle={
          runningJob
            ? `基础数据正在维护 ${runningJob.totalCount || runningJob.processedCount} 只标的，${stockV2TriggerTypeLabel(runningJob.triggerType)}`
            : latestJob && stockV2AIProgressActive(latestJob)
              ? `基础维护已结束，AI 画像队列继续处理，${formatCompactTime(latestJob.startAt)}`
              : latestJob
                ? `上次基础维护：${stockV2UpdateStatusLabel(latestJob)}，${stockV2TriggerTypeLabel(latestJob.triggerType)}，${formatCompactTime(latestJob.startAt)}`
              : "尚未执行过维护"
        }
        actions={
          <div className="flex gap-2">
            <Button onClick={() => setHistoryOpen(true)}>
              <ClockCounterClockwise size={14} className="mr-1.5" />
              维护历史
            </Button>
            <Button tone="primary" onClick={() => void runAction("触发数据资产维护", handleTriggerUpdate)} disabled={!!runningJob}>
              <ArrowClockwise size={14} className="mr-1.5" />
              {runningJob ? "维护中..." : "立即维护"}
            </Button>
          </div>
        }
      >
        {runningJob || latestJob ? (
          <StockV2MaintenanceProgress job={runningJob || latestJob} />
        ) : (
          <div className="text-sm text-[var(--muted)]">首次运行后，这里会分别显示基础数据和 AI 画像进度。</div>
        )}
        <StockV2ReadinessOverview
          error={readinessOverviewError}
          loading={readinessOverviewLoading}
          onRefresh={handleReadinessOverviewRefresh}
          overview={readinessOverview}
        />
      </Panel>

      {/* 标的列表 */}
      <Panel
        title={`标的数据资产 (${totalCount > 0 ? totalCount : "..."})`}
        subtitle="证券字典、最新行情、日 K 覆盖和画像状态的统一入口"
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
            <select
              value={profileStatusFilter}
              onChange={(e) => setProfileStatusFilter(e.target.value)}
              className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              aria-label="按画像状态过滤"
            >
              <option value="">全部画像</option>
              <option value="basic_ready">基础就绪</option>
              <option value="basic_partial">基础部分</option>
              <option value="basic_missing">基础缺失</option>
              <option value="ai_ready">AI 就绪</option>
              <option value="ai_failed">AI 失败</option>
              <option value="ai_not_configured">AI 未配置</option>
              <option value="ai_missing">AI 缺失</option>
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
            {isSearching ? `未找到「${searchQuery.trim()}」匹配的标的。` : "暂无标的数据，点击右上角「立即维护」开始首次同步。"}
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
                    <th className="py-2 pr-4 font-medium">日 K 覆盖</th>
                    <th className="py-2 pr-4 font-medium">基础画像</th>
                    <th className="py-2 pr-4 font-medium">公告 / 重大事项</th>
                    <th className="py-2 pr-4 font-medium">AI 状态</th>
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
                      summary={assetSummaries[inst.symbol]}
                      supplementLoading={supplementLoading}
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

      {/* 维护历史弹窗 */}
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

function StockV2MaintenanceTasks({
  actions,
  data,
  jobs,
  runAction,
  runningJob,
  onTriggerUpdate,
}: {
  actions: AppActions;
  data: AppData;
  jobs: StockV2UpdateJob[];
  runAction: RunAction;
  runningJob?: StockV2UpdateJob;
  onTriggerUpdate: () => Promise<void>;
}) {
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null);
  const [jobItems, setJobItems] = useState<Record<string, StockV2AssetMaintenanceItem[]>>({});
  const [itemsLoading, setItemsLoading] = useState(false);

  async function toggleJob(job: StockV2UpdateJob) {
    if (expandedJobId === job.id) {
      setExpandedJobId(null);
      return;
    }
    setExpandedJobId(job.id);
    if (jobItems[job.id]) return;
    setItemsLoading(true);
    try {
      const res = await actions.api<{ items?: StockV2AssetMaintenanceItem[] }>(
        `/api/stockv2/update/jobs/${encodeURIComponent(job.id)}/items?limit=300`,
      );
      setJobItems((prev) => ({ ...prev, [job.id]: res.items ?? [] }));
    } catch (e) {
      actions.setToast(`加载维护明细失败：${friendlyError(e)}`, "danger");
    } finally {
      setItemsLoading(false);
    }
  }

  return (
    <div className="grid gap-4">
      <Panel
        title="数据资产维护任务"
        subtitle="日 K 缺口补齐、基础画像、公告/重大事项和 AI 总结触发的统一管线"
        actions={
          <Button tone="primary" onClick={() => void runAction("触发数据资产维护", onTriggerUpdate)} disabled={!!runningJob}>
            <ArrowClockwise size={14} className="mr-1.5" />
            {runningJob ? "维护中..." : "立即维护"}
          </Button>
        }
      >
        {jobs.length === 0 ? (
          <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-6 text-center text-sm text-[var(--muted)]">
            暂无数据资产维护记录。点击“立即维护”会拉取全量标的列表、最新价，并按需补齐日 K。
          </div>
        ) : (
          <div className="grid gap-2">
            {jobs.map((job) => (
              <div
                className={`rounded-lg border bg-[var(--surface)] p-3 ${
                  job.status === "running" ? "border-[rgba(199,85,8,0.28)]" : "border-[var(--line)]"
                }`}
                key={job.id}
              >
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <Pill tone={stockV2UpdateStatusTone(job)}>
                        {stockV2UpdateStatusLabel(job)}
                      </Pill>
                      <span className="text-sm font-medium">{stockV2TriggerTypeLabel(job.triggerType)}</span>
                      {job.triggerSource ? <Pill tone="neutral">{job.triggerSource}</Pill> : null}
                    </div>
                    <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
                      <span>总数 {job.totalCount || job.processedCount || "-"}</span>
                      <span>已处理 {job.processedCount}</span>
                      <span className="text-[var(--good)]">成功 {job.successCount}</span>
                      <span className={job.failedCount > 0 ? "text-[var(--danger)]" : ""}>
                        失败{" "}
                        {job.failedCount > 0 && job.failedItems?.length ? (
                          <button className="font-semibold underline decoration-dotted underline-offset-2" onClick={() => void toggleJob(job)} type="button">{job.failedCount}</button>
                        ) : (
                          job.failedCount
                        )}
                      </span>
                      {job.endAt ? <span>耗时 {formatDuration(job.startAt, job.endAt)}</span> : null}
                    </div>
                    <div className="mt-3">
                      <StockV2MaintenanceProgress job={job} compact />
                    </div>
                    {job.assetStats ? (
                      <div className="mt-2 flex flex-wrap gap-1">
                        <Pill tone="neutral">日 K {job.assetStats.dailyBarFetched}</Pill>
                        <Pill tone="neutral">基础更新 {job.assetStats.baseProfileUpdated}</Pill>
                        <Pill tone="neutral">新公告 {job.assetStats.announcementsNew}</Pill>
                        <Pill tone={job.assetStats.majorAnnouncementsNew > 0 ? "warn" : "neutral"}>重大 {job.assetStats.majorAnnouncementsNew}</Pill>
                      </div>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <span className="text-xs text-[var(--muted)]">{formatDateTime(job.startAt)}</span>
                    <Button onClick={() => void toggleJob(job)}>{expandedJobId === job.id ? "收起" : "明细"}</Button>
                  </div>
                </div>

                {expandedJobId === job.id ? (
                  <div className="mt-3 rounded border border-[var(--line)] bg-[var(--surface-soft)] p-3">
                    {itemsLoading && !jobItems[job.id] ? (
                      <div className="py-4 text-sm text-[var(--muted)]">读取维护明细...</div>
                    ) : jobItems[job.id]?.length ? (
                      <div className="overflow-x-auto">
                        <table className="w-full text-xs">
                          <thead>
                            <tr className="border-b border-[var(--line)] text-left text-[var(--muted)]">
                              <th className="py-2 pr-3 font-medium">标的</th>
                              <th className="py-2 pr-3 font-medium">日 K</th>
                              <th className="py-2 pr-3 font-medium">基础画像</th>
                              <th className="py-2 pr-3 font-medium">公告</th>
                              <th className="py-2 pr-3 font-medium">重大</th>
                              <th className="py-2 pr-3 font-medium">AI</th>
                              <th className="py-2 pr-3 font-medium">耗时</th>
                              <th className="py-2 pr-0 font-medium">错误</th>
                            </tr>
                          </thead>
                          <tbody>
                            {jobItems[job.id].map((item) => (
                              <tr className="border-b border-[var(--line-soft)] last:border-b-0" key={item.id}>
                                <td className="py-2 pr-3 font-mono text-[var(--text)]">{item.symbol}</td>
                                <td className="py-2 pr-3">{item.dailyBarStatus || "-"} {item.dailyBarFetched ? `+${item.dailyBarFetched}` : ""}</td>
                                <td className="py-2 pr-3">{item.baseProfileStatus || "-"}{item.baseProfileChanged ? " · changed" : ""}</td>
                                <td className="py-2 pr-3">{item.announcementsNew}</td>
                                <td className="py-2 pr-3">{item.majorAnnouncementsNew}</td>
                                <td className="py-2 pr-3">
                                  <div className="grid gap-0.5">
                                    <span className={item.agentRunId ? "font-mono" : ""}>{item.aiDecision || "-"}</span>
                                    {item.aiQueueStatus || item.aiProfileStatus ? (
                                      <span className="text-[var(--muted)]">{assetAIItemStatusLabel(item)}</span>
                                    ) : null}
                                  </div>
                                </td>
                                <td className="py-2 pr-3">{formatMs(item.durationMs)}</td>
                                <td className="max-w-[280px] truncate py-2 pr-0 text-[var(--danger)]">{item.errorMessage || ""}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    ) : job.failedItems?.length ? (
                      <div className="grid max-h-40 gap-1 overflow-y-auto">
                        {job.failedItems.map((item, idx) => (
                          <div className="flex gap-2 text-xs" key={idx}>
                            <span className="font-mono text-[var(--text)]">{item.symbol}</span>
                            <span className="truncate text-[var(--muted-strong)]">{item.reason}</span>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <div className="py-3 text-sm text-[var(--muted)]">暂无明细记录。</div>
                    )}
                  </div>
                ) : null}

                {job.errorMessage ? (
                  <div className="mt-3 rounded border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] px-2 py-1.5 text-xs text-[var(--danger)]">
                    {job.errorMessage}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Panel>

      <StockV2DailyBarsMaintenance actions={actions} data={data} runAction={runAction} />
    </div>
  );
}

function StockV2AnnouncementsPanel({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2Announcement[]>([]);
  const [symbol, setSymbol] = useState("");
  const [majorOnly, setMajorOnly] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function loadAnnouncements() {
    const params = new URLSearchParams();
    params.set("limit", "80");
    if (symbol.trim()) params.set("symbol", symbol.trim());
    if (majorOnly) params.set("majorOnly", "true");
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<{ items?: StockV2Announcement[] }>(`/api/stockv2/announcements?${params.toString()}`);
      setItems(res.items ?? []);
    } catch (e) {
      setError(friendlyError(e));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadAnnouncements();
    }, symbol.trim() ? 250 : 0);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [symbol, majorOnly]);

  return (
    <Panel
      title="公告 / 重大事项"
      subtitle="独立公告资产表，按来源、标的和内容 hash 去重"
      actions={
        <Button onClick={() => void loadAnnouncements()} disabled={loading}>
          <ArrowClockwise size={14} className="mr-1.5" />
          刷新
        </Button>
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative w-[260px] max-w-full">
          <MagnifyingGlass size={15} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
          <input
            type="search"
            value={symbol}
            onChange={(event) => setSymbol(event.target.value)}
            placeholder="按标的代码过滤"
            className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
          />
        </div>
        <label className="flex items-center gap-2 text-sm text-[var(--muted-strong)]">
          <input
            type="checkbox"
            checked={majorOnly}
            onChange={(event) => setMajorOnly(event.target.checked)}
          />
          仅重大事项
        </label>
      </div>
      {error ? (
        <div className="rounded border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] px-3 py-2 text-sm text-[var(--danger)]">
          {error}
        </div>
      ) : loading && items.length === 0 ? (
        <div className="py-8 text-center text-sm text-[var(--muted)]">读取中...</div>
      ) : items.length === 0 ? (
        <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-6 text-center text-sm text-[var(--muted)]">
          暂无公告记录。维护单只标的或运行数据资产维护后会在这里出现。
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--line)] text-left text-xs text-[var(--muted)]">
                <th className="py-2 pr-4 font-medium">标的</th>
                <th className="py-2 pr-4 font-medium">发布日期</th>
                <th className="py-2 pr-4 font-medium">标题</th>
                <th className="py-2 pr-4 font-medium">分类</th>
                <th className="py-2 pr-4 font-medium">重大事项</th>
                <th className="py-2 pr-2 text-right font-medium">来源</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr className="border-b border-[var(--line-soft)] last:border-b-0" key={item.id}>
                  <td className="py-2 pr-4 font-mono">{item.symbol}</td>
                  <td className="py-2 pr-4 text-xs text-[var(--muted)]">{formatCompactTime(item.publishedAt || item.fetchedAt)}</td>
                  <td className="max-w-[520px] py-2 pr-4">
                    <span className="block truncate">{item.title}</span>
                  </td>
                  <td className="py-2 pr-4 text-xs text-[var(--muted-strong)]">{item.category || "-"}</td>
                  <td className="py-2 pr-4">
                    <Pill tone={item.major ? "warn" : "neutral"}>{item.major ? item.majorReason || "重大" : "普通"}</Pill>
                  </td>
                  <td className="py-2 pl-2 text-right">
                    {item.pdfUrl ? (
                      <a className="text-xs text-[var(--accent)] hover:underline" href={item.pdfUrl} rel="noreferrer" target="_blank">
                        PDF
                      </a>
                    ) : (
                      <span className="text-xs text-[var(--muted)]">{item.source}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function StockRow({
  inst,
  onAdd,
  onClick,
  summary,
  supplementLoading,
}: {
  inst: StockV2Instrument;
  onAdd: () => void;
  onClick?: () => void;
  summary?: StockV2AssetSummary;
  supplementLoading?: boolean;
}) {
  const marketLabel = { SH: "沪市", SZ: "深市", BJ: "北市" }[inst.market] || inst.market;
  const dailyQuality = summary?.dailyBarQuality;
  const profile = summary?.profileSummary;
  const profileTone = profile?.status === "ready" ? "good" : profile?.status === "partial" ? "warn" : "neutral";
  const aiTone = profile?.aiProfileStatus === "ready" ? "good" : profile?.aiProfileStatus === "failed" ? "danger" : "neutral";
  const readiness = summary?.readiness;
  const dailyTone = stockV2DailyBarsQualityTone(dailyQuality);
  const majorCount = summary?.majorAnnouncementCount ?? 0;
  const announcementCount = summary?.announcementCount ?? 0;

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
      <td className="py-2 pr-4">
        <div className="grid gap-1">
          <Pill tone={dailyQuality ? dailyTone : "neutral"}>
            {dailyQuality ? stockV2DailyBarsQualityLabel(dailyQuality) : supplementLoading ? "读取中" : "未评估"}
          </Pill>
          <span className="text-xs text-[var(--muted)]">
            {dailyQuality?.latestDate ? `最近 ${dailyQuality.latestDate}` : "本地未覆盖"}
          </span>
        </div>
      </td>
      <td className="max-w-[320px] py-2 pr-4 text-xs text-[var(--muted-strong)]">
        <span className="block max-h-10 overflow-hidden leading-5">{profile?.businessSummary || (supplementLoading ? "读取中..." : "-")}</span>
      </td>
      <td className="py-2 pr-4">
        <div className="grid gap-1">
          <div className="flex flex-wrap gap-1">
            <Pill tone={announcementCount > 0 ? "neutral" : "neutral"}>{supplementLoading && !summary ? "读取中" : `公告 ${announcementCount}`}</Pill>
            <Pill tone={majorCount > 0 ? "warn" : "neutral"}>重大 {majorCount}</Pill>
          </div>
          <span className="block max-w-[260px] truncate text-xs text-[var(--muted)]">
            {summary?.latestAnnouncementTitle || "暂无公告记录"}
          </span>
        </div>
      </td>
      <td className="py-2 pr-4">
        <div className="flex flex-wrap gap-1">
          <Pill tone={profile ? profileTone : "neutral"}>
            {profile ? profile.status === "ready" ? "基础就绪" : profile.status === "partial" ? "基础部分" : "基础缺失" : supplementLoading ? "读取中" : "基础缺失"}
          </Pill>
          <Pill tone={profile ? aiTone : "neutral"}>AI {profile?.aiProfileStatus || (supplementLoading ? "读取中" : "missing")}</Pill>
          {readiness ? (
            <Pill tone={readiness.ready ? "good" : readiness.dataReady ? "warn" : "neutral"}>
              {readiness.ready ? "资产可用" : readiness.dataReady ? "AI 待完成" : "数据待刷新"}
            </Pill>
          ) : null}
        </div>
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

function assetAIItemStatusLabel(item: StockV2AssetMaintenanceItem): string {
  switch (item.aiQueueStatus) {
    case "ready": return "排队中";
    case "running": return "运行中";
    case "retry_wait": return "等待重试";
    case "completed": return "已完成";
    case "failed": return "失败";
  }
  switch (item.aiProfileStatus) {
    case "queued": return "排队中";
    case "running": return "运行中";
    case "ready": return "已完成";
    case "failed": return "失败";
    case "not_configured": return "未配置";
    default: return item.aiProfileStatus || "";
  }
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
            <h3 className="m-0 text-base font-semibold">维护历史记录</h3>
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
                        <span className="text-xs text-[var(--muted)]">{job.triggerSource}</span>
                      ) : null}
                    </div>
                    <span className="text-xs text-[var(--muted)]">
                      {formatDateTime(job.startAt)}
                    </span>
                  </div>

                  <div className="mt-3">
                    <StockV2MaintenanceProgress job={job} compact />
                  </div>
                  {job.failedCount > 0 && job.failedItems?.length ? (
                    <button
                      onClick={() => toggleFailed(job)}
                      className="mt-2 text-left text-xs font-semibold text-[var(--danger)] underline decoration-dotted underline-offset-2"
                      title="点击查看基础维护失败详情"
                      type="button"
                    >
                      {expandedJobId === job.id ? "收起基础维护失败详情" : `查看基础维护失败详情 (${job.failedCount})`}
                    </button>
                  ) : null}

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

function formatMs(ms?: number): string {
  if (!ms || ms <= 0) return "-";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} 秒`;
}
