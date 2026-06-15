import { useMemo } from "react";
import type { AppData } from "../../app/types";
import { ContextList, EmptyState, Field, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { useQueryParamState } from "../../hooks/useQueryParamState";
import { money, percent, price } from "./format";

export function StockSymbolDetail({ data }: { data: AppData }) {
  const symbols = useMemo(() => {
    const set = new Set<string>();
    for (const item of data.stock.quotes || []) if (item.symbol) set.add(item.symbol);
    for (const item of data.stock.instruments || []) if (item.symbol) set.add(item.symbol);
    for (const item of data.stock.opportunities || []) if (item.symbol) set.add(item.symbol);
    for (const item of data.stock.strategies || []) if (item.symbol) set.add(item.symbol);
    for (const item of data.stock.watches || []) if (item.symbol) set.add(item.symbol);
    return Array.from(set).sort();
  }, [data.stock.instruments, data.stock.opportunities, data.stock.quotes, data.stock.strategies, data.stock.watches]);
  const [querySymbol, setQuerySymbol] = useQueryParamState("symbol", symbols, symbols[0] || "");
  const selected = symbols.includes(querySymbol) ? querySymbol : symbols[0] || "";
  const quote = (data.stock.quotes || []).find((item) => item.symbol === selected);
  const instrument = (data.stock.instruments || []).find((item) => item.symbol === selected);
  const opportunities = (data.stock.opportunities || []).filter((item) => item.symbol === selected);
  const strategies = (data.stock.strategies || []).filter((item) => item.symbol === selected);
  const watches = (data.stock.watches || []).filter((item) => item.symbol === selected);
  const reviews = (data.stock.reviews || []).filter((item) => item.symbol === selected);
  const holdings = (data.stock.portfolios || []).flatMap((portfolio) => (portfolio.holdings || []).filter((holding) => holding.symbol === selected).map((holding) => ({ ...holding, portfolioName: portfolio.name })));

  if (!symbols.length) return <EmptyState body="写入行情、主数据、机会或策略后，可以在这里按股票查看对象网络。" title="暂无股票对象" />;

  return (
    <div className="grid gap-4">
      <Panel title="股票详情" subtitle="按单一股票汇总行情、持仓、机会、策略、盯盘和 Review。">
        <div className="grid grid-cols-[260px_minmax(0,1fr)] gap-4 max-lg:grid-cols-1">
          <Field label="股票">
            <select className="select mono" value={selected} onChange={(event) => setQuerySymbol(event.currentTarget.value)}>
              {symbols.map((symbol) => <option key={symbol} value={symbol}>{symbol}</option>)}
            </select>
          </Field>
          <ContextList
            items={[
              ["名称", instrument?.name || quote?.name || "-"],
              ["行业", instrument?.industry || "-"],
              ["概念", instrument?.concept || "-"],
              ["最新价", price(quote?.lastPrice)],
              ["行情时间", quote?.dataTimestamp ? formatDate(quote.dataTimestamp) : "-"],
              ["状态", <Pill tone={quote?.dataFreshness === "fresh" && quote?.tradableStatus === "tradable" ? "good" : "warn"}>{quote?.dataFreshness || "unknown"} / {quote?.tradableStatus || "unknown"}</Pill>],
            ]}
          />
        </div>
      </Panel>

      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
        <Panel title="持仓">
          <div className="grid gap-2">
            {holdings.map((holding) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={holding.id}>
                <div className="flex flex-wrap items-center gap-2">
                  <strong>{holding.portfolioName}</strong>
                  <Pill>{holding.tradableStatus || "unknown"}</Pill>
                </div>
                <p className="muted mt-2 mb-0 text-xs">数量 {holding.quantity || 0} / 可卖 {holding.availableQuantity || 0} / 市值 {money(holding.marketValue)} / 仓位 {percent(holding.positionPct)}</p>
              </div>
            ))}
            {!holdings.length ? <EmptyState body="账户持仓里还没有这个股票。" title="暂无持仓" /> : null}
          </div>
        </Panel>
        <Panel title="机会与策略">
          <div className="grid gap-2">
            {opportunities.map((item) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={item.id}>
                <div className="flex flex-wrap items-center gap-2"><strong>{item.title}</strong><Pill>{item.status || "candidate"}</Pill><Pill>{item.confidence || "medium"}</Pill></div>
                <p className="muted mt-2 mb-0 text-xs">{item.thesis}</p>
              </div>
            ))}
            {strategies.map((item) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-sm" key={item.id}>
                <div className="flex flex-wrap items-center gap-2"><strong>{item.title}</strong><Pill>{item.strategyType || "account_agnostic"}</Pill><Pill>{item.direction || "watch"}</Pill></div>
              </div>
            ))}
            {!opportunities.length && !strategies.length ? <EmptyState body="还没有关联机会或策略。" title="暂无机会/策略" /> : null}
          </div>
        </Panel>
      </div>

      <div className="grid grid-cols-2 gap-4 max-xl:grid-cols-1">
        <Panel title="盯盘">
          <div className="grid gap-2">
            {watches.map((item) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={item.id}>
                <div className="flex flex-wrap items-center gap-2"><strong className="mono">{item.id}</strong><Pill>{item.status || "active"}</Pill></div>
                <p className="muted mt-2 mb-0 text-xs">上穿 {price(item.triggerPriceAbove)} / 下破 {price(item.triggerPriceBelow)} / 最近检查 {item.lastCheckedAt ? formatDate(item.lastCheckedAt) : "-"}</p>
              </div>
            ))}
            {!watches.length ? <EmptyState body="还没有盯盘任务。" title="暂无盯盘" /> : null}
          </div>
        </Panel>
        <Panel title="Review">
          <div className="grid gap-2">
            {reviews.slice(0, 8).map((item) => (
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-sm" key={item.id}>
                <div className="flex flex-wrap items-center gap-2"><strong>{item.reviewResult}</strong><Pill>{item.guardrailResult || "n/a"}</Pill><span className="muted text-xs">{formatDate(item.completedAt || item.createdAt)}</span></div>
                <p className="muted mt-2 mb-0 text-xs">{item.summary}</p>
              </div>
            ))}
            {!reviews.length ? <EmptyState body="还没有该股票的 Review。" title="暂无 Review" /> : null}
          </div>
        </Panel>
      </div>
    </div>
  );
}
