import type { AuditEvent } from "../app/types";
import { EmptyState, Panel } from "../components/ui";
import { auditLabel, auditSummary, formatDate } from "../domain/labels";

export function ActivityView({ audit }: { audit: AuditEvent[] }) {
  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_332px] max-xl:grid-cols-1">
      <div className="p-5">
        <Panel subtitle="登录、项目变更和 Codex 执行都会写入审计。" title="审计时间线">
          <div className="grid gap-2">
            {audit.length ? (
              audit.map((item) => (
                <article className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={item.id || `${item.eventType}-${item.createdAt}`}>
                  <strong className="block text-sm">{auditSummary(item)}</strong>
                  <div className="muted mono mt-2 flex flex-wrap gap-3 text-xs">
                    <span>{auditLabel(item.eventType)}</span>
                    <span>{formatDate(item.createdAt)}</span>
                    {item.riskLevel ? <span>risk:{item.riskLevel}</span> : null}
                  </div>
                </article>
              ))
            ) : (
              <EmptyState body="操作后会在这里看到历史记录。" title="还没有审计事件" />
            )}
          </div>
        </Panel>
      </div>
      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="Audit">
          <div className="grid gap-2 text-sm">
            <div className="flex justify-between border-b border-[var(--line)] py-2"><span className="muted">记录数</span><strong>{audit.length}</strong></div>
            <div className="flex justify-between border-b border-[var(--line)] py-2"><span className="muted">存储</span><strong>SQLite</strong></div>
            <div className="flex justify-between py-2"><span className="muted">范围</span><strong>最近 100 条</strong></div>
          </div>
        </Panel>
      </aside>
    </div>
  );
}
