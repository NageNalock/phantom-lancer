import { useCallback, useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexApproval, CodexModel, CodexSettings, CodexStatus, CodexThread, CodexWorkspace } from "../../app/types";
import { friendlyError } from "../../api/client";
import { useBoolQueryParamState, useQueryParamState, useStringQueryParamState } from "../../hooks/useQueryParamState";
import { useDangerConfirm } from "../../components/ui";
import { ComposerEmptyState, ThreadList } from "./ThreadSidebar";
import type { CreateConversationMode, CreateThreadOptions } from "./ThreadSidebar";
import { ThreadInspector } from "./ThreadInspector";
import { ChatWorkspace } from "./ChatWorkspace";
import { ThreadWorkspace } from "./ThreadWorkspace";

type ThreadStatusFilter = "all" | "idle" | "running" | "needs_approval" | "queued" | "failed" | "archived";
const THREAD_STATUS_FILTERS: ThreadStatusFilter[] = ["all", "idle", "running", "needs_approval", "queued", "failed", "archived"];
const THREAD_CLEAR_KEYS = ["codexInbox", "codexRuntime"];

export function ThreadsTab({ actions, focusThreadId, status, onStatusChange }: { actions: AppActions; focusThreadId?: string; status?: CodexStatus; onStatusChange: () => void }) {
  const [threads, setThreads] = useState<CodexThread[]>([]);
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
  const [approvals, setApprovals] = useState<CodexApproval[]>([]);
  const [models, setModels] = useState<CodexModel[]>([]);
  const [scratchWorkspaceId, setScratchWorkspaceId] = useState("");
  const [activeId, setActiveId] = useStringQueryParamState("codexThread", "", { clearKeys: THREAD_CLEAR_KEYS });
  const [query, setQuery] = useStringQueryParamState("codexQ", "", { clearKeys: THREAD_CLEAR_KEYS });
  const [workspaceFilter, setWorkspaceFilter] = useStringQueryParamState("codexWorkspace", "all", { clearKeys: THREAD_CLEAR_KEYS });
  const [statusFilter, setStatusFilter] = useQueryParamState<ThreadStatusFilter>("codexThreadStatus", THREAD_STATUS_FILTERS, "all", { clearKeys: THREAD_CLEAR_KEYS });
  const [includeArchived, setIncludeArchived] = useBoolQueryParamState("codexArchived", false, { clearKeys: THREAD_CLEAR_KEYS });
  const [loading, setLoading] = useState(false);
  const [composerDrafts, setComposerDrafts] = useState<Record<string, string>>({});
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const loadThreads = useCallback(async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams();
      if (query.trim()) params.set("q", query.trim());
      if (workspaceFilter !== "all") params.set("workspace_id", workspaceFilter);
      if (statusFilter !== "all") params.set("status", statusFilter);
      if (includeArchived || statusFilter === "archived") params.set("archived", "1");
      const suffix = params.toString() ? `?${params.toString()}` : "";
      const response = await actions.api<{ items?: CodexThread[] }>(`/api/codex/threads${suffix}`);
      const nextThreads = response.items || [];
      setThreads(nextThreads);
      if (!activeId || !nextThreads.some((thread) => thread.id === activeId)) setActiveId(nextThreads[0]?.id || "");
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions, activeId, includeArchived, query, setActiveId, statusFilter, workspaceFilter]);

  const loadWorkspaces = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexWorkspace[] }>("/api/codex/workspaces");
      setWorkspaces(response.items || []);
    } catch {
      // surfaced elsewhere
    }
  }, [actions]);

  const loadSettings = useCallback(async () => {
    try {
      const response = await actions.api<{ settings?: CodexSettings }>("/api/codex/settings");
      setScratchWorkspaceId(response.settings?.scratchWorkspaceId || "");
    } catch {
      // settings are only needed for the read-only conversation create preset.
    }
  }, [actions]);

  const loadModels = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexModel[] }>("/api/codex/models");
      setModels(response.items || []);
    } catch {
      setModels([]);
    }
  }, [actions]);

  const loadApprovals = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexApproval[] }>("/api/codex/approvals?status=pending");
      setApprovals(response.items || []);
    } catch {
      // The inspector can still render without the cross-thread approval list.
    }
  }, [actions]);

  useEffect(() => {
    void loadThreads();
  }, [loadThreads]);
  useEffect(() => {
    void loadWorkspaces();
  }, [loadWorkspaces]);
  useEffect(() => {
    void loadSettings();
  }, [loadSettings]);
  useEffect(() => {
    void loadModels();
  }, [loadModels]);
  useEffect(() => {
    void loadApprovals();
  }, [loadApprovals]);
  useEffect(() => {
    if (!focusThreadId) return;
    setWorkspaceFilter("all");
    setStatusFilter("all");
    setIncludeArchived(true);
    setActiveId(focusThreadId);
  }, [focusThreadId]);

  const activeThread = useMemo(() => threads.find((thread) => thread.id === activeId) || null, [threads, activeId]);
  const scratchReady = Boolean(scratchWorkspaceId);

  const updateThreadInList = useCallback((nextThread: CodexThread) => {
    setThreads((current) =>
      current.map((thread) => (thread.id === nextThread.id ? { ...thread, ...nextThread } : thread)),
    );
  }, []);

  async function createThread(workspaceId: string, mode: CreateConversationMode = "code", options: CreateThreadOptions = {}) {
    try {
      const initialPrompt = options.initialPrompt?.trim() || "";
      const response = mode === "chat"
        ? await actions.api<{ thread: CodexThread }>("/api/codex/chats", { method: "POST", csrf: actions.csrf, body: { title: "" } })
        : await actions.api<{ thread: CodexThread }>("/api/codex/threads", { method: "POST", csrf: actions.csrf, body: { workspaceId, model: options.model || "", sandbox: options.sandbox || "", approvalPolicy: options.approvalPolicy || "on-request", executionMode: options.executionMode || "workspace" } });
      await loadThreads();
      setActiveId(response.thread.id);
      if (initialPrompt) {
        await actions.api(`/api/codex/threads/${response.thread.id}/turns`, {
          method: "POST",
          csrf: actions.csrf,
          body: { prompt: initialPrompt, sandbox: mode === "chat" ? "read-only" : (options.sandbox || response.thread.sandboxMode), approvalPolicy: options.approvalPolicy || response.thread.approvalPolicy || "on-request", model: options.model || response.thread.model || "" },
        });
        await loadThreads();
      }
      onStatusChange();
      await loadApprovals();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function togglePin(thread: CodexThread) {
    try {
      await actions.api(`/api/codex/threads/${thread.id}`, { method: "PATCH", csrf: actions.csrf, body: { pinned: !thread.pinned } });
      await loadThreads();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function archive(thread: CodexThread) {
    const confirmed = await confirmDanger({
      title: "归档会话",
      body: "这会把该 Codex 会话移出默认列表。",
      objectName: thread.title || thread.id,
      impact: ["当前会话会从默认列表隐藏。", "运行中的任务不会因此自动停止。"],
      recovery: "可以打开“显示已归档会话”后恢复该会话。",
      confirmLabel: "归档",
    });
    if (!confirmed) return;
    try {
      await actions.api(`/api/codex/threads/${thread.id}/archive`, { method: "POST", csrf: actions.csrf });
      await loadThreads();
      if (activeId === thread.id) setActiveId("");
      onStatusChange();
      await loadApprovals();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function fork(thread: CodexThread) {
    try {
      const response = await actions.api<{ thread: CodexThread }>(`/api/codex/threads/${thread.id}/fork`, { method: "POST", csrf: actions.csrf });
      await loadThreads();
      setActiveId(response.thread.id);
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function resume(thread: CodexThread) {
    try {
      const response = await actions.api<{ thread: CodexThread }>(`/api/codex/threads/${thread.id}/resume`, { method: "POST", csrf: actions.csrf });
      await loadThreads();
      setActiveId(response.thread.id);
      onStatusChange();
      await loadApprovals();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <>
    {dangerConfirmDialog}
    <div className="grid min-h-[calc(100dvh-8.5rem)] min-w-0 grid-cols-[280px_minmax(0,1fr)_360px] gap-3 max-xl:grid-cols-[260px_minmax(0,1fr)_320px] max-lg:grid-cols-1">
      <ThreadList
        loading={loading}
        threads={threads}
        workspaces={workspaces}
        activeId={activeId}
        query={query}
        workspaceFilter={workspaceFilter}
        statusFilter={statusFilter}
        includeArchived={includeArchived}
        scratchReady={scratchReady}
        models={models}
        onQuery={setQuery}
        onWorkspaceFilter={setWorkspaceFilter}
        onStatusFilter={(value) => setStatusFilter(value as ThreadStatusFilter)}
        onIncludeArchived={setIncludeArchived}
        onSearch={() => void loadThreads()}
        onSelect={setActiveId}
        onCreate={createThread}
        onTogglePin={togglePin}
        onArchive={archive}
        onResume={resume}
        onFork={fork}
      />
      {activeThread?.kind === "chat" ? (
        <ChatWorkspace key={activeThread.id} actions={actions} status={status} thread={activeThread} workspaces={workspaces} onStatusChange={onStatusChange} onThreadChange={loadThreads} onThreadUpdated={updateThreadInList} />
      ) : activeThread ? (
        <ThreadWorkspace
          key={activeThread.id}
          actions={actions}
          status={status}
          thread={activeThread}
          workspaces={workspaces}
          draftPrompt={composerDrafts[activeThread.id] || ""}
          onDraftConsumed={() => setComposerDrafts((current) => {
            const next = { ...current };
            delete next[activeThread.id];
            return next;
          })}
          onStatusChange={onStatusChange}
          onThreadChange={loadThreads}
          onThreadUpdated={updateThreadInList}
        />
      ) : (
        <ComposerEmptyState workspaces={workspaces} onCreate={createThread} />
      )}
      <div className="max-xl:col-span-2 max-lg:col-span-1">
        <ThreadInspector
          actions={actions}
          approvals={approvals.filter((approval) => !activeThread || approval.threadId === activeThread.id)}
          status={status}
          thread={activeThread}
          workspaces={workspaces}
          onApprovalsChange={() => { void loadApprovals(); onStatusChange(); }}
          onDraft={(threadId, prompt) => {
            setComposerDrafts((current) => ({ ...current, [threadId]: prompt }));
            setActiveId(threadId);
          }}
        />
      </div>
    </div>
    </>
  );
}
