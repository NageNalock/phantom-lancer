import { ArrowsClockwise, Database, MagnifyingGlass, Plus, ShieldCheck } from "@phosphor-icons/react";
import { useEffect, useState, type FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockDataMaintenanceResult, StockDataSource, StockInstrument, StockInstrumentSearchResponse } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Field, Metric, Notice, Panel, Pill, SubTabs } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { StockDataMaintenanceInspector } from "./StockDataMaintenanceInspector";
import { StockSymbolCombobox } from "./StockSymbolCombobox";
import { number, text } from "./format";

type StockDataTab = "instruments" | "sources" | "manual" | "market" | "news";

const STOCK_DATA_TABS: Array<{ id: StockDataTab; label: string }> = [
  { id: "instruments", label: "股票列表" },
  { id: "sources", label: "数据源" },
  { id: "manual", label: "主数据录入" },
  { id: "market", label: "历史指标" },
  { id: "news", label: "消息面" },
];

export function StockDataWorkbench({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const sources = data.stock.dataSources || [];
  const defaultSource = sources[0]?.source || "manual_seed";
  const recentInstruments = data.stock.instruments || [];
  const [lastMaintenanceRun, setLastMaintenanceRun] = useState<StockDataMaintenanceResult | null>(null);
  const [activeDataTab, setActiveDataTab] = useState<StockDataTab>("instruments");

  async function saveSource(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const authMode = text(form, "authMode") || "none";
    await runAction("已保存数据源", async () => {
      await actions.api("/api/stock/data-sources", {
        method: "POST",
        body: {
          source: text(form, "source"),
          displayName: text(form, "displayName"),
          sourceType: text(form, "sourceType") || "market_data",
          authMode,
          enabled: authMode !== "disabled",
          rateLimitSeconds: number(form, "rateLimitSeconds") || 60,
        },
      });
      formElement.reset();
      await actions.refreshStock();
    });
  }

  async function checkSource(source: StockDataSource) {
    await runAction("数据源健康检查完成", async () => {
      await actions.api(`/api/stock/data-sources/${source.source}/health-check`, { method: "POST", body: {} });
      await actions.refreshStock();
    });
  }

  async function checkQuoteRefresh() {
    await runAction("已检查行情刷新状态", async () => {
      await actions.api("/api/stock/quotes/refresh", { method: "POST", body: {} });
    });
  }

  async function runDataMaintenance() {
    await runAction("已执行数据维护任务", async () => {
      const result = await actions.api<StockDataMaintenanceResult>("/api/stock/data-tasks/run", { method: "POST", body: {} });
      setLastMaintenanceRun(result);
      await actions.refreshStock();
    });
  }

  async function saveInstrument(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    await runAction("已刷新股票主数据", async () => {
      await actions.api("/api/stock/instruments", {
        method: "POST",
        body: {
          source: text(form, "source") || defaultSource,
          items: [{
            symbol: text(form, "symbol"),
            market: text(form, "market"),
            name: text(form, "name"),
            status: text(form, "status") || "listed",
            industry: text(form, "industry"),
            concept: text(form, "concept"),
            listingDate: text(form, "listingDate"),
            quality: text(form, "quality") || "fresh",
          }],
        },
      });
      formElement.reset();
      await actions.refreshStock();
    });
  }

  async function saveMarketData(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    await runAction("已写入历史/指标数据", async () => {
      await actions.api("/api/stock/market-data", {
        method: "POST",
        body: {
          source: text(form, "source") || defaultSource,
          points: [{
            symbol: text(form, "symbol"),
            market: text(form, "market"),
            dataset: text(form, "dataset") || "daily_kline",
            dataDate: text(form, "dataDate"),
            open: number(form, "open"),
            high: number(form, "high"),
            low: number(form, "low"),
            close: number(form, "close"),
            volume: number(form, "volume"),
            amount: number(form, "amount"),
            pe: number(form, "pe"),
            pb: number(form, "pb"),
            turnoverRate: number(form, "turnoverRate"),
            netInflow: number(form, "netInflow"),
            quality: text(form, "quality") || "fresh",
          }],
        },
      });
      formElement.reset();
      await actions.refreshStock();
    });
  }

  async function ingestNews(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    await runAction("已采集消息面数据", async () => {
      await actions.api("/api/stock/news/ingest", {
        method: "POST",
        body: {
          source: text(form, "source") || defaultSource,
          items: [{
            sourceItemId: text(form, "sourceItemId"),
            symbol: text(form, "symbol"),
            market: text(form, "market"),
            title: text(form, "title"),
            summary: text(form, "summary"),
            category: text(form, "category"),
            importance: text(form, "importance") || "normal",
            keywords: text(form, "keywords"),
            quality: text(form, "quality") || "fresh",
            publishedAt: text(form, "publishedAt") ? new Date(text(form, "publishedAt")).toISOString() : "",
          }],
        },
      });
      formElement.reset();
      await actions.refreshStock();
    });
  }

  return (
    <div className="grid gap-4">
      <section className="grid grid-cols-4 gap-3 max-2xl:grid-cols-2 max-sm:grid-cols-1">
        <Metric label="数据源" value={data.stock.dataHealth?.sourceCount || 0} detail={`${data.stock.dataHealth?.availableSources || 0} 可用`} tone={data.stock.dataHealth?.failedSources ? "warn" : "neutral"} />
        <Metric label="主数据" value={data.stock.dataHealth?.instrumentCount || 0} detail="股票代码与基础盘" />
        <Metric label="指标点" value={data.stock.dataHealth?.marketPointCount || 0} detail={`${data.stock.dataCoverage?.length || 0} 个覆盖面`} />
        <Metric label="消息" value={data.stock.dataHealth?.newsItemCount || 0} detail={`${data.stock.dataHealth?.importantNewsCount || 0} 条重要`} tone={data.stock.dataHealth?.importantNewsCount ? "warn" : "neutral"} />
      </section>

      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 max-md:grid-cols-1">
        <Notice>周期数据维护会按数据源 next_allowed_at、失败退避和限流执行：行情只落盘为 quote_snapshot / quote_derived_kline；真实 daily_kline 只来自历史/日级数据源。</Notice>
        <div className="flex flex-wrap justify-end gap-2">
          <Button onClick={() => void runDataMaintenance()} tone="primary"><ArrowsClockwise size={15} />运行数据维护</Button>
          <Button onClick={() => void checkQuoteRefresh()}><ShieldCheck size={15} />检查行情刷新</Button>
        </div>
      </div>

      <StockDataMaintenanceInspector data={data.stock} lastRun={lastMaintenanceRun} />

      <SubTabs
        activeId={activeDataTab}
        ariaLabel="股票数据二级导航"
        onChange={(id) => setActiveDataTab(id as StockDataTab)}
        rightSlot={lastMaintenanceRun ? <Pill tone={lastMaintenanceRun.tasks?.some((task) => task.status === "failed") ? "warn" : "good"}>{lastMaintenanceRun.tasks?.length || 0} 任务</Pill> : null}
        tabs={STOCK_DATA_TABS}
      />

      {activeDataTab === "instruments" ? (
        <StockInstrumentList actions={actions} data={data} runAction={runAction} />
      ) : null}

      {activeDataTab === "sources" ? (
        <DataSourceGovernance
          checkSource={checkSource}
          data={data}
          defaultSource={defaultSource}
          saveSource={saveSource}
          sources={sources}
        />
      ) : null}

      {activeDataTab === "manual" ? (
        <Panel title="刷新股票主数据" subtitle="用于少量手动校正；全量 A 股初始化请在股票列表中执行。">
          <form className="grid gap-3" onSubmit={(event) => void saveInstrument(event)}>
            <SourceSelect defaultSource={defaultSource} sources={sources} />
            <StockSymbolCombobox actions={actions} label="股票" recent={recentInstruments} required />
            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="行业"><input className="input" name="industry" /></Field>
              <Field label="概念"><input className="input" name="concept" /></Field>
            </div>
            <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
              <Field label="上市日期"><input className="input" name="listingDate" type="date" /></Field>
              <Field label="状态">
                <select className="select" name="status" defaultValue="listed">
                  <option value="listed">已上市</option>
                  <option value="suspended">停牌</option>
                  <option value="delisted">退市</option>
                </select>
              </Field>
              <Field label="质量"><QualitySelect /></Field>
            </div>
            <div><Button tone="primary" type="submit"><Plus size={15} />刷新主数据</Button></div>
          </form>
        </Panel>
      ) : null}

      {activeDataTab === "market" ? (
        <div className="grid grid-cols-[minmax(0,1fr)_360px] gap-4 max-xl:grid-cols-1">
          <Panel title="历史 K 线 / 估值 / 资金流">
            <form className="grid gap-3" onSubmit={(event) => void saveMarketData(event)}>
              <SourceSelect defaultSource={defaultSource} sources={sources} />
              <div className="grid grid-cols-[minmax(0,1fr)_220px_180px] gap-3 max-xl:grid-cols-1">
                <StockSymbolCombobox actions={actions} label="股票" recent={recentInstruments} required />
                <Field label="数据集">
                  <select className="select" name="dataset" defaultValue="daily_kline">
                    <option value="daily_kline">日 K 线 (daily_kline)</option>
                    <option value="quote_derived_kline">行情派生 K (quote_derived_kline)</option>
                    <option value="valuation">估值 (valuation)</option>
                    <option value="fund_flow">资金流 (fund_flow)</option>
                    <option value="quote_snapshot">行情快照 (quote_snapshot)</option>
                  </select>
                </Field>
                <Field label="日期"><input className="input" name="dataDate" required type="date" /></Field>
              </div>
              <div className="grid grid-cols-4 gap-3 max-md:grid-cols-2">
                <Field label="开"><input className="input" min="0" name="open" step="0.001" type="number" /></Field>
                <Field label="高"><input className="input" min="0" name="high" step="0.001" type="number" /></Field>
                <Field label="低"><input className="input" min="0" name="low" step="0.001" type="number" /></Field>
                <Field label="收"><input className="input" min="0" name="close" step="0.001" type="number" /></Field>
              </div>
              <div className="grid grid-cols-4 gap-3 max-md:grid-cols-2">
                <Field label="成交量"><input className="input" min="0" name="volume" step="1" type="number" /></Field>
                <Field label="成交额"><input className="input" min="0" name="amount" step="0.01" type="number" /></Field>
                <Field label="PE"><input className="input" min="0" name="pe" step="0.01" type="number" /></Field>
                <Field label="PB"><input className="input" min="0" name="pb" step="0.01" type="number" /></Field>
              </div>
              <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
                <Field label="换手率"><input className="input" min="0" name="turnoverRate" step="0.01" type="number" /></Field>
                <Field label="资金净流入"><input className="input" name="netInflow" step="0.01" type="number" /></Field>
                <Field label="质量"><QualitySelect /></Field>
              </div>
              <div><Button tone="primary" type="submit"><Plus size={15} />写入指标</Button></div>
            </form>
          </Panel>
          <DataCoveragePanel data={data} />
        </div>
      ) : null}

      {activeDataTab === "news" ? (
        <div className="grid grid-cols-[minmax(0,1fr)_420px] gap-4 max-xl:grid-cols-1">
          <Panel title="消息面采集">
            <form className="grid gap-3" onSubmit={(event) => void ingestNews(event)}>
              <SourceSelect defaultSource={defaultSource} sources={sources} />
              <div className="grid grid-cols-[minmax(0,1fr)_220px] gap-3 max-xl:grid-cols-1">
                <StockSymbolCombobox actions={actions} allowFreeInput label="关联股票" recent={recentInstruments} />
                <Field label="来源 ID"><input className="input mono" name="sourceItemId" /></Field>
              </div>
              <Field label="标题"><input className="input" name="title" required /></Field>
              <Field label="摘要"><textarea className="textarea" name="summary" /></Field>
              <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
                <Field label="类别"><input className="input" name="category" placeholder="policy / earnings" /></Field>
                <Field label="重要性">
                  <select className="select" name="importance" defaultValue="normal">
                    <option value="low">低</option>
                    <option value="normal">普通</option>
                    <option value="high">高</option>
                    <option value="urgent">紧急</option>
                  </select>
                </Field>
                <Field label="发布时间"><input className="input" name="publishedAt" type="datetime-local" /></Field>
              </div>
              <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
                <Field label="关键词"><input className="input" name="keywords" /></Field>
                <Field label="质量"><QualitySelect /></Field>
              </div>
              <div><Button tone="primary" type="submit"><Plus size={15} />采集消息</Button></div>
            </form>
          </Panel>
          <RecentNewsAndTasks data={data} />
        </div>
      ) : null}
    </div>
  );
}

function StockInstrumentList({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const [query, setQuery] = useState("");
  const [market, setMarket] = useState("all");
  const [status, setStatus] = useState("listed");
  const [industry, setIndustry] = useState("");
  const [quality, setQuality] = useState("all");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);
  const [result, setResult] = useState<StockInstrumentSearchResponse | null>(null);
  const [industries, setIndustries] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refreshToken, setRefreshToken] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void actions.api<{ items?: string[] }>("/api/stock/instruments/industries")
      .then((res) => {
        if (!cancelled) setIndustries(res.items || []);
      })
      .catch(() => {
        if (!cancelled) setIndustries([]);
      });
    return () => {
      cancelled = true;
    };
  }, [actions]);

  useEffect(() => {
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      const params = new URLSearchParams();
      if (query.trim()) params.set("q", query.trim());
      if (market !== "all") params.append("market", market);
      if (status !== "all") params.append("status", status);
      if (industry) params.set("industry", industry);
      if (quality !== "all") params.set("quality", quality);
      if (status === "all") params.set("include_delisted", "true");
      params.set("sort", query.trim() ? "relevance" : "market_then_symbol");
      params.set("page", String(page));
      params.set("pageSize", String(pageSize));
      setLoading(true);
      setError("");
      try {
        const next = await actions.api<StockInstrumentSearchResponse>(`/api/stock/instruments/search?${params.toString()}`);
        if (!cancelled) setResult(next);
      } catch (err) {
        if (!cancelled) setError(friendlyError(err));
      } finally {
        if (!cancelled) setLoading(false);
      }
    }, 180);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [actions, industry, market, page, pageSize, quality, query, refreshToken, status]);

  function resetPage(fn: () => void) {
    setPage(1);
    fn();
  }

  async function refreshUniverse() {
    await runAction("已执行 A 股主数据初始化", async () => {
      const run = await actions.api<StockDataMaintenanceResult>("/api/stock/instruments", {
        method: "POST",
        body: { source: "eastmoney_universe", auto: true },
      });
      setResult((current) => current ? { ...current, items: run.instruments || current.items } : current);
      await actions.refreshStock();
      setPage(1);
      setRefreshToken((current) => current + 1);
    });
  }

  const total = result?.total || 0;
  const effectivePageSize = result?.pageSize || pageSize;
  const pageCount = Math.max(1, Math.ceil(total / Math.max(1, effectivePageSize)));
  const items = result?.items || [];

  return (
    <Panel
      actions={<Button onClick={() => void refreshUniverse()} tone="primary"><ArrowsClockwise size={15} />全量初始化</Button>}
      subtitle="支持代码、名称、拼音首字母、拼音全拼、行业与概念搜索；分页查询默认不展开退市股票。"
      title="股票主数据"
    >
      <div className="mb-3 grid grid-cols-[minmax(260px,1fr)_140px_150px_180px_140px_120px] gap-2 max-2xl:grid-cols-3 max-xl:grid-cols-2">
        <Field label="搜索">
          <div className="relative">
            <MagnifyingGlass className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-[var(--muted)]" size={15} />
            <input className="input pl-9" onChange={(event) => resetPage(() => setQuery(event.target.value))} placeholder="600519 / 茅台 / mt / maotai" value={query} />
          </div>
        </Field>
        <Field label="市场">
          <select className="select mono" onChange={(event) => resetPage(() => setMarket(event.target.value))} value={market}>
            <option value="all">全部</option>
            <option value="SH">SH</option>
            <option value="SZ">SZ</option>
            <option value="BJ">BJ</option>
          </select>
        </Field>
        <Field label="状态">
          <select className="select" onChange={(event) => resetPage(() => setStatus(event.target.value))} value={status}>
            <option value="listed">已上市</option>
            <option value="suspended">停牌</option>
            <option value="delisted">退市</option>
            <option value="all">全部</option>
          </select>
        </Field>
        <Field label="行业">
          <select className="select" onChange={(event) => resetPage(() => setIndustry(event.target.value))} value={industry}>
            <option value="">全部</option>
            {industries.map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </Field>
        <Field label="质量">
          <select className="select" onChange={(event) => resetPage(() => setQuality(event.target.value))} value={quality}>
            <option value="all">全部</option>
            <option value="fresh">新鲜</option>
            <option value="partial">部分</option>
            <option value="stale">过期</option>
            <option value="failed">失败</option>
            <option value="unknown">未知</option>
          </select>
        </Field>
        <Field label="每页">
          <select className="select mono" onChange={(event) => resetPage(() => setPageSize(Number(event.target.value)))} value={pageSize}>
            <option value={25}>25</option>
            <option value={50}>50</option>
            <option value={100}>100</option>
          </select>
        </Field>
      </div>

      {error ? <Notice tone="danger">{error}</Notice> : null}
      <div className="overflow-hidden rounded-lg border border-[var(--line)]">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[980px] border-collapse text-left text-sm">
            <thead className="bg-[var(--surface-soft)] text-xs text-[var(--muted-strong)]">
              <tr>
                <th className="px-3 py-2 font-medium">代码</th>
                <th className="px-3 py-2 font-medium">名称</th>
                <th className="px-3 py-2 font-medium">市场</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">行业</th>
                <th className="px-3 py-2 font-medium">概念</th>
                <th className="px-3 py-2 font-medium">上市日期</th>
                <th className="px-3 py-2 font-medium">质量</th>
                <th className="px-3 py-2 font-medium">更新时间</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr className="border-t border-[var(--line)] bg-[var(--surface)] hover:bg-[var(--surface-soft)]" key={`${item.market}-${item.symbol}`}>
                  <td className="px-3 py-2 mono font-medium">{item.symbol}</td>
                  <td className="px-3 py-2">
                    <div className="grid gap-0.5">
                      <span>{item.name || "-"}</span>
                      {item.py ? <span className="mono text-xs text-[var(--muted)]">{item.py} {item.pyFull ? `/ ${item.pyFull}` : ""}</span> : null}
                    </div>
                  </td>
                  <td className="px-3 py-2 mono">{item.market || "-"}</td>
                  <td className="px-3 py-2"><Pill tone={stockStatusTone(item.status)}>{stockStatusLabel(item.status)}</Pill></td>
                  <td className="px-3 py-2">{item.industry || "-"}</td>
                  <td className="px-3 py-2">
                    <div className="flex max-w-[260px] flex-wrap gap-1">
                      {conceptTokens(item.concept).map((concept) => <Pill key={concept}>{concept}</Pill>)}
                      {!conceptTokens(item.concept).length ? <span className="muted">-</span> : null}
                    </div>
                  </td>
                  <td className="px-3 py-2 mono text-xs">{item.listingDate || "-"}</td>
                  <td className="px-3 py-2"><Pill tone={qualityTone(item.quality)}>{item.quality || "unknown"}</Pill></td>
                  <td className="px-3 py-2 mono text-xs">{formatDate(item.updatedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!items.length ? (
          <div className="border-t border-[var(--line)] p-6">
            <EmptyState body={loading ? "正在读取股票主数据。" : "可以先执行 A 股全量初始化，或调整搜索条件。"} title={loading ? "加载中" : "暂无股票"} />
          </div>
        ) : null}
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--muted-strong)]">
        <span>第 <span className="mono">{result?.page || page}</span> / <span className="mono">{pageCount}</span> 页 · 总量 <span className="mono">{total}</span>{result?.fts ? " · FTS" : ""}</span>
        <div className="flex items-center gap-2">
          <Button className="min-h-7 px-2 text-xs" disabled={loading || page <= 1} onClick={() => setPage((current) => Math.max(1, current - 1))}>上一页</Button>
          <Button className="min-h-7 px-2 text-xs" disabled={loading || page >= pageCount} onClick={() => setPage((current) => current + 1)}>下一页</Button>
        </div>
      </div>
    </Panel>
  );
}

function DataSourceGovernance({ checkSource, data, defaultSource, saveSource, sources }: { checkSource: (source: StockDataSource) => Promise<void>; data: AppData; defaultSource: string; saveSource: (event: FormEvent<HTMLFormElement>) => void; sources: StockDataSource[] }) {
  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
        <Panel title="数据源治理" subtitle="只登记授权模式和健康状态，不保存示例 token、cookie 或私有 endpoint。">
          <form className="grid gap-3" onSubmit={(event) => void saveSource(event)}>
            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="Source key"><input className="input mono" name="source" placeholder={defaultSource} required /></Field>
              <Field label="显示名"><input className="input" name="displayName" placeholder="Manual Seed" /></Field>
            </div>
            <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
              <Field label="类型">
                <select className="select" name="sourceType" defaultValue="market_data">
                  <option value="market_data">行情数据</option>
                  <option value="news">新闻</option>
                  <option value="report">研报</option>
                  <option value="search">搜索</option>
                  <option value="skill">技能</option>
                  <option value="scheduler">调度器</option>
                </select>
              </Field>
              <Field label="授权模式">
                <select className="select" name="authMode" defaultValue="none">
                  <option value="none">无</option>
                  <option value="user_config">用户配置</option>
                  <option value="api_key">API Key</option>
                  <option value="cookie">Cookie</option>
                  <option value="disabled">已禁用</option>
                </select>
              </Field>
              <Field label="限流秒数"><input className="input" defaultValue="60" min="0" name="rateLimitSeconds" step="1" type="number" /></Field>
            </div>
            <div><Button tone="primary" type="submit"><Database size={15} />保存数据源</Button></div>
          </form>
        </Panel>

        <Panel title="Adapter 配置状态" subtitle="只读取本机环境变量是否存在，不回显 URL、token 或 cookie。">
          <div className="grid gap-2">
            {(data.stock.dataAdapters || []).map((adapter) => (
              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={adapter.key || adapter.source}>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <strong>{adapter.label || adapter.source}</strong>
                    <Pill tone={adapter.configured ? "good" : "warn"}>{adapter.configured ? "已配置" : "缺失"}</Pill>
                    <Pill>{adapter.category || "-"}</Pill>
                  </div>
                  <span className="mono mt-2 block truncate text-xs text-[var(--muted-strong)]">{adapter.key}</span>
                </div>
                <span className="self-center text-xs text-[var(--muted-strong)]">{adapter.source}</span>
              </div>
            ))}
            {!data.stock.dataAdapters?.length ? <EmptyState body="后端未返回 adapter 状态，仍可通过数据源任务查看 blocked reason。" title="暂无 adapter 状态" /> : null}
          </div>
        </Panel>
      </div>

      <Panel title="数据源状态">
        <div className="grid gap-2">
          {sources.map((source) => (
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={source.source}>
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <strong className="mono">{source.source}</strong>
                  <Pill tone={source.status === "available" ? "good" : source.status === "auth_required" || source.status === "disabled" ? "warn" : "neutral"}>{source.status || "unknown"}</Pill>
                  <Pill>{source.authMode || "none"}</Pill>
                </div>
                <p className="muted mt-2 mb-0 text-xs">{source.failureSummary || `cursor ${source.lastCursor || "-"} / next ${source.nextAllowedAt ? formatDate(source.nextAllowedAt) : "-"}`}</p>
              </div>
              <Button onClick={() => void checkSource(source)}><ShieldCheck size={15} />检查</Button>
            </div>
          ))}
          {!sources.length ? <EmptyState body="先登记 manual_seed 或 a_stock_data 这类数据源，再执行刷新任务。" title="暂无数据源" /> : null}
        </div>
      </Panel>
    </div>
  );
}

function DataCoveragePanel({ data }: { data: AppData }) {
  return (
    <Panel title="数据覆盖">
      <div className="grid gap-2">
        {(data.stock.dataCoverage || []).slice(0, 12).map((item) => (
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={`${item.symbol}-${item.dataset}`}>
            <div className="flex flex-wrap items-center gap-2">
              <strong className="mono">{item.symbol}</strong>
              <Pill>{item.dataset || "dataset"}</Pill>
              <Pill tone={item.latestQuality === "fresh" ? "good" : item.latestQuality === "stale" ? "warn" : "neutral"}>{item.latestQuality || "unknown"}</Pill>
            </div>
            <p className="muted mt-2 mb-0 text-xs">{item.firstDate || "-"} 至 {item.lastDate || "-"} / {item.pointCount || 0} 条 / {item.latestSource || "-"}</p>
          </div>
        ))}
        {!data.stock.dataCoverage?.length ? <EmptyState body="写入历史 K 线、估值或资金流后，会自动形成覆盖度。" title="暂无覆盖度" /> : null}
      </div>
    </Panel>
  );
}

function RecentNewsAndTasks({ data }: { data: AppData }) {
  return (
    <Panel title="最近消息与数据任务">
      <div className="grid gap-3">
        {(data.stock.newsItems || []).slice(0, 5).map((item) => (
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={item.id || item.dedupeKey}>
            <div className="flex flex-wrap items-center gap-2">
              <strong>{item.title}</strong>
              <Pill tone={item.importance === "high" || item.importance === "urgent" ? "warn" : "neutral"}>{item.importance || "normal"}</Pill>
              {item.symbol ? <Pill>{item.symbol}</Pill> : null}
            </div>
            <p className="muted mt-2 mb-0 text-xs">{item.summary || item.source}</p>
          </div>
        ))}
        {(data.stock.dataTasks || []).slice(0, 6).map((task) => (
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs" key={task.id}>
            <div className="flex flex-wrap items-center gap-2">
              <strong className="mono text-sm">{task.taskType}</strong>
              <Pill tone={task.status === "completed" ? "good" : task.status === "failed" || task.status === "degraded" ? "warn" : "neutral"}>{task.status || "unknown"}</Pill>
              <span className="muted">{formatDate(task.completedAt || task.createdAt)}</span>
            </div>
            <span className="muted mt-1 block">processed {task.processedCount || 0} / failed {task.failedCount || 0} / next {task.nextRunAt ? formatDate(task.nextRunAt) : "-"}</span>
            {task.failureSummary ? <span className="muted mt-1 block">{task.failureSummary}</span> : null}
          </div>
        ))}
        {!data.stock.newsItems?.length && !data.stock.dataTasks?.length ? <EmptyState body="执行采集或补数后会保留任务记录。" title="暂无数据任务" /> : null}
      </div>
    </Panel>
  );
}

function SourceSelect({ defaultSource, sources }: { defaultSource: string; sources: StockDataSource[] }) {
  return (
    <Field label="数据源">
      <select className="select mono" name="source" defaultValue={defaultSource}>
        {!sources.length ? <option value="manual_seed">manual_seed</option> : null}
        {sources.map((source) => (
          <option key={source.source} value={source.source}>{source.source}</option>
        ))}
      </select>
    </Field>
  );
}

function QualitySelect() {
  return (
    <select className="select" name="quality" defaultValue="fresh">
      <option value="fresh">新鲜 (fresh)</option>
      <option value="stale">过期 (stale)</option>
      <option value="partial">部分 (partial)</option>
      <option value="failed">失败 (failed)</option>
      <option value="unknown">未知 (unknown)</option>
    </select>
  );
}

function stockStatusLabel(status?: string): string {
  switch (status) {
    case "listed": return "已上市";
    case "suspended": return "停牌";
    case "delisted": return "退市";
    default: return status || "unknown";
  }
}

function stockStatusTone(status?: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "listed") return "good";
  if (status === "suspended") return "warn";
  if (status === "delisted") return "danger";
  return "neutral";
}

function qualityTone(quality?: string): "neutral" | "good" | "warn" | "danger" {
  if (quality === "fresh") return "good";
  if (quality === "stale" || quality === "partial") return "warn";
  if (quality === "failed") return "danger";
  return "neutral";
}

function conceptTokens(concept?: string): string[] {
  return (concept || "")
    .split(/[,\s，、]+/)
    .map((item) => item.trim())
    .filter(Boolean)
    .slice(0, 3);
}
