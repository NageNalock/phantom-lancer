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
import { MailSettingsTab } from "./MailSettingsTab";

// The 11 top-level sub-tabs match §6 of the design doc.  Ordering is
// deliberate: walk a new operator through installation → provisioning →
// ongoing operation → settings in a single left-to-right pass.
const MAIL_TAB_IDS = [
  "overview",
  "setup",
  "domains",
  "accounts",
  "aliases",
  "certificates",
  "mailbox",
  "delivery",
  "queue",
  "logs",
  "settings",
] as const;
type MailTab = (typeof MAIL_TAB_IDS)[number];

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
  delivery: { label: "投递", hint: "投递事件、退信统计、出站速率、DNSBL 声誉" },
  queue: { label: "队列", hint: "hold / schedule / retry / fail / drop 队列管理与抑制列表" },
  logs: { label: "日志与事件", hint: "Mox stdout 红acted 视图、审计与事件流" },
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

  const tabs = MAIL_TAB_IDS.map((id) => {
    let badge: string | undefined;
    if (id === "domains") badge = status?.domain_count ? `${status.domain_count}` : undefined;
    if (id === "accounts") badge = status?.account_count ? `${status.account_count}` : undefined;
    return {
      id,
      label: MAIL_TAB_LABELS[id].label,
      href: tabHref(id),
      badge,
    };
  });

  // Pill driven by runtimeStatus (preferred) with fallback to legacy signal.
  function pillTone(): "good" | "warn" | "danger" | "neutral" {
    if (runtimeStatus) {
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
    delivery: ({ data }) => <MailDeliveryTab actions={actions} reload={reloadMail} data={data} defaultSub="deliveries" />,
    queue: ({ data }) => <MailDeliveryTab actions={actions} reload={reloadMail} data={data} defaultSub="queue" />,
    logs: ({ data }) => <MailLogsTab actions={actions} reload={reloadMail} data={data} />,
    settings: ({ data }) => <MailSettingsTab actions={actions} reload={reloadMail} data={data} />,
  };
  const TabPanel = TAB_RENDER[activeTab];

  return (
    <section className="grid gap-3">
      <header className="grid gap-1 pt-1">
        <h2 className="m-0 text-lg font-semibold">Mail / Mox 控制面</h2>
        <p className="muted m-0 text-xs">{activeHint}</p>
      </header>
      <SubTabs activeId={activeTab} onChange={(id) => setActiveTab(id as MailTab)} rightSlot={rightSlot} tabs={tabs} ariaLabel="Mail 二级导航" />
      <TabPanel data={data} />
    </section>
  );
}
