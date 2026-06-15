import type { ReactNode } from "react";
import type {
  StockDataMaintenanceResult,
  StockDataTask,
  StockMarketDataPoint,
  StockNewsItem,
  StockOpportunity,
  StockPayload,
  StockQuote,
  Tone,
} from "../../app/types";
import { EmptyState, Metric, Notice, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { numberText, price, snippet } from "./format";

export function StockDataMaintenanceInspector({
  data,
  lastRun,
}: {
  data: StockPayload;
  lastRun: StockDataMaintenanceResult | null;
}) {
  const taskRows = lastRun ? lastRun.tasks || [] : data.dataTasks || [];
  const quoteRows = lastRun ? lastRun.quotes || [] : data.quotes || [];
  const marketRows = lastRun ? lastRun.marketData || [] : data.marketData || [];
  const newsRows = lastRun ? lastRun.newsItems || [] : data.newsItems || [];
  const opportunityRows = lastRun ? lastRun.opportunities || [] : data.opportunities || [];
  const alertRows = lastRun ? lastRun.alerts || [] : data.alerts || [];
  const metricDetail = lastRun ? "本次运行" : "当前快照";
  const completedAt = latestTaskDate(taskRows);

  return (
    <Panel
      title="最近一次数据维护"
      subtitle={lastRun ? `本次手动维护返回结果${completedAt ? `，完成于 ${formatDate(completedAt)}` : ""}` : "尚未手动运行，当前展示已有快照数据。点击运行数据维护后会切换为本次结果。"}
      actions={<Pill tone={lastRun ? "good" : "neutral"}>{lastRun ? "本次结果" : "当前快照"}</Pill>}
    >
      <div className="grid gap-4">
        <section className="grid grid-cols-6 gap-3 max-2xl:grid-cols-3 max-lg:grid-cols-2">
          <Metric label="任务" value={numberText(taskRows.length)} detail={metricDetail} tone={taskRows.some((task) => task.status === "failed") ? "warn" : "neutral"} />
          <Metric label="行情快照" value={numberText(quoteRows.length)} detail={metricDetail} />
          <Metric label="指标点" value={numberText(marketRows.length)} detail={metricDetail} />
          <Metric label="消息" value={numberText(newsRows.length)} detail={metricDetail} tone={newsRows.some((item) => item.importance === "high" || item.importance === "urgent") ? "warn" : "neutral"} />
          <Metric label="机会" value={numberText(opportunityRows.length)} detail={metricDetail} />
          <Metric label="提醒" value={numberText(alertRows.length)} detail={metricDetail} tone={alertRows.length ? "warn" : "neutral"} />
        </section>

        {lastRun?.notes?.length ? (
          <Notice>
            <div className="grid gap-1">
              <strong>维护备注</strong>
              {lastRun.notes.slice(0, 6).map((note, index) => (
                <span className="text-xs leading-relaxed" key={`${note}-${index}`}>{note}</span>
              ))}
            </div>
          </Notice>
        ) : null}

        <DataSection
          empty={!taskRows.length}
          emptyBody="运行数据维护后，会在这里看到每个子任务的处理量、失败数和下一次可执行时间。"
          emptyTitle="暂无维护任务"
          title="维护任务记录"
        >
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--line)]">
                <TableHead>任务</TableHead>
                <TableHead>数据源</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>处理</TableHead>
                <TableHead>失败</TableHead>
                <TableHead>完成时间</TableHead>
                <TableHead>下次可执行</TableHead>
                <TableHead>摘要</TableHead>
              </tr>
            </thead>
            <tbody>
              {taskRows.slice(0, 12).map((task, index) => (
                <tr className="border-b border-[var(--line)] last:border-b-0" key={task.id || `${task.taskType}-${index}`}>
                  <TableCell><span className="mono">{task.taskType || "-"}</span></TableCell>
                  <TableCell><span className="mono">{task.source || "-"}</span></TableCell>
                  <TableCell><Pill tone={taskTone(task.status)}>{task.status || "unknown"}</Pill></TableCell>
                  <TableCell>{numberText(task.processedCount || 0)}</TableCell>
                  <TableCell>{numberText(task.failedCount || 0)}</TableCell>
                  <TableCell>{dateText(task.completedAt || task.createdAt)}</TableCell>
                  <TableCell>{dateText(task.nextRunAt)}</TableCell>
                  <TableCell><span className="inline-block max-w-[260px] truncate align-bottom" title={taskSummary(task)}>{taskSummary(task)}</span></TableCell>
                </tr>
              ))}
            </tbody>
          </table>
        </DataSection>

        <DataSection
          empty={!quoteRows.length && !marketRows.length}
          emptyBody={lastRun ? "本次维护没有写入行情或指标点。检查数据源状态、交易时段和限流退避后再运行。" : "当前快照里暂无行情或指标点。"}
          emptyTitle="暂无行情/指标样本"
          title="行情与指标样本"
        >
          <MarketDataTable marketRows={marketRows} quoteRows={quoteRows} />
        </DataSection>

        <div className="grid grid-cols-2 gap-4 max-2xl:grid-cols-1">
          <DataSection
            empty={!newsRows.length}
            emptyBody={lastRun ? "本次维护没有采集到消息。若 adapter 未配置，数据源任务会显示 blocked reason。" : "当前快照里暂无消息面数据。"}
            emptyTitle="暂无消息样本"
            title="消息样本"
          >
            <NewsTable rows={newsRows} />
          </DataSection>

          <DataSection
            empty={!opportunityRows.length}
            emptyBody={lastRun ? "本次维护没有生成新机会。通常需要行情异动、消息面触发或历史数据满足条件。" : "当前快照里暂无机会。"}
            emptyTitle="暂无机会样本"
            title="机会样本"
          >
            <OpportunityTable rows={opportunityRows} />
          </DataSection>
        </div>
      </div>
    </Panel>
  );
}

function DataSection({
  children,
  empty,
  emptyBody,
  emptyTitle,
  title,
}: {
  children: ReactNode;
  empty: boolean;
  emptyBody: string;
  emptyTitle: string;
  title: string;
}) {
  return (
    <section className="grid gap-2">
      <h3 className="m-0 text-xs font-semibold text-[var(--muted-strong)]">{title}</h3>
      {empty ? (
        <EmptyState body={emptyBody} title={emptyTitle} />
      ) : (
        <div className="overflow-x-auto rounded-lg border border-[var(--line)]">
          {children}
        </div>
      )}
    </section>
  );
}

function MarketDataTable({ marketRows, quoteRows }: { marketRows: StockMarketDataPoint[]; quoteRows: StockQuote[] }) {
  const rows = [
    ...quoteRows.map((quote, index) => ({
      key: `quote-${quote.symbol || "unknown"}-${quote.dataTimestamp || index}`,
      symbol: quote.symbol,
      market: quote.market,
      dataset: "quote_snapshot",
      dataDate: quote.dataTimestamp,
      close: quote.lastPrice,
      volume: quote.volume,
      amount: quote.amount,
      source: "public_quote",
      quality: quote.dataFreshness || quote.tradableStatus,
    })),
    ...marketRows.map((point, index) => ({
      key: point.id || `market-${point.symbol || "unknown"}-${point.dataset || "dataset"}-${point.dataDate || index}`,
      symbol: point.symbol,
      market: point.market,
      dataset: point.dataset,
      dataDate: point.dataDate,
      close: point.close,
      volume: point.volume,
      amount: point.amount,
      source: point.source,
      quality: point.quality,
    })),
  ];

  return (
    <table className="w-full border-collapse text-left text-sm">
      <thead>
        <tr className="border-b border-[var(--line)]">
          <TableHead>股票</TableHead>
          <TableHead>数据集</TableHead>
          <TableHead>日期/时间</TableHead>
          <TableHead>价格</TableHead>
          <TableHead>成交量</TableHead>
          <TableHead>成交额</TableHead>
          <TableHead>来源</TableHead>
          <TableHead>质量</TableHead>
        </tr>
      </thead>
      <tbody>
        {rows.slice(0, 12).map((row) => (
          <tr className="border-b border-[var(--line)] last:border-b-0" key={row.key}>
            <TableCell><span className="mono">{stockLabel(row.symbol, row.market)}</span></TableCell>
            <TableCell><Pill>{row.dataset || "-"}</Pill></TableCell>
            <TableCell>{dateText(row.dataDate)}</TableCell>
            <TableCell>{price(row.close)}</TableCell>
            <TableCell>{optionalNumber(row.volume)}</TableCell>
            <TableCell>{optionalNumber(row.amount)}</TableCell>
            <TableCell><span className="mono">{row.source || "-"}</span></TableCell>
            <TableCell><Pill tone={qualityTone(row.quality)}>{row.quality || "unknown"}</Pill></TableCell>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function NewsTable({ rows }: { rows: StockNewsItem[] }) {
  return (
    <table className="w-full border-collapse text-left text-sm">
      <thead>
        <tr className="border-b border-[var(--line)]">
          <TableHead>标题</TableHead>
          <TableHead>股票</TableHead>
          <TableHead>重要性</TableHead>
          <TableHead>类别</TableHead>
          <TableHead>来源</TableHead>
          <TableHead>发布时间</TableHead>
        </tr>
      </thead>
      <tbody>
        {rows.slice(0, 10).map((item, index) => (
          <tr className="border-b border-[var(--line)] last:border-b-0" key={item.id || item.dedupeKey || `${item.title}-${index}`}>
            <TableCell><span className="inline-block max-w-[320px] truncate align-bottom" title={item.summary || item.title}>{item.title || "-"}</span></TableCell>
            <TableCell><span className="mono">{stockLabel(item.symbol, item.market)}</span></TableCell>
            <TableCell><Pill tone={importanceTone(item.importance)}>{item.importance || "normal"}</Pill></TableCell>
            <TableCell>{item.category || "-"}</TableCell>
            <TableCell><span className="mono">{item.source || "-"}</span></TableCell>
            <TableCell>{dateText(item.publishedAt || item.createdAt)}</TableCell>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function OpportunityTable({ rows }: { rows: StockOpportunity[] }) {
  return (
    <table className="w-full border-collapse text-left text-sm">
      <thead>
        <tr className="border-b border-[var(--line)]">
          <TableHead>机会</TableHead>
          <TableHead>股票</TableHead>
          <TableHead>主题</TableHead>
          <TableHead>置信度</TableHead>
          <TableHead>来源</TableHead>
          <TableHead>状态</TableHead>
        </tr>
      </thead>
      <tbody>
        {rows.slice(0, 10).map((item, index) => (
          <tr className="border-b border-[var(--line)] last:border-b-0" key={item.id || `${item.title}-${index}`}>
            <TableCell><span className="inline-block max-w-[320px] truncate align-bottom" title={item.evidenceSummary || item.thesis || item.title}>{item.title || "-"}</span></TableCell>
            <TableCell><span className="mono">{stockLabel(item.symbol, item.market)}</span></TableCell>
            <TableCell>{item.theme || "-"}</TableCell>
            <TableCell><Pill tone={confidenceTone(item.confidence)}>{item.confidence || "unknown"}</Pill></TableCell>
            <TableCell><span className="mono">{item.sourceType || "-"}</span></TableCell>
            <TableCell><Pill>{item.status || "open"}</Pill></TableCell>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TableHead({ children }: { children: ReactNode }) {
  return <th className="muted px-2 py-2 text-xs font-medium">{children}</th>;
}

function TableCell({ children }: { children: ReactNode }) {
  return <td className="px-2 py-2 align-top">{children}</td>;
}

function latestTaskDate(tasks: StockDataTask[]): string {
  const dates = tasks
    .map((task) => task.completedAt || task.updatedAt || task.createdAt || "")
    .filter(Boolean)
    .sort();
  return dates[dates.length - 1] || "";
}

function taskSummary(task: StockDataTask): string {
  return task.failureSummary || snippet(task.resultJson, 120);
}

function dateText(value?: string): string {
  return formatDate(value) || "-";
}

function stockLabel(symbol?: string, market?: string): string {
  if (!symbol && !market) return "-";
  if (!market) return symbol || "-";
  return `${symbol || "-"} ${market}`;
}

function optionalNumber(value?: number): string {
  return value === undefined || value === null ? "-" : numberText(value);
}

function taskTone(status?: string): Tone {
  if (status === "completed") return "good";
  if (status === "failed") return "danger";
  if (status === "degraded" || status === "blocked" || status === "skipped") return "warn";
  return "neutral";
}

function qualityTone(value?: string): Tone {
  if (value === "fresh" || value === "tradable") return "good";
  if (value === "failed") return "danger";
  if (value === "stale" || value === "partial" || value === "delayed") return "warn";
  return "neutral";
}

function importanceTone(value?: string): Tone {
  if (value === "urgent" || value === "high") return "warn";
  return "neutral";
}

function confidenceTone(value?: string): Tone {
  if (value === "high") return "good";
  if (value === "low") return "warn";
  return "neutral";
}
