import { useCallback, useEffect, useState } from "react";
import type { AppActions } from "../app/App";
import type { AppData, CodexStatus } from "../app/types";
import { Pill } from "../components/ui";
import { codexAppServerStateLabel } from "../domain/labels";
import { ThreadsTab } from "./codex/ThreadsTab";
import { WorkspacesTab } from "./codex/WorkspacesTab";
import { ApprovalsTab } from "./codex/ApprovalsTab";
import { DiagnosticsTab } from "./codex/DiagnosticsTab";
import { CodexSettingsTab } from "./codex/CodexSettingsTab";

type CodexTab = "threads" | "workspaces" | "approvals" | "diagnostics" | "settings";

const TABS: Array<{ id: CodexTab; label: string }> = [
  { id: "threads", label: "Threads" },
  { id: "workspaces", label: "Workspaces" },
  { id: "approvals", label: "Approvals" },
  { id: "diagnostics", label: "Diagnostics" },
  { id: "settings", label: "Settings" },
];

export function CodexView({ actions, data }: { actions: AppActions; data: AppData }) {
  const [tab, setTab] = useState<CodexTab>("threads");
  const [status, setStatus] = useState<CodexStatus | undefined>(data.dashboard.codex);

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

  return (
    <section className="grid gap-4 p-4">
      <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] pb-2">
        {TABS.map((item) => {
          const active = item.id === tab;
          return (
            <button
              aria-pressed={active}
              className={`rounded-md px-3 py-1.5 text-sm transition ${active ? "bg-[var(--surface-strong)] text-[var(--text)] shadow-[inset_0_-2px_0_var(--accent)]" : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"}`}
              key={item.id}
              onClick={() => setTab(item.id)}
              type="button"
            >
              {item.label}
              {item.id === "approvals" && pending > 0 ? <span className="ml-1.5 rounded-full bg-[var(--warn-soft)] px-1.5 text-xs text-[var(--warn)]">{pending}</span> : null}
            </button>
          );
        })}
        <span className="ml-auto flex items-center gap-2 text-xs text-[var(--muted)]">
          <Pill tone={appServerTone}>app-server {codexAppServerStateLabel(appServer?.state)}</Pill>
        </span>
      </div>

      {tab === "threads" ? <ThreadsTab actions={actions} status={status} onStatusChange={refreshStatus} /> : null}
      {tab === "workspaces" ? <WorkspacesTab actions={actions} onChange={refreshStatus} /> : null}
      {tab === "approvals" ? <ApprovalsTab actions={actions} onChange={refreshStatus} /> : null}
      {tab === "diagnostics" ? <DiagnosticsTab actions={actions} status={status} onChange={refreshStatus} /> : null}
      {tab === "settings" ? <CodexSettingsTab actions={actions} onChange={refreshStatus} /> : null}
    </section>
  );
}
