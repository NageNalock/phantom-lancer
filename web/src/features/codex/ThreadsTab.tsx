import { useCallback, useEffect, useMemo, useState } from "react";
import type { AppActions } from "../../app/App";
import type { CodexSettings, CodexStatus, CodexThread, CodexWorkspace } from "../../app/types";
import { friendlyError } from "../../api/client";
import { ComposerEmptyState, ThreadList } from "./ThreadSidebar";
import type { CreateConversationMode } from "./ThreadSidebar";
import { ThreadInspector } from "./ThreadInspector";
import { ChatWorkspace } from "./ChatWorkspace";
import { ThreadWorkspace } from "./ThreadWorkspace";

export function ThreadsTab({ actions, focusThreadId, status, onStatusChange }: { actions: AppActions; focusThreadId?: string; status?: CodexStatus; onStatusChange: () => void }) {
  const [threads, setThreads] = useState<CodexThread[]>([]);
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
  const [scratchWorkspaceId, setScratchWorkspaceId] = useState("");
  const [activeId, setActiveId] = useState("");
  const [query, setQuery] = useState("");
  const [workspaceFilter, setWorkspaceFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [includeArchived, setIncludeArchived] = useState(false);
  const [loading, setLoading] = useState(false);

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
      setActiveId((current) => (current && nextThreads.some((thread) => thread.id === current) ? current : nextThreads[0]?.id || ""));
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setLoading(false);
    }
  }, [actions, includeArchived, query, statusFilter, workspaceFilter]);

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

  async function createThread(workspaceId: string, mode: CreateConversationMode = "code") {
    try {
      const response = mode === "chat"
        ? await actions.api<{ thread: CodexThread }>("/api/codex/chats", { method: "POST", csrf: actions.csrf, body: { title: "" } })
        : await actions.api<{ thread: CodexThread }>("/api/codex/threads", { method: "POST", csrf: actions.csrf, body: { workspaceId } });
      await loadThreads();
      setActiveId(response.thread.id);
      onStatusChange();
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
    try {
      await actions.api(`/api/codex/threads/${thread.id}/archive`, { method: "POST", csrf: actions.csrf });
      await loadThreads();
      if (activeId === thread.id) setActiveId("");
      onStatusChange();
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
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <div className="grid min-w-0 grid-cols-[280px_minmax(0,1fr)_300px] gap-4 max-xl:grid-cols-[260px_minmax(0,1fr)] max-lg:grid-cols-1">
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
        onQuery={setQuery}
        onWorkspaceFilter={setWorkspaceFilter}
        onStatusFilter={setStatusFilter}
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
        <ThreadWorkspace key={activeThread.id} actions={actions} status={status} thread={activeThread} workspaces={workspaces} onStatusChange={onStatusChange} onThreadChange={loadThreads} onThreadUpdated={updateThreadInList} />
      ) : (
        <ComposerEmptyState workspaces={workspaces} onCreate={createThread} />
      )}
      <div className="max-xl:col-span-2 max-lg:col-span-1">
        <ThreadInspector status={status} thread={activeThread} workspaces={workspaces} />
      </div>
    </div>
  );
}
