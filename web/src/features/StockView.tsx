import { Plus } from "@phosphor-icons/react";
import type { FormEvent } from "react";
import type { AppActions } from "../app/App";
import type { AppData, StockStrategy } from "../app/types";
import { friendlyError } from "../api/client";
import { Button, ContextList, EmptyState, Field, Notice, Panel, Pill, SubTabs } from "../components/ui";
import { formatDate, stockAgentTraceLabel } from "../domain/labels";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { StockDataWorkbench } from "./stock/StockDataWorkbench";
import { StockMemory } from "./stock/StockMemory";
import { StockOpportunities } from "./stock/StockOpportunities";
import { StockOverview } from "./stock/StockOverview";
import { StockPortfolios } from "./stock/StockPortfolios";
import { StockSymbolDetail } from "./stock/StockSymbolDetail";
import { StockSymbolCombobox } from "./stock/StockSymbolCombobox";
import { StockWatchReview } from "./stock/StockWatchReview";
import { directionLabel, marketSessionLabel, money, number, percent, percentInput, price, text } from "./stock/format";

type StockTab = "overview" | "portfolios" | "data" | "strategies" | "watch" | "memory";
type StockRouteTab = StockTab | "detail" | "opportunities";

const stockTabs: Array<{ id: StockTab; label: string }> = [
  { id: "overview", label: "总览" },
  { id: "portfolios", label: "账户/仓位" },
  { id: "data", label: "股票/数据" },
  { id: "strategies", label: "策略" },
  { id: "watch", label: "盯盘 / Review" },
  { id: "memory", label: "记忆 / 诊断" },
];
const stockTabIds = stockTabs.map((tab) => tab.id);
const stockRouteTabIds: StockRouteTab[] = [...stockTabIds, "detail", "opportunities"];

export function StockView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [activeRoute, setActiveRoute, stockTabHref] = useQueryParamState<StockRouteTab>("stock", stockRouteTabIds, "overview");
  const active = normalizeStockTab(activeRoute);
  const stock = data.stock;
  const summary = stock.summary || {};
  const pendingOperations = (stock.proposedOperations || []).filter((item) => item.status === "pending_confirmation");
  const openAlerts = (stock.alerts || []).filter((item) => item.status === "new" || item.status === "acknowledged" || item.status === "snoozed");

  async function runAction(label: string, fn: () => Promise<void>) {
    try {
      await fn();
      actions.setToast(label, "good");
      await actions.refreshStock();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_340px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-5">
        <SubTabs
          activeId={active}
          onChange={(id) => setActiveRoute(id as StockTab)}
          rightSlot={
            <Button onClick={() => void actions.refreshStock()}>
              刷新
            </Button>
          }
          tabs={stockTabs.map((tab) => ({ ...tab, href: stockTabHref(tab.id), badge: tab.id === "watch" && openAlerts.length ? <Pill tone="warn">{openAlerts.length}</Pill> : undefined }))}
        />

        {active === "overview" ? <StockOverview data={data} openAlerts={openAlerts} pendingOperations={pendingOperations} /> : null}
        {active === "portfolios" ? <StockPortfolios actions={actions} data={data} runAction={runAction} /> : null}
        {active === "data" ? <StockDataWorkspace actions={actions} data={data} runAction={runAction} /> : null}
        {active === "strategies" ? <StockStrategyWorkspace actions={actions} data={data} runAction={runAction} /> : null}
        {active === "watch" ? <StockWatchReview actions={actions} data={data} openAlerts={openAlerts} pendingOperations={pendingOperations} runAction={runAction} /> : null}
        {active === "memory" ? <StockMemory actions={actions} data={data} runAction={runAction} /> : null}
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="闭环状态">
          <ContextList
            items={[
              ["账户", summary.portfolioCount || 0],
              ["策略", summary.strategyCount || 0],
              ["盯盘", summary.activeWatchCount || 0],
              ["提醒", summary.openAlertCount || 0],
              ["待确认", summary.pendingOperationCount || 0],
              ["总资产", money(summary.totalAssetValue)],
            ]}
          />
        </Panel>
        <div className="mt-4">
          <Panel title="A 股时段">
            <ContextList
              items={[
                ["状态", stock.marketClock?.activeSession ? <Pill tone="good">连续竞价</Pill> : <Pill tone="neutral">{marketSessionLabel(stock.marketClock?.session)}</Pill>],
                ["交易日", stock.marketClock?.tradingDay ? "是" : "否"],
                ["日历", stock.marketClock?.calendarStatus === "exchange_calendar" ? "交易所日历" : "工作日回退"],
                ["时区", stock.marketClock?.timezone || "Asia/Shanghai"],
                ["提示", stock.marketClock?.nextActionHint || "-"],
              ]}
            />
          </Panel>
        </div>
        <div className="mt-4">
          <Panel title="Agent 留痕">
            <ContextList
              items={[
                ["状态", <Pill tone={stock.agentTrace?.pendingPatchCount ? "warn" : stock.agentTrace?.runCount ? "good" : "neutral"}>{stockAgentTraceLabel(stock.agentTrace)}</Pill>],
                ["Runs", stock.agentTrace?.runCount || 0],
                ["Claims", stock.agentTrace?.claimCount || 0],
                ["待确认补丁", stock.agentTrace?.pendingPatchCount || 0],
              ]}
            />
          </Panel>
        </div>
        <div className="mt-4">
          <Panel title="最近提醒">
            <div className="grid gap-2">
              {(stock.alerts || []).slice(0, 4).map((alert) => (
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs" key={alert.id}>
                  <div className="flex items-center justify-between gap-2">
                    <strong className="text-sm">{alert.symbol || "-"}</strong>
                    <Pill tone={alert.status === "new" ? "warn" : "neutral"}>{alert.status || "unknown"}</Pill>
                  </div>
                  <p className="muted mt-2 mb-0 leading-relaxed">{alert.summary || alert.title}</p>
                </div>
              ))}
              {!stock.alerts?.length ? <EmptyState body="盯盘触发后会在这里保留可处理提醒。" title="暂无提醒" /> : null}
            </div>
          </Panel>
        </div>
      </aside>
    </div>
  );
}

function normalizeStockTab(tab: StockRouteTab): StockTab {
  if (tab === "detail") return "data";
  if (tab === "opportunities") return "strategies";
  return tab;
}

function StockDataWorkspace({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  return (
    <div className="grid gap-4">
      <StockDataWorkbench actions={actions} data={data} runAction={runAction} />
      <StockSymbolDetail data={data} />
    </div>
  );
}

function StockStrategyWorkspace({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  return (
    <div className="grid gap-4">
      <StockOpportunities actions={actions} data={data} runAction={runAction} />
      <StockStrategies actions={actions} data={data} runAction={runAction} />
    </div>
  );
}

function StockStrategies({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const portfolios = data.stock.portfolios || [];
  const strategies = data.stock.strategies || [];
  const recentInstruments = data.stock.instruments || [];
  async function createStrategy(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const strategyType = text(form, "strategyType") || "account_agnostic";
    const portfolioId = text(form, "portfolioId");
    if (strategyType === "account_bound" && !portfolioId) {
      actions.setToast("账户绑定策略必须先选择账户", "danger");
      return;
    }
    await runAction("已创建策略", async () => {
      await actions.api("/api/stock/strategies", {
        method: "POST",
        body: {
          title: text(form, "title"),
          strategyType,
          portfolioId: strategyType === "account_bound" ? portfolioId : "",
          symbol: text(form, "symbol"),
          market: text(form, "market"),
          name: text(form, "name"),
          direction: text(form, "direction"),
          triggerPriceAbove: number(form, "triggerPriceAbove"),
          triggerPriceBelow: number(form, "triggerPriceBelow"),
          entryPriceLow: number(form, "entryPriceLow"),
          entryPriceHigh: number(form, "entryPriceHigh"),
          takeProfit: number(form, "takeProfit"),
          stopLoss: number(form, "stopLoss"),
          targetPositionPct: percentInput(form, "targetPositionPct", 10),
          thesis: text(form, "thesis"),
          riskNotes: text(form, "riskNotes"),
        },
      });
      formElement.reset();
    });
  }
  async function createWatch(strategy: StockStrategy) {
    await runAction("已创建盯盘", async () => {
      await actions.api(`/api/stock/strategies/${strategy.id}/watch`, { method: "POST", body: {} });
    });
  }
  return (
    <div className="grid gap-4">
      <Panel title="人工策略录入" subtitle="账户无关策略只会生成 trade_signal；绑定账户后才可能产生 proposed_operation。">
        {!portfolios.length ? <Notice>当前没有账户。可以先创建账户/仓位组合；在此之前只能创建账户无关策略。</Notice> : null}
        <form className="grid gap-3" onSubmit={(event) => void createStrategy(event)}>
          <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
            <Field label="标题"><input className="input" name="title" required /></Field>
            <Field label="策略类型">
              <select className="select" name="strategyType" defaultValue="account_agnostic">
                <option value="account_agnostic">账户无关</option>
                <option disabled={!portfolios.length} value="account_bound">账户绑定</option>
              </select>
            </Field>
          </div>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(260px,1.4fr)] gap-3 max-lg:grid-cols-1">
            <Field label="账户">
              <select className="select" name="portfolioId" defaultValue={portfolios[0]?.id || ""}>
                <option value="">不绑定</option>
                {portfolios.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
              </select>
            </Field>
            <StockSymbolCombobox actions={actions} label="股票" recent={recentInstruments} required />
          </div>
          <div className="grid grid-cols-5 gap-3 max-xl:grid-cols-3 max-md:grid-cols-1">
            <Field label="方向">
              <select className="select" name="direction" defaultValue="watch">
                <option value="watch">观察</option>
                <option value="buy">买入</option>
                <option value="add">加仓</option>
                <option value="hold">持有</option>
                <option value="reduce">减仓</option>
                <option value="sell">卖出</option>
              </select>
            </Field>
            <Field label="上穿触发"><input className="input" min="0" name="triggerPriceAbove" step="0.001" type="number" /></Field>
            <Field label="下破触发"><input className="input" min="0" name="triggerPriceBelow" step="0.001" type="number" /></Field>
            <Field label="目标仓位(%)"><input className="input" defaultValue="10" min="0" name="targetPositionPct" step="1" type="number" /></Field>
            <Field label="止损"><input className="input" min="0" name="stopLoss" step="0.001" type="number" /></Field>
          </div>
          <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
            <Field label="买入区间低"><input className="input" min="0" name="entryPriceLow" step="0.001" type="number" /></Field>
            <Field label="买入区间高"><input className="input" min="0" name="entryPriceHigh" step="0.001" type="number" /></Field>
            <Field label="止盈"><input className="input" min="0" name="takeProfit" step="0.001" type="number" /></Field>
            <Field label="风险备注"><input className="input" name="riskNotes" /></Field>
          </div>
          <Field label="策略原文"><textarea className="textarea" name="thesis" /></Field>
          <div><Button tone="primary" type="submit"><Plus size={15} />保存策略</Button></div>
        </form>
      </Panel>
      <div className="grid gap-3">
        {strategies.map((strategy) => (
          <StrategyRow key={strategy.id} onCreateWatch={() => void createWatch(strategy)} strategy={strategy} />
        ))}
        {!strategies.length ? <EmptyState body="可以先录入一条账户无关策略，再创建盯盘验证触发链路。" title="暂无策略" /> : null}
      </div>
    </div>
  );
}

function StrategyRow({ strategy, onCreateWatch }: { strategy: StockStrategy; onCreateWatch: () => void }) {
  return (
    <Panel
      actions={<Button onClick={onCreateWatch}><Plus size={15} />创建盯盘</Button>}
      subtitle={`${strategy.symbol || "-"} / ${strategy.strategyType === "account_bound" ? "账户绑定" : "账户无关"} / v${strategy.currentVersion || 1}`}
      title={strategy.title || "未命名策略"}
    >
      <div className="grid gap-3 text-sm">
        <div className="flex flex-wrap gap-2">
          <Pill>{directionLabel(strategy.direction)}</Pill>
          {strategy.triggerPriceAbove ? <Pill tone="warn">上穿 {price(strategy.triggerPriceAbove)}</Pill> : null}
          {strategy.triggerPriceBelow ? <Pill tone="warn">下破 {price(strategy.triggerPriceBelow)}</Pill> : null}
          {strategy.targetPositionPct ? <Pill>目标 {percent(strategy.targetPositionPct)}</Pill> : null}
        </div>
        {strategy.thesis ? <p className="muted m-0 leading-relaxed">{strategy.thesis}</p> : null}
      </div>
    </Panel>
  );
}

function LoopRow({ done, label, value }: { done: boolean; label: string; value: string }) {
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <Pill tone={done ? "good" : "neutral"}>{done ? "done" : "todo"}</Pill>
      <div><strong className="block text-sm">{label}</strong><span className="muted mt-1 block text-xs">{value}</span></div>
    </div>
  );
}
