import { useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexSession } from "../app/types";
import { Button, EmptyState, Panel, Pill } from "../components/ui";
import { friendlyError } from "../api/client";
import { auditLabel, formatDate, sessionStatusLabel, workspaceName } from "../domain/labels";

export function CodexReviewsView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [busy, setBusy] = useState("");
  const reviewEvents = data.audit.filter((item) => item.eventType === "codex.review.start").slice(0, 12);

  async function startReview(session: CodexSession) {
    setBusy(session.id);
    try {
      await actions.api(`/api/codex/sessions/${encodeURIComponent(session.id)}/review`, { method: "POST", csrf: actions.csrf, body: { target: "uncommittedChanges" } });
      await actions.reloadData();
      actions.setToast("已启动 Codex review", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_340px] gap-4 p-5 max-xl:grid-cols-1">
      <Panel subtitle="Review 默认检查当前项目或 worktree 的未提交改动，并在会话内继续输出。" title="Reviews">
        {data.codexSessions.length ? (
          <div className="grid gap-2">
            {data.codexSessions.map((session) => {
              const workspace = data.workspaces.find((item) => item.id === session.workspaceId);
              return (
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 max-md:grid-cols-1" key={session.id}>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="truncate text-sm">{session.title}</strong>
                      <Pill tone={session.status === "active" ? "good" : session.archived ? "warn" : "neutral"}>{sessionStatusLabel(session.status)}</Pill>
                      {session.model ? <Pill>{session.model}</Pill> : null}
                    </div>
                    <p className="muted mt-1 mb-0 truncate text-xs">{workspaceName(workspace)} / {session.codexThreadId || "no thread"}</p>
                  </div>
                  <div className="flex flex-wrap justify-end gap-2 max-md:justify-start">
                    <Button
                      onClick={() => {
                        void actions.setActiveSessionId(session.id);
                        actions.setCodexTab("sessions");
                      }}
                    >
                      打开
                    </Button>
                    <Button disabled={session.archived || busy === session.id} onClick={() => void startReview(session)} tone="primary">
                      Review
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <EmptyState title="暂无会话" body="先创建 Codex 会话，再从这里启动 review。" />
        )}
      </Panel>

      <aside className="grid content-start gap-4">
        <Panel title="最近 Review">
          {reviewEvents.length ? (
            <div className="grid gap-2">
              {reviewEvents.map((item) => (
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3" key={item.id}>
                  <strong className="block text-sm">{auditLabel(item.eventType)}</strong>
                  <span className="muted mt-1 block text-xs">{formatDate(item.createdAt)}</span>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无 review 历史" body="启动 review 后会写入活动审计。" />
          )}
        </Panel>
      </aside>
    </div>
  );
}
