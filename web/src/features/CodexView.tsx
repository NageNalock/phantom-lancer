import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexStatus } from "../app/types";
import { Pill, SubTabs } from "../components/ui";
import { codexAppServerStateLabel } from "../domain/labels";
import { ThreadsTab } from "./codex/ThreadsTab";
import { WorkspacesTab } from "./codex/WorkspacesTab";
import { ApprovalsTab } from "./codex/ApprovalsTab";
import { DiagnosticsTab } from "./codex/DiagnosticsTab";
import { CodexSettingsTab } from "./codex/CodexSettingsTab";
import { AutomationsTab } from "./codex/AutomationsTab";
import { CapabilitiesTab } from "./codex/CapabilitiesTab";
import { NotificationsTab } from "./codex/NotificationsTab";
import { ChatsTab } from "./codex/ChatsTab";

type CodexTab = "threads" | "chats" | "workspaces" | "approvals" | "automations" | "capabilities" | "notifications" | "diagnostics" | "settings";

const TABS: Array<{ id: CodexTab; label: string }> = [
  { id: "threads", label: "Threads" },
  { id: "chats", label: "Chats" },
  { id: "workspaces", label: "Workspaces" },
  { id: "approvals", label: "Approvals" },
  { id: "automations", label: "Automations" },
  { id: "capabilities", label: "Capabilities" },
  { id: "notifications", label: "Notifications" },
  { id: "diagnostics", label: "Diagnostics" },
  { id: "settings", label: "Settings" },
];

export function CodexView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab] = useState<CodexTab>("threads");
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
          badge: t.id === "approvals" && pending > 0 ? (
            <span className="rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{pending}</span>
          ) : undefined,
        }))}
      />

      {tab === "threads" ? <ThreadsTab actions={actions} focusThreadId={focusThreadId} status={status} onStatusChange={refreshStatus} /> : null}
      {tab === "chats" ? <ChatsTab actions={actions} status={status} onStatusChange={refreshStatus} /> : null}
      {tab === "workspaces" ? <WorkspacesTab actions={actions} onChange={refreshStatus} /> : null}
      {tab === "approvals" ? <ApprovalsTab actions={actions} onChange={refreshStatus} /> : null}
      {tab === "automations" ? <AutomationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
      {tab === "capabilities" ? <CapabilitiesTab actions={actions} /> : null}
      {tab === "notifications" ? <NotificationsTab actions={actions} onOpenThread={(threadId) => { setFocusThreadId(threadId); setTab("threads"); }} /> : null}
      {tab === "diagnostics" ? <DiagnosticsTab actions={actions} status={status} onChange={refreshStatus} /> : null}
      {tab === "settings" ? <CodexSettingsTab actions={actions} onChange={refreshStatus} /> : null}
    </section>
  );
}
