import { useState } from "react";
import type { AppActions } from "../../app/App";
import { Button, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
import { buildQueryHref } from "../../hooks/useQueryParamState";
import {
  friendlyError,
  mailEmergencyInboundRejectDisable,
  mailEmergencyInboundRejectEnable,
  type MailEmergencyInboundRejectState,
  type MailRuntimeStatus,
} from "../../api/client";

type EmergencyPanelMode = "full" | "compact";

export function MailEmergencyPanel({
  actions,
  reload,
  status,
  mode = "full",
  title = "域禁用降级保护",
}: {
  actions: AppActions;
  reload: () => Promise<void>;
  status: MailRuntimeStatus | null;
  mode?: EmergencyPanelMode;
  title?: string;
}) {
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const emergency = status?.emergency_inbound_reject;
  const isImport = !!status?.import_mode;
  const [reason, setReason] = useState("");
  const rollbackRisk = emergency?.apply_unknown || emergency?.restore_conflict === "apply_failed_rollback_unknown" || (emergency?.last_rollback_result || "").includes("rollback_failed");
  const probeRisk = (emergency?.last_probe_result || "").includes("failed") || (emergency?.last_probe_result || "").includes("error");

  async function handleEmergencyEnable(minutes?: number) {
    const autoRestore = minutes ? new Date(Date.now() + minutes * 60 * 1000).toISOString() : undefined;
    const reasonText = mode === "full" ? reason.trim() : "";
    const ok = await confirmDanger({
      title: "启用域禁用降级保护",
      body: "这不是最终的早期 SMTP 拒收能力。当前会通过 Mox Domain.Disabled 禁用已启用域，作为遇到爆量入站时的受控 fallback。",
      confirmLabel: "启用降级保护",
      confirmationText: "REJECT-INBOUND",
      impact: [
        "所有已启用域会被写入 Disabled: true",
        "已有队列、已有邮箱内容和日志不会被删除",
        "这会同时影响这些域的提交发送、ACME 和域级配置行为",
        "会执行 configapply、reload/restart 和 probe，失败会回滚并保留失败步骤",
      ],
      recovery: "攻击缓解后点击恢复域配置。若启用自动恢复，到期后后台会尝试恢复，失败时保持降级保护并写 high-risk 事件。",
    });
    if (!ok) return;
    try {
      const res = await mailEmergencyInboundRejectEnable({
        confirmation: "REJECT-INBOUND",
        reason: reasonText || "manual emergency protection from Mail control plane",
        auto_restore_at: autoRestore,
      }, actions.csrf);
      actions.setToast(res.pipeline?.summary || "已启用域禁用降级保护", "danger");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  async function handleEmergencyDisable() {
    const ok = await confirmDanger({
      title: "恢复域配置",
      body: "恢复会移除 Domain.Disabled 降级保护并重新应用 Mox 配置。若开启期间检测到配置漂移，系统会要求先解决冲突，避免覆盖中间合法变更。",
      confirmLabel: "恢复域配置",
      confirmationText: "RESTORE-INBOUND",
      impact: ["新入站 SMTP 投递将重新按域名、账户和别名配置处理", "已有队列不会被自动清理或重投递", "恢复失败时拒收状态会保持开启"],
      recovery: "如果恢复失败，请在 Events/Logs 查看失败步骤、rollback 和 probe 结果后再处理。",
    });
    if (!ok) return;
    try {
      const res = await mailEmergencyInboundRejectDisable({
        confirmation: "RESTORE-INBOUND",
        reason: "manual restore from Mail control plane",
      }, actions.csrf);
      actions.setToast(res.pipeline?.summary || "已恢复域配置", "good");
      await reload();
    } catch (e) {
      actions.setToast(friendlyError(e), "danger");
    }
  }

  const actionsSlot = emergency?.enabled ? (
    <Button tone="primary" onClick={handleEmergencyDisable} disabled={isImport}>恢复域配置</Button>
  ) : (
    <div className="flex flex-wrap gap-2">
      <Button tone="danger" onClick={() => void handleEmergencyEnable()} disabled={isImport}>启用降级保护</Button>
      {mode === "full" ? (
        <>
          <Button onClick={() => void handleEmergencyEnable(15)} disabled={isImport}>15 分钟</Button>
          <Button onClick={() => void handleEmergencyEnable(60)} disabled={isImport}>1 小时</Button>
          <Button onClick={() => void handleEmergencyEnable(240)} disabled={isImport}>4 小时</Button>
        </>
      ) : null}
    </div>
  );

  const body = (
    <div className="grid gap-3">
      {mode === "full" && !emergency?.enabled ? (
        <Field label="开启原因">
          <textarea
            className="input min-h-[72px] resize-y"
            maxLength={240}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="例如：入站投递爆量、队列接近打满、疑似收信攻击"
          />
        </Field>
      ) : null}
      {emergency?.enabled ? (
        <Notice tone="danger">
          <strong>域禁用降级保护已启用。</strong> 当前通过 Mox Domain.Disabled 禁用域；自动恢复：{emergency.auto_restore_at || "手动恢复"}。
        </Notice>
      ) : null}
      {rollbackRisk ? (
        <Notice tone="danger">
          <strong>配置状态未知。</strong> 最近一次 apply/reload/probe 失败后未确认完成回滚。请打开事件详情查看 pipeline，并先解决 drift/磁盘配置后再恢复域配置。
        </Notice>
      ) : null}
      {emergency?.restore_conflict === "config_drifted" ? (
        <Notice tone="danger">
          <strong>恢复被配置漂移阻断。</strong> 期望 hash：{emergency.restore_expected_hash || "-"}；磁盘 hash：{emergency.restore_disk_hash || "-"}。请先在配置漂移处理里选择以 Phantom 或磁盘为准。
        </Notice>
      ) : null}
      {probeRisk ? (
        <Notice tone="danger">
          <strong>最近探针未通过。</strong> {emergency.last_probe_result}。请先查看 Logs/Events，再决定是否恢复域配置。
        </Notice>
      ) : null}
      {emergency?.auto_restore_blocked_at ? (
        <Notice tone="danger">
          <strong>自动恢复已暂停重试。</strong> 针对 {emergency.auto_restore_blocked_at} 的自动恢复已经失败一次；请人工处理后再恢复入站或重新设置自动恢复时间。
        </Notice>
      ) : null}
      {emergency?.degraded_implementation ? (
        <Notice tone="warn">
          <strong>当前是降级实现。</strong> {emergency.degraded_reason || "使用 Mox Domain.Disabled 作为受控 fallback，可能影响提交发送和 ACME。"}
        </Notice>
      ) : null}
      {emergency?.last_failure ? (
        <Notice tone="danger">
          <strong>最近一次入站保护操作失败。</strong> {emergency.last_failure}
        </Notice>
      ) : null}
      <EmergencyStateGrid emergency={emergency} compact={mode === "compact"} />
    </div>
  );

  if (mode === "compact") {
    return (
      <div className="grid gap-3 border-t border-[var(--line)] pt-3">
        {dangerConfirmDialog}
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <strong className="block text-sm">{title}</strong>
            <p className="muted m-0 mt-1 text-xs">当前不是正式早期 SMTP 拒收能力；完整操作集中在入站保护页。</p>
          </div>
          <a className="button shrink-0 min-h-8 px-2 text-xs" href={buildQueryHref({ mail: "emergency" }, ["codex", "codexInbox", "codexRuntime", "gateway", "images", "docker", "settings"])}>
            打开入站保护
          </a>
        </div>
        {body}
      </div>
    );
  }

  return (
    <Panel title={title} subtitle="当前是 Mox Domain.Disabled fallback，不是最终 early SMTP reject 能力；不会删除队列或邮箱内容。" actions={actionsSlot}>
      {dangerConfirmDialog}
      {body}
    </Panel>
  );
}

function EmergencyStateGrid({ emergency, compact }: { emergency?: MailEmergencyInboundRejectState; compact?: boolean }) {
  const rows: Array<[string, string]> = [
    ["状态", emergency?.enabled ? "降级保护中" : "未开启"],
    ["影响范围", `${emergency?.affected_domains ?? 0} 个域 · ${emergency?.affected_accounts ?? 0} 个账户`],
    ["自动恢复", emergency?.auto_restore_at || "手动"],
    ["最近失败", emergency?.last_failure || "-"],
    ["恢复冲突", emergency?.restore_conflict || "-"],
  ];
  if (!compact) {
    rows.push(["开启原因", emergency?.reason || "-"], ["开启时间", emergency?.applied_at || "-"]);
  }
  const diagnostics: Array<[string, string]> = [
    ["预期模式", emergency?.mode || "domain_disabled_fallback"],
    ["实际策略", emergency?.actual_mox_strategy || "mox_domain_disabled"],
    ["自动恢复阻断", emergency?.auto_restore_blocked_at || "-"],
    ["上次正常配置", emergency?.last_normal_config_hash || "-"],
    ["最近配置 hash", emergency?.last_config_hash || "-"],
    ["reload", emergency?.last_reload_result || "-"],
    ["probe", emergency?.last_probe_result || "-"],
    ["rollback", emergency?.last_rollback_result || "-"],
    ["状态未知", emergency?.apply_unknown ? "是" : "否"],
    ["期望 hash", emergency?.restore_expected_hash || "-"],
    ["磁盘 hash", emergency?.restore_disk_hash || "-"],
  ];

  return (
    <div className="grid gap-3">
      <KeyValueGrid rows={rows} emergency={emergency} />
      {!compact ? (
        <details className="rounded-md border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <summary className="cursor-pointer text-[var(--muted-strong)]">诊断详情</summary>
          <div className="mt-3">
            <KeyValueGrid rows={diagnostics} emergency={emergency} />
          </div>
        </details>
      ) : null}
    </div>
  );
}

function KeyValueGrid({ rows, emergency }: { rows: Array<[string, string]>; emergency?: MailEmergencyInboundRejectState }) {
  return (
    <div className="grid grid-cols-[150px_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <span className="text-[var(--muted-strong)]">{label}</span>
          <span className={value.length > 48 ? "mono truncate text-xs" : ""}>
            {label === "状态" ? <Pill tone={emergency?.enabled ? "danger" : "neutral"}>{value}</Pill> : value}
          </span>
        </div>
      ))}
    </div>
  );
}
