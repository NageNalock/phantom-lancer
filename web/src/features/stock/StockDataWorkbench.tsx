import { ArrowsClockwise, Database, Plus, ShieldCheck } from "@phosphor-icons/react";
import { useState, type FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockDataMaintenanceResult, StockDataSource } from "../../app/types";
import { Button, CollapsibleSection, EmptyState, Field, Metric, Notice, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { StockDataMaintenanceInspector } from "./StockDataMaintenanceInspector";
import { number, text } from "./format";

export function StockDataWorkbench({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const sources = data.stock.dataSources || [];
  const defaultSource = sources[0]?.source || "manual_seed";
  const [lastMaintenanceRun, setLastMaintenanceRun] = useState<StockDataMaintenanceResult | null>(null);

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
    });
  }

  async function checkSource(source: StockDataSource) {
    await runAction("数据源健康检查完成", async () => {
      await actions.api(`/api/stock/data-sources/${source.source}/health-check`, { method: "POST", body: {} });
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

      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
        <Panel title="数据源治理" subtitle="只登记授权模式和健康状态，不保存示例 token、cookie 或私有 endpoint。">
          <form className="grid gap-3" onSubmit={(event) => void saveSource(event)}>
            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="Source key"><input className="input mono" name="source" placeholder="manual_seed" required /></Field>
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

      <div className="grid grid-cols-1 gap-4">
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

      <CollapsibleSection title="低频数据录入" subtitle="主视图优先展示状态；主数据、历史指标和消息采集按需展开。">
        <div className="grid grid-cols-3 gap-4 max-2xl:grid-cols-1">
          <Panel title="刷新股票主数据">
          <form className="grid gap-3" onSubmit={(event) => void saveInstrument(event)}>
            <SourceSelect defaultSource={defaultSource} sources={sources} />
            <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
              <Field label="代码"><input className="input mono" name="symbol" required /></Field>
              <Field label="市场">
                <select className="select mono" name="market" defaultValue="SH">
                  <option value="SH">沪市 (SH)</option>
                  <option value="SZ">深市 (SZ)</option>
                  <option value="BJ">北市 (BJ)</option>
                </select>
              </Field>
              <Field label="名称"><input className="input" name="name" required /></Field>
            </div>
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

        <Panel title="历史 K 线 / 估值 / 资金流">
          <form className="grid gap-3" onSubmit={(event) => void saveMarketData(event)}>
            <SourceSelect defaultSource={defaultSource} sources={sources} />
            <div className="grid grid-cols-4 gap-3 max-md:grid-cols-1">
              <Field label="代码"><input className="input mono" name="symbol" required /></Field>
              <Field label="市场">
                <select className="select mono" name="market" defaultValue="SH">
                  <option value="SH">沪市 (SH)</option>
                  <option value="SZ">深市 (SZ)</option>
                  <option value="BJ">北市 (BJ)</option>
                </select>
              </Field>
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
              <Field label="PE"><input className="input" min="0" name="pe" step="0.01" type="number" /></Field>
              <Field label="PB"><input className="input" min="0" name="pb" step="0.01" type="number" /></Field>
              <Field label="资金净流入"><input className="input" name="netInflow" step="0.01" type="number" /></Field>
            </div>
            <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
              <Field label="成交额"><input className="input" min="0" name="amount" step="0.01" type="number" /></Field>
              <Field label="质量"><QualitySelect /></Field>
            </div>
            <div><Button tone="primary" type="submit"><Plus size={15} />写入指标</Button></div>
          </form>
        </Panel>

        <Panel title="消息面采集">
          <form className="grid gap-3" onSubmit={(event) => void ingestNews(event)}>
            <SourceSelect defaultSource={defaultSource} sources={sources} />
            <div className="grid grid-cols-3 gap-3 max-md:grid-cols-1">
              <Field label="代码"><input className="input mono" name="symbol" /></Field>
              <Field label="市场">
                <select className="select mono" name="market" defaultValue="SH">
                  <option value="SH">沪市 (SH)</option>
                  <option value="SZ">深市 (SZ)</option>
                  <option value="BJ">北市 (BJ)</option>
                </select>
              </Field>
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
        </div>
      </CollapsibleSection>

      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
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
      </div>
    </div>
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
