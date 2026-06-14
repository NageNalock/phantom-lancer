import React, { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type { AppData } from "../../app/types";
import { EmptyState, Pill, SubTabs } from "../../components/ui";
import { useQueryParamState } from "../../hooks/useQueryParamState";
import { friendlyError, mailRuntimeStatus, type MailRuntimeStatus } from "../../api/client";
import { MailOverviewTab } from "./MailOverviewTab";
import { MailSetupTab } from "./MailSetupTab";
import { MailDomainsTab } from "./MailDomainsTab";
import { MailCertificatesTab } from "./MailCertificatesTab";
import { MailAccountsTab } from "./MailAccountsTab";
import { MailAliasesTab } from "./MailAliasesTab";
import { MailDeliveryTab } from "./MailDeliveryTab";
import { MailboxTab } from "./MailboxTab";
import { MailLogsTab } from "./MailLogsTab";
import { MailEventsTab } from "./MailEventsTab";
import { MailSettingsTab } from "./MailSettingsTab";
import { MailEmergencyPanel } from "./MailEmergencyPanel";

// Mail sub-tabs mirror the product capability map. Ordering is deliberate:
// walk a new operator through installation → provisioning → ongoing
// operation → settings in a single left-to-right pass.
const MAIL_TAB_IDS = [
  "overview",
  "setup",
  "domains",
  "accounts",
  "aliases",
  "certificates",
  "mailbox",
  "delivery",
  // Legacy deep-link value. It is accepted so ?mail=queue still opens the
  // queue view, but it is not rendered as a separate top-level tab.
  "queue",
  "emergency",
  "logs",
  "events",
  "settings",
] as const;
type MailTab = (typeof MAIL_TAB_IDS)[number];
const MAIL_VISIBLE_TAB_IDS = MAIL_TAB_IDS.filter((id) => id !== "queue");

const MAIL_TAB_CLEAR_KEYS = [
  "codex",
  "codexInbox",
  "codexRuntime",
  "gateway",
  "images",
  "docker",
  "settings",
];

const MAIL_TAB_LABELS: Record<MailTab, { label: string; hint: string }> = {
  overview: { label: "总览", hint: "Mox 运行状态、9 层探针摘要和仪表盘" },
  setup: { label: "安装与初始化", hint: "Mox 二进制、端口预检、实例初始化、外部只读接入" },
  domains: { label: "域名", hint: "收发域名、DNS 记录清单、DKIM/DMARC 状态" },
  accounts: { label: "邮箱账户", hint: "本地邮箱、密码重置、IMAP 同步进度" },
  aliases: { label: "别名与转发", hint: "alias、catch-all、邮件列表" },
  certificates: { label: "证书", hint: "ACME DNS-01 签发、续签倒计时、TLSA 记录" },
  mailbox: { label: "邮箱", hint: "三栏邮件浏览、全文搜索、撰写与草稿" },
  delivery: { label: "投递与队列", hint: "投递事件、队列、抑制列表、Webhook、出站速率和 DNSBL 声誉" },
  queue: { label: "队列", hint: "hold / schedule / retry / fail / drop 队列管理与抑制列表" },
  emergency: { label: "入站保护", hint: "域禁用降级保护、自动恢复、失败回滚和漂移冲突状态" },
  logs: { label: "日志", hint: "Mox stdout 红acted 视图、bounded tail 和 live stream" },
  events: { label: "事件", hint: "Mail 模块事件和审计过滤视图" },
  settings: { label: "设置", hint: "运行期配置、保留策略、备份、危险区" },
};

// Per-tab Phase-1 stubs.  Upgraded tabs have been imported above.
function CertificatesTab_() {
  return <EmptyState title="证书管理 (Phase 4)" body="此处将展示 ACME DNS-01 签发流水线、续签倒计时、TLSA 记录与手动模式等待弹窗。" />;
}

export function MailView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [activeTab, setActiveTab, tabHref] = useQueryParamState<MailTab>("mail", MAIL_TAB_IDS as unknown as MailTab[], "overview", {
    clearKeys: MAIL_TAB_CLEAR_KEYS,
  });

  const [runtimeStatus, setRuntimeStatus] = useState<MailRuntimeStatus | null>(null);
  const status = data.mail.status;
  const activeHint = MAIL_TAB_LABELS[activeTab].hint;
  const visibleActiveTab = activeTab === "queue" ? "delivery" : activeTab;

  async function loadRuntime() {
    try {
      const s = await mailRuntimeStatus();
      setRuntimeStatus(s);
    } catch (e) {
      // Best-effort. Swallow so setup tabs remain usable; toast so the operator
      // knows runtime status isn't available.
      actions.setToast(friendlyError(e), "warn");
    }
  }

  async function reloadMail() {
    await Promise.all([loadRuntime(), actions.reloadData()]);
  }

  useEffect(() => {
    void loadRuntime();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const tabs = MAIL_VISIBLE_TAB_IDS.map((id) => {
    let badge: string | undefined;
    if (id === "domains") badge = status?.domain_count ? `${status.domain_count}` : undefined;
    if (id === "accounts") badge = status?.account_count ? `${status.account_count}` : undefined;
    return {
      id,
      label: MAIL_TAB_LABELS[id].label,
      href: tabHref(id as MailTab),
      badge,
    };
  });

  // Pill driven by runtimeStatus (preferred) with fallback to legacy signal.
  function pillTone(): "good" | "warn" | "danger" | "neutral" {
    if (runtimeStatus) {
      if (runtimeStatus.emergency_inbound_reject?.enabled) return "danger";
      if (runtimeStatus.crash_loop_state) return "danger";
      if (runtimeStatus.observed_state === "running" && runtimeStatus.desired_state === "running") return "good";
      if (runtimeStatus.observed_state === "stopped" || runtimeStatus.observed_state === "unknown") return "neutral";
      if (runtimeStatus.overall === "critical" || runtimeStatus.overall === "error") return "danger";
      if (runtimeStatus.overall === "warn") return "warn";
    }
    return status?.service_ready ? "good" : status?.ok ? "warn" : "warn";
  }
  function pillLabel(): string {
    if (runtimeStatus) {
      if (runtimeStatus.emergency_inbound_reject?.enabled) return "降级保护中";
      if (runtimeStatus.import_mode) return "只读接入";
      if (runtimeStatus.crash_loop_state) return `崩溃循环 (${runtimeStatus.consecutive_failures})`;
      if (runtimeStatus.observed_state === "running" && runtimeStatus.desired_state === "running") return "运行中";
      if (runtimeStatus.observed_state) return runtimeStatus.observed_state;
    }
    return status?.service_ready ? "服务就绪" : status?.ok ? "待启动" : "未初始化";
  }

  const rightSlot = (
    <div className="flex shrink-0 items-center gap-2 text-xs">
      <Pill tone={pillTone()}>{pillLabel()}</Pill>
      {runtimeStatus?.emergency_inbound_reject?.enabled ? <Pill tone="danger">降级保护</Pill> : null}
      {runtimeStatus?.import_mode ? <Pill tone="warn">只读</Pill> : null}
    </div>
  );

  const TAB_RENDER: Record<MailTab, (p: { data: AppData }) => React.ReactElement> = {
    overview: () => <MailOverviewTab actions={actions} status={runtimeStatus} reload={reloadMail} />,
    setup: () => <MailSetupTab actions={actions} reload={reloadMail} />,
    domains: () => <MailDomainsTab actions={actions} status={null} reload={reloadMail} />,
    accounts: ({ data }) => <MailAccountsTab actions={actions} reload={reloadMail} data={data} />,
    aliases: ({ data }) => <MailAliasesTab actions={actions} reload={reloadMail} data={data} />,
    certificates: () => <MailCertificatesTab actions={actions} reload={reloadMail} />,
    mailbox: ({ data }) => <MailboxTab actions={actions} reload={reloadMail} data={data} />,
    delivery: ({ data }) => <MailDeliveryTab actions={actions} reload={reloadMail} data={data} status={runtimeStatus} defaultSub="deliveries" />,
    queue: ({ data }) => <MailDeliveryTab actions={actions} reload={reloadMail} data={data} status={runtimeStatus} defaultSub="queue" />,
    emergency: () => <div className="pt-4"><MailEmergencyPanel actions={actions} reload={reloadMail} status={runtimeStatus} /></div>,
    logs: ({ data }) => <MailLogsTab actions={actions} reload={reloadMail} data={data} />,
    events: () => <MailEventsTab actions={actions} />,
    settings: ({ data }) => <MailSettingsTab actions={actions} reload={reloadMail} data={data} />,
  };
  const TabPanel = TAB_RENDER[activeTab];

  return (
    <section className="grid gap-3">
      <header className="grid gap-1 pt-1">
        <h2 className="m-0 text-lg font-semibold">Mail / Mox 控制面</h2>
        <p className="muted m-0 text-xs">{activeHint}</p>
      </header>
      <SubTabs activeId={visibleActiveTab} onChange={(id) => setActiveTab(id as MailTab)} rightSlot={rightSlot} tabs={tabs} ariaLabel="Mail 二级导航" />
      <TabPanel data={data} />
    </section>
  );
}
