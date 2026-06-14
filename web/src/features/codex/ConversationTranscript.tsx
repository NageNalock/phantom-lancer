import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { EmptyState } from "../../components/ui";
import { formatDate } from "../../domain/labels";
import type { ChatEntry } from "./ChatWorkspace/transcript";

const RichMessage = lazy(() => import("./MessageFormat").then((module) => ({ default: module.RichMessage })));

export function ConversationTranscript({
  className = "",
  emptyBody,
  emptyTitle,
  entries,
}: {
  className?: string;
  emptyBody: string;
  emptyTitle: string;
  entries: ChatEntry[];
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lastEntrySignal = entries.length ? `${entries[entries.length - 1].key}:${entries[entries.length - 1].sequence}:${entryTextLength(entries[entries.length - 1])}` : "empty";

  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [lastEntrySignal]);

  return (
    <div className={`chat-transcript ${className}`} ref={scrollRef}>
      {entries.length ? (
        entries.map((entry) => (
          entry.kind === "message"
            ? <ConversationMessage entry={entry} key={entry.key} />
            : entry.kind === "reasoning"
              ? <ConversationReasoning entry={entry} key={entry.key} />
            : entry.kind === "artifact"
              ? <ConversationArtifact entry={entry} key={entry.key} />
              : <ConversationStatus entry={entry} key={entry.key} />
        ))
      ) : (
        <div className="mx-auto flex min-h-64 max-w-2xl items-center justify-center px-4">
          <EmptyState body={emptyBody} title={emptyTitle} />
        </div>
      )}
    </div>
  );
}

function ConversationArtifact({ entry }: { entry: Extract<ChatEntry, { kind: "artifact" }> }) {
  return (
    <article className="chat-entry chat-artifact-entry">
      <div className="chat-assistant-rail" aria-hidden="true">C</div>
      <div className="chat-artifact-card">
        <div className="chat-artifact-meta">
          <span>图片结果</span>
          <span>{formatDate(entry.createdAt)}</span>
        </div>
        <div className="chat-artifact-grid">
          {entry.artifacts.map((artifact) => (
            <a className="chat-image-artifact" href={artifact.src} key={artifact.id} rel="noreferrer" target="_blank">
              <img alt={artifact.label} decoding="async" height={480} loading="lazy" src={artifact.src} width={720} />
              <span>{artifact.label}</span>
            </a>
          ))}
        </div>
      </div>
    </article>
  );
}

function ConversationReasoning({ entry }: { entry: Extract<ChatEntry, { kind: "reasoning" }> }) {
  return (
    <article className="chat-entry chat-reasoning-entry">
      <div className="chat-assistant-rail" aria-hidden="true">C</div>
      <ReasoningDisclosure entry={entry} />
    </article>
  );
}

function ReasoningDisclosure({ entry }: { entry: Extract<ChatEntry, { kind: "reasoning" }> }) {
  const [open, setOpen] = useState(Boolean(entry.active));
  const wasActiveRef = useRef(Boolean(entry.active));

  useEffect(() => {
    if (entry.active) {
      setOpen(true);
      wasActiveRef.current = true;
      return;
    }
    if (wasActiveRef.current) {
      setOpen(false);
      wasActiveRef.current = false;
    }
  }, [entry.active]);

  return (
    <details className={`chat-reasoning-card ${entry.active ? "chat-reasoning-card-active" : ""}`} onToggle={(event) => setOpen(event.currentTarget.open)} open={open}>
      <summary>
        <span className="chat-reasoning-summary-main">
          {entry.active ? <span className="chat-thinking-dot" /> : null}
          <span>{entry.active ? "正在思考" : "思考内容"}</span>
        </span>
        <span className="chat-reasoning-summary-meta">{entry.active ? "实时" : entry.duration ? `已处理 ${entry.duration}` : "已完成"}</span>
      </summary>
      <div className="chat-reasoning-body">
        {entry.text ? entry.text : "正在整理推理内容。"}
      </div>
    </details>
  );
}

function ConversationMessage({ entry }: { entry: Extract<ChatEntry, { kind: "message" }> }) {
  const isUser = entry.role === "user";
  return (
    <article className={`chat-entry flex ${isUser ? "justify-end" : "justify-start"}`}>
      {isUser ? null : <div className="chat-assistant-rail" aria-hidden="true">C</div>}
      <div className={isUser ? "chat-user-message" : "chat-assistant-message"}>
        {isUser ? (
          <div className="whitespace-pre-wrap break-words leading-relaxed">{entry.text}</div>
        ) : (
          <Suspense fallback={<PlainMessageFallback streaming={entry.streaming} text={entry.text} />}>
            <RichMessage streaming={entry.streaming} text={entry.text} />
          </Suspense>
        )}
        <div className={`mt-2 text-xs ${isUser ? "text-right text-[var(--muted)]" : "text-[var(--muted)]"}`}>{formatDate(entry.createdAt)}</div>
      </div>
    </article>
  );
}

function PlainMessageFallback({ streaming, text }: { streaming?: boolean; text: string }) {
  return <div className={`message-rich whitespace-pre-wrap ${streaming ? "chat-streaming-text" : ""}`}>{text}</div>;
}

function ConversationStatus({ entry }: { entry: Extract<ChatEntry, { kind: "status" }> }) {
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
      {entry.detail ? <div className="max-w-[52ch] truncate text-xs text-[var(--muted)]">{entry.detail}</div> : null}
    </div>
  );
}

function entryTextLength(entry: ChatEntry): number {
  if (entry.kind === "message") return entry.text.length;
  if (entry.kind === "reasoning") return entry.text.length;
  if (entry.kind === "artifact") return entry.artifacts.length;
  return entry.detail?.length || 0;
}
