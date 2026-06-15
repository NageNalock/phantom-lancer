import { MagnifyingGlass, Plus } from "@phosphor-icons/react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { AppData, StockOpportunity } from "../../app/types";
import { Button, EmptyState, Field, Notice, Panel, Pill } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { number, percentInput, text } from "./format";

export function StockOpportunities({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: (label: string, fn: () => Promise<void>) => Promise<void> }) {
  const portfolios = data.stock.portfolios || [];
  const opportunities = data.stock.opportunities || [];

  async function createOpportunity(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await runAction("已创建候选机会", async () => {
      await actions.api("/api/stock/opportunities", {
        method: "POST",
        body: {
          title: text(form, "title"),
          sourceType: text(form, "sourceType") || "manual",
          sourceRefId: text(form, "sourceRefId"),
          symbol: text(form, "symbol"),
          market: text(form, "market"),
          name: text(form, "name"),
          theme: text(form, "theme"),
          thesis: text(form, "thesis"),
          evidenceSummary: text(form, "evidenceSummary"),
          confidence: text(form, "confidence") || "medium",
          status: "candidate",
        },
      });
      event.currentTarget.reset();
    });
  }

  async function discoverOpportunities() {
    await runAction("已执行自动机会发现", async () => {
      await actions.api("/api/stock/opportunities/discover", { method: "POST", body: {} });
    });
  }

  async function createStrategy(opportunity: StockOpportunity, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const strategyType = text(form, "strategyType") || "account_agnostic";
    const portfolioId = strategyType === "account_bound" ? text(form, "portfolioId") : "";
    await runAction("已从机会生成策略", async () => {
      if (strategyType === "account_bound" && !portfolioId) {
        throw new Error("账户绑定策略必须先选择一个账户/组合");
      }
      await actions.api(`/api/stock/opportunities/${opportunity.id}/strategy`, {
        method: "POST",
        body: {
          title: text(form, "title") || opportunity.title,
          strategyType,
          portfolioId,
          direction: text(form, "direction"),
          triggerPriceAbove: number(form, "triggerPriceAbove"),
          triggerPriceBelow: number(form, "triggerPriceBelow"),
          targetPositionPct: percentInput(form, "targetPositionPct", 10),
          stopLoss: number(form, "stopLoss"),
          takeProfit: number(form, "takeProfit"),
        },
      });
    });
  }

  return (
    <div className="grid gap-4">
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 max-md:grid-cols-1">
        <Notice>自动发现会从已落盘新闻、财报/研报类消息、行情异动和 K 线异动生成去重候选机会；没有足够证据时只保留任务记录，不会凭空生成机会。</Notice>
        <Button onClick={() => void discoverOpportunities()} tone="primary"><MagnifyingGlass size={15} />自动发现</Button>
      </div>
      <Panel title="候选机会录入" subtitle="把主题、事件、消息或记忆先沉淀为机会，再决定是否生成策略。">
        <form className="grid gap-3" onSubmit={(event) => void createOpportunity(event)}>
          <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
            <Field label="机会标题"><input className="input" name="title" required /></Field>
            <Field label="主题"><input className="input" name="theme" placeholder="AI 算力 / 政策催化" /></Field>
          </div>
          <div className="grid grid-cols-4 gap-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
            <Field label="代码"><input className="input mono" name="symbol" required /></Field>
            <Field label="市场"><input className="input mono" name="market" defaultValue="SH" /></Field>
            <Field label="名称"><input className="input" name="name" /></Field>
            <Field label="信心">
              <select className="select" name="confidence" defaultValue="medium">
                <option value="low">low</option>
                <option value="medium">medium</option>
                <option value="high">high</option>
              </select>
            </Field>
          </div>
          <div className="grid grid-cols-2 gap-3 max-md:grid-cols-1">
            <Field label="来源类型"><input className="input mono" name="sourceType" placeholder="manual / news_item / memory" /></Field>
            <Field label="来源 ID"><input className="input mono" name="sourceRefId" /></Field>
          </div>
          <Field label="机会假设"><textarea className="textarea" name="thesis" required /></Field>
          <Field label="证据摘要"><textarea className="textarea" name="evidenceSummary" /></Field>
          <div><Button tone="primary" type="submit"><Plus size={15} />保存机会</Button></div>
        </form>
      </Panel>

      <div className="grid gap-3">
        {opportunities.map((opportunity) => (
          <Panel key={opportunity.id} title={opportunity.title || "未命名机会"} subtitle={`${opportunity.symbol || "-"} / ${opportunity.theme || "未分类"} / ${formatDate(opportunity.updatedAt || opportunity.createdAt)}`}>
            <div className="grid gap-3 text-sm">
              <div className="flex flex-wrap gap-2">
                <Pill tone={opportunity.status === "candidate" ? "warn" : opportunity.status === "strategy_created" ? "good" : "neutral"}>{opportunity.status || "candidate"}</Pill>
                <Pill>{opportunity.confidence || "medium"}</Pill>
                <Pill>{opportunity.sourceType || "manual"}</Pill>
                {opportunity.linkedStrategyId ? <Pill tone="good">strategy linked</Pill> : null}
              </div>
              <p className="muted m-0 leading-relaxed">{opportunity.thesis}</p>
              {opportunity.evidenceSummary ? <p className="muted m-0 text-xs leading-relaxed">{opportunity.evidenceSummary}</p> : null}
              {!opportunity.linkedStrategyId ? (
                <form className="grid grid-cols-[minmax(160px,1fr)_150px_150px_120px_120px_120px_auto] items-end gap-2 max-2xl:grid-cols-3 max-sm:grid-cols-1" onSubmit={(event) => void createStrategy(opportunity, event)}>
                  <Field label="策略标题"><input className="input" name="title" placeholder={opportunity.title} /></Field>
                  <Field label="类型">
                    <select className="select" name="strategyType" defaultValue="account_agnostic">
                      <option value="account_agnostic">账户无关</option>
                      <option value="account_bound" disabled={!portfolios.length}>账户绑定</option>
                    </select>
                  </Field>
                  <Field label="账户">
                    <select className="select" name="portfolioId" defaultValue={portfolios[0]?.id || ""}>
                      {portfolios.length ? portfolios.map((item) => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">暂无账户</option>}
                    </select>
                  </Field>
                  <Field label="方向">
                    <select className="select" name="direction" defaultValue="watch">
                      <option value="watch">观察</option>
                      <option value="buy">买入</option>
                      <option value="add">加仓</option>
                      <option value="reduce">减仓</option>
                      <option value="sell">卖出</option>
                    </select>
                  </Field>
                  <Field label="上穿"><input className="input" min="0" name="triggerPriceAbove" step="0.001" type="number" /></Field>
                  <Field label="目标(%)"><input className="input" defaultValue="10" min="0" name="targetPositionPct" step="1" type="number" /></Field>
                  <Button type="submit">生成策略</Button>
                </form>
              ) : null}
            </div>
          </Panel>
        ))}
        {!opportunities.length ? <EmptyState body="可以从主题、消息、公告或复盘记忆沉淀候选机会，再生成策略和盯盘。" title="暂无机会" /> : null}
      </div>
    </div>
  );
}
