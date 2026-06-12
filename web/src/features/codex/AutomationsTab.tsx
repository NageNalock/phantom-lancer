import { useCallback, useEffect, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexAutomation, CodexThread, CodexTriageInbox, CodexWorkspace } from "../../app/types";
import { Button, Panel, useDangerConfirm } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { AutomationList } from "./Automations/AutomationList";
import { TriageInboxPanel } from "./Automations/TriageInboxPanel";
import { AutomationEditor } from "./Automations/AutomationEditor";
import type { AutomationDraft } from "./Automations/AutomationEditor";

export function AutomationsTab({ actions, onOpenThread }: { actions: AppActions; onOpenThread: (threadId: string) => void }) {
  const [items, setItems] = useState<CodexAutomation[]>([]);
  const [triage, setTriage] = useState<CodexTriageInbox>({});
  const [threads, setThreads] = useState<CodexThread[]>([]);
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
  const [editingId, setEditingId] = useState("");
  const [runningIds, setRunningIds] = useState<Set<string>>(() => new Set());
  const runRequestIds = useRef<Record<string, string>>({});
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const load = useCallback(async () => {
    const [automationResp, triageResp, threadResp, workspaceResp] = await Promise.all([
      actions.api<{ items?: CodexAutomation[] }>("/api/codex/automations"),
      actions.api<CodexTriageInbox>("/api/codex/triage"),
      actions.api<{ items?: CodexThread[] }>("/api/codex/threads"),
      actions.api<{ items?: CodexWorkspace[] }>("/api/codex/workspaces"),
    ]);
    setItems(automationResp.items || []);
    setTriage(triageResp || {});
    setThreads(threadResp.items || []);
    setWorkspaces(workspaceResp.items || []);
  }, [actions]);

  useEffect(() => {
    void load().catch((error) => actions.setToast(friendlyError(error), "danger"));
  }, [actions, load]);

  const editing = items.find((item) => item.id === editingId);

  async function submit(draft: AutomationDraft) {
    const body = { ...draft, enabled: editing?.enabled ?? true };
    try {
      if (editingId) {
        await actions.api(`/api/codex/automations/${editingId}`, { method: "PATCH", csrf: actions.csrf, body });
      } else {
        await actions.api("/api/codex/automations", { method: "POST", csrf: actions.csrf, body });
      }
      setEditingId("");
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function toggleEnabled(item: CodexAutomation) {
    try {
      await actions.api(`/api/codex/automations/${item.id}`, { method: "PATCH", csrf: actions.csrf, body: { enabled: !item.enabled } });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function remove(item: CodexAutomation) {
    const confirmed = await confirmDanger({
      title: "删除 Codex 自动化",
      objectName: item.title || item.id,
      body: "该自动化将不再按计划运行，已产生的 run 和关联会话记录会保留。",
      confirmLabel: "删除自动化",
      impact: ["后续定时触发停止。", "历史 run、triage 和审计记录不被清理。"],
      recovery: "删除后如需恢复，需要重新创建自动化配置。",
    });
    if (!confirmed) return;
    try {
      await actions.api(`/api/codex/automations/${item.id}`, { method: "DELETE", csrf: actions.csrf });
      if (editingId === item.id) setEditingId("");
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function runNow(id: string) {
    if (runningIds.has(id)) return;
    const requestId = newAutomationRunRequestId(id);
    runRequestIds.current[id] = requestId;
    setRunningIds((current) => new Set(current).add(id));
    try {
      await actions.api(`/api/codex/automations/${id}/run-now`, { method: "POST", csrf: actions.csrf, body: { clientRequestId: requestId } });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      delete runRequestIds.current[id];
      setRunningIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    }
  }

  async function archiveRun(id: string) {
    try {
      await actions.api(`/api/codex/automation-runs/${id}/archive`, { method: "POST", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function archiveThread(threadId: string) {
    if (!threadId) return;
    try {
      await actions.api(`/api/codex/threads/${threadId}/archive`, { method: "POST", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function resolveComment(id: string) {
    try {
      await actions.api(`/api/codex/review/comments/${id}`, { method: "DELETE", csrf: actions.csrf });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function promoteThread(threadId: string) {
    if (!threadId) return;
    try {
      await actions.api(`/api/codex/threads/${threadId}`, { method: "PATCH", csrf: actions.csrf, body: { background: false } });
      await load();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <>
      <div className="grid gap-4">
        <div className="grid grid-cols-[minmax(0,1fr)_320px] gap-4 max-lg:grid-cols-1">
          <Panel actions={<Button onClick={() => void load()}>刷新</Button>} subtitle="P2 自动化默认 read-only，不自动提交或 push。" title="自动化规则">
            <AutomationList
              items={items}
              runningIds={runningIds}
              onRun={(id) => void runNow(id)}
              onEdit={(item) => setEditingId(item.id)}
              onToggle={(item) => void toggleEnabled(item)}
              onRemove={(item) => void remove(item)}
            />
          </Panel>

          <AutomationEditor
            editing={editing}
            threads={threads}
            workspaces={workspaces}
            onSubmit={submit}
            onReset={() => setEditingId("")}
          />
        </div>
        <Panel subtitle="自动化运行结果、后台会话、失败 turn 和未解决 review comment 集中在这里处理。" title="自动化收件箱">
          <TriageInboxPanel
            triage={triage}
            onArchiveRun={(id) => void archiveRun(id)}
            onArchiveThread={(threadId) => void archiveThread(threadId)}
            onPromoteThread={(threadId) => void promoteThread(threadId)}
            onResolveComment={(id) => void resolveComment(id)}
            onOpenThread={onOpenThread}
          />
        </Panel>
      </div>
      {dangerConfirmDialog}
    </>
  );
}

function newAutomationRunRequestId(id: string): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `ui-${id}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
