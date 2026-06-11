import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent, RefObject } from "react";
import type { AppActions } from "../../../app/App";
import type { CodexEvent, CodexModel, CodexStatus, CodexThread, CodexTurn, CodexWorkspace } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState, Notice, Pill } from "../../../components/ui";
import { codexAppServerStateLabel, codexThreadStatusLabel, formatDate } from "../../../domain/labels";
import { buildChatTranscript, mergeCodexEvent } from "./transcript";
import type { ChatEntry } from "./transcript";

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

export function ChatWorkspace({
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
  const [models, setModels] = useState<CodexModel[]>([]);
  const [model, setModel] = useState(thread.model || "");
  const [prompt, setPrompt] = useState("");
  const [sending, setSending] = useState(false);
  const [savingTitle, setSavingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState(thread.title || "");
  const [streamState, setStreamState] = useState<"connecting" | "live" | "reconnecting">("connecting");
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const promptRef = useRef<HTMLTextAreaElement | null>(null);

  const workspace = workspaces.find((item) => item.id === thread.workspaceId);
  const activeTurn = turns.find((turn) => turn.status === "running" || turn.status === "waiting_approval");
  const busy = thread.status === "running" || thread.status === "needs_approval" || thread.status === "queued" || Boolean(activeTurn);
  const entries = useMemo(() => buildChatTranscript(events, turns), [events, turns]);
  const lastEntrySignal = entries.length ? `${entries[entries.length - 1].key}:${entries[entries.length - 1].sequence}:${entryTextLength(entries[entries.length - 1])}` : "empty";

  const loadEvents = useCallback(async () => {
    const response = await actions.api<{ items?: CodexEvent[] }>(`/api/codex/threads/${thread.id}/events?limit=500`);
    setEvents(response.items || []);
  }, [actions, thread.id]);

  const loadThread = useCallback(async () => {
    const response = await actions.api<{ turns?: CodexTurn[] }>(`/api/codex/threads/${thread.id}`);
    setTurns(response.turns || []);
  }, [actions, thread.id]);

  const loadModels = useCallback(async () => {
    try {
      const response = await actions.api<{ items?: CodexModel[] }>("/api/codex/models");
      setModels(response.items || []);
    } catch {
      setModels([]);
    }
  }, [actions]);

  useEffect(() => {
    setTitleDraft(thread.title || "");
    setModel(thread.model || "");
  }, [thread.id, thread.model, thread.title]);

  useEffect(() => {
    if (!models.length) return;
    setModel((current) => {
      const trimmed = current.trim();
      if (trimmed && models.some((item) => item.id === trimmed)) return trimmed;
      return models.find((item) => item.isDefault)?.id || models[0]?.id || trimmed;
    });
  }, [models]);

  useEffect(() => {
    void loadEvents().catch(() => undefined);
    void loadThread().catch(() => undefined);
    void loadModels();
  }, [loadEvents, loadThread, loadModels]);

  useEffect(() => {
    let closed = false;
    const source = new EventSource(`/api/codex/threads/${thread.id}/events?stream=1`);
    const handleEvent = (message: MessageEvent<string>) => {
      const next = parseCodexStreamEvent(message.data);
      if (!next) return;
      setEvents((current) => mergeCodexEvent(current, next));
      if (shouldRefreshThread(next)) {
        void loadThread().catch(() => undefined);
        onThreadChange();
        onStatusChange();
      }
    };
    source.onopen = () => {
      if (!closed) setStreamState("live");
    };
    source.onerror = () => {
      if (!closed) setStreamState("reconnecting");
    };
    CODEX_STREAM_EVENTS.forEach((eventType) => source.addEventListener(eventType, handleEvent as EventListener));
    return () => {
      closed = true;
      source.close();
    };
  }, [thread.id, loadThread, onThreadChange, onStatusChange]);

  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [lastEntrySignal]);

  async function startAppServer() {
    try {
      await actions.api("/api/codex/app-server/start", { method: "POST", csrf: actions.csrf });
      onStatusChange();
      await loadModels();
    } catch (error) {
      actions.setToast(friendlyError(error), "danger");
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

  async function submit(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    const text = prompt.trim();
    if (!text) return;
    if (status?.appServer?.state === "running" && !model.trim()) {
      actions.setToast("请选择一个可用模型后再发送。", "danger");
      return;
    }
    setSending(true);
    try {
      if (activeTurn) {
        await actions.api(`/api/codex/turns/${activeTurn.id}/steer`, { method: "POST", csrf: actions.csrf, body: { prompt: text } });
      } else {
        await actions.api(`/api/codex/threads/${thread.id}/turns`, {
          method: "POST",
          csrf: actions.csrf,
          body: { prompt: text, sandbox: "read-only", approvalPolicy: "on-request", model: model.trim() },
        });
      }
      setPrompt("");
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

  return (
    <section className="chat-workspace panel h-[calc(100dvh-172px)] max-h-[calc(100dvh-172px)] overflow-hidden max-lg:h-[calc(100dvh-204px)] max-lg:max-h-none">
      <div className="chat-workspace-header">
        <div className="min-w-0">
          <input
            aria-label="Chat 标题"
            className="chat-title-input"
            disabled={savingTitle}
            onBlur={() => void saveTitle()}
            onChange={(event) => setTitleDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.currentTarget.blur();
            }}
            placeholder="新对话"
            value={titleDraft}
          />
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
            <span className="truncate">{workspace?.label || workspace?.pathSummary || "scratch workspace"}</span>
            <span>只读 Chat</span>
            <span>SSE {streamStateLabel(streamState)}</span>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
          <Pill tone={threadTone(thread.status)}>{codexThreadStatusLabel(thread.status)}</Pill>
          <Pill tone={status?.appServer?.state === "running" ? "good" : status?.appServer?.state === "failed" ? "danger" : "neutral"}>app-server {codexAppServerStateLabel(status?.appServer?.state)}</Pill>
        </div>
      </div>

      {status?.appServer?.state !== "running" ? (
        <div className="border-b border-[var(--line)] px-5 py-3">
          <Notice tone={status?.appServer?.state === "failed" ? "danger" : "warn"}>
            <span className="mr-3">app-server 未处于运行状态，Chat 会降级或无法获得长会话流式体验。</span>
            <Button className="min-h-7 px-2 text-xs" onClick={() => void startAppServer()}>{status?.appServer?.state === "failed" ? "重试启动" : "启动 app-server"}</Button>
          </Notice>
        </div>
      ) : null}

      <div className="chat-transcript" ref={scrollRef}>
        {entries.length ? (
          entries.map((entry) => (entry.kind === "message" ? <ChatMessage entry={entry} key={entry.key} /> : <ChatStatus entry={entry} key={entry.key} />))
        ) : (
          <div className="mx-auto flex min-h-64 max-w-2xl items-center justify-center px-4">
            <EmptyState title="开始一段 Chat" body="这里会以对话形式展示只读问答、计划和命令草案；工具和审批只作为状态行出现。" />
          </div>
        )}
      </div>

      <ChatComposer
        activeTurn={Boolean(activeTurn)}
        busy={busy}
        disabled={sending}
        model={model}
        models={models}
        onInterrupt={() => void interrupt()}
        onModel={setModel}
        onPrompt={setPrompt}
        onSubmit={(event) => void submit(event)}
        prompt={prompt}
        promptRef={promptRef}
        sending={sending}
      />
    </section>
  );
}

function ChatMessage({ entry }: { entry: Extract<ChatEntry, { kind: "message" }> }) {
  const isUser = entry.role === "user";
  return (
    <article className={`chat-entry flex ${isUser ? "justify-end" : "justify-start"}`}>
      <div className={isUser ? "chat-user-message" : "chat-assistant-message"}>
        <div className={`whitespace-pre-wrap break-words leading-relaxed ${entry.streaming ? "chat-streaming-text" : ""}`}>{entry.text}</div>
        <div className={`mt-2 text-xs ${isUser ? "text-right text-[var(--muted)]" : "text-[var(--muted)]"}`}>{formatDate(entry.createdAt)}</div>
      </div>
    </article>
  );
}

function ChatStatus({ entry }: { entry: Extract<ChatEntry, { kind: "status" }> }) {
  const toneClass =
    entry.tone === "danger"
      ? "text-[var(--danger)]"
      : entry.tone === "warn"
        ? "text-[var(--warn)]"
        : entry.tone === "good"
          ? "text-[var(--good)]"
          : "text-[var(--muted)]";
  return (
    <div className="chat-status-row chat-entry">
      <div className={`flex shrink-0 items-center gap-2 text-sm font-medium ${toneClass}`}>
        {entry.active ? <span className="chat-thinking-dot" /> : null}
        <span>{entry.label}</span>
      </div>
      <div className="h-px min-w-8 flex-1 bg-[var(--line)]" />
      {entry.detail ? <div className="max-w-[52ch] truncate text-xs text-[var(--muted)]">{entry.detail}</div> : null}
    </div>
  );
}

function ChatComposer({
  activeTurn,
  busy,
  disabled,
  model,
  models,
  onInterrupt,
  onModel,
  onPrompt,
  onSubmit,
  prompt,
  promptRef,
  sending,
}: {
  activeTurn: boolean;
  busy: boolean;
  disabled: boolean;
  model: string;
  models: CodexModel[];
  onInterrupt: () => void;
  onModel: (value: string) => void;
  onPrompt: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  prompt: string;
  promptRef: RefObject<HTMLTextAreaElement | null>;
  sending: boolean;
}) {
  return (
    <form className="chat-composer" onSubmit={onSubmit}>
      <textarea
        className="chat-composer-input"
        disabled={disabled}
        onChange={(event) => onPrompt(event.target.value)}
        onKeyDown={(event) => {
          if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
            event.preventDefault();
            event.currentTarget.form?.requestSubmit();
          }
        }}
        placeholder={activeTurn ? "补充要求，Codex 会继续这一轮..." : "输入消息，适合解释、计划、命令草案..."}
        ref={promptRef}
        rows={3}
        value={prompt}
      />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
          <span className="rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1">read-only</span>
          <span className="rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-1">on-request</span>
        </div>
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
          {models.length ? (
            <select aria-label="模型" className="chat-model-select" onChange={(event) => onModel(event.target.value)} value={model}>
              {models.map((item) => (
                <option key={item.id} value={item.id}>{item.displayName || item.id}</option>
              ))}
            </select>
          ) : model ? (
            <input aria-label="模型" className="chat-model-select" onChange={(event) => onModel(event.target.value)} value={model} />
          ) : null}
          {activeTurn ? <Button className="rounded-full" onClick={onInterrupt} tone="danger">停止</Button> : null}
          <Button className="rounded-full px-4" disabled={disabled || !prompt.trim()} tone="primary" type="submit">
            {sending ? "发送中" : activeTurn ? "补充" : "发送"}
          </Button>
        </div>
      </div>
    </form>
  );
}

function parseCodexStreamEvent(data: string): CodexEvent | null {
  try {
    const parsed = JSON.parse(data) as CodexEvent & { type?: string; scopeId?: string };
    if (parsed.eventType) return parsed;
    const payload = parsed.payload || {};
    if (parsed.type) {
      return {
        id: stringValue(payload.codexEventId) || parsed.id,
        threadId: parsed.scopeId,
        turnId: stringValue(payload.turnId),
        sequence: numberValue(payload.sequence) || parsed.sequence,
        eventType: parsed.type,
        codexMethod: stringValue(payload.codexMethod),
        itemType: stringValue(payload.itemType),
        textPreview: stringValue(payload.textPreview),
        payload,
        createdAt: parsed.createdAt,
      };
    }
  } catch {
    return null;
  }
  return null;
}

function shouldRefreshThread(event: CodexEvent): boolean {
  const type = event.eventType || "";
  return type.startsWith("turn.") || type.startsWith("approval.") || type === "thread.status.changed" || type === "diagnostic.error";
}

function threadTone(status?: string) {
  if (status === "running") return "good" as const;
  if (status === "needs_approval" || status === "queued") return "warn" as const;
  if (status === "failed") return "danger" as const;
  return "neutral" as const;
}

function streamStateLabel(value: string): string {
  if (value === "live") return "live";
  if (value === "reconnecting") return "reconnecting";
  return "connecting";
}

function entryTextLength(entry: ChatEntry): number {
  return entry.kind === "message" ? entry.text.length : entry.detail?.length || 0;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown): number {
  if (typeof value === "number") return value;
  if (typeof value === "string") return Number(value) || 0;
  return 0;
}
