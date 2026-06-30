import type { AppData } from "../../app/types";
import { Metric, Panel, Pill } from "../../components/ui";
import { stockV2RiskLabel, stockV2TriggerTypeLabel, stockV2UpdateStatusLabel, stockV2UpdateStatusTone } from "../../domain/labels";

export function StockV2Overview({ data }: { data: AppData }) {
  const stockv2 = data.stockv2;
  const portfolios = stockv2.portfolios || [];
  const instruments = stockv2.instruments || [];
  const jobs = stockv2.updateJobs || [];
  const latestJob = jobs[0];
  const runningJob = jobs.find(j => j.status === "running");
  const settings = stockv2.settings;

  const totalAssetValue = portfolios.reduce((sum, p) => sum + (p.totalAssetValue || p.cash || 0), 0);
  const totalHoldings = portfolios.reduce((sum, p) => sum + (p.holdings?.length || 0), 0);

  return (
    <div className="grid gap-4">
      <section className="grid grid-cols-4 gap-3 max-2xl:grid-cols-2 max-sm:grid-cols-1">
        <Metric
          label="投资组合"
          value={portfolios.length}
          detail={`${totalHoldings} 只持仓`}
          tone={portfolios.length ? "good" : "neutral"}
        />
        <Metric
          label="总资产"
          value={formatMoney(totalAssetValue)}
          detail={`${portfolios.length} 个组合`}
          tone={portfolios.length ? "good" : "neutral"}
        />
        <Metric
          label="主数据标的"
          value={instruments.length}
          detail="股票 / 场内基金"
          tone={instruments.length ? "good" : "warn"}
        />
        <Metric
          label="更新状态"
          value={runningJob ? "更新中" : latestJob ? stockV2UpdateStatusLabel(latestJob) : "未执行"}
          detail={latestJob ? `${stockV2TriggerTypeLabel(latestJob.triggerType)} · ${formatCompactTime(latestJob.startAt)}` : "点击手动触发"}
          tone={runningJob ? "warn" : latestJob ? stockV2UpdateStatusTone(latestJob) : "neutral"}
        />
      </section>

      <Panel title="功能闭环">
        <div className="grid gap-3">
          <LoopRow done={instruments.length > 0} label="标的主数据" value="从新浪列表源和腾讯行情源拉取 A 股股票与场内基金，支持批量打散更新和实时进度。" />
          <LoopRow done={portfolios.length > 0} label="投资组合 / 仓位" value="支持创建多个组合，配置风控参数（风险等级、单票上限、最大回撤），独立管理持仓。" />
          <LoopRow done={!!settings?.autoUpdateEnabled} label="数据资产自动维护" value="每日 23:00 后低峰窗口统一维护标的、最新价和日 K；设置里的秒数只作为数据新鲜度窗口。" />
          <LoopRow done={jobs.length > 0} label="更新历史追溯" value="每次更新任务完整记录：触发方式、成功/失败数、耗时、错误信息，可弹窗查看。" />
        </div>
      </Panel>

      <Panel title="投资组合概览">
        {portfolios.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">还没有投资组合，在「仓位」标签创建第一个组合。</p>
        ) : (
          <div className="grid gap-3">
            {portfolios.map((p) => (
              <div
                className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3"
                key={p.id}
              >
                <div>
                  <div className="flex items-center gap-2">
                    <strong className="text-sm">{p.name}</strong>
                    <Pill tone="neutral">{stockV2RiskLabel(p.riskLevel)}</Pill>
                  </div>
                  {p.description ? <p className="muted mt-1 text-xs">{p.description}</p> : null}
                  <div className="mt-2 flex flex-wrap gap-3 text-xs">
                    <span>持仓 <strong>{p.holdings?.length || 0}</strong> 只</span>
                    <span>现金 <strong>{formatMoney(p.cash)}</strong></span>
                    <span>
                      总市值 <strong>{formatMoney(p.totalValue || 0)}</strong>
                    </span>
                  </div>
                </div>
                <div className="text-right">
                  <div className="text-sm font-semibold">{formatMoney(p.totalAssetValue || p.cash)}</div>
                  <div className="text-xs text-[var(--muted)]">总资产</div>
                </div>
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="数据来源">
        <div className="grid gap-3">
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">腾讯行情接口</strong>
                <span className="ml-2 text-xs text-[var(--muted)]">qt.gtimg.cn / web.ifzq.gtimg.cn</span>
              </div>
              <Pill tone="good">主数据 / Quote / 日 K</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              标的主数据与批量 Quote 走 qt.gtimg.cn，日 K 走腾讯 fqkline；批量请求会打散，避免短时间打满数据源。
            </p>
          </div>
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
            <div className="flex items-center justify-between">
              <div>
                <strong className="text-sm">东方财富 fallback</strong>
                <span className="ml-2 text-xs text-[var(--muted)]">push2his / fund NAV</span>
              </div>
              <Pill tone="neutral">分钟线 / 基金净值</Pill>
            </div>
            <p className="muted mt-2 text-xs">
              最新价优先用分钟线投影，腾讯分钟线不可用时回退东方财富分钟线；场内基金日 K 缺口会回退基金净值接口。
            </p>
          </div>
          {settings?.proxyEnabled ? (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <div className="flex items-center justify-between">
                <div>
                  <strong className="text-sm">代理</strong>
                  <span className="ml-2 text-xs text-[var(--muted)]">
                    {settings.proxyType}://{settings.proxyHost}:{settings.proxyPort}
                  </span>
                </div>
                <Pill tone="warn">已启用</Pill>
              </div>
            </div>
          ) : null}
        </div>
      </Panel>
    </div>
  );
}

function LoopRow({ done, label, value }: { done: boolean; label: string; value: string }) {
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <Pill tone={done ? "good" : "neutral"}>{done ? "done" : "todo"}</Pill>
      <div>
        <strong className="block text-sm">{label}</strong>
        <span className="muted mt-1 block text-xs">{value}</span>
      </div>
    </div>
  );
}

function formatMoney(value: number): string {
  if (!value) return "¥0";
  if (Math.abs(value) >= 100000000) {
    return `¥${(value / 100000000).toFixed(2)} 亿`;
  }
  if (Math.abs(value) >= 10000) {
    return `¥${(value / 10000).toFixed(2)} 万`;
  }
  return `¥${value.toFixed(2)}`;
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
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}
