import { useEffect } from "react";
import type { AppActions } from "../../app/App";
import { Button, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
import { buildQueryHref } from "../../hooks/useQueryParamState";
import {
  friendlyError,
  mailRuntimeProbe,
  mailRuntimeStart,
  mailRuntimeStop,
  mailRuntimeRestart,
  type MailRuntimeStatus,
} from "../../api/client";

function fmtDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n: number) => n.toString().padStart(2, "0");
  if (h > 0) return `${h}h ${pad(m)}m ${pad(s)}s`;
  if (m > 0) return `${m}m ${pad(s)}s`;
  return `${s}s`;
}

type PillTone = "good" | "warn" | "danger" | "neutral";

function statusPillTone(status: MailRuntimeStatus | null): PillTone {
  if (!status) return "neutral";
  if (status.crash_loop_state) return "danger";
  if (status.observed_state === "running" && status.desired_state === "running") return "good";
  if (status.observed_state === "stopped" || status.observed_state === "unknown") return "neutral";
  if (status.overall === "critical" || status.overall === "error") return "danger";
  if (status.overall === "warn") return "warn";
  return "neutral";
}

function statusPillLabel(status: MailRuntimeStatus | null): string {
  if (!status) return "未加载";
  if (status.crash_loop_state) return `崩溃循环 (${status.consecutive_failures})`;
  if (status.import_mode) return "只读接入";
  if (status.observed_state === "running" && status.desired_state === "running") return "运行中";
  if (status.observed_state === "stopped") return "已停止";
  if (status.observed_state === "unknown") return "未知";
  return status.observed_state || "—";
}

const PROBE_LAYERS: Array<{ layer: number; label: string }> = [
  { layer: 1, label: "L1 Process" },
  { layer: 2, label: "L2 Control" },
  { layer: 3, label: "L3 WebAPI" },
  { layer: 4, label: "L4 SMTP" },
  { layer: 5, label: "L5 IMAP" },
  { layer: 6, label: "L6 DNS" },
  { layer: 7, label: "L7 Delivery" },
  { layer: 8, label: "L8 Certs" },
  { layer: 9, label: "L9 Reputation" },
];

function probeColor(state: string | undefined): string {
  switch (state) {
    case "good":
      return "var(--good)";
    case "warn":
      return "var(--warn)";
    case "critical":
    case "error":
      return "var(--danger)";
    default:
      return "var(--muted)";
  }
}

export function MailOverviewTab({
  actions,
  status,
  reload,
}: {
  actions: AppActions;
  status: MailRuntimeStatus | null;
  reload: () => Promise<void>;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  useEffect(() => {
    // no-op, placeholder for future effects
  }, []);

  async function handleStart() {
    try {
      const res = await mailRuntimeStart({}, actions.csrf);
      actions.setToast(res.message || "Mox 启动请求已接受", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleStop() {
    const ok = await confirmDanger({
      title: "停止 Mox",
      body: "停止 Mox 后，所有收件、发件、IMAP 与 WebMail 访问将不可用，直到重新启动。",
      confirmLabel: "确认停止",
      impact: ["SMTP 25/465/587 端口将不再接受连接", "IMAP/IMAPS 访问中断", "WebMail 面板离线", "队列中的待投递邮件将在重启后重试"],
      recovery: "可在本面板点击「启动」恢复服务。",
    });
    if (!ok) return;
    try {
      const res = await mailRuntimeStop({}, actions.csrf);
      actions.setToast(res.message || "Mox 停止请求已接受", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleRestart() {
    const ok = await confirmDanger({
      title: "重启 Mox",
      body: "重启会先安全关闭 Mox，然后重新拉起。该过程将造成约 10-30 秒的服务中断。",
      confirmLabel: "确认重启",
      impact: ["短暂中断所有连接", "可能触发 DNS / TLS 证书刷新", "现有 IMAP 会话将被断开"],
      recovery: "Mox 将自动恢复运行，本页面会自动刷新状态。",
    });
    if (!ok) return;
    try {
      const res = await mailRuntimeRestart({}, actions.csrf);
      actions.setToast(res.message || "Mox 重启请求已接受", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleProbe() {
    try {
      const res = await mailRuntimeProbe({}, actions.csrf);
      actions.setToast(`探针完成，共 ${res.results.length} 项，总体：${res.overall}`, "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  const pillTone = statusPillTone(status);
  const pillLabel = statusPillLabel(status);
  const isImport = !!status?.import_mode;
  const emergency = status?.emergency_inbound_reject;

  const headerActions = (
    <div className="flex flex-wrap gap-2">
      <Button tone="primary" onClick={handleStart} disabled={isImport}>
        启动
      </Button>
      <Button tone="danger" onClick={handleStop} disabled={isImport}>
        停止
      </Button>
      <Button tone="danger" onClick={handleRestart} disabled={isImport}>
        重启
      </Button>
    </div>
  );

  return (
    <div className="grid gap-4 pt-4">
      {dangerConfirmDialog}
      <Panel
        title="运行概览"
        subtitle={
          isImport
            ? "当前为外部 Mox 只读接入模式，启停操作已禁用。"
            : "展示 Mox 运行状态、9 层探针摘要与快捷生命周期操作。"
        }
        actions={headerActions}
      >
        <div className="grid gap-4">
          {emergency?.enabled ? (
            <Notice tone="danger">
              <strong>域禁用降级保护已启用。</strong> 当前通过 Mox Domain.Disabled 禁用域；已有队列和邮箱内容不会被删除。
              <a className="button ml-3 min-h-8 px-2 text-xs" href={buildQueryHref({ mail: "emergency" }, ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings"])}>
                打开入站保护
              </a>
            </Notice>
          ) : (
            <div className="flex items-center justify-between gap-3 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
              <span className="muted">
                入站保护当前是 Domain.Disabled 降级实现；正式 early SMTP reject 尚未完成。
              </span>
              <a className="button ml-3 min-h-8 px-2 text-xs" href={buildQueryHref({ mail: "emergency" }, ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings"])}>
                查看入站保护
              </a>
            </div>
          )}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Pill tone={pillTone}>{pillLabel}</Pill>
              {emergency?.enabled ? <Pill tone="danger">降级保护</Pill> : null}
              {isImport ? <Pill tone="warn">只读接入</Pill> : null}
            </div>
            <span className="muted text-xs">
              最近变化：{status?.last_change_at || "—"}；最近探针：{status?.last_probe_at || "—"}
            </span>
          </div>

          <div className="grid grid-cols-[160px_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
            <span className="text-[var(--muted-strong)]">配置模式</span>
            <span>
              {status?.config_mode === "managed"
                ? "托管模式"
                : status?.config_mode === "import"
                ? "外部只读接入"
                : "尚未初始化"}
            </span>

            <span className="text-[var(--muted-strong)]">期望状态</span>
            <span>{status?.desired_state === "running" ? "运行" : status?.desired_state === "stopped" ? "停止" : "—"}</span>

            <span className="text-[var(--muted-strong)]">观察状态</span>
            <span>{status?.observed_state || "—"}</span>

            <span className="text-[var(--muted-strong)]">PID</span>
            <span className="font-mono text-xs">{status?.pid && status.pid > 0 ? status.pid : "—"}</span>

            <span className="text-[var(--muted-strong)]">运行时长</span>
            <span className="font-mono text-xs">{fmtDuration(status?.uptime_ms ?? 0)}</span>

            <span className="text-[var(--muted-strong)]">boot_id</span>
            <span className="font-mono text-[10px] text-[var(--muted-strong)]">{status?.boot_id || "—"}</span>

            <span className="text-[var(--muted-strong)]">崩溃循环</span>
            <span>
              {status?.crash_loop_state
                ? `${status.crash_loop_state}  连续失败 ${status.consecutive_failures} 次，退避 ${fmtDuration(
                    status.backoff_remaining_ms ?? 0,
                  )}`
                : "无"}
            </span>

            <span className="text-[var(--muted-strong)]">域名数</span>
            <span>{status?.domain_count ?? 0}</span>

            <span className="text-[var(--muted-strong)]">账户数</span>
            <span>{status?.account_count ?? 0}</span>

            <span className="text-[var(--muted-strong)]">入站保护</span>
            <span>
              {emergency?.enabled
                ? `域禁用降级保护 · ${emergency.affected_domains ?? 0} 个域 · ${emergency.actual_mox_strategy}`
                : "未开启"}
            </span>
          </div>

          <div>
            <div className="muted mb-2 text-xs">9 层探针摘要</div>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
              {PROBE_LAYERS.map((l) => {
                const probe = status?.probes?.find((p) => p.layer === l.layer);
                const dotColor = probeColor(probe?.state || status?.overall);
                return (
                  <div key={l.layer} className="flex items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs">
                    <span
                      className="inline-block h-3 w-3 rounded-full border border-[var(--line)]"
                      style={{ backgroundColor: dotColor }}
                      title={probe?.message || `${l.label} state: ${probe?.state ?? status?.overall ?? "unknown"}`}
                    />
                    <span className="min-w-0 flex-1 truncate">{l.label}</span>
                    <span className="muted font-mono text-[10px]">{probe?.state || "—"}</span>
                  </div>
                );
              })}
            </div>
          </div>

          <div className="flex items-center justify-between border-t border-[var(--line)] pt-3">
            <span className="muted text-xs">点击执行全量探针，将刷新全部 9 层状态。</span>
            <Button tone="primary" onClick={handleProbe}>
              立即探针
            </Button>
          </div>
        </div>
      </Panel>
    </div>
  );
}
