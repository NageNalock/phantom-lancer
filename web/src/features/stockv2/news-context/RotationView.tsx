import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ArrowClockwise, ArrowRight, ChartLineUp, CirclesThreePlus, TrendDown, TrendUp } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type { StockV2NewsContextRotationItem, StockV2NewsContextRotationSignals } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState, Notice, Pill } from "../../../components/ui";
import {
  confidenceLabel,
  confirmationLabel,
  confirmationTone,
  formatNewsContextTime,
  namedObjectLabel,
  rotationTitle,
  themeStageLabel,
  themeStageTone,
} from "./model";

export function RotationView({
  actions,
  refreshKey,
  onOpenTheme,
}: {
  actions: AppActions;
  refreshKey: number;
  onOpenTheme: (themeId: string) => void;
}) {
  const [data, setData] = useState<StockV2NewsContextRotationSignals | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    if (data) setRefreshing(true);
    else setLoading(true);
    setError(null);
    try {
      const result = await actions.api<StockV2NewsContextRotationSignals>("/api/stockv2/news-context/rotation-signals");
      setData(result);
    } catch (loadError) {
      setError(friendlyError(loadError));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  if (loading && !data) return <RotationSkeleton />;

  const hasContent = !!data && [
    data.mainThemes,
    data.acceleratingThemes,
    data.fadingThemes,
    data.relayCandidates,
    data.confirmationSignals,
    data.invalidationSignals,
  ].some((items) => (items?.length || 0) > 0);

  return (
    <div className="grid gap-4">
      <header className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
        <div>
          <div className="flex items-center gap-2">
            <ChartLineUp size={17} className="text-[var(--accent)]" />
            <h2 className="m-0 text-sm font-semibold">板块轮换线索</h2>
            {data?.dataStatus ? <Pill tone={confirmationTone(data.dataStatus)}>{confirmationLabel(data.dataStatus)}</Pill> : null}
          </div>
          <p className="mt-2 mb-0 max-w-4xl text-xs leading-5 text-[var(--muted-strong)]">
            {data?.summary || "消息只负责发现叙事变化，轮换结论还需要行情、覆盖范围和量价表现共同确认。"}
          </p>
        </div>
        <div className="flex items-center gap-2 text-xs text-[var(--muted)]">
          <span>更新 {formatNewsContextTime(data?.updatedAt || data?.asOf)}</span>
          <Button disabled={refreshing} onClick={() => void load()}>
            <ArrowClockwise size={14} />
            {refreshing ? "刷新中" : "刷新"}
          </Button>
        </div>
      </header>

      {error ? (
        <Notice tone="danger">
          <span className="flex flex-wrap items-center justify-between gap-2">
            <span>轮换线索加载失败：{error}{data ? "。当前保留上次结果。" : ""}</span>
            <Button onClick={() => void load()}>重试</Button>
          </span>
        </Notice>
      ) : null}

      {!hasContent && !error ? (
        <EmptyState title="暂无轮换线索" body="每日归纳结合行情形成可验证结论后，会在这里展示主线、退潮和接力方向。" />
      ) : null}

      {hasContent && data ? (
        <div className="grid grid-cols-2 gap-4 max-lg:grid-cols-1">
          <RotationGroup
            className="col-span-2 max-lg:col-span-1"
            icon={<CirclesThreePlus size={16} />}
            items={data.mainThemes || []}
            title="当前主线"
            empty="暂无已确认主线"
            onOpenTheme={onOpenTheme}
          />
          <RotationGroup icon={<TrendUp size={16} />} items={data.acceleratingThemes || []} title="正在加速" empty="暂无加速主题" onOpenTheme={onOpenTheme} />
          <RotationGroup icon={<TrendDown size={16} />} items={data.fadingThemes || []} title="正在退潮" empty="暂无退潮主题" onOpenTheme={onOpenTheme} />
          <RotationGroup
            className="col-span-2 max-lg:col-span-1"
            icon={<ArrowRight size={16} />}
            items={data.relayCandidates || []}
            title="潜在接力方向"
            empty="暂无接力候选"
            onOpenTheme={onOpenTheme}
          />
          <SignalGroup title="确认信号" items={data.confirmationSignals || []} tone="good" />
          <SignalGroup title="证伪信号" items={data.invalidationSignals || []} tone="warn" />
        </div>
      ) : null}
    </div>
  );
}

function RotationSkeleton() {
  return (
    <div aria-label="轮换线索加载中" className="grid grid-cols-2 gap-4 max-lg:grid-cols-1">
      {[0, 1, 2, 3].map((item) => (
        <div className={`h-44 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4 ${item === 0 || item === 3 ? "col-span-2 max-lg:col-span-1" : ""}`} key={item}>
          <div className="h-4 w-28 rounded bg-[var(--surface-strong)]" />
          <div className="mt-4 h-3 w-full rounded bg-[var(--surface-soft)]" />
          <div className="mt-2 h-3 w-3/4 rounded bg-[var(--surface-soft)]" />
        </div>
      ))}
    </div>
  );
}

function RotationGroup({
  className = "",
  empty,
  icon,
  items,
  onOpenTheme,
  title,
}: {
  className?: string;
  empty: string;
  icon: ReactNode;
  items: StockV2NewsContextRotationItem[];
  onOpenTheme: (themeId: string) => void;
  title: string;
}) {
  return (
    <section className={`min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] ${className}`}>
      <div className="flex items-center gap-2 border-b border-[var(--line)] px-4 py-3 text-sm font-semibold">
        <span className="text-[var(--muted-strong)]">{icon}</span>{title}
        <Pill tone="neutral">{items.length}</Pill>
      </div>
      {items.length === 0 ? <p className="m-0 p-4 text-xs text-[var(--muted)]">{empty}</p> : (
        <div className="divide-y divide-[var(--line)]">
          {items.map((item, index) => <RotationRow item={item} key={item.id || item.themeId || `${title}-${index}`} onOpenTheme={onOpenTheme} />)}
        </div>
      )}
    </section>
  );
}

function RotationRow({ item, onOpenTheme }: { item: StockV2NewsContextRotationItem; onOpenTheme: (themeId: string) => void }) {
  const content = (
    <>
      <span className="flex flex-wrap items-center gap-2">
        <strong className="text-sm">{rotationTitle(item)}</strong>
        {item.stage ? <Pill tone={themeStageTone(item.stage)}>{themeStageLabel(item.stage)}</Pill> : null}
        <Pill tone={confirmationTone(item.dataConfirmation)}>{confirmationLabel(item.dataConfirmation)}</Pill>
      </span>
      {item.summary ? <span className="mt-1 block text-xs leading-5 text-[var(--muted-strong)]">{item.summary}</span> : null}
      <span className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[var(--muted)]">
        {item.industries?.length ? <span>{item.industries.join("、")}</span> : null}
        {item.symbols?.length ? <span>{item.symbols.slice(0, 4).map(namedObjectLabel).join("、")}</span> : null}
        {typeof item.confidence === "number" ? <span className="ml-auto">可信程度 {confidenceLabel(item.confidence)}</span> : null}
      </span>
      {item.confirmationSignals?.length || item.invalidationSignals?.length ? (
        <span className="mt-2 grid gap-1 text-[11px]">
          {item.confirmationSignals?.length ? <span className="text-[var(--good)]">确认：{item.confirmationSignals.join("；")}</span> : null}
          {item.invalidationSignals?.length ? <span className="text-[var(--warn)]">证伪：{item.invalidationSignals.join("；")}</span> : null}
        </span>
      ) : null}
    </>
  );
  if (!item.themeId) return <div className="p-3 text-left">{content}</div>;
  return (
    <button className="block w-full p-3 text-left transition hover:bg-[var(--surface-soft)]" onClick={() => onOpenTheme(item.themeId || "")} type="button">
      {content}
    </button>
  );
}

function SignalGroup({ title, items, tone }: { title: string; items: string[]; tone: "good" | "warn" }) {
  return (
    <section className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
      <h3 className="m-0 text-sm font-semibold">{title}</h3>
      {items.length ? (
        <ul className={`mt-3 mb-0 grid gap-2 pl-4 text-xs leading-5 ${tone === "good" ? "text-[var(--good)]" : "text-[var(--warn)]"}`}>
          {items.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}
        </ul>
      ) : <p className="mt-3 mb-0 text-xs text-[var(--muted)]">暂无{title}</p>}
    </section>
  );
}
