import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexCapabilitySummary, CodexEvent, CodexModel, CodexStatus, CodexThread, CodexTurn, CodexWorkspace } from "../../../app/types";
import { Notice, Pill } from "../../../components/ui";
import { friendlyError } from "../../../api/client";
import { codexThreadStatusLabel } from "../../../domain/labels";
import { ThreadP1Panels } from "../ThreadP1Panels";
import { AppServerStrip } from "./AppServerStrip";
import { EventStream } from "./EventStream";
import { Composer } from "./Composer";
import type { ComposerAttachment } from "./Composer";

// Thread stream event names mirror the stable Phantom Lancer event vocabulary;
// the SSE channel only triggers a refresh, structured rows come from history.
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
  "command.owner.output",
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

export function ThreadWorkspace({
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
  const [skills, setSkills] = useState<string[]>([]);
  const [titleDraft, setTitleDraft] = useState(thread.title || "");
  const [sending, setSending] = useState(false);
  const [savingTitle, setSavingTitle] = useState(false);
  const [steering, setSteering] = useState(false);
  const [attachments, setAttachments] = useState<ComposerAttachment[]>([]);
  const promptRef = useRef<HTMLTextAreaElement | null>(null);

  const workspace = workspaces.find((item) => item.id === thread.workspaceId);
  const busy = thread.status === "running" || thread.status === "needs_approval" || thread.status === "queued";
  const interactive = thread.status === "running" || thread.status === "needs_approval";
  const activeTurn = turns.find((turn) => turn.status === "running" || turn.status === "waiting_approval");
  // Chat threads are fixed read-only research/planning; never offer workspace-write
  // even on a trusted workspace, matching the server-side enforcement.
  const isChat = thread.kind === "chat";
  const workspaceWriteAllowed = !isChat && workspace?.trustState === "trusted";

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

  const loadSkills = useCallback(async () => {
    try {
      const response = await actions.api<{ capability?: CodexCapabilitySummary }>("/api/codex/capabilities/skills");
      const names = (response.capability?.items || [])
        .map((item) => (typeof item.name === "string" ? item.name : ""))
        .filter((name): name is string => name.length > 0);
      setSkills(names);
    } catch {
      // skills are best-effort; composer still accepts manual $skill input
    }
  }, [actions]);

  function insertSkill(name: string) {
    if (!name) return;
    const token = `$${name} `;
    const field = promptRef.current;
    if (!field) {
      setPrompt((current) => `${current}${token}`);
      return;
    }
    const start = field.selectionStart ?? prompt.length;
    const end = field.selectionEnd ?? prompt.length;
    const next = prompt.slice(0, start) + token + prompt.slice(end);
    setPrompt(next);
    requestAnimationFrame(() => {
      field.focus();
      const caret = start + token.length;
      field.setSelectionRange(caret, caret);
    });
  }

  useEffect(() => {
    void loadEvents();
    void loadThread();
    void loadModels();
    void loadSkills();
  }, [loadEvents, loadThread, loadModels, loadSkills]);
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

  async function uploadAttachment(file: File) {
    const form = new FormData();
    form.append("threadId", thread.id);
    form.append("file", file);
    try {
      const response = await actions.api<{ attachment: ComposerAttachment }>("/api/codex/attachments", { method: "POST", csrf: actions.csrf, body: form });
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

  async function submitTurn(path: string) {
    if (!prompt.trim()) return;
    setSending(true);
    try {
      await actions.api(`/api/codex/threads/${thread.id}/${path}`, {
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

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitTurn("turns");
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
        <EventStream events={events} />
        <ThreadP1Panels actions={actions} thread={thread} onRefresh={loadEvents} />

        {thread.lastError ? <Notice tone="danger">{thread.lastError}</Notice> : null}

        <Composer
          prompt={prompt}
          onPrompt={setPrompt}
          promptRef={promptRef}
          sandbox={sandbox}
          onSandbox={setSandbox}
          approval={approval}
          onApproval={setApproval}
          model={model}
          onModel={setModel}
          models={models}
          skills={skills}
          onInsertSkill={insertSkill}
          workspaceWriteAllowed={Boolean(workspaceWriteAllowed)}
          sandboxLocked={isChat}
          attachments={attachments}
          onUpload={(file) => void uploadAttachment(file)}
          onRemoveAttachment={(id) => void removeAttachment(id)}
          busy={busy}
          interactive={interactive}
          hasActiveTurn={Boolean(activeTurn)}
          sending={sending}
          steering={steering}
          onSend={(event) => void send(event)}
          onQueue={() => void submitTurn("queue")}
          onInterrupt={() => void interrupt()}
          onSteer={() => void steer()}
        />
      </div>
    </section>
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
