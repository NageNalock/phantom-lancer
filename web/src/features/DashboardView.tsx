import type { AppActions } from "../app/App";
import type { AppData, MainTab } from "../app/types";
import type { MouseEvent } from "react";
import { useState } from "react";
import { ContextList, Metric, Panel } from "../components/ui";
import { auditLabel, auditSummary, codexGatewayStatusLabel, codexModuleStatusLabel, formatDate, imageStatusLabel, stockV2StatusLabel, v2rayStateLabel } from "../domain/labels";
import { shouldHandleQueryLinkClick } from "../hooks/useQueryParamState";
import { formatBytesIEC } from "../utils/format";

interface DashboardAction {
  title: string;
  body: string;
  label: string;
  tab: MainTab;
  tone: "neutral" | "good" | "warn" | "danger";
}

export function DashboardView({ actions, data }: { actions: AppActions; data: AppData }) {
  const gateway = data.codexGateway.status || data.dashboard.codexGateway;
  const v2ray = data.v2ray.status || data.dashboard.v2ray;
  const images = data.images.status || data.dashboard.images;
  const codex = data.dashboard.codex;
  const stockv2 = data.stockv2;
  const stockInstrumentTotal = stockv2?.instrumentTotal ?? stockv2?.instruments?.length ?? 0;
  const latestAudit = data.audit[0];
  const nextActions = dashboardNextActions(codex, gateway, images, v2ray);
  const allowedRoots = data.settings.runtime?.allowedRoots || [];
  const handleMainTabLink = (event: MouseEvent<HTMLAnchorElement | HTMLButtonElement>, tab: MainTab) => {
    if (event.currentTarget instanceof HTMLAnchorElement) {
      if (!shouldHandleQueryLinkClick(event as MouseEvent<HTMLAnchorElement>)) return;
      event.preventDefault();
    }
    actions.setMainTab(tab);
  };

  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_332px] max-xl:grid-cols-1">
      <div className="grid content-start gap-4 p-5">
        <section className="grid grid-cols-3 gap-3 max-xl:grid-cols-2 max-sm:grid-cols-1">
          <Metric
            detail={codexCardDetail(codex)}
            href={actions.mainTabHref("codex")}
            label="Codex"
            onClick={(event) => handleMainTabLink(event, "codex")}
            tone={codexCardTone(codex)}
            value={codexModuleStatusLabel(codex)}
          />
          <Metric
            detail={`${gateway?.activeAccounts || 0} accounts / ${gateway?.publicApiKeys || 0} keys`}
            href={actions.mainTabHref("codex-gateway")}
            label="Codex Gateway"
            onClick={(event) => handleMainTabLink(event, "codex-gateway")}
            tone={gateway?.enabled && gateway?.activeAccounts && gateway?.publicApiKeys ? "good" : "warn"}
            value={codexGatewayStatusLabel(gateway)}
          />
          <Metric
            detail={`${images?.historyCount || data.images.count || 0} 条历史`}
            href={actions.mainTabHref("images")}
            label="多媒体"
            onClick={(event) => handleMainTabLink(event, "images")}
            tone={images?.hasApiKey ? "good" : "warn"}
            value={imageStatusLabel(images)}
          />
          <Metric
            detail={v2ray?.endpoint || "未暴露端点"}
            href={actions.mainTabHref("v2ray")}
            label="V2Ray"
            onClick={(event) => handleMainTabLink(event, "v2ray")}
            tone={v2ray?.running ? "good" : "warn"}
            value={v2rayStateLabel(v2ray)}
          />
          <Metric
            detail={`${stockv2?.portfolios?.length || 0} 组合 / ${stockInstrumentTotal} 标的`}
            href={actions.mainTabHref("stockv2")}
            label="股票V2"
            onClick={(event) => handleMainTabLink(event, "stockv2")}
            tone={stockv2?.updateJobs?.some((job) => job.status === "running") ? "warn" : stockInstrumentTotal ? "good" : "neutral"}
            value={stockV2StatusLabel(stockv2)}
          />
        </section>

        <Panel
          subtitle="只展示需要 owner 处理的风险、阻塞项和下一步入口。"
          title="风险与下一步"
        >
          {nextActions.length ? (
            <div className="grid gap-2">
              {nextActions.map((item) => (
                <div className={`grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border p-3 ${dashboardActionClass(item.tone)}`} key={item.title}>
                  <div className="min-w-0">
                    <strong className="block text-sm">{item.title}</strong>
                    <span className="muted mt-1 block text-xs leading-relaxed">{item.body}</span>
                  </div>
                  <a
                    className="button"
                    href={actions.mainTabHref(item.tab)}
                    onClick={(event) => handleMainTabLink(event, item.tab)}
                  >
                    {item.label}
                  </a>
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <strong className="block text-sm">当前没有阻塞项</strong>
              <p className="muted mt-1 mb-0 text-xs">需要操作的模块会在这里出现；日常状态保留在上方指标卡。</p>
            </div>
          )}
        </Panel>
      </div>

      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="运行边界">
          <ContextList
            items={[
              ["最近审计", latestAudit ? `${auditLabel(latestAudit.eventType)} / ${formatDate(latestAudit.createdAt)}` : "暂无"],
              ["允许根目录", allowedRoots.length ? `${allowedRoots.length} 个` : "未配置"],
              ["Cookie", data.settings.runtime?.cookieSecure ? "Secure" : "本地/HTTP"],
              ["配置文件", data.settings.file?.configPath || "-"],
              ["数据目录", data.settings.file?.dataDir || "-"],
            ]}
          />
        </Panel>
        <div className="mt-4">
          <DatabasePanel data={data} />
        </div>
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

function dashboardNextActions(
  codex: AppData["dashboard"]["codex"] | undefined,
  gateway: AppData["dashboard"]["codexGateway"] | undefined,
  images: AppData["dashboard"]["images"] | undefined,
  v2ray: AppData["dashboard"]["v2ray"] | undefined,
): DashboardAction[] {
  const items: DashboardAction[] = [];
  if (codex?.pendingApprovals) {
    items.push({
      title: `${codex.pendingApprovals} 个 Codex 审批待处理`,
      body: "审批会阻塞当前 turn，处理后会写入审计并恢复执行链路。",
      label: "处理审批",
      tab: "codex",
      tone: "warn",
    });
  } else if (!codex?.enabled || codex.installation?.status === "needs_setup" || codex.installation?.status === "unavailable") {
    items.push({
      title: "Codex CLI 需要检查",
      body: "会话能力依赖本机 codex CLI、认证状态和 app-server runtime。",
      label: "打开 Codex",
      tab: "codex",
      tone: "warn",
    });
  } else if (codex.appServer?.state === "failed") {
    items.push({
      title: "Codex app-server 异常",
      body: "进入运行诊断查看失败摘要，再决定是否重新启动 runtime。",
      label: "运行诊断",
      tab: "codex",
      tone: "danger",
    });
  }

  if (!gateway?.enabled || !gateway.publicApiKeys || !gateway.activeAccounts) {
    items.push({
      title: "Codex Gateway 未就绪",
      body: !gateway?.enabled ? "Gateway 当前未启用。" : !gateway.publicApiKeys ? "缺少对外 API key。" : "缺少可用上游账号。",
      label: "配置 Gateway",
      tab: "codex-gateway",
      tone: "warn",
    });
  } else if (gateway.recentFailureCount) {
    items.push({
      title: "Gateway 最近请求失败",
      body: "查看账号健康、模型刷新和请求日志，确认是否为上游或凭据问题。",
      label: "查看 Gateway",
      tab: "codex-gateway",
      tone: "warn",
    });
  }

  if (!images?.hasApiKey) {
    items.push({
      title: "多媒体缺少 provider key",
      body: "生成任务需要先配置多媒体模块的 xAI / Agnes provider 密钥。",
      label: "配置多媒体",
      tab: "images",
      tone: "warn",
    });
  } else if (images.lastJobStatus === "failed") {
    items.push({
      title: "多媒体最近任务失败",
      body: "进入历史查看上游错误摘要，必要时检查对象存储写入策略。",
      label: "查看历史",
      tab: "images",
      tone: "warn",
    });
  }

  if (v2ray?.stale) {
    items.push({
      title: "V2Ray 配置待重启",
      body: "已保存配置尚未被运行进程加载，建议在服务状态中执行保存并重启。",
      label: "打开 V2Ray",
      tab: "v2ray",
      tone: "warn",
    });
  } else if (!v2ray?.running) {
    items.push({
      title: "V2Ray 当前未运行",
      body: "如果需要远程接入，先确认配置校验通过，再从服务状态启动。",
      label: "打开 V2Ray",
      tab: "v2ray",
      tone: "neutral",
    });
  }

  return items.slice(0, 5);
}

function dashboardActionClass(tone: DashboardAction["tone"]): string {
  if (tone === "danger") return "border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)]";
  if (tone === "warn") return "border-[rgba(199,85,8,0.22)] bg-[var(--warn-soft)]";
  if (tone === "good") return "border-[rgba(18,132,79,0.18)] bg-[var(--good-soft)]";
  return "border-[var(--line)] bg-[var(--surface-soft)]";
}

function codexCardDetail(codex?: AppData["dashboard"]["codex"]): string {
  if (!codex) return "未探测";
  const parts = [`${codex.threadCount || 0} 会话`, `${codex.workspaceCount || 0} 工作区`];
  if (codex.pendingApprovals) parts.push(`${codex.pendingApprovals} 待审批`);
  return parts.join(" / ");
}

function codexCardTone(codex?: AppData["dashboard"]["codex"]) {
  if (!codex || !codex.enabled) return "warn" as const;
  if (codex.pendingApprovals) return "warn" as const;
  const install = codex.installation?.status;
  if (install === "needs_setup" || install === "unavailable") return "warn" as const;
  if (codex.appServer?.state === "failed") return "danger" as const;
  if (codex.appServer?.state === "running" || install === "ready") return "good" as const;
  return "neutral" as const;
}

const DB_TOP_TABLES_DEFAULT = 8;

function DatabasePanel({ data }: { data: AppData }) {
  const stats = data.dashboard.dbStats;
  const [expanded, setExpanded] = useState(false);

  if (!stats || !stats.tables?.length || stats.totalBytes === 0) {
    return (
      <Panel title="数据库">
        <div className="muted text-xs">正在加载数据库统计…</div>
      </Panel>
    );
  }

  const visibleTables = expanded ? stats.tables : stats.tables.slice(0, DB_TOP_TABLES_DEFAULT);
  const hiddenCount = stats.tables.length - DB_TOP_TABLES_DEFAULT;

  return (
    <Panel title="数据库">
      <div className="mb-3 flex items-baseline justify-between">
        <span className="text-xl font-semibold">{formatBytesIEC(stats.totalBytes)}</span>
        <span className="muted text-[11px]">共 {stats.tables.length} 张表</span>
      </div>
      <div className="grid gap-1.5">
        {visibleTables.map((table) => {
          const totalTableBytes = (table.sizeBytes || 0) + (table.indexSizeBytes || 0);
          const pct = stats.totalBytes > 0 ? (totalTableBytes / stats.totalBytes) * 100 : 0;
          return (
            <div
              className="group relative grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-md px-2 py-1.5 hover:bg-[var(--surface)]"
              key={table.name}
              title={table.description || table.name}
            >
              <div className="relative overflow-hidden">
                <div
                  className="absolute inset-y-0 left-0 bg-[var(--accent-soft)] opacity-40 group-hover:opacity-60"
                  style={{ width: `${Math.max(pct, 0.5)}%` }}
                />
                <span className="relative mono text-[12px]">{table.name}</span>
                {table.description ? (
                  <span className="relative ml-2 hidden text-[10px] text-[var(--muted)] max-md:inline">
                    {table.description}
                  </span>
                ) : null}
              </div>
              <span className="relative mono text-[11px] text-[var(--muted)]">
                {formatBytesIEC(totalTableBytes)}
              </span>
            </div>
          );
        })}
      </div>
      {hiddenCount > 0 ? (
        <button
          className="mt-2 w-full text-center text-[11px] text-[var(--muted)] underline decoration-dotted underline-offset-2 hover:text-[var(--fg)]"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? "收起" : `展开剩余 ${hiddenCount} 张表`}
        </button>
      ) : null}
      <div className="mt-2 text-center text-[10px] text-[var(--muted)]">
        统计更新于 {formatDate(stats.updatedAt) || "-"}
      </div>
    </Panel>
  );
}
