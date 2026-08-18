import type {
  StockV2ModelHorizonOutlook,
  StockV2ModelPortfolioHorizonOutlook,
} from "../../app/types";
import { Pill } from "../../components/ui";

const HORIZON_ORDER = ["short", "medium", "long"];

export function ModelHorizonOutlookCompact({ items }: { items?: StockV2ModelHorizonOutlook[] }) {
  const item = ordered(items)[0];
  if (!item) return <span className="text-[var(--muted)]">旧记录无预测</span>;
  return (
    <span className="grid gap-0.5" title={item.thesis}>
      <span className={`font-mono ${directionClass(item.direction)}`}>
        {item.tradingDays} 日上涨 {percentProbability(item.probabilityUp)}
      </span>
      <span className="text-[10px] text-[var(--muted)]">
        预期 {formatPrice(item.expectedPrice)} / 目标 {formatPrice(item.targetPrice)} 触及 {percentProbability(item.targetProbability)}
      </span>
    </span>
  );
}

export function ModelHorizonOutlookPanel({
  items,
  title = "模型周期预期",
}: {
  items?: StockV2ModelHorizonOutlook[];
  title?: string;
}) {
  const outlooks = ordered(items);
  if (outlooks.length === 0) return null;
  return (
    <section className="grid gap-2" aria-label={title}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <strong className="text-sm">{title}</strong>
        <span className="text-xs text-[var(--muted)]">模型条件估计，不是收益保证</span>
      </div>
      <div className="grid grid-cols-3 gap-2">
        {outlooks.map((item) => (
          <article className="min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)] p-2.5 text-xs" key={item.horizon}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <strong>{horizonLabel(item.horizon, item.tradingDays)}</strong>
              <Pill tone={qualityTone(item.dataQuality)}>{qualityLabel(item.dataQuality)}</Pill>
            </div>
            <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1">
              <Metric label="上涨概率" value={percentProbability(item.probabilityUp)} tone={directionClass(item.direction)} />
              <Metric label="预期价" value={formatPrice(item.expectedPrice)} />
              <Metric label="预期收益" value={formatPercent(item.expectedReturnPct)} tone={returnClass(item.expectedReturnPct)} />
              <Metric label="跑赢概率" value={percentProbability(item.probabilityOutperform)} />
              <Metric label="目标触及" value={`${formatPrice(item.targetPrice)} / ${percentProbability(item.targetProbability)}`} />
              <Metric label="下行风险" value={formatPercent(-Math.abs(item.downsideRiskPct))} tone="text-[var(--danger)]" />
              <Metric label="价格区间" value={`${formatPrice(item.rangeLow)} - ${formatPrice(item.rangeHigh)}`} />
              <Metric label="模型置信" value={percentProbability(item.confidence)} />
            </div>
            <p className="mt-2 leading-relaxed text-[var(--muted-strong)]">{item.thesis}</p>
            <TextList label="失效条件" items={item.invalidConditions} />
            <TextList label="不确定性" items={item.uncertainties} muted />
          </article>
        ))}
      </div>
    </section>
  );
}

export function ModelPortfolioOutlookPanel({
  items,
  title = "组合周期预期",
}: {
  items?: StockV2ModelPortfolioHorizonOutlook[];
  title?: string;
}) {
  const outlooks = orderedPortfolio(items);
  if (outlooks.length === 0) return null;
  return (
    <section className="grid gap-2" aria-label={title}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <strong className="text-sm">{title}</strong>
        <span className="text-xs text-[var(--muted)]">已综合集中度与持仓相关性</span>
      </div>
      <div className="grid grid-cols-3 gap-2">
        {outlooks.map((item) => (
          <article className="min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)] p-2.5 text-xs" key={item.horizon}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <strong>{horizonLabel(item.horizon, item.tradingDays)}</strong>
              <Pill tone={qualityTone(item.dataQuality)}>{qualityLabel(item.dataQuality)}</Pill>
            </div>
            <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1">
              <Metric label="盈利概率" value={percentProbability(item.probabilityGain)} tone={directionClass(item.direction)} />
              <Metric label="预期收益" value={formatPercent(item.expectedReturnPct)} tone={returnClass(item.expectedReturnPct)} />
              <Metric label="收益区间" value={`${formatPercent(item.rangeLowReturnPct)} - ${formatPercent(item.rangeHighReturnPct)}`} />
              <Metric label="跑赢概率" value={percentProbability(item.probabilityOutperform)} />
              <Metric label="预期最大回撤" value={formatPercent(-Math.abs(item.expectedMaxDrawdownPct))} tone="text-[var(--danger)]" />
              <Metric label="模型置信" value={percentProbability(item.confidence)} />
            </div>
            <p className="mt-2 leading-relaxed text-[var(--muted-strong)]">{item.summary}</p>
            <TextList label="失效条件" items={item.invalidConditions} />
            <TextList label="不确定性" items={item.uncertainties} muted />
          </article>
        ))}
      </div>
    </section>
  );
}

function Metric({ label, value, tone = "text-[var(--text)]" }: { label: string; value: string; tone?: string }) {
  return <div className="min-w-0"><span className="block text-[10px] text-[var(--muted)]">{label}</span><span className={`block break-words font-mono ${tone}`}>{value}</span></div>;
}

function TextList({ label, items, muted = false }: { label: string; items?: string[]; muted?: boolean }) {
  if (!items?.length) return null;
  return <p className={`mt-1.5 leading-relaxed ${muted ? "text-[var(--muted)]" : "text-[var(--muted-strong)]"}`}><span className="font-medium">{label}：</span>{items.join("；")}</p>;
}

function ordered(items?: StockV2ModelHorizonOutlook[]): StockV2ModelHorizonOutlook[] {
  return [...(items || [])].sort((a, b) => HORIZON_ORDER.indexOf(a.horizon) - HORIZON_ORDER.indexOf(b.horizon));
}

function orderedPortfolio(items?: StockV2ModelPortfolioHorizonOutlook[]): StockV2ModelPortfolioHorizonOutlook[] {
  return [...(items || [])].sort((a, b) => HORIZON_ORDER.indexOf(a.horizon) - HORIZON_ORDER.indexOf(b.horizon));
}

function horizonLabel(horizon: string, tradingDays: number): string {
  const name = ({ short: "短期", medium: "中期", long: "长期" } as Record<string, string>)[horizon] || horizon;
  return `${name} ${tradingDays} 日`;
}

function qualityLabel(value: string): string {
  return ({ healthy: "数据健康", degraded: "数据降级", insufficient: "数据不足" } as Record<string, string>)[value] || value;
}

function qualityTone(value: string): "good" | "warn" | "danger" | "neutral" {
  if (value === "healthy") return "good";
  if (value === "insufficient") return "danger";
  if (value === "degraded") return "warn";
  return "neutral";
}

function directionClass(value: string): string {
  if (value === "bullish") return "text-[var(--good)]";
  if (value === "bearish") return "text-[var(--danger)]";
  return "text-[var(--text)]";
}

function returnClass(value: number): string {
  if (value > 0) return "text-[var(--good)]";
  if (value < 0) return "text-[var(--danger)]";
  return "text-[var(--text)]";
}

function percentProbability(value: number): string {
  return Number.isFinite(value) ? `${Math.round(value * 100)}%` : "-";
}

function formatPercent(value: number): string {
  return Number.isFinite(value) ? `${value > 0 ? "+" : ""}${value.toFixed(1)}%` : "-";
}

function formatPrice(value: number): string {
  if (!Number.isFinite(value)) return "-";
  return value >= 100 ? value.toFixed(2) : value.toFixed(3).replace(/0+$/, "").replace(/\.$/, "");
}
