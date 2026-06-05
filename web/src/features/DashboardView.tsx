import type { AppActions } from "../app/App";
import type { AppData } from "../app/types";
import { Button, ContextList, Metric, Panel } from "../components/ui";
import { auditLabel, auditSummary, formatDate, imageStatusLabel, v2rayStateLabel } from "../domain/labels";

export function DashboardView({ actions, data }: { actions: AppActions; data: AppData }) {
  const codex = data.codexStatus;
  const v2ray = data.v2ray.status || data.dashboard.v2ray;
  const images = data.images.status || data.dashboard.images;
  const activeSessions = data.codexSessions.filter((item) => item.status === "active").length;
  const latestAudit = data.audit[0];

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_332px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-5">
        <section className="grid grid-cols-5 gap-3 max-2xl:grid-cols-3 max-xl:grid-cols-2 max-sm:grid-cols-1">
          <Metric detail={codex.version || codex.error || "等待检测"} label="Codex" tone={codex.available ? "good" : "warn"} value={codex.available ? "可用" : "不可用"} />
          <Metric detail={`${activeSessions} 个正在运行`} label="会话" value={data.codexSessions.length} />
          <Metric
            detail={`${images?.historyCount || data.images.count || 0} 条历史`}
            label="Images"
            onClick={() => actions.setMainTab("images")}
            tone={images?.hasApiKey ? "good" : "warn"}
            value={imageStatusLabel(images)}
          />
          <Metric
            detail={v2ray?.endpoint || "未暴露端点"}
            label="V2Ray"
            onClick={() => actions.setMainTab("v2ray")}
            tone={v2ray?.running ? "good" : "warn"}
            value={v2rayStateLabel(v2ray)}
          />
          <Metric detail="Prompt first for risky actions" label="审批" tone={data.pendingApprovals.length ? "warn" : "good"} value={data.pendingApprovals.length} />
        </section>

        <Panel
          actions={
            <>
              <Button onClick={() => actions.setMainTab("images")}>打开 Images</Button>
              <Button onClick={() => actions.setMainTab("v2ray")}>配置 V2Ray</Button>
              <Button
                disabled={!data.workspaces.length}
                onClick={() => {
                  actions.setMainTab("codex");
                  actions.setCodexTab("sessions");
                }}
                tone="primary"
              >
                进入 Codex
              </Button>
            </>
          }
          subtitle="首屏保留服务器状态、主操作和必要上下文。"
          title="控制台"
        >
          <div className="grid gap-3">
            {data.workspaces.slice(0, 4).map((workspace) => (
              <div className="flex items-start justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={workspace.id}>
                <div>
                  <strong className="block text-sm">{workspace.name}</strong>
                  <p className="muted mono mt-1 mb-0 break-all text-xs">{workspace.rootPath}</p>
                </div>
                <Button
                  onClick={() => {
                    actions.setSelectedWorkspaceId(workspace.id);
                    actions.setMainTab("codex");
                    actions.setCodexTab("projects");
                  }}
                >
                  查看
                </Button>
              </div>
            ))}
            {!data.workspaces.length ? <div className="muted rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-4 text-center text-sm">先添加一个项目，再启动 Codex 会话。</div> : null}
          </div>
        </Panel>
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="Runtime">
          <ContextList
            items={[
              ["Codex CLI", codex.available ? "available" : "missing"],
              ["app-server", codex.appServerAvailable ? "available" : "unavailable"],
              ["Images", imageStatusLabel(images)],
              ["V2Ray", v2rayStateLabel(v2ray)],
              ["最近审计", latestAudit ? `${auditLabel(latestAudit.eventType)} / ${formatDate(latestAudit.createdAt)}` : "暂无"],
            ]}
          />
        </Panel>
        <div className="mt-4">
          <Panel title="最近活动">
            <div className="grid gap-2">
              {data.audit.slice(0, 5).map((item) => (
                <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs" key={item.id || `${item.eventType}-${item.createdAt}`}>
                  <strong className="block text-sm">{auditSummary(item)}</strong>
                  <span className="muted mt-1 block">{auditLabel(item.eventType)} / {formatDate(item.createdAt)}</span>
                </div>
              ))}
            </div>
          </Panel>
        </div>
      </aside>
    </div>
  );
}
