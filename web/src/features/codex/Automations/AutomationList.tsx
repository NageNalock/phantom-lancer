import type { CodexAutomation } from "../../../app/types";
import { Button, EmptyState, Pill } from "../../../components/ui";
import { formatDate } from "../../../domain/labels";

export function AutomationList({
  items,
  runningIds,
  onRun,
  onEdit,
  onToggle,
  onRemove,
}: {
  items: CodexAutomation[];
  runningIds: Set<string>;
  onRun: (id: string) => void;
  onEdit: (item: CodexAutomation) => void;
  onToggle: (item: CodexAutomation) => void;
  onRemove: (item: CodexAutomation) => void;
}) {
  if (!items.length) {
    return <EmptyState title="暂无自动化" body="创建 Thread Wakeup 或 Project Automation 后，后台会按计划排队 read-only Codex turn。" />;
  }
  return (
    <div className="grid gap-2">
      {items.map((item) => (
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={item.id}>
          <div className="flex items-start justify-between gap-2">
            <div>
              <strong className="text-sm">{item.title || item.id}</strong>
              <p className="muted mt-1 mb-0 text-xs">{item.promptSummary || "read-only wakeup"}</p>
            </div>
            <div className="flex items-center gap-1.5">
              <Pill tone={item.enabled ? "good" : "neutral"}>{item.kind || "thread_wakeup"}</Pill>
              {!item.enabled ? <Pill tone="neutral">已暂停</Pill> : null}
            </div>
          </div>
          <div className="mt-2 flex flex-wrap gap-2 text-xs text-[var(--muted)]">
            <span className="mono">{scheduleLabel(item)}</span>
            <span>next {formatDate(item.nextRunAt) || "-"}</span>
            <span>last {formatDate(item.lastRunAt) || "-"}</span>
            {item.failureBackoffUntil ? <span>backoff {formatDate(item.failureBackoffUntil)}</span> : null}
            {item.retryCount ? <span>retry {item.retryCount}</span> : null}
          </div>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button disabled={runningIds.has(item.id)} onClick={() => onRun(item.id)}>{runningIds.has(item.id) ? "排队中" : "Run now"}</Button>
            <Button onClick={() => onEdit(item)}>编辑</Button>
            <Button onClick={() => onToggle(item)}>{item.enabled ? "暂停" : "启用"}</Button>
            <Button tone="danger" onClick={() => onRemove(item)}>删除</Button>
          </div>
        </div>
      ))}
    </div>
  );
}

function scheduleLabel(item: CodexAutomation): string {
  const cron = typeof item.schedule?.cron === "string" ? item.schedule.cron : "";
  if (cron) return `cron ${cron}`;
  const interval = Number(item.schedule?.intervalMinutes);
  return Number.isFinite(interval) && interval > 0 ? `每 ${interval} 分钟` : "interval";
}
