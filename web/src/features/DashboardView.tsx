import type { AppActions } from "../app/App";
import type { AppData } from "../app/types";
import { Button, ContextList, Metric, Panel } from "../components/ui";
import { auditLabel, auditSummary, codexGatewayStatusLabel, formatDate, imageStatusLabel, v2rayStateLabel } from "../domain/labels";

export function DashboardView({ actions, data }: { actions: AppActions; data: AppData }) {
  const gateway = data.codexGateway.status || data.dashboard.codexGateway;
  const v2ray = data.v2ray.status || data.dashboard.v2ray;
  const images = data.images.status || data.dashboard.images;
  const latestAudit = data.audit[0];

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_332px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-5">
        <section className="grid grid-cols-3 gap-3 max-xl:grid-cols-2 max-sm:grid-cols-1">
          <Metric
            detail={`${gateway?.activeAccounts || 0} accounts / ${gateway?.publicApiKeys || 0} keys`}
            label="Codex Gateway"
            onClick={() => actions.setMainTab("codex-gateway")}
            tone={gateway?.enabled && gateway?.activeAccounts && gateway?.publicApiKeys ? "good" : "warn"}
            value={codexGatewayStatusLabel(gateway)}
          />
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
        </section>

        <Panel
          actions={
            <>
              <Button onClick={() => actions.setMainTab("images")}>打开 Images</Button>
              <Button onClick={() => actions.setMainTab("v2ray")}>配置 V2Ray</Button>
              <Button onClick={() => actions.setMainTab("codex-gateway")} tone="primary">
                打开 Codex Gateway
              </Button>
            </>
          }
          subtitle="首屏保留服务器状态、主操作和必要上下文。"
          title="控制台"
        >
          <div className="grid gap-3">
            <ContextList
              items={[
                ["Codex Gateway", codexGatewayStatusLabel(gateway)],
                ["Images", imageStatusLabel(images)],
                ["V2Ray", v2rayStateLabel(v2ray)],
              ]}
            />
          </div>
        </Panel>
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="Runtime">
          <ContextList
            items={[
              ["Gateway", codexGatewayStatusLabel(gateway)],
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
              {!data.audit.length ? <div className="muted rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-center text-xs">暂无活动记录</div> : null}
            </div>
          </Panel>
        </div>
      </aside>
    </div>
  );
}
