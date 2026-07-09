import { ArrowClockwise } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { StockV2PagedResponse, StockV2StockProfileUpdateTask } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Notice, Panel, Pill } from "../../components/ui";
import { formatMeaningfulDateTime as formatTime, hasMeaningfulTime } from "./time";

const PROFILE_TASK_PAGE_SIZE = 12;

export function StockV2ProfileRecords({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2StockProfileUpdateTask[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function load(nextPage = page) {
    setLoading(true);
    setError("");
    try {
      const safePage = Math.max(1, nextPage);
      const params = new URLSearchParams({
        limit: String(PROFILE_TASK_PAGE_SIZE),
        offset: String((safePage - 1) * PROFILE_TASK_PAGE_SIZE),
      });
      const res = await actions.api<StockV2PagedResponse<StockV2StockProfileUpdateTask>>(
        `/api/stockv2/profiles/update-tasks?${params.toString()}`,
      );
      setItems(res.items ?? []);
      setTotal(res.total ?? 0);
      setPage(safePage);
    } catch (err) {
      setError(friendlyError(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Panel
      title="画像更新记录"
      subtitle="查看自动/手动触发、基础输入变化、AI 调用决策和数据源状态"
      actions={
        <Button className="inline-flex items-center gap-1.5" onClick={() => void load(page)} disabled={loading}>
          <ArrowClockwise size={15} />
          刷新
        </Button>
      }
    >
      {error ? <Notice tone="danger">加载画像更新记录失败：{error}</Notice> : null}
      {items.length === 0 && !loading ? (
        <EmptyState title="暂无画像更新记录" body="统一数据资产维护或单只标的手动维护后，会在这里留下任务记录。" />
      ) : (
        <StockProfileUpdateTasksTable items={items} loading={loading} />
      )}
      <ProfileTaskPager page={page} total={total} loading={loading} onPage={(next) => void load(next)} />
    </Panel>
  );
}

function StockProfileUpdateTasksTable({ items, loading }: { items: StockV2StockProfileUpdateTask[]; loading: boolean }) {
  return (
    <div className="overflow-x-auto rounded-md border border-[var(--line)] bg-[var(--surface)]">
      <table className="min-w-full text-left text-sm">
        <thead className="border-b border-[var(--line)] text-xs text-[var(--muted)]">
          <tr>
            <th className="px-3 py-2 font-medium">标的</th>
            <th className="px-3 py-2 font-medium">触发</th>
            <th className="px-3 py-2 font-medium">状态</th>
            <th className="px-3 py-2 font-medium">基础输入</th>
            <th className="px-3 py-2 font-medium">AI</th>
            <th className="px-3 py-2 font-medium">数据源</th>
            <th className="px-3 py-2 font-medium">时间</th>
          </tr>
        </thead>
        <tbody>
          {items.map((task) => (
            <tr key={task.id} className="border-b border-[var(--line)] last:border-b-0">
              <td className="px-3 py-2 align-top">
                <div className="font-mono text-xs font-semibold">{task.symbol}</div>
                <div className="muted mt-0.5 text-xs">{task.market || "-"}</div>
              </td>
              <td className="px-3 py-2 align-top">
                <Pill tone={task.triggerSource === "auto" ? "good" : "neutral"}>{triggerSourceLabel(task.triggerSource)}</Pill>
                {task.triggerReason ? <div className="muted mt-1 max-w-[220px] truncate text-xs">{task.triggerReason}</div> : null}
              </td>
              <td className="px-3 py-2 align-top">
                <Pill tone={taskStatusTone(task.status)}>{taskStatusLabel(task.status)}</Pill>
                {task.errorMessage ? <div className="mt-1 max-w-[240px] truncate text-xs text-[var(--danger)]">{task.errorMessage}</div> : null}
              </td>
              <td className="px-3 py-2 align-top">
                <Pill tone={task.baseInputChanged ? "warn" : "neutral"}>{task.baseInputChanged ? "有变化" : "无变化"}</Pill>
                {task.baseProfileStatus ? (
                  <div className="mt-1">
                    <Pill tone={profileResultTone(task.baseProfileStatus)}>{baseProfileStatusLabel(task.baseProfileStatus)}</Pill>
                  </div>
                ) : null}
              </td>
              <td className="px-3 py-2 align-top">
                <Pill tone={aiDecisionTone(task.aiDecision)}>{aiDecisionLabel(task.aiDecision)}</Pill>
                {task.aiProfileStatus ? (
                  <div className="mt-1">
                    <Pill tone={profileResultTone(task.aiProfileStatus)}>{aiProfileStatusLabel(task.aiProfileStatus)}</Pill>
                  </div>
                ) : null}
                {task.agentRunId ? <div className="muted mt-1 font-mono text-xs">run {shortID(task.agentRunId)}</div> : null}
                {task.aiProfileError ? <div className="mt-1 max-w-[240px] truncate text-xs text-[var(--danger)]">{task.aiProfileError}</div> : null}
              </td>
              <td className="px-3 py-2 align-top">
                <StockProfileSourceStatusSummary task={task} />
              </td>
              <td className="px-3 py-2 align-top">
                <div className="text-xs">{formatTime(task.startedAt)}</div>
                {hasMeaningfulTime(task.finishedAt) ? <div className="muted mt-0.5 text-xs">完成 {formatTime(task.finishedAt)}</div> : null}
              </td>
            </tr>
          ))}
          {loading ? (
            <tr>
              <td className="muted px-3 py-4 text-center text-sm" colSpan={7}>加载中...</td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}

function ProfileTaskPager({
  loading,
  onPage,
  page,
  total,
}: {
  loading: boolean;
  onPage: (page: number) => void;
  page: number;
  total: number;
}) {
  const totalPages = Math.max(1, Math.ceil(total / PROFILE_TASK_PAGE_SIZE));
  const start = total === 0 ? 0 : (page - 1) * PROFILE_TASK_PAGE_SIZE + 1;
  const end = Math.min(total, page * PROFILE_TASK_PAGE_SIZE);
  return (
    <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--muted)]">
      <span>
        第 {page} / {totalPages} 页 · {start}-{end} / {total}
      </span>
      <div className="flex items-center gap-1.5">
        <Button disabled={loading || page <= 1} onClick={() => onPage(page - 1)}>上一页</Button>
        <Button disabled={loading || page >= totalPages} onClick={() => onPage(page + 1)}>下一页</Button>
      </div>
    </div>
  );
}

function StockProfileSourceStatusSummary({ task }: { task: StockV2StockProfileUpdateTask }) {
  const statuses = task.sourceStatuses ?? [];
  if (statuses.length === 0) return <span className="text-xs text-[var(--muted)]">-</span>;
  return (
    <div className="flex max-w-[260px] flex-wrap gap-1">
      {statuses.slice(0, 3).map((item) => (
        <Pill className="max-w-[180px] truncate" key={`${task.id}-${item.source}`} tone={sourceStatusTone(item.status)}>
          {item.source} {sourceStatusLabel(item.status)}
        </Pill>
      ))}
      {statuses.length > 3 ? <span className="text-xs text-[var(--muted)]">+{statuses.length - 3}</span> : null}
    </div>
  );
}

function triggerSourceLabel(value: string): string {
  if (value === "auto") return "自动";
  if (value === "manual") return "手动";
  return value || "-";
}

function taskStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    running: "运行中",
    completed: "完成",
    partial: "部分完成",
    failed: "失败",
  };
  return labels[status] ?? status;
}

function taskStatusTone(status: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "running") return "warn";
  if (status === "completed") return "good";
  if (status === "partial") return "warn";
  if (status === "failed") return "danger";
  return "neutral";
}

function aiDecisionLabel(decision: string): string {
  const labels: Record<string, string> = {
    called: "已调用",
    skipped_unchanged: "输入未变",
    skipped_not_configured: "未配置",
    skipped_unavailable: "不可用",
    failed: "失败",
  };
  return labels[decision] ?? (decision || "-");
}

function aiDecisionTone(decision: string): "neutral" | "good" | "warn" | "danger" {
  if (decision === "called") return "good";
  if (decision === "failed") return "danger";
  if (decision === "skipped_unavailable") return "warn";
  return "neutral";
}

function baseProfileStatusLabel(status?: string): string {
  if (status === "ready") return "基础已生成";
  if (status === "failed") return "基础失败";
  return status || "-";
}

function aiProfileStatusLabel(status?: string): string {
  if (status === "ready") return "AI 生成成功";
  if (status === "running") return "AI 生成中";
  if (status === "failed") return "AI 生成失败";
  if (status === "not_configured") return "AI 未配置";
  if (status === "missing") return "AI 未生成";
  return status || "-";
}

function profileResultTone(status?: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "ready") return "good";
  if (status === "running" || status === "not_configured" || status === "missing") return "warn";
  if (status === "failed") return "danger";
  return "neutral";
}

function sourceStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    success: "成功",
    failed: "失败",
    skipped: "跳过",
  };
  return labels[status] ?? status;
}

function sourceStatusTone(status: string): "neutral" | "good" | "warn" | "danger" {
  if (status === "success") return "good";
  if (status === "failed") return "danger";
  return "neutral";
}

function shortID(id: string): string {
  if (!id) return "-";
  return id.length <= 10 ? id : id.slice(0, 8);
}
