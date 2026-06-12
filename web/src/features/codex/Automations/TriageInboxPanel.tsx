import type { CodexTriageInbox } from "../../../app/types";
import { Pill } from "../../../components/ui";

export function TriageInboxPanel({
  triage,
  onArchiveRun,
  onArchiveThread,
  onPromoteThread,
  onResolveComment,
  onOpenThread,
}: {
  triage: CodexTriageInbox;
  onArchiveRun: (id: string) => void;
  onArchiveThread: (threadId: string) => void;
  onPromoteThread: (threadId: string) => void;
  onResolveComment: (id: string) => void;
  onOpenThread: (threadId: string) => void;
}) {
  const automationRuns = triage.automationRuns || [];
  const backgroundThreads = triage.backgroundThreads || [];
  const failedTurns = triage.failedTurns || [];
  const reviewComments = triage.reviewComments || [];

  return (
    <div className="grid gap-3">
      {automationRuns.length ? (
        <div className="grid gap-2">
          {automationRuns.map((run) => (
            <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={run.id}>
              <div className="flex justify-between gap-2">
                <span>{run.findingSummary || run.errorSummary || run.id}</span>
                <Pill tone={run.status === "failed" ? "danger" : "neutral"}>{run.status}</Pill>
              </div>
              <div className="mt-1 flex flex-wrap gap-2 text-[var(--muted)]">
                {run.threadId ? <span className="mono">thread {run.threadId}</span> : null}
                {run.turnId ? <span className="mono">turn {run.turnId}</span> : null}
              </div>
              <button className="mt-1 text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onArchiveRun(run.id)} type="button">归档</button>
              {run.threadId ? <button className="mt-1 ml-2 text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onOpenThread(run.threadId || "")} type="button">跳到会话</button> : null}
            </div>
          ))}
        </div>
      ) : (
        <p className="muted m-0 text-xs">当前没有待处理自动化结果。</p>
      )}

      {backgroundThreads.length ? (
        <div className="mt-3">
          <span className="text-xs font-semibold text-[var(--muted-strong)]">后台 thread</span>
          <div className="mt-2 grid gap-2">
            {backgroundThreads.map((thread) => (
              <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={thread.id}>
                <div className="flex justify-between gap-2">
                  <span>{thread.title || thread.id}</span>
                  <Pill tone={thread.status === "failed" ? "danger" : thread.status === "running" || thread.status === "queued" ? "warn" : "neutral"}>{thread.status || "idle"}</Pill>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-[var(--muted)]">
                  <span className="mono">{thread.backgroundSource || "background"}</span>
                  <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onOpenThread(thread.id)} type="button">跳到会话</button>
                  <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onPromoteThread(thread.id)} type="button">转普通会话</button>
                  {thread.status !== "archived" ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onArchiveThread(thread.id)} type="button">归档</button> : null}
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {failedTurns.length ? (
        <div className="mt-3">
          <span className="text-xs font-semibold text-[var(--muted-strong)]">失败的 turn</span>
          <div className="mt-2 grid gap-2">
            {failedTurns.map((turn) => (
              <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={turn.turnId}>
                <div className="flex justify-between gap-2">
                  <span>{turn.errorSummary || "turn failed"}</span>
                  <Pill tone="danger">failed</Pill>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-[var(--muted)]">
                  <span className="mono">turn {turn.turnId}</span>
                  <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onOpenThread(turn.threadId)} type="button">跳到会话</button>
                  <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onArchiveThread(turn.threadId)} type="button">归档会话</button>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {reviewComments.length ? (
        <div className="mt-3">
          <span className="text-xs font-semibold text-[var(--muted-strong)]">未解决 review comment</span>
          <div className="mt-2 grid gap-2">
            {reviewComments.map((comment) => (
              <div className="rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs" key={comment.id}>
                <div className="flex justify-between gap-2">
                  <span>{comment.body || comment.id}</span>
                  <Pill tone="warn">open</Pill>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-[var(--muted)]">
                  {comment.filePath ? <span className="mono">{comment.filePath}</span> : null}
                  {comment.threadId ? <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onOpenThread(comment.threadId || "")} type="button">跳到会话</button> : null}
                  <button className="text-[var(--muted-strong)] hover:text-[var(--text)]" onClick={() => onResolveComment(comment.id)} type="button">标记已解决</button>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
