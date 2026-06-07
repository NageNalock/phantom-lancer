import type { CodexStatus, CodexThread, CodexWorkspace } from "../../app/types";

export function ThreadInspector({ status, thread, workspaces }: { status?: CodexStatus; thread: CodexThread | null; workspaces: CodexWorkspace[] }) {
  const workspace = thread ? workspaces.find((item) => item.id === thread.workspaceId) : null;
  return (
    <section className="panel max-xl:col-span-2 max-lg:col-span-1">
      <div className="panel-header">
        <h2 className="m-0 text-sm font-semibold">Inspector</h2>
      </div>
      <div className="panel-body grid gap-3 text-sm">
        {thread ? (
          <>
            <InspectorRow label="工作区" value={workspace?.label || workspace?.pathSummary || "-"} />
            <InspectorRow label="路径" mono value={workspace?.pathSummary || "-"} />
            <InspectorRow label="信任" value={workspace?.trustState || "-"} />
            <InspectorRow label="模型" mono value={thread.model || "运行时探测"} />
            <InspectorRow label="沙箱" value={thread.sandboxMode || "-"} />
            <InspectorRow label="审批" value={thread.approvalPolicy || "-"} />
            <InspectorRow label="来源" value={thread.sourceMode === "exec" ? "exec 兜底" : "app-server"} />
            <InspectorRow label="codex id" mono value={thread.codexThreadId || "-"} />
          </>
        ) : (
          <p className="muted m-0 text-sm">选择会话后显示工作区、CLI 状态、审批和诊断信息。</p>
        )}
        <div className="mt-2 border-t border-[var(--line)] pt-2">
          <InspectorRow label="CLI" value={status?.installation?.status || "-"} />
          <InspectorRow label="app-server" value={status?.appServer?.state || "-"} />
          <InspectorRow label="待审批" value={String(status?.pendingApprovals || 0)} />
        </div>
      </div>
    </section>
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
