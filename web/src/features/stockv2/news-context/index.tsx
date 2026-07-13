import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ArrowClockwise, Database, GearSix, GitBranch, HardDrives, Robot } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type { StockV2NewsContextSummary, StockV2NewsContextView } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, Notice, Pill, SubTabs } from "../../../components/ui";
import { useQueryParamState, useStringQueryParamState } from "../../../hooks/useQueryParamState";
import { ThemeWorkspace } from "./ThemeWorkspace";
import { RotationView } from "./RotationView";
import { RunRecordsView } from "./RunRecordsView";
import { NewsContextSettingsDrawer } from "./NewsContextSettingsDrawer";
import {
  NEWS_CONTEXT_VIEWS,
  formatNewsContextBytes,
  formatNewsContextTime,
  indexStatusLabel,
  indexStatusTone,
  mcpVerificationLabel,
  mcpVerificationTone,
} from "./model";

export function StockV2NewsContext({ actions }: { actions: AppActions }) {
  const [view, setView, viewHref] = useQueryParamState<StockV2NewsContextView>(
    "stockv2NewsContext",
    NEWS_CONTEXT_VIEWS,
    "themes",
  );
  const [selectedThemeId, setSelectedThemeId] = useStringQueryParamState("stockv2NewsTheme");
  const [summary, setSummary] = useState<StockV2NewsContextSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = useState(true);
  const [summaryRefreshing, setSummaryRefreshing] = useState(false);
  const [summaryError, setSummaryError] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  async function loadSummary(force = false) {
    if (summary) setSummaryRefreshing(true);
    else setSummaryLoading(true);
    setSummaryError(null);
    try {
      const result = await actions.api<StockV2NewsContextSummary>("/api/stockv2/news-context/summary");
      setSummary(result);
    } catch (error) {
      setSummaryError(friendlyError(error));
    } finally {
      setSummaryLoading(false);
      setSummaryRefreshing(false);
    }
    if (force) setRefreshKey((value) => value + 1);
  }

  useEffect(() => {
    void loadSummary();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function openTheme(themeId: string) {
    setView("themes");
    setSelectedThemeId(themeId);
  }

  return (
    <div className="grid gap-4">
      <SubTabs
        activeId={view}
        ariaLabel="消息脉络视图"
        onChange={(id) => setView(id as StockV2NewsContextView)}
        rightSlot={
          <span className="flex items-center gap-2">
            <Button disabled={summaryLoading || !summary?.config} onClick={() => setSettingsOpen(true)}>
              <GearSix size={14} />配置
            </Button>
            <Button disabled={summaryRefreshing} onClick={() => void loadSummary(true)}>
              <ArrowClockwise size={14} />
              {summaryRefreshing ? "刷新中" : "刷新"}
            </Button>
          </span>
        }
        tabs={[
          { id: "themes", label: "主题总览", href: viewHref("themes") },
          { id: "rotation", label: "轮换线索", href: viewHref("rotation") },
          { id: "aggregation", label: "归纳记录", href: viewHref("aggregation") },
          { id: "cleanup", label: "清理记录", href: viewHref("cleanup") },
        ]}
      />

      <SummaryStrip error={summaryError} loading={summaryLoading} summary={summary} onRetry={() => void loadSummary()} />

      {view === "themes" ? (
        <ThemeWorkspace
          actions={actions}
          refreshKey={refreshKey}
          selectedId={selectedThemeId}
          onSelect={setSelectedThemeId}
        />
      ) : null}
      {view === "rotation" ? (
        <RotationView actions={actions} refreshKey={refreshKey} onOpenTheme={openTheme} />
      ) : null}
      {view === "aggregation" ? (
        <RunRecordsView
          actions={actions}
          kind="aggregation"
          refreshKey={refreshKey}
          onChanged={() => void loadSummary()}
        />
      ) : null}
      {view === "cleanup" ? (
        <RunRecordsView
          actions={actions}
          kind="cleanup"
          refreshKey={refreshKey}
          onChanged={() => void loadSummary()}
        />
      ) : null}
      {settingsOpen && summary?.config ? (
        <NewsContextSettingsDrawer
          actions={actions}
          config={summary.config}
          onClose={() => setSettingsOpen(false)}
          onSaved={() => loadSummary(true)}
          summary={summary}
        />
      ) : null}
    </div>
  );
}

function SummaryStrip({
  error,
  loading,
  summary,
  onRetry,
}: {
  error: string | null;
  loading: boolean;
  summary: StockV2NewsContextSummary | null;
  onRetry: () => void;
}) {
  if (loading && !summary) {
    return (
      <div className="grid grid-cols-4 gap-px overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--line)] max-lg:grid-cols-2">
        {["主题", "新闻存量", "待清理", "移除内容"].map((label) => (
          <div className="min-h-20 bg-[var(--surface)] p-3" key={label}>
            <div className="text-xs text-[var(--muted)]">{label}</div>
            <div className="mt-3 h-4 w-16 rounded bg-[var(--surface-strong)]" />
          </div>
        ))}
      </div>
    );
  }

  const resolvedIndexStatus = summary?.indexStatus
    || ((summary?.indexFailedCount || 0) > 0
      ? "failed"
      : (summary?.indexMissingCount || 0) > 0 || (summary?.indexStaleCount || 0) > 0
        ? "stale"
        : (summary?.indexReadyCount || 0) > 0
          ? "ready"
          : "missing");

  return (
    <div className="grid gap-3">
      {error ? (
        <Notice tone="danger">
          <span className="flex flex-wrap items-center justify-between gap-2">
            <span>健康摘要加载失败：{error}。下方视图仍可独立使用。</span>
            <Button onClick={onRetry}>重试摘要</Button>
          </span>
        </Notice>
      ) : null}

      {summary ? (
        <section className="overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]">
          <div className="grid grid-cols-4 divide-x divide-[var(--line)] max-lg:grid-cols-2 max-lg:divide-x-0">
            <SummaryCell icon={<GitBranch size={15} />} label="有效主题" value={summary.themeCount ?? 0} detail={`今日变化 ${summary.changedThemeCount ?? 0}`} />
            <SummaryCell icon={<Database size={15} />} label="当前新闻存量" value={summary.currentNewsCount ?? 0} detail={`历史处理 ${summary.historicalProcessedCount ?? 0}`} />
            <SummaryCell icon={<HardDrives size={15} />} label="等待安全清理" value={summary.pendingCleanupCount ?? 0} detail={`受保护 ${summary.protectedNewsCount ?? 0}`} />
            <SummaryCell icon={<HardDrives size={15} />} label="累计移除内容量" value={formatNewsContextBytes(summary.releasedBytes)} detail={`已压缩 ${summary.compressedNewsCount ?? 0} 条`} />
          </div>
          <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
            <span className="inline-flex items-center gap-2">
              自动归纳
              <Pill tone={summary.config?.enabled ? "good" : "neutral"}>{summary.config?.enabled ? "运行中" : "已暂停"}</Pill>
            </span>
            <span className="inline-flex items-center gap-2">
              自动清理
              <Pill tone={summary.config?.autoCleanupEnabled ? "warn" : "neutral"}>{summary.config?.autoCleanupEnabled ? "已启用" : "已暂停"}</Pill>
            </span>
            <span className="inline-flex items-center gap-2">
              <Database size={14} className="text-[var(--muted)]" />
              主题索引
              <Pill tone={indexStatusTone(resolvedIndexStatus)}>{indexStatusLabel(resolvedIndexStatus)}</Pill>
              <span className="text-[var(--muted)]">就绪 {summary.indexReadyCount ?? 0} / 异常 {(summary.indexMissingCount ?? 0) + (summary.indexStaleCount ?? 0) + (summary.indexFailedCount ?? 0)}</span>
            </span>
            <span className="inline-flex items-center gap-2">
              <Robot size={14} className="text-[var(--muted)]" />
              CLI 检索
              <Pill tone={mcpVerificationTone(summary.mcpAvailable, summary.mcpToolsReady, summary.mcpVerificationStatus)}>
                {mcpVerificationLabel(summary.mcpAvailable, summary.mcpToolsReady, summary.mcpVerificationStatus)}
              </Pill>
              {summary.mcpLastVerifiedAt ? <span className="text-[var(--muted)]">最近检查 {formatNewsContextTime(summary.mcpLastVerifiedAt)}</span> : null}
            </span>
            <span className="ml-auto text-[var(--muted)]">
              摘要更新 {formatNewsContextTime(summary.updatedAt)}
            </span>
          </div>
          {summary.indexError || summary.mcpError ? (
            <div className="border-t border-[var(--line)] px-3 py-2 text-xs text-[var(--danger)]">
              {[summary.indexError, summary.mcpError].filter(Boolean).join("；")}
            </div>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}

function SummaryCell({ icon, label, value, detail }: { icon: ReactNode; label: string; value: ReactNode; detail: string }) {
  return (
    <div className="min-h-20 bg-[var(--surface)] p-3">
      <div className="flex items-center gap-2 text-xs text-[var(--muted)]">{icon}{label}</div>
      <div className="mt-2 flex items-baseline gap-2">
        <strong className="font-mono text-lg font-semibold">{value}</strong>
        <span className="text-xs text-[var(--muted)]">{detail}</span>
      </div>
    </div>
  );
}
