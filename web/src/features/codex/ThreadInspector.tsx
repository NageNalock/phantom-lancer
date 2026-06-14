import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexApproval, CodexStatus, CodexThread, CodexWorkspace, CodexWorktreeStatus } from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Pill, useDangerConfirm } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import { shouldHandleQueryLinkClick, useQueryParamState } from "../../hooks/useQueryParamState";
import { BrowserPane } from "./ThreadP1Panels/BrowserPane";
import { CommandPane } from "./ThreadP1Panels/CommandPane";
import { ReviewPane } from "./ThreadP1Panels/ReviewPane";

type InspectorView = "changes" | "preview" | "run" | "state";

const INSPECTOR_VIEWS: Array<[InspectorView, string]> = [
  ["changes", "变更"],
  ["preview", "预览"],
  ["run", "运行"],
  ["state", "状态"],
];
const INSPECTOR_VIEW_IDS: InspectorView[] = INSPECTOR_VIEWS.map(([id]) => id);

export function ThreadInspector({
  actions,
  approvals,
  status,
  thread,
  workspaces,
  onApprovalsChange,
  onDraft,
}: {
  actions: AppActions;
  approvals: CodexApproval[];
  status?: CodexStatus;
  thread: CodexThread | null;
  workspaces: CodexWorkspace[];
  onApprovalsChange: () => void;
  onDraft: (threadId: string, prompt: string) => void;
}) {
  const [view, setView, viewHref] = useQueryParamState<InspectorView>("codexInspector", INSPECTOR_VIEW_IDS, "changes", { clearKeys: ["codex", "codexInbox", "codexRuntime"] });
  const workspace = thread ? workspaces.find((item) => item.id === thread.workspaceId) : null;
  return (
    <section className="panel flex max-h-[calc(100dvh-8.5rem)] min-h-0 flex-col max-lg:max-h-none">
      <div className="panel-header">
        <div className="min-w-0">
          <h2 className="m-0 text-sm font-semibold">检查器</h2>
          <p className="muted mt-1 mb-0 text-xs">当前 thread 的审批、变更、预览和运行状态。</p>
        </div>
      </div>
      <div className="panel-body grid min-h-0 flex-1 grid-rows-[auto_auto_minmax(0,1fr)] gap-3 overflow-hidden text-sm">
        {thread ? <ApprovalStack actions={actions} approvals={approvals} onChange={onApprovalsChange} /> : null}
        {thread ? (
          <div className="flex min-w-0 gap-1 overflow-x-auto border-b border-[var(--line)] pb-2">
            {INSPECTOR_VIEWS.map(([id, label]) => (
              <a
                className={`shrink-0 rounded-md px-2 py-1 text-xs no-underline transition ${view === id ? "bg-[var(--surface-strong)] text-[var(--text)] shadow-[inset_0_-2px_0_var(--accent)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`}
                href={viewHref(id)}
                key={id}
                onClick={(event) => {
                  if (!shouldHandleQueryLinkClick(event)) return;
                  event.preventDefault();
                  setView(id);
                }}
              >
                {label}
              </a>
            ))}
          </div>
        ) : (
          <p className="muted m-0 text-sm">选择会话后显示工作区、CLI 状态、审批和诊断信息。</p>
        )}
        <div className="min-h-0 overflow-auto pr-1">
          {!thread ? <EmptyState title="未选择 thread" body="展开项目 / 会话后选择最近 thread，或创建新的代码任务。" /> : null}
          {thread && view === "changes" ? <ReviewPane actions={actions} thread={thread} onDraft={onDraft} onRefresh={onApprovalsChange} /> : null}
          {thread && view === "preview" ? <BrowserPane actions={actions} thread={thread} onDraft={onDraft} onRefresh={onApprovalsChange} /> : null}
          {thread && view === "run" ? <CommandPane actions={actions} thread={thread} onRefresh={onApprovalsChange} /> : null}
          {thread && view === "state" ? <StatePanel actions={actions} status={status} thread={thread} workspace={workspace} /> : null}
        </div>
      </div>
    </section>
  );
}

function ApprovalStack({ actions, approvals, onChange }: { actions: AppActions; approvals: CodexApproval[]; onChange: () => void }) {
  if (!approvals.length) return null;
  async function resolve(approval: CodexApproval, action: "approve" | "deny" | "cancel") {
    try {
      await actions.api(`/api/codex/approvals/${approval.id}/${action}`, { method: "POST", csrf: actions.csrf });
      onChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }
  return (
    <div className="grid gap-2">
      {approvals.map((approval) => (
        <div className="rounded-lg border border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)] p-2 text-xs" key={approval.id}>
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0">
              <strong className="block text-[var(--warn)]">等待审批</strong>
              {approval.commandPreview ? <span className="mono mt-1 block max-h-12 overflow-auto whitespace-pre-wrap text-[var(--muted-strong)]">{approval.commandPreview}</span> : null}
            </div>
            <Pill tone={approval.riskLevel === "high" ? "danger" : approval.riskLevel === "low" ? "good" : "warn"}>{approval.riskLevel || "medium"}</Pill>
          </div>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button className="h-8 min-h-8 px-2 text-xs" tone="primary" onClick={() => void resolve(approval, "approve")}>允许一次</Button>
            <Button className="h-8 min-h-8 px-2 text-xs" onClick={() => void resolve(approval, "cancel")}>取消</Button>
            <Button className="h-8 min-h-8 px-2 text-xs" tone="danger" onClick={() => void resolve(approval, "deny")}>拒绝</Button>
          </div>
        </div>
      ))}
    </div>
  );
}

function StatePanel({ actions, status, thread, workspace }: { actions: AppActions; status?: CodexStatus; thread: CodexThread; workspace?: CodexWorkspace | null }) {
  const [worktree, setWorktree] = useState<CodexWorktreeStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const loadWorktree = useCallback(async () => {
    if (thread.executionMode !== "worktree") return;
    try {
      const response = await actions.api<{ worktree?: CodexWorktreeStatus }>(`/api/codex/threads/${thread.id}/worktree`);
      setWorktree(response.worktree || null);
    } catch {
      // State panel still shows persisted thread metadata.
    }
  }, [actions, thread.executionMode, thread.id]);
  useEffect(() => {
    void loadWorktree();
  }, [loadWorktree]);

  async function discardWorktree() {
    const confirmed = await confirmDanger({
      title: "丢弃 worktree",
      body: "这会删除该 Codex 任务的隔离 worktree。",
      objectName: worktreeStatus.branchName || thread.branchName || thread.id,
      impact: [
        `worktree: ${worktreeStatus.worktreeSummary || thread.worktreeSummary || "-"}`,
        `dirty: ${worktreeStatus.dirtyStatus || "unknown"}`,
        "未 apply 回原工作区的修改会被删除。",
      ],
      recovery: "该操作不可从 Phantom Lancer 恢复。确认前请先 review diff 或 apply。",
      confirmLabel: "丢弃 worktree",
      confirmationText: thread.id,
      confirmationLabel: "输入 thread id 确认",
    });
    if (!confirmed) return;
    setBusy(true);
    try {
      const response = await actions.api<{ worktree?: CodexWorktreeStatus }>(`/api/codex/threads/${thread.id}/worktree`, { method: "POST", csrf: actions.csrf, body: { action: "discard" } });
      setWorktree(response.worktree || null);
      actions.setToast("已丢弃 worktree", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy(false);
    }
  }

  async function applyWorktree() {
    const confirmed = await confirmDanger({
      title: "应用 worktree diff",
      body: "这会把该隔离 worktree 的 diff 写回原工作区。",
      objectName: worktreeStatus.branchName || thread.branchName || thread.id,
      impact: [
        `worktree: ${worktreeStatus.worktreeSummary || thread.worktreeSummary || "-"}`,
        `dirty: ${worktreeStatus.dirtyStatus || "unknown"}`,
        "后端只会在原工作区 clean、HEAD 未变化、worktree 无 untracked 文件且 diff 未超限时执行。",
        "该操作不是 merge commit；它会把 diff 应用为原工作区的未提交修改。",
      ],
      recovery: "如果 apply 成功，原工作区会出现未提交修改；需要你继续 review、测试、提交或手动回滚。",
      confirmLabel: "应用 diff",
      confirmationText: thread.id,
      confirmationLabel: "输入 thread id 确认",
    });
    if (!confirmed) return;
    setBusy(true);
    try {
      const response = await actions.api<{ worktree?: CodexWorktreeStatus }>(`/api/codex/threads/${thread.id}/worktree`, { method: "POST", csrf: actions.csrf, body: { action: "apply" } });
      setWorktree(response.worktree || null);
      actions.setToast("已将 worktree diff 应用回原工作区", "good");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setBusy(false);
    }
  }

  const worktreeStatus = worktree || {
    worktreeSummary: thread.worktreeSummary,
    baseBranch: thread.baseBranch,
    branchName: thread.branchName,
    worktreeStatus: thread.worktreeStatus,
    mergeStatus: thread.mergeStatus,
    discardedAt: thread.discardedAt,
    dirtyStatus: "",
  };
  const worktreeDiscarded = Boolean(thread.discardedAt || worktree?.discardedAt || worktreeStatus.mergeStatus === "discarded");
  return (
    <div className="grid gap-3">
      {dangerConfirmDialog}
      <div className="grid gap-2">
        <InspectorRow label="工作区" value={workspace?.label || workspace?.pathSummary || "-"} />
        <InspectorRow label="路径" mono value={workspace?.pathSummary || "-"} />
        <InspectorRow label="Git" mono value={workspace?.gitBranch || "-"} />
        <InspectorRow label="信任" value={workspace?.trustState || "-"} />
        <InspectorRow label="模型" mono value={thread.model || "运行时探测"} />
        <InspectorRow label="沙箱" value={thread.sandboxMode || "-"} />
        <InspectorRow label="审批" value={thread.approvalPolicy || "-"} />
        <InspectorRow label="来源" value={thread.sourceMode === "exec" ? "exec 兜底" : "app-server"} />
        <InspectorRow label="执行" value={thread.executionMode === "worktree" ? "Worktree" : "当前工作区"} />
        {thread.executionMode === "worktree" ? (
          <>
            <InspectorRow label="worktree" mono value={worktreeStatus.worktreeSummary || "-"} />
            <InspectorRow label="base" mono value={worktreeStatus.baseBranch || "-"} />
            <InspectorRow label="branch" mono value={worktreeStatus.branchName || "-"} />
            <InspectorRow label="状态" value={`${worktreeStatus.worktreeStatus || "-"} / ${worktreeStatus.dirtyStatus || "unknown"}`} />
            <InspectorRow label="应用" value={worktreeStatus.mergeStatus || "not_merged"} />
          </>
        ) : null}
        <InspectorRow label="codex id" mono value={thread.codexThreadId || "-"} />
      </div>
      {thread.executionMode === "worktree" ? (
        <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs">
          <div className="flex flex-wrap gap-2">
            <Button className="h-8 min-h-8 px-2 text-xs" onClick={() => void loadWorktree()}>刷新 worktree</Button>
            <Button className="h-8 min-h-8 px-2 text-xs" disabled={busy || worktreeDiscarded || worktreeStatus.mergeStatus === "applied"} onClick={() => void applyWorktree()} tone="danger">应用到原工作区</Button>
            <Button className="h-8 min-h-8 px-2 text-xs" disabled={busy || worktreeDiscarded} onClick={() => void discardWorktree()} tone="danger">丢弃 worktree</Button>
          </div>
          {worktreeDiscarded ? (
            <span className="text-[var(--muted-strong)]">worktree 已丢弃，不能再应用 diff。</span>
          ) : worktreeStatus.mergeStatus === "applied" ? (
            <span className="text-[var(--good)]">diff 已写回原工作区；worktree 仍保留用于对照，可在确认后丢弃。</span>
          ) : (
            <span className="muted">应用仅在原工作区 clean、HEAD 未变化且 worktree diff 可安全应用时执行；merge commit 尚未开放。</span>
          )}
        </div>
      ) : null}
      {thread.lastError ? (
        <div className="rounded-lg border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] p-2 text-xs text-[var(--danger)]">
          {thread.lastError}
        </div>
      ) : null}
      <div className="border-t border-[var(--line)] pt-2">
        <InspectorRow label="CLI" value={status?.installation?.status || "-"} />
        <InspectorRow label="app-server" value={status?.appServer?.state || "-"} />
        <InspectorRow label="待审批" value={String(status?.pendingApprovals || 0)} />
        <InspectorRow label="运行" value={String(status?.runtime?.running || 0)} />
        <InspectorRow label="队列" value={String(status?.runtime?.queued || 0)} />
        <InspectorRow label="失败" value={String(status?.runtime?.failed || 0)} />
      </div>
      <span className="muted text-xs">更新于 {formatDate(thread.updatedAt)}</span>
    </div>
  );
}

function InspectorRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2">
      <span className="muted text-xs">{label}</span>
      <span className={`min-w-0 break-words text-sm ${mono ? "mono" : ""}`}>{value}</span>
    </div>
  );
}
