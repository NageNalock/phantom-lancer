import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexStatus } from "../app/types";
import { Pill, SubTabs } from "../components/ui";
import { codexAppServerStateLabel } from "../domain/labels";
import { useQueryParamState } from "../hooks/useQueryParamState";
import { ThreadsTab } from "./codex/ThreadsTab";
import { WorkspacesTab } from "./codex/WorkspacesTab";
import { ApprovalsTab } from "./codex/ApprovalsTab";
import { DiagnosticsTab } from "./codex/DiagnosticsTab";
import { CodexSettingsTab } from "./codex/CodexSettingsTab";
import { AutomationsTab } from "./codex/AutomationsTab";
import { CapabilitiesTab } from "./codex/CapabilitiesTab";
import { NotificationsTab } from "./codex/NotificationsTab";

type CodexTab = "threads" | "workspaces" | "inbox" | "runtime";
type CodexInboxTab = "approvals" | "notifications" | "automations";
type CodexRuntimeTab = "diagnostics" | "capabilities" | "settings";

const TABS: Array<{ id: CodexTab; label: string }> = [
  { id: "threads", label: "会话" },
  { id: "workspaces", label: "工作区" },
  { id: "inbox", label: "收件箱" },
  { id: "runtime", label: "运行时" },
];

const INBOX_TABS: Array<{ id: CodexInboxTab; label: string }> = [
  { id: "approvals", label: "审批" },
  { id: "notifications", label: "通知" },
  { id: "automations", label: "自动化" },
];

const RUNTIME_TABS: Array<{ id: CodexRuntimeTab; label: string }> = [
  { id: "diagnostics", label: "运行诊断" },
  { id: "capabilities", label: "能力" },
  { id: "settings", label: "设置" },
];
const CODEX_TAB_IDS: CodexTab[] = TABS.map((item) => item.id);
const CODEX_INBOX_TAB_IDS: CodexInboxTab[] = INBOX_TABS.map((item) => item.id);
const CODEX_RUNTIME_TAB_IDS: CodexRuntimeTab[] = RUNTIME_TABS.map((item) => item.id);
const CODEX_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexInbox", "codexRuntime"];
const CODEX_INBOX_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexRuntime"];
const CODEX_RUNTIME_CLEAR_KEYS = ["gateway", "images", "docker", "settings", "codexInbox"];

export function CodexView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab, tabHref] = useQueryParamState<CodexTab>("codex", CODEX_TAB_IDS, "threads", { clearKeys: CODEX_CLEAR_KEYS });
  const [inboxTab, setInboxTab, inboxTabHref] = useQueryParamState<CodexInboxTab>("codexInbox", CODEX_INBOX_TAB_IDS, "approvals", { clearKeys: CODEX_INBOX_CLEAR_KEYS });
  const [runtimeTab, setRuntimeTab, runtimeTabHref] = useQueryParamState<CodexRuntimeTab>("codexRuntime", CODEX_RUNTIME_TAB_IDS, "diagnostics", { clearKeys: CODEX_RUNTIME_CLEAR_KEYS });
  const [status, setStatus] = useState<CodexStatus | undefined>(data.dashboard.codex);
  const [focusThreadId, setFocusThreadId] = useState("");

  const refreshStatus = useCallback(async () => {
    try {
      const next = await actions.api<CodexStatus>("/api/codex/status");
      setStatus(next);
    } catch {
      // status errors are surfaced inside each tab; keep the shell quiet.
    }
  }, [actions]);

  useEffect(() => {
    void refreshStatus();
  }, [refreshStatus]);

  const appServer = status?.appServer;
  const appServerTone = appServer?.state === "running" ? "good" : appServer?.state === "failed" ? "danger" : appServer?.state === "starting" ? "warn" : "neutral";
  const pending = status?.pendingApprovals || 0;
  const runtime = status?.runtime;

  return (
    <section className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-4 p-4">
      <SubTabs
        activeId={tab}
        onChange={(id) => setTab(id as CodexTab)}
        rightSlot={
          <span className="flex flex-wrap items-center justify-end gap-2 text-xs text-[var(--muted)]">
            {runtime?.running ? <Pill tone="good">running {runtime.running}</Pill> : null}
            {runtime?.waitingApproval ? <Pill tone="warn">approval {runtime.waitingApproval}</Pill> : null}
            {runtime?.queued ? <Pill tone="warn">queued {runtime.queued}</Pill> : null}
            {runtime?.failed ? <Pill tone="danger">failed {runtime.failed}</Pill> : null}
            <Pill tone={appServerTone}>app-server {codexAppServerStateLabel(appServer?.state)}</Pill>
          </span>
        }
	        tabs={TABS.map((t) => ({
	          ...t,
	          href: tabHref(t.id),
	          badge: t.id === "inbox" && pending > 0 ? (
            <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{pending}</span>
          ) : undefined,
        }))}
      />

      {tab === "threads" ? <ThreadsTab actions={actions} focusThreadId={focusThreadId} status={status} onStatusChange={refreshStatus} /> : null}
      {tab === "workspaces" ? <WorkspacesTab actions={actions} onChange={refreshStatus} /> : null}
      {tab === "inbox" ? (
        <div className="grid gap-4">
          <SubTabs
            activeId={inboxTab}
            className="border-b-0 pb-0"
            onChange={(id) => setInboxTab(id as CodexInboxTab)}
	            tabs={INBOX_TABS.map((item) => ({
	              ...item,
	              href: inboxTabHref(item.id),
	              badge: item.id === "approvals" && pending > 0 ? (
                <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{pending}</span>
              ) : undefined,
            }))}
          />
          {inboxTab === "approvals" ? <ApprovalsTab actions={actions} onChange={refreshStatus} /> : null}
	          {inboxTab === "notifications" ? <NotificationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
	          {inboxTab === "automations" ? <AutomationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
        </div>
      ) : null}
      {tab === "runtime" ? (
        <div className="grid gap-4">
          <SubTabs
            activeId={runtimeTab}
            className="border-b-0 pb-0"
            onChange={(id) => setRuntimeTab(id as CodexRuntimeTab)}
	            tabs={RUNTIME_TABS.map((item) => ({ ...item, href: runtimeTabHref(item.id) }))}
          />
          {runtimeTab === "diagnostics" ? <DiagnosticsTab actions={actions} status={status} onChange={refreshStatus} /> : null}
          {runtimeTab === "capabilities" ? <CapabilitiesTab actions={actions} /> : null}
          {runtimeTab === "settings" ? <CodexSettingsTab actions={actions} onChange={refreshStatus} /> : null}
        </div>
      ) : null}
    </section>
  );
}
