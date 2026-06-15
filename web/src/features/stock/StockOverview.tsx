import type { AppData, StockAlert, StockProposedOperation } from "../../app/types";
import { Metric, Panel, Pill } from "../../components/ui";
import { formatDate, stockAgentTraceLabel } from "../../domain/labels";
import { money } from "./format";

export function StockOverview({ data, openAlerts, pendingOperations }: { data: AppData; openAlerts: StockAlert[]; pendingOperations: StockProposedOperation[] }) {
  const summary = data.stock.summary || {};
  const dataHealth = data.stock.dataHealth || {};
  const agentTrace = data.stock.agentTrace || {};
  return (
    <div className="grid gap-4">
      <section className="grid grid-cols-4 gap-3 max-2xl:grid-cols-2 max-sm:grid-cols-1">
        <Metric label="总资产" value={money(summary.totalAssetValue)} detail={`现金 ${money(summary.totalCash)}`} tone={summary.portfolioCount ? "good" : "neutral"} />
        <Metric label="市值" value={money(summary.totalMarketValue)} detail={`${summary.portfolioCount || 0} 个账户`} />
        <Metric label="提醒" value={summary.openAlertCount || 0} detail={summary.lastAlertAt ? formatDate(summary.lastAlertAt) : "暂无触发"} tone={openAlerts.length ? "warn" : "neutral"} />
        <Metric label="待确认操作" value={summary.pendingOperationCount || 0} detail="必须人工确认后才更新持仓" tone={pendingOperations.length ? "warn" : "neutral"} />
        <Metric label="数据源" value={dataHealth.sourceCount || 0} detail={`${dataHealth.availableSources || 0} 可用 / ${dataHealth.degradedSources || 0} 降级`} tone={dataHealth.failedSources ? "warn" : dataHealth.sourceCount ? "good" : "neutral"} />
        <Metric label="股票主数据" value={dataHealth.instrumentCount || 0} detail={`${dataHealth.marketPointCount || 0} 条指标点`} />
        <Metric label="消息面" value={dataHealth.newsItemCount || 0} detail={`${dataHealth.importantNewsCount || 0} 条重要消息`} tone={dataHealth.importantNewsCount ? "warn" : "neutral"} />
        <Metric label="数据任务" value={dataHealth.taskCount || 0} detail={dataHealth.lastTaskAt ? formatDate(dataHealth.lastTaskAt) : "暂无任务"} tone={dataHealth.failedTaskCount ? "warn" : "neutral"} />
        <Metric label="Agent 留痕" value={stockAgentTraceLabel(agentTrace)} detail={`${agentTrace.runCount || 0} runs / ${agentTrace.claimCount || 0} claims`} tone={agentTrace.pendingPatchCount ? "warn" : agentTrace.runCount ? "good" : "neutral"} />
      </section>
      <Panel title="基础可用闭环">
        <div className="grid gap-3">
          <LoopRow done={(summary.portfolioCount || 0) > 0} label="账户/持仓" value="建立账户，录入现金和持仓。" />
          <LoopRow done={(data.stock.opportunities || []).length > 0} label="机会" value="从主题、消息、事件或记忆沉淀候选机会。" />
          <LoopRow done={(summary.strategyCount || 0) > 0} label="策略" value="策略区分账户无关与账户绑定。" />
          <LoopRow done={(summary.activeWatchCount || 0) > 0} label="系统盯盘" value="按行情快照做价格触发检查。" />
          <LoopRow done={(data.stock.alerts || []).length > 0} label="Alert / Review" value="触发后生成提醒，再执行 Review。" />
          <LoopRow done={(data.stock.tradeSignals || []).length > 0 || (data.stock.proposedOperations || []).length > 0} label="信号 / 操作建议" value="无账户输出信号，账户绑定输出建议。" />
          <LoopRow done={(data.stock.operations || []).length > 0} label="人工操作记录" value="确认后更新现金、持仓和复盘记忆。" />
        </div>
      </Panel>
      <Panel title="数据增强闭环">
        <div className="grid gap-3">
          <LoopRow done={(dataHealth.sourceCount || 0) > 0} label="数据源治理" value="记录授权模式、健康状态、失败和游标。" />
          <LoopRow done={(dataHealth.instrumentCount || 0) > 0} label="股票主数据" value="股票代码、名称、行业、概念和上市状态落盘。" />
          <LoopRow done={(dataHealth.marketPointCount || 0) > 0} label="历史指标" value="K 线、估值、成交量和资金流数据可补数。" />
          <LoopRow done={(dataHealth.newsItemCount || 0) > 0} label="消息聚合" value="消息面数据去重、质量标记并保留游标。" />
          <LoopRow done={(data.stock.alerts || []).some((alert) => alert.sourceType === "news_item")} label="信息面触发" value="命中已有盯盘后进入同一套 Alert Ledger。" />
        </div>
      </Panel>
      <Panel title="Agent 可追溯闭环">
        <div className="grid gap-3">
          <LoopRow done={(agentTrace.runCount || 0) > 0} label="Decision Ledger" value="每次 Review 保存 Prompt、输入、输出、模型 profile 和成本摘要。" />
          <LoopRow done={(data.stock.agentSteps || []).length > 0} label="运行子步骤" value="记录 context、evidence、challenge、guardrail 和 report_formatter。" />
          <LoopRow done={(agentTrace.claimCount || 0) > 0} label="Claim Ledger" value="把触发、行情、策略、账户和 guardrails 变成可核验证据。" />
          <LoopRow done={(data.stock.strategyPatches || []).length > 0} label="策略补丁确认" value="Review 只生成 pending patch，人工接受后才产生新策略版本。" />
          <LoopRow done={(data.stock.memories || []).some((memory) => memory.objectType === "agent_run")} label="记忆回流" value="运行摘要回写为股票记忆，供后续相似案例检索。" />
        </div>
      </Panel>
    </div>
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
