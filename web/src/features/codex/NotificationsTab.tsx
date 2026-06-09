import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexNotification } from "../../app/types";
import { Button, EmptyState, Panel, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { formatDate } from "../../domain/labels";

export function NotificationsTab({ actions, onOpenThread }: { actions: AppActions; onOpenThread: (threadId: string) => void }) {
  const [items, setItems] = useState<CodexNotification[]>([]);
  const [status, setStatus] = useState("unread");

  const load = useCallback(async () => {
    const [thread, automation, appServer] = await Promise.all([
      actions.api<{ items?: CodexNotification[] }>(`/api/notifications?scope=codex.thread&status=${status}`),
      actions.api<{ items?: CodexNotification[] }>(`/api/notifications?scope=codex.automation&status=${status}`),
      actions.api<{ items?: CodexNotification[] }>(`/api/notifications?scope=codex.app_server&status=${status}`),
    ]);
    setItems([...(thread.items || []), ...(automation.items || []), ...(appServer.items || [])]);
  }, [actions, status]);

  useEffect(() => {
    void load().catch((error) => actions.setToast(friendlyError(error), "danger"));
  }, [actions, load]);

  // Live updates: the backend publishes summary-only events on a fixed scope when
  // a notification is created, so the center refreshes without polling.
  useEffect(() => {
    const params = new URLSearchParams({ scope: "codex.notifications", id: "default" });
    const source = new EventSource(`/api/events/stream?${params.toString()}`);
    const refresh = () => {
      void load().catch(() => {});
    };
    source.addEventListener("codex.notification.created", refresh);
    source.onerror = () => source.close();
    return () => source.close();
  }, [load]);

  async function update(id: string, nextStatus: string) {
    try {
      await actions.api(`/api/notifications/${id}`, { method: "PATCH", csrf: actions.csrf, body: { status: nextStatus } });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function archiveRead() {
    try {
      await actions.api("/api/notifications/archive-read", { method: "POST", csrf: actions.csrf, body: { scope: "codex.thread" } });
      await actions.api("/api/notifications/archive-read", { method: "POST", csrf: actions.csrf, body: { scope: "codex.automation" } });
      await actions.api("/api/notifications/archive-read", { method: "POST", csrf: actions.csrf, body: { scope: "codex.app_server" } });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <Panel
      actions={
        <>
          <select className="select" onChange={(event) => setStatus(event.target.value)} value={status}>
            <option value="unread">未读</option>
            <option value="read">已读</option>
            <option value="all">全部</option>
          </select>
          <Button onClick={() => void archiveRead()}>归档已读</Button>
          <Button onClick={() => void load()}>刷新</Button>
        </>
      }
      subtitle="聚合待审批、turn 完成/失败、自动化和命令结果摘要。"
      title="Notifications"
    >
      {items.length ? (
        <div className="grid gap-2">
          {items.map((item) => (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={item.id}>
              <div className="flex items-start justify-between gap-2">
                <div>
                  <strong className="text-sm">{item.title || item.eventType}</strong>
                  <p className="muted mt-1 mb-0 text-xs">{item.summary}</p>
                </div>
                <Pill tone={item.severity === "danger" ? "danger" : item.severity === "warn" ? "warn" : "neutral"}>{item.status}</Pill>
              </div>
              <div className="mt-2 flex flex-wrap gap-2 text-xs">
                <span className="muted">{formatDate(item.createdAt)}</span>
                {item.status !== "read" ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => void update(item.id, "read")} type="button">标记已读</button> : null}
                {item.status !== "archived" ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => void update(item.id, "archived")} type="button">归档</button> : null}
                {notificationThreadId(item) ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onOpenThread(notificationThreadId(item))} type="button">跳到会话</button> : null}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState title="暂无通知" body="Codex 关键状态变化会以摘要形式进入通知中心。" />
      )}
    </Panel>
  );
}

function notificationThreadId(item: CodexNotification): string {
  const value = item.payload?.threadId;
  return typeof value === "string" ? value : "";
}
