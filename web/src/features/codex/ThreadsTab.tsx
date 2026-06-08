import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../app/App";
import type { CodexEvent, CodexModel, CodexStatus, CodexThread, CodexTurn, CodexWorkspace } from "../../app/types";
import { Button, EmptyState, Notice, Pill } from "../../components/ui";
import { friendlyError } from "../../api/client";
import { codexThreadStatusLabel } from "../../domain/labels";
import { ComposerEmptyState, ThreadList } from "./ThreadSidebar";
import { EventRow } from "./ThreadEventRow";
import { ThreadInspector } from "./ThreadInspector";
import { ThreadP1Panels } from "./ThreadP1Panels";

const CODEX_STREAM_EVENTS = [
  "thread.started",
  "thread.resumed",
  "thread.archived",
  "thread.status.changed",
  "turn.queued",
  "turn.started",
  "turn.completed",
  "turn.failed",
  "turn.cancelled",
  "message.user",
  "message.agent",
  "message.reasoning",
  "command.started",
  "command.completed",
  "command.owner.queued",
  "command.owner.started",
  "command.owner.output.attached",
  "command.owner.completed",
  "file_change.started",
  "file_change.completed",
  "approval.requested",
  "approval.resolved",
  "tool.started",
  "tool.completed",
  "plan.updated",
  "diff.updated",
  "review.comment.created",
  "browser.preview.opened",
  "browser.preview.comment",
  "usage.updated",
  "diagnostic.warning",
  "diagnostic.error",
];

export function ThreadsTab({ actions, status, onStatusChange }: { actions: AppActions; status?: CodexStatus; onStatusChange: () => void }) {
  const [threads, setThreads] = useState<CodexThread[]>([]);
  const [workspaces, setWorkspaces] = useState<CodexWorkspace[]>([]);
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
      setThreads(response.items || []);
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

  useEffect(() => {
    void loadThreads();
  }, [loadThreads]);
  useEffect(() => {
    void loadWorkspaces();
  }, [loadWorkspaces]);

  const activeThread = useMemo(() => threads.find((thread) => thread.id === activeId) || null, [threads, activeId]);

  async function createThread(workspaceId: string) {
    try {
      const response = await actions.api<{ thread: CodexThread }>("/api/codex/threads", { method: "POST", csrf: actions.csrf, body: { workspaceId } });
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
    <div className="grid grid-cols-[280px_minmax(0,1fr)_300px] gap-4 max-xl:grid-cols-[260px_minmax(0,1fr)] max-lg:grid-cols-1">
      <ThreadList
        loading={loading}
        threads={threads}
        workspaces={workspaces}
        activeId={activeId}
        query={query}
        workspaceFilter={workspaceFilter}
        statusFilter={statusFilter}
        includeArchived={includeArchived}
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
      {activeThread ? (
        <ThreadWorkspace key={activeThread.id} actions={actions} status={status} thread={activeThread} workspaces={workspaces} onStatusChange={onStatusChange} onThreadChange={loadThreads} />
      ) : (
        <ComposerEmptyState workspaces={workspaces} onCreate={createThread} />
      )}
      <ThreadInspector status={status} thread={activeThread} workspaces={workspaces} />
    </div>
  );
}

function ThreadWorkspace({
  actions,
  status,
  thread,
  workspaces,
  onStatusChange,
  onThreadChange,
}: {
  actions: AppActions;
  status?: CodexStatus;
  thread: CodexThread;
  workspaces: CodexWorkspace[];
  onStatusChange: () => void;
  onThreadChange: () => void;
}) {
  const [events, setEvents] = useState<CodexEvent[]>([]);
  const [turns, setTurns] = useState<CodexTurn[]>([]);
  const [prompt, setPrompt] = useState("");
  const [sandbox, setSandbox] = useState(thread.sandboxMode || "read-only");
  const [approval, setApproval] = useState(safeApprovalPolicy(thread.approvalPolicy));
  const [model, setModel] = useState(thread.model || "");
  const [models, setModels] = useState<CodexModel[]>([]);
  const [titleDraft, setTitleDraft] = useState(thread.title || "");
  const [sending, setSending] = useState(false);
  const [savingTitle, setSavingTitle] = useState(false);
  const [steering, setSteering] = useState(false);
  const [attachments, setAttachments] = useState<Array<{ id: string; filename?: string }>>([]);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const workspace = workspaces.find((item) => item.id === thread.workspaceId);
  const running = thread.status === "running" || thread.status === "needs_approval";
  const activeTurn = turns.find((turn) => turn.status === "running" || turn.status === "waiting_approval");
  const workspaceWriteAllowed = workspace?.trustState === "trusted";

  const loadEvents = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexEvent[] }>(`/api/codex/threads/${thread.id}/events?limit=500`);
      setEvents(response.items || []);
    } catch {
      // keep quiet, polling will retry
    }
  }, [actions, thread.id]);

  const loadThread = useCallback(async () => {
    try {
      const response = await actions.api<{ turns?: CodexTurn[] }>(`/api/codex/threads/${thread.id}`);
      setTurns(response.turns || []);
    } catch {
      // ignore
    }
  }, [actions, thread.id]);

  const loadModels = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexModel[] }>("/api/codex/models");
      setModels(response.items || []);
    } catch {
      // model catalog only available while app-server runs; fall back to text input
    }
  }, [actions]);

  useEffect(() => {
    void loadEvents();
    void loadThread();
    void loadModels();
  }, [loadEvents, loadThread, loadModels]);
  useEffect(() => {
    setTitleDraft(thread.title || "");
  }, [thread.title]);
  useEffect(() => {
    if (!workspaceWriteAllowed && sandbox === "workspace-write") setSandbox("read-only");
  }, [sandbox, workspaceWriteAllowed]);

  // Live updates reuse the shared Event API; the Codex history endpoint remains
  // the source for structured transcript rows.
  useEffect(() => {
    const params = new URLSearchParams({ scope: "codex.thread", id: thread.id });
    const url = `/api/events/stream?${params.toString()}`;
    const source = new EventSource(url);
    const refresh = () => {
      void loadEvents();
      void loadThread();
    };
    source.onmessage = refresh;
    CODEX_STREAM_EVENTS.forEach((eventType) => source.addEventListener(eventType, refresh));
    source.onerror = () => {
      source.close();
    };
    return () => source.close();
  }, [thread.id, loadEvents, loadThread]);

  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [events.length]);

  async function uploadAttachment(file: File) {
    const form = new FormData();
    form.append("threadId", thread.id);
    form.append("file", file);
    try {
      const response = await actions.api<{ attachment: { id: string; filename?: string } }>("/api/codex/attachments", { method: "POST", csrf: actions.csrf, body: form });
      setAttachments((current) => [...current, { id: response.attachment.id, filename: response.attachment.filename }]);
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function removeAttachment(id: string) {
    setAttachments((current) => current.filter((item) => item.id !== id));
    try {
      await actions.api(`/api/codex/attachments/${id}`, { method: "DELETE", csrf: actions.csrf });
    } catch {
      // best effort; the attachment will be GC'd by TTL even if this fails
    }
  }

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!prompt.trim()) return;
    setSending(true);
    try {
      await actions.api(`/api/codex/threads/${thread.id}/turns`, {
        method: "POST",
        csrf: actions.csrf,
        body: { prompt: prompt.trim(), sandbox, approvalPolicy: approval, model: model.trim(), attachmentIds: attachments.map((item) => item.id) },
      });
      setPrompt("");
      setAttachments([]);
      await loadEvents();
      await loadThread();
      onThreadChange();
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSending(false);
    }
  }

  async function queueTurn() {
    if (!prompt.trim()) return;
    setSending(true);
    try {
      await actions.api(`/api/codex/threads/${thread.id}/queue`, {
        method: "POST",
        csrf: actions.csrf,
        body: { prompt: prompt.trim(), sandbox, approvalPolicy: approval, model: model.trim(), attachmentIds: attachments.map((item) => item.id) },
      });
      setPrompt("");
      setAttachments([]);
      await loadEvents();
      await loadThread();
      onThreadChange();
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSending(false);
    }
  }

  async function interrupt() {
    if (!activeTurn) return;
    try {
      await actions.api(`/api/codex/turns/${activeTurn.id}/interrupt`, { method: "POST", csrf: actions.csrf });
      await loadThread();
      onThreadChange();
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  async function steer() {
    if (!activeTurn || !prompt.trim()) return;
    setSteering(true);
    try {
      await actions.api(`/api/codex/turns/${activeTurn.id}/steer`, { method: "POST", csrf: actions.csrf, body: { prompt: prompt.trim() } });
      setPrompt("");
      await loadEvents();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSteering(false);
    }
  }

  async function saveTitle() {
    const nextTitle = titleDraft.trim();
    if (nextTitle === (thread.title || "")) return;
    setSavingTitle(true);
    try {
      await actions.api(`/api/codex/threads/${thread.id}`, { method: "PATCH", csrf: actions.csrf, body: { title: nextTitle } });
      onThreadChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    } finally {
      setSavingTitle(false);
    }
  }

  async function startAppServer() {
    try {
      await actions.api("/api/codex/app-server/start", { method: "POST", csrf: actions.csrf });
      onStatusChange();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
    }
  }

  return (
    <section className="panel flex min-h-0 flex-col">
      <div className="panel-header">
        <div className="min-w-0">
          <input
            aria-label="会话标题"
            className="input h-8 w-full max-w-md text-sm font-semibold"
            disabled={savingTitle}
            onBlur={() => void saveTitle()}
            onChange={(event) => setTitleDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.currentTarget.blur();
              }
            }}
            placeholder="新对话"
            value={titleDraft}
          />
          <p className="muted mt-1 mb-0 truncate text-xs">{workspace?.label || workspace?.pathSummary}</p>
        </div>
        <Pill tone={threadTone(thread.status)}>{codexThreadStatusLabel(thread.status)}</Pill>
      </div>
      <div className="panel-body flex min-h-0 flex-1 flex-col gap-3">
        <AppServerStrip status={status} onStart={startAppServer} />
        <div className="grid max-h-[52vh] gap-2 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" ref={scrollRef}>
          {events.length ? events.map((event) => <EventRow key={event.id || event.sequence} event={event} />) : <EmptyState body="发送第一条 prompt 后会在此显示消息、命令、diff 和审批。" title="空会话" />}
        </div>
        <ThreadP1Panels actions={actions} thread={thread} onRefresh={loadEvents} />

        {thread.lastError ? <Notice tone="danger">{thread.lastError}</Notice> : null}

        <form className="grid gap-2" onSubmit={send}>
          <textarea
            className="input min-h-20 resize-y"
            onChange={(event) => setPrompt(event.target.value)}
            placeholder="输入 prompt，Codex 将在受控沙箱内执行"
            value={prompt}
          />
          {attachments.length ? (
            <div className="flex flex-wrap gap-2 text-xs">
              {attachments.map((item) => (
                <span className="flex items-center gap-1.5 rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1" key={item.id}>
                  {item.filename || item.id}
                  <button aria-label="移除附件" className="text-[var(--muted-strong)] hover:text-[var(--danger)]" onClick={() => void removeAttachment(item.id)} type="button">
                    ×
                  </button>
                </span>
              ))}
            </div>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            <select className="select" onChange={(event) => setSandbox(event.target.value)} value={sandbox}>
              <option value="read-only">只读咨询</option>
              <option disabled={!workspaceWriteAllowed} value="workspace-write">
                工作区写入
              </option>
            </select>
            <select className="select" onChange={(event) => setApproval(event.target.value)} value={approval}>
              <option value="on-request">on-request</option>
            </select>
            {models.length ? (
              <select className="select" onChange={(event) => setModel(event.target.value)} value={model}>
                <option value="">默认模型</option>
                {models.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.displayName || item.id}
                    {item.isDefault ? "（默认）" : ""}
                  </option>
                ))}
              </select>
            ) : (
              <input className="input w-40" onChange={(event) => setModel(event.target.value)} placeholder="模型（运行时探测）" value={model} />
            )}
            <label className="button cursor-pointer">
              附件
              <input
                accept="image/png,image/jpeg,image/webp,image/gif"
                className="hidden"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (file) void uploadAttachment(file);
                  event.target.value = "";
                }}
                type="file"
              />
            </label>
            <div className="ml-auto flex gap-2">
              {running && activeTurn ? (
                <>
                  <Button disabled={steering || !prompt.trim()} onClick={() => void steer()}>
                    {steering ? "追加中" : "追加输入"}
                  </Button>
                  <Button tone="danger" onClick={() => void interrupt()}>
                    中断
                  </Button>
                </>
              ) : null}
              {running ? (
                <Button disabled={sending || !prompt.trim()} onClick={() => void queueTurn()}>
                  排队
                </Button>
              ) : null}
              <Button disabled={sending || running || !prompt.trim()} tone="primary" type="submit">
                {sending ? "发送中" : "发送"}
              </Button>
            </div>
          </div>
        </form>
      </div>
    </section>
  );
}

function AppServerStrip({ status, onStart }: { status?: CodexStatus; onStart: () => Promise<void> }) {
  const state = status?.appServer?.state || "unknown";
  const canStart = state === "stopped" || state === "failed" || state === "degraded" || state === "unknown";
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-xs text-[var(--muted)]">
      <span className={`h-2 w-2 rounded-full ${state === "running" ? "bg-[var(--success)]" : state === "failed" || state === "degraded" ? "bg-[var(--warning)]" : "bg-[var(--muted)]"}`} />
      <span>app-server {state}</span>
      {status?.appServer?.pid ? <span className="mono">pid {status.appServer.pid}</span> : null}
      {status?.appServer?.lastError ? <span className="truncate text-[var(--warning)]">{status.appServer.lastError}</span> : null}
      {canStart ? (
        <Button onClick={() => void onStart()}>{state === "failed" ? "重试启动" : "启动 app-server"}</Button>
      ) : null}
    </div>
  );
}

function safeApprovalPolicy(value?: string): string {
  return value === "on-request" ? value : "on-request";
}

function threadTone(status?: string) {
  if (status === "running") return "good" as const;
  if (status === "needs_approval" || status === "queued") return "warn" as const;
  if (status === "failed") return "danger" as const;
  return "neutral" as const;
}
