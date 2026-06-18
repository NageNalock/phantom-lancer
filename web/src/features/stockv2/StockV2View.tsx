import { ArrowClockwise, ChartLine, Database, Faders, Plus, Wallet } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type { AppData, StockV2Tab } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Panel, Pill, SubTabs } from "../../components/ui";
import {
  stockV2PortfolioCountLabel,
  stockV2SettingsSummary,
  stockV2TriggerTypeLabel,
  stockV2UpdateStatusLabel,
  stockV2UpdateStatusTone,
} from "../../domain/labels";
import { useQueryParamState } from "../../hooks/useQueryParamState";
import { StockV2Overview } from "./StockV2Overview";
import { StockV2Universe } from "./StockV2Universe";
import { StockV2Portfolios } from "./StockV2Portfolios";
import { StockV2Settings } from "./StockV2Settings";
import { StockV2DailyBars } from "./StockV2DailyBars";

const v2Tabs: Array<{ id: StockV2Tab; label: string; icon?: typeof Plus }> = [
  { id: "overview", label: "总览", icon: Faders },
  { id: "universe", label: "主数据", icon: Database },
  { id: "dailyBars", label: "行情", icon: ChartLine },
  { id: "portfolios", label: "仓位", icon: Wallet },
  { id: "settings", label: "设置", icon: Faders },
];

export function StockV2View({ actions, data }: { actions: AppActions; data: AppData }) {
  const [activeTab, setActiveTab, tabHref] = useQueryParamState<StockV2Tab>(
    "stockv2",
    ["overview", "universe", "dailyBars", "portfolios", "settings"],
    "overview",
  );
  const stockv2 = data.stockv2;
  const runningJob = stockv2.updateJobs?.find(j => j.status === "running");

  async function runAction(label: string, fn: () => Promise<void>) {
    try {
      await fn();
      actions.setToast(label, "good");
      await actions.refreshStockV2();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_340px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-5">
        <SubTabs
          activeId={activeTab}
          onChange={(id) => setActiveTab(id as StockV2Tab)}
          rightSlot={
            <Button onClick={() => void actions.refreshStockV2()}>
              <ArrowClockwise size={14} className="mr-1.5" />
              刷新
            </Button>
          }
          tabs={v2Tabs.map((tab) => ({
            ...tab,
            href: tabHref(tab.id),
          }))}
        />

        {activeTab === "overview" ? <StockV2Overview data={data} /> : null}
        {activeTab === "universe" ? <StockV2Universe actions={actions} data={data} runAction={runAction} /> : null}
        {activeTab === "dailyBars" ? <StockV2DailyBars actions={actions} data={data} runAction={runAction} /> : null}
        {activeTab === "portfolios" ? <StockV2Portfolios actions={actions} data={data} runAction={runAction} /> : null}
        {activeTab === "settings" ? <StockV2Settings actions={actions} data={data} runAction={runAction} /> : null}
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="V2 概览">
          <div className="grid gap-3 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">投资组合</span>
              <Pill tone={stockv2.portfolios?.length ? "good" : "neutral"}>
                {stockV2PortfolioCountLabel(stockv2)}
              </Pill>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">更新状态</span>
              {runningJob ? (
                <Pill tone="warn">更新中 · {runningJob.processedCount}/{runningJob.totalCount}</Pill>
              ) : stockv2.updateJobs?.[0] ? (
                <Pill tone={stockV2UpdateStatusTone(stockv2.updateJobs[0])}>
                  {stockV2UpdateStatusLabel(stockv2.updateJobs[0])}
                </Pill>
              ) : (
                <Pill tone="neutral">未执行</Pill>
              )}
            </div>
            <div className="flex items-center justify-between">
              <span className="text-[var(--muted)]">更新模式</span>
              <Pill tone="neutral">{stockV2SettingsSummary(stockv2.settings)}</Pill>
            </div>
          </div>
        </Panel>

        <div className="mt-4">
          <Panel title="最近更新">
            <div className="grid gap-2">
              {(stockv2.updateJobs || []).slice(0, 5).length === 0 ? (
                <p className="text-xs text-[var(--muted)]">暂无更新记录</p>
              ) : (
                (stockv2.updateJobs || []).slice(0, 5).map((job) => (
                  <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs" key={job.id}>
                    <div className="flex items-center justify-between">
                      <span className="font-medium">{stockV2TriggerTypeLabel(job.triggerType)}</span>
                      <Pill tone={stockV2UpdateStatusTone(job)}>{stockV2UpdateStatusLabel(job)}</Pill>
                    </div>
                    <div className="mt-1 text-[var(--muted)]">
                      成功 {job.successCount} / 失败 {job.failedCount} / 共 {job.totalCount || job.processedCount}
                    </div>
                    <div className="mt-1 text-[var(--muted-strong)]">
                      {formatCompactTime(job.startAt)}
                    </div>
                  </div>
                ))
              )}
            </div>
          </Panel>
        </div>
      </aside>
    </div>
  );
}

function formatCompactTime(iso?: string): string {
  if (!hasMeaningfulTime(iso)) return "-";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin} 分钟前`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr} 小时前`;
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function hasMeaningfulTime(iso?: string): iso is string {
  return !!iso && !iso.startsWith("0001-01-01");
}
